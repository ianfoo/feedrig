# Architecture

## Goals

- **Curated, not algorithmic:** the user explicitly chooses creators; the system never recommends.
- **Low-friction review:** a player tuned for triage — scrubber, fast playback-speed switching, fluid keyboard/scroll nav between videos, save / delete on the playback screen, last-watched marker per creator.
- **Self-curating library:** unsaved videos auto-expire (TTL → pending-deletion → removal) so the disk doesn't grow without bound.
- **Mobile-first UI** with a path to native packaging (Capacitor / React Native).
- **Local-first deployment** with a clean S3 / hosted upgrade path.

## Process model

A single Go binary that:

1. Serves the web UI and JSON API on `:7777`.
2. Runs an in-process scheduler that polls creators on a cadence and runs the TTL sweeper.
3. Shells out to `yt-dlp` and (later) `whisper` / Chromium for ingestion and transcription.
4. Persists everything to SQLite (`data/feedrig.db`) and the media filesystem (`media/<handle>/<shortcode>.*`).

Future: split scheduler into a separate `feedrig worker` subcommand if hosted deployment makes sharing inconvenient.

## Layered design

```
                     ┌──────────────────────────┐
   browser /         │     web/  (HTTP layer)   │
   future native ───►│  HTML templates + /api/* │
                     └────────────┬─────────────┘
                                  │
            ┌─────────────────────┼─────────────────────┐
            │                     │                     │
   ┌────────▼─────────┐  ┌────────▼─────────┐  ┌────────▼─────────┐
   │  creator/        │  │  video/          │  │  ingest/         │
   │  CRUD on         │  │  CRUD + state    │  │  Discoverer +    │
   │  monitored       │  │  + watch state   │  │  Downloader      │
   │  accounts        │  │                  │  │  interfaces      │
   └────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘
            │                     │                     │
            └─────────────────────┼─────────────────────┘
                                  │
                         ┌────────▼─────────┐
                         │  db/  SQLite     │
                         └──────────────────┘
```

Future packages: `summarize/`, `transcribe/`, `schedule/`, `ttl/`, `digest/`, `storage/` (local + S3), `groups/` (smart playlists).

## Data model

See [`internal/db/schema.sql`](../internal/db/schema.sql) for the canonical schema. Key tables:

- `creators` — `(handle UNIQUE, profile_url, added_at, last_fetched_at)`. Adding a creator is a one-time decision; `last_fetched_at` is bumped after every poll.
- `videos` — `(creator_id, external_id UNIQUE per creator, file_path, state, posted_at, …)`. `state ∈ {active, saved, pending_deletion}`.
- `watch_state` — `(video_id PK, last_position_seconds, watched, watched_at)`. The "where did I leave off" data; also drives the per-creator last-watched marker.

Planned tables (v0.2+):

- `transcripts` — `(video_id PK, text, model, lang, generated_at)`
- `summaries` — `(video_id PK, summary, model, generated_at)`
- `tags` / `video_tags` — categorization (`pizza`, `bass`, `political-commentary`, …)
- `groups` / `creator_group_memberships` — for smart playlists
- `group_check_state` — per-group "what was new since last check?"

## Ingestion

There is no public Instagram API for "monitor creators I don't own." The system supports multiple **discovery** strategies behind a single interface (`ingest.Discoverer`), with **download** as a separate concern (`ingest.Downloader`). See [`docs/DECISIONS.md`](DECISIONS.md) for the rationale.

Current implementations:

- **Discovery** — `instago` (siongui/instago, no-login profile scrape, stale upstream, expected to fail).
- **Discovery (planned)** — `chromedp` (drives a headless Chromium against the profile page).
- **Discovery (planned)** — `cookiedlp` (`yt-dlp` profile listing with a burner cookie file).
- **Download** — `ytdlp` (shells out to `yt-dlp` per post URL; supports `--cookies`).

The ingestion service iterates discovered shortcodes, skips any already in the DB (set difference, not high-water-mark — robust to deletions and backfills), and persists what's new.

## Web UI

- v0.1: server-rendered Go templates + minimal vanilla JS for the player. Mobile-responsive via CSS grid + viewport meta.
- v0.5: JSON API surface introduced at `/api/v1/*` alongside the HTML routes. Same service-layer calls, no logic duplication.
- v0.6 (planned): React + Vite + TypeScript SPA as the primary UI, Capacitor wrap for native iOS/Android. Templates retire to a debug surface or are removed.

## Storage

- **Metadata:** SQLite. WAL mode, foreign keys ON, 5s busy timeout.
- **Media:** local disk under `media/<handle>/<shortcode>.<ext>`. Thumbnails: `<shortcode>.jpg`. yt-dlp metadata: `<shortcode>.info.json`.
- **Hosted future:** introduce a `storage.Blob` interface; current local-disk impl moves under it; add an S3 impl. Database stays as SQLite single-tenant; if multi-user emerges, move to Postgres.

## Security posture (single-user local-first)

- Binds `127.0.0.1` by default.
- Static media served from a path-confined `safeFileServer` to prevent traversal.
- No auth in v0.1 (single-user assumption). When hosted, add session auth before exposing publicly.
