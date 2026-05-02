package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MaxComments caps how many comments yt-dlp pulls per post. The user
// flagged that some posts have thousands; capping here keeps the per-post
// cost bounded while still surfacing the top discussion.
const MaxComments = 10

// YtDlpDownloader shells out to the yt-dlp CLI to fetch a single post.
type YtDlpDownloader struct {
	Binary     string // defaults to "yt-dlp"
	CookieFile string // optional, e.g. exported instagram cookies
}

func (d YtDlpDownloader) bin() string {
	if d.Binary == "" {
		return "yt-dlp"
	}
	return d.Binary
}

// shortcodeRE pulls SHORTCODE from /p/SHORTCODE/, /reel/SHORTCODE/, /tv/SHORTCODE/.
var shortcodeRE = regexp.MustCompile(`instagram\.com/(?:p|reel|tv|reels)/([A-Za-z0-9_-]+)`)

func extractShortcode(postURL string) (string, error) {
	m := shortcodeRE.FindStringSubmatch(postURL)
	if len(m) < 2 {
		return "", fmt.Errorf("no shortcode in url %q", postURL)
	}
	return m[1], nil
}

func (d YtDlpDownloader) Download(ctx context.Context, postURL, outDir string) (*DownloadResult, error) {
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
		"--write-info-json",
		"--write-thumbnail",
		"--convert-thumbnails", "jpg",
		"--write-comments",
		"--extractor-args", fmt.Sprintf("instagram:max_comments=%d", MaxComments),
		"--restrict-filenames",
		"-o", "%(id)s.%(ext)s",
		"-P", outDir,
	}
	if d.CookieFile != "" {
		args = append(args, "--cookies", d.CookieFile)
	}
	args = append(args, postURL)

	cmd := exec.CommandContext(ctx, d.bin(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp: %w: %s", err, strings.TrimSpace(stderr.String()))
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
	// yt-dlp's "id" can differ from the URL shortcode for carousels; trust info.
	if info.ID != "" {
		shortcode = info.ID
	}

	videoPath, err := findMediaFile(outDir, shortcode)
	if err != nil {
		return nil, err
	}
	thumb := filepath.Join(outDir, shortcode+".jpg")
	if _, statErr := os.Stat(thumb); statErr != nil {
		thumb = ""
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
		FilePath:        videoPath,
		ThumbnailPath:   thumb,
		Comments:        comments,
	}, nil
}

// findMediaFile picks the downloaded video file for shortcode, preferring mp4.
func findMediaFile(outDir, shortcode string) (string, error) {
	exts := []string{".mp4", ".mkv", ".webm", ".mov"}
	for _, ext := range exts {
		p := filepath.Join(outDir, shortcode+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// Fall back to glob — yt-dlp may have used a numeric suffix.
	matches, _ := filepath.Glob(filepath.Join(outDir, shortcode+".*"))
	for _, m := range matches {
		if strings.HasSuffix(m, ".info.json") || strings.HasSuffix(m, ".jpg") || strings.HasSuffix(m, ".webp") {
			continue
		}
		return m, nil
	}
	return "", fmt.Errorf("no media file produced for %s in %s", shortcode, outDir)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

type ytdlpInfo struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Fulltitle   string         `json:"fulltitle"`
	Description string         `json:"description"`
	Duration    float64        `json:"duration"`
	UploadDate  string         `json:"upload_date"` // YYYYMMDD
	Timestamp   int64          `json:"timestamp"`   // unix seconds, when present
	Comments    []ytdlpComment `json:"comments"`    // populated when --write-comments was passed
}

// ytdlpComment matches the shape yt-dlp puts into info.json. Field names
// are stable across versions for the basic ones; we accept either author /
// author_id and either timestamp / time_text.
type ytdlpComment struct {
	Author    string `json:"author"`
	AuthorID  string `json:"author_id"`
	Text      string `json:"text"`
	LikeCount int64  `json:"like_count"`
	Timestamp int64  `json:"timestamp"`
}

func (c ytdlpComment) toDownloaded() DownloadedComment {
	author := c.Author
	if author == "" {
		author = c.AuthorID
	}
	dc := DownloadedComment{
		Author: author,
		Text:   c.Text,
		Likes:  c.LikeCount,
	}
	if c.Timestamp > 0 {
		dc.PostedAt = time.Unix(c.Timestamp, 0)
	}
	return dc
}

func (i ytdlpInfo) posted() time.Time {
	if i.Timestamp > 0 {
		return time.Unix(i.Timestamp, 0)
	}
	if len(i.UploadDate) == 8 {
		if t, err := time.Parse("20060102", i.UploadDate); err == nil {
			return t
		}
	}
	return time.Time{}
}
