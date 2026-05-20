// Package detect identifies brand mentions inside model response text and
// inside the list of grounded sources a provider returns.
package detect

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/salience-cli/salience/internal/config"
)

// Source is a single grounded citation returned by a provider's web tool.
// Any of the fields may be empty; detection scans them all.
type Source struct {
	URL     string
	Title   string
	Snippet string
}

// Match describes one detected occurrence of a brand alias.
type Match struct {
	Brand    string // canonical brand name (the Brand.Name field)
	Alias    string // the alias text that actually hit
	Where    string // "text" or "source"
	Start    int    // byte offset in the haystack for text matches; -1 for source matches
	End      int    // exclusive byte end offset for text matches; -1 for source matches
	IsDomain bool
	Context  string // the sentence containing the hit (text matches) or the source URL (source matches); used for sentiment + display
}

// Result is the aggregate detection outcome for a single response.
type Result struct {
	// PerBrand reports whether each brand (keyed by canonical Name) was mentioned.
	PerBrand map[string]bool
	// Matches is the deduplicated list of underlying matches in deterministic order.
	Matches []Match
}

// Detect scans the response body and the grounded sources for any alias of any
// brand. It dedupes overlapping spans in text and avoids double-counting when
// multiple aliases hit the same source URL.
func Detect(text string, sources []Source, brands []config.Brand) Result {
	res := Result{PerBrand: make(map[string]bool, len(brands))}
	lowered := strings.ToLower(text)

	var spans []spanRange

	// Track which (brand, source-index) pairs we've already counted to avoid
	// double counting when, say, the brand name AND a domain alias both appear
	// in the same URL.
	seenSourceHit := map[[2]int]bool{}

	for bi, b := range brands {
		for _, alias := range aliasList(b) {
			al := strings.TrimSpace(alias)
			if al == "" {
				continue
			}
			isDomain := looksLikeDomain(al)
			// Aliases in spaceless scripts (CJK, Thai, etc.) match as
			// substring; they have no whitespace word boundaries to anchor
			// against. Latin / Cyrillic / Greek / Arabic / etc. use Unicode-
			// aware word boundaries.
			useSubstring := isDomain || !usesWordBoundaries(al)
			needle := strings.ToLower(al)

			// --- text scan ---
			if useSubstring {
				idx := 0
				for {
					i := strings.Index(lowered[idx:], needle)
					if i < 0 {
						break
					}
					start := idx + i
					end := start + len(needle)
					if !overlaps(spans, start, end) {
						spans = append(spans, spanRange{start, end})
						res.PerBrand[b.Name] = true
						res.Matches = append(res.Matches, Match{
							Brand: b.Name, Alias: al, Where: "text",
							Start: start, End: end, IsDomain: isDomain,
							Context: SentenceAround(text, start, end, 240),
						})
					}
					idx = end
				}
			} else {
				for _, m := range findWordBoundary(lowered, needle) {
					if !overlaps(spans, m[0], m[1]) {
						spans = append(spans, spanRange{m[0], m[1]})
						res.PerBrand[b.Name] = true
						res.Matches = append(res.Matches, Match{
							Brand: b.Name, Alias: al, Where: "text",
							Start: m[0], End: m[1], IsDomain: false,
							Context: SentenceAround(text, m[0], m[1], 240),
						})
					}
				}
			}

			// --- source scan ---
			for si, s := range sources {
				hay := strings.ToLower(s.URL + "\n" + s.Title + "\n" + s.Snippet)
				hit := false
				if useSubstring {
					hit = strings.Contains(hay, needle)
				} else {
					hit = len(findWordBoundary(hay, needle)) > 0
				}
				if hit {
					key := [2]int{bi, si}
					if !seenSourceHit[key] {
						seenSourceHit[key] = true
						res.PerBrand[b.Name] = true
						res.Matches = append(res.Matches, Match{
							Brand: b.Name, Alias: al, Where: "source",
							Start: -1, End: -1, IsDomain: isDomain,
							Context: strings.TrimSpace(s.Title + " — " + s.URL),
						})
					}
				}
			}
		}
	}

	sort.SliceStable(res.Matches, func(i, j int) bool {
		a, b := res.Matches[i], res.Matches[j]
		if a.Brand != b.Brand {
			return a.Brand < b.Brand
		}
		if a.Where != b.Where {
			return a.Where < b.Where
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.Alias < b.Alias
	})

	return res
}

func aliasList(b config.Brand) []string {
	out := make([]string, 0, 1+len(b.Aliases))
	if strings.TrimSpace(b.Name) != "" {
		out = append(out, b.Name)
	}
	out = append(out, b.Aliases...)
	return out
}

// looksLikeDomain heuristically classifies an alias as a domain. The spec asks
// us to treat aliases that "look like a domain (contain a .tld tail)" as
// substring matches; everything else is a word-boundary match.
func looksLikeDomain(s string) bool {
	dot := strings.LastIndexByte(s, '.')
	if dot < 0 || dot == len(s)-1 {
		return false
	}
	tail := s[dot+1:]
	if len(tail) < 2 || len(tail) > 24 {
		return false
	}
	for _, r := range tail {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	// Reject things like "v1.0" (numeric prefix) or "Mr." style — require the
	// portion before the dot to contain at least one letter.
	head := s[:dot]
	hasLetter := false
	for _, r := range head {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

// findWordBoundary returns [start,end) byte offsets of every occurrence of
// needle in haystack where both ends sit at a word boundary. Inputs must
// already be lowercased.
func findWordBoundary(haystack, needle string) [][2]int {
	if needle == "" {
		return nil
	}
	var out [][2]int
	idx := 0
	for {
		i := strings.Index(haystack[idx:], needle)
		if i < 0 {
			break
		}
		start := idx + i
		end := start + len(needle)
		if isBoundary(haystack, start, end) {
			out = append(out, [2]int{start, end})
		}
		idx = start + 1 // allow overlapping search; dedupe happens via span tracker
	}
	return out
}

// isBoundary checks that the candidate span [start,end) sits at a word
// boundary on both sides. A boundary is either:
//   - the outer rune is non-word (whitespace, punctuation, ASCII or fullwidth),
//     OR
//   - the outer and inner runes belong to different Unicode scripts (so
//     "Toyota" inside "Toyotaの車" is detected — the Latin→Hiragana
//     transition counts as a boundary even though both sides are "letters").
func isBoundary(s string, start, end int) bool {
	innerL, _ := utf8.DecodeRuneInString(s[start:end])
	innerR, _ := utf8.DecodeLastRuneInString(s[start:end])

	left := start == 0
	if !left {
		outer, _ := utf8.DecodeLastRuneInString(s[:start])
		left = !isWordRune(outer) || scriptOf(outer) != scriptOf(innerL)
	}
	right := end == len(s)
	if !right {
		outer, _ := utf8.DecodeRuneInString(s[end:])
		right = !isWordRune(outer) || scriptOf(outer) != scriptOf(innerR)
	}
	return left && right
}

// scriptOf returns a coarse name of the rune's Unicode script. It only
// distinguishes the scripts we care about for boundary detection; everything
// else is bucketed as "other".
func scriptOf(r rune) string {
	switch {
	case unicode.Is(unicode.Latin, r):
		return "latin"
	case unicode.Is(unicode.Cyrillic, r):
		return "cyrillic"
	case unicode.Is(unicode.Greek, r):
		return "greek"
	case unicode.Is(unicode.Han, r):
		return "han"
	case unicode.Is(unicode.Hiragana, r):
		return "hiragana"
	case unicode.Is(unicode.Katakana, r):
		return "katakana"
	case unicode.Is(unicode.Hangul, r):
		return "hangul"
	case unicode.Is(unicode.Arabic, r):
		return "arabic"
	case unicode.Is(unicode.Hebrew, r):
		return "hebrew"
	case unicode.Is(unicode.Thai, r):
		return "thai"
	case unicode.Is(unicode.Devanagari, r):
		return "devanagari"
	case unicode.IsDigit(r):
		return "digit"
	}
	return "other"
}

func isWordRune(r rune) bool {
	if r == utf8.RuneError || r == '_' {
		return r == '_'
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// usesWordBoundaries reports whether an alias should be matched with word-
// boundary anchoring. Returns false for aliases written in spaceless scripts
// where word boundaries are meaningless: CJK ideographs, Hiragana, Katakana,
// Hangul, Thai, Lao, Khmer, Myanmar. Those aliases fall back to substring
// matching — which is the only sensible default, since e.g. Japanese text
// usually has no whitespace around a brand name.
func usesWordBoundaries(alias string) bool {
	for _, r := range alias {
		if isSpacelessScript(r) {
			return false
		}
	}
	return true
}

func isSpacelessScript(r rune) bool {
	switch {
	case unicode.Is(unicode.Han, r),
		unicode.Is(unicode.Hiragana, r),
		unicode.Is(unicode.Katakana, r),
		unicode.Is(unicode.Hangul, r),
		unicode.Is(unicode.Thai, r),
		unicode.Is(unicode.Lao, r),
		unicode.Is(unicode.Khmer, r),
		unicode.Is(unicode.Myanmar, r):
		return true
	}
	return false
}

type spanRange struct{ start, end int }

func overlaps(spans []spanRange, a, b int) bool {
	for _, sp := range spans {
		if a < sp.end && sp.start < b {
			return true
		}
	}
	return false
}
