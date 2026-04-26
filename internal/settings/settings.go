// Package settings is a typed wrapper around the settings KV table.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Keys ---------------------------------------------------------------

const (
	KeyDefaultPollInterval = "default_poll_interval_seconds"
	KeyTTLDays             = "ttl_days"
	KeyGraceDays           = "grace_days"
	KeyMetadataTTLDays     = "metadata_ttl_days"
	KeyTTLSweepInterval    = "ttl_sweep_interval_seconds"
)

// Day is a named constant since Go's time package conspicuously omits one,
// and we deal in days a lot. Keeps callers free of bare 24*time.Hour.
const Day = 24 * time.Hour

// Defaults — used by getters when the key is absent.
const (
	DefaultPollInterval  = 6 * time.Hour
	DefaultTTLDays       = 30
	DefaultGraceDays     = 7
	DefaultMetadataTTL   = 365 // days; 0 = unlimited
	DefaultSweepInterval = 1 * time.Hour
)

// DurationFromDays / DaysFromDuration are the small conversion helpers used
// throughout the app to keep day-vs-hours-vs-seconds arithmetic localized.
func DurationFromDays(d int) time.Duration { return time.Duration(d) * Day }
func DaysFromDuration(d time.Duration) int { return int(d / Day) }

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// PollInterval returns the configured global default poll interval.
func (s *Store) PollInterval(ctx context.Context) time.Duration {
	v, _ := s.Get(ctx, KeyDefaultPollInterval)
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return DefaultPollInterval
}

func (s *Store) TTL(ctx context.Context) (ttl, grace time.Duration) {
	ttlV, _ := s.Get(ctx, KeyTTLDays)
	graceV, _ := s.Get(ctx, KeyGraceDays)
	ttl = DurationFromDays(intOr(ttlV, DefaultTTLDays))
	grace = DurationFromDays(intOr(graceV, DefaultGraceDays))
	return
}

// MetadataTTL returns the configured age (as a Duration) past which an
// archived video's metadata row is hard-deleted. A return value of 0 means
// "unlimited — never purge metadata."
func (s *Store) MetadataTTL(ctx context.Context) time.Duration {
	v, _ := s.Get(ctx, KeyMetadataTTLDays)
	// We allow an explicit "0" here to mean unlimited; intOr's > 0 guard
	// would otherwise return the default. Read manually.
	if v == "0" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return DurationFromDays(n)
	}
	return DurationFromDays(DefaultMetadataTTL)
}

func (s *Store) SweepInterval(ctx context.Context) time.Duration {
	v, _ := s.Get(ctx, KeyTTLSweepInterval)
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return DefaultSweepInterval
}

func intOr(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return fallback
}
