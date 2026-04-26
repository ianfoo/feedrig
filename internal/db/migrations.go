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
