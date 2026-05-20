package config

// StarterJSON is the example config emitted by `salience init`.
const StarterJSON = `{
  "brand": {
    "name": "Acme",
    "aliases": ["Acme Inc", "acme.com"]
  },
  "competitors": [
    {
      "name": "Globex",
      "aliases": ["globex.com"]
    },
    {
      "name": "Initech",
      "aliases": ["initech.io"]
    }
  ],
  "prompts": [
    "Recommend three SaaS tools for managing customer feedback.",
    "What are the best vendors for enterprise widget orchestration?"
  ],
  "providers": [
    {
      "name": "openai-gpt-4o",
      "kind": "openai",
      "model": "gpt-4o"
    },
    {
      "name": "anthropic-sonnet",
      "kind": "anthropic",
      "model": "claude-3-5-sonnet-latest"
    }
  ],
  "regions": [
    { "code": "global", "label": "Global",         "prefix": "" },
    { "code": "us",     "label": "United States",  "prefix": "Context: I am asking from the United States." },
    { "code": "in",     "label": "India",          "prefix": "Context: I am asking from India." },
    { "code": "jp",     "label": "Japan",          "prefix": "前提: 私は日本から尋ねています。" }
  ],
  "samples_per_prompt": 5,
  "concurrency_per_provider": 3,
  "max_tokens": 512
}
`

// StarterEnv is the .env template emitted by `salience init`.
const StarterEnv = `# Salience API keys. Fill in the ones you plan to use.
# Lines starting with # are ignored. Values may be quoted or bare.

OPENAI_API_KEY=
ANTHROPIC_API_KEY=
PERPLEXITY_API_KEY=

# Optional endpoint overrides (used by tests or to route through a proxy):
# OPENAI_BASE_URL=
# ANTHROPIC_BASE_URL=
# PERPLEXITY_BASE_URL=
`
