// Package enrich persists transcripts, summaries, and tags for videos, and
// orchestrates the transcribe → summarize → tag pipeline.
package enrich

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Transcript struct {
	VideoID     int64
	Text        string
	Language    string
	Model       string
	GeneratedAt time.Time
}

type Summary struct {
	VideoID     int64
	Summary     string
	Notes       string
	Model       string
	GeneratedAt time.Time
}

type Tag struct {
	ID   int64
	Name string
}

type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
	StateSkipped State = "skipped"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) UpsertTranscript(ctx context.Context, t Transcript) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO transcripts(video_id, text, language, model, generated_at)
			VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(video_id) DO UPDATE SET
			text = excluded.text,
			language = excluded.language,
			model = excluded.model,
			generated_at = excluded.generated_at
	`, t.VideoID, t.Text, nullableString(t.Language), t.Model, time.Now().Unix())
	return err
}

func (s *Store) GetTranscript(ctx context.Context, videoID int64) (*Transcript, error) {
	var t Transcript
	var lang sql.NullString
	var gen int64
	err := s.db.QueryRowContext(ctx,
		`SELECT video_id, text, language, model, generated_at FROM transcripts WHERE video_id = ?`,
		videoID,
	).Scan(&t.VideoID, &t.Text, &lang, &t.Model, &gen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Language = lang.String
	t.GeneratedAt = time.Unix(gen, 0)
	return &t, nil
}

func (s *Store) UpsertSummary(ctx context.Context, sm Summary) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO summaries(video_id, summary, notes, model, generated_at)
			VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(video_id) DO UPDATE SET
			summary = excluded.summary,
			notes = excluded.notes,
			model = excluded.model,
			generated_at = excluded.generated_at
	`, sm.VideoID, sm.Summary, nullableString(sm.Notes), sm.Model, time.Now().Unix())
	return err
}

func (s *Store) GetSummary(ctx context.Context, videoID int64) (*Summary, error) {
	var sm Summary
	var notes sql.NullString
	var gen int64
	err := s.db.QueryRowContext(ctx,
		`SELECT video_id, summary, notes, model, generated_at FROM summaries WHERE video_id = ?`,
		videoID,
	).Scan(&sm.VideoID, &sm.Summary, &notes, &sm.Model, &gen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sm.Notes = notes.String
	sm.GeneratedAt = time.Unix(gen, 0)
	return &sm, nil
}

// SetTags replaces the tag set for a video. Source = 'auto' or 'manual'.
func (s *Store) SetTags(ctx context.Context, videoID int64, tags []string, source string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM video_tags WHERE video_id = ? AND source = ?`, videoID, source); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, raw := range tags {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tags(name, created_at) VALUES(?, ?) ON CONFLICT(name) DO NOTHING`,
			name, now,
		); err != nil {
			return err
		}
		var tagID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = ?`, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO video_tags(video_id, tag_id, source) VALUES(?, ?, ?) ON CONFLICT DO NOTHING`,
			videoID, tagID, source,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) TagsForVideo(ctx context.Context, videoID int64) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name
		FROM tags t JOIN video_tags vt ON vt.tag_id = t.id
		WHERE vt.video_id = ?
		ORDER BY t.name
	`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetEnrichmentState persists the videos.enrichment_state column.
func (s *Store) SetEnrichmentState(ctx context.Context, videoID int64, state State, errMsg string) error {
	var nm sql.NullString
	if errMsg != "" {
		nm = sql.NullString{String: errMsg, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE videos SET enrichment_state = ?, enrichment_error = ? WHERE id = ?`,
		string(state), nm, videoID,
	)
	return err
}

// PendingVideoIDs returns video IDs whose enrichment is pending or failed
// (we re-run failed jobs on next worker startup; if they fail again the
// failure persists so we don't loop forever).
func (s *Store) PendingVideoIDs(ctx context.Context, includeFailed bool) ([]int64, error) {
	q := `SELECT id FROM videos WHERE enrichment_state = 'pending'`
	if includeFailed {
		q = `SELECT id FROM videos WHERE enrichment_state IN ('pending', 'failed')`
	}
	q += ` ORDER BY downloaded_at DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
