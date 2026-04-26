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

**Status:** 🚧 in progress.

**Features:**

- [ ] `chromedp` discoverer that drives a headless Chromium against `instagram.com/<handle>/`, scrolls to load more posts, and extracts shortcodes from the rendered DOM.
- [ ] `cookiedlp` discoverer: `yt-dlp --flat-playlist <profile_url> --cookies <file>` to enumerate posts using a burner-account session. Useful when the headless approach hits rate limits or login walls.
- [ ] `--discoverer` flag selects the active strategy; `auto` tries chromedp → instago and reports which path succeeded.
- [ ] Soft fallback chain: discovery failures don't crash; they return `ErrDiscovery` and the UI logs a hint about which discoverer to try next.

**Deviations:** TBD.

---

## v0.2 — Transcription, summary, auto-tagging; JSON API surface

**Status:** ⏳ planned.

**Features:**

- [ ] `transcribe.Service` interface; impls: `whispercpp` (local, default), `openrouter-whisper` (remote).
- [ ] `summarize.Service` interface; impls: `ollama` (local, default), `openrouter-llm` (remote, supports Claude / GPT / Kimi via single client).
- [ ] Schema additions: `transcripts`, `summaries`.
- [ ] Auto-tagging via LLM prompt (categories user-configurable in a `categories.txt` or DB table).
- [ ] Schema additions: `tags`, `video_tags`.
- [ ] Background worker pool: when a new video is downloaded, enqueue (transcribe → summarize → tag).
- [ ] Player page shows summary + tags; creator detail card shows category chips.
- [ ] JSON API surface introduced: `/api/v1/creators`, `/api/v1/videos`, `/api/v1/videos/{id}/position`, etc. HTML routes call into the same service layer.

**Deviations:** TBD.

---

## v0.3 — Scheduler + TTL

**Status:** ⏳ planned.

**Features:**

- [ ] In-process scheduler: per-creator polling cadence (default 6h, per-creator override via UI). Goroutine + ticker per creator, jittered.
- [ ] TTL sweeper: nightly job. `active` videos older than `ttl_days` (default 30) → `pending_deletion`. `pending_deletion` rows older than `grace_days` (default 7) → file removed, row marked `deleted` (or hard-deleted; TBD).
- [ ] Pending-deletion review page lists items about to expire with quick "Save" / "Delete now" actions.
- [ ] Configurable globals (TTL, grace, default cadence) live in a `settings` KV table.

**Deviations:** TBD.

---

## v0.4 — Smart playlists / groups + filtering

**Status:** ⏳ planned.

**Features:**

- [ ] `groups` and `creator_group_memberships` tables.
- [ ] Group cadence (separate from per-creator polling — drives the "what's new since I last looked at this group?" semantics).
- [ ] Group detail page: feed of videos new since last visit, filterable by category tags. Mark-as-seen on visit.
- [ ] Inter-group filtering: "show me only `political` across `news` group".
- [ ] Drag-to-reorder groups in the sidebar.

**Deviations:** TBD.

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
