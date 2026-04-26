package db

import (
	"database/sql"
	"fmt"
)

// Migrations are SQL statements applied once each, in order. The applied
// version is tracked via SQLite's PRAGMA user_version, which starts at 0.
// Append new migrations; never edit or reorder existing ones.
var migrations = []string{
	// 1: add enrichment_state + enrichment_error columns to videos. The base
	// schema applies before migrations, so this is safe on first run too —
	// IF NOT EXISTS isn't supported by ADD COLUMN, hence the user_version gate.
	`ALTER TABLE videos ADD COLUMN enrichment_state TEXT NOT NULL DEFAULT 'pending';
	 ALTER TABLE videos ADD COLUMN enrichment_error TEXT;`,
	// 2: per-creator poll interval override (NULL = use global default).
	`ALTER TABLE creators ADD COLUMN poll_interval_seconds INTEGER;`,
	// 3: smart-playlist groups + creator memberships. Tag filters are stored
	// as comma-separated strings on the group row — they're authoritative
	// (the canonical tag table is just for normalization on the video side).
	// recency_days default keeps the SQL self-contained; the Go-side default
	// (groups.DefaultRecencyDays) is what's used when the column is null on
	// insert via the store.
	`CREATE TABLE groups (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		name            TEXT    NOT NULL,
		slug            TEXT    NOT NULL UNIQUE,
		recency_days    INTEGER NOT NULL DEFAULT 7,
		include_tags    TEXT    NOT NULL DEFAULT '',
		exclude_tags    TEXT    NOT NULL DEFAULT '',
		position        INTEGER NOT NULL DEFAULT 0,
		last_visited_at INTEGER,
		created_at      INTEGER NOT NULL
	);
	CREATE TABLE group_creator_memberships (
		group_id   INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
		creator_id INTEGER NOT NULL REFERENCES creators(id) ON DELETE CASCADE,
		excluded   INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(group_id, creator_id)
	);
	CREATE INDEX idx_group_creator_memberships_creator ON group_creator_memberships(creator_id);`,
}

func applyMigrations(conn *sql.DB) error {
	var current int
	if err := conn.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for i, sqlText := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		if _, err := conn.Exec(sqlText); err != nil {
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := conn.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
			return fmt.Errorf("set user_version %d: %w", v, err)
		}
	}
	return nil
}
