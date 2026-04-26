// Package groups manages smart-playlist groups: a named bundle of
// creators (with optional exclusions), a recency window, and tag filters.
// Smart playlists are queries — results are computed from the videos table
// on demand and never materialized.
package groups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ianfoo/feedrig/internal/settings"
	"github.com/ianfoo/feedrig/internal/video"
)

// DefaultRecencyDays is used when a group is created without an explicit
// window or with a non-positive value.
const DefaultRecencyDays = 7

type Group struct {
	ID            int64
	Name          string
	Slug          string
	RecencyDays   int
	IncludeTags   []string
	ExcludeTags   []string
	Position      int
	LastVisitedAt sql.NullInt64
	CreatedAt     time.Time
}

type Membership struct {
	GroupID   int64
	CreatorID int64
	Excluded  bool
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// CRUD ---------------------------------------------------------------

func (s *Store) Create(ctx context.Context, name string, recency int, include, exclude []string) (*Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name required")
	}
	if recency <= 0 {
		recency = DefaultRecencyDays
	}
	slug := slugify(name)
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO groups(name, slug, recency_days, include_tags, exclude_tags, position, created_at)
		VALUES(?, ?, ?, ?, ?, COALESCE((SELECT MAX(position)+1 FROM groups), 0), ?)
	`, name, slug, recency, joinCSV(include), joinCSV(exclude), now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrSlugExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Get(ctx, id)
}

func (s *Store) Update(ctx context.Context, id int64, name string, recency int, include, exclude []string) error {
	if recency <= 0 {
		recency = DefaultRecencyDays
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE groups SET name = ?, recency_days = ?, include_tags = ?, exclude_tags = ?
		WHERE id = ?
	`, strings.TrimSpace(name), recency, joinCSV(include), joinCSV(exclude), id)
	return err
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	return err
}

func (s *Store) Get(ctx context.Context, id int64) (*Group, error) {
	var g Group
	var inc, exc string
	var created int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, recency_days, include_tags, exclude_tags, position, last_visited_at, created_at
		FROM groups WHERE id = ?
	`, id).Scan(&g.ID, &g.Name, &g.Slug, &g.RecencyDays, &inc, &exc, &g.Position, &g.LastVisitedAt, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.IncludeTags = splitCSV(inc)
	g.ExcludeTags = splitCSV(exc)
	g.CreatedAt = time.Unix(created, 0)
	return &g, nil
}

func (s *Store) GetBySlug(ctx context.Context, slug string) (*Group, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM groups WHERE slug = ?`, slug).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Store) List(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, slug, recency_days, include_tags, exclude_tags, position, last_visited_at, created_at
		FROM groups ORDER BY position, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var inc, exc string
		var created int64
		if err := rows.Scan(&g.ID, &g.Name, &g.Slug, &g.RecencyDays, &inc, &exc, &g.Position, &g.LastVisitedAt, &created); err != nil {
			return nil, err
		}
		g.IncludeTags = splitCSV(inc)
		g.ExcludeTags = splitCSV(exc)
		g.CreatedAt = time.Unix(created, 0)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) MarkVisited(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE groups SET last_visited_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// Membership ---------------------------------------------------------

func (s *Store) SetMembers(ctx context.Context, groupID int64, includedIDs, excludedIDs []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_creator_memberships WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	for _, cid := range includedIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_creator_memberships(group_id, creator_id, excluded) VALUES(?, ?, 0) ON CONFLICT DO NOTHING`, groupID, cid); err != nil {
			return err
		}
	}
	for _, cid := range excludedIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_creator_memberships(group_id, creator_id, excluded) VALUES(?, ?, 1) ON CONFLICT DO NOTHING`, groupID, cid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Members(ctx context.Context, groupID int64) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT group_id, creator_id, excluded FROM group_creator_memberships WHERE group_id = ? ORDER BY creator_id
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		var ex int
		if err := rows.Scan(&m.GroupID, &m.CreatorID, &ex); err != nil {
			return nil, err
		}
		m.Excluded = ex == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// Feed ---------------------------------------------------------------

// WatchedFilter narrows a feed by per-video watched state.
type WatchedFilter string

const (
	WatchedAny       WatchedFilter = ""          // no filter
	WatchedOnly      WatchedFilter = "watched"   // only videos marked watched
	WatchedUnwatched WatchedFilter = "unwatched" // only videos NOT marked watched
)

// FeedQuery customizes a smart-playlist read. UI surfaces these as filter
// chips so the user can narrow within a group without redefining it.
type FeedQuery struct {
	OnlyUnseen   bool          // limit to videos posted after group.last_visited_at
	OnlyNew      bool          // alias for OnlyUnseen
	TagsAny      []string      // additional category narrowing (must match at least one)
	Watched      WatchedFilter // per-video watched-state filter
	MinDuration  int           // seconds; 0 = no minimum
	MaxDuration  int           // seconds; 0 = no maximum
	Limit        int           // 0 = no limit
}

// Feed returns the videos that match the group's definition + the optional
// FeedQuery overrides. Results are newest-first.
func (s *Store) Feed(ctx context.Context, g *Group, q FeedQuery) ([]video.Video, error) {
	since := time.Now().Add(-settings.DurationFromDays(g.RecencyDays)).Unix()
	if (q.OnlyUnseen || q.OnlyNew) && g.LastVisitedAt.Valid && g.LastVisitedAt.Int64 > since {
		since = g.LastVisitedAt.Int64
	}

	var args []any
	sb := strings.Builder{}
	sb.WriteString(`
		SELECT v.id, v.creator_id, v.external_id, v.url, v.title, v.description,
		       v.duration_seconds, v.posted_at, v.downloaded_at, v.file_path,
		       v.thumbnail_path, v.state, v.state_changed_at
		FROM videos v
		JOIN group_creator_memberships m
		  ON m.creator_id = v.creator_id AND m.group_id = ? AND m.excluded = 0
		WHERE v.state IN ('active', 'saved')
		  AND COALESCE(v.posted_at, v.downloaded_at) >= ?
	`)
	args = append(args, g.ID, since)

	includeAll := dedupeLower(append(append([]string{}, g.IncludeTags...), q.TagsAny...))
	if len(includeAll) > 0 {
		sb.WriteString(` AND EXISTS (SELECT 1 FROM video_tags vt JOIN tags t ON t.id = vt.tag_id WHERE vt.video_id = v.id AND t.name IN (`)
		sb.WriteString(placeholders(len(includeAll)))
		sb.WriteString(`))`)
		for _, t := range includeAll {
			args = append(args, t)
		}
	}
	if len(g.ExcludeTags) > 0 {
		sb.WriteString(` AND NOT EXISTS (SELECT 1 FROM video_tags vt JOIN tags t ON t.id = vt.tag_id WHERE vt.video_id = v.id AND t.name IN (`)
		sb.WriteString(placeholders(len(g.ExcludeTags)))
		sb.WriteString(`))`)
		for _, t := range g.ExcludeTags {
			args = append(args, t)
		}
	}

	switch q.Watched {
	case WatchedOnly:
		sb.WriteString(` AND EXISTS (SELECT 1 FROM watch_state w WHERE w.video_id = v.id AND w.watched = 1)`)
	case WatchedUnwatched:
		sb.WriteString(` AND NOT EXISTS (SELECT 1 FROM watch_state w WHERE w.video_id = v.id AND w.watched = 1)`)
	}

	if q.MinDuration > 0 {
		sb.WriteString(` AND COALESCE(v.duration_seconds, 0) >= ?`)
		args = append(args, q.MinDuration)
	}
	if q.MaxDuration > 0 {
		sb.WriteString(` AND COALESCE(v.duration_seconds, 0) > 0 AND v.duration_seconds <= ?`)
		args = append(args, q.MaxDuration)
	}

	sb.WriteString(` ORDER BY COALESCE(v.posted_at, v.downloaded_at) DESC, v.id DESC`)
	if q.Limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT %d", q.Limit))
	}

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []video.Video
	for rows.Next() {
		var v video.Video
		var downloaded, stateChanged int64
		var state string
		if err := rows.Scan(&v.ID, &v.CreatorID, &v.ExternalID, &v.URL, &v.Title, &v.Description,
			&v.DurationSeconds, &v.PostedAt, &downloaded, &v.FilePath, &v.ThumbnailPath,
			&state, &stateChanged); err != nil {
			return nil, err
		}
		v.DownloadedAt = time.Unix(downloaded, 0)
		v.StateChangedAt = time.Unix(stateChanged, 0)
		v.State = video.State(state)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Errors / helpers ---------------------------------------------------

var (
	ErrNotFound   = errors.New("group not found")
	ErrSlugExists = errors.New("group slug already exists")
)

var slugRE = regexp.MustCompile(`[^a-z0-9-]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRE.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if s == "" {
		s = fmt.Sprintf("group-%d", time.Now().UnixNano())
	}
	return s
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinCSV(items []string) string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.ToLower(strings.TrimSpace(it))
		if it != "" {
			out = append(out, it)
		}
	}
	return strings.Join(out, ",")
}

func dedupeLower(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.ToLower(strings.TrimSpace(it))
		if it == "" {
			continue
		}
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}
