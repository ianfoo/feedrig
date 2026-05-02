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

// Ollama calls a local Ollama instance (default http://localhost:11434).
// Same prompt shape as OpenRouter so callers can swap them freely. Pulls
// pricing and rate-limit concerns off the table — it's all local.
//
// Recommended models (small enough for laptops, smart enough for video
// summaries): qwen2.5:3b, llama3.2:3b, phi3:mini. Larger models (mistral,
// llama3.1:8b) produce better summaries if you have the RAM.
type Ollama struct {
	BaseURL string // default http://localhost:11434
	Model   string // e.g. "llama3.2:3b"
	HTTP    *http.Client
}

func (o Ollama) baseURL() string {
	if o.BaseURL == "" {
		return "http://localhost:11434"
	}
	return strings.TrimRight(o.BaseURL, "/")
}

func (o Ollama) http() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	// Local, but small models can still take a beat to warm up; 2 min cap.
	return &http.Client{Timeout: 2 * time.Minute}
}

func (o Ollama) Summarize(ctx context.Context, in Input) (*Result, error) {
	if o.Model == "" {
		return nil, fmt.Errorf("%w: Ollama.Model unset", ErrUnavailable)
	}

	categoryHint := "(none provided — invent a 1–2 word kebab-case tag)"
	if len(in.Categories) > 0 {
		categoryHint = strings.Join(in.Categories, ", ")
	}

	prompt := fmt.Sprintf(`Summarize this short-form video for a viewer triaging a feed.

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
		"model":  o.Model,
		"prompt": prompt,
		"stream": false,
		"format": "json",
		// Keep temperature low: this is a structured-output task.
		"options": map[string]any{"temperature": 0.2},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", o.baseURL()+"/api/generate", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.http().Do(req)
	if err != nil {
		// Connection refused etc. → ErrUnavailable so the worker downgrades
		// gracefully rather than burning the video as failed.
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var envelope struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("ollama envelope: %w", err)
	}

	var parsed struct {
		Summary string   `json:"summary"`
		Notes   string   `json:"notes"`
		Tags    []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(envelope.Response), &parsed); err != nil {
		// Some models still drop in stray prose; degrade to "summary = raw".
		return &Result{Summary: strings.TrimSpace(envelope.Response), Model: "ollama:" + o.Model}, nil
	}
	return &Result{
		Summary: parsed.Summary,
		Notes:   parsed.Notes,
		Tags:    normalizeTags(parsed.Tags),
		Model:   "ollama:" + o.Model,
	}, nil
}
