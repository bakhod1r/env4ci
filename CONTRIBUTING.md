# Contributing

```bash
go test -race ./...
golangci-lint run
```

## Layout

- `internal/domain`: pure rules, no I/O. Tests first.
- `internal/application`: use cases over the `Provider` port.
- `internal/infrastructure`: parsers, config, HTTP, providers.
- `cmd/env4ci`: CLI.

## Golden files

Scanner and CLI output are checked against `testdata/**/*.golden`. After an intended change:

```bash
go test ./internal/infrastructure/ciscan ./cmd/env4ci -update
git diff testdata   # review every line
```

## New provider

Implement `application.Provider` (and `application.Validator` if the platform has value limits),
test it against `httptest`, and wire it in `cmd/env4ci/main.go`.

## Rules

- Never print or log a variable value.
- Conventional commits (`feat:`, `fix:`, `docs:` ...).
