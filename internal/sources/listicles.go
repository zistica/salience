package sources

import (
	"regexp"
	"strings"
)

// listicleSignals matches titles that look like a "best of" / "top N" list —
// the kind of page LLMs disproportionately reach for when asked for
// recommendations.
var listicleSignals = regexp.MustCompile(
	`(?i)\b(top|best|leading|greatest)\s+\d*\s*\w*|in\s+(20\d\d|next\s+year)|comparison|alternatives?\s+to|review\s*-?\s*round\s*-?\s*up|guide\s+to`)

// IsListicle classifies a scraped page as a listicle from its title.
// Heuristic — not perfect, but tightly aligned with what actually gets
// cited by LLMs.
func IsListicle(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return false
	}
	return listicleSignals.MatchString(t)
}

// PageRow is one row of the page-level gap report.
type PageRow struct {
	URL                string
	Title              string
	Description        string
	MentionsCompetitors []string
	MissingBrand       bool
}

// PageGap finds listicle pages cited in this run whose body mentions any
// competitor but not the user's brand. The caller provides the scraped page
// content + the brand/competitor lists; the function returns a sorted list
// of actionable page-level to-dos.
type ScrapedPageInput struct {
	URL         string
	Title       string
	Description string
	Body        string
}

// PageGapAnalysis returns the actionable page-level gap rows.
func PageGapAnalysis(pages []ScrapedPageInput, userBrand string, competitors []string) []PageRow {
	if userBrand == "" {
		return nil
	}
	lowerUser := strings.ToLower(userBrand)
	lowerComps := make([]string, len(competitors))
	for i, c := range competitors {
		lowerComps[i] = strings.ToLower(c)
	}

	var out []PageRow
	for _, p := range pages {
		if !IsListicle(p.Title) {
			continue
		}
		hay := strings.ToLower(p.Title + " " + p.Description + " " + p.Body)
		userPresent := strings.Contains(hay, lowerUser)
		var hits []string
		for i, c := range lowerComps {
			if c == "" {
				continue
			}
			if strings.Contains(hay, c) {
				hits = append(hits, competitors[i])
			}
		}
		// Only interesting if competitors are there but the user isn't.
		if len(hits) > 0 && !userPresent {
			out = append(out, PageRow{
				URL:                p.URL,
				Title:              p.Title,
				Description:        p.Description,
				MentionsCompetitors: hits,
				MissingBrand:       true,
			})
		}
	}
	return out
}
