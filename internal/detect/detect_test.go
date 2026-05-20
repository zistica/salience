package detect

import (
	"testing"

	"github.com/salience-cli/salience/internal/config"
)

func brands() []config.Brand {
	return []config.Brand{
		{Name: "Acme", Aliases: []string{"Acme Inc", "acme.com"}},
		{Name: "Globex", Aliases: []string{"globex.com"}},
		{Name: "Initech", Aliases: []string{"initech.io"}},
	}
}

func TestDetect_PlainNameWordBoundary(t *testing.T) {
	r := Detect("Acme is great. We also like Globex.", nil, brands())
	if !r.PerBrand["Acme"] {
		t.Errorf("Acme should be detected")
	}
	if !r.PerBrand["Globex"] {
		t.Errorf("Globex should be detected")
	}
	if r.PerBrand["Initech"] {
		t.Errorf("Initech should not be detected")
	}
}

func TestDetect_NameIsCaseInsensitive(t *testing.T) {
	r := Detect("acme is the WINNER. globex came second.", nil, brands())
	if !r.PerBrand["Acme"] {
		t.Errorf("Acme should be detected case-insensitively")
	}
	if !r.PerBrand["Globex"] {
		t.Errorf("Globex should be detected case-insensitively")
	}
}

func TestDetect_PlainNameRequiresWordBoundary(t *testing.T) {
	// "Acme" inside "Acmesoft" should NOT match (it's a non-domain alias).
	r := Detect("We evaluated Acmesoft and Initechx.", nil, brands())
	if r.PerBrand["Acme"] {
		t.Errorf("Acme should NOT match inside Acmesoft")
	}
	if r.PerBrand["Initech"] {
		t.Errorf("Initech should NOT match inside Initechx")
	}
}

func TestDetect_DomainAliasMatchesSubstring(t *testing.T) {
	// domain aliases are substring matches per spec
	r := Detect("Check https://blog.acme.com/post for more.", nil, brands())
	if !r.PerBrand["Acme"] {
		t.Errorf("acme.com substring should hit Acme")
	}
}

func TestDetect_SourceMatch(t *testing.T) {
	srcs := []Source{
		{URL: "https://www.globex.com/about", Title: "Globex — About"},
		{URL: "https://example.org/", Title: "Unrelated"},
	}
	r := Detect("No brand names in the body at all.", srcs, brands())
	if !r.PerBrand["Globex"] {
		t.Errorf("Globex should be detected from source")
	}
	if r.PerBrand["Acme"] {
		t.Errorf("Acme should not be detected")
	}
}

func TestDetect_NoDoubleCountOverlap(t *testing.T) {
	// "Acme Inc" (alias) overlaps "Acme" (name). Should produce a single text match.
	r := Detect("Acme Inc is our top pick.", nil, brands())
	textMatches := 0
	for _, m := range r.Matches {
		if m.Brand == "Acme" && m.Where == "text" {
			textMatches++
		}
	}
	if textMatches != 1 {
		t.Errorf("expected 1 deduplicated text match for Acme, got %d (%+v)", textMatches, r.Matches)
	}
}

func TestDetect_NoDoubleCountSameSource(t *testing.T) {
	// Both the name "Acme" and the domain alias "acme.com" appear in the same source.
	// We should count it as one mention per source, not two.
	srcs := []Source{
		{URL: "https://acme.com/", Title: "Acme Homepage"},
	}
	r := Detect("", srcs, brands())
	sourceMatches := 0
	for _, m := range r.Matches {
		if m.Brand == "Acme" && m.Where == "source" {
			sourceMatches++
		}
	}
	if sourceMatches != 1 {
		t.Errorf("expected 1 deduplicated source match for Acme, got %d", sourceMatches)
	}
}

func TestLooksLikeDomain(t *testing.T) {
	cases := map[string]bool{
		"acme.com":     true,
		"foo.io":       true,
		"sub.acme.com": true,
		"Acme":         false,
		"v1.0":         false,
		"Mr.":          false,
		".com":         false,
		"acme.":        false,
		"":             false,
	}
	for in, want := range cases {
		if got := looksLikeDomain(in); got != want {
			t.Errorf("looksLikeDomain(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFindWordBoundary(t *testing.T) {
	// non-overlapping detections at proper boundaries
	got := findWordBoundary("acme and acme-co and acme!", "acme")
	if len(got) != 3 {
		t.Fatalf("expected 3 hits, got %d (%v)", len(got), got)
	}
	// embedded inside word should not match
	got = findWordBoundary("acmesoft acme", "acme")
	if len(got) != 1 {
		t.Errorf("expected 1 boundary hit, got %d", len(got))
	}
}

// ===== i18n: non-Latin brand detection =====

func TestDetect_JapaneseBrandSubstring(t *testing.T) {
	// Japanese text has no spaces between "words"; a brand written in
	// Katakana embedded in a Japanese sentence must still be detected.
	brands := []config.Brand{
		{Name: "Toyota", Aliases: []string{"トヨタ"}},
		{Name: "Honda", Aliases: []string{"ホンダ"}},
	}
	r := Detect("私はトヨタの車を購入しました。ホンダも検討しました。", nil, brands)
	if !r.PerBrand["Toyota"] {
		t.Errorf("Toyota (トヨタ) should be detected in Japanese text")
	}
	if !r.PerBrand["Honda"] {
		t.Errorf("Honda (ホンダ) should be detected in Japanese text")
	}
}

func TestDetect_LatinBrandInJapaneseText(t *testing.T) {
	// Latin-script brand names often appear in Japanese answers verbatim —
	// "Toyotaの車" — and should still be detected via word boundaries (the
	// "の" rune is a Unicode letter and acts as a boundary against "Toyota").
	brands := []config.Brand{{Name: "Toyota"}}
	r := Detect("私はToyotaの車を購入しました。", nil, brands)
	if !r.PerBrand["Toyota"] {
		t.Errorf("Toyota (Latin) should be detected in Japanese surrounding text")
	}
}

func TestDetect_CyrillicBrand(t *testing.T) {
	brands := []config.Brand{{Name: "Яндекс"}}
	r := Detect("Я использую Яндекс каждый день.", nil, brands)
	if !r.PerBrand["Яндекс"] {
		t.Errorf("Яндекс should be detected; Cyrillic uses Unicode word boundaries")
	}
}

func TestDetect_CyrillicNoSubstringFalsePositive(t *testing.T) {
	// "Яндекса" is the genitive form; with word-boundary semantics it must
	// NOT match the alias "Яндекс" (the trailing 'а' is a word char).
	brands := []config.Brand{{Name: "Яндекс"}}
	r := Detect("Сегодня нет Яндекса.", nil, brands)
	if r.PerBrand["Яндекс"] {
		t.Errorf("Яндекс should NOT match the inflected form Яндекса (word-boundary should reject it)")
	}
}

func TestDetect_KoreanBrand(t *testing.T) {
	brands := []config.Brand{{Name: "Samsung", Aliases: []string{"삼성"}}}
	r := Detect("삼성의 새 휴대폰을 검토합니다.", nil, brands)
	if !r.PerBrand["Samsung"] {
		t.Errorf("Samsung (삼성) should be detected in Korean text")
	}
}

func TestDetect_ChineseBrand(t *testing.T) {
	brands := []config.Brand{{Name: "Huawei", Aliases: []string{"华为"}}}
	r := Detect("我推荐华为的手机。", nil, brands)
	if !r.PerBrand["Huawei"] {
		t.Errorf("Huawei (华为) should be detected in Chinese text")
	}
}

func TestDetect_ArabicBrand(t *testing.T) {
	// Arabic uses spaces between words; word-boundary anchoring applies.
	brands := []config.Brand{{Name: "أرامكو"}} // Aramco
	r := Detect("أرامكو هي أكبر شركة نفط.", nil, brands)
	if !r.PerBrand["أرامكو"] {
		t.Errorf("Arabic brand should be detected with Unicode word boundaries")
	}
}
