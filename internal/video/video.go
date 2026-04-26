// Package video persists downloaded video metadata and watch state.
package video

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type State string

const (
	StateActive          State = "active"
	StateSaved           State = "saved"
	StatePendingDeletion State = "pending_deletion"
	// StateArchived: media file removed by the TTL sweeper, but metadata
	// (title, description, summary, transcript, tags, thumbnail) is kept
	// indefinitely. Re-watchable via redownload from the original URL.
	StateArchived State = "archived"
)

// Video is the domain type. Optional strings use empty-string-is-unset; an
// optional duration uses 0; optional timestamps use *time.Time. SQL nullables
// are confined to the store implementation. (ADR-012.)
type Video struct {
	ID              int64
	CreatorID       int64
	ExternalID      string
	URL             string
	Title           string
	Description     string
	DurationSeconds int64      // 0 = unknown
	PostedAt        *time.Time // nil = IG didn't expose the timestamp
	DownloadedAt    time.Time
	FilePath        string
	ThumbnailPath   string // empty = no thumbnail
	State           State
	StateChangedAt  time.Time
}

// Posted reports whether the IG-reported post time is known.
func (v Video) Posted() (time.Time, bool) {
	if v.PostedAt == nil {
		return time.Time{}, false
	}
	return *v.PostedAt, true
}

// SortKey returns the timestamp used to order videos newest-first; falls back
// to download time when the IG-reported post time is missing.
func (v Video) SortKey() time.Time {
	if t, ok := v.Posted(); ok {
		return t
	}
	return v.DownloadedAt
}

type WatchState struct {
	VideoID             int64
	LastPositionSeconds float64
	Watched             bool
	WatchedAt           *time.Time
	UpdatedAt           time.Time
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Insert persists a freshly-downloaded video. Returns the new ID, or
// ErrDuplicate if (creator_id, external_id) already exists.
func (s *Store) Insert(ctx context.Context, v *Video) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO videos(
			creator_id, external_id, url, title, description,
			duration_seconds, posted_at, downloaded_at, file_path,
			thumbnail_path, state, state_changed_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		v.CreatorID, v.ExternalID, v.URL,
		nullableString(v.Title), nullableString(v.Description),
		nullableInt64(v.DurationSeconds), nullableUnix(v.PostedAt), now, v.FilePath,
		nullableString(v.ThumbnailPath), string(StateActive), now,
	)
	if err != nil {
		if isUnique(err) {
			return 0, ErrDuplicate
		}
		return 0, fmt.Errorf("insert video: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (s *Store) Get(ctx context.Context, id int64) (*Video, error) {
	row := s.db.QueryRowContext(ctx, selectCols+` WHERE id = ?`, id)
	return scanVideo(row)
}

// ListForCreator returns the creator's videos newest-first. By default it
// hides pending-deletion and archived rows from the active feed; pass true
// for the corresponding flags to include them.
func (s *Store) ListForCreator(ctx context.Context, creatorID int64, includeDeletionPending bool) ([]Video, error) {
	return s.listForCreator(ctx, creatorID, includeDeletionPending, false)
}

// ListForCreatorAll returns every video for the creator, including pending
// and archived. Used by the history view.
func (s *Store) ListForCreatorAll(ctx context.Context, creatorID int64) ([]Video, error) {
	return s.listForCreator(ctx, creatorID, true, true)
}

func (s *Store) listForCreator(ctx context.Context, creatorID int64, includePending, includeArchived bool) ([]Video, error) {
	q := selectCols + ` WHERE creator_id = ?`
	args := []any{creatorID}
	excluded := []string{}
	if !includePending {
		excluded = append(excluded, string(StatePendingDeletion))
	}
	if !includeArchived {
		excluded = append(excluded, string(StateArchived))
	}
	for _, st := range excluded {
		q += ` AND state != ?`
		args = append(args, st)
	}
	q += ` ORDER BY COALESCE(posted_at, downloaded_at) DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// ExistingExternalIDs returns the set of external_ids already stored for a
// creator, so the ingester can skip re-downloading them.
func (s *Store) ExistingExternalIDs(ctx context.Context, creatorID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT external_id FROM videos WHERE creator_id = ?`, creatorID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out[s] = true
	}
	return out, rows.Err()
}

func (s *Store) SetState(ctx context.Context, id int64, state State) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE videos SET state = ?, state_changed_at = ? WHERE id = ?`,
		string(state), time.Now().Unix(), id,
	)
	return err
}

// UpdateAfterRedownload swaps the on-disk file path and resets state to
// active after the downloader has fetched a fresh copy. The download_at
// timestamp is bumped so the TTL clock starts over.
func (s *Store) UpdateAfterRedownload(ctx context.Context, id int64, filePath, thumbPath string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE videos
		   SET file_path = ?, thumbnail_path = COALESCE(?, thumbnail_path),
		       downloaded_at = ?, state = ?, state_changed_at = ?
		 WHERE id = ?
	`, filePath, nullableString(thumbPath), now, string(StateActive), now, id)
	return err
}

// ListByState returns all videos in the given state, newest first by
// state_changed_at — useful for the pending-deletion review page.
func (s *Store) ListByState(ctx context.Context, state State) ([]Video, error) {
	rows, err := s.db.QueryContext(ctx,
		selectCols+` WHERE state = ? ORDER BY state_changed_at DESC, id DESC`,
		string(state),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// --- watch state ---

func (s *Store) GetWatch(ctx context.Context, videoID int64) (*WatchState, error) {
	var w WatchState
	var watchedAt sql.NullInt64
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT video_id, last_position_seconds, watched, watched_at, updated_at FROM watch_state WHERE video_id = ?`,
		videoID,
	).Scan(&w.VideoID, &w.LastPositionSeconds, &w.Watched, &watchedAt, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return &WatchState{VideoID: videoID}, nil
	}
	if err != nil {
		return nil, err
	}
	if watchedAt.Valid {
		t := time.Unix(watchedAt.Int64, 0)
		w.WatchedAt = &t
	}
	w.UpdatedAt = time.Unix(updated, 0)
	return &w, nil
}

func (s *Store) UpsertPosition(ctx context.Context, videoID int64, position float64, watched bool) error {
	now := time.Now().Unix()
	var watchedAt sql.NullInt64
	if watched {
		watchedAt = sql.NullInt64{Int64: now, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO watch_state(video_id, last_position_seconds, watched, watched_at, updated_at)
			VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(video_id) DO UPDATE SET
			last_position_seconds = excluded.last_position_seconds,
			watched = watch_state.watched OR excluded.watched,
			watched_at = COALESCE(watch_state.watched_at, excluded.watched_at),
			updated_at = excluded.updated_at
	`, videoID, position, boolToInt(watched), watchedAt, now)
	return err
}

// LatestWatchedID returns the id of the most recently watched video for a
// creator, or 0 if none yet.
func (s *Store) LatestWatchedID(ctx context.Context, creatorID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT v.id
		FROM videos v
		JOIN watch_state w ON w.video_id = v.id
		WHERE v.creator_id = ? AND w.watched_at IS NOT NULL
		ORDER BY w.watched_at DESC
		LIMIT 1
	`, creatorID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// --- internals ---

const selectCols = `SELECT id, creator_id, external_id, url, title, description,
	duration_seconds, posted_at, downloaded_at, file_path, thumbnail_path,
	state, state_changed_at FROM videos`

type scanner interface {
	Scan(dest ...any) error
}

// ScanRow is the exported scan helper for callers (e.g. internal/groups)
// that build their own Video-shaped queries with the same column order as
// `selectCols`. The argument is anything implementing Scan — typically
// *sql.Rows in a Next() loop, or *sql.Row.
func ScanRow(s scanner) (*Video, error) { return scanVideo(s) }

func scanVideo(s scanner) (*Video, error) {
	var v Video
	var title, description, thumbnailPath sql.NullString
	var durationSeconds, postedAt sql.NullInt64
	var downloaded, stateChanged int64
	var state string
	if err := s.Scan(
		&v.ID, &v.CreatorID, &v.ExternalID, &v.URL, &title, &description,
		&durationSeconds, &postedAt, &downloaded, &v.FilePath, &thumbnailPath,
		&state, &stateChanged,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if title.Valid {
		v.Title = title.String
	}
	if description.Valid {
		v.Description = description.String
	}
	if thumbnailPath.Valid {
		v.ThumbnailPath = thumbnailPath.String
	}
	if durationSeconds.Valid {
		v.DurationSeconds = durationSeconds.Int64
	}
	if postedAt.Valid {
		t := time.Unix(postedAt.Int64, 0)
		v.PostedAt = &t
	}
	v.DownloadedAt = time.Unix(downloaded, 0)
	v.StateChangedAt = time.Unix(stateChanged, 0)
	v.State = State(state)
	return &v, nil
}

// nullableString returns a SQL NullString value driver-friendly: empty
// string becomes NULL, non-empty becomes Valid.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullableInt64 maps 0 → NULL, non-zero → Valid.
func nullableInt64(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}

// nullableUnix maps a *time.Time into a SQL NullInt64 (unix seconds).
func nullableUnix(t *time.Time) sql.NullInt64 {
	if t == nil || t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

func isUnique(err error) bool {
	return err != nil && (contains(err.Error(), "UNIQUE") || contains(err.Error(), "constraint"))
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var (
	ErrDuplicate = errors.New("video already exists")
	ErrNotFound  = errors.New("video not found")
)
