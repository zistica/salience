# Salience

> Track how often your brand appears in answers from ChatGPT, Claude, and
> Perplexity — and what you'd need to do to be recommended more.

[![CI](https://github.com/zistica/salience/actions/workflows/ci.yml/badge.svg)](https://github.com/zistica/salience/actions/workflows/ci.yml)
[![License: AGPL v3](https://img.shields.io/badge/license-AGPL%20v3-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.25-00ADD8)](go.mod)

Salience is a **local-first**, single-binary tool that benchmarks your brand
inside LLM answers. It queries each configured model with the prompts your
customers actually type, detects every brand mention in both the answer text
and the model's cited sources, classifies sentiment, and shows you a per-
prompt picture of where you're winning, losing, and invisible.

It also probes the LLMs for **why** competitors are winning and **what** you'd
need to do to flip those recommendations — turning a measurement tool into
an optimization tool.

---

## Why this exists

Customers ask LLMs for product recommendations now. If ChatGPT recommends
your competitor and never mentions you, that's a traffic source going dark.
Traditional SEO tools measure Google rankings — they have no idea what's
happening inside an LLM's answer.

Salience measures exactly that gap, locally, with statistical rigor, across
every script and language a brand might appear in.

## What it does

- **Benchmark** — sends each prompt to each provider N times, persists every
  response to a local SQLite file.
- **Detect** — finds every brand mention in the answer text and the model's
  cited URLs. Works in Latin, CJK (Japanese, Chinese, Korean), Cyrillic,
  Arabic, Hebrew, Thai, and Devanagari scripts. Script transitions count as
  word boundaries, so `Toyotaの車` correctly detects `Toyota`.
- **Classify** — three-way sentiment per mention (positive / neutral /
  negative) using weighted keyword markers in English, Japanese, Spanish,
  French, and German.
- **Quantify** — Wilson 95% confidence intervals on every rate. Cells with
  n &lt; 10 are flagged so you don't over-claim from noise.
- **Attribute** — every cited URL is grouped by domain and categorized
  (review site / forum / wiki / news / vendor blog / etc.). The **source
  gap** report surfaces domains driving competitor mentions that you don't
  co-occur with.
- **Explain** — probes the LLM with follow-up calls (*"Earlier you mentioned
  X, why?"*) and aggregates the stated reasons per competitor.
- **Advise** — asks the LLM (*"What would my brand need to demonstrate to
  earn this recommendation?"*) and persists a ranked action plan per
  losing prompt.
- **Compare** — `salience diff` between any two runs, with statistical
  significance flagged when 95% CIs no longer overlap.
- **Visualize** — a local web dashboard (`salience serve`) with light and
  dark themes, live SSE updates while a bench runs, and a project picker
  for tracking multiple brands at once.

## Quick start

```bash
# Build (Go 1.25+)
make build

# Set up your first project
./salience init                  # writes salience.json + .env template
$EDITOR salience.json            # add brand, competitors, prompts, providers
$EDITOR .env                     # add OPENAI_API_KEY / ANTHROPIC_API_KEY / PERPLEXITY_API_KEY

# Take a measurement
./salience bench -dry-run        # cost preview
./salience bench                 # run it

# Look at the results
./salience report                # Markdown summary
./salience serve                 # open the dashboard at http://127.0.0.1:7878
```

The dashboard supports creating, editing, and deleting projects from the UI,
and lets you trigger benchmark / explain / advise runs with cost-preview
modals. Power users keep the CLI; everyone else lives in the dashboard.

## Subcommands

| Command | What it does |
|---|---|
| `salience init` | Write a starter config + `.env` template |
| `salience bench` | Query every provider for every prompt, persist results |
| `salience report` | Markdown / HTML / JSON / CSV report from stored data |
| `salience runs` | List every persisted run |
| `salience show -run N` | Inspect samples + mentions + sentiment + context for one run |
| `salience diff -from A -to B` | Per-cell movement between two runs with significance |
| `salience sources` | URL / domain leaderboard and the source-gap to-do list |
| `salience explain` | Probe the LLM for *why* it recommended each competitor |
| `salience advise` | Ask the LLM what you'd need to do to win each losing prompt |
| `salience playbook` | One Markdown doc combining sources + explain + advise |
| `salience serve` | Local web dashboard with live updates |
| `salience project` | Manage workspaces (list / new / show / edit / export / import / delete) |

## Architecture

```
salience/
├── main.go                          CLI dispatcher + signal handling
├── internal/
│   ├── cli/                         subcommand glue (init, bench, report, …)
│   ├── config/                      config schema + .env loader + validation
│   ├── detect/                      brand-mention detection, i18n, sentiment
│   ├── envfile/                     minimal .env loader (no overwrite)
│   ├── pricing/                     per-model USD pricing table
│   ├── provider/                    OpenAI / Anthropic / Perplexity over net/http
│   ├── report/                      rate math, confidence intervals, renderers
│   ├── runner/                      fan-out, retries, resume, cost estimate
│   ├── server/                      dashboard HTTP server + SSE + embedded UI
│   ├── sources/                     URL / domain attribution analysis
│   └── store/                       SQLite schema + queries (modernc.org/sqlite)
└── .github/workflows/ci.yml         test + cross-compile on push
```

## Supported providers

| Provider | Models | Notes |
|---|---|---|
| OpenAI | gpt-4o, gpt-4o-mini, gpt-4.1, gpt-4.1-mini, o3-mini | Web-search tool requested when available |
| Anthropic | Claude 3.5 (Sonnet, Haiku), Claude 4 (Haiku 4.5, Sonnet 4.5/4.6, Opus 4.5/4.7) | Web-search tool block (`web_search_20250305`) |
| Perplexity | sonar, sonar-pro, sonar-reasoning, sonar-reasoning-pro | Native search built in |

All providers accept endpoint overrides via `OPENAI_BASE_URL`,
`ANTHROPIC_BASE_URL`, `PERPLEXITY_BASE_URL` — useful for proxies and local
LLM gateways.

## Cost protection

Every paid action (`bench`, `explain`, `advise`) supports:

- `-dry-run` — preview the call count and estimated cost without spending
- `-max-cost USD` — abort if the estimate exceeds your cap
- `-yes` — skip the interactive confirmation prompt (for cron / CI)

The dashboard mirrors all three: clicking **Run benchmark** opens a cost-
preview modal you must confirm before any provider is called.

## Project status

Salience is alpha but operationally usable today. It is dogfood for
[zistica](https://github.com/zistica) and the rough edges are documented
in [CHANGELOG.md](CHANGELOG.md). API surface may still change.

## License

Salience is released under the [GNU AGPLv3](LICENSE).

- **Free for personal and internal use** — self-host, modify, extend.
- **Hosted SaaS / commercial integrations**: the AGPL requires you to
  open-source any modifications you offer over a network. If that doesn't
  fit, reach out about a commercial license.

See [LICENSE](LICENSE) for the full text.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports, prompts for new
provider adapters, and i18n sentiment-marker contributions are especially
welcome.

## Security

See [SECURITY.md](SECURITY.md). Do not file public issues for security
vulnerabilities.

---

Built and maintained by [zistica](https://github.com/zistica) · Fukuoka, Japan.
