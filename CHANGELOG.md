# Changelog

All notable changes to salience are recorded here. Versions follow
[semver](https://semver.org/): `MAJOR.MINOR.PATCH`.

## [Unreleased]

### Added
- `salience runs` lists every persisted run with start time, duration, brand,
  status, and ok/errored sample counts.
- `salience show` inspects a single run: the samples table with cost and
  brand-hit summary, plus every detected mention with alias and origin.
- Report formats: `-format json` for machine consumption, `-format csv` for
  pipe-into-a-spreadsheet workflows. `markdown` (default) and `html` continue
  to work unchanged.
- Provider endpoint overrides via `OPENAI_BASE_URL` and `ANTHROPIC_BASE_URL`
  environment variables. Used by the integration test suite; also handy for
  routing through a local LLM gateway.
- Pricing table now covers the Claude 4 family (`claude-haiku-4-5`,
  `claude-sonnet-4-5`, `claude-sonnet-4-6`, `claude-opus-4-5`,
  `claude-opus-4-7`) alongside the existing Claude 3.5 and OpenAI entries.
- End-to-end integration test that stands up `httptest.Server` mocks for both
  providers, runs a full `bench` cycle, and asserts on persisted samples and
  mentions. No real network or API credits required.
- Auth-abort integration test that verifies a 401 from a provider short-
  circuits the run instead of looping on retries.
- Store unit tests covering open/migrate, the start/finish lifecycle, the
  unique-constraint resume path, list/count helpers, and run ordering.
- `buildVersion` baked at link time. `make build` now stamps the binary with
  `git describe --tags --always` via `-ldflags`; `salience version` prints it.
- Makefile cross-compile targets: `build-darwin-arm64`, `build-darwin-amd64`,
  `build-linux-amd64`, `build-linux-arm64`, plus a `dist` target that builds
  all four into `dist/`.

### Changed
- `make clean` now also removes `salience.db-wal` and `salience.db-shm`
  alongside the main DB file, and clears the `dist/` directory.

## [0.1.0] - 2026-05-20

Initial clean-room implementation. Three subcommands (`init`, `bench`,
`report`), OpenAI + Anthropic providers via raw `net/http`, SQLite-backed
storage via pure-Go `modernc.org/sqlite`, brand-mention detection with
word-boundary and domain-substring modes, Markdown and HTML report renderers,
graceful Ctrl-C with `-resume`, and a 4-attempt exponential-backoff retry
policy with jitter.
