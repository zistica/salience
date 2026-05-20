// Package scraper fetches HTML pages cited by LLMs and extracts their
// title, meta description, and main body text. It exists so other parts
// of the system can answer questions like "what does the page that the
// LLM is grounding against actually say about my competitor?" — without
// requiring the user to open each link.
package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Page is the normalized output of one fetch.
type Page struct {
	URL         string
	Title       string
	Description string // <meta name="description">
	Body        string // extracted visible text, capped
	StatusCode  int
	Err         string
	FetchedAt   time.Time
}

// Client wraps a *http.Client with sane defaults: timeout, browser-ish
// User-Agent so polite sites don't return 403s to a default-Go-UA fetcher.
type Client struct {
	HTTP   *http.Client
	UA     string
	BodyMax int // truncate extracted body to this many runes; 0 = 4000
}

// NewClient returns a Client with reasonable defaults.
func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 20 * time.Second},
		UA:      "salience/0.2 (+https://github.com/zistica/salience)",
		BodyMax: 4000,
	}
}

// Fetch retrieves url and returns a populated *Page. Network / HTTP errors
// are recorded on the Page.Err field rather than returned, so a single bad
// URL doesn't abort batch scrapes — the caller decides how to react.
func (c *Client) Fetch(ctx context.Context, url string) *Page {
	p := &Page{URL: url, FetchedAt: time.Now().UTC()}
	if c.BodyMax == 0 {
		c.BodyMax = 4000
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		p.Err = err.Error()
		return p
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		p.Err = err.Error()
		return p
	}
	defer resp.Body.Close()
	p.StatusCode = resp.StatusCode

	if resp.StatusCode >= 400 {
		p.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		// Best-effort still: try to read the body in case it's a useful
		// page that returns a non-2xx status.
	}

	// Cap the read to 4 MB so a giant page doesn't OOM us.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil && !errors.Is(err, io.EOF) {
		if p.Err == "" {
			p.Err = err.Error()
		}
		return p
	}

	doc, perr := html.Parse(strings.NewReader(string(body)))
	if perr != nil {
		if p.Err == "" {
			p.Err = "parse: " + perr.Error()
		}
		return p
	}
	extract(doc, p)
	p.Body = trimRunes(p.Body, c.BodyMax)
	return p
}

// FetchAll fetches urls with bounded concurrency. The slice it returns is
// in the same order as the input.
func (c *Client) FetchAll(ctx context.Context, urls []string, parallel int) []*Page {
	if parallel <= 0 {
		parallel = 4
	}
	out := make([]*Page, len(urls))
	sem := make(chan struct{}, parallel)
	done := make(chan int, len(urls))
	for i, u := range urls {
		i, u := i, u
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = c.Fetch(ctx, u)
			done <- i
		}()
	}
	for range urls {
		<-done
	}
	return out
}

// extract walks the HTML tree to pull title, meta description, and the
// concatenation of text nodes outside of `<script>`/`<style>`/`<nav>` etc.
func extract(n *html.Node, p *Page) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		switch tag {
		case "script", "style", "noscript", "nav", "footer", "form", "svg":
			return // skip subtree
		case "title":
			if p.Title == "" {
				p.Title = strings.TrimSpace(collectText(n))
			}
		case "meta":
			var name, content string
			for _, a := range n.Attr {
				al := strings.ToLower(a.Key)
				if al == "name" || al == "property" {
					name = strings.ToLower(a.Val)
				}
				if al == "content" {
					content = a.Val
				}
			}
			if (name == "description" || name == "og:description") && p.Description == "" {
				p.Description = strings.TrimSpace(content)
			}
		}
	case html.TextNode:
		t := strings.TrimSpace(n.Data)
		if t != "" {
			if p.Body != "" {
				p.Body += " "
			}
			p.Body += t
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extract(c, p)
	}
}

func collectText(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(collectText(c))
	}
	return b.String()
}

// trimRunes returns s capped at maxRunes; appends an ellipsis when it
// truncates so the rendered output makes it obvious.
func trimRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) == 0 {
		return s
	}
	n := 0
	for i := range s {
		if n == maxRunes {
			return s[:i] + " …"
		}
		n++
	}
	return s
}
