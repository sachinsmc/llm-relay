<!-- Keep PRs focused: one logical change. -->

## What & why

<!-- What does this change and what problem does it solve? Link any issue. -->

## Checklist

- [ ] `make lint` is clean (`go vet` + `gofmt`)
- [ ] `make test` passes (`go test -race ./...`)
- [ ] New behaviour has a test
- [ ] Exported symbols have doc comments
- [ ] No new third-party dependencies (or discussed in an issue first)
- [ ] Docs updated if config/providers changed (README + `cmd/llm-relay`)
