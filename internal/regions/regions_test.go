package regions

import "testing"

func TestPresets_HasGlobalAndCoreLocales(t *testing.T) {
	got := Presets()
	if len(got) < 6 {
		t.Fatalf("expected at least 6 default regions, got %d", len(got))
	}
	// Global must be first so callers iterating in declaration order get
	// the baseline first.
	if got[0].Code != Global {
		t.Errorf("first preset should be Global, got %q", got[0].Code)
	}
	// Spot-check a few specific locales.
	want := []string{"us", "in", "jp", "de", "fr"}
	idx := map[string]bool{}
	for _, r := range got {
		idx[r.Code] = true
	}
	for _, code := range want {
		if !idx[code] {
			t.Errorf("expected preset for %q", code)
		}
	}
}

func TestApplyPrefix_GlobalNoChange(t *testing.T) {
	r := Region{Code: Global, Label: "Global", Prefix: ""}
	if got := r.ApplyPrefix("best shampoo"); got != "best shampoo" {
		t.Errorf("Global ApplyPrefix should pass through, got %q", got)
	}
}

func TestApplyPrefix_PrependsAndJoins(t *testing.T) {
	r := Region{Code: "jp", Label: "Japan", Prefix: "前提: 私は日本から尋ねています。"}
	got := r.ApplyPrefix("best shampoo")
	want := "前提: 私は日本から尋ねています。 best shampoo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyPrefix_HandlesWhitespacePrefix(t *testing.T) {
	r := Region{Code: "us", Label: "US", Prefix: "   "} // effectively empty
	if got := r.ApplyPrefix("best CRM"); got != "best CRM" {
		t.Errorf("whitespace-only prefix should pass through, got %q", got)
	}
}

func TestIsGlobal(t *testing.T) {
	cases := []struct {
		name string
		r    Region
		want bool
	}{
		{"explicit global", Region{Code: Global}, true},
		{"empty code", Region{Code: ""}, true},
		{"empty prefix", Region{Code: "us", Prefix: ""}, true},
		{"populated", Region{Code: "us", Prefix: "Context: US."}, false},
	}
	for _, c := range cases {
		if got := c.r.IsGlobal(); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFindByCode(t *testing.T) {
	list := Presets()
	if r := FindByCode(list, "jp"); r.Code != "jp" {
		t.Errorf("FindByCode(jp): got %#v", r)
	}
	if r := FindByCode(list, "no-such-code"); r.Code != Global {
		t.Errorf("FindByCode(missing) should fall back to global, got %q", r.Code)
	}
}
