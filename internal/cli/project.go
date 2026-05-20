package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/store"
)

// RunProject is the top-level dispatcher for `salience project ...`.
func RunProject(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return projectUsage()
	}
	switch args[0] {
	case "list", "ls":
		return projectList(ctx, args[1:])
	case "show":
		return projectShow(ctx, args[1:])
	case "new", "create":
		return projectNew(ctx, args[1:])
	case "edit":
		return projectEdit(ctx, args[1:])
	case "delete", "rm":
		return projectDelete(ctx, args[1:])
	case "export":
		return projectExport(ctx, args[1:])
	case "import":
		return projectImport(ctx, args[1:])
	case "-h", "--help", "help":
		return projectUsage()
	}
	return fmt.Errorf("unknown project subcommand %q (try `salience project help`)", args[0])
}

func projectUsage() error {
	fmt.Print(`salience project — manage tracked workspaces

Usage:
  salience project list                       List all projects
  salience project show     NAME|ID           Print a project's config as JSON
  salience project new      [-from FILE]      Create a project (interactive, or imported from JSON)
  salience project edit     NAME|ID           Open a project JSON file in $EDITOR and save it back
  salience project delete   NAME|ID           Delete a project and all its runs
  salience project export   NAME|ID [-out F]  Dump a project as JSON
  salience project import   [-in FILE]        Create a project from JSON (file or stdin)

Each paid command (bench, explain, advise) accepts -project NAME-OR-ID.
`)
	return nil
}

func openStore(args []string) (*store.Store, []string, error) {
	fs := flag.NewFlagSet("project", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return nil, nil, err
	}
	return st, fs.Args(), nil
}

func projectList(ctx context.Context, args []string) error {
	st, _, err := openStore(args)
	if err != nil {
		return err
	}
	defer st.Close()
	projs, err := st.ListProjects(ctx)
	if err != nil {
		return err
	}
	if len(projs) == 0 {
		fmt.Println("no projects yet. Create one with `salience project new` or via the dashboard.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSLUG\tCOMPETITORS\tPROMPTS\tPROVIDERS\tUPDATED")
	for _, p := range projs {
		var comps, prompts, provs []any
		_ = json.Unmarshal([]byte(p.CompetitorsJSON), &comps)
		_ = json.Unmarshal([]byte(p.PromptsJSON), &prompts)
		_ = json.Unmarshal([]byte(p.ProvidersJSON), &provs)
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\t%d\t%s\n",
			p.ID, p.Name, p.Slug, len(comps), len(prompts), len(provs),
			p.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}

// resolveProject looks up a project by id or name/slug.
func resolveProject(ctx context.Context, st *store.Store, key string) (*store.Project, error) {
	if id, err := strconv.ParseInt(key, 10, 64); err == nil {
		if p, err := st.GetProject(ctx, id); err == nil {
			return p, nil
		}
	}
	return st.GetProjectBySlugOrName(ctx, key)
}

func projectShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: salience project show NAME|ID")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := resolveProject(ctx, st, rest[0])
	if err != nil {
		return err
	}
	return writeProjectJSON(os.Stdout, p)
}

func projectExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	outPath := fs.String("out", "", "write to file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: salience project export NAME|ID [-out FILE]")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := resolveProject(ctx, st, rest[0])
	if err != nil {
		return err
	}
	var out io.Writer = os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	if err := writeProjectJSON(out, p); err != nil {
		return err
	}
	if *outPath != "" {
		fmt.Printf("wrote %s\n", *outPath)
	}
	return nil
}

func projectImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	inPath := fs.String("in", "", "read JSON from file (default: stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var data []byte
	if *inPath != "" {
		b, err := os.ReadFile(*inPath)
		if err != nil {
			return err
		}
		data = b
	} else {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		data = b
	}
	var pj projectJSON
	if err := json.Unmarshal(data, &pj); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	pr := projectFromJSON(pj)
	id, err := st.CreateProject(ctx, pr)
	if err != nil {
		return err
	}
	fmt.Printf("created project #%d %q\n", id, pj.Name)
	return nil
}

func projectNew(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	fromPath := fs.String("from", "", "read JSON spec from file (skips prompts)")
	name := fs.String("name", "", "project name")
	brand := fs.String("brand", "", "brand name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromPath != "" {
		return projectImport(ctx, []string{"-db", *dbPath, "-in", *fromPath})
	}
	if *name == "" || *brand == "" {
		return fmt.Errorf("usage: salience project new -name NAME -brand BRAND\n" +
			"  Or: salience project new -from project.json\n" +
			"  Or: use the dashboard at `salience serve` for an interactive form")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	brandJSON, _ := json.Marshal(config.Brand{Name: *brand})
	id, err := st.CreateProject(ctx, store.Project{
		Name:                   *name,
		BrandJSON:              string(brandJSON),
		CompetitorsJSON:        "[]",
		PromptsJSON:            "[]",
		ProvidersJSON:          "[]",
		SamplesPerPrompt:       5,
		ConcurrencyPerProvider: 3,
		MaxTokens:              512,
	})
	if err != nil {
		return err
	}
	fmt.Printf("created project #%d %q\n", id, *name)
	fmt.Println("Add competitors / prompts / providers via the dashboard or `salience project edit`.")
	return nil
}

func projectEdit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: salience project edit NAME|ID")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := resolveProject(ctx, st, rest[0])
	if err != nil {
		return err
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	tmp, err := os.CreateTemp("", "salience-project-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := writeProjectJSON(tmp, p); err != nil {
		return err
	}
	tmp.Close()
	cmd := exec.Command(editor, tmp.Name())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	var pj projectJSON
	if err := json.Unmarshal(data, &pj); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	pr := projectFromJSON(pj)
	pr.ID = p.ID
	if err := st.UpdateProject(ctx, pr); err != nil {
		return err
	}
	fmt.Printf("saved project #%d %q\n", p.ID, pj.Name)
	return nil
}

func projectDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	force := fs.Bool("force", false, "skip confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: salience project delete NAME|ID")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := resolveProject(ctx, st, rest[0])
	if err != nil {
		return err
	}
	if !*force {
		fmt.Printf("Delete project #%d %q and all its runs? [y/N] ", p.ID, p.Name)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Println("aborted")
			return nil
		}
	}
	if err := st.DeleteProject(ctx, p.ID); err != nil {
		return err
	}
	fmt.Printf("deleted project #%d %q\n", p.ID, p.Name)
	return nil
}

// ---------- shared JSON shape for import/export ----------

type projectJSON struct {
	ID                     int64             `json:"id,omitempty"`
	Name                   string            `json:"name"`
	Slug                   string            `json:"slug,omitempty"`
	Brand                  config.Brand      `json:"brand"`
	Competitors            []config.Brand    `json:"competitors"`
	Prompts                []string          `json:"prompts"`
	Providers              []config.Provider `json:"providers"`
	Regions                []config.Region   `json:"regions,omitempty"`
	SamplesPerPrompt       int               `json:"samples_per_prompt"`
	ConcurrencyPerProvider int               `json:"concurrency_per_provider"`
	MaxTokens              int               `json:"max_tokens"`
	Notes                  string            `json:"notes,omitempty"`
	CreatedAt              string            `json:"created_at,omitempty"`
	UpdatedAt              string            `json:"updated_at,omitempty"`
}

func writeProjectJSON(w io.Writer, p *store.Project) error {
	var brand config.Brand
	var comps []config.Brand
	var prompts []string
	var provs []config.Provider
	var regions []config.Region
	_ = json.Unmarshal([]byte(p.BrandJSON), &brand)
	_ = json.Unmarshal([]byte(p.CompetitorsJSON), &comps)
	_ = json.Unmarshal([]byte(p.PromptsJSON), &prompts)
	_ = json.Unmarshal([]byte(p.ProvidersJSON), &provs)
	_ = json.Unmarshal([]byte(p.RegionsJSON), &regions)
	pj := projectJSON{
		ID: p.ID, Name: p.Name, Slug: p.Slug,
		Brand: brand, Competitors: comps, Prompts: prompts, Providers: provs,
		Regions:                regions,
		SamplesPerPrompt:       p.SamplesPerPrompt,
		ConcurrencyPerProvider: p.ConcurrencyPerProvider,
		MaxTokens:              p.MaxTokens,
		Notes:                  p.Notes,
		CreatedAt:              p.CreatedAt.Format(time.RFC3339),
		UpdatedAt:              p.UpdatedAt.Format(time.RFC3339),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(pj)
}

func projectFromJSON(pj projectJSON) store.Project {
	brand, _ := json.Marshal(pj.Brand)
	comps, _ := json.Marshal(pj.Competitors)
	prompts, _ := json.Marshal(pj.Prompts)
	provs, _ := json.Marshal(pj.Providers)
	regions, _ := json.Marshal(pj.Regions)
	return store.Project{
		Name:                   pj.Name,
		Slug:                   pj.Slug,
		BrandJSON:              string(brand),
		CompetitorsJSON:        string(comps),
		PromptsJSON:            string(prompts),
		ProvidersJSON:          string(provs),
		RegionsJSON:            string(regions),
		SamplesPerPrompt:       pj.SamplesPerPrompt,
		ConcurrencyPerProvider: pj.ConcurrencyPerProvider,
		MaxTokens:              pj.MaxTokens,
		Notes:                  pj.Notes,
	}
}
