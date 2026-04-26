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
go build -o feedrig .
./feedrig -addr 127.0.0.1:7777
# open http://127.0.0.1:7777
```

Flags:

| Flag | Default | Purpose |
| --- | --- | --- |
| `-addr` | `127.0.0.1:7777` | HTTP listen address |
| `-data` | `data` | SQLite DB directory |
| `-media` | `media` | Downloaded video directory |
| `-cookies` | (empty) | Optional `yt-dlp`-format cookie file for IG auth |

Requires `yt-dlp` and `ffmpeg` on `PATH` for ingestion.

## Documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — system overview
- [`docs/DECISIONS.md`](docs/DECISIONS.md) — architectural decision log
- [`docs/MILESTONES.md`](docs/MILESTONES.md) — milestone-by-milestone progress
