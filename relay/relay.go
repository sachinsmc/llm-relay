// Package relay is a lightweight, OpenAI-compatible LLM gateway. It accepts a
// standard chat-completions request, enforces a per-user daily quota, injects a
// server-side API key and model, and streams the upstream's Server-Sent Events
// straight back to the caller. When the primary provider is rate-limited or
// down it transparently fails over to the next configured provider.
//
// It is provider-agnostic: every upstream speaks the OpenAI wire format, so you
// can point it at OpenAI, OpenRouter, Groq, Cerebras, Together, Fireworks, or a
// local vLLM/Ollama by URL. Tool execution stays on the client, which re-POSTs
// the tool result to continue a turn.
package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Sentinel errors returned by StartStream. They are returned before any bytes
// are streamed, so a transport can still choose an HTTP status code.
var (
	// ErrQuotaExceeded means the user hit the configured daily cap.
	ErrQuotaExceeded = errors.New("daily quota exceeded")
	// ErrBadRequest means the relayed payload was malformed.
	ErrBadRequest = errors.New("invalid request")
	// ErrUpstream means every provider returned a non-200 response or was
	// unreachable.
	ErrUpstream = errors.New("upstream error")
	// ErrNoProviders means no provider with a non-empty key was configured.
	ErrNoProviders = errors.New("no providers configured")
)

// Config wires a Service. Providers is primary-first; the rest are tried in
// order on failover.
type Config struct {
	// Providers is the ordered failover chain (primary first). Entries with an
	// empty API key, or an unknown name and no BaseURL, are dropped.
	Providers []Provider
	// DailyCap bounds fresh user turns per user per UTC day. 0 means unlimited.
	DailyCap int
	// Limiter stores per-user usage. Defaults to an in-memory limiter.
	Limiter Limiter
	// Client is the HTTP client used for upstream calls. Defaults to a client
	// with a 120s timeout.
	Client *http.Client
	// Logger receives structured warn/info logs. Defaults to a discard logger.
	Logger *slog.Logger
	// Attribution sets optional HTTP-Referer / X-Title headers for providers
	// that surface them on a dashboard (OpenRouter). Both are optional.
	Attribution Attribution
}

// Attribution carries optional dashboard headers some providers display.
type Attribution struct {
	Referer string
	Title   string
}

// Service is a configured relay. It is safe for concurrent use.
type Service struct {
	upstreams   []upstream
	dailyCap    int
	limiter     Limiter
	client      *http.Client
	logger      *slog.Logger
	attribution Attribution
}

// New validates the config and builds the failover chain. It returns
// ErrNoProviders when no usable provider remains after dropping entries with an
// empty key or an unresolvable endpoint.
func New(cfg Config) (*Service, error) {
	upstreams := make([]upstream, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		if p.APIKey == "" {
			continue
		}
		up, err := resolve(p)
		if err != nil {
			return nil, err
		}
		upstreams = append(upstreams, up)
	}
	if len(upstreams) == 0 {
		return nil, ErrNoProviders
	}

	s := &Service{
		upstreams:   upstreams,
		dailyCap:    cfg.DailyCap,
		limiter:     cfg.Limiter,
		client:      cfg.Client,
		logger:      cfg.Logger,
		attribution: cfg.Attribution,
	}
	if s.limiter == nil {
		s.limiter = NewMemoryLimiter()
	}
	if s.client == nil {
		s.client = &http.Client{Timeout: 120 * time.Second}
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	return s, nil
}

// relayRequest is the minimal shape parsed from the client payload. messages
// and tools are forwarded verbatim (already OpenAI format); only the last
// message's role is inspected to decide whether this is a billable user turn.
type relayRequest struct {
	Messages []json.RawMessage `json:"messages"`
	Tools    []json.RawMessage `json:"tools"`
}

// StartStream validates and quota-checks reqBody, opens the upstream stream, and
// returns it for the caller to relay. The caller must Close the returned reader.
// Quota and upstream errors are returned before any bytes are produced.
func (s *Service) StartStream(ctx context.Context, user string, reqBody []byte) (io.ReadCloser, error) {
	var req relayRequest
	if err := json.Unmarshal(reqBody, &req); err != nil || len(req.Messages) == 0 {
		return nil, ErrBadRequest
	}

	// Count only fresh user turns. A continuation whose last message is a tool
	// result belongs to a turn already counted.
	if s.isUserTurn(req.Messages) {
		today := time.Now().UTC().Format("2006-01-02")
		ok, err := s.limiter.Allow(ctx, user, today, s.dailyCap)
		if err != nil {
			return nil, fmt.Errorf("checking quota: %w", err)
		}
		if !ok {
			return nil, ErrQuotaExceeded
		}
	}

	// Try the primary, then fail over to each fallback when the upstream is
	// exhausted (rate-limited) or down. Failover happens before any bytes reach
	// the client, so it never sees a partial stream then a switch.
	var lastErr error
	for i, up := range s.upstreams {
		body, err := s.buildUpstreamBody(req, up)
		if err != nil {
			return nil, ErrBadRequest
		}
		rc, retryable, err := s.callUpstream(ctx, up, body)
		if err == nil {
			if i > 0 {
				s.logger.Info("served by fallback provider",
					slog.String("provider", up.name), slog.String("model", up.model),
					slog.Int("attempt", i+1))
			}
			return rc, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return nil, lastErr
}

// callUpstream POSTs to one provider. On success it returns the live stream. On
// failure it reports whether the failure is retryable (worth failing over):
// network errors, timeouts, 429, and 5xx are; a 4xx like 400/401/403 is a
// payload/config problem the next provider can't fix. Every failure is logged
// with provider/model/status/latency.
func (s *Service) callUpstream(ctx context.Context, up upstream, body []byte) (io.ReadCloser, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, up.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("building upstream request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+up.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if up.attribution {
		if s.attribution.Referer != "" {
			httpReq.Header.Set("HTTP-Referer", s.attribution.Referer)
		}
		if s.attribution.Title != "" {
			httpReq.Header.Set("X-Title", s.attribution.Title)
		}
	}

	start := time.Now()
	resp, err := s.client.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		// Network/timeout: the provider is unreachable, fail over.
		s.logger.Warn("upstream unreachable",
			slog.String("provider", up.name), slog.String("model", up.model),
			slog.Duration("latency", latency), slog.Bool("retryable", true), slog.Any("error", err))
		return nil, true, fmt.Errorf("%w: %s: %v", ErrUpstream, up.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode >= http.StatusInternalServerError
		s.logger.Warn("upstream error",
			slog.String("provider", up.name), slog.String("model", up.model),
			slog.Int("status", resp.StatusCode), slog.Duration("latency", latency),
			slog.Bool("retryable", retryable), slog.String("body", string(snippet)))
		return nil, retryable, fmt.Errorf("%w: %s: status %d: %s", ErrUpstream, up.name, resp.StatusCode, string(snippet))
	}
	return resp.Body, false, nil
}

// isUserTurn reports whether the final message is a user message (a new turn)
// rather than a tool-result continuation of the current turn.
func (s *Service) isUserTurn(messages []json.RawMessage) bool {
	var last struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(messages[len(messages)-1], &last); err != nil {
		// Be conservative: count it so a parse miss never relays for free.
		return true
	}
	return last.Role == "user"
}

// buildUpstreamBody assembles the upstream payload: the server-side model and
// stream flag, the verbatim messages/tools, and the optional no-train hint.
func (s *Service) buildUpstreamBody(req relayRequest, up upstream) ([]byte, error) {
	body := map[string]any{
		"model":    up.model,
		"stream":   true,
		"messages": req.Messages,
	}
	if up.noTrain {
		body["provider"] = map[string]any{"data_collection": "deny"}
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	return json.Marshal(body)
}
