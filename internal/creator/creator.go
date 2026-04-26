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

type Creator struct {
	ID                  int64
	Handle              string
	DisplayName         sql.NullString
	ProfileURL          string
	AddedAt             time.Time
	LastFetchedAt       sql.NullInt64
	PollIntervalSeconds sql.NullInt64
	FollowedAt          sql.NullInt64 // unix seconds, from IG data-export import
}

func (c Creator) LastFetched() (time.Time, bool) {
	if !c.LastFetchedAt.Valid {
		return time.Time{}, false
	}
	return time.Unix(c.LastFetchedAt.Int64, 0), true
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
	if displayName = strings.TrimSpace(displayName); displayName != "" {
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
	return &Creator{ID: id, Handle: handle, DisplayName: dn, ProfileURL: profileURL, AddedAt: time.Unix(now, 0)}, nil
}

type SortOrder string

const (
	SortHandle      SortOrder = "handle"
	SortAdded       SortOrder = "added"
	SortLastFetched SortOrder = "last_fetched"
	SortFollowed    SortOrder = "followed" // most recently followed on IG first
)

func (s *Store) List(ctx context.Context, order SortOrder) ([]Creator, error) {
	orderBy := "handle COLLATE NOCASE"
	switch order {
	case SortAdded:
		orderBy = "added_at DESC"
	case SortLastFetched:
		// NULL last_fetched_at sorts at the end.
		orderBy = "last_fetched_at IS NULL, last_fetched_at DESC"
	case SortFollowed:
		// NULL followed_at sorts at the end (manually-added creators).
		orderBy = "followed_at IS NULL, followed_at DESC"
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, handle, display_name, profile_url, added_at, last_fetched_at, poll_interval_seconds, followed_at FROM creators ORDER BY `+orderBy,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Creator
	for rows.Next() {
		var c Creator
		var added int64
		if err := rows.Scan(&c.ID, &c.Handle, &c.DisplayName, &c.ProfileURL, &added, &c.LastFetchedAt, &c.PollIntervalSeconds, &c.FollowedAt); err != nil {
			return nil, err
		}
		c.AddedAt = time.Unix(added, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (*Creator, error) {
	var c Creator
	var added int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, handle, display_name, profile_url, added_at, last_fetched_at, poll_interval_seconds, followed_at FROM creators WHERE id = ?`,
		id,
	).Scan(&c.ID, &c.Handle, &c.DisplayName, &c.ProfileURL, &added, &c.LastFetchedAt, &c.PollIntervalSeconds, &c.FollowedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.AddedAt = time.Unix(added, 0)
	return &c, nil
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

// ImportEntry is one record from an Instagram data-export following.json.
type ImportEntry struct {
	Handle     string
	FollowedAt time.Time // zero if unknown
}

// ImportFollowing inserts (or upserts the followed_at on existing rows)
// each entry. Returns counts. Used by the data-export importer.
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
			// Could be insert or update; differentiate via a quick lookup.
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

// BulkAddResult summarizes a bulk-add operation. Errors per line so the
// caller can surface a partial-success message in the UI.
type BulkAddResult struct {
	Added     []Creator
	Skipped   []string // already-existed handles
	Failures  []BulkFailure
}

type BulkFailure struct {
	Input string
	Err   string
}

// BulkAdd parses the input string as one-handle-per-line, ignores blank
// lines and lines starting with '#', and adds each. Duplicates are silently
// skipped (recorded in Skipped). Per-line failures don't abort the batch.
func (s *Store) BulkAdd(ctx context.Context, raw string) BulkAddResult {
	var res BulkAddResult
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Allow "handle, Display Name" syntax for CSV-style input.
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

var (
	ErrExists   = errors.New("creator already exists")
	ErrNotFound = errors.New("creator not found")
)

// parseInput normalizes "@natgeo", "natgeo", or "https://instagram.com/natgeo/"
// into (handle, canonical profile URL).
func parseInput(in string) (handle, profileURL string, err error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", "", errors.New("empty handle")
	}
	in = strings.TrimPrefix(in, "@")
	if strings.Contains(in, "instagram.com") {
		// Pull the first path segment after the host.
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
