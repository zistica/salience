package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ProfileRow mirrors a row in source_profiles. brand_hits_json is left
// as a raw JSON string; callers in the profiler package decode it back
// into the typed BrandHit slice. Keeping the JSON opaque at the store
// layer means new fields in the profiler's typed view don't require a
// schema migration.
type ProfileRow struct {
	ID               int64
	URL              string
	Domain           string
	FetchedAt        time.Time
	StatusCode       int
	HTMLLang         string
	Title            string
	Description      string
	WordCount        int
	HasSchemaProduct bool
	HasSchemaReview  bool
	HasSchemaArticle bool
	HasSchemaOrg     bool
	LastModified     string
	AuthorityScore   int
	PageKind         string
	BrandHitsJSON    string
	Err              string
}

// UpsertSourceProfile stores or refreshes a profile keyed on the URL.
// Only one row per URL is kept — historical fetches aren't needed for
// the anatomy view, only the freshest analysis.
func (s *Store) UpsertSourceProfile(ctx context.Context, p ProfileRow) (int64, error) {
	if p.FetchedAt.IsZero() {
		p.FetchedAt = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO source_profiles(
			url, domain, fetched_at, status_code, html_lang, title, description,
			word_count, has_schema_product, has_schema_review, has_schema_article,
			has_schema_org, last_modified, authority_score, page_kind,
			brand_hits_json, err
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(url) DO UPDATE SET
			domain=excluded.domain,
			fetched_at=excluded.fetched_at,
			status_code=excluded.status_code,
			html_lang=excluded.html_lang,
			title=excluded.title,
			description=excluded.description,
			word_count=excluded.word_count,
			has_schema_product=excluded.has_schema_product,
			has_schema_review=excluded.has_schema_review,
			has_schema_article=excluded.has_schema_article,
			has_schema_org=excluded.has_schema_org,
			last_modified=excluded.last_modified,
			authority_score=excluded.authority_score,
			page_kind=excluded.page_kind,
			brand_hits_json=excluded.brand_hits_json,
			err=excluded.err`,
		p.URL, p.Domain, p.FetchedAt.Format(time.RFC3339Nano), p.StatusCode,
		p.HTMLLang, p.Title, p.Description, p.WordCount,
		boolToInt(p.HasSchemaProduct), boolToInt(p.HasSchemaReview),
		boolToInt(p.HasSchemaArticle), boolToInt(p.HasSchemaOrg),
		p.LastModified, p.AuthorityScore, p.PageKind, p.BrandHitsJSON, p.Err,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetSourceProfile returns the cached row for url, or ErrNoProfile if
// none exists yet. The caller decides whether the row is fresh enough
// (TTL is policy, not storage).
var ErrNoProfile = errors.New("source profile not found")

func (s *Store) GetSourceProfile(ctx context.Context, url string) (*ProfileRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, url, domain, fetched_at, status_code, html_lang, title, description,
		       word_count, has_schema_product, has_schema_review, has_schema_article,
		       has_schema_org, last_modified, authority_score, page_kind,
		       brand_hits_json, err
		FROM source_profiles WHERE url = ?`, url)
	return scanProfileRow(row)
}

// GetSourceProfiles batches the lookup — one query per URL would be
// slow for an "Anatomy" panel that has 10–20 cited URLs per sample.
// Returns a map from URL to row; URLs without a cached profile are
// simply absent from the map.
func (s *Store) GetSourceProfiles(ctx context.Context, urls []string) (map[string]*ProfileRow, error) {
	out := make(map[string]*ProfileRow, len(urls))
	if len(urls) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(urls))
	args := make([]any, len(urls))
	for i, u := range urls {
		placeholders[i] = "?"
		args[i] = u
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, url, domain, fetched_at, status_code, html_lang, title, description,
		       word_count, has_schema_product, has_schema_review, has_schema_article,
		       has_schema_org, last_modified, authority_score, page_kind,
		       brand_hits_json, err
		FROM source_profiles WHERE url IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanProfileRowMulti(rows)
		if err != nil {
			return nil, err
		}
		out[p.URL] = p
	}
	return out, rows.Err()
}

// CitedDomainStat is one row of the "domain hit list" aggregate — for a
// given run, how often each domain was cited, plus a flag for whether
// the user's brand was found on those pages and how many distinct
// competitors were.
type CitedDomainStat struct {
	Domain          string
	Citations       int
	YouMentioned    bool
	CompetitorCount int
	PageKind        string
	AuthScore       int
	SampleURLs      []string // up to 3, for drill-down
}

// row scanners. A QueryRow returns a *sql.Row, which has a different
// shape than *sql.Rows; we have two small helpers to keep the column
// list defined once.

func scanProfileRow(r *sql.Row) (*ProfileRow, error) {
	var p ProfileRow
	var fetched string
	var hp, hr, ha, ho int
	err := r.Scan(&p.ID, &p.URL, &p.Domain, &fetched, &p.StatusCode, &p.HTMLLang,
		&p.Title, &p.Description, &p.WordCount, &hp, &hr, &ha, &ho,
		&p.LastModified, &p.AuthorityScore, &p.PageKind, &p.BrandHitsJSON, &p.Err)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoProfile
		}
		return nil, err
	}
	hydrateProfile(&p, fetched, hp, hr, ha, ho)
	return &p, nil
}

func scanProfileRowMulti(r *sql.Rows) (*ProfileRow, error) {
	var p ProfileRow
	var fetched string
	var hp, hr, ha, ho int
	if err := r.Scan(&p.ID, &p.URL, &p.Domain, &fetched, &p.StatusCode, &p.HTMLLang,
		&p.Title, &p.Description, &p.WordCount, &hp, &hr, &ha, &ho,
		&p.LastModified, &p.AuthorityScore, &p.PageKind, &p.BrandHitsJSON, &p.Err); err != nil {
		return nil, err
	}
	hydrateProfile(&p, fetched, hp, hr, ha, ho)
	return &p, nil
}

func hydrateProfile(p *ProfileRow, fetched string, hp, hr, ha, ho int) {
	if t, err := time.Parse(time.RFC3339Nano, fetched); err == nil {
		p.FetchedAt = t
	}
	p.HasSchemaProduct = hp != 0
	p.HasSchemaReview = hr != 0
	p.HasSchemaArticle = ha != 0
	p.HasSchemaOrg = ho != 0
}

