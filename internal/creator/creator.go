// Package creator manages the curated list of monitored Instagram accounts.
package creator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Creator is the domain type. Optional fields use the empty string or a nil
// time.Time pointer to signal absence; SQL nullables are confined to the
// store implementation. (ADR-012.)
type Creator struct {
	ID                  int64
	Handle              string
	DisplayName         string     // empty = unset
	ProfileURL          string
	AddedAt             time.Time
	LastFetchedAt       *time.Time // nil = never fetched
	PollIntervalSeconds int64      // 0 = use global default
	FollowedAt          *time.Time // nil = unknown (e.g. manually added)
}

// Followed reports whether the creator has a known follow timestamp.
func (c Creator) Followed() (time.Time, bool) {
	if c.FollowedAt == nil {
		return time.Time{}, false
	}
	return *c.FollowedAt, true
}

// LastFetched reports whether the creator has ever been polled.
func (c Creator) LastFetched() (time.Time, bool) {
	if c.LastFetchedAt == nil {
		return time.Time{}, false
	}
	return *c.LastFetchedAt, true
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Add inserts a creator from a raw input. The input may be a bare handle
// ("@natgeo", "natgeo") or a full instagram.com URL. Duplicates return ErrExists.
func (s *Store) Add(ctx context.Context, input, displayName string) (*Creator, error) {
	handle, profileURL, err := parseInput(input)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()

	var dn sql.NullString
	displayName = strings.TrimSpace(displayName)
	if displayName != "" {
		dn = sql.NullString{String: displayName, Valid: true}
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO creators(handle, display_name, profile_url, added_at) VALUES(?, ?, ?, ?)`,
		handle, dn, profileURL, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrExists
		}
		return nil, fmt.Errorf("insert creator: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Creator{
		ID:          id,
		Handle:      handle,
		DisplayName: displayName,
		ProfileURL:  profileURL,
		AddedAt:     time.Unix(now, 0),
	}, nil
}

type SortOrder string

const (
	SortHandle      SortOrder = "handle"
	SortAdded       SortOrder = "added"
	SortLastFetched SortOrder = "last_fetched"
	SortFollowed    SortOrder = "followed"
)

func (s *Store) List(ctx context.Context, order SortOrder) ([]Creator, error) {
	orderBy := "handle COLLATE NOCASE"
	switch order {
	case SortAdded:
		orderBy = "added_at DESC"
	case SortLastFetched:
		orderBy = "last_fetched_at IS NULL, last_fetched_at DESC"
	case SortFollowed:
		orderBy = "followed_at IS NULL, followed_at DESC"
	}
	rows, err := s.db.QueryContext(ctx, selectCols+` ORDER BY `+orderBy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Creator
	for rows.Next() {
		c, err := scanCreator(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (*Creator, error) {
	row := s.db.QueryRowContext(ctx, selectCols+` WHERE id = ?`, id)
	c, err := scanCreator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) MarkFetched(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE creators SET last_fetched_at = ? WHERE id = ?`,
		time.Now().Unix(), id,
	)
	return err
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM creators WHERE id = ?`, id)
	return err
}

func (s *Store) UpdateDisplayName(ctx context.Context, id int64, name string) error {
	var dn sql.NullString
	if name = strings.TrimSpace(name); name != "" {
		dn = sql.NullString{String: name, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE creators SET display_name = ? WHERE id = ?`, dn, id)
	return err
}

// SetPollIntervalSeconds sets the per-creator polling cadence override. Pass
// 0 (or negative) to clear the override and revert to the global default.
func (s *Store) SetPollIntervalSeconds(ctx context.Context, id int64, seconds int64) error {
	if seconds <= 0 {
		_, err := s.db.ExecContext(ctx, `UPDATE creators SET poll_interval_seconds = NULL WHERE id = ?`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE creators SET poll_interval_seconds = ? WHERE id = ?`,
		seconds, id,
	)
	return err
}

// BulkAddResult summarizes a bulk-add operation. Errors per line so the
// caller can surface a partial-success message in the UI.
type BulkAddResult struct {
	Added    []Creator
	Skipped  []string // already-existed handles
	Failures []BulkFailure
}

type BulkFailure struct {
	Input string
	Err   string
}

// BulkAdd parses the input string as one-handle-per-line, ignores blank
// lines and lines starting with '#', and adds each.
func (s *Store) BulkAdd(ctx context.Context, raw string) BulkAddResult {
	var res BulkAddResult
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var handle, display string
		if i := strings.Index(line, ","); i >= 0 {
			handle = strings.TrimSpace(line[:i])
			display = strings.TrimSpace(line[i+1:])
		} else {
			handle = line
		}
		c, err := s.Add(ctx, handle, display)
		if err != nil {
			if errors.Is(err, ErrExists) {
				res.Skipped = append(res.Skipped, handle)
				continue
			}
			res.Failures = append(res.Failures, BulkFailure{Input: line, Err: err.Error()})
			continue
		}
		res.Added = append(res.Added, *c)
	}
	return res
}

// ImportEntry is one record from an Instagram data-export following.json.
type ImportEntry struct {
	Handle     string
	FollowedAt time.Time // zero if unknown
}

// ImportFollowing inserts (or upserts the followed_at on existing rows)
// each entry. Returns counts.
func (s *Store) ImportFollowing(ctx context.Context, entries []ImportEntry) (added, updated int, failures []BulkFailure) {
	for _, e := range entries {
		handle := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(e.Handle, "@")))
		if handle == "" {
			continue
		}
		profileURL := "https://www.instagram.com/" + handle + "/"
		var followedAt sql.NullInt64
		if !e.FollowedAt.IsZero() {
			followedAt = sql.NullInt64{Int64: e.FollowedAt.Unix(), Valid: true}
		}
		now := time.Now().Unix()
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO creators(handle, profile_url, added_at, followed_at) VALUES(?, ?, ?, ?)
			ON CONFLICT(handle) DO UPDATE SET followed_at = COALESCE(excluded.followed_at, creators.followed_at)
		`, handle, profileURL, now, followedAt)
		if err != nil {
			failures = append(failures, BulkFailure{Input: handle, Err: err.Error()})
			continue
		}
		if n, _ := res.RowsAffected(); n == 1 {
			var existingAdded int64
			_ = s.db.QueryRowContext(ctx, `SELECT added_at FROM creators WHERE handle = ?`, handle).Scan(&existingAdded)
			if existingAdded == now {
				added++
			} else {
				updated++
			}
		}
	}
	return
}

var (
	ErrExists   = errors.New("creator already exists")
	ErrNotFound = errors.New("creator not found")
)

// --- internal scan/parse helpers ---

const selectCols = `SELECT id, handle, display_name, profile_url, added_at, last_fetched_at, poll_interval_seconds, followed_at FROM creators`

type scanner interface {
	Scan(...any) error
}

// scanCreator reads one row of selectCols into a domain Creator. Nullable
// SQL fields are translated at the seam.
func scanCreator(s scanner) (*Creator, error) {
	var c Creator
	var displayName sql.NullString
	var added int64
	var lastFetched, followedAt, pollInterval sql.NullInt64
	if err := s.Scan(&c.ID, &c.Handle, &displayName, &c.ProfileURL, &added, &lastFetched, &pollInterval, &followedAt); err != nil {
		return nil, err
	}
	c.AddedAt = time.Unix(added, 0)
	if displayName.Valid {
		c.DisplayName = displayName.String
	}
	if lastFetched.Valid {
		t := time.Unix(lastFetched.Int64, 0)
		c.LastFetchedAt = &t
	}
	if followedAt.Valid {
		t := time.Unix(followedAt.Int64, 0)
		c.FollowedAt = &t
	}
	if pollInterval.Valid {
		c.PollIntervalSeconds = pollInterval.Int64
	}
	return &c, nil
}

// parseInput normalizes "@natgeo", "natgeo", or "https://instagram.com/natgeo/"
// into (handle, canonical profile URL).
func parseInput(in string) (handle, profileURL string, err error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", "", errors.New("empty handle")
	}
	in = strings.TrimPrefix(in, "@")
	if strings.Contains(in, "instagram.com") {
		i := strings.Index(in, "instagram.com/")
		if i < 0 {
			return "", "", fmt.Errorf("malformed instagram url: %s", in)
		}
		rest := in[i+len("instagram.com/"):]
		rest = strings.TrimPrefix(rest, "_u/")
		rest = strings.Trim(rest, "/")
		if j := strings.Index(rest, "/"); j >= 0 {
			rest = rest[:j]
		}
		if q := strings.Index(rest, "?"); q >= 0 {
			rest = rest[:q]
		}
		handle = rest
	} else {
		handle = in
	}
	handle = strings.ToLower(strings.TrimSpace(handle))
	if handle == "" || strings.ContainsAny(handle, " /?#") {
		return "", "", fmt.Errorf("invalid handle: %q", in)
	}
	return handle, "https://www.instagram.com/" + handle + "/", nil
}
