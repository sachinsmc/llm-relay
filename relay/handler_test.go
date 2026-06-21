package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_StreamsAndStatuses(t *testing.T) {
	up, _ := fakeUpstream(t, http.StatusOK, "data: ok\n\n")
	s := mustService(t, 1, Provider{Name: "test", BaseURL: up.URL, APIKey: "k", Model: "m"})
	h := s.Handler()

	// GET is rejected.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}

	// POST streams the upstream bytes with SSE headers.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(userTurn))
	req.Header.Set("X-User-Id", "u1")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if body, _ := io.ReadAll(rec.Body); string(body) != "data: ok\n\n" {
		t.Errorf("body = %q, want streamed bytes", body)
	}

	// Second turn for the same user exceeds the cap of 1 -> 429 JSON.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(userTurn))
	req.Header.Set("X-User-Id", "u1")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("over-quota status = %d, want 429", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("error Content-Type = %q, want application/json", ct)
	}
}

func TestHandler_WithUserFunc(t *testing.T) {
	up, _ := fakeUpstream(t, http.StatusOK, "data: ok\n\n")
	s := mustService(t, 1, Provider{Name: "test", BaseURL: up.URL, APIKey: "k", Model: "m"})
	h := s.Handler().WithUserFunc(func(*http.Request) string { return "fixed" })

	// Two requests with no X-User-Id still share a key via the custom func, so
	// the second one is rate limited.
	for i, wantCode := range []int{http.StatusOK, http.StatusTooManyRequests} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(userTurn)))
		if rec.Code != wantCode {
			t.Errorf("request %d: status = %d, want %d", i, rec.Code, wantCode)
		}
	}
}
