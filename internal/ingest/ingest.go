// Package ingest discovers and downloads new posts from Instagram creators.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
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
	dl        Downloader // full media download
	preview   Downloader // metadata + thumbnail only (for preview-mode creators)
	creators  *creator.Store
	videos    *video.Store
	enricher  Enricher
	mediaRoot string
	log       *slog.Logger

	// inFlight dedupes concurrent FetchNewForCreator calls for the same
	// creator id (manual web click + scheduled poll racing each other).
	inFlight sync.Map // map[int64]struct{}
}

func NewService(disc Discoverer, dl Downloader, creators *creator.Store, videos *video.Store, mediaRoot string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{disc: disc, dl: dl, creators: creators, videos: videos, mediaRoot: mediaRoot, log: log}
}

// SetPreviewer wires the metadata-only downloader used for creators whose
// ingest_mode is 'preview'. Optional: without it, preview-mode creators
// silently fall back to the full downloader.
func (s *Service) SetPreviewer(p Downloader) { s.preview = p }

// IsFetching reports whether a fetch is currently in flight for the given
// creator. Web layer surfaces this in the UI; scheduler skips on `true`.
func (s *Service) IsFetching(creatorID int64) bool {
	_, ok := s.inFlight.Load(creatorID)
	return ok
}

// SetEnricher wires the enrichment worker; new downloads will be enqueued.
// Safe to leave unset; the service degrades to no enrichment.
func (s *Service) SetEnricher(e Enricher) { s.enricher = e }

// PerVideoDownloadTimeout caps a single yt-dlp invocation. Long enough for
// the largest reels and slow networks; short enough that a stuck fetch
// doesn't wedge the per-creator iteration loop.
const PerVideoDownloadTimeout = 5 * time.Minute

// FetchNewForCreator discovers recent posts for the creator and downloads any
// not yet stored. Returns the count of newly added videos, or ErrAlreadyFetching
// if another goroutine is already processing this creator.
//
// Discovery failures are wrapped in ErrDiscovery so callers can suggest the
// manual-paste path. Each per-video download gets its own
// PerVideoDownloadTimeout so one slow download doesn't kill the rest of the
// batch. The caller's ctx still bounds the overall operation, but should be
// generous (server-life, not request-life) — at 12 videos × up to 5 min each,
// total budget can exceed an hour in pathological cases. The web layer
// detaches via a goroutine.
func (s *Service) FetchNewForCreator(ctx context.Context, c *creator.Creator) (int, error) {
	if _, busy := s.inFlight.LoadOrStore(c.ID, struct{}{}); busy {
		return 0, ErrAlreadyFetching
	}
	defer s.inFlight.Delete(c.ID)

	s.log.Info("fetch starting", "creator", c.Handle)
	batchStart := time.Now()

	discStart := time.Now()
	codes, err := s.disc.Recent(ctx, c.Handle)
	if err != nil {
		s.log.Warn("discovery failed", "creator", c.Handle, "elapsed", time.Since(discStart).Round(time.Millisecond), "err", err)
		return 0, fmt.Errorf("%w: %v", ErrDiscovery, err)
	}
	s.log.Info("discovery completed", "creator", c.Handle, "count", len(codes), "elapsed", time.Since(discStart).Round(time.Millisecond))
	existing, err := s.videos.ExistingExternalIDs(ctx, c.ID)
	if err != nil {
		return 0, fmt.Errorf("load existing: %w", err)
	}

	added := 0
	for _, code := range codes {
		if existing[code] {
			continue
		}
		// Bail early if the parent ctx is already done — no point starting
		// a fresh sub-context just to time out immediately.
		if err := ctx.Err(); err != nil {
			s.log.Warn("fetch loop canceled", "creator", c.Handle, "err", err)
			break
		}
		url := fmt.Sprintf("https://www.instagram.com/p/%s/", code)
		s.log.Info("download starting", "creator", c.Handle, "code", code)
		started := time.Now()
		dlCtx, cancel := context.WithTimeout(ctx, PerVideoDownloadTimeout)
		v, err := s.fetchURL(dlCtx, c, url)
		cancel()
		elapsed := time.Since(started).Round(time.Millisecond)
		if err != nil {
			s.log.Warn("download failed", "creator", c.Handle, "code", code, "elapsed", elapsed, "err", err)
			continue
		}
		s.log.Info("download succeeded", "creator", c.Handle, "code", code, "video_id", v.ID, "title", v.Title, "elapsed", elapsed)
		added++
	}
	if err := s.creators.MarkFetched(ctx, c.ID); err != nil {
		s.log.Warn("mark fetched", "err", err)
	}
	s.log.Info("fetch completed", "creator", c.Handle, "added", added, "elapsed", time.Since(batchStart).Round(time.Millisecond))
	return added, nil
}

// Promote turns a preview-state row into a full download: fetches the media
// file with the standard yt-dlp downloader, fills in file_path on the
// existing row, sets state='active', and enqueues enrichment. Used by
// "Watch now" on a preview card.
//
// Implementation reuses the redownload helper since the row-update shape is
// the same — the only differences (state transition, enrichment enqueue)
// happen automatically.
func (s *Service) Promote(ctx context.Context, v *video.Video) error {
	return s.Redownload(ctx, v)
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
	// Pick the right downloader based on the creator's ingest mode. If the
	// previewer isn't configured for some reason, fall back to the full
	// downloader so the user still gets something rather than nothing.
	dl := s.dl
	preview := false
	if c.IngestMode == creator.IngestPreview && s.preview != nil {
		dl = s.preview
		preview = true
	}

	outDir := filepath.Join(s.mediaRoot, c.Handle)
	res, err := dl.Download(ctx, url, outDir)
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
	if preview {
		v.State = video.StatePreview
	}

	id, err := s.videos.Insert(ctx, v)
	if err != nil {
		if errors.Is(err, video.ErrDuplicate) {
			return nil, ErrAlreadyHave
		}
		return nil, fmt.Errorf("persist: %w", err)
	}
	v.ID = id
	// Don't transcribe / summarize previews — there's no media file to
	// transcribe, and summarizing the IG caption alone produces low-value
	// output. Enrichment kicks in on promote.
	if !preview && s.enricher != nil {
		s.enricher.Enqueue(id)
	}
	return v, nil
}

var (
	ErrDiscovery       = errors.New("profile discovery unavailable")
	ErrAlreadyHave     = errors.New("video already in library")
	ErrAlreadyFetching = errors.New("a fetch is already in progress for this creator")
)
