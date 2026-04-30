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

// Run starts the worker loop. The startup rescan also picks up 'running'
// and 'failed' rows so a previous-instance crash recovers cleanly:
// 'running' = the previous worker died mid-pipeline; 'failed' = previous
// run errored and we'll retry once across this restart boundary. The
// periodic rescan is more conservative (pending + running only) so failed
// rows don't loop forever within a single process lifetime.
func (w *Worker) Run(ctx context.Context) {
	w.ensureInit()
	w.wg.Add(1)
	defer w.wg.Done()

	w.rescan(ctx, true) // startup: include 'failed' for crash recovery
	rescanTicker := time.NewTicker(rescanInterval)
	defer rescanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rescanTicker.C:
			w.rescan(ctx, false)
		case id := <-w.queue:
			w.process(ctx, id)
		}
	}
}

const rescanInterval = 30 * time.Second

// rescan re-enqueues any DB rows the worker should pick up. includeFailed
// is true at startup (crash recovery) and false during steady-state ticks.
func (w *Worker) rescan(ctx context.Context, includeFailed bool) {
	ids, err := w.Enrich.PendingVideoIDs(ctx, includeFailed)
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

// ProcessOne runs the enrichment pipeline synchronously on a single video.
// Suited for the `feedrig enrich <id>` subcommand: cron / one-shot
// invocations that want a definite return rather than a long-running queue.
// Returns when the pipeline finishes or context is canceled.
func (w *Worker) ProcessOne(ctx context.Context, videoID int64) {
	w.process(ctx, videoID)
}

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
	log = log.With("title", v.Title)
	log.Info("enrich starting")
	enrichStart := time.Now()

	in := summarize.Input{
		Title:       v.Title,
		Description: v.Description,
		Categories:  w.Categories,
	}

	// Step 1: transcribe (best-effort).
	if w.Transcriber != nil {
		log.Info("transcribe starting")
		tStart := time.Now()
		tctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		tres, terr := w.Transcriber.Transcribe(tctx, v.FilePath)
		cancel()
		tElapsed := time.Since(tStart).Round(time.Millisecond)
		switch {
		case terr == nil && tres != nil:
			if err := w.Enrich.UpsertTranscript(ctx, Transcript{
				VideoID: videoID, Text: tres.Text, Language: tres.Language, Model: tres.Model,
			}); err != nil {
				log.Warn("save transcript", "err", err)
			}
			in.Transcript = tres.Text
			log.Info("transcribe completed", "model", tres.Model, "chars", len(tres.Text), "elapsed", tElapsed)
		case errors.Is(terr, transcribe.ErrUnavailable):
			log.Info("transcribe skipped", "reason", "unavailable", "elapsed", tElapsed)
		default:
			log.Warn("transcribe failed", "elapsed", tElapsed, "err", terr)
		}
	}

	// Step 2: summarize.
	if w.Summarizer != nil {
		log.Info("summarize starting")
		sStart := time.Now()
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		sres, serr := w.Summarizer.Summarize(sctx, in)
		cancel()
		sElapsed := time.Since(sStart).Round(time.Millisecond)
		if serr != nil {
			if errors.Is(serr, summarize.ErrUnavailable) {
				log.Info("summarize skipped", "reason", "unavailable", "elapsed", sElapsed)
				_ = w.Enrich.SetEnrichmentState(ctx, videoID, StateSkipped, "summarizer unavailable")
				return
			}
			log.Warn("summarize failed", "elapsed", sElapsed, "err", serr)
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
		log.Info("summarize completed", "model", sres.Model, "tags", len(sres.Tags), "elapsed", sElapsed)
	}

	if err := w.Enrich.SetEnrichmentState(ctx, videoID, StateDone, ""); err != nil {
		log.Warn("save final state", "err", err)
	}
	log.Info("enrich completed", "elapsed", time.Since(enrichStart).Round(time.Millisecond))
}
