package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salience-cli/salience/internal/config"
)

// runDiagnosisRules turns the raw Anatomy data into a small ordered
// list of human-readable findings + concrete actions. The rules below
// are deliberately conservative: each one looks for a single, clearly
// recognisable pattern and emits at most one Diagnosis. Multiple rules
// can fire on the same sample, but we cap the output so the UI doesn't
// drown the user.
//
// Severity scale:
//   - "critical": you weren't mentioned at all in the answer.
//   - "warn":     you were mentioned but losing ground in a fixable way.
//   - "info":     observation worth noting, no urgent action.
//
// Adding a rule = adding a function, registering it in the rules list.
func runDiagnosisRules(a *SampleAnatomy, userBrand string, competitors []config.Brand) []Diagnosis {
	var out []Diagnosis
	for _, fn := range rules {
		if d := fn(a, userBrand, competitors); d != nil {
			out = append(out, *d)
		}
	}
	// Stable order: critical → warn → info, then by title.
	rank := map[string]int{"critical": 0, "warn": 1, "info": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].Title < out[j].Title
	})
	// Hard cap — the UI shows ~4 cards cleanly; more is noise.
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

type ruleFn func(a *SampleAnatomy, user string, competitors []config.Brand) *Diagnosis

var rules = []ruleFn{
	ruleMissingFromAllCitedDomains,
	ruleCompetitorOwnsCitedAggregator,
	ruleCompetitorOwnsCitedBrandPage,
	ruleNoSchemaProductWhileWinnersHave,
	ruleLanguageMismatch,
	ruleNoCitationsAtAll,
	ruleNegativeSentimentOnSource,
}

// ruleMissingFromAllCitedDomains fires when the LLM cited at least two
// distinct profiled pages and the user's brand was on none of them. This
// is the single biggest pattern: "you're not on any source the LLM
// trusts, so it can't pick you."
func ruleMissingFromAllCitedDomains(a *SampleAnatomy, user string, _ []config.Brand) *Diagnosis {
	if user == "" {
		return nil
	}
	profiled := 0
	missing := 0
	doms := map[string]bool{}
	for _, s := range a.Sources {
		if s.Pending || s.StatusCode == 0 {
			continue
		}
		profiled++
		if !s.UserPresent {
			missing++
			doms[s.Domain] = true
		}
	}
	if profiled == 0 || missing < profiled {
		return nil
	}
	names := keysSorted(doms)
	if len(names) > 3 {
		names = names[:3]
	}
	return &Diagnosis{
		Severity: "critical",
		Title:    "You're missing from every website AI quoted",
		Detail: fmt.Sprintf("AI quoted %d website(s) to answer this question, and your brand wasn't on any of them. The notable ones: %s.",
			profiled, strings.Join(names, ", ")),
		Action: "Get your brand listed on at least one of those websites. Even a single product page or a customer review there will start changing AI's answers within a few weeks.",
	}
}

// ruleCompetitorOwnsCitedAggregator looks for review aggregator pages
// where the competitor is present and the user isn't. This is the
// "cosme.net" pattern from the JP demo: the aggregator is the LLM's
// canonical reference and missing-from-it is fatal.
func ruleCompetitorOwnsCitedAggregator(a *SampleAnatomy, user string, _ []config.Brand) *Diagnosis {
	for _, s := range a.Sources {
		if s.PageKind != "review_aggregator" {
			continue
		}
		if s.UserPresent {
			continue
		}
		// At least one competitor visible.
		var comps []string
		for _, h := range s.BrandHits {
			if h.IsUser {
				continue
			}
			comps = append(comps, h.Brand)
		}
		if len(comps) == 0 {
			continue
		}
		sort.Strings(comps)
		if len(comps) > 3 {
			comps = comps[:3]
		}
		return &Diagnosis{
			Severity: "critical",
			Title:    "Competitors are on a review site you're missing from",
			Detail: fmt.Sprintf("AI quoted %s, a review site that lists %s but not you.",
				s.Domain, strings.Join(comps, ", ")),
			Action: fmt.Sprintf("Get your brand listed on %s. AI treats review sites as proof that a brand exists in this category — being absent reads like \"doesn't exist\".", s.Domain),
		}
	}
	return nil
}

// ruleCompetitorOwnsCitedBrandPage fires when the LLM cited a competitor's
// own marketing page. That's the strongest possible signal that the
// competitor "owns" this query in the LLM's mind. The fix is either
// to publish counter-content on a third-party site the LLM also trusts,
// or to make your own brand page more discoverable (sitemap, schema).
func ruleCompetitorOwnsCitedBrandPage(a *SampleAnatomy, user string, _ []config.Brand) *Diagnosis {
	for _, s := range a.Sources {
		if s.PageKind != "brand_own" {
			continue
		}
		if s.UserPresent {
			continue
		}
		// Find the brand the page belongs to (the brand with the most
		// hits on the page is almost always the page owner).
		var owner string
		var ownerCount int
		for _, h := range s.BrandHits {
			if h.IsUser {
				continue
			}
			if h.Count > ownerCount {
				owner = h.Brand
				ownerCount = h.Count
			}
		}
		if owner == "" {
			continue
		}
		return &Diagnosis{
			Severity: "critical",
			Title:    fmt.Sprintf("AI is using %s's own website", owner),
			Detail: fmt.Sprintf("AI is quoting %s — %s's own marketing page — to answer this question. That's a strong signal AI sees %s as the answer here.",
				s.Domain, owner, owner),
			Action: fmt.Sprintf("Publish a similar %s product page on your own website. Make sure it's well-structured (proper page title, product description, reviews if you have them) so AI can read and quote it the same way.", strings.SplitN(a.RegionLabel, " ", 2)[0]),
		}
	}
	return nil
}

// ruleNoSchemaProductWhileWinnersHave fires when the cited pages widely
// expose schema.org/Product but we have no evidence the user's pages do.
// We can only know the *cited* pages' markup, but the asymmetry alone
// is a strong hint: structured data is a cheap, repeatable win.
func ruleNoSchemaProductWhileWinnersHave(a *SampleAnatomy, _ string, _ []config.Brand) *Diagnosis {
	with := 0
	total := 0
	for _, s := range a.Sources {
		if s.Pending || s.StatusCode == 0 {
			continue
		}
		total++
		if s.HasSchemaProduct {
			with++
		}
	}
	if total < 2 || with == 0 {
		return nil
	}
	if float64(with)/float64(total) < 0.5 {
		return nil
	}
	return &Diagnosis{
		Severity: "warn",
		Title:    "AI prefers websites with well-structured product pages",
		Detail: fmt.Sprintf("%d of the %d websites AI quoted have proper product page structure (price, description, reviews tagged in a way AI can read easily). Yours might not.",
			with, total),
		Action: "Ask your dev team to add product page \"structured data\" (schema.org/Product, Review, AggregateRating) to your product pages. It's a one-time change that makes AI much more likely to quote you.",
	}
}

// ruleLanguageMismatch fires when the answer is for a non-Global region
// but the cited pages aren't in the regional language — that means the
// LLM didn't have strong regional sources, which is an opportunity for
// the user to publish in that language and dominate.
func ruleLanguageMismatch(a *SampleAnatomy, _ string, _ []config.Brand) *Diagnosis {
	if a.RegionCode == "" || a.RegionCode == "global" {
		return nil
	}
	expectedLang := regionLangHint(a.RegionCode)
	if expectedLang == "" {
		return nil
	}
	matched := 0
	total := 0
	for _, s := range a.Sources {
		if s.HTMLLang == "" {
			continue
		}
		total++
		if strings.HasPrefix(s.HTMLLang, expectedLang) {
			matched++
		}
	}
	if total < 2 {
		return nil
	}
	if float64(matched)/float64(total) >= 0.5 {
		return nil
	}
	return &Diagnosis{
		Severity: "warn",
		Title:    fmt.Sprintf("AI couldn't find many %s-language pages to quote", a.RegionLabel),
		Detail: fmt.Sprintf("Only %d of the %d websites AI quoted are in the local language. AI is reaching for whatever it can find — that's a gap you can fill.",
			matched, total),
		Action: fmt.Sprintf("Publish content in %s for this market. Local-language pages will be picked over English ones every time when someone asks AI from this region.", a.RegionLabel),
	}
}

// ruleNoCitationsAtAll covers the case where the LLM answered from its
// internal weights with no web grounding. Nothing the user does to web
// content can change that answer — they need brand presence in the
// training corpus directly.
func ruleNoCitationsAtAll(a *SampleAnatomy, _ string, _ []config.Brand) *Diagnosis {
	if len(a.Sources) > 0 {
		return nil
	}
	return &Diagnosis{
		Severity: "info",
		Title:    "AI answered from memory, not from any website",
		Detail:   "AI didn't search the web for this one — it answered from what it already \"knows\". That means tweaking your website pages won't change this answer.",
		Action:   "Build longer-term presence that gets into AI's training: Wikipedia article, press coverage on big outlets, well-structured product pages that get indexed widely. These take longer but feed the next version of AI.",
	}
}

// ruleNegativeSentimentOnSource fires when the user IS mentioned on a
// cited page but with majority-negative sentiment. That's an active
// liability the user can address directly.
func ruleNegativeSentimentOnSource(a *SampleAnatomy, user string, _ []config.Brand) *Diagnosis {
	if user == "" {
		return nil
	}
	for _, s := range a.Sources {
		for _, h := range s.BrandHits {
			if h.Brand != user {
				continue
			}
			if h.Negative > h.Positive && h.Negative > 0 {
				return &Diagnosis{
					Severity: "warn",
					Title:    "A website AI quotes is criticising you",
					Detail: fmt.Sprintf("%s mentions you %d time(s), mostly negatively. AI is reading those words when answering this question.",
						s.Domain, h.Count),
					Action: fmt.Sprintf("Reach out to %s — respond to the criticism, share a fix or a customer success story, ask for an updated review. Pages with constructive updates get quoted by AI far more often than purely negative ones.", s.Domain),
				}
			}
		}
	}
	return nil
}

// keysSorted returns the sorted keys of a string-keyed map. Tiny helper
// so the rules don't repeat the loop.
func keysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// regionLangHint maps the region code we ship in REGION_PRESETS to the
// expected document language. Same set as the dashboard's region menu
// so the rule stays in sync with the UI. Returns "" when the region
// doesn't have a single dominant language.
func regionLangHint(code string) string {
	switch code {
	case "jp":
		return "ja"
	case "kr":
		return "ko"
	case "de":
		return "de"
	case "fr":
		return "fr"
	case "br":
		return "pt"
	case "mx":
		return "es"
	case "id":
		return "id"
	case "us", "uk":
		return "en"
	}
	return ""
}
