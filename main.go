// salience — a CLI that benchmarks how often configured brands appear in LLM answers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/salience-cli/salience/internal/cli"
)

const usage = `salience — benchmark how often your brand appears in LLM answers

Usage:
  salience init                      Write a starter config and .env template into the current directory.
  salience bench   [flags]           Run the benchmark and persist all responses to the SQLite database.
  salience report  [flags]           Render a Markdown / HTML / JSON / CSV report from persisted data.
  salience runs    [flags]           List persisted runs (newest first).
  salience show    [flags]           Inspect a single run: samples + every detected mention.
  salience diff    [flags]           Compare two runs and show the biggest rate movers.
  salience sources [flags]           URL / domain leaderboard for a run (no API calls).
  salience explain [flags]           Ask the LLM why it recommended each competitor.
  salience advise  [flags]           Ask the LLM what you'd need to do to win each losing prompt.
  salience playbook [flags]          Combined Markdown doc: sources + explain + advise.
  salience serve   [flags]           Local web dashboard with live updates over SSE.
  salience project SUB [flags]       Manage tracked workspaces (list/show/new/edit/delete/export/import).
  salience version                   Print the version string.

Common flags:
  -config PATH        Path to the JSON config (default: ./salience.json).
  -db PATH            Path to the SQLite database (default: ./salience.db).

bench flags:
  -resume             Resume the most recent unfinished run instead of starting a new one.

report flags:
  -format markdown|html|json|csv   Output format (default: markdown).
  -out PATH                        File to write to (default: stdout).
  -run N                           Run id to report on (default: latest).

runs flags:
  -limit N            Max rows to show (default 20; 0 = all).

show flags:
  -run N              Run id (default: latest).
  -mentions           Print only the mentions list, not the samples summary.

Environment:
  OPENAI_API_KEY       Required when an openai provider is configured.
  ANTHROPIC_API_KEY    Required when an anthropic provider is configured.
  OPENAI_BASE_URL      Optional override (defaults to the OpenAI public URL).
  ANTHROPIC_BASE_URL   Optional override (defaults to the Anthropic public URL).
  A .env file in the current directory is loaded automatically.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigs:
			fmt.Fprintln(os.Stderr, "\nreceived interrupt — shutting down gracefully; rerun with -resume to finish")
			cancel()
		case <-ctx.Done():
		}
	}()

	switch cmd {
	case "init":
		if err := cli.RunInit(args); err != nil {
			fail(err)
		}
	case "bench":
		if err := cli.RunBench(ctx, args); err != nil {
			if errors.Is(err, context.Canceled) {
				os.Exit(130)
			}
			fail(err)
		}
	case "report":
		if err := cli.RunReport(ctx, args); err != nil {
			fail(err)
		}
	case "runs":
		if err := cli.RunRuns(ctx, args); err != nil {
			fail(err)
		}
	case "show":
		if err := cli.RunShow(ctx, args); err != nil {
			fail(err)
		}
	case "diff":
		if err := cli.RunDiff(ctx, args); err != nil {
			fail(err)
		}
	case "sources":
		if err := cli.RunSources(ctx, args); err != nil {
			fail(err)
		}
	case "explain":
		if err := cli.RunExplain(ctx, args); err != nil {
			fail(err)
		}
	case "advise":
		if err := cli.RunAdvise(ctx, args); err != nil {
			fail(err)
		}
	case "playbook":
		if err := cli.RunPlaybook(ctx, args); err != nil {
			fail(err)
		}
	case "serve":
		if err := cli.RunServe(ctx, args); err != nil {
			fail(err)
		}
	case "project", "projects":
		if err := cli.RunProject(ctx, args); err != nil {
			fail(err)
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "-v", "--version", "version":
		fmt.Println("salience", versionString())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	_ = flag.Lookup // keeps the flag import alive for the linter if no subcommand parses; harmless
	_ = filepath.Clean
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "salience: %v\n", err)
	os.Exit(1)
}

// buildVersion is overwritten at link time via:
//   go build -ldflags "-X 'main.buildVersion=v1.2.3'"
// The Makefile feeds in `git describe --tags --always` so a built binary
// reports its commit/tag, not the fallback below.
var buildVersion = "0.1.0-dev"

func versionString() string {
	return buildVersion
}
