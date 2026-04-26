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
	ID            int64
	Handle        string
	DisplayName   sql.NullString
	ProfileURL    string
	AddedAt       time.Time
	LastFetchedAt sql.NullInt64
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

func (s *Store) List(ctx context.Context) ([]Creator, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, handle, display_name, profile_url, added_at, last_fetched_at FROM creators ORDER BY handle`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Creator
	for rows.Next() {
		var c Creator
		var added int64
		if err := rows.Scan(&c.ID, &c.Handle, &c.DisplayName, &c.ProfileURL, &added, &c.LastFetchedAt); err != nil {
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
		`SELECT id, handle, display_name, profile_url, added_at, last_fetched_at FROM creators WHERE id = ?`,
		id,
	).Scan(&c.ID, &c.Handle, &c.DisplayName, &c.ProfileURL, &added, &c.LastFetchedAt)
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
