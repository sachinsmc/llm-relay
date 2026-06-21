// Command llm-relay runs the relay as a standalone OpenAI-compatible HTTP
// server, configured entirely from the environment. POST chat-completions
// requests to /v1/chat/completions and read the streamed SSE response.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sachinsmc/llm-relay/relay"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe /healthz on the local server and exit (for container HEALTHCHECK)")
	flag.Parse()
	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	svc, err := relay.New(relay.Config{
		Providers:   providersFromEnv(),
		DailyCap:    intEnv("DAILY_CAP", 0),
		Logger:      logger,
		Attribution: relay.Attribution{Referer: os.Getenv("ATTRIBUTION_REFERER"), Title: os.Getenv("ATTRIBUTION_TITLE")},
	})
	if err != nil {
		logger.Error("startup failed", slog.Any("error", err),
			slog.String("hint", "set a primary provider and its API key, e.g. PROVIDER=groq GROQ_API_KEY=... GROQ_MODEL=..."))
		os.Exit(1)
	}

	addr := ":" + strEnv("PORT", "8080")
	mux := http.NewServeMux()
	mux.Handle("POST /v1/chat/completions", svc.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		logger.Info("llm-relay listening", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// runHealthcheck probes the local /healthz endpoint and returns a process exit
// code. Used by the container HEALTHCHECK, which has no shell or curl available.
func runHealthcheck() int {
	url := "http://127.0.0.1:" + strEnv("PORT", "8080") + "/healthz"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// providersFromEnv builds the failover chain from PROVIDER (primary) and
// FALLBACK (comma-separated). Each provider name N reads N_API_KEY, N_MODEL,
// and optional N_BASE_URL (required for names not built in). NO_TRAIN toggles
// the OpenRouter no-data-collection hint.
func providersFromEnv() []relay.Provider {
	order := []string{}
	if p := strings.TrimSpace(os.Getenv("PROVIDER")); p != "" {
		order = append(order, p)
	}
	for _, f := range strings.Split(os.Getenv("FALLBACK"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			order = append(order, f)
		}
	}

	noTrain := boolEnv("NO_TRAIN", false)
	providers := make([]relay.Provider, 0, len(order))
	for _, name := range order {
		key := strings.ToUpper(name)
		providers = append(providers, relay.Provider{
			Name:    name,
			BaseURL: os.Getenv(key + "_BASE_URL"),
			APIKey:  os.Getenv(key + "_API_KEY"),
			Model:   os.Getenv(key + "_MODEL"),
			NoTrain: noTrain,
		})
	}
	return providers
}

func strEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func boolEnv(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
