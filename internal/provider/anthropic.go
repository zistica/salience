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

// Anthropic implements Provider against the Messages API directly. Web search
// is requested via the built-in server tool when the model supports it.
type Anthropic struct {
	HTTP     *http.Client
	APIKey   string
	NameStr  string
	ModelID  string
	Endpoint string
	Version  string // anthropic-version header; defaults to a recent stable date
}

func NewAnthropic(name, model, apiKey string) *Anthropic {
	return &Anthropic{
		HTTP:    &http.Client{Timeout: 120 * time.Second},
		APIKey:  apiKey,
		NameStr: name,
		ModelID: model,
		Version: "2023-06-01",
	}
}

func (a *Anthropic) Name() string  { return a.NameStr }
func (a *Anthropic) Kind() string  { return "anthropic" }
func (a *Anthropic) Model() string { return a.ModelID }

type antMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type antRequest struct {
	Model       string                   `json:"model"`
	MaxTokens   int                      `json:"max_tokens"`
	Messages    []antMsg                 `json:"messages"`
	Temperature *float64                 `json:"temperature,omitempty"`
	Tools       []map[string]interface{} `json:"tools,omitempty"`
}

type antContent struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Content json.RawMessage `json:"content,omitempty"` // tool_result payload
	Source  *struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"source,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

type antResponse struct {
	Content []antContent `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Type  string `json:"type"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (a *Anthropic) endpoint() string {
	if a.Endpoint != "" {
		return a.Endpoint
	}
	return "https://api.anthropic.com/v1/messages"
}

func (a *Anthropic) Call(ctx context.Context, prompt string, maxTokens int, temperature *float64) (*Response, error) {
	body := antRequest{
		Model:       a.ModelID,
		MaxTokens:   maxTokens,
		Messages:    []antMsg{{Role: "user", Content: prompt}},
		Temperature: temperature,
		Tools: []map[string]interface{}{
			{
				"type":     "web_search_20250305",
				"name":     "web_search",
				"max_uses": 3,
			},
		},
	}
	resp, raw, err := a.do(ctx, body)
	if err != nil {
		// If the tool block isn't supported by this model, retry without it.
		if isToolRejection(err) || isToolRejection(fmt.Errorf("%s", string(raw))) {
			body.Tools = nil
			resp, raw, err = a.do(ctx, body)
		}
		if err != nil {
			return nil, err
		}
	}
	out := &Response{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		RawJSON:      string(raw),
	}
	var textParts []string
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			textParts = append(textParts, c.Text)
		case "web_search_tool_result":
			// Server tool result: an array of {type,url,title,...} entries.
			var items []struct {
				URL   string `json:"url"`
				Title string `json:"title"`
				Type  string `json:"type"`
			}
			if len(c.Content) > 0 {
				_ = json.Unmarshal(c.Content, &items)
			}
			for _, it := range items {
				if it.URL != "" {
					out.Sources = append(out.Sources, detect.Source{URL: it.URL, Title: it.Title})
				}
			}
		}
	}
	out.Text = strings.Join(textParts, "\n")
	return out, nil
}

func (a *Anthropic) do(ctx context.Context, body antRequest) (*antResponse, []byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint(), bytes.NewReader(buf))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", a.Version)

	httpResp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, nil, Transient(err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, nil, Transient(err)
	}

	switch {
	case httpResp.StatusCode == 401 || httpResp.StatusCode == 403:
		return nil, raw, fmt.Errorf("%w: anthropic %d: %s", ErrAuth, httpResp.StatusCode, truncate(string(raw), 300))
	case httpResp.StatusCode == 429 || httpResp.StatusCode >= 500:
		return nil, raw, Transient(fmt.Errorf("anthropic http %d: %s", httpResp.StatusCode, truncate(string(raw), 300)))
	case httpResp.StatusCode >= 400:
		return nil, raw, fmt.Errorf("anthropic http %d: %s", httpResp.StatusCode, truncate(string(raw), 300))
	}
	var parsed antResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, fmt.Errorf("decode anthropic response: %w", err)
	}
	if parsed.Error != nil {
		return nil, raw, fmt.Errorf("anthropic api error: %s", parsed.Error.Message)
	}
	return &parsed, raw, nil
}

func isToolRejection(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "tool") && (strings.Contains(s, "not supported") || strings.Contains(s, "invalid") || strings.Contains(s, "web_search"))
}
