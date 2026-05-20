package detect

import (
	"strings"
	"unicode"
)

// Sentiment is a three-way classification of how a brand was mentioned.
// "positive" means recommendation / endorsement; "negative" means warning /
// criticism; "neutral" means descriptive or ambiguous.
type Sentiment string

const (
	SentimentPositive Sentiment = "positive"
	SentimentNeutral  Sentiment = "neutral"
	SentimentNegative Sentiment = "negative"
)

// marker is a weighted keyword that pulls a sentence toward positive or
// negative. Markers are matched whole-word against the sentence (case-
// insensitive) for Latin scripts, and as substring for CJK scripts.
type marker struct {
	word   string
	weight int
}

// positiveMarkers — English plus a small Japanese set. To add another
// language, append entries; matching is locale-agnostic at the rune level.
var positiveMarkers = []marker{
	// English
	{"best", 3}, {"top", 2}, {"winner", 3}, {"wins", 2}, {"recommend", 3},
	{"recommended", 3}, {"leading", 2}, {"great", 2}, {"excellent", 3},
	{"favorite", 3}, {"preferred", 3}, {"ideal", 2}, {"strong", 1}, {"solid", 1},
	{"love", 2}, {"amazing", 2}, {"perfect", 2}, {"superior", 3},
	{"go with", 3}, {"would pick", 3}, {"first choice", 3},
	// Japanese
	{"おすすめ", 3}, {"最高", 3}, {"ベスト", 3}, {"優れた", 2}, {"優秀", 2},
	{"良い", 1}, {"好き", 2}, {"一番", 2}, {"勝者", 2},
	// Spanish / French / German — small starter set
	{"mejor", 3}, {"recomiendo", 3}, {"excelente", 2},
	{"meilleur", 3}, {"recommande", 3},
	{"beste", 3}, {"empfehle", 3},
}

var negativeMarkers = []marker{
	// English
	{"worst", 3}, {"avoid", 3}, {"skip", 2}, {"weak", 2}, {"weaker", 2},
	{"inferior", 3}, {"poor", 2}, {"subpar", 3}, {"issues", 1}, {"problems", 1},
	{"slow", 1}, {"buggy", 2}, {"unreliable", 2}, {"warning", 2}, {"dated", 1},
	{"clunky", 2}, {"limited", 1}, {"less than", 1}, {"not recommend", 4},
	// Japanese
	{"避け", 3}, {"悪い", 2}, {"最悪", 3}, {"ダメ", 2}, {"問題", 1},
	{"非推奨", 3}, {"おすすめしない", 4},
	// Spanish / French / German
	{"peor", 3}, {"evitar", 3}, {"pire", 3}, {"éviter", 3},
	{"schlecht", 2}, {"vermeiden", 3},
}

// Classify scores the sentiment of context as it relates to alias. It uses
// weighted keyword markers in English plus a small set of common
// translations. The score threshold for "positive"/"negative" is ±2 so a
// single weak marker doesn't flip the label.
//
// Known limitations:
//   - English-style negation ("not the best" → still classified positive) is
//     not handled beyond the special-case marker "not recommend".
//   - Sarcasm and irony are not detected.
//   - For languages without keyword coverage the result will usually be
//     "neutral"; add markers above to extend.
func Classify(context, alias string) Sentiment {
	if context == "" {
		return SentimentNeutral
	}
	low := strings.ToLower(context)
	pos := scoreMarkers(low, positiveMarkers)
	neg := scoreMarkers(low, negativeMarkers)
	score := pos - neg

	switch {
	case score >= 2:
		return SentimentPositive
	case score <= -2:
		return SentimentNegative
	default:
		return SentimentNeutral
	}
}

func scoreMarkers(haystack string, ms []marker) int {
	total := 0
	for _, m := range ms {
		if isLatinMarker(m.word) {
			// Whole-word match for Latin markers.
			total += m.weight * countWholeWord(haystack, m.word)
		} else {
			// Substring match for CJK and other markers without word boundaries.
			total += m.weight * strings.Count(haystack, m.word)
		}
	}
	return total
}

// isLatinMarker reports whether the marker is in a script that uses
// whitespace word boundaries.
func isLatinMarker(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && isSpacelessScript(r) {
			return false
		}
	}
	return true
}

// countWholeWord returns the number of whole-word occurrences of needle in
// haystack. Both should be lowercased.
func countWholeWord(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	n := 0
	idx := 0
	for {
		i := strings.Index(haystack[idx:], needle)
		if i < 0 {
			return n
		}
		start := idx + i
		end := start + len(needle)
		if isBoundary(haystack, start, end) {
			n++
		}
		idx = start + 1
	}
}
