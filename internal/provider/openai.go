package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/salience-cli/salience/internal/detect"
)

// OpenAI implements Provider using the chat completions API directly via
// net/http. Web search grounding is requested via the optional "tools" field
// when the model supports it (no-op for models that do not).
type OpenAI struct {
	HTTP    *http.Client
	APIKey  string
	NameStr string
	ModelID string
	Endpoint string // override for tests; empty means production
}

func NewOpenAI(name, model, apiKey string) *OpenAI {
	return &OpenAI{
		HTTP:    &http.Client{Timeout: 120 * time.Second},
		APIKey:  apiKey,
		NameStr: name,
		ModelID: model,
	}
}

func (o *OpenAI) Name() string  { return o.NameStr }
func (o *OpenAI) Kind() string  { return "openai" }
func (o *OpenAI) Model() string { return o.ModelID }

type oaiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaiRequest struct {
	Model       string                   `json:"model"`
	Messages    []oaiMsg                 `json:"messages"`
	MaxTokens   int                      `json:"max_tokens,omitempty"`
	Temperature *float64                 `json:"temperature,omitempty"`
	Tools       []map[string]interface{} `json:"tools,omitempty"`
}

type oaiResponse struct {
	Choices []struct {
		Message struct {
			Content     string `json:"content"`
			Annotations []struct {
				Type        string `json:"type"`
				URLCitation struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"url_citation"`
			} `json:"annotations"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func (o *OpenAI) endpoint() string {
	if o.Endpoint != "" {
		return o.Endpoint
	}
	return "https://api.openai.com/v1/chat/completions"
}

func (o *OpenAI) Call(ctx context.Context, prompt string, maxTokens int, temperature *float64) (*Response, error) {
	body := oaiRequest{
		Model:       o.ModelID,
		Messages:    []oaiMsg{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: temperature,
		Tools: []map[string]interface{}{
			{"type": "web_search"},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint(), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)

	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, Transient(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, Transient(err)
	}

	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, fmt.Errorf("%w: openai %d: %s", ErrAuth, resp.StatusCode, truncate(string(raw), 300))
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		return nil, Transient(fmt.Errorf("openai http %d: %s", resp.StatusCode, truncate(string(raw), 300)))
	case resp.StatusCode >= 400:
		// Some models reject the tools field. Retry once without it.
		if strings.Contains(strings.ToLower(string(raw)), "tools") || strings.Contains(strings.ToLower(string(raw)), "web_search") {
			return o.callNoTools(ctx, prompt, maxTokens, temperature)
		}
		return nil, fmt.Errorf("openai http %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed oaiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("openai api error: %s", parsed.Error.Message)
	}
	out := &Response{
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		RawJSON:      string(raw),
	}
	if len(parsed.Choices) > 0 {
		out.Text = parsed.Choices[0].Message.Content
		for _, a := range parsed.Choices[0].Message.Annotations {
			if a.Type == "url_citation" && a.URLCitation.URL != "" {
				out.Sources = append(out.Sources, detect.Source{
					URL:   a.URLCitation.URL,
					Title: a.URLCitation.Title,
				})
			}
		}
	}
	return out, nil
}

func (o *OpenAI) callNoTools(ctx context.Context, prompt string, maxTokens int, temperature *float64) (*Response, error) {
	body := oaiRequest{
		Model:       o.ModelID,
		Messages:    []oaiMsg{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint(), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, Transient(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, fmt.Errorf("%w: openai %d", ErrAuth, resp.StatusCode)
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		return nil, Transient(fmt.Errorf("openai http %d: %s", resp.StatusCode, truncate(string(raw), 300)))
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("openai http %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var parsed oaiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	out := &Response{
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		RawJSON:      string(raw),
	}
	if len(parsed.Choices) > 0 {
		out.Text = parsed.Choices[0].Message.Content
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
