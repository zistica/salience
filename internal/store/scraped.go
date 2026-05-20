package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ScrapedPage is one fetched URL with its extracted content.
type ScrapedPage struct {
	ID          int64
	URL         string
	Title       string
	Description string
	Body        string
	StatusCode  int
	Err         string
	FetchedAt   time.Time
}

// UpsertScrapedPage stores a fresh fetch, replacing the previous content
// for the same URL. Each URL keeps exactly one row — historical content
// for watchers lives in watcher_snapshots instead.
func (s *Store) UpsertScrapedPage(ctx context.Context, p ScrapedPage) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO scraped_pages(url, title, description, body, status_code, fetched_at, err)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(url) DO UPDATE SET
			title=excluded.title,
			description=excluded.description,
			body=excluded.body,
			status_code=excluded.status_code,
			fetched_at=excluded.fetched_at,
			err=excluded.err`,
		p.URL, p.Title, p.Description, p.Body, p.StatusCode,
		time.Now().UTC().Format(time.RFC3339Nano), p.Err)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetScrapedPage returns the cached row for url, or ErrNotFound.
func (s *Store) GetScrapedPage(ctx context.Context, url string) (*ScrapedPage, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, url, title, description, body, status_code, fetched_at, err
		FROM scraped_pages WHERE url=?`, url)
	var p ScrapedPage
	var fetched string
	if err := row.Scan(&p.ID, &p.URL, &p.Title, &p.Description,
		&p.Body, &p.StatusCode, &fetched, &p.Err); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("scraped page not found")
		}
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339Nano, fetched); err == nil {
		p.FetchedAt = t
	}
	return &p, nil
}

// ListScrapedPages returns every cached scrape, newest first.
func (s *Store) ListScrapedPages(ctx context.Context, limit int) ([]ScrapedPage, error) {
	q := `SELECT id, url, title, description, body, status_code, fetched_at, err
	      FROM scraped_pages ORDER BY id DESC`
	if limit > 0 {
		q += " LIMIT ?"
	}
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.QueryContext(ctx, q, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScrapedPage
	for rows.Next() {
		var p ScrapedPage
		var fetched string
		if err := rows.Scan(&p.ID, &p.URL, &p.Title, &p.Description,
			&p.Body, &p.StatusCode, &fetched, &p.Err); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, fetched); err == nil {
			p.FetchedAt = t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
