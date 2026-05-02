// Package summarize turns a transcript (and metadata) into a short summary,
// notes, and a category tag drawn from a configurable list.
package summarize

import (
	"context"
	"errors"
	"strings"
)

// Input is everything the summarizer can use to write a useful summary. Any
// field may be empty; implementations should handle missing pieces gracefully
// (e.g. no transcript means rely on title/description only).
type Input struct {
	Title       string
	Description string
	Transcript  string
	// Categories is the menu of tag names the summarizer should pick from.
	// Empty list = unconstrained; the model may invent a tag.
	Categories []string
}

type Result struct {
	Summary string   // 1–3 sentence summary
	Notes   string   // optional bullet-point notes
	Tags    []string // chosen categories (lowercase, kebab-case)
	Model   string   // free-form, e.g. "openrouter:anthropic/claude-3.5-haiku"
}

type Summarizer interface {
	Summarize(ctx context.Context, in Input) (*Result, error)
}

type Noop struct{}

func (Noop) Summarize(_ context.Context, _ Input) (*Result, error) {
	return nil, ErrUnavailable
}

var ErrUnavailable = errors.New("summarizer unavailable")

// truncate is a small helper used by impls when keeping prompts compact.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
