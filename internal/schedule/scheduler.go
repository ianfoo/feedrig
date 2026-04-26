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

// Scheduler launches one polling goroutine per creator. It is safe to
// restart on creator add / delete by replacing the *Scheduler value; in v0.3
// we don't yet do hot-reload — a server restart picks up newly-added
// creators. (TODO v0.3.x: subscribe to creator add/delete events.)
type Scheduler struct {
	Creators *creator.Store
	Settings *settings.Store
	Ingest   *ingest.Service
	Log      *slog.Logger

	wg sync.WaitGroup
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Run launches a goroutine per creator and blocks until ctx is canceled.
// Each goroutine waits a randomized fraction of its interval before its
// first run to avoid a thundering-herd burst on startup.
func (s *Scheduler) Run(ctx context.Context) {
	creators, err := s.Creators.List(ctx, creator.SortHandle)
	if err != nil {
		s.log().Error("scheduler: load creators", "err", err)
		return
	}
	defaultInt := s.Settings.PollInterval(ctx)
	for _, c := range creators {
		c := c
		interval := defaultInt
		if c.PollIntervalSeconds.Valid && c.PollIntervalSeconds.Int64 > 0 {
			interval = time.Duration(c.PollIntervalSeconds.Int64) * time.Second
		}
		s.wg.Add(1)
		go s.runOne(ctx, c, interval)
	}
	<-ctx.Done()
	s.wg.Wait()
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
