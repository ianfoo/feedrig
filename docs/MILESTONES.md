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

## v0.5 — Digest + S3 + React SPA

**Status:** ⏳ planned.

**Features:**

- [ ] Digest renderer: per-group HTML digest of new content with summaries + thumbnails. Configurable cadence (daily / weekly).
- [ ] Delivery channels: in-app digest page, email (SMTP env config), optional webhook for "Slack / Discord / signal-cli".
- [ ] `storage.Blob` interface; impls: `localfs`, `s3` (AWS or any S3-compatible — Cloudflare R2, MinIO).
- [ ] React + Vite + TypeScript SPA at `/app/`. Mobile-first. Tailwind for styling. Wraps cleanly under Capacitor for native packaging.
- [ ] HTML template UI removed (or kept as `/admin/` debug surface — decide at the time).

**Deviations:** TBD.

---

## How to update this file

When working on a milestone:

1. Move it to "in progress" if not already.
2. Tick boxes as features land.
3. Add deviations as they happen — spec drift is normal; what matters is recording it.
4. When all boxes for a milestone are ticked, change status to ✅ shipped and add a brief retrospective.

Keep entries terse. Detailed rationale belongs in [`DECISIONS.md`](DECISIONS.md), not here.
