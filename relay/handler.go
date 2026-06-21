package relay

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// Handler is a stdlib http.Handler that accepts an OpenAI-format chat-completions
// request and streams the relayed response back as Server-Sent Events. It works
// with any router (net/http, chi, gin via gin.WrapH, echo, ...).
type Handler struct {
	svc      *Service
	userFunc func(*http.Request) string
	logger   *slog.Logger
}

// Handler returns an http.Handler for this Service. By default the per-user
// quota key is the "X-User-Id" request header (falling back to "anonymous").
// Use WithUserFunc to derive it from a JWT, session, or API key instead.
func (s *Service) Handler() *Handler {
	return &Handler{svc: s, userFunc: userFromHeader, logger: s.logger}
}

// WithUserFunc overrides how the per-user quota key is extracted from a request.
func (h *Handler) WithUserFunc(f func(*http.Request) string) *Handler {
	h.userFunc = f
	return h
}

func userFromHeader(r *http.Request) string {
	if id := r.Header.Get("X-User-Id"); id != "" {
		return id
	}
	return "anonymous"
}

// ServeHTTP relays one turn. It only accepts POST. Errors are returned as JSON
// with an appropriate status before streaming begins; once the upstream stream
// opens, bytes are flushed straight through.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "could not read request body")
		return
	}

	stream, err := h.svc.StartStream(r.Context(), h.userFunc(r), body)
	if err != nil {
		switch {
		case errors.Is(err, ErrQuotaExceeded):
			writeError(w, http.StatusTooManyRequests, "rate_limited", "daily quota reached; it resets tomorrow")
		case errors.Is(err, ErrBadRequest):
			writeError(w, http.StatusBadRequest, "bad_request", "invalid request")
		case errors.Is(err, ErrNoProviders):
			writeError(w, http.StatusServiceUnavailable, "unavailable", "no providers configured")
		case errors.Is(err, ErrUpstream):
			h.logger.Error("upstream error", slog.Any("error", err))
			writeError(w, http.StatusBadGateway, "upstream_error", "the upstream provider is temporarily unavailable")
		default:
			h.logger.Error("relay failed", slog.Any("error", err))
			writeError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
		}
		return
	}
	defer func() { _ = stream.Close() }()

	// Stream the upstream SSE bytes straight through, flushing each chunk so the
	// client sees tokens as they arrive. The upstream call is tied to the
	// request context, so a client disconnect cancels it.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // don't let nginx buffer SSE
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := stream.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return // client gone
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return // EOF or upstream/context error: stream is done
		}
	}
}

// writeError emits a small JSON error envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
