# feedrig

Curate your scrolling.

A self-hosted curator for video content from a list of Instagram creators.
The point of the tool is to **break the algorithmic-feed habit** while still
keeping up with creators whose content you actually value. Instead of opening
Instagram and getting served whatever the algorithm prefers, you maintain a
small, deliberate list of creators; feedrig polls them, downloads new videos,
summarizes them, tags them by topic, and presents them in a player optimized
for review (scrubber, fast playback-speed switching, keyboard nav between
videos in a creator's list).

Videos auto-expire after a configurable TTL unless you explicitly save them,
so the local library doesn't sprawl.

## Status

Early development. See [`docs/MILESTONES.md`](docs/MILESTONES.md) for what's
shipped vs. planned.

## Quick start

```sh
make           # builds the SPA and the Go binary
./feedrig      # starts the server on http://127.0.0.1:7777
# Server-rendered UI: http://127.0.0.1:7777/
# React SPA:          http://127.0.0.1:7777/app/
```

Or, if you don't want the SPA built into the binary:

```sh
make go        # Go-only build; SPA shell is empty
./feedrig
```

Or with Docker:

```sh
make docker
docker run -p 7777:7777 -v feedrig-data:/data -v feedrig-media:/media feedrig
```

Flags:

| Flag | Default | Purpose |
| --- | --- | --- |
| `-addr` | `127.0.0.1:7777` | HTTP listen address |
| `-data` | `data` | SQLite DB directory |
| `-media` | `media` | Downloaded video directory |
| `-cookies` | (empty) | Optional `yt-dlp`-format cookie file for IG auth |
| `-discoverer` | `auto` | `auto` (chromedp → instago) / `chromedp` / `instago` / `none` |
| `-chrome` | (empty) | Path to Chromium/Chrome (default: search PATH) |
| `-summarizer` | `auto` | `auto` (probes ollama then falls back to stub) / `ollama` / `openrouter` / `stub` / `none` |
| `-ollama-url` | `http://localhost:11434` | Ollama base URL |
| `-ollama-model` | `llama3.2:3b` | Ollama model (must be pulled: `ollama pull <name>`) |
| `-openrouter-model` | `anthropic/claude-3.5-haiku` | OpenRouter model id (set `OPENROUTER_API_KEY`) |
| `-whisper-model` | (empty) | Path to a `whisper.cpp` `.bin` model; empty disables transcription |
| `-categories` | (preset list) | Comma-separated category menu shown to the summarizer |

Requires `yt-dlp`, `ffmpeg`, and (for `chromedp` discovery) Chromium / Chrome on `PATH`.

## Major routes

- `/creators` — manage your curated list (sort, filter, bulk add via paste or `.txt`/`.csv`)
- `/creators/{id}` — per-creator video grid with last-watched marker
- `/videos/{id}` — player (scrubber, speed buttons, keyboard nav, save/delete)
- `/groups` and `/groups/{slug}` — smart playlists with tag filters and "unseen since last visit"
- `/groups/{slug}/digest` — print-friendly per-group digest
- `/pending` — videos about to be auto-removed (TTL grace window)
- `/settings` — poll cadence, TTL, grace
- `/api/v1/*` — JSON API mirroring the HTML routes (consumed by future SPA / native app)

## Documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — system overview
- [`docs/DECISIONS.md`](docs/DECISIONS.md) — architectural decision log
- [`docs/MILESTONES.md`](docs/MILESTONES.md) — milestone-by-milestone progress
