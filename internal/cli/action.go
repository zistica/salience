package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/salience-cli/salience/internal/store"
)

// RunAction dispatches `salience action {add,list,remove}`.
func RunAction(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return actionUsage()
	}
	switch args[0] {
	case "add":
		return actionAdd(ctx, args[1:])
	case "list", "ls":
		return actionList(ctx, args[1:])
	case "remove", "rm":
		return actionRemove(ctx, args[1:])
	case "-h", "--help":
		return actionUsage()
	}
	return fmt.Errorf("unknown action subcommand %q (try `salience action help`)", args[0])
}

func actionUsage() error {
	fmt.Print(`salience action — log operational events for attribution

Usage:
  salience action add  "Description" -date YYYY-MM-DD [-prompts "p1,p2"] [-notes "..."]
  salience action list [-project NAME|ID]
  salience action rm ID

Logged actions get overlaid by 'salience diff' so you can see which rate
movements happened after which actions (correlational, not causal).
`)
	return nil
}

func actionAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action add", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project name or id (default: latest)")
	dateStr := fs.String("date", time.Now().UTC().Format("2006-01-02"), "when the action happened (YYYY-MM-DD or RFC3339)")
	promptList := fs.String("prompts", "", "comma-separated prompts this action targets (optional)")
	notes := fs.String("notes", "", "free-form notes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("description argument required, e.g. salience action add \"Published G2 listing\" -date 2026-05-20")
	}
	desc := strings.Join(rest, " ")
	taken, err := parseDateFlexible(*dateStr)
	if err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	proj, err := pickProject(ctx, st, *projectKey)
	if err != nil {
		return err
	}
	var prompts []string
	for _, p := range strings.Split(*promptList, ",") {
		if t := strings.TrimSpace(p); t != "" {
			prompts = append(prompts, t)
		}
	}
	id, err := st.InsertAction(ctx, store.Action{
		ProjectID:        proj.ID,
		Description:      desc,
		TakenAt:          taken,
		AppliesToPrompts: prompts,
		Notes:            *notes,
	})
	if err != nil {
		return err
	}
	fmt.Printf("logged action #%d for project %q on %s\n", id, proj.Name, taken.Format("2006-01-02"))
	return nil
}

func actionList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action list", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project name or id (default: latest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	proj, err := pickProject(ctx, st, *projectKey)
	if err != nil {
		return err
	}
	actions, err := st.ListActions(ctx, proj.ID)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		fmt.Println("no actions logged yet")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDATE\tDESCRIPTION\tPROMPTS\tNOTES")
	for _, a := range actions {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			a.ID, a.TakenAt.Format("2006-01-02"),
			clip(a.Description, 50),
			clip(strings.Join(a.AppliesToPrompts, ", "), 40),
			clip(a.Notes, 30))
	}
	return tw.Flush()
}

func actionRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action rm", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: salience action rm ID")
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid id %q", rest[0])
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteAction(ctx, id); err != nil {
		return err
	}
	fmt.Printf("deleted action #%d\n", id)
	return nil
}

// parseDateFlexible accepts YYYY-MM-DD or RFC3339.
func parseDateFlexible(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("could not parse date %q (use YYYY-MM-DD or RFC3339)", s)
}
