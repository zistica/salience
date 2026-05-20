package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	ts := httptest.NewServer(handler)
	c := NewClient()
	// Tests should be cheap — keep the body cap small for cleaner assertions.
	c.BodyMax = 200
	return ts, c
}

func TestFetch_Basic(t *testing.T) {
	ts, c := newServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>
<head>
  <title>Best CRMs 2026</title>
  <meta name="description" content="A list of leading CRM products.">
</head>
<body>
  <h1>Best CRMs</h1>
  <p>Northwind is the leading choice. Contoso is close behind.</p>
  <script>var x = "ignore me";</script>
</body>
</html>`))
	})
	defer ts.Close()

	p := c.Fetch(context.Background(), ts.URL)
	if p.StatusCode != 200 {
		t.Errorf("expected 200, got %d", p.StatusCode)
	}
	if p.Title != "Best CRMs 2026" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Description != "A list of leading CRM products." {
		t.Errorf("description = %q", p.Description)
	}
	if !strings.Contains(p.Body, "Northwind is the leading choice") {
		t.Errorf("body missing expected text; got %q", p.Body)
	}
	if strings.Contains(p.Body, "ignore me") {
		t.Errorf("script content should be excluded from Body: %q", p.Body)
	}
}

func TestFetch_OGDescriptionFallback(t *testing.T) {
	ts, c := newServer(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head>
  <meta property="og:description" content="Via OpenGraph.">
</head><body>hi</body></html>`))
	})
	defer ts.Close()

	p := c.Fetch(context.Background(), ts.URL)
	if p.Description != "Via OpenGraph." {
		t.Errorf("og:description should populate Description; got %q", p.Description)
	}
}

func TestFetch_404(t *testing.T) {
	ts, c := newServer(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	defer ts.Close()

	p := c.Fetch(context.Background(), ts.URL)
	if p.StatusCode != 404 {
		t.Errorf("expected 404, got %d", p.StatusCode)
	}
	if p.Err == "" {
		t.Errorf("expected Err to be set on 404")
	}
}

func TestFetch_NetworkError(t *testing.T) {
	c := NewClient()
	// A port on localhost almost certain to be closed.
	p := c.Fetch(context.Background(), "http://127.0.0.1:1/no-such-thing")
	if p.Err == "" {
		t.Errorf("expected connection failure to populate Err")
	}
}

func TestFetch_BodyTruncation(t *testing.T) {
	long := strings.Repeat("salience ", 200) // ~1800 chars
	ts, c := newServer(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body><p>" + long + "</p></body></html>"))
	})
	defer ts.Close()
	c.BodyMax = 80
	p := c.Fetch(context.Background(), ts.URL)
	// "salience" is 8 runes (8 ASCII chars + a space); clip to 80 runes gives
	// substantially less than the full text. Just assert the truncation marker.
	if !strings.HasSuffix(p.Body, "…") {
		t.Errorf("expected ellipsis suffix on truncated body; got %q", p.Body)
	}
}

func TestFetchAll_BoundedConcurrency(t *testing.T) {
	// 3 URLs, parallel=2. We don't actually verify the parallelism limit
	// (timing tests are flaky); just ensure FetchAll returns aligned results.
	ts, c := newServer(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><head><title>" + r.URL.Path + "</title></head><body></body></html>"))
	})
	defer ts.Close()
	urls := []string{ts.URL + "/a", ts.URL + "/b", ts.URL + "/c"}
	pages := c.FetchAll(context.Background(), urls, 2)
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(pages))
	}
	for i, p := range pages {
		if p.Title != urls[i][len(ts.URL):] {
			t.Errorf("page %d title misaligned with input order: got %q, want %q",
				i, p.Title, urls[i][len(ts.URL):])
		}
	}
}
