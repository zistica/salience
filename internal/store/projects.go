package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Project is one tracked brand workspace. The config fields are serialized
// as JSON blobs inside the row so the UI can edit them without touching
// disk, and a future schema change to the inner config doesn't require a
// migration of the projects table itself.
type Project struct {
	ID                     int64
	Name                   string
	Slug                   string
	BrandJSON              string
	CompetitorsJSON        string
	PromptsJSON            string
	ProvidersJSON          string
	RegionsJSON            string // v0.3 — JSON array of Region {code,label,prefix}
	SamplesPerPrompt       int
	ConcurrencyPerProvider int
	MaxTokens              int
	Notes                  string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// ErrProjectNotFound is returned when a lookup by id/name/slug misses.
var ErrProjectNotFound = errors.New("project not found")

// CreateProject inserts a new project. The slug must be unique; if blank
// it's auto-derived from name.
func (s *Store) CreateProject(ctx context.Context, p Project) (int64, error) {
	if strings.TrimSpace(p.Name) == "" {
		return 0, fmt.Errorf("project name is required")
	}
	if p.Slug == "" {
		p.Slug = slugify(p.Name)
	}
	p.BrandJSON = orJSONObject(p.BrandJSON)
	p.CompetitorsJSON = orJSONArray(p.CompetitorsJSON)
	p.PromptsJSON = orJSONArray(p.PromptsJSON)
	p.ProvidersJSON = orJSONArray(p.ProvidersJSON)
	p.RegionsJSON = orJSONArray(p.RegionsJSON)
	if p.SamplesPerPrompt <= 0 {
		p.SamplesPerPrompt = 5
	}
	if p.ConcurrencyPerProvider <= 0 {
		p.ConcurrencyPerProvider = 3
	}
	if p.MaxTokens <= 0 {
		p.MaxTokens = 512
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO projects(name, slug, brand_json, competitors_json, prompts_json,
			providers_json, regions_json, samples_per_prompt, concurrency_per_provider, max_tokens,
			notes, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Slug, p.BrandJSON, p.CompetitorsJSON, p.PromptsJSON,
		p.ProvidersJSON, p.RegionsJSON, p.SamplesPerPrompt, p.ConcurrencyPerProvider, p.MaxTokens,
		p.Notes, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateProject replaces the editable fields of an existing project.
func (s *Store) UpdateProject(ctx context.Context, p Project) error {
	if p.ID == 0 {
		return fmt.Errorf("project id is required")
	}
	if p.Slug == "" {
		p.Slug = slugify(p.Name)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	p.RegionsJSON = orJSONArray(p.RegionsJSON)
	res, err := s.db.ExecContext(ctx, `
		UPDATE projects
		   SET name=?, slug=?, brand_json=?, competitors_json=?, prompts_json=?,
		       providers_json=?, regions_json=?, samples_per_prompt=?, concurrency_per_provider=?,
		       max_tokens=?, notes=?, updated_at=?
		 WHERE id=?`,
		p.Name, p.Slug, p.BrandJSON, p.CompetitorsJSON, p.PromptsJSON,
		p.ProvidersJSON, p.RegionsJSON, p.SamplesPerPrompt, p.ConcurrencyPerProvider, p.MaxTokens,
		p.Notes, now, p.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// DeleteProject removes a project and all its runs / samples / mentions
// (cascade through FK).
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// ListProjects returns all projects, newest-first.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, slug, brand_json, competitors_json, prompts_json,
		       providers_json, COALESCE(regions_json, '[]'), samples_per_prompt, concurrency_per_provider,
		       max_tokens, notes, created_at, updated_at
		FROM projects ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjects(rows)
}

// GetProject loads one project by id.
func (s *Store) GetProject(ctx context.Context, id int64) (*Project, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, brand_json, competitors_json, prompts_json,
		       providers_json, COALESCE(regions_json, '[]'), samples_per_prompt, concurrency_per_provider,
		       max_tokens, notes, created_at, updated_at
		FROM projects WHERE id=?`, id)
	return scanProjectRow(row)
}

// GetProjectBySlugOrName returns the project matching either the slug or the
// name. Useful for CLI `-project` flag.
func (s *Store) GetProjectBySlugOrName(ctx context.Context, key string) (*Project, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, ErrProjectNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, brand_json, competitors_json, prompts_json,
		       providers_json, COALESCE(regions_json, '[]'), samples_per_prompt, concurrency_per_provider,
		       max_tokens, notes, created_at, updated_at
		FROM projects WHERE slug=? OR name=? LIMIT 1`, key, key)
	return scanProjectRow(row)
}

// LatestProjectID returns the most recently created project id, or 0 if none.
func (s *Store) LatestProjectID(ctx context.Context) (int64, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id FROM projects ORDER BY id DESC LIMIT 1`)
	var id int64
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// AttachRunToProject sets the project_id on a run. Used during migration
// from legacy schema and when a CLI run is recorded against a project.
func (s *Store) AttachRunToProject(ctx context.Context, runID, projectID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET project_id=? WHERE id=?`, projectID, runID)
	return err
}

// ListRunsForProject filters runs by project_id.
func (s *Store) ListRunsForProject(ctx context.Context, projectID int64, limit int) ([]Run, error) {
	q := `SELECT id, started_at, finished_at, brand_name, status, config_json
	      FROM runs WHERE project_id = ? ORDER BY id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var started string
		var finished sql.NullString
		if err := rows.Scan(&r.ID, &started, &finished, &r.BrandName, &r.Status, &r.ConfigJSON); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, started); err == nil {
			r.StartedAt = t
		}
		if finished.Valid {
			if t, err := time.Parse(time.RFC3339Nano, finished.String); err == nil {
				r.FinishedAt = &t
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- helpers ---

func scanProjects(rows *sql.Rows) ([]Project, error) {
	var out []Project
	for rows.Next() {
		p, err := scanProjectRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProjectRow(row rowScanner) (*Project, error) {
	var p Project
	var created, updated string
	if err := row.Scan(&p.ID, &p.Name, &p.Slug,
		&p.BrandJSON, &p.CompetitorsJSON, &p.PromptsJSON, &p.ProvidersJSON,
		&p.RegionsJSON,
		&p.SamplesPerPrompt, &p.ConcurrencyPerProvider, &p.MaxTokens,
		&p.Notes, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		p.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		p.UpdatedAt = t
	}
	return &p, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify produces a URL-safe lowercase slug.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = fmt.Sprintf("project-%d", time.Now().Unix())
	}
	return s
}

func orJSONObject(s string) string {
	if strings.TrimSpace(s) == "" || !json.Valid([]byte(s)) {
		return "{}"
	}
	return s
}
func orJSONArray(s string) string {
	if strings.TrimSpace(s) == "" || !json.Valid([]byte(s)) {
		return "[]"
	}
	return s
}
