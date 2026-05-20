package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/salience-cli/salience/internal/detect"
)

// Perplexity uses Perplexity's OpenAI-compatible chat/completions endpoint.
// Their sonar* models already do web search natively, so no tool parameter
// is required. They also return a separate `citations` field on the response
// that we map into Source.URL.
type Perplexity struct {
	HTTP     *http.Client
	APIKey   string
	NameStr  string
	ModelID  string
	Endpoint string // override for tests; empty means production
}

// NewPerplexity returns a configured client.
func NewPerplexity(name, model, apiKey string) *Perplexity {
	return &Perplexity{
		HTTP:    &http.Client{Timeout: 120 * time.Second},
		APIKey:  apiKey,
		NameStr: name,
		ModelID: model,
	}
}

func (p *Perplexity) Name() string  { return p.NameStr }
func (p *Perplexity) Kind() string  { return "perplexity" }
func (p *Perplexity) Model() string { return p.ModelID }

func (p *Perplexity) endpoint() string {
	if p.Endpoint != "" {
		return p.Endpoint
	}
	return "https://api.perplexity.ai/chat/completions"
}

type pplxMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type pplxRequest struct {
	Model       string    `json:"model"`
	Messages    []pplxMsg `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
}

type pplxResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	// Perplexity returns citations as a flat list of URLs alongside the
	// content. Newer responses also include search_results with titles —
	// we accept either.
	Citations     []string `json:"citations"`
	SearchResults []struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"search_results"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (p *Perplexity) Call(ctx context.Context, prompt string, maxTokens int, temperature *float64) (*Response, error) {
	body := pplxRequest{
		Model:       p.ModelID,
		Messages:    []pplxMsg{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	httpResp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, Transient(err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, Transient(err)
	}

	switch {
	case httpResp.StatusCode == 401 || httpResp.StatusCode == 403:
		return nil, fmt.Errorf("%w: perplexity %d: %s", ErrAuth, httpResp.StatusCode, truncate(string(raw), 300))
	case httpResp.StatusCode == 429 || httpResp.StatusCode >= 500:
		return nil, Transient(fmt.Errorf("perplexity http %d: %s", httpResp.StatusCode, truncate(string(raw), 300)))
	case httpResp.StatusCode >= 400:
		return nil, fmt.Errorf("perplexity http %d: %s", httpResp.StatusCode, truncate(string(raw), 300))
	}

	var parsed pplxResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode perplexity response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("perplexity api error: %s", parsed.Error.Message)
	}

	out := &Response{
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		RawJSON:      string(raw),
	}
	if len(parsed.Choices) > 0 {
		out.Text = parsed.Choices[0].Message.Content
	}
	// Prefer search_results (richer) over the flat citations list.
	seen := map[string]bool{}
	for _, sr := range parsed.SearchResults {
		if sr.URL != "" && !seen[sr.URL] {
			seen[sr.URL] = true
			out.Sources = append(out.Sources, detect.Source{URL: sr.URL, Title: sr.Title})
		}
	}
	for _, u := range parsed.Citations {
		if u != "" && !seen[u] {
			seen[u] = true
			out.Sources = append(out.Sources, detect.Source{URL: u})
		}
	}
	return out, nil
}
