// Package ttl runs the periodic sweep that ages active videos into
// pending_deletion and finalizes pending_deletion videos that have outlived
// their grace window.
package ttl

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"time"

	"github.com/ianfoo/feedrig/internal/settings"
	"github.com/ianfoo/feedrig/internal/video"
)

type Sweeper struct {
	DB       *sql.DB
	Videos   *video.Store
	Settings *settings.Store
	Log      *slog.Logger
}

func (s *Sweeper) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Run executes one sweep on startup, then on a periodic ticker until ctx is canceled.
func (s *Sweeper) Run(ctx context.Context) {
	s.sweep(ctx)
	t := time.NewTicker(s.Settings.SweepInterval(ctx))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

// SweepResult is exposed for testing / introspection.
type SweepResult struct {
	Aged    int // active → pending_deletion
	Removed int // pending_deletion → file removed (state stays pending_deletion in DB; row pruning is a separate pass)
	Pruned  int // pending_deletion rows whose files no longer exist and which are past grace window
}

func (s *Sweeper) sweep(ctx context.Context) {
	res, err := s.SweepOnce(ctx)
	if err != nil {
		s.log().Warn("ttl sweep failed", "err", err)
		return
	}
	if res.Aged > 0 || res.Removed > 0 || res.Pruned > 0 {
		s.log().Info("ttl sweep", "aged", res.Aged, "removed", res.Removed, "pruned", res.Pruned)
	}
}

// SweepOnce performs a single sweep pass and returns counts.
func (s *Sweeper) SweepOnce(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	ttl, grace := s.Settings.TTL(ctx)
	now := time.Now()

	// 1) age active → pending_deletion when downloaded_at older than ttl.
	cutoff := now.Add(-ttl).Unix()
	r, err := s.DB.ExecContext(ctx, `
		UPDATE videos SET state = 'pending_deletion', state_changed_at = ?
		WHERE state = 'active' AND downloaded_at < ?
	`, now.Unix(), cutoff)
	if err != nil {
		return res, err
	}
	if n, _ := r.RowsAffected(); n > 0 {
		res.Aged = int(n)
	}

	// 2) for pending_deletion videos older than (ttl + grace) since
	// state_changed_at, remove the file from disk and mark state_changed_at
	// to "now" so we know the file's gone. We don't hard-delete the row yet —
	// retaining metadata after media removal lets the user see "deleted" in
	// the UI without storage cost. (TODO v0.3.x: a separate config knob to
	// hard-delete N days after file removal.)
	graceCutoff := now.Add(-grace).Unix()
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, file_path, thumbnail_path FROM videos
		WHERE state = 'pending_deletion' AND state_changed_at < ?
	`, graceCutoff)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	type doomed struct {
		id    int64
		file  string
		thumb sql.NullString
	}
	var batch []doomed
	for rows.Next() {
		var d doomed
		if err := rows.Scan(&d.id, &d.file, &d.thumb); err != nil {
			return res, err
		}
		batch = append(batch, d)
	}
	rows.Close()

	for _, d := range batch {
		if d.file != "" {
			if err := os.Remove(d.file); err == nil || os.IsNotExist(err) {
				res.Removed++
			} else {
				s.log().Warn("ttl: remove media", "id", d.id, "path", d.file, "err", err)
			}
		}
		if d.thumb.Valid && d.thumb.String != "" {
			_ = os.Remove(d.thumb.String)
		}
		// Hard-delete the row. The user has had their grace window;
		// after that the metadata is no longer useful and just clutters lists.
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM videos WHERE id = ?`, d.id); err != nil {
			s.log().Warn("ttl: delete row", "id", d.id, "err", err)
			continue
		}
		res.Pruned++
	}

	return res, nil
}
