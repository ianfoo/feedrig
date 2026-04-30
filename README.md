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
| `-yt-dlp-bin` | `yt-dlp` | Override the `yt-dlp` binary — see "yt-dlp on macOS" if Homebrew's version crashes on Python 3.14 |
| `-discoverer` | `auto` | `auto` (chromedp → instago) / `chromedp` / `instago` / `none` |
| `-chrome` | (empty) | Path to Chromium/Chrome (default: search PATH) |
| `-summarizer` | `auto` | `auto` (probes ollama then falls back to stub) / `ollama` / `openrouter` / `stub` / `none` |
| `-ollama-url` | `http://localhost:11434` | Ollama base URL |
| `-ollama-model` | `llama3.2:3b` | Ollama model (must be pulled: `ollama pull <name>`) |
| `-openrouter-model` | `anthropic/claude-3.5-haiku` | OpenRouter model id (set `OPENROUTER_API_KEY`) |
| `-whisper-bin` | `whisper-cli` | whisper.cpp binary on `PATH` (or absolute path) — see "Enabling Whisper" |
| `-whisper-model` | (empty) | Path to a whisper.cpp `.bin` model — empty disables transcription |
| `-whisper-lang` | (empty) | Language hint for Whisper, e.g. `en`; empty = auto-detect |

## yt-dlp on macOS (Python 3.14 workaround)

Homebrew's `yt-dlp` formula installs against whatever Python it
considers current — at the moment that's Python 3.14, which has a
known incompatibility producing errors like:

```
python: posix_spawn: ...Python.app/Contents/MacOS/Python: Undefined error: 0
```

The fix is to use the **standalone yt-dlp binary** that ships with
Python embedded:

```sh
mkdir -p ~/bin
curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos \
  -o ~/bin/yt-dlp
chmod +x ~/bin/yt-dlp
xattr -d com.apple.quarantine ~/bin/yt-dlp 2>/dev/null || true
~/bin/yt-dlp --version    # confirm it runs
```

Then point feedrig at it:

```sh
./feedrig -yt-dlp-bin ~/bin/yt-dlp ...
```

Or add `~/bin` to your `PATH` ahead of `/opt/homebrew/bin` and the
default `-yt-dlp-bin yt-dlp` will pick it up.

## Enabling Whisper transcription

Transcription is **off by default** (no `-whisper-model` set → the
`Noop` transcriber returns "unavailable" and the worker skips the step).
Summaries still run; they just have title + description + (no
transcript) to work with.

Turn it on by installing [whisper.cpp](https://github.com/ggerganov/whisper.cpp)
and pointing feedrig at the binary + a model file:

```sh
# 1) Build whisper.cpp (one-time, ~30s on a modern CPU)
git clone https://github.com/ggerganov/whisper.cpp ~/whisper.cpp
cd ~/whisper.cpp
make            # produces ./build/bin/whisper-cli (or ./main on older builds)

# 2) Download a model. base.en is the sweet spot for short reels:
#    ~150 MB, fast on CPU, English-only. Other options: tiny.en (75 MB),
#    small.en (500 MB), medium (1.5 GB).
./models/download-ggml-model.sh base.en

# 3) Make the binary discoverable, then run feedrig pointing at the model.
sudo cp ./build/bin/whisper-cli /usr/local/bin/
./feedrig \
  -whisper-model ~/whisper.cpp/models/ggml-base.en.bin \
  -whisper-lang en
```

If you skipped the `cp` step, pass an absolute path instead:
`-whisper-bin ~/whisper.cpp/build/bin/whisper-cli`.

**Re-enrich existing videos** (their transcripts populate one at a time
as the worker tick processes them):

```sh
./feedrig enrich -whisper-model ~/whisper.cpp/models/ggml-base.en.bin <video-id>
# or trigger the rescan path: nothing — every 30s the worker scans for
# pending/failed enrichment_state rows and processes them.
```

**Notes:**
- The OpenAI Python `whisper` CLI is *not* compatible (different argument
  format). If you have it installed and want to use it, that's a small
  follow-up — file an issue.
- We extract a 16 kHz mono WAV via ffmpeg before invoking whisper.cpp,
  matching the format whisper.cpp expects.
- For non-English content, drop `-whisper-lang en` (or set it to the
  right ISO code) and use `ggml-base.bin` (multilingual) instead of
  `ggml-base.en.bin` (English-only).
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
