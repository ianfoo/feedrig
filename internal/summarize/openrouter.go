package summarize

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenRouter calls https://openrouter.ai/api/v1/chat/completions with the
// configured Model (e.g. "anthropic/claude-3.5-haiku", "openai/gpt-4o-mini",
// "moonshotai/kimi-k2"). One client → many providers.
type OpenRouter struct {
	APIKey  string
	Model   string
	BaseURL string // defaults to https://openrouter.ai/api/v1
	HTTP    *http.Client
}

func (o OpenRouter) baseURL() string {
	if o.BaseURL == "" {
		return "https://openrouter.ai/api/v1"
	}
	return o.BaseURL
}

func (o OpenRouter) http() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (o OpenRouter) Summarize(ctx context.Context, in Input) (*Result, error) {
	if o.APIKey == "" {
		return nil, fmt.Errorf("%w: OpenRouter.APIKey unset", ErrUnavailable)
	}
	if o.Model == "" {
		return nil, fmt.Errorf("%w: OpenRouter.Model unset", ErrUnavailable)
	}

	categoryHint := "(none provided — invent a 1–2 word kebab-case tag)"
	if len(in.Categories) > 0 {
		categoryHint = strings.Join(in.Categories, ", ")
	}

	user := fmt.Sprintf(`Summarize this short-form video for a viewer triaging a feed.

Title: %s
Description: %s
Transcript: %s

Pick 1–3 category tags from this list (case-insensitive; you may omit if none fit): %s

Respond as JSON only, with this shape:
{"summary": "1–3 sentences capturing the essence", "notes": "optional bullet-point notes (markdown allowed)", "tags": ["tag1", "tag2"]}`,
		truncate(in.Title, 200),
		truncate(in.Description, 800),
		truncate(in.Transcript, 6000),
		categoryHint,
	)

	reqBody, _ := json.Marshal(map[string]any{
		"model":           o.Model,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": "You write tight, factual video summaries. Always respond as valid JSON matching the requested shape."},
			{"role": "user", "content": user},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", o.baseURL()+"/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/ianfoo/feedrig")
	req.Header.Set("X-Title", "feedrig")

	resp, err := o.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openrouter %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("openrouter envelope: %w", err)
	}
	if len(envelope.Choices) == 0 {
		return nil, fmt.Errorf("openrouter: no choices in response")
	}

	var parsed struct {
		Summary string   `json:"summary"`
		Notes   string   `json:"notes"`
		Tags    []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &parsed); err != nil {
		// Fallback: return raw content as summary if model didn't produce JSON.
		return &Result{Summary: envelope.Choices[0].Message.Content, Model: "openrouter:" + o.Model}, nil
	}

	return &Result{
		Summary: parsed.Summary,
		Notes:   parsed.Notes,
		Tags:    normalizeTags(parsed.Tags),
		Model:   "openrouter:" + o.Model,
	}, nil
}

func normalizeTags(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		t = strings.ReplaceAll(t, " ", "-")
		t = strings.ReplaceAll(t, "_", "-")
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
