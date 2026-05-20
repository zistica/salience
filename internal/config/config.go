// Package config defines the salience configuration file format and loader.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Brand represents the user's brand or a competitor brand.
// Aliases may include alternate spellings, abbreviations, or domain names.
type Brand struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

// Provider declares one LLM endpoint and the specific model to use against it.
// Kind must be one of "openai" or "anthropic".
type Provider struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Model string `json:"model"`
}

// Config is the top-level JSON document the user supplies.
type Config struct {
	Brand        Brand      `json:"brand"`
	Competitors  []Brand    `json:"competitors"`
	Prompts      []string   `json:"prompts"`
	Providers    []Provider `json:"providers"`
	// Regions is the list of locales each prompt should be asked from.
	// An empty list collapses to a single implicit "global" region —
	// preserving v0.1/v0.2 behavior.
	Regions      []Region   `json:"regions,omitempty"`
	SamplesPer   int        `json:"samples_per_prompt"`
	Concurrency  int        `json:"concurrency_per_provider"`
	MaxTokens    int        `json:"max_tokens,omitempty"`
	Temperature  *float64   `json:"temperature,omitempty"`
}

// Region is one tracked locale that gets prepended to each prompt before
// the call goes out. Carried inline on Config (rather than as a separate
// internal/regions import) so the JSON schema stays self-contained.
type Region struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Prefix string `json:"prefix"`
}

// Defaults supplies the numeric knob defaults the spec leaves up to the
// implementation. Callers should invoke this after Load to fill in zero values.
func (c *Config) Defaults() {
	if c.SamplesPer <= 0 {
		c.SamplesPer = 5
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 3
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = 512
	}
}

// Validate returns an error if the configuration is missing required pieces.
func (c *Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Brand.Name) == "" {
		problems = append(problems, "brand.name is required")
	}
	if len(c.Prompts) == 0 {
		problems = append(problems, "at least one prompt is required")
	}
	if len(c.Providers) == 0 {
		problems = append(problems, "at least one provider is required")
	}
	for i, p := range c.Providers {
		if strings.TrimSpace(p.Name) == "" {
			problems = append(problems, fmt.Sprintf("providers[%d].name is required", i))
		}
		switch p.Kind {
		case "openai", "anthropic":
		default:
			problems = append(problems, fmt.Sprintf("providers[%d].kind %q is not supported (use openai or anthropic)", i, p.Kind))
		}
		if strings.TrimSpace(p.Model) == "" {
			problems = append(problems, fmt.Sprintf("providers[%d].model is required", i))
		}
	}
	if len(problems) > 0 {
		return errors.New("config invalid: " + strings.Join(problems, "; "))
	}
	return nil
}

// Load reads a JSON config file from disk and returns a populated Config with
// defaults applied. It does not call Validate; callers can choose when.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	c.Defaults()
	return &c, nil
}

// AllBrands returns the user brand followed by the competitor brands in the
// order they appear in the config. Useful when iterating for detection.
func (c *Config) AllBrands() []Brand {
	out := make([]Brand, 0, 1+len(c.Competitors))
	out = append(out, c.Brand)
	out = append(out, c.Competitors...)
	return out
}
