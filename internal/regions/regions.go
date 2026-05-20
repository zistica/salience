// Package regions defines the per-project "where am I asking from?"
// context that's mixed into each provider call. Brands are wildly
// region-dependent — a JP-only ride-hailing app is invisible in a US
// "best ride-hailing" answer — so a project should measure each prompt
// in every region it cares about, not just the LLM's default.
package regions

import "strings"

// Global is the implicit "no region context" — same behavior as v0.1.
const Global = "global"

// Region is one tracked locale. Code is the short slug used in the
// samples.region column; Label is what users see in dashboards; Prefix
// is prepended to each user prompt before it's sent to the provider.
type Region struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Prefix string `json:"prefix"`
}

// ApplyPrefix returns prompt with the region's Prefix prepended. Empty
// prefix returns the prompt unchanged (so Global is a no-op).
func (r Region) ApplyPrefix(prompt string) string {
	p := strings.TrimSpace(r.Prefix)
	if p == "" {
		return prompt
	}
	return p + " " + prompt
}

// IsGlobal reports whether r is the implicit no-region default.
func (r Region) IsGlobal() bool {
	return r.Code == Global || r.Code == "" || strings.TrimSpace(r.Prefix) == ""
}

// Presets is the default set of regions injected into a starter project.
// Users can edit/remove freely; the language hint is baked in to the
// prefix so a Japanese-language prompt automatically nudges the LLM to
// answer in Japanese with Japan-relevant brands.
func Presets() []Region {
	return []Region{
		{Code: Global, Label: "Global", Prefix: ""},
		{Code: "us",   Label: "United States",  Prefix: "Context: I am asking from the United States."},
		{Code: "uk",   Label: "United Kingdom", Prefix: "Context: I am asking from the United Kingdom."},
		{Code: "in",   Label: "India",          Prefix: "Context: I am asking from India."},
		{Code: "jp",   Label: "Japan",          Prefix: "前提: 私は日本から尋ねています。"},
		{Code: "de",   Label: "Germany",        Prefix: "Hinweis: Ich frage aus Deutschland."},
		{Code: "fr",   Label: "France",         Prefix: "Contexte: je pose la question depuis la France."},
		{Code: "br",   Label: "Brazil",         Prefix: "Contexto: estou perguntando do Brasil."},
		{Code: "mx",   Label: "Mexico",         Prefix: "Contexto: pregunto desde México."},
		{Code: "id",   Label: "Indonesia",      Prefix: "Konteks: saya bertanya dari Indonesia."},
		{Code: "kr",   Label: "South Korea",    Prefix: "맥락: 저는 한국에서 묻고 있습니다."},
	}
}

// Default returns the Global preset. Useful for callers that need a
// non-nil region when none is configured.
func Default() Region {
	return Region{Code: Global, Label: "Global", Prefix: ""}
}

// FindByCode returns the region with the given Code, or Default() if not
// found. Used by report rendering and the dashboard region filter.
func FindByCode(list []Region, code string) Region {
	for _, r := range list {
		if r.Code == code {
			return r
		}
	}
	return Default()
}
