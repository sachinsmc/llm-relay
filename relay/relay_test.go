package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fakeUpstream returns an httptest server that responds with status/body and
// records how many times it was called.
func fakeUpstream(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("Authorization"); got == "" {
			t.Errorf("upstream missing Authorization header")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func mustService(t *testing.T, cap int, providers ...Provider) *Service {
	t.Helper()
	s, err := New(Config{Providers: providers, DailyCap: cap})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

const userTurn = `{"messages":[{"role":"user","content":"hi"}]}`

func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return string(b)
}

func TestStartStream_Success(t *testing.T) {
	up, hits := fakeUpstream(t, http.StatusOK, "data: hello\n\n")
	s := mustService(t, 0, Provider{Name: "test", BaseURL: up.URL, APIKey: "k", Model: "m"})

	rc, err := s.StartStream(context.Background(), "u1", []byte(userTurn))
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if got := readAll(t, rc); got != "data: hello\n\n" {
		t.Errorf("body = %q, want streamed upstream bytes", got)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", hits.Load())
	}
}

func TestStartStream_FailoverOnRetryable(t *testing.T) {
	primary, ph := fakeUpstream(t, http.StatusTooManyRequests, "slow down")
	fallback, fh := fakeUpstream(t, http.StatusOK, "data: ok\n\n")
	s := mustService(t, 0,
		Provider{Name: "primary", BaseURL: primary.URL, APIKey: "k", Model: "m"},
		Provider{Name: "fallback", BaseURL: fallback.URL, APIKey: "k", Model: "m"},
	)

	rc, err := s.StartStream(context.Background(), "u1", []byte(userTurn))
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if got := readAll(t, rc); got != "data: ok\n\n" {
		t.Errorf("body = %q, want fallback bytes", got)
	}
	if ph.Load() != 1 || fh.Load() != 1 {
		t.Errorf("hits primary=%d fallback=%d, want 1 and 1", ph.Load(), fh.Load())
	}
}

func TestStartStream_NoFailoverOnClientError(t *testing.T) {
	primary, ph := fakeUpstream(t, http.StatusBadRequest, "bad payload")
	fallback, fh := fakeUpstream(t, http.StatusOK, "data: ok\n\n")
	s := mustService(t, 0,
		Provider{Name: "primary", BaseURL: primary.URL, APIKey: "k", Model: "m"},
		Provider{Name: "fallback", BaseURL: fallback.URL, APIKey: "k", Model: "m"},
	)

	_, err := s.StartStream(context.Background(), "u1", []byte(userTurn))
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if ph.Load() != 1 {
		t.Errorf("primary hits = %d, want 1", ph.Load())
	}
	if fh.Load() != 0 {
		t.Errorf("fallback hits = %d, want 0 (4xx must not fail over)", fh.Load())
	}
}

func TestStartStream_BadRequest(t *testing.T) {
	up, _ := fakeUpstream(t, http.StatusOK, "data: x\n\n")
	s := mustService(t, 0, Provider{Name: "test", BaseURL: up.URL, APIKey: "k", Model: "m"})

	for _, body := range []string{`not json`, `{"messages":[]}`} {
		if _, err := s.StartStream(context.Background(), "u1", []byte(body)); !errors.Is(err, ErrBadRequest) {
			t.Errorf("body %q: err = %v, want ErrBadRequest", body, err)
		}
	}
}

func TestStartStream_QuotaCountsOnlyUserTurns(t *testing.T) {
	up, _ := fakeUpstream(t, http.StatusOK, "data: ok\n\n")
	s := mustService(t, 1, Provider{Name: "test", BaseURL: up.URL, APIKey: "k", Model: "m"})
	ctx := context.Background()

	// First user turn consumes the single-turn budget.
	rc, err := s.StartStream(ctx, "u1", []byte(userTurn))
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	_ = readAll(t, rc)

	// A tool-result continuation is part of the same turn and must not count.
	const continuation = `{"messages":[{"role":"user","content":"hi"},{"role":"tool","content":"42"}]}`
	rc, err = s.StartStream(ctx, "u1", []byte(continuation))
	if err != nil {
		t.Fatalf("continuation should be allowed: %v", err)
	}
	_ = readAll(t, rc)

	// A second fresh user turn exceeds the cap.
	if _, err := s.StartStream(ctx, "u1", []byte(userTurn)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second user turn: err = %v, want ErrQuotaExceeded", err)
	}

	// A different user has their own budget.
	if _, err := s.StartStream(ctx, "u2", []byte(userTurn)); err != nil {
		t.Fatalf("other user should have own budget: %v", err)
	}
}

func TestNew_NoProviders(t *testing.T) {
	// Empty key drops the only provider.
	if _, err := New(Config{Providers: []Provider{{Name: "groq", APIKey: ""}}}); !errors.Is(err, ErrNoProviders) {
		t.Fatalf("err = %v, want ErrNoProviders", err)
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name        string
		in          Provider
		wantURL     string
		wantNoTrain bool
		wantErr     bool
	}{
		{name: "known fills url", in: Provider{Name: "groq", APIKey: "k"}, wantURL: "https://api.groq.com/openai/v1/chat/completions"},
		{name: "explicit url overrides known default", in: Provider{Name: "openai", BaseURL: "https://my-azure.openai.azure.com/v1/chat/completions", APIKey: "k"}, wantURL: "https://my-azure.openai.azure.com/v1/chat/completions"},
		{name: "openrouter honours notrain", in: Provider{Name: "openrouter", APIKey: "k", NoTrain: true}, wantURL: "https://openrouter.ai/api/v1/chat/completions", wantNoTrain: true},
		{name: "groq ignores notrain", in: Provider{Name: "groq", APIKey: "k", NoTrain: true}, wantURL: "https://api.groq.com/openai/v1/chat/completions", wantNoTrain: false},
		{name: "custom url for unknown", in: Provider{Name: "local", BaseURL: "http://localhost:11434/v1/chat/completions", APIKey: "k"}, wantURL: "http://localhost:11434/v1/chat/completions"},
		{name: "unknown without url errors", in: Provider{Name: "mystery", APIKey: "k"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.baseURL != tt.wantURL {
				t.Errorf("baseURL = %q, want %q", got.baseURL, tt.wantURL)
			}
			if got.noTrain != tt.wantNoTrain {
				t.Errorf("noTrain = %v, want %v", got.noTrain, tt.wantNoTrain)
			}
		})
	}
}
