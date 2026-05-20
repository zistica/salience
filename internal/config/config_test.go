package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "salience.json")
	if err := os.WriteFile(p, []byte(StarterJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
	if cfg.SamplesPer == 0 || cfg.Concurrency == 0 {
		t.Errorf("defaults not applied")
	}
}

func TestValidate_MissingFields(t *testing.T) {
	c := &Config{}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"brand", "prompt", "provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestAllBrands(t *testing.T) {
	c := &Config{Brand: Brand{Name: "Acme"}, Competitors: []Brand{{Name: "Globex"}, {Name: "Initech"}}}
	bs := c.AllBrands()
	if len(bs) != 3 || bs[0].Name != "Acme" || bs[1].Name != "Globex" || bs[2].Name != "Initech" {
		t.Errorf("unexpected: %+v", bs)
	}
}
