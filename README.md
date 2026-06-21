# llm-relay

A lightweight, **OpenAI-compatible LLM gateway and proxy** written in Go. Point
any OpenAI client at it and get **multi-provider failover**, **SSE streaming
passthrough**, **server-side API keys**, and a **per-user daily quota** — in a
single static binary, a Docker image, or an embeddable Go package.

[![CI](https://github.com/sachinsmc/llm-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/sachinsmc/llm-relay/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sachinsmc/llm-relay.svg)](https://pkg.go.dev/github.com/sachinsmc/llm-relay)
[![Go Report Card](https://goreportcard.com/badge/github.com/sachinsmc/llm-relay)](https://goreportcard.com/report/github.com/sachinsmc/llm-relay)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Why

Calling LLM providers directly from a mobile or web client means shipping your
API key in the client and hard-coding one provider. `llm-relay` sits in between:

- **Keep keys server-side.** The client only ever talks to your relay; the
  upstream key and model never leave the server.
- **Swap providers without a client release.** Every upstream speaks the OpenAI
  chat-completions wire format, so you flip an env var to change provider.
- **Survive provider outages.** When the primary is rate-limited or down, the
  relay transparently fails over to the next provider — before any bytes reach
  the client, so it never sees a half-stream then a switch.
- **Cap abuse.** A per-user daily turn quota is built in (in-memory by default,
  pluggable for Redis/Postgres).

It is **provider-agnostic**: OpenAI, OpenRouter, Groq, Cerebras, Together,
Fireworks, or a local vLLM/Ollama all work — by name or by URL. **Tool calling
streams through** untouched; tool execution stays on the client, which re-POSTs
the tool result to continue the turn.

Zero third-party dependencies — just the Go standard library.

## How it works

```
client (OpenAI SDK)  ──POST /v1/chat/completions──►  llm-relay  ──►  primary provider
   ▲                                                    │  fail over │
   └──────────────  SSE stream  ◄───────────────────────┘            ▼
                                                              fallback provider(s)
```

## Quickstart

### Run with Docker

Pull the prebuilt multi-arch image from GHCR (no build needed):

```sh
docker run --rm -p 8080:8080 \
  -e PROVIDER=groq \
  -e GROQ_API_KEY=$GROQ_API_KEY \
  -e GROQ_MODEL=llama-3.3-70b-versatile \
  ghcr.io/sachinsmc/llm-relay:latest
```

Or build it yourself:

```sh
docker build -t llm-relay .
docker run --rm -p 8080:8080 --env-file .env llm-relay
```

Or with Compose (reads a local `.env`):

```sh
cp .env.example .env   # fill in your key
docker compose up --build
```

### Run the binary

```sh
go install github.com/sachinsmc/llm-relay/cmd/llm-relay@latest

PROVIDER=groq GROQ_API_KEY=$GROQ_API_KEY GROQ_MODEL=llama-3.3-70b-versatile \
  llm-relay
# llm-relay listening on :8080
```

### Run locally from source (no Docker)

Requires Go 1.26+. Clone, set your env, and run:

```sh
git clone https://github.com/sachinsmc/llm-relay
cd llm-relay
cp .env.example .env          # fill in PROVIDER + its <NAME>_API_KEY / <NAME>_MODEL

# load .env into the shell, then run
set -a; . ./.env; set +a
go run ./cmd/llm-relay        # or: make run
# llm-relay listening on :8080
```

Or pass the config inline without a file:

```sh
PROVIDER=groq GROQ_API_KEY=$GROQ_API_KEY GROQ_MODEL=llama-3.3-70b-versatile \
  go run ./cmd/llm-relay
```

Check it's up: `curl localhost:8080/healthz` → `ok`.

### Point any OpenAI client at it

The relay exposes the standard `POST /v1/chat/completions` route, so existing
OpenAI SDKs work by changing only the `base_url`:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="unused")
stream = client.chat.completions.create(
    model="ignored-the-relay-sets-it",
    messages=[{"role": "user", "content": "Hello!"}],
    stream=True,
    extra_headers={"X-User-Id": "user-123"},  # per-user quota key
)
for chunk in stream:
    print(chunk.choices[0].delta.content or "", end="")
```

```sh
curl -N http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-User-Id: user-123' \
  -d '{"messages":[{"role":"user","content":"Hello!"}]}'
```

The relay overrides `model` with the server-configured one, so the client's
`model` field is ignored.

## Use as a Go package

```go
import "github.com/sachinsmc/llm-relay/relay"

svc, err := relay.New(relay.Config{
    Providers: []relay.Provider{
        {Name: "groq", APIKey: os.Getenv("GROQ_API_KEY"), Model: "llama-3.3-70b-versatile"},
        {Name: "openrouter", APIKey: os.Getenv("OPENROUTER_API_KEY"), Model: "openai/gpt-4o-mini"}, // failover
    },
    DailyCap: 50,
})
if err != nil {
    log.Fatal(err)
}

// Mount the ready-made SSE handler on any router...
mux := http.NewServeMux()
mux.Handle("POST /v1/chat/completions", svc.Handler().WithUserFunc(userIDFromJWT))

// ...or drive the stream yourself.
stream, err := svc.StartStream(ctx, "user-123", reqBody)
```

`Service.Handler()` returns a stdlib `http.Handler`, so it drops into net/http,
chi, echo, or gin (`gin.WrapH`). Full API on
[pkg.go.dev](https://pkg.go.dev/github.com/sachinsmc/llm-relay/relay).

## Configuration

The standalone server is configured entirely from the environment:

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP port. |
| `DAILY_CAP` | `0` | Per-user daily turn cap (`0` = unlimited). |
| `PROVIDER` | — | Primary provider name (required). |
| `FALLBACK` | — | Comma-separated failover chain, tried in order. |
| `<NAME>_API_KEY` | — | API key for provider `NAME` (e.g. `GROQ_API_KEY`). |
| `<NAME>_MODEL` | — | Model id forwarded upstream for provider `NAME`. |
| `<NAME>_BASE_URL` | built-in for known names | Chat-completions URL. Required for custom providers; **overrides the built-in URL** for known ones (Azure, regional endpoints, a proxy, version pinning). |
| `NO_TRAIN` | `false` | OpenRouter-only no-data-collection routing hint. |
| `ATTRIBUTION_REFERER` / `ATTRIBUTION_TITLE` | — | Optional OpenRouter dashboard headers. |

See [`.env.example`](.env.example) for a worked example.

## Providers

Built-in names (URL filled in for you):

| Name | Endpoint |
|---|---|
| `openai` | `api.openai.com` |
| `openrouter` | `openrouter.ai` |
| `groq` | `api.groq.com` |
| `cerebras` | `api.cerebras.ai` |
| `gemini` | `generativelanguage.googleapis.com` (OpenAI-compatible endpoint) |
| `together` | `api.together.xyz` |
| `fireworks` | `api.fireworks.ai` |
| `deepseek` | `api.deepseek.com` |
| `mistral` | `api.mistral.ai` |

**Add any other provider from env — no code, no rebuild.** Pick a name, put it
in `PROVIDER`/`FALLBACK`, and set `<NAME>_BASE_URL`, `<NAME>_API_KEY`,
`<NAME>_MODEL`. Any OpenAI-compatible endpoint works (xAI, Perplexity, a local
vLLM/Ollama, an internal gateway, ...):

```sh
# A local model as primary, xAI as failover — both defined entirely in env.
PROVIDER=local FALLBACK=xai \
LOCAL_BASE_URL=http://localhost:11434/v1/chat/completions \
LOCAL_API_KEY=ollama LOCAL_MODEL=llama3.1 \
XAI_BASE_URL=https://api.x.ai/v1/chat/completions \
XAI_API_KEY=$XAI_API_KEY XAI_MODEL=grok-2-latest \
  llm-relay
```

The built-in names above are just a convenience so you don't have to type a URL
for the common cases.

## Failover semantics

A provider is retried (failed over to the next) on a **network error, timeout,
`408`, `429`, or any `5xx`**. A `4xx` like `400/401/403` is a payload/config
problem the next provider can't fix, so it is surfaced immediately. Failover
always happens before the first byte is streamed back.

## Quota

A "turn" is one fresh user message. Tool-result continuations and failovers do
**not** count again. The default `MemoryLimiter` is per-process; implement
`relay.Limiter` to share the cap across instances (Redis, Postgres, ...).

## Development

```sh
make test     # go test -race ./...
make lint     # go vet + gofmt check
make build    # static binary
make docker   # build the image
```

## License

[MIT](LICENSE)
