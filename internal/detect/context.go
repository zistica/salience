package detect

import (
	"strings"
	"unicode/utf8"
)

// sentenceEnders is the set of code points that terminate a sentence.
// Includes ASCII (. ! ? \n) and CJK fullwidth / Japanese punctuation
// (。！？．).
var sentenceEnders = map[rune]bool{
	'.':  true, // U+002E ASCII full stop
	'!':  true, // U+0021 ASCII exclamation
	'?':  true, // U+003F ASCII question
	'\n': true,
	'。':  true, // U+3002 ideographic full stop
	'！':  true, // U+FF01 fullwidth exclamation
	'？':  true, // U+FF1F fullwidth question
	'．':  true, // U+FF0E fullwidth full stop
}

// SentenceAround returns the sentence containing the byte range [start,end)
// inside body. Sentence boundaries are detected on ASCII and CJK punctuation
// (. ! ? 。 ！ ？ and newline). The result is trimmed and capped at maxLen
// runes so absurdly long sentences don't blow up the report. If start/end are
// out of range it falls back to the entire body, trimmed.
func SentenceAround(body string, start, end, maxLen int) string {
	if start < 0 || end > len(body) || start > end {
		return clipRunes(strings.TrimSpace(body), maxLen)
	}
	// Walk left until we find a sentence ender or the start of the string.
	left := start
	for left > 0 {
		r, sz := utf8.DecodeLastRuneInString(body[:left])
		if sentenceEnders[r] {
			break
		}
		left -= sz
	}
	// Walk right until we find a sentence ender or end of string.
	right := end
	for right < len(body) {
		r, sz := utf8.DecodeRuneInString(body[right:])
		if sentenceEnders[r] {
			right += sz // include the punctuation
			break
		}
		right += sz
	}
	out := strings.TrimSpace(body[left:right])
	return clipRunes(out, maxLen)
}

// clipRunes returns s capped at maxLen runes, with an ellipsis if truncated.
// maxLen<=0 disables truncation.
func clipRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	n := 0
	for i := range s {
		if n == maxLen {
			return s[:i] + "…"
		}
		n++
	}
	return s
}
