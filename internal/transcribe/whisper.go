package transcribe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WhisperCpp shells out to the `whisper` (whisper.cpp) CLI binary.
//
// Expected install: whisper.cpp built with `make`, the `main` binary on PATH
// renamed to `whisper`, and a model file (e.g. ggml-base.en.bin) somewhere
// readable. ffmpeg must also be on PATH — we extract a 16kHz mono wav first
// since that's whisper.cpp's required input format.
type WhisperCpp struct {
	// Binary path; defaults to "whisper".
	Binary string
	// Model file path, e.g. /opt/whisper.cpp/models/ggml-base.en.bin.
	ModelPath string
	// Language hint, e.g. "en". Empty = auto-detect.
	Language string
}

func (w WhisperCpp) bin() string {
	if w.Binary == "" {
		return "whisper"
	}
	return w.Binary
}

func (w WhisperCpp) Transcribe(ctx context.Context, videoPath string) (*Result, error) {
	if w.ModelPath == "" {
		return nil, fmt.Errorf("%w: WhisperCpp.ModelPath unset", ErrUnavailable)
	}

	tmpDir, err := os.MkdirTemp("", "feedrig-whisper-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, "audio.wav")
	ff := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", videoPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wavPath)
	var ffErr bytes.Buffer
	ff.Stderr = &ffErr
	if err := ff.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract: %w: %s", err, strings.TrimSpace(ffErr.String()))
	}

	args := []string{"-m", w.ModelPath, "-f", wavPath, "-otxt", "-of", filepath.Join(tmpDir, "out")}
	if w.Language != "" {
		args = append(args, "-l", w.Language)
	}
	cmd := exec.CommandContext(ctx, w.bin(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	textBytes, err := os.ReadFile(filepath.Join(tmpDir, "out.txt"))
	if err != nil {
		return nil, fmt.Errorf("read whisper output: %w", err)
	}
	model := "whisper-cpp"
	if base := filepath.Base(w.ModelPath); base != "" {
		model = "whisper-cpp:" + strings.TrimSuffix(base, ".bin")
	}
	return &Result{
		Text:     strings.TrimSpace(string(textBytes)),
		Language: w.Language,
		Model:    model,
	}, nil
}
