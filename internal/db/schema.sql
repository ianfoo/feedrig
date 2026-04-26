CREATE TABLE IF NOT EXISTS creators (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    handle          TEXT    NOT NULL UNIQUE,
    display_name    TEXT,
    profile_url     TEXT    NOT NULL,
    added_at        INTEGER NOT NULL,
    last_fetched_at INTEGER
);

CREATE TABLE IF NOT EXISTS videos (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    creator_id       INTEGER NOT NULL REFERENCES creators(id) ON DELETE CASCADE,
    external_id      TEXT    NOT NULL,
    url              TEXT    NOT NULL,
    title            TEXT,
    description      TEXT,
    duration_seconds INTEGER,
    posted_at        INTEGER,
    downloaded_at    INTEGER NOT NULL,
    file_path        TEXT    NOT NULL,
    thumbnail_path   TEXT,
    state            TEXT    NOT NULL DEFAULT 'active',
    state_changed_at INTEGER NOT NULL,
    UNIQUE(creator_id, external_id)
);

CREATE INDEX IF NOT EXISTS idx_videos_creator_posted
    ON videos(creator_id, posted_at DESC);

CREATE INDEX IF NOT EXISTS idx_videos_state ON videos(state);

CREATE TABLE IF NOT EXISTS watch_state (
    video_id              INTEGER PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    last_position_seconds REAL    NOT NULL DEFAULT 0,
    watched               INTEGER NOT NULL DEFAULT 0,
    watched_at            INTEGER,
    updated_at            INTEGER NOT NULL
);
