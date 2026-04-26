# Decision log

Lightweight ADRs. Each entry: what, why, alternatives considered, status.

---

## ADR-001: Go for the backend

**Decision:** Backend in Go (single static binary).

**Rationale:** User preference. Single-binary deployment story; good stdlib HTTP; cheap goroutine-based scheduler; `os/exec` for shelling out to `yt-dlp`/`ffmpeg`/`whisper` is straightforward.

**Alternatives:** Python (richer ML/IG-scraping ecosystem, weaker deployment), Node (mature SPA story, less fond of for backend services).

**Status:** Accepted.

---

## ADR-002: SQLite via pure-Go driver (`modernc.org/sqlite`)

**Decision:** SQLite, pure-Go driver — no CGO.

**Rationale:** Self-contained binary. Single-user concurrency profile is well within SQLite's WAL-mode envelope. Easy backup (one file).

**Alternatives:** `mattn/go-sqlite3` (CGO required), Postgres (overkill for single-user).

**Trade-off:** Slightly slower than CGO sqlite under heavy write load. Not a concern for our workload.

**Status:** Accepted.

---

## ADR-003: Set-membership over high-water-mark for "what's new"

**Decision:** Track every downloaded post in `videos(creator_id, external_id)` and compute "new" as set-difference against discovered shortcodes. Do **not** rely on a single timestamp watermark per creator.

**Rationale:** A watermark silently misses items if a creator deletes a recent post (watermark advances past content that's still live) or backfills an older one (older items are now "newer than watermark" but really older).

**Status:** Accepted.

---

## ADR-004: Discovery and download split behind interfaces

**Decision:** `ingest.Discoverer` enumerates a creator's recent shortcodes; `ingest.Downloader` fetches a single known post URL. The orchestrating `ingest.Service` composes them and persists results.

**Rationale:** Discovery is brittle and IG-specific; download is more stable. Splitting lets us swap discovery strategies (no-login scrape → headless browser → cookie-authed yt-dlp) without touching the download path.

**Status:** Accepted.

---

## ADR-005: v0.1 ingestion ships with `siongui/instago` knowing it likely fails

**Decision:** Wire up `siongui/instago.GetRecentPostCodeNoLogin` as the v0.1 discoverer even though the underlying technique (Instagram's `_sharedData` JSON blob) was largely retired by Meta years ago.

**Rationale:** It validates the interface and gives the v0.1 scaffold something to call. The manual "Add by URL" form is the user-facing fallback. v0.1.1 adds a real discovery path.

**Status:** Superseded — plan to replace with `chromedp` (headless Chromium) and/or cookie-authed `yt-dlp` for actual reliability. The user has already stated a burner IG account is acceptable.

---

## ADR-006: v0.1 frontend is server-rendered templates; React arrives at v0.5

**Decision:** Build v0.1 with `html/template` + minimal vanilla JS. Introduce a JSON API surface (`/api/v1/*`) at v0.2 alongside the existing HTML handlers. Replace HTML pages with a React + Vite + TypeScript SPA at v0.5.

**Rationale:** The user values a smooth, modern, mobile-first feel and wants the option to package as a native mobile app via Capacitor or React Native. That pushes the long-term frontend toward an SPA. But the user also said v0.1 is not the place to perfect this, and rewriting the UI would block backend feature work. So:

1. Ship v0.1 fast on templates so the app is usable end-to-end.
2. Refactor handlers to expose JSON at v0.2 — needed anyway for the player's incremental position-save and for v0.4's smart-playlist filtering.
3. Build the SPA on the same JSON API at v0.5. The same React build will wrap with Capacitor for native packaging without a code rewrite.

**Trade-off:** Two UIs in the codebase between v0.2 and v0.5. The HTML templates may be retired entirely at v0.5 or kept as a low-JS admin surface — TBD when we get there.

**Status:** Accepted.

---

## ADR-007: Mobile-first; native via Capacitor primarily

**Decision:** The v0.5 React SPA targets phones first. Native packaging is via Capacitor (web bundle wrapped in a WKWebView/Android WebView shell), with React Native as an optional later refactor if a fully-native shell becomes worthwhile.

**Rationale:** Capacitor reuses the entire React codebase verbatim — minimal duplicated work, fastest path to a phone-installed app. React Native shares only logic / API code; UI components must be rewritten with `react-native` primitives, which doubles the maintenance surface for a one-developer project.

**Status:** Accepted (as default direction; revisit at v0.5).

---

## ADR-008: Pluggable summarization stack; default to local

**Decision:** `summarize.Service` accepts a `Transcriber` and a `Summarizer` interface. v0.2 ships:

- Transcriber: `whisper.cpp` (local) by default; OpenAI Whisper via OpenRouter as an opt-in alternative.
- Summarizer: local LLM via `ollama` HTTP by default; remote LLMs (Claude / OpenAI / Kimi) via a single OpenRouter HTTP client as an opt-in alternative.

**Rationale:** User explicitly asked for local-first with easy switching. OpenRouter is one HTTP integration that fans out to multiple providers, sparing us from per-vendor SDK churn.

**Status:** Accepted (planned for v0.2).

---

## ADR-009: TTL with grace state, not hard delete

**Decision:** TTL sweeper (v0.3) marks unsaved videos older than `ttl_days` (default 30) as `pending_deletion`, leaving the file on disk. A second pass after `grace_days` (default 7) removes the file and finalizes the row to `deleted` (or hard-deletes the row).

**Rationale:** Lets the user review and rescue items before they're truly gone, matching the user's stated preference for "I want to see what's about to be tossed out."

**Status:** Accepted (planned for v0.3).

---

## ADR-010: Smart playlists are queries, not materialized lists

**Decision:** A "smart playlist" / group view is a saved query: `(creator_group_id, category_filters[], recency_window, last_checked_at)`. Results are computed on demand from the `videos` table. The only persistent group state is membership and the per-group `last_checked_at`.

**Rationale:** Avoids a sync problem between materialized rows and the underlying videos. Filter changes are instantaneous. Cost is fine — a few hundred to a few thousand videos per group max in a personal-use tool.

**Status:** Accepted (planned for v0.4).

---

## ADR-012: Domain types vs. SQL types (cleanup queued, not done in v0.5)

**Decision (deferred to v0.6):** Introduce a domain-types layer (`internal/domain` or per-package `*_domain.go`) that uses idiomatic Go zero values (`time.Time`, `string`, `int64`, `*T` for nullable) instead of `database/sql` types (`sql.NullString`, `sql.NullInt64`). The store packages map between domain types and SQL types at the persistence boundary. Handlers, services, and templates work with domain types only; they should not import `database/sql`.

**Rationale (per user feedback during v0.5):** The current code lets `sql.NullString` leak through every layer — handlers reach into `.DisplayName.String` and `.PostedAt.Valid` directly. That couples the HTTP layer to the database driver, makes tests harder (anything that produces a `Video` has to construct `sql.Null*` zero values), and clutters templates with `.Valid` / `.Int64` accessors. A clean domain layer:

- Tests can `Video{Title: "x"}` instead of `Video{Title: sql.NullString{String: "x", Valid: true}}`.
- Templates use `{{.Title}}` instead of `{{.Title.String}}`.
- Swapping the persistence backend (e.g. Postgres) changes only the store package.
- Repository pattern lands naturally — store packages become an interface implemented today by SQLite, tomorrow by anything else.

**Why deferred:** It's a wide refactor (every store + every handler + every template) and the current code is functional; the user explicitly noted "I'm not going to sweat it too hard." Doing half a migration leaves the codebase worse than either pole.

**Migration plan when picked up:**

1. Define domain types in each existing store package (e.g. `creator.Domain`, `video.Domain`) — keep the SQL-ish `Video` / `Creator` private (`videoRow`, `creatorRow`).
2. Conversion functions: `toDomain(row)`, `toRow(domain)`.
3. Update store methods to return domain types.
4. Update handlers, templates, ingest, summarize, groups, ttl, schedule.
5. Templates: drop `.Valid` and `.Int64` accessors; use plain field access with `{{with}}` for optionals.
6. Optional: extract a repository interface so non-SQLite implementations are straightforward.

**Status:** Deferred to v0.6 cleanup. ADR captured so the intent isn't lost.

---

## ADR-013: History retention via `archived` state, not hard delete

**Decision:** When the TTL grace window expires for a `pending_deletion` video, the sweeper transitions state to `archived`, removes the media file from disk, and **keeps everything else** — title, description, summary, transcript, tag assignments, and thumbnail. The row is never hard-deleted by the sweeper.

**Rationale (per user feedback during v0.5):** "I should be able to review the creator and smart playlist histories as far back as we want, even if the corresponding video is deleted." Re-watching an archived video is a redownload of the original URL; the summary already in hand tells the user whether it's worth re-fetching. Storage cost is negligible — text data (summary + transcript) compresses well even uncompressed in SQLite, and thumbnails are small JPEGs.

**Trade-offs:**
- The `videos` table grows monotonically. SQLite handles this fine for a single-user tool indefinitely (millions of rows is well within budget).
- If a creator deletes the original post on Instagram, redownload will fail. We surface that as an error on the redownload action; the metadata stays in the row regardless.

**Status:** Accepted; superseded ADR-009 (the hard-delete version).

---

## ADR-011a: No automated burner-account creation; no multi-account work distribution

**Decision:** feedrig will not include features to:
- Auto-create Instagram burner accounts via the headless browser + email verification.
- Distribute scraping work across multiple sock-puppet accounts to evade rate limits.

**Rationale:**

1. **TOS posture.** Instagram's terms explicitly prohibit automated account creation and operation of multiple accounts to circumvent enforcement. The legitimate path is one user-created burner whose session cookies the user passes via `--cookies`.
2. **Detection-evasion line.** Auto-signup + multi-account orchestration for the explicit purpose of "looking less like one bot" is detection-evasion territory regardless of the otherwise benign use case (personal media curation). Politeness pacing — randomized sleeps, conservative scroll — stays.
3. **Technical fragility.** Single-IP sock puppets get banned together when one trips a flag, so the engineering cost doesn't even buy the resilience benefit it advertises.

**Status:** Accepted (decline). The pacing/jitter feature lands; the rest does not.

---

## ADR-011: No `run_in_background` for long-running servers in dev workflows

**Decision (process):** When smoke-testing the server during development, run it in the foreground inside a single shell command that boots, hits, and kills the server. Avoid `run_in_background` for the server.

**Rationale:** Background-process slots in the agent harness can show a stale "Running…" spinner long after the actual process is dead, creating the impression of a hang and slowing iteration. Foreground commands are more transparent and more easily reasoned about.

**Status:** Accepted (process note, no code impact).
