package store

import (
	"context"
	"time"
)

// PromptSuggestion is a candidate new prompt produced by `salience expand`.
// The user (CLI or dashboard) accepts/rejects them; accepted ones get
// folded into the project's active prompt list.
type PromptSuggestion struct {
	ID        int64
	ProjectID int64
	Text      string
	Rationale string
	Accepted  bool
	CreatedAt time.Time
}

// InsertPromptSuggestion persists a single candidate.
func (s *Store) InsertPromptSuggestion(ctx context.Context, p PromptSuggestion) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO prompt_suggestions(project_id, text, rationale, accepted, created_at)
		VALUES(?,?,?,?,?)`,
		p.ProjectID, p.Text, p.Rationale, boolToInt(p.Accepted),
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListPromptSuggestions returns all candidates for a project, newest first.
func (s *Store) ListPromptSuggestions(ctx context.Context, projectID int64) ([]PromptSuggestion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, text, rationale, accepted, created_at
		FROM prompt_suggestions WHERE project_id=? ORDER BY id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PromptSuggestion
	for rows.Next() {
		var p PromptSuggestion
		var accepted int
		var created string
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Text, &p.Rationale, &accepted, &created); err != nil {
			return nil, err
		}
		p.Accepted = accepted != 0
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			p.CreatedAt = t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPromptSuggestionAccepted toggles the accepted flag on a row.
func (s *Store) SetPromptSuggestionAccepted(ctx context.Context, id int64, accepted bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE prompt_suggestions SET accepted=? WHERE id=?`,
		boolToInt(accepted), id)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
