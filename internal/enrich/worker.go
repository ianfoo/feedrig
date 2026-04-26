package enrich

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ianfoo/feedrig/internal/summarize"
	"github.com/ianfoo/feedrig/internal/transcribe"
	"github.com/ianfoo/feedrig/internal/video"
)

// Worker runs the transcribe → summarize → tag pipeline for one video at a
// time, off a buffered channel. Concurrency is intentionally one for v0.2:
// LLM calls are network-bound and predictable; we don't want surprises.
type Worker struct {
	Videos      *video.Store
	Enrich      *Store
	Transcriber transcribe.Transcriber
	Summarizer  summarize.Summarizer
	Categories  []string // suggested category menu for the summarizer
	Log         *slog.Logger

	queue chan int64
	wg    sync.WaitGroup
	once  sync.Once
}

// Enqueue schedules a video for enrichment. Non-blocking: if the queue is
// full, the request is dropped and the next Run() startup will rescan
// pending videos and pick it up.
func (w *Worker) Enqueue(videoID int64) {
	w.ensureInit()
	select {
	case w.queue <- videoID:
	default:
		w.log().Warn("enrichment queue full; will be picked up on next sweep", "video_id", videoID)
	}
}

func (w *Worker) ensureInit() {
	w.once.Do(func() {
		if w.queue == nil {
			w.queue = make(chan int64, 64)
		}
	})
}

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// Run starts the worker loop. It periodically rescans the DB for pending or
// failed-only-once videos and processes the queue until ctx is canceled.
func (w *Worker) Run(ctx context.Context) {
	w.ensureInit()
	w.wg.Add(1)
	defer w.wg.Done()

	w.rescan(ctx)
	rescanTicker := time.NewTicker(rescanInterval)
	defer rescanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rescanTicker.C:
			w.rescan(ctx)
		case id := <-w.queue:
			w.process(ctx, id)
		}
	}
}

const rescanInterval = 30 * time.Second

func (w *Worker) rescan(ctx context.Context) {
	ids, err := w.Enrich.PendingVideoIDs(ctx, false) // pending-only; failed stays failed until restarted
	if err != nil {
		w.log().Warn("rescan failed", "err", err)
		return
	}
	for _, id := range ids {
		select {
		case w.queue <- id:
		default:
			w.log().Info("rescan: queue full, will retry next tick", "remaining", len(ids))
			return
		}
	}
}

// Wait blocks until Run has returned. Safe to call multiple times.
func (w *Worker) Wait() { w.wg.Wait() }

func (w *Worker) process(ctx context.Context, videoID int64) {
	log := w.log().With("video_id", videoID)
	if err := w.Enrich.SetEnrichmentState(ctx, videoID, StateRunning, ""); err != nil {
		log.Warn("set running", "err", err)
	}

	v, err := w.Videos.Get(ctx, videoID)
	if err != nil {
		log.Warn("load video", "err", err)
		_ = w.Enrich.SetEnrichmentState(ctx, videoID, StateFailed, err.Error())
		return
	}

	in := summarize.Input{
		Title:       v.Title.String,
		Description: v.Description.String,
		Categories:  w.Categories,
	}

	// Step 1: transcribe (best-effort).
	if w.Transcriber != nil {
		tctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		tres, terr := w.Transcriber.Transcribe(tctx, v.FilePath)
		cancel()
		if terr == nil && tres != nil {
			if err := w.Enrich.UpsertTranscript(ctx, Transcript{
				VideoID: videoID, Text: tres.Text, Language: tres.Language, Model: tres.Model,
			}); err != nil {
				log.Warn("save transcript", "err", err)
			}
			in.Transcript = tres.Text
		} else if !errors.Is(terr, transcribe.ErrUnavailable) {
			log.Warn("transcribe", "err", terr)
		}
	}

	// Step 2: summarize.
	if w.Summarizer != nil {
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		sres, serr := w.Summarizer.Summarize(sctx, in)
		cancel()
		if serr != nil {
			if errors.Is(serr, summarize.ErrUnavailable) {
				_ = w.Enrich.SetEnrichmentState(ctx, videoID, StateSkipped, "summarizer unavailable")
				return
			}
			log.Warn("summarize", "err", serr)
			_ = w.Enrich.SetEnrichmentState(ctx, videoID, StateFailed, serr.Error())
			return
		}
		if err := w.Enrich.UpsertSummary(ctx, Summary{
			VideoID: videoID, Summary: sres.Summary, Notes: sres.Notes, Model: sres.Model,
		}); err != nil {
			log.Warn("save summary", "err", err)
			_ = w.Enrich.SetEnrichmentState(ctx, videoID, StateFailed, err.Error())
			return
		}
		if len(sres.Tags) > 0 {
			if err := w.Enrich.SetTags(ctx, videoID, sres.Tags, "auto"); err != nil {
				log.Warn("save tags", "err", err)
			}
		}
	}

	if err := w.Enrich.SetEnrichmentState(ctx, videoID, StateDone, ""); err != nil {
		log.Warn("set done", "err", err)
	}
}
