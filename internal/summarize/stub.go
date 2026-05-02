package summarize

import (
	"context"
	"strings"
)

// Stub is a deterministic offline summarizer used for development and tests.
// It produces a brief summary by echoing the title and a snippet of the
// transcript, and assigns up to two tags by keyword-matching the configured
// categories. Useful for end-to-end smoke tests when no API key is available.
type Stub struct{}

func (Stub) Summarize(_ context.Context, in Input) (*Result, error) {
	body := strings.TrimSpace(in.Transcript)
	if body == "" {
		body = strings.TrimSpace(in.Description)
	}
	summary := in.Title
	if snippet := truncate(body, 200); snippet != "" {
		if summary == "" {
			summary = snippet
		} else {
			summary = summary + " — " + snippet
		}
	}
	if summary == "" {
		summary = "(no content available)"
	}

	tags := []string{}
	hay := strings.ToLower(in.Title + " " + in.Description + " " + in.Transcript)
	for _, cat := range in.Categories {
		needle := strings.ToLower(strings.ReplaceAll(cat, "-", " "))
		if needle != "" && strings.Contains(hay, needle) {
			tags = append(tags, normalizeTags([]string{cat})...)
			if len(tags) >= 2 {
				break
			}
		}
	}

	return &Result{
		Summary: summary,
		Tags:    tags,
		Model:   "stub:keyword-match",
	}, nil
}
