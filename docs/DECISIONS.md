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
