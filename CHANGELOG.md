# Changelog

All notable changes to salience are recorded here. Versions follow
[semver](https://semver.org/): `MAJOR.MINOR.PATCH`.

## [Unreleased]

### Added — the v0.2 "diagnose → prescribe → act → verify" layer

The big shift from "measurement tool" toward "optimization tool":

- **`salience scrape`** — fetches every URL cited by a run and persists its
  title, meta description, and first ~4 KB of body text via a new
  `scraped_pages` table. Powers everything below.
- **`salience expand`** — asks an LLM to brainstorm 20+ realistic customer
  prompt variations based on the project's existing prompts. Staged in a
  `prompt_suggestions` table for the user to accept/reject.
- **`salience brief`** — generates a Markdown content brief for one losing
  prompt by combining scraped competitor content + LLM-stated competitor
  strengths + LLM-recommended actions. Persisted to `content_briefs`.
- **`salience action`** — `add`/`list`/`rm` operational events ("Published
  G2 listing", "Wikipedia article approved"). `salience diff` now overlays
  these between two runs so you can see what your team did in the window.
- **`salience schedule`** — recurring benchmarks driven by a server-side
  ticker (only active while `salience serve` is running). Supports standard
  5-field cron, plus `@hourly`/`@daily`/`@weekly`/`@every <duration>`. New
  internal `cron` package — no external dep.
- **`salience watch`** — periodic fetch of external URLs (G2 categories,
  Wikipedia, listicles). Hashes content, stores snapshots, flags changes.
- **`salience simulate`** — prepends a Markdown draft as context to each
  provider call and compares baseline vs primed mention rate, so you can
  test content *before* you publish.
- **Page-level gap analysis** in `salience sources` — detects listicle
  pages (title contains "Top N" / "Best of" / "in 2026") and surfaces
  ones that mention competitors but never the user's brand. Concrete
  "ask to be added here" to-do list.
- **Cross-model triangulation in advise** — every provider now answers
  about every losing prompt from an outside perspective, not just
  justifying its own pick.

### Added — supporting infrastructure
- New tables: `scraped_pages`, `prompt_suggestions`, `content_briefs`,
  `actions`, `schedules`, `watchers`, `watcher_snapshots`, `simulations`.
- New internal packages: `internal/scraper`, `internal/cron`.
- New read-only dashboard endpoints: `/api/scraped`, `/api/actions`,
  `/api/briefs`, `/api/suggestions`, `/api/schedules`, `/api/watchers`,
  `/api/simulations`.

### Fixed
- Data race in `runner.Runner.Run` when `Out` is a non-thread-safe
  `io.Writer` (e.g. `bytes.Buffer` in tests). Now wrapped in a mutex
  shim. Caught by CI on the v0.1.0 release branch.

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
