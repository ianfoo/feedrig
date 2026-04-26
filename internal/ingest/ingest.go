// Package ingest discovers and downloads new posts from Instagram creators.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/video"
)


// Discoverer enumerates a creator's recent post shortcodes (newest first).
// Implementations should be safe to call without authentication.
type Discoverer interface {
	Recent(ctx context.Context, handle string) ([]string, error)
}

// Downloader fetches a single post URL into outDir and reports its metadata.
type Downloader interface {
	Download(ctx context.Context, postURL, outDir string) (*DownloadResult, error)
}

type DownloadResult struct {
	ExternalID      string
	URL             string
	Title           string
	Description     string
	DurationSeconds int64
	PostedAt        time.Time
	FilePath        string // absolute path on disk
	ThumbnailPath   string // absolute path on disk, if any
}

// Enricher is the small surface ingest needs from the enrichment worker.
// Defined locally to avoid an import cycle with internal/enrich.
type Enricher interface {
	Enqueue(videoID int64)
}

// Service orchestrates discovery + download + persistence.
type Service struct {
	disc      Discoverer
	dl        Downloader
	creators  *creator.Store
	videos    *video.Store
	enricher  Enricher
	mediaRoot string
	log       *slog.Logger
}

func NewService(disc Discoverer, dl Downloader, creators *creator.Store, videos *video.Store, mediaRoot string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{disc: disc, dl: dl, creators: creators, videos: videos, mediaRoot: mediaRoot, log: log}
}

// SetEnricher wires the enrichment worker; new downloads will be enqueued.
// Safe to leave unset; the service degrades to no enrichment.
func (s *Service) SetEnricher(e Enricher) { s.enricher = e }

// FetchNewForCreator discovers recent posts for the creator and downloads any
// not yet stored. Returns the count of newly added videos. Discovery failures
// are wrapped in ErrDiscovery so callers can suggest the manual-paste path.
func (s *Service) FetchNewForCreator(ctx context.Context, c *creator.Creator) (int, error) {
	codes, err := s.disc.Recent(ctx, c.Handle)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrDiscovery, err)
	}
	existing, err := s.videos.ExistingExternalIDs(ctx, c.ID)
	if err != nil {
		return 0, fmt.Errorf("load existing: %w", err)
	}

	added := 0
	for _, code := range codes {
		if existing[code] {
			continue
		}
		url := fmt.Sprintf("https://www.instagram.com/p/%s/", code)
		if _, err := s.fetchURL(ctx, c, url); err != nil {
			s.log.Warn("download failed", "creator", c.Handle, "code", code, "err", err)
			continue
		}
		added++
	}
	if err := s.creators.MarkFetched(ctx, c.ID); err != nil {
		s.log.Warn("mark fetched", "err", err)
	}
	return added, nil
}

// Redownload fetches a video that's already in the store (typically in the
// archived state because its media file was reaped by the TTL sweeper),
// replaces the on-disk file, and resets the row to active. Re-enqueues
// enrichment so the summary/transcript get a refresh too.
func (s *Service) Redownload(ctx context.Context, v *video.Video) error {
	c, err := s.creators.Get(ctx, v.CreatorID)
	if err != nil {
		return fmt.Errorf("load creator: %w", err)
	}
	outDir := filepath.Join(s.mediaRoot, c.Handle)
	res, err := s.dl.Download(ctx, v.URL, outDir)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if err := s.videos.UpdateAfterRedownload(ctx, v.ID, res.FilePath, res.ThumbnailPath); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	if s.enricher != nil {
		s.enricher.Enqueue(v.ID)
	}
	return nil
}

// FetchURL downloads a single post by URL — the manual-paste path. Returns
// the persisted video, or ErrAlreadyHave if it's already in the store.
func (s *Service) FetchURL(ctx context.Context, c *creator.Creator, url string) (*video.Video, error) {
	v, err := s.fetchURL(ctx, c, url)
	if err != nil {
		return nil, err
	}
	_ = s.creators.MarkFetched(ctx, c.ID)
	return v, nil
}

func (s *Service) fetchURL(ctx context.Context, c *creator.Creator, url string) (*video.Video, error) {
	outDir := filepath.Join(s.mediaRoot, c.Handle)
	res, err := s.dl.Download(ctx, url, outDir)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	v := &video.Video{
		CreatorID:       c.ID,
		ExternalID:      res.ExternalID,
		URL:             res.URL,
		Title:           res.Title,
		Description:     res.Description,
		DurationSeconds: res.DurationSeconds,
		FilePath:        res.FilePath,
		ThumbnailPath:   res.ThumbnailPath,
	}
	if !res.PostedAt.IsZero() {
		t := res.PostedAt
		v.PostedAt = &t
	}

	id, err := s.videos.Insert(ctx, v)
	if err != nil {
		if errors.Is(err, video.ErrDuplicate) {
			return nil, ErrAlreadyHave
		}
		return nil, fmt.Errorf("persist: %w", err)
	}
	v.ID = id
	if s.enricher != nil {
		s.enricher.Enqueue(id)
	}
	return v, nil
}

var (
	ErrDiscovery   = errors.New("profile discovery unavailable")
	ErrAlreadyHave = errors.New("video already in library")
)
