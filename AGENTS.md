# AGENTS.md

Guidance for AI coding agents (and humans) working in this repository. See
[agents.md](https://agents.md) for the convention.

## What this is

`llm-relay` is a lightweight, OpenAI-compatible LLM gateway/proxy in Go. It
accepts a standard chat-completions request, enforces a per-user daily quota,
injects a server-side API key and model, and streams the upstream's Server-Sent
Events straight back. When the primary provider is rate-limited or down it fails
over to the next configured provider before any bytes reach the client.

It ships three ways from one codebase:

- **Library** — `import "github.com/sachinsmc/llm-relay/relay"`.
- **Standalone server** — `cmd/llm-relay`, configured from the environment.
- **Container** — distroless image built from the `Dockerfile`.

## Layout

```
relay/            # the library (no main, no framework deps)
  relay.go        # Config, Service, New, StartStream — the core relay + failover
  provider.go     # Provider, built-in registry, resolve()
  limiter.go      # Limiter interface + in-memory default
  handler.go      # stdlib http.Handler (SSE), error -> status mapping
  *_test.go       # table-driven tests using httptest fakes
cmd/llm-relay/    # standalone server: env config, graceful shutdown, healthcheck
```

## Conventions

- **Standard library only.** No third-party dependencies. Logging is
  `log/slog`; HTTP is `net/http`; no web framework in the `relay` package.
- **Keep the relay thin.** It forwards `messages`/`tools` verbatim (already
  OpenAI format) and only sets `model`, `stream`, and the optional no-train
  hint. Do not parse or rewrite message content.
- **Errors:** wrap with `fmt.Errorf("operation: %w", err)`; expose behaviour via
  the sentinel `Err*` values so callers can `errors.Is`.
- **Framework-agnostic core.** Anything Gin/chi/echo-specific belongs in the
  caller, not in `relay`. The package exposes a stdlib `http.Handler`.
- **Doc comments** on every exported symbol, full sentences.
- Run `gofmt` on save; code must be `gofmt`-clean.

## Adding a provider

Every upstream is OpenAI-compatible, so most "new providers" need no code — set
`<NAME>_BASE_URL`. Only add a row to `knownProviders` in `provider.go` when you
want a built-in default URL or a protocol quirk (`supportsNoTrain`,
`attribution`). Update the README provider list if you do.

## Commands

```sh
make test      # go test -race ./...   (must pass)
make lint      # go vet + gofmt -l check
make build     # static binary
make docker    # build the distroless image
make run       # run the server locally
```

## Definition of done

Before committing: `make lint` clean, `make test` green, and the standalone
server still builds (`go build ./...`). Keep the README's config table and
provider list in sync with `cmd/llm-relay` env parsing and `knownProviders`.
