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
	Aged     int // active → pending_deletion
	Archived int // pending_deletion → archived: media file removed, metadata kept for history
	Purged   int // archived rows hard-deleted past their metadata TTL
}

func (s *Sweeper) sweep(ctx context.Context) {
	res, err := s.SweepOnce(ctx)
	if err != nil {
		s.log().Warn("ttl sweep failed", "err", err)
		return
	}
	if res.Aged > 0 || res.Archived > 0 || res.Purged > 0 {
		s.log().Info("ttl sweep", "aged", res.Aged, "archived", res.Archived, "purged", res.Purged)
	}
}

// SweepOnce performs a single sweep pass and returns counts.
//
// Per-creator TTL override: when creators.ttl_days_override is non-null, it
// replaces the global ttl_days for that creator's videos. Implemented as
// SQL-side COALESCE so a single UPDATE handles both paths.
func (s *Sweeper) SweepOnce(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	ttl, grace := s.Settings.TTL(ctx)
	now := time.Now()
	globalTTLSeconds := int64(ttl.Seconds())

	// 1) age active → pending_deletion when downloaded_at older than the
	//    effective TTL (creator override falling back to global).
	r, err := s.DB.ExecContext(ctx, `
		UPDATE videos SET state = 'pending_deletion', state_changed_at = ?
		WHERE state = 'active'
		  AND downloaded_at < ? - (
		    SELECT COALESCE(c.ttl_days_override * 86400, ?)
		    FROM creators c WHERE c.id = videos.creator_id
		  )
	`, now.Unix(), now.Unix(), globalTTLSeconds)
	if err != nil {
		return res, err
	}
	if n, _ := r.RowsAffected(); n > 0 {
		res.Aged = int(n)
	}

	// 2) for pending_deletion videos whose state_changed_at is older than
	// `grace`, remove the *media file* from disk and flip state to
	// 'archived'. Metadata (title, description, summary, transcript, tags,
	// thumbnail) is retained indefinitely so the user can review history
	// and request a redownload from the original URL.
	graceCutoff := now.Add(-grace).Unix()
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, file_path FROM videos
		WHERE state = 'pending_deletion' AND state_changed_at < ?
	`, graceCutoff)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	type doomed struct {
		id   int64
		file string
	}
	var batch []doomed
	for rows.Next() {
		var d doomed
		if err := rows.Scan(&d.id, &d.file); err != nil {
			return res, err
		}
		batch = append(batch, d)
	}
	rows.Close()

	for _, d := range batch {
		if d.file != "" {
			if err := os.Remove(d.file); err != nil && !os.IsNotExist(err) {
				s.log().Warn("ttl: remove media", "id", d.id, "path", d.file, "err", err)
				continue
			}
		}
		// Thumbnail intentionally retained — it's small and helps the
		// history view stay visually scannable.
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE videos SET state = ?, state_changed_at = ? WHERE id = ?`,
			string(video.StateArchived), now.Unix(), d.id,
		); err != nil {
			s.log().Warn("ttl: archive row", "id", d.id, "err", err)
			continue
		}
		res.Archived++
	}

	// 3) metadata TTL: hard-purge archived rows whose state_changed_at is
	// older than the configured `metadata_ttl_days`. 0 means "unlimited" —
	// retain forever. Thumbnails on disk are removed alongside the row.
	metaTTL := s.Settings.MetadataTTL(ctx)
	if metaTTL > 0 {
		metaCutoff := now.Add(-metaTTL).Unix()
		thumbRows, err := s.DB.QueryContext(ctx, `
			SELECT id, COALESCE(thumbnail_path, '') FROM videos
			WHERE state = 'archived' AND state_changed_at < ?
		`, metaCutoff)
		if err != nil {
			return res, err
		}
		type doomedMeta struct {
			id    int64
			thumb string
		}
		var metaBatch []doomedMeta
		for thumbRows.Next() {
			var dm doomedMeta
			if err := thumbRows.Scan(&dm.id, &dm.thumb); err != nil {
				thumbRows.Close()
				return res, err
			}
			metaBatch = append(metaBatch, dm)
		}
		thumbRows.Close()

		for _, dm := range metaBatch {
			if dm.thumb != "" {
				_ = os.Remove(dm.thumb) // best-effort
			}
			// Cascade deletes on transcripts, summaries, video_tags,
			// watch_state via ON DELETE CASCADE in the schema.
			if _, err := s.DB.ExecContext(ctx, `DELETE FROM videos WHERE id = ?`, dm.id); err != nil {
				s.log().Warn("ttl: purge archived row", "id", dm.id, "err", err)
				continue
			}
			res.Purged++
		}
	}

	return res, nil
}
