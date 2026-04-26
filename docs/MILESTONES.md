# Milestones

Tracks what's shipped, what's in progress, and what's planned. Each milestone lists features, current status, and any deviations from the original spec.

Spec source: the user's initial requirements, distilled in [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) goals.

---

## v0.1 — Skeleton: add creator, fetch, watch, save/delete

**Status:** ✅ shipped (with one caveat — see "deviations").

**Features:**

- [x] Go module scaffold, single-binary build.
- [x] SQLite schema: `creators`, `videos`, `watch_state`.
- [x] Creator CRUD with handle/URL parsing (`internal/creator`).
- [x] Video CRUD with `state ∈ {active, saved, pending_deletion}` and watch-state tracking (`internal/video`).
- [x] Ingestion interface + `yt-dlp` downloader (`internal/ingest`).
- [x] Web UI: creators list, add creator, creator detail (videos sorted by date, last-watched marker), player page.
- [x] Player: HTML5 `<video>` with native scrubber, speed buttons (0.75× → 2.5×), keyboard nav (Space/J/K/L, ←/→ within-video and Shift+←/→ between-videos, 1–7 for speed presets), wheel-scroll between videos with cooldown.
- [x] Save / delete from playback screen (delete = mark `pending_deletion` + remove media file).
- [x] Position persistence (throttled `sendBeacon`) so resume-from-last-position works.
- [x] Per-creator last-watched marker on the creator page.
- [x] Mobile-responsive CSS (viewport meta + grid).

**Deviations from spec:**

- **Discovery is non-functional in v0.1.** `siongui/instago` (the only Go-native no-login profile lister we found) relies on Instagram's `_sharedData` JSON blob, which Meta retired years ago. The "Fetch new" button shows a helpful error directing the user to the manual "Add by URL" form. Real discovery comes in v0.1.1.
- Manual URL paste was added as a fallback. The user has indicated they don't expect to use it; it stays as an escape hatch but isn't a feature investment.

**Smoke test (synthetic video, since IG access fails in the dev sandbox):**
- Add creator: ✅ 302 → `/creators/1`
- Fetch new: ✅ returns the expected discovery-failure error.
- Insert synthetic video + load player: ✅ player renders, media serves, scrubber/speed/keyboard work, position-save returns 204 and persists, save action transitions row to `state='saved'`.

**Files:** `main.go`, `internal/{creator,video,ingest,db,web}/*`.

---

## v0.1.1 — Real discovery

**Status:** ✅ shipped.

**Features:**

- [x] `chromedp` discoverer (`internal/ingest/chromedp.go`) drives headless Chromium against the profile page, scrolls (configurable) to load more posts, harvests `/p/`, `/reel/`, and `/tv/` anchor hrefs from the rendered DOM. Robust to small HTML changes because it only inspects link targets.
- [x] `--discoverer` flag (`auto|chromedp|instago|none`); `auto` chains chromedp → instago and logs which path succeeded.
- [x] `ChainDiscoverer` (`internal/ingest/chain.go`): tries strategies in order, returns the first that doesn't error.
- [x] `NoopDiscoverer`: explicit "disable discovery" mode for users who only want manual paste.

**Deviations from original v0.1.1 plan:**

- **`cookiedlp` (yt-dlp profile listing) dropped.** Upstream `InstagramUserIE` is marked `_WORKING = False`; even with cookies it fails. If we want a non-Chromium auth-required path later, we'll write our own GraphQL client (the InstaFix technique).
- **No login UI for IG cookies in v0.1.1.** When the user is ready for a burner account, they'll export cookies via a browser extension (e.g., "Get cookies.txt") and pass `--cookies path.txt`. v0.2+ may add a UI flow.

**Open issues to monitor:**

- IG often rate-limits unauthenticated requests from datacenter IPs. From a residential IP the no-login chromedp approach is more reliable. We may need to add cookie support to chromedp itself (not just yt-dlp) once we hit the wall.

---

## v0.2 — Transcription, summary, auto-tagging, creator management

**Status:** ✅ shipped (with deviations — see below).

**Features:**

- [x] Schema migrations system (`internal/db/migrations.go`) using `PRAGMA user_version`.
- [x] Schema additions: `transcripts`, `summaries`, `tags`, `video_tags`, `settings`, `videos.enrichment_state`, `videos.enrichment_error`.
- [x] `internal/transcribe`: `Transcriber` interface; `WhisperCpp` impl (subprocess: ffmpeg → 16kHz mono wav → whisper.cpp `-otxt`); `Noop` for "no transcription configured".
- [x] `internal/summarize`: `Summarizer` interface; `OpenRouter` impl (single HTTP client → many providers via `--openrouter-model` flag, e.g. `anthropic/claude-3.5-haiku`, `moonshotai/kimi-k2`); `Stub` deterministic offline summarizer for testing; `Noop`.
- [x] `internal/enrich`: store + worker. Worker runs the transcribe → summarize → tag pipeline serially off a buffered queue, with a 30s periodic rescan that picks up `pending` videos missed by the in-memory queue (e.g., after restart).
- [x] Ingest service auto-enqueues newly-downloaded videos via a small `Enricher` interface (avoids import cycle).
- [x] Player page renders summary block + tag chips; original caption hidden in a `<details>` since the summary is now the primary brief.
- [x] **Creator management UI** (per user feedback added mid-milestone): sort by name / added / last-fetched (server-side); client-side substring filter; bulk add via textarea or `.txt`/`.csv` file upload (`POST /creators/bulk`, supports `handle, Display Name` per line and `#` comments); per-row inline edit display name + delete actions (`POST /creators/{id}/update`, `POST /creators/{id}/delete`).

**Deviations from original v0.2 plan:**

- **JSON API surface deferred to v0.5.** Building it twice (HTML + JSON) before there's an SPA consumer would be churn. The handlers cleanly delegate to the service layer, so adding `/api/v1/*` later is mechanical.
- **Local LLM via ollama deferred.** `OpenRouter` covers most of the user's "switch to a different model" requirement (Claude, GPT, Kimi via one client). `ollama` integration is mostly the same shape (HTTP client to `:11434`); will add when we hit a use case.
- **OpenRouter Whisper transcription deferred.** Local `whisper.cpp` is wired and tested-by-shape; remote whisper would be a parallel impl.
- **Creator management was added** (not in original plan) per explicit user feedback during the milestone. Bulk add, sort, filter, and inline edit/delete are all live.

**End-to-end smoke test (deterministic, no network):**

```sh
./feedrig -summarizer stub -discoverer none -categories "test,nature,wildlife"
# → bulk-add → manual SQL insert of a video file → 30s rescan tick
# → enrichment_state=done, summary populated, tags auto-assigned
# → player page renders summary block + tag chips
```

**Open design questions raised mid-milestone (for v0.4):**

- The user asked what other smart-playlist settings might be worth exposing. Capturing the candidate list here so it's not lost:
    - **Recency window** (last N days)
    - **Category include / exclude filters**
    - **Min / max video duration** (filter shorts vs long-form)
    - **Watched / unwatched only**
    - **Sort:** newest / random / by creator / longest unwatched
    - **Item cap** (top N)
    - **Per-playlist default playback speed**
    - **Auto-play next within playlist** (binge mode)
    - **Mark-as-seen behavior:** on scroll-past vs on watched
    - **Optional notification** (push / email) when N+ items match
    - **Excluded creators** (subtractive set, useful when a creator joins a topic group but you want to skip them in the news view)
    - **Visual customization** (color, icon, position in sidebar)
    - **Export as RSS / OPML** for piping into a reader
- I'll evaluate which to ship at v0.4 and which to defer.

---

## v0.3 — Scheduler + TTL

**Status:** ✅ shipped.

**Features:**

- [x] `internal/schedule/Scheduler`: launches one goroutine per creator on startup, jittered first-run (0–60% of interval, capped at 5 min) to avoid thundering herd, ±10% jitter on subsequent cycles. Calls `ingest.Service.FetchNewForCreator` per tick.
- [x] Schema migration 2: `creators.poll_interval_seconds` (per-creator override; `NULL` = use default).
- [x] `internal/ttl/Sweeper`: runs every `ttl_sweep_interval_seconds` (default 1h). Pass 1 ages `active` videos older than `ttl_days` (default 30) → `pending_deletion`. Pass 2 finds `pending_deletion` rows whose `state_changed_at` is older than `grace_days` (default 7), removes media + thumbnail files from disk, and hard-deletes the row.
- [x] `internal/settings`: typed wrapper over the `settings` KV table; keys for poll interval, TTL days, grace days, sweep interval; sensible fallback defaults.
- [x] `/pending` page: lists videos in pending-deletion state with `Keep` (→ saved) / `Restore` (→ active) actions.
- [x] `/settings` page: edit poll cadence, TTL days, grace days.
- [x] Topbar nav entries: `Pending`, `Settings`.

**Deviations from original v0.3 plan:**

- **Hard-delete on grace expiry (chose this) vs. soft `state='deleted'`.** Picked hard-delete because retaining metadata after the file is gone clutters lists and the user has already had the grace window. If we ever need an audit trail, that's a separate decision.
- **Scheduler reload on creator add / delete is NOT live.** A server restart picks up newly-added creators. Hot-reload (subscribing to creator add/delete events) is queued for v0.3.x. In practice this is fine for a single-user tool.
- **Per-creator UI to override poll cadence** is not built. The DB column is in place; the form is queued for v0.3.x.

**Smoke test (with ttl_days=1, grace_days=1):**

- Active video downloaded 5 days ago → sweeper aged it: `aged=1` (state → pending_deletion). ✅
- Pending-deletion row whose state changed 3 days ago → sweeper removed media + hard-deleted row: `removed=1, pruned=1`. ✅
- `/pending` renders the aged row with Keep/Restore actions. ✅
- `/settings` renders configured values and accepts updates. ✅
- Schema migration applied cleanly: `PRAGMA user_version = 2`. ✅

---

## v0.4 — Smart playlists / groups + filtering

**Status:** ✅ shipped.

**Features:**

- [x] Schema migration 3: `groups` table (name, slug, recency_days, include_tags, exclude_tags, position, last_visited_at) + `group_creator_memberships` (group_id, creator_id, excluded). Tag filters stored as comma-separated strings on the group row; the canonical `tags` table normalizes the per-video side.
- [x] `internal/groups`: CRUD + a `Feed(group, FeedQuery)` query that joins videos to memberships, applies recency window, include-tags, exclude-tags, and an optional ad-hoc `TagsAny` narrowing chip. Excluded creators are subtracted from the membership join (`excluded = 0` filter). Results never materialized.
- [x] `last_visited_at` updated after every group-feed render so the "Unseen since last visit" toggle has a stable boundary.
- [x] `/groups` — list + create form.
- [x] `/groups/{slug}` — feed with All / Unseen filter row + clickable tag chips that re-render with `?tag=`.
- [x] `/groups/{slug}/edit` — name + window + include/exclude tag inputs; per-creator radio (none/include/exclude) with a small JS shim that translates the radios into `include[]` and `exclude[]` arrays at submit; danger-zone delete.
- [x] Topbar nav adds Groups link.

**Deviations from the original v0.4 plan:**

- **Group polling cadence dropped.** Per-creator cadence (v0.3) is sufficient — discovery is per-creator, and groups are read-only views over the resulting videos table. Adding a per-group cadence would have meant a second scheduler and confusing "what triggers a fetch" semantics. If we ever want a per-group "force a discovery sweep now" button, it's an HTTP endpoint away.
- **Drag-to-reorder deferred.** Position column is in the schema; UI is queued for v0.4.x.

**Smoke test** (4 videos across 3 creators, tags assigned manually):

- Group "News" with members `newsfeed`, `cryptobro`; `include_tags=news`; `exclude_tags=crypto`.
- Result: NEWS1 (news tag, no crypto) shown ✅; NEWS2 (news + crypto) hidden ✅; CRYPTO1 (crypto only) hidden ✅; MUSIC1 (creator not in group) hidden ✅.
- `?tag=news` narrowing returns only NEWS1 ✅.
- `?unseen=1` toggle works (verified by `last_visited_at` updates).

**Smart-playlist settings evaluated** (per the user's open question):

- **Shipped in v0.4:** name, recency window, include-tag filters, exclude-tag filters, creator membership with per-creator exclude, tag-chip narrowing via `?tag=`, "unseen since last visit" toggle.
- **Deferred to v0.4.x or v0.5:** position / drag-reorder, color, item cap, min/max duration, watched-only / unwatched-only toggle, sort order overrides, per-playlist default playback speed, auto-play next within playlist, notifications, RSS export.
- **Probably won't build:** mark-as-seen-on-scroll-past (the `last_visited_at` model is simpler and matches the user's mental model), excluded-by-handle UI (already covered by per-creator exclude in membership).

---

## v0.5 — Digest, JSON API, storage interface, pacing

**Status:** ✅ shipped (with React SPA + email delivery deferred to v0.6 — see below).

**Features:**

- [x] **Digest renderer**: `/groups/{slug}/digest` produces a print-friendly per-group digest with thumbnails, tags, and summaries; "Print" button calls `window.print()`.
- [x] **JSON API surface** (`/api/v1/*`): list creators, add/delete creator, get video, save / mark-pending video, position update, list groups, group feed (with `?unseen=1` and `?tag=`). Versioned for breaking-change isolation. Same service-layer calls as the HTML routes — no logic duplication.
- [x] **`internal/storage`**: `Blob` interface with `LocalFS` impl that mirrors today's on-disk layout, plus an `S3` stub that returns `ErrNotConfigured`. Wiring the system to use the interface is queued (current code paths still touch the filesystem directly via `os` calls); the interface alone unblocks future hosted deployment without further design churn.
- [x] **Pacing in the chromedp discoverer**: jittered sleeps (default 1.5s–4s) between scrolls, configurable via `PacingMin` / `PacingMax`, so the headless browser doesn't hammer profile pages back-to-back. Politeness, not detection-evasion.

**Deviations from the original v0.5 plan:**

- **React + Vite + Tailwind SPA deferred to v0.6.** The JSON API surface needed for it is in place. Building the SPA itself is a multi-day task and not the highest-value next step; the templated UI is fully functional and mobile-responsive.
- **Email digest delivery deferred to v0.6.** The digest is rendered as an HTML page; piping it through an SMTP client + cron-like trigger is a separate, isolated piece of work.
- **Webhook delivery (Slack / Discord) deferred** as a v0.6+ candidate.
- **Existing code paths still touch the filesystem directly.** Routing them through `storage.Blob` is mechanical refactoring queued for v0.6.

**Smoke test:**

- `GET /api/v1/creators` → `[]` (empty); `POST {"handle":"natgeo",...}` → 201 + full DTO; `GET` again → populated. ✅
- `GET /groups/news/digest` renders an `<article class="digest-item">` per video. ✅
- `GET /api/v1/groups/news/feed` returns `{group, videos[]}` with full video DTOs. ✅

---

## v0.5.1 — Polish: history retention, ollama, logo, magic-number sweep

**Status:** ✅ shipped.

**Features:**

- [x] **History retention.** TTL sweeper now archives (`state='archived'`) instead of hard-deleting: media file removed, metadata + summary + transcript + thumbnail kept indefinitely. New `creator/{id}/history` page lists all videos including archived; `POST /videos/{id}/redownload` rehydrates an archived video by re-running the downloader on its original URL. Player page shows a "Re-download" prompt instead of the player when state is `archived`. Supersedes ADR-009 with ADR-013.
- [x] **Ollama summarizer.** `summarize.Ollama` HTTP client (`/api/generate` with JSON output mode). New `--summarizer auto` (default) probes `/api/tags` at startup; if Ollama is reachable it's selected, otherwise falls back to the offline `Stub` with a log line explaining how to enable it. New flags: `--ollama-url`, `--ollama-model`.
- [x] **Logo + favicon.** Crane-rig SVG (mast, jib, hoist cable, hanging "v" load). Linked into the topbar and as `link[rel=icon]`. State badges added for `saved` / `archived` / `pending_deletion` so the history view is scannable at a glance.
- [x] **Magic-number cleanup** (per user feedback): `settings.Day`, `settings.DurationFromDays`, `settings.DaysFromDuration` consolidate the day-vs-hour-vs-second arithmetic. `groups.DefaultRecencyDays` replaces the `recency = 7` literals. Web layer's `int(x.Hours()/24)` / `pollHours*3600` rewritten to use the helpers.
- [x] **Doc drift fixed:** `docs/ARCHITECTURE.md` no longer claims the SPA shipped at v0.5; correctly states "v0.6 (planned)".
- [x] **ADR-012 captured** documenting the domain-types-vs-SQL-types refactor plan (deferred to v0.6 per the user's "not going to sweat it too hard").

**Smoke test:**

- Pending-deletion row with state_changed_at 9 days ago, grace_days=1 → sweep transitions to `archived` (`archived=1`), file removed, summary preserved in DB. ✅
- Player page for the archived row renders the archived-shell with Re-download. ✅
- `/creators/{id}/history` lists the archived video with state badge + summary. ✅
- `--summarizer auto` with Ollama not running logs the helpful message and uses Stub. ✅

---

## v0.6 — In progress (rolling)

**Status:** 🚧 in progress. Items below tick off as they ship; remainder lives in the unprioritized backlog at the bottom.

### Shipped this session

- [x] **Schema migration 4**: `creators.followed_at` (when each creator was followed on Instagram, populated from data export).
- [x] **Instagram data-export import** (`POST /creators/import`): parses `following.json` (both object and array forms) and bulk-creates / updates creators with their follow timestamps. New "Sort: followed (IG)" option on the creators list. Exposed in the creators page under a "Import from Instagram data export" disclosure.
- [x] **Per-creator cadence override UI** on the creator detail page (`POST /creators/{id}/cadence`). Empty / 0 clears the override.
- [x] **Smart-playlist filter polish**: watched / unwatched / any chips on the group feed; advanced-filter form for min/max duration (seconds) and item cap. New `WatchedFilter`, `MinDuration`, `MaxDuration` fields on `groups.FeedQuery`.
- [x] **Corpus search** (`/search?q=`): SQL `LIKE` across video title, description, summary, and transcript with one-result-per-video deduplication and ~160-char excerpts. Shipped as the building block both for the in-app search UI and for a future MCP server (which is now just a thin protocol shell over `enrich.Store.Search`).
- [x] **Scheduler hot-reload**: `Scheduler.Reload()` cancels per-creator workers and respawns from a fresh DB read. Wired into the add / bulk-add / import / delete / cadence handlers via a small `SchedulerReloader` interface, so changes take effect without a server restart. Cadence-page hint updated.
- [x] **Creator-level top-tags aggregation**: `enrich.Store.TopTagsForCreator(id, limit)` computes the most common tags across a creator's videos. Surfaced as a "Topics:" line with chips on the creator detail page (each chip shows the tag name + a count badge). Cheap content pre-categorization without re-running the LLM.
- [x] **RSS per group**: `GET /groups/{slug}/rss` emits an RSS 2.0 feed (title = video title, description = summary if present else original caption, pubDate = posted_at) so any reader (NetNewsWire, Feedbin, etc.) can subscribe. RSS button added to the group feed header alongside Digest / Edit.
- [x] **Group reorder**: up/down arrows on the groups list (`POST /groups/{slug}/reorder` with `dir=up|down`), three-step swap of the `position` column inside a transaction so it's safe under any future UNIQUE constraint. Edge moves are silent no-ops.
- [x] **MCP server protocol shell** (`feedrig mcp`): JSON-RPC 2.0 over stdio with `initialize`, `tools/list`, `tools/call`. Tools: `search_videos`, `get_video`, `list_creators`, `list_groups`. All four delegate to existing stores; the protocol layer is small. Wire from any MCP client (Claude Desktop, Cline, etc.) — pointed at the same `data/` dir as the web app, so they share state.
- [x] **Metadata TTL on archived rows**: optional second-stage purge in the TTL sweeper. Default 365 days; 0 = unlimited. After the configured age, archived rows are hard-deleted (with their thumbnails on disk and cascade-deleted transcripts/summaries/tags/watch_state via FK). Settings page exposes the knob.
- [x] **ADR-014**: deployment-target decision — one binary, multiple targets (local / droplet / Fargate / Cloud Run / Container Apps). Not Lambda-decomposed. v0.7 adds the opt-in subcommand surface (`feedrig sweep`, `feedrig poll`, `feedrig enrich`) and a Dockerfile for hosted-container deployments.

### Smoke-tested end-to-end

- Following.json (object form) imported 3 creators with timestamps; sort=followed orders them recent-first ✅
- Per-creator cadence: 2h override stored as `7200` seconds; cleared back to NULL ✅
- Search "slap" matches title; "polling" matches transcript ✅
- Group feed: watched=yes returns only the watched video; watched=no returns only the unwatched; min=60 hides the 45s clip ✅

### From the user mid-v0.5 / v0.5.1

- **Pre-categorization of currently-followed creators.** When the user adds a creator, optionally fetch their bio + link-in-bio (Linktree, Beacons, etc.) and use the LLM to suggest categories the user can accept / reject. Dramatically cuts initial-setup friction.
- **"Who I follow" import path.** No public API; Instagram does provide a "Download your data" feature that exports a `following.html` / `following.json`. Add an import endpoint that consumes that file and bulk-creates creators.
- **Pre-categorization heuristic from existing posts.** First N videos through the enrichment pipeline already produce tags; aggregate those into a "primary topics" suggestion for the creator card.
- **MCP server over the transcript / summary corpus.** Expose a small MCP tool surface so an LLM can run topic searches across the user's library ("find videos that talk about chinese cooking technique"). Underlying query is SQL `LIKE` / FTS5 over `transcripts.text` and `summaries.summary`; MCP is just the protocol shell. Probably ships alongside the React SPA so the user has a UI to enable/disable it.
- **Out of scope (declined):** automated burner-account creation, multi-account work distribution to evade rate limits. Both are TOS-violating, technically fragile (single-IP sock puppets ban together), and approach detection-evasion. The legitimate path is one user-created burner with cookies passed via `--cookies`.

### Frontend

- React + Vite + TypeScript + Tailwind SPA at `/app/`, consuming `/api/v1/*`. Mobile-first.
- Capacitor wrapper for native iOS/Android packaging — same web build, no parallel UI codebase.
- TikTok-style swipe feed for group-feed pages (vertical snap, preload next).
- HTML templates retired (or relocated to `/admin/`).

### Delivery

- Email digest via SMTP (env-configured: `FEEDRIG_SMTP_HOST` etc.) with per-group cadence.
- Optional webhook for Slack / Discord / signal-cli.
- Push notifications via Capacitor's plugin (when SPA is up).

### Infra

- Wire all media reads/writes through `storage.Blob` so swapping `LocalFS` ↔ `S3` is a config change.
- Real S3 impl using `aws-sdk-go-v2`, with Cloudflare R2 / MinIO compatibility tested.
- Optional auth (single-user session cookie) once the binary leaves localhost.

### Storage growth control

- **Metadata TTL on archived rows.** Today archived videos persist forever. A second-stage TTL ("hard-purge metadata after N days, default ~365") would cap growth. Rough storage math per archived row:
    - thumbnail JPEG: ~30 KB
    - summary + notes (text): ~1–2 KB
    - transcript (text, 60s reel): ~2–5 KB
    - row metadata (title/desc/etc.): ~1 KB
    - **per row: ~35–40 KB**
  - 50 creators × 30 posts/yr × 1 yr ≈ 50 MB. Fine.
  - 200 creators × 100 posts/yr × 5 yrs ≈ 3.5 GB. Probably fine, getting noticeable.
  - Add a `metadata_ttl_days` setting + a second sweep pass; default ~365 (or 0 = unlimited). Surface in the settings page.

### Smaller follow-ups

- Per-creator UI to override poll cadence (column already in DB).
- Drag-to-reorder groups (column already in DB).
- Item cap, min/max-duration, watched-only filters on smart playlists.
- Hot-reload of the scheduler when creators are added/deleted (no server restart).
- RSS export per group.
- React SPA / Capacitor wrap.

---

## How to update this file

When working on a milestone:

1. Move it to "in progress" if not already.
2. Tick boxes as features land.
3. Add deviations as they happen — spec drift is normal; what matters is recording it.
4. When all boxes for a milestone are ticked, change status to ✅ shipped and add a brief retrospective.

Keep entries terse. Detailed rationale belongs in [`DECISIONS.md`](DECISIONS.md), not here.
