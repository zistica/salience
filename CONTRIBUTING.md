# Contributing to Salience

Thanks for considering a contribution. A few quick things before you dive in.

## Project scope

Salience tracks how often configured brands appear in LLM answers (OpenAI,
Anthropic, Perplexity). The core philosophy is **single static binary,
pure-Go dependencies, local-first**. Contributions that pull this in a
different direction (heavy frameworks, daemons, cloud lock-in) will probably
be politely declined.

## Getting set up

```bash
git clone https://github.com/zistica/salience
cd salience
make build         # produces ./salience
make test          # runs the full suite
make vet
```

Go 1.25 or newer required. The only external dependency is
`modernc.org/sqlite` (pure-Go SQLite — no cgo).

## Running locally

```bash
./salience init               # write a starter config + .env in the cwd
./salience bench              # query providers, persist results
./salience serve              # launch the dashboard at http://127.0.0.1:7878
```

For development, point providers at a mock to avoid burning API credits:

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1/chat/completions
```

## Tests

Please add tests for any non-trivial logic. The existing suite covers:

- `internal/detect`   — brand-mention detection, i18n, sentiment
- `internal/report`   — rate math, confidence intervals
- `internal/sources`  — source attribution, domain categorization
- `internal/runner`   — end-to-end against `httptest` mocks
- `internal/server`   — API contract tests
- `internal/store`    — DB lifecycle

`go test ./...` should pass before you open a PR.

## Coding conventions

- Standard `gofmt` / `go vet` cleanliness.
- Idiomatic Go layout: `internal/` for non-public packages, `cmd/` is reserved
  for binary entrypoints (currently only `main.go` at the root).
- Avoid pulling in cgo dependencies — the single-binary story matters.
- Avoid heavy JS frameworks in the dashboard — vanilla JS + inline SVG only.
- Keep comments focused on *why*, not *what*.

## Pull requests

- One concern per PR. Multi-feature PRs will be asked to split.
- Update the README / CHANGELOG when behavior changes.
- Don't include AI-tooling co-author footers in commits.
- New subcommands should have CLI parity + dashboard parity where applicable.

## Reporting bugs / security

- General bugs: open an issue with a reproducible config and the version
  reported by `salience version`.
- Security issues: see [SECURITY.md](./SECURITY.md) — do not file public
  issues for vulnerabilities.

## License

By contributing, you agree your contribution is licensed under the AGPL-3.0
(see [LICENSE](./LICENSE)). If you cannot agree to this, please don't open
a PR — we cannot accept it.
