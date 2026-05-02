package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// YtDlpPreviewer fetches metadata + thumbnail only — no video file. Same
// flag and cookie surface as YtDlpDownloader; uses --skip-download so the
// expensive media transfer is skipped. Result: ~30 KB of JPEG + a row
// instead of multi-MB per post. The user can later promote the row to a
// full download via /videos/{id}/download.
type YtDlpPreviewer struct {
	Binary     string
	CookieFile string
}

func (p YtDlpPreviewer) bin() string {
	if p.Binary == "" {
		return "yt-dlp"
	}
	return p.Binary
}

func (p YtDlpPreviewer) Download(ctx context.Context, postURL, outDir string) (*DownloadResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	shortcode, err := extractShortcode(postURL)
	if err != nil {
		return nil, err
	}

	args := []string{
		"--no-playlist",
		"--no-progress",
		"--no-warnings",
		"--skip-download", // the whole point — metadata + thumb only
		"--write-info-json",
		"--write-thumbnail",
		"--convert-thumbnails", "jpg",
		"--write-comments",
		"--extractor-args", fmt.Sprintf("instagram:max_comments=%d", MaxComments),
		"--restrict-filenames",
		"-o", "%(id)s.%(ext)s",
		"-P", outDir,
	}
	if p.CookieFile != "" {
		args = append(args, "--cookies", p.CookieFile)
	}
	args = append(args, postURL)

	cmd := exec.CommandContext(ctx, p.bin(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp (preview): %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	infoPath := filepath.Join(outDir, shortcode+".info.json")
	infoData, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, fmt.Errorf("read info json %s: %w", infoPath, err)
	}
	var info ytdlpInfo
	if err := json.Unmarshal(infoData, &info); err != nil {
		return nil, fmt.Errorf("parse info json: %w", err)
	}
	if info.ID != "" {
		shortcode = info.ID
	}

	thumb := filepath.Join(outDir, shortcode+".jpg")
	if _, statErr := os.Stat(thumb); statErr != nil {
		// fall back to .webp / unconverted
		matches, _ := filepath.Glob(filepath.Join(outDir, shortcode+".*"))
		thumb = ""
		for _, m := range matches {
			if strings.HasSuffix(m, ".info.json") {
				continue
			}
			if strings.HasSuffix(m, ".jpg") || strings.HasSuffix(m, ".jpeg") || strings.HasSuffix(m, ".webp") || strings.HasSuffix(m, ".png") {
				thumb = m
				break
			}
		}
	}

	comments := make([]DownloadedComment, 0, len(info.Comments))
	for i, c := range info.Comments {
		if i >= MaxComments {
			break
		}
		if c.Text == "" {
			continue
		}
		comments = append(comments, c.toDownloaded())
	}

	return &DownloadResult{
		ExternalID:      shortcode,
		URL:             postURL,
		Title:           firstNonEmpty(info.Title, info.Fulltitle),
		Description:     info.Description,
		DurationSeconds: int64(info.Duration),
		PostedAt:        info.posted(),
		FilePath:        "", // preview-only: no media file
		ThumbnailPath:   thumb,
		Comments:        comments,
	}, nil
}

