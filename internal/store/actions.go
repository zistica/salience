package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Action is one user-logged operational event — "published page X",
// "ran Reddit AMA", "Wikipedia article approved". Used by `salience diff`
// to correlate observed rate movements with what the team did between
// the two runs.
type Action struct {
	ID                int64
	ProjectID         int64
	Description       string
	TakenAt           time.Time
	AppliesToPrompts  []string
	Notes             string
	CreatedAt         time.Time
}

// InsertAction persists one action row.
func (s *Store) InsertAction(ctx context.Context, a Action) (int64, error) {
	prompts, _ := json.Marshal(a.AppliesToPrompts)
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO actions(project_id, description, taken_at, applies_to_prompts, notes, created_at)
		VALUES(?,?,?,?,?,?)`,
		a.ProjectID, a.Description,
		a.TakenAt.UTC().Format(time.RFC3339Nano),
		string(prompts), a.Notes,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListActions returns actions for a project, newest first.
func (s *Store) ListActions(ctx context.Context, projectID int64) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, description, taken_at, applies_to_prompts, notes, created_at
		FROM actions WHERE project_id=? ORDER BY taken_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanActions(rows)
}

// ActionsBetween returns actions taken between startISO and endISO (both
// inclusive). Used by `salience diff` overlay.
func (s *Store) ActionsBetween(ctx context.Context, projectID int64, startISO, endISO string) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, description, taken_at, applies_to_prompts, notes, created_at
		FROM actions WHERE project_id=? AND taken_at >= ? AND taken_at <= ?
		ORDER BY taken_at`, projectID, startISO, endISO)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanActions(rows)
}

// DeleteAction removes one action by id.
func (s *Store) DeleteAction(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM actions WHERE id=?`, id)
	return err
}

func scanActions(rows interface{ Next() bool; Scan(...any) error; Err() error; Close() error }) ([]Action, error) {
	var out []Action
	for rows.Next() {
		var a Action
		var taken, created, prompts string
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Description, &taken, &prompts, &a.Notes, &created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, taken); err == nil {
			a.TakenAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			a.CreatedAt = t
		}
		if strings.TrimSpace(prompts) != "" {
			_ = json.Unmarshal([]byte(prompts), &a.AppliesToPrompts)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
