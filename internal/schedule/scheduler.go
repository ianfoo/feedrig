// Package schedule periodically polls each creator for new posts.
package schedule

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/ingest"
	"github.com/ianfoo/feedrig/internal/settings"
)

// Scheduler launches one polling goroutine per creator. v0.6 adds hot
// reload: handlers that mutate the creator set (add, delete, cadence
// override) call Reload(), which cancels the existing per-creator workers
// and respawns from the current DB state. The reload is a stop-the-world
// operation but takes milliseconds — fine for the single-user model.
type Scheduler struct {
	Creators *creator.Store
	Settings *settings.Store
	Ingest   *ingest.Service
	Log      *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	rootCtx context.Context
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Run blocks until rootCtx (the parent passed to Run) is canceled. Internally
// it spawns the per-creator workers; Reload() cancels and respawns them.
func (s *Scheduler) Run(rootCtx context.Context) {
	s.mu.Lock()
	s.rootCtx = rootCtx
	s.mu.Unlock()
	s.spawn()
	<-rootCtx.Done()
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Reload cancels the current per-creator workers and respawns from a fresh
// DB read. Idempotent and safe to call from any goroutine.
func (s *Scheduler) Reload() {
	s.mu.Lock()
	if s.rootCtx == nil {
		// Run hasn't been called yet; nothing to reload.
		s.mu.Unlock()
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.spawn()
}

func (s *Scheduler) spawn() {
	s.mu.Lock()
	rootCtx := s.rootCtx
	if rootCtx == nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(rootCtx)
	s.cancel = cancel
	s.mu.Unlock()

	creators, err := s.Creators.List(ctx, creator.SortHandle)
	if err != nil {
		s.log().Error("scheduler: load creators", "err", err)
		return
	}
	defaultInt := s.Settings.PollInterval(ctx)
	for _, c := range creators {
		c := c
		interval := defaultInt
		if c.PollIntervalSeconds > 0 {
			interval = time.Duration(c.PollIntervalSeconds) * time.Second
		}
		s.wg.Add(1)
		go s.runOne(ctx, c, interval)
	}
}

func (s *Scheduler) runOne(ctx context.Context, c creator.Creator, interval time.Duration) {
	defer s.wg.Done()

	// Initial jitter: 0–60% of interval, capped at 5 minutes for short intervals.
	jitter := time.Duration(rand.Int64N(int64(interval) / 2))
	if jitter > 5*time.Minute {
		jitter = 5 * time.Minute
	}
	timer := time.NewTimer(jitter)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		runCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		added, err := s.Ingest.FetchNewForCreator(runCtx, &c)
		cancel()
		if err != nil {
			s.log().Warn("scheduled fetch failed", "creator", c.Handle, "err", err)
		} else if added > 0 {
			s.log().Info("scheduled fetch added videos", "creator", c.Handle, "count", added)
		}
		// Reset for next cycle with small jitter (±10%).
		j := time.Duration(rand.Int64N(int64(interval) / 5))
		next := interval - interval/10 + j
		timer.Reset(next)
	}
}
