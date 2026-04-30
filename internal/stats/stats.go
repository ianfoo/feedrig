// Package stats computes lightweight library-level metrics on demand.
// Today: total bytes used by media + a few state counts. Cached behind a
// short TTL so the topbar/footer can display them on every page render
// without stat'ing the filesystem each time.
package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type Snapshot struct {
	MediaBytes    int64
	VideoCount    int
	SavedCount    int
	ArchivedCount int
	PendingCount  int
	GeneratedAt   time.Time
}

// HumanBytes formats a byte count as a short string (e.g. "1.2 GB").
// 1024-based to match what users expect from disk-usage tools.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	suffixes := [...]string{"KB", "MB", "GB", "TB", "PB"}
	div, exp := int64(unit), 0
	for cur := n / unit; cur >= unit && exp < len(suffixes)-1; cur /= unit {
		div *= unit
		exp++
	}
	val := float64(n) / float64(div)
	if val < 10 {
		return fmt.Sprintf("%.1f %s", val, suffixes[exp])
	}
	return fmt.Sprintf("%d %s", int64(val+0.5), suffixes[exp])
}

// Computer caches a Snapshot for TTL so the on-page rendering path doesn't
// re-walk the media filesystem on every request. Safe for concurrent use.
type Computer struct {
	DB        *sql.DB
	MediaRoot string
	TTL       time.Duration // defaults to 30s

	mu       sync.Mutex
	cached   Snapshot
	cachedAt time.Time
}

// Latest returns the cached snapshot if fresh; otherwise recomputes.
func (c *Computer) Latest(ctx context.Context) Snapshot {
	c.mu.Lock()
	if !c.cachedAt.IsZero() && time.Since(c.cachedAt) < c.ttl() {
		s := c.cached
		c.mu.Unlock()
		return s
	}
	c.mu.Unlock()

	s := c.compute(ctx)
	c.mu.Lock()
	c.cached = s
	c.cachedAt = time.Now()
	c.mu.Unlock()
	return s
}

// Invalidate forces the next Latest() call to recompute.
func (c *Computer) Invalidate() {
	c.mu.Lock()
	c.cachedAt = time.Time{}
	c.mu.Unlock()
}

func (c *Computer) ttl() time.Duration {
	if c.TTL <= 0 {
		return 30 * time.Second
	}
	return c.TTL
}

func (c *Computer) compute(ctx context.Context) Snapshot {
	s := Snapshot{GeneratedAt: time.Now()}

	if c.MediaRoot != "" {
		s.MediaBytes, _ = walkSize(c.MediaRoot)
	}

	if c.DB != nil {
		row := c.DB.QueryRowContext(ctx, `
			SELECT
				COUNT(*),
				SUM(CASE WHEN state = 'saved' THEN 1 ELSE 0 END),
				SUM(CASE WHEN state = 'archived' THEN 1 ELSE 0 END),
				SUM(CASE WHEN state = 'pending_deletion' THEN 1 ELSE 0 END)
			FROM videos
		`)
		var saved, archived, pending sql.NullInt64
		err := row.Scan(&s.VideoCount, &saved, &archived, &pending)
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			if saved.Valid {
				s.SavedCount = int(saved.Int64)
			}
			if archived.Valid {
				s.ArchivedCount = int(archived.Int64)
			}
			if pending.Valid {
				s.PendingCount = int(pending.Int64)
			}
		}
	}

	return s
}

func walkSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort; tolerate unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return total, err
	}
	return total, nil
}
