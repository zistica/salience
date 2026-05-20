// Package store persists salience runs to a local SQLite database. It uses
// modernc.org/sqlite (pure-Go, no cgo).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the connection handle to the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database file at path. It runs the embedded
// migrations and returns a ready handle.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
-- A project is a brand-tracking workspace. Each project carries its full
-- config inline so the UI can edit it without touching files on disk.
CREATE TABLE IF NOT EXISTS projects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	slug TEXT NOT NULL UNIQUE,
	brand_json TEXT NOT NULL DEFAULT '{}',
	competitors_json TEXT NOT NULL DEFAULT '[]',
	prompts_json TEXT NOT NULL DEFAULT '[]',
	providers_json TEXT NOT NULL DEFAULT '[]',
	samples_per_prompt INTEGER NOT NULL DEFAULT 5,
	concurrency_per_provider INTEGER NOT NULL DEFAULT 3,
	max_tokens INTEGER NOT NULL DEFAULT 512,
	notes TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
	started_at TEXT NOT NULL,
	finished_at TEXT,
	config_json TEXT NOT NULL,
	brand_name TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'running'
);

CREATE TABLE IF NOT EXISTS samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	prompt TEXT NOT NULL,
	provider_name TEXT NOT NULL,
	provider_kind TEXT NOT NULL,
	model TEXT NOT NULL,
	sample_idx INTEGER NOT NULL,
	region TEXT NOT NULL DEFAULT 'global',
	created_at TEXT NOT NULL,
	response_text TEXT NOT NULL,
	sources_json TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	UNIQUE(run_id, prompt, provider_name, region, sample_idx)
);

CREATE TABLE IF NOT EXISTS mentions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sample_id INTEGER NOT NULL REFERENCES samples(id) ON DELETE CASCADE,
	brand TEXT NOT NULL,
	alias TEXT NOT NULL,
	where_found TEXT NOT NULL,
	is_domain INTEGER NOT NULL DEFAULT 0,
	context TEXT NOT NULL DEFAULT '',
	sentiment TEXT NOT NULL DEFAULT 'neutral'
);

CREATE INDEX IF NOT EXISTS idx_samples_run ON samples(run_id);
CREATE INDEX IF NOT EXISTS idx_mentions_sample ON mentions(sample_id);

-- Follow-up "why was X recommended?" calls. One row per probe.
CREATE TABLE IF NOT EXISTS explanations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	source_sample_id INTEGER REFERENCES samples(id) ON DELETE SET NULL,
	provider_name TEXT NOT NULL,
	model TEXT NOT NULL,
	asked_about_brand TEXT NOT NULL,
	prompt TEXT NOT NULL,
	reasoning TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_explanations_run ON explanations(run_id);
CREATE INDEX IF NOT EXISTS idx_explanations_brand ON explanations(asked_about_brand);

-- Scraped HTML pages — the actual content the LLM is citing.
CREATE TABLE IF NOT EXISTS scraped_pages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	status_code INTEGER NOT NULL DEFAULT 0,
	fetched_at TEXT NOT NULL,
	err TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_scraped_pages_url ON scraped_pages(url);

-- Suggested additional prompts produced by 'salience expand'.
CREATE TABLE IF NOT EXISTS prompt_suggestions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	text TEXT NOT NULL,
	rationale TEXT NOT NULL DEFAULT '',
	accepted INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

-- Content briefs produced by 'salience brief'.
CREATE TABLE IF NOT EXISTS content_briefs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	prompt TEXT NOT NULL,
	body_markdown TEXT NOT NULL,
	source_run_id INTEGER REFERENCES runs(id) ON DELETE SET NULL,
	created_at TEXT NOT NULL
);

-- User-logged actions (PR campaigns, content publishes, etc.).
CREATE TABLE IF NOT EXISTS actions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	description TEXT NOT NULL,
	taken_at TEXT NOT NULL,
	applies_to_prompts TEXT NOT NULL DEFAULT '[]',
	notes TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_actions_project ON actions(project_id);
CREATE INDEX IF NOT EXISTS idx_actions_taken_at ON actions(taken_at);

-- Recurring bench schedules (driven by the server's background ticker).
CREATE TABLE IF NOT EXISTS schedules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	cron_expr TEXT NOT NULL,
	last_fired_at TEXT,
	next_fires_at TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);

-- URL watchers — fetch on a cadence, alert when content changes.
CREATE TABLE IF NOT EXISTS watchers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	url TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	interval_seconds INTEGER NOT NULL DEFAULT 86400,
	last_fetched_at TEXT,
	last_hash TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);

-- One row per watcher fetch — used to render the change history.
CREATE TABLE IF NOT EXISTS watcher_snapshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	watcher_id INTEGER NOT NULL REFERENCES watchers(id) ON DELETE CASCADE,
	fetched_at TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL,
	brand_present INTEGER NOT NULL DEFAULT 0,
	competitors_present INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_watcher_snapshots_watcher ON watcher_snapshots(watcher_id);

-- "Will this draft move the needle?" simulations.
CREATE TABLE IF NOT EXISTS simulations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	prompt TEXT NOT NULL,
	content_draft TEXT NOT NULL,
	baseline_rate REAL NOT NULL DEFAULT 0,
	simulated_rate REAL NOT NULL DEFAULT 0,
	delta REAL NOT NULL DEFAULT 0,
	n_samples INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

-- "What would I need to do to win this prompt?" responses.
CREATE TABLE IF NOT EXISTS advice (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	provider_name TEXT NOT NULL,
	model TEXT NOT NULL,
	asker_brand TEXT NOT NULL,
	winner_brand TEXT NOT NULL,
	prompt TEXT NOT NULL,
	advice TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_advice_run ON advice(run_id);
CREATE INDEX IF NOT EXISTS idx_advice_prompt ON advice(prompt);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Best-effort additive migrations for databases created by older builds.
	// SQLite rejects "ADD COLUMN" when the column already exists; we swallow
	// "duplicate column" errors and surface anything else.
	for _, alter := range []string{
		`ALTER TABLE mentions ADD COLUMN context TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mentions ADD COLUMN sentiment TEXT NOT NULL DEFAULT 'neutral'`,
		// projects support
		`ALTER TABLE runs ADD COLUMN project_id INTEGER`,
		// region-aware bench (v0.3): existing samples become "global".
		`ALTER TABLE samples ADD COLUMN region TEXT NOT NULL DEFAULT 'global'`,
		`ALTER TABLE projects ADD COLUMN regions_json TEXT NOT NULL DEFAULT '[]'`,
	} {
		if _, err := s.db.Exec(alter); err != nil &&
			!strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migrate: %s: %w", alter, err)
		}
	}
	return nil
}

// Run is one persisted execution of the bench command.
type Run struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt *time.Time
	BrandName  string
	Status     string
	ConfigJSON string
}

// StartRun records a new run row in the database and returns its id.
func (s *Store) StartRun(ctx context.Context, configJSON, brandName string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO runs(started_at, config_json, brand_name, status) VALUES(?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339Nano), configJSON, brandName, "running",
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun stamps the finished_at and status for an existing run.
func (s *Store) FinishRun(ctx context.Context, runID int64, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET finished_at=?, status=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), status, runID,
	)
	return err
}

// SampleRecord is the payload stored for a single LLM call.
type SampleRecord struct {
	RunID        int64
	Prompt       string
	ProviderName string
	ProviderKind string
	Model        string
	SampleIdx    int
	Region       string // e.g. "global", "jp", "us"; defaults to "global"
	ResponseText string
	Sources      any // serialized as JSON
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	Error        string
}

// MentionRecord is one row of detection output attached to a sample.
type MentionRecord struct {
	Brand     string
	Alias     string
	Where     string
	IsDomain  bool
	Context   string // sentence containing the hit (text) or "title — url" (source)
	Sentiment string // "positive" | "neutral" | "negative"
}

// SaveSample persists a sample plus its detected mentions atomically.
// It returns ErrAlreadyExists if a sample with the same (run, prompt, provider,
// sample_idx) tuple is already in the database, which is how resume works.
func (s *Store) SaveSample(ctx context.Context, sr SampleRecord, mentions []MentionRecord) error {
	srcJSON, err := json.Marshal(sr.Sources)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	region := sr.Region
	if region == "" {
		region = "global"
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO samples(run_id, prompt, provider_name, provider_kind, model, sample_idx, region, created_at,
			response_text, sources_json, input_tokens, output_tokens, cost_usd, error)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sr.RunID, sr.Prompt, sr.ProviderName, sr.ProviderKind, sr.Model, sr.SampleIdx, region,
		time.Now().UTC().Format(time.RFC3339Nano),
		sr.ResponseText, string(srcJSON), sr.InputTokens, sr.OutputTokens, sr.CostUSD, sr.Error,
	)
	if err != nil {
		if isUnique(err) {
			return ErrAlreadyExists
		}
		return err
	}
	sampleID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for _, m := range mentions {
		dom := 0
		if m.IsDomain {
			dom = 1
		}
		sentiment := m.Sentiment
		if sentiment == "" {
			sentiment = "neutral"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mentions(sample_id, brand, alias, where_found, is_domain, context, sentiment)
			 VALUES(?,?,?,?,?,?,?)`,
			sampleID, m.Brand, m.Alias, m.Where, dom, m.Context, sentiment,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrAlreadyExists is returned when a sample is already persisted; the runner
// uses this to skip completed work on resume.
var ErrAlreadyExists = errors.New("sample already exists")

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}

// CompletedKeys returns the set of (prompt, provider, region, sample_idx)
// tuples already present for a run, as a map keyed by EncodeKey.
func (s *Store) CompletedKeys(ctx context.Context, runID int64) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT prompt, provider_name, COALESCE(region, 'global'), sample_idx FROM samples WHERE run_id=? AND error=''`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var p, pn, region string
		var idx int
		if err := rows.Scan(&p, &pn, &region, &idx); err != nil {
			return nil, err
		}
		out[EncodeKey(p, pn, region, idx)] = struct{}{}
	}
	return out, rows.Err()
}

// EncodeKey produces the deterministic key used by CompletedKeys. Region
// is part of the key so the same (prompt, provider, sample) across two
// different regions is not collapsed.
func EncodeKey(prompt, providerName, region string, sampleIdx int) string {
	if region == "" {
		region = "global"
	}
	return fmt.Sprintf("%s\x1f%s\x1f%s\x1f%d", prompt, providerName, region, sampleIdx)
}

// LatestRunID returns the id of the most recent run or 0 when there are none.
func (s *Store) LatestRunID(ctx context.Context) (int64, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id FROM runs ORDER BY id DESC LIMIT 1`)
	var id int64
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// LatestUnfinishedRunID returns the id of the most recent run that is still
// marked "running" (i.e. a candidate to resume). 0 if no such run exists.
func (s *Store) LatestUnfinishedRunID(ctx context.Context) (int64, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id FROM runs WHERE status='running' ORDER BY id DESC LIMIT 1`)
	var id int64
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// GetRun returns metadata about a single run.
func (s *Store) GetRun(ctx context.Context, runID int64) (*Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, started_at, finished_at, brand_name, status, config_json FROM runs WHERE id=?`, runID)
	var r Run
	var started string
	var finished sql.NullString
	if err := row.Scan(&r.ID, &started, &finished, &r.BrandName, &r.Status, &r.ConfigJSON); err != nil {
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339Nano, started)
	r.StartedAt = t
	if finished.Valid {
		ft, _ := time.Parse(time.RFC3339Nano, finished.String)
		r.FinishedAt = &ft
	}
	return &r, nil
}

// SampleRow is a denormalized row returned by ListSamples for reporting.
type SampleRow struct {
	ID           int64
	Prompt       string
	ProviderName string
	ProviderKind string
	Model        string
	SampleIdx    int
	Region       string
	Error        string
	CostUSD      float64
	BrandsHit    []string // unique canonical brand names mentioned in this sample
}

// ListRuns returns the most recent runs (newest first), up to limit. Use
// limit <= 0 for "all runs". Loads metadata only, not samples.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	q := `SELECT id, started_at, finished_at, brand_name, status, config_json FROM runs ORDER BY id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q)
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

// ListMentionsForRun returns every detected mention in run, joined to its
// originating sample's prompt + provider for context.
func (s *Store) ListMentionsForRun(ctx context.Context, runID int64) ([]MentionDetail, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.brand, m.alias, m.where_found, m.is_domain, m.context, m.sentiment,
		       s.prompt, s.provider_name, s.sample_idx
		FROM mentions m JOIN samples s ON s.id = m.sample_id
		WHERE s.run_id = ?
		ORDER BY s.prompt, s.provider_name, s.sample_idx`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MentionDetail
	for rows.Next() {
		var d MentionDetail
		var isDom int
		if err := rows.Scan(&d.Brand, &d.Alias, &d.Where, &isDom, &d.Context, &d.Sentiment,
			&d.Prompt, &d.ProviderName, &d.SampleIdx); err != nil {
			return nil, err
		}
		d.IsDomain = isDom != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

// MentionDetail is a mention row joined with its sample context.
type MentionDetail struct {
	Brand        string
	Alias        string
	Where        string
	IsDomain     bool
	Context      string
	Sentiment    string
	Prompt       string
	ProviderName string
	SampleIdx    int
}

// ExplanationRecord is a single "why was this brand recommended" probe.
type ExplanationRecord struct {
	RunID           int64
	SourceSampleID  int64 // optional — 0 if not tied to a specific sample
	ProviderName    string
	Model           string
	AskedAboutBrand string
	Prompt          string
	Reasoning       string
	InputTokens     int
	OutputTokens    int
	CostUSD         float64
	Error           string
}

// InsertExplanation persists one explanation row.
func (s *Store) InsertExplanation(ctx context.Context, e ExplanationRecord) error {
	var src any
	if e.SourceSampleID > 0 {
		src = e.SourceSampleID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO explanations(run_id, source_sample_id, provider_name, model,
			asked_about_brand, prompt, reasoning, input_tokens, output_tokens, cost_usd, error, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.RunID, src, e.ProviderName, e.Model, e.AskedAboutBrand, e.Prompt, e.Reasoning,
		e.InputTokens, e.OutputTokens, e.CostUSD, e.Error,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// AdviceRecord is the LLM's answer to "what would I need to do to get
// recommended for this prompt?"
type AdviceRecord struct {
	RunID        int64
	ProviderName string
	Model        string
	AskerBrand   string
	WinnerBrand  string
	Prompt       string
	Advice       string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	Error        string
}

// InsertAdvice persists one advice row.
func (s *Store) InsertAdvice(ctx context.Context, a AdviceRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO advice(run_id, provider_name, model, asker_brand, winner_brand,
			prompt, advice, input_tokens, output_tokens, cost_usd, error, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.RunID, a.ProviderName, a.Model, a.AskerBrand, a.WinnerBrand, a.Prompt, a.Advice,
		a.InputTokens, a.OutputTokens, a.CostUSD, a.Error,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// ListExplanations returns every persisted explanation for a run, optionally
// filtered by brand. Returns rows newest-first.
func (s *Store) ListExplanations(ctx context.Context, runID int64, brand string) ([]ExplanationRecord, error) {
	q := `SELECT run_id, COALESCE(source_sample_id, 0), provider_name, model,
		asked_about_brand, prompt, reasoning, input_tokens, output_tokens, cost_usd, error
		FROM explanations WHERE run_id = ?`
	args := []any{runID}
	if brand != "" {
		q += " AND asked_about_brand = ?"
		args = append(args, brand)
	}
	q += " ORDER BY id DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExplanationRecord
	for rows.Next() {
		var e ExplanationRecord
		if err := rows.Scan(&e.RunID, &e.SourceSampleID, &e.ProviderName, &e.Model,
			&e.AskedAboutBrand, &e.Prompt, &e.Reasoning,
			&e.InputTokens, &e.OutputTokens, &e.CostUSD, &e.Error); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListAdvice returns persisted advice rows for a run.
func (s *Store) ListAdvice(ctx context.Context, runID int64) ([]AdviceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, provider_name, model, asker_brand, winner_brand,
		       prompt, advice, input_tokens, output_tokens, cost_usd, error
		FROM advice WHERE run_id = ? ORDER BY id DESC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdviceRecord
	for rows.Next() {
		var a AdviceRecord
		if err := rows.Scan(&a.RunID, &a.ProviderName, &a.Model, &a.AskerBrand, &a.WinnerBrand,
			&a.Prompt, &a.Advice, &a.InputTokens, &a.OutputTokens, &a.CostUSD, &a.Error); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SourcesJSONByRun returns a sampleID → raw sources_json string map for
// every non-errored sample in run. Useful for source-attribution analysis
// which needs the JSON intact (ListSamples doesn't carry it).
func (s *Store) SourcesJSONByRun(ctx context.Context, runID int64) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, sources_json FROM samples WHERE run_id = ? AND error = ''`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var src string
		if err := rows.Scan(&id, &src); err != nil {
			return nil, err
		}
		out[id] = src
	}
	return out, rows.Err()
}

// CountSamples returns (ok, errored) counts for one run.
func (s *Store) CountSamples(ctx context.Context, runID int64) (ok, errored int, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
		  SUM(CASE WHEN error='' THEN 1 ELSE 0 END) AS ok,
		  SUM(CASE WHEN error<>'' THEN 1 ELSE 0 END) AS errored
		FROM samples WHERE run_id = ?`, runID)
	var okN, errN sql.NullInt64
	if e := row.Scan(&okN, &errN); e != nil {
		return 0, 0, e
	}
	return int(okN.Int64), int(errN.Int64), nil
}

// ListSamples returns every sample for a run, with the deduplicated list of
// brands that hit it.
func (s *Store) ListSamples(ctx context.Context, runID int64) ([]SampleRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.prompt, s.provider_name, s.provider_kind, s.model, s.sample_idx,
		       COALESCE(s.region, 'global'), s.error, s.cost_usd
		FROM samples s WHERE s.run_id=? ORDER BY s.prompt, s.provider_name, s.region, s.sample_idx`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SampleRow
	byID := map[int64]int{}
	for rows.Next() {
		var r SampleRow
		if err := rows.Scan(&r.ID, &r.Prompt, &r.ProviderName, &r.ProviderKind, &r.Model, &r.SampleIdx,
			&r.Region, &r.Error, &r.CostUSD); err != nil {
			return nil, err
		}
		byID[r.ID] = len(out)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	mrows, err := s.db.QueryContext(ctx, `
		SELECT m.sample_id, m.brand FROM mentions m
		JOIN samples s ON s.id = m.sample_id WHERE s.run_id=?`, runID)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	seen := map[int64]map[string]bool{}
	for mrows.Next() {
		var sid int64
		var brand string
		if err := mrows.Scan(&sid, &brand); err != nil {
			return nil, err
		}
		if _, ok := seen[sid]; !ok {
			seen[sid] = map[string]bool{}
		}
		if !seen[sid][brand] {
			seen[sid][brand] = true
			idx, ok := byID[sid]
			if ok {
				out[idx].BrandsHit = append(out[idx].BrandsHit, brand)
			}
		}
	}
	return out, mrows.Err()
}
