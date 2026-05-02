// Package transcribe converts a video file's audio track into text.
package transcribe

import (
	"context"
	"errors"
)

type Result struct {
	Text     string
	Language string // ISO 639-1, e.g. "en"
	Model    string // free-form, e.g. "whisper-cpp:base.en"
}

type Transcriber interface {
	Transcribe(ctx context.Context, videoPath string) (*Result, error)
}

// Noop returns ErrUnavailable. Use as the default when no real transcriber
// is configured; the worker will skip transcription gracefully.
type Noop struct{}

func (Noop) Transcribe(_ context.Context, _ string) (*Result, error) {
	return nil, ErrUnavailable
}

var ErrUnavailable = errors.New("transcriber unavailable")
