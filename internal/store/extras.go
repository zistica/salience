// Store CRUD for schedules, watchers (+ snapshots), and simulations.
// Kept together so the related tables and helpers live in one file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ---------- schedules ----------

// Schedule is one recurring bench for a project.
type Schedule struct {
	ID          int64
	ProjectID   int64
	CronExpr    string
	LastFired   *time.Time
	NextFires   time.Time
	Enabled     bool
	CreatedAt   time.Time
}

// InsertSchedule persists a new schedule.
func (s *Store) InsertSchedule(ctx context.Context, sc Schedule) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO schedules(project_id, cron_expr, last_fired_at, next_fires_at, enabled, created_at)
		VALUES(?,?,?,?,?,?)`,
		sc.ProjectID, sc.CronExpr,
		nullableTime(sc.LastFired),
		sc.NextFires.UTC().Format(time.RFC3339Nano),
		boolToInt(sc.Enabled),
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListSchedules returns all schedules across projects (used by the server's
// ticker) or for a single project when projectID > 0.
func (s *Store) ListSchedules(ctx context.Context, projectID int64) ([]Schedule, error) {
	var rows *sql.Rows
	var err error
	if projectID > 0 {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, project_id, cron_expr, last_fired_at, next_fires_at, enabled, created_at
			FROM schedules WHERE project_id=? ORDER BY id DESC`, projectID)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, project_id, cron_expr, last_fired_at, next_fires_at, enabled, created_at
			FROM schedules ORDER BY id DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var s Schedule
		var lastF sql.NullString
		var nextF, created string
		var en int
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.CronExpr, &lastF, &nextF, &en, &created); err != nil {
			return nil, err
		}
		s.Enabled = en != 0
		if t, err := time.Parse(time.RFC3339Nano, nextF); err == nil {
			s.NextFires = t
		}
		if lastF.Valid {
			if t, err := time.Parse(time.RFC3339Nano, lastF.String); err == nil {
				s.LastFired = &t
			}
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			s.CreatedAt = t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateScheduleFired stamps the last+next fire times after a run completes.
func (s *Store) UpdateScheduleFired(ctx context.Context, id int64, last, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE schedules SET last_fired_at=?, next_fires_at=? WHERE id=?`,
		last.UTC().Format(time.RFC3339Nano),
		next.UTC().Format(time.RFC3339Nano),
		id)
	return err
}

// DeleteSchedule removes a schedule.
func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id=?`, id)
	return err
}

// ---------- watchers ----------

// Watcher is a URL we periodically refetch to track content changes.
type Watcher struct {
	ID              int64
	ProjectID       int64
	URL             string
	Label           string
	IntervalSeconds int
	LastFetchedAt   *time.Time
	LastHash        string
	Enabled         bool
	CreatedAt       time.Time
}

// InsertWatcher persists a watcher.
func (s *Store) InsertWatcher(ctx context.Context, w Watcher) (int64, error) {
	if w.IntervalSeconds <= 0 {
		w.IntervalSeconds = 86400
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO watchers(project_id, url, label, interval_seconds, last_fetched_at, last_hash, enabled, created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		w.ProjectID, w.URL, w.Label, w.IntervalSeconds,
		nullableTime(w.LastFetchedAt), w.LastHash, boolToInt(w.Enabled),
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListWatchers returns watchers for a project (or all if projectID == 0).
func (s *Store) ListWatchers(ctx context.Context, projectID int64) ([]Watcher, error) {
	var rows *sql.Rows
	var err error
	if projectID > 0 {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, project_id, url, label, interval_seconds, last_fetched_at, last_hash, enabled, created_at
			FROM watchers WHERE project_id=? ORDER BY id DESC`, projectID)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, project_id, url, label, interval_seconds, last_fetched_at, last_hash, enabled, created_at
			FROM watchers ORDER BY id DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Watcher
	for rows.Next() {
		var w Watcher
		var last sql.NullString
		var created string
		var en int
		if err := rows.Scan(&w.ID, &w.ProjectID, &w.URL, &w.Label,
			&w.IntervalSeconds, &last, &w.LastHash, &en, &created); err != nil {
			return nil, err
		}
		w.Enabled = en != 0
		if last.Valid {
			if t, err := time.Parse(time.RFC3339Nano, last.String); err == nil {
				w.LastFetchedAt = &t
			}
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			w.CreatedAt = t
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWatcher removes a watcher and (via cascade) its snapshots.
func (s *Store) DeleteWatcher(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM watchers WHERE id=?`, id)
	return err
}

// WatcherSnapshot is one stored fetch of a watched URL.
type WatcherSnapshot struct {
	ID                  int64
	WatcherID           int64
	FetchedAt           time.Time
	Title               string
	Body                string
	ContentHash         string
	BrandPresent        bool
	CompetitorsPresent  int
}

// InsertWatcherSnapshot persists a fetch.
func (s *Store) InsertWatcherSnapshot(ctx context.Context, sn WatcherSnapshot) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO watcher_snapshots(watcher_id, fetched_at, title, body, content_hash, brand_present, competitors_present)
		VALUES(?,?,?,?,?,?,?)`,
		sn.WatcherID,
		time.Now().UTC().Format(time.RFC3339Nano),
		sn.Title, sn.Body, sn.ContentHash,
		boolToInt(sn.BrandPresent), sn.CompetitorsPresent)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateWatcherFetched bumps the last_fetched_at + last_hash on the watcher.
func (s *Store) UpdateWatcherFetched(ctx context.Context, watcherID int64, hash string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE watchers SET last_fetched_at=?, last_hash=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), hash, watcherID)
	return err
}

// ListWatcherSnapshots returns snapshots for one watcher, newest first.
func (s *Store) ListWatcherSnapshots(ctx context.Context, watcherID int64, limit int) ([]WatcherSnapshot, error) {
	q := `SELECT id, watcher_id, fetched_at, title, body, content_hash, brand_present, competitors_present
	      FROM watcher_snapshots WHERE watcher_id=? ORDER BY id DESC`
	args := []any{watcherID}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WatcherSnapshot
	for rows.Next() {
		var sn WatcherSnapshot
		var fetched string
		var brand, comps int
		if err := rows.Scan(&sn.ID, &sn.WatcherID, &fetched, &sn.Title, &sn.Body,
			&sn.ContentHash, &brand, &comps); err != nil {
			return nil, err
		}
		sn.BrandPresent = brand != 0
		sn.CompetitorsPresent = comps
		if t, err := time.Parse(time.RFC3339Nano, fetched); err == nil {
			sn.FetchedAt = t
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// ---------- simulations ----------

// Simulation is one "would publishing this content move the needle?" test.
type Simulation struct {
	ID            int64
	ProjectID     int64
	Prompt        string
	ContentDraft  string
	BaselineRate  float64
	SimulatedRate float64
	Delta         float64
	NSamples      int
	CreatedAt     time.Time
}

// InsertSimulation persists a simulation result.
func (s *Store) InsertSimulation(ctx context.Context, sm Simulation) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO simulations(project_id, prompt, content_draft, baseline_rate, simulated_rate, delta, n_samples, created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		sm.ProjectID, sm.Prompt, sm.ContentDraft,
		sm.BaselineRate, sm.SimulatedRate, sm.Delta, sm.NSamples,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListSimulations returns simulations for a project, newest first.
func (s *Store) ListSimulations(ctx context.Context, projectID int64) ([]Simulation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, prompt, content_draft, baseline_rate, simulated_rate, delta, n_samples, created_at
		FROM simulations WHERE project_id=? ORDER BY id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Simulation
	for rows.Next() {
		var sm Simulation
		var created string
		if err := rows.Scan(&sm.ID, &sm.ProjectID, &sm.Prompt, &sm.ContentDraft,
			&sm.BaselineRate, &sm.SimulatedRate, &sm.Delta, &sm.NSamples, &created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			sm.CreatedAt = t
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// ---------- helpers ----------

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// ensure unused-import check passes when only some helpers fire
var _ = errors.New
