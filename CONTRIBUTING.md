# Contributing

Thanks for your interest in `llm-relay`. Issues and pull requests are welcome.

## Ground rules

- **Standard library only.** The `relay` package has zero third-party
  dependencies and we intend to keep it that way. If you think you need a
  dependency, open an issue first to discuss it.
- **Keep the relay thin.** It forwards `messages`/`tools` verbatim and only sets
  `model`, `stream`, and the optional no-train hint. It does not parse, rewrite,
  or buffer message content.
- **Framework-agnostic core.** The `relay` package exposes a stdlib
  `http.Handler`. Anything Gin/chi/echo-specific belongs in the caller.

See [AGENTS.md](AGENTS.md) for the architecture and layout.

## Development

Requires Go 1.26+.

```sh
make test      # go test -race ./...   (must pass)
make lint      # go vet + gofmt -l check
make build     # static binary
make run       # run the server locally
```

Before opening a PR:

1. `make lint` is clean and `make test` passes.
2. New behaviour has a test (table-driven, using `httptest` fakes — see the
   existing `relay/*_test.go`).
3. Exported symbols have full-sentence doc comments.
4. If you touched config or providers, the README config table / provider list
   and `cmd/llm-relay` env parsing stay in sync.

## Adding a provider

Most "new providers" need no code — they are OpenAI-compatible, so a user just
sets `<NAME>_BASE_URL`. Only add a row to `knownProviders` in
`relay/provider.go` when you want a built-in default URL or a protocol quirk
(`supportsNoTrain`, `attribution`). Update the README provider list if you do.

## Commits & PRs

- Use clear, imperative commit messages (Conventional Commits style is
  appreciated: `feat:`, `fix:`, `docs:`, ...).
- Keep PRs focused; one logical change per PR.

## Reporting security issues

Please do not file public issues for vulnerabilities — see
[SECURITY.md](SECURITY.md).
