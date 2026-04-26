// Package web serves the feedrig HTTP UI.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/enrich"
	"github.com/ianfoo/feedrig/internal/groups"
	"github.com/ianfoo/feedrig/internal/ingest"
	"github.com/ianfoo/feedrig/internal/settings"
	"github.com/ianfoo/feedrig/internal/storage"
	"github.com/ianfoo/feedrig/internal/video"
)

//go:embed templates/*.html static/*
var assets embed.FS

// SchedulerReloader is the small surface the web layer needs to nudge the
// scheduler when creators or cadences change. Defined locally to avoid a
// hard dependency on internal/schedule from internal/web.
type SchedulerReloader interface {
	Reload()
}

type Server struct {
	creators  *creator.Store
	videos    *video.Store
	enrich    *enrich.Store
	settings  *settings.Store
	groups    *groups.Store
	ingest    *ingest.Service
	scheduler SchedulerReloader
	mediaRoot string      // local root; still needed for static file serving + key derivation
	blob      storage.Blob // routes URL generation; LocalFS today, S3 later
	tpl       *template.Template
	log       *slog.Logger
}

func NewServer(creators *creator.Store, videos *video.Store, en *enrich.Store, st *settings.Store, gr *groups.Store, ing *ingest.Service, mediaRoot string, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	srv := &Server{
		creators:  creators,
		videos:    videos,
		enrich:    en,
		settings:  st,
		groups:    gr,
		ingest:    ing,
		mediaRoot: mediaRoot,
		blob:      storage.LocalFS{Root: mediaRoot, URLPrefix: "/media"},
		log:       log,
	}
	tpl, err := template.New("").Funcs(funcMap).Funcs(template.FuncMap{
		"thumbURL": func(v video.Video) string {
			if v.ThumbnailPath == "" {
				return ""
			}
			key := storage.KeyFor(srv.mediaRoot, v.ThumbnailPath)
			if key == "" {
				return ""
			}
			return srv.blob.PublicURL(key)
		},
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	srv.tpl = tpl
	return srv, nil
}

// SetScheduler wires the scheduler so creator-mutating handlers can hot-
// reload it. Safe to leave unset; reloads simply become no-ops.
func (s *Server) SetScheduler(r SchedulerReloader) { s.scheduler = r }

// SetBlob overrides the default LocalFS storage. Useful for tests and for a
// future S3-backed deployment without changing the call sites that already
// route through s.blob for URL generation.
func (s *Server) SetBlob(b storage.Blob) {
	if b != nil {
		s.blob = b
	}
}

// publicURL is the canonical Server-bound helper for turning an absolute
// on-disk media path (as stored on the videos row) into a URL the user
// can hit. Today: /media/<rel>. With an S3 backend: a presigned URL.
func (s *Server) publicURL(absPath string) string {
	key := storage.KeyFor(s.mediaRoot, absPath)
	if key == "" {
		return ""
	}
	return s.blob.PublicURL(key)
}

// reload nudges the scheduler if one is wired. Errors are swallowed since a
// missed reload only means the change takes effect on next restart.
func (s *Server) reload() {
	if s.scheduler != nil {
		go s.scheduler.Reload()
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// SPA: any /app/... path that doesn't resolve to a built asset falls
	// back to index.html so React Router can take over. The Vite build
	// places output under internal/web/static/app/.
	mux.HandleFunc("GET /app/", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/app/")
		if rel == "" {
			rel = "index.html"
		}
		path := "static/app/" + rel
		// Try the requested asset; if missing, serve index.html.
		if _, err := fs.Stat(assets, path); err != nil {
			path = "static/app/index.html"
		}
		f, err := assets.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, _ := f.Stat()
		if seeker, ok := f.(io.ReadSeeker); ok {
			http.ServeContent(w, r, stat.Name(), stat.ModTime(), seeker)
			return
		}
		http.NotFound(w, r)
	})

	// Media files served from disk; relative paths only, no traversal.
	mux.Handle("GET /media/", http.StripPrefix("/media/", safeFileServer(s.mediaRoot)))

	mux.HandleFunc("GET /{$}", s.redirect("/creators"))
	mux.HandleFunc("GET /creators", s.listCreators)
	mux.HandleFunc("POST /creators", s.addCreator)
	mux.HandleFunc("POST /creators/bulk", s.bulkAddCreators)
	mux.HandleFunc("POST /creators/import", s.importFollowing)
	mux.HandleFunc("GET /creators/{id}", s.creatorDetail)
	mux.HandleFunc("GET /creators/{id}/history", s.creatorHistory)
	mux.HandleFunc("POST /creators/{id}/fetch", s.fetchNew)
	mux.HandleFunc("POST /creators/{id}/add-url", s.addByURL)
	mux.HandleFunc("POST /creators/{id}/update", s.updateCreator)
	mux.HandleFunc("POST /creators/{id}/cadence", s.updateCreatorCadence)
	mux.HandleFunc("POST /creators/{id}/delete", s.deleteCreator)

	mux.HandleFunc("GET /videos/{id}", s.player)
	mux.HandleFunc("POST /videos/{id}/position", s.savePosition)
	mux.HandleFunc("POST /videos/{id}/save", s.saveVideo)
	mux.HandleFunc("POST /videos/{id}/delete", s.deleteVideo)
	mux.HandleFunc("POST /videos/{id}/restore", s.restoreVideo)
	mux.HandleFunc("POST /videos/{id}/redownload", s.redownloadVideo)

	mux.HandleFunc("GET /search", s.searchPage)
	mux.HandleFunc("GET /pending", s.pendingList)
	mux.HandleFunc("GET /settings", s.settingsPage)
	mux.HandleFunc("POST /settings", s.saveSettings)

	mux.HandleFunc("GET /groups", s.groupsList)
	mux.HandleFunc("POST /groups", s.createGroup)
	mux.HandleFunc("GET /groups/{slug}", s.groupFeed)
	mux.HandleFunc("GET /groups/{slug}/edit", s.groupEdit)
	mux.HandleFunc("POST /groups/{slug}/update", s.groupUpdate)
	mux.HandleFunc("POST /groups/{slug}/delete", s.groupDelete)
	mux.HandleFunc("POST /groups/{slug}/members", s.groupSetMembers)
	mux.HandleFunc("POST /groups/{slug}/reorder", s.groupReorder)
	mux.HandleFunc("GET /groups/{slug}/digest", s.groupDigest)
	mux.HandleFunc("GET /groups/{slug}/rss", s.groupRSS)

	s.registerAPI(mux)

	return mux
}

// --- handlers ---

func (s *Server) redirect(to string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, to, http.StatusFound)
	}
}

func (s *Server) listCreators(w http.ResponseWriter, r *http.Request) {
	order := creator.SortOrder(r.URL.Query().Get("sort"))
	if order == "" {
		order = creator.SortHandle
	}
	cs, err := s.creators.List(r.Context(), order)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.render(w, "creators.html", map[string]any{
		"Creators": cs,
		"Sort":     string(order),
		"Flash":    r.URL.Query().Get("flash"),
		"Error":    r.URL.Query().Get("err"),
	})
}

func (s *Server) addCreator(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	c, err := s.creators.Add(r.Context(), r.FormValue("handle"), r.FormValue("display_name"))
	if err != nil {
		http.Redirect(w, r, "/creators?err="+escape(err.Error()), http.StatusFound)
		return
	}
	s.reload()
	http.Redirect(w, r, fmt.Sprintf("/creators/%d?flash=Added+%s", c.ID, c.Handle), http.StatusFound)
}

func (s *Server) bulkAddCreators(w http.ResponseWriter, r *http.Request) {
	// Accept either a textarea field or a file upload, both named "list".
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		// Not multipart? Try plain form.
		if err := r.ParseForm(); err != nil {
			s.userError(w, "invalid form")
			return
		}
	}

	var content string
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		buf := make([]byte, 1<<20)
		n, _ := f.Read(buf)
		content = string(buf[:n])
	}
	if extra := r.FormValue("list"); extra != "" {
		if content != "" {
			content += "\n"
		}
		content += extra
	}
	if content == "" {
		http.Redirect(w, r, "/creators?err=No+list+provided", http.StatusFound)
		return
	}
	res := s.creators.BulkAdd(r.Context(), content)
	if len(res.Added) > 0 {
		s.reload()
	}
	flash := fmt.Sprintf("Added %d, skipped %d, failed %d",
		len(res.Added), len(res.Skipped), len(res.Failures))
	http.Redirect(w, r, "/creators?flash="+escape(flash), http.StatusFound)
}

func (s *Server) updateCreator(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	if err := s.creators.UpdateDisplayName(r.Context(), id, r.FormValue("display_name")); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/creators/%d?flash=Updated", id), http.StatusFound)
}

func (s *Server) updateCreatorCadence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	hours, _ := strconv.Atoi(r.FormValue("poll_hours"))
	var seconds int64
	if hours > 0 {
		seconds = int64((time.Duration(hours) * time.Hour).Seconds())
	}
	if err := s.creators.SetPollIntervalSeconds(r.Context(), id, seconds); err != nil {
		s.serverError(w, err)
		return
	}
	s.reload()
	flash := "Cadence updated"
	if seconds == 0 {
		flash = "Cadence override cleared"
	}
	http.Redirect(w, r, fmt.Sprintf("/creators/%d?flash=%s", id, escape(flash)), http.StatusFound)
}

func (s *Server) importFollowing(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Redirect(w, r, "/creators?err="+escape("Upload failed: "+err.Error()), http.StatusFound)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Redirect(w, r, "/creators?err=Choose+a+following.json+to+upload", http.StatusFound)
		return
	}
	defer f.Close()
	entries, err := creator.ParseFollowingJSON(f)
	if err != nil {
		http.Redirect(w, r, "/creators?err="+escape("Parse failed: "+err.Error()), http.StatusFound)
		return
	}
	added, updated, failures := s.creators.ImportFollowing(r.Context(), entries)
	if added > 0 {
		s.reload()
	}
	flash := fmt.Sprintf("Imported %d, updated %d, failed %d (parsed %d entries)",
		added, updated, len(failures), len(entries))
	http.Redirect(w, r, "/creators?sort=followed&flash="+escape(flash), http.StatusFound)
}

func (s *Server) deleteCreator(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := s.creators.Delete(r.Context(), id); err != nil {
		s.serverError(w, err)
		return
	}
	s.reload()
	http.Redirect(w, r, "/creators?flash=Removed+creator", http.StatusFound)
}

func (s *Server) creatorDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	c, err := s.creators.Get(r.Context(), id)
	if errors.Is(err, creator.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	vids, err := s.videos.ListForCreator(r.Context(), id, false)
	if err != nil {
		s.serverError(w, err)
		return
	}
	latestWatchedID, _ := s.videos.LatestWatchedID(r.Context(), id)
	topTags, _ := s.enrich.TopTagsForCreator(r.Context(), id, 6)

	type row struct {
		Video       video.Video
		IsLastWatch bool
	}
	rows := make([]row, len(vids))
	for i, v := range vids {
		rows[i] = row{Video: v, IsLastWatch: v.ID == latestWatchedID}
	}

	s.render(w, "creator.html", map[string]any{
		"Creator": c,
		"Rows":    rows,
		"TopTags": topTags,
		"Flash":   r.URL.Query().Get("flash"),
		"Error":   r.URL.Query().Get("err"),
	})
}

func (s *Server) fetchNew(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	c, err := s.creators.Get(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	added, err := s.ingest.FetchNewForCreator(ctx, c)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, ingest.ErrDiscovery) {
			msg = "Profile discovery failed (Instagram likely blocked the scrape). Use the 'Add by URL' form below to paste post URLs manually."
		}
		http.Redirect(w, r, fmt.Sprintf("/creators/%d?err=%s", id, escape(msg)), http.StatusFound)
		return
	}
	flash := fmt.Sprintf("Fetched %d new", added)
	if added == 0 {
		flash = "No new posts"
	}
	http.Redirect(w, r, fmt.Sprintf("/creators/%d?flash=%s", id, escape(flash)), http.StatusFound)
}

func (s *Server) addByURL(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	url := strings.TrimSpace(r.FormValue("url"))
	if url == "" {
		http.Redirect(w, r, fmt.Sprintf("/creators/%d?err=URL+required", id), http.StatusFound)
		return
	}
	c, err := s.creators.Get(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	v, err := s.ingest.FetchURL(ctx, c, url)
	if err != nil {
		if errors.Is(err, ingest.ErrAlreadyHave) {
			http.Redirect(w, r, fmt.Sprintf("/creators/%d?flash=Already+in+library", id), http.StatusFound)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/creators/%d?err=%s", id, escape(err.Error())), http.StatusFound)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/videos/%d", v.ID), http.StatusFound)
}

func (s *Server) player(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	v, err := s.videos.Get(r.Context(), id)
	if errors.Is(err, video.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	c, err := s.creators.Get(r.Context(), v.CreatorID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	siblings, err := s.videos.ListForCreator(r.Context(), v.CreatorID, false)
	if err != nil {
		s.serverError(w, err)
		return
	}

	var prev, next *video.Video
	for i, sib := range siblings {
		if sib.ID == v.ID {
			if i > 0 {
				prev = &siblings[i-1] // newer
			}
			if i+1 < len(siblings) {
				next = &siblings[i+1] // older
			}
			break
		}
	}
	watch, err := s.videos.GetWatch(r.Context(), v.ID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	summary, _ := s.enrich.GetSummary(r.Context(), v.ID)
	tags, _ := s.enrich.TagsForVideo(r.Context(), v.ID)

	mediaURL := s.publicURL(v.FilePath)
	thumbURL := ""
	if v.ThumbnailPath != "" {
		thumbURL = s.publicURL(v.ThumbnailPath)
	}

	s.render(w, "player.html", map[string]any{
		"Video":    v,
		"Creator":  c,
		"Watch":    watch,
		"Summary":  summary,
		"Tags":     tags,
		"Prev":     prev, // newer
		"Next":     next, // older
		"MediaURL": mediaURL,
		"ThumbURL": thumbURL,
	})
}

func (s *Server) savePosition(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	pos, _ := strconv.ParseFloat(r.FormValue("position"), 64)
	watched := r.FormValue("watched") == "1"
	if err := s.videos.UpsertPosition(r.Context(), id, pos, watched); err != nil {
		s.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveVideo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := s.videos.SetState(r.Context(), id, video.StateSaved); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/videos/%d?flash=Saved", id), http.StatusFound)
}

func (s *Server) deleteVideo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	v, err := s.videos.Get(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	creatorID := v.CreatorID
	// Move to pending_deletion. Don't remove the file yet — the TTL sweeper
	// finalizes after the grace window. This gives the user a chance to
	// restore from /pending.
	if err := s.videos.SetState(r.Context(), id, video.StatePendingDeletion); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/creators/%d?flash=Moved+to+pending+deletion", creatorID), http.StatusFound)
}

func (s *Server) redownloadVideo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	v, err := s.videos.Get(r.Context(), id)
	if errors.Is(err, video.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	if err := s.ingest.Redownload(ctx, v); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/videos/%d?err=%s", id, escape(err.Error())), http.StatusFound)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/videos/%d?flash=Re-downloaded", id), http.StatusFound)
}

func (s *Server) creatorHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	c, err := s.creators.Get(r.Context(), id)
	if errors.Is(err, creator.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	vids, err := s.videos.ListForCreatorAll(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	type row struct {
		Video   video.Video
		Tags    []enrich.Tag
		Summary *enrich.Summary
	}
	rows := make([]row, len(vids))
	for i, v := range vids {
		tags, _ := s.enrich.TagsForVideo(r.Context(), v.ID)
		summary, _ := s.enrich.GetSummary(r.Context(), v.ID)
		rows[i] = row{Video: v, Tags: tags, Summary: summary}
	}
	s.render(w, "creator_history.html", map[string]any{
		"Creator": c,
		"Rows":    rows,
		"Flash":   r.URL.Query().Get("flash"),
		"Error":   r.URL.Query().Get("err"),
	})
}

func (s *Server) restoreVideo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := s.videos.SetState(r.Context(), id, video.StateActive); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/pending?flash=Restored", http.StatusFound)
}

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	type row struct {
		Video   video.Video
		Creator string
		Hit     enrich.SearchHit
	}
	var rows []row
	if q != "" {
		hits, err := s.enrich.Search(r.Context(), q, 100)
		if err != nil {
			s.serverError(w, err)
			return
		}
		creatorCache := map[int64]string{}
		for _, h := range hits {
			v, err := s.videos.Get(r.Context(), h.VideoID)
			if err != nil {
				continue
			}
			handle, ok := creatorCache[h.CreatorID]
			if !ok {
				if c, err := s.creators.Get(r.Context(), h.CreatorID); err == nil {
					handle = c.Handle
					creatorCache[h.CreatorID] = handle
				}
			}
			rows = append(rows, row{Video: *v, Creator: handle, Hit: h})
		}
	}
	s.render(w, "search.html", map[string]any{
		"Query": q,
		"Rows":  rows,
	})
}

func (s *Server) pendingList(w http.ResponseWriter, r *http.Request) {
	vids, err := s.videos.ListByState(r.Context(), video.StatePendingDeletion)
	if err != nil {
		s.serverError(w, err)
		return
	}
	ttlDays, graceDays := s.settings.TTL(r.Context())
	type row struct {
		Video   video.Video
		Creator string
	}
	rows := make([]row, len(vids))
	for i, v := range vids {
		c, err := s.creators.Get(r.Context(), v.CreatorID)
		var handle string
		if err == nil {
			handle = c.Handle
		}
		rows[i] = row{Video: v, Creator: handle}
	}
	s.render(w, "pending.html", map[string]any{
		"Rows":          rows,
		"TTLDays":   settings.DaysFromDuration(ttlDays),
		"GraceDays": settings.DaysFromDuration(graceDays),
		"Flash":         r.URL.Query().Get("flash"),
		"Error":         r.URL.Query().Get("err"),
	})
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	pollInt := s.settings.PollInterval(r.Context())
	ttl, grace := s.settings.TTL(r.Context())
	metaTTL := s.settings.MetadataTTL(r.Context())
	metaTTLDays := -1 // sentinel for "unlimited"
	if metaTTL > 0 {
		metaTTLDays = settings.DaysFromDuration(metaTTL)
	}
	s.render(w, "settings.html", map[string]any{
		"PollHours":       int(pollInt.Hours()),
		"TTLDays":         settings.DaysFromDuration(ttl),
		"GraceDays":       settings.DaysFromDuration(grace),
		"MetadataTTLDays": metaTTLDays,
		"Flash":           r.URL.Query().Get("flash"),
		"Error":           r.URL.Query().Get("err"),
	})
}

func (s *Server) groupsList(w http.ResponseWriter, r *http.Request) {
	gs, err := s.groups.List(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.render(w, "groups.html", map[string]any{
		"Groups": gs,
		"Flash":  r.URL.Query().Get("flash"),
		"Error":  r.URL.Query().Get("err"),
	})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	recency, _ := strconv.Atoi(r.FormValue("recency_days"))
	g, err := s.groups.Create(r.Context(),
		r.FormValue("name"), recency,
		splitCSVField(r.FormValue("include_tags")),
		splitCSVField(r.FormValue("exclude_tags")),
	)
	if err != nil {
		http.Redirect(w, r, "/groups?err="+escape(err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/groups/"+g.Slug+"/edit?flash=Created", http.StatusFound)
}

func (s *Server) groupFeed(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}

	q := groups.FeedQuery{
		OnlyUnseen: r.URL.Query().Get("unseen") == "1",
	}
	if t := r.URL.Query().Get("tag"); t != "" {
		q.TagsAny = []string{strings.ToLower(t)}
	}
	switch r.URL.Query().Get("watched") {
	case "yes":
		q.Watched = groups.WatchedOnly
	case "no":
		q.Watched = groups.WatchedUnwatched
	}
	if minD, _ := strconv.Atoi(r.URL.Query().Get("min")); minD > 0 {
		q.MinDuration = minD
	}
	if maxD, _ := strconv.Atoi(r.URL.Query().Get("max")); maxD > 0 {
		q.MaxDuration = maxD
	}
	if limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); limit > 0 {
		q.Limit = limit
	}

	vids, err := s.groups.Feed(r.Context(), g, q)
	if err != nil {
		s.serverError(w, err)
		return
	}

	type row struct {
		Video   video.Video
		Creator string
		Tags    []enrich.Tag
		Summary *enrich.Summary
	}
	rows := make([]row, len(vids))
	creatorCache := map[int64]string{}
	for i, v := range vids {
		handle, ok := creatorCache[v.CreatorID]
		if !ok {
			if c, err := s.creators.Get(r.Context(), v.CreatorID); err == nil {
				handle = c.Handle
				creatorCache[v.CreatorID] = handle
			}
		}
		tags, _ := s.enrich.TagsForVideo(r.Context(), v.ID)
		summary, _ := s.enrich.GetSummary(r.Context(), v.ID)
		rows[i] = row{Video: v, Creator: handle, Tags: tags, Summary: summary}
	}

	// Mark visited AFTER computing the feed so "unseen" results stay stable
	// for this render. The next visit gets the new boundary.
	_ = s.groups.MarkVisited(r.Context(), g.ID)

	s.render(w, "group_feed.html", map[string]any{
		"Group":         g,
		"Rows":          rows,
		"OnlyUnseen":    q.OnlyUnseen,
		"ActiveTag":     r.URL.Query().Get("tag"),
		"WatchedFilter": r.URL.Query().Get("watched"),
		"MinDuration":   q.MinDuration,
		"MaxDuration":   q.MaxDuration,
		"Limit":         q.Limit,
		"Flash":         r.URL.Query().Get("flash"),
		"Error":         r.URL.Query().Get("err"),
	})
}

func (s *Server) groupDigest(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	q := groups.FeedQuery{OnlyUnseen: r.URL.Query().Get("unseen") == "1"}
	vids, err := s.groups.Feed(r.Context(), g, q)
	if err != nil {
		s.serverError(w, err)
		return
	}
	type row struct {
		Video   video.Video
		Creator string
		Tags    []enrich.Tag
		Summary *enrich.Summary
	}
	rows := make([]row, len(vids))
	creatorCache := map[int64]string{}
	for i, v := range vids {
		handle := creatorCache[v.CreatorID]
		if handle == "" {
			if c, err := s.creators.Get(r.Context(), v.CreatorID); err == nil {
				handle = c.Handle
				creatorCache[v.CreatorID] = handle
			}
		}
		tags, _ := s.enrich.TagsForVideo(r.Context(), v.ID)
		summary, _ := s.enrich.GetSummary(r.Context(), v.ID)
		rows[i] = row{Video: v, Creator: handle, Tags: tags, Summary: summary}
	}
	s.render(w, "digest.html", map[string]any{
		"Group":       g,
		"Rows":        rows,
		"GeneratedAt": time.Now(),
	})
}

func (s *Server) groupEdit(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	allCreators, err := s.creators.List(r.Context(), creator.SortHandle)
	if err != nil {
		s.serverError(w, err)
		return
	}
	members, err := s.groups.Members(r.Context(), g.ID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	memberMap := map[int64]groups.Membership{}
	for _, m := range members {
		memberMap[m.CreatorID] = m
	}
	type row struct {
		Creator  creator.Creator
		Included bool
		Excluded bool
	}
	rows := make([]row, len(allCreators))
	for i, c := range allCreators {
		m, ok := memberMap[c.ID]
		rows[i] = row{Creator: c, Included: ok && !m.Excluded, Excluded: ok && m.Excluded}
	}

	s.render(w, "group_edit.html", map[string]any{
		"Group":   g,
		"Rows":    rows,
		"Flash":   r.URL.Query().Get("flash"),
		"Error":   r.URL.Query().Get("err"),
	})
}

func (s *Server) groupUpdate(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	recency, _ := strconv.Atoi(r.FormValue("recency_days"))
	if err := s.groups.Update(r.Context(), g.ID,
		r.FormValue("name"), recency,
		splitCSVField(r.FormValue("include_tags")),
		splitCSVField(r.FormValue("exclude_tags")),
	); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/groups/"+g.Slug+"/edit?flash=Updated", http.StatusFound)
}

func (s *Server) groupDelete(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := s.groups.Delete(r.Context(), g.ID); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/groups?flash=Deleted", http.StatusFound)
}

func (s *Server) groupReorder(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	dir := -1
	if r.FormValue("dir") == "down" {
		dir = 1
	}
	if err := s.groups.Reorder(r.Context(), g.ID, dir); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/groups", http.StatusFound)
}

func (s *Server) groupSetMembers(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	included := parseInt64s(r.Form["include[]"])
	excluded := parseInt64s(r.Form["exclude[]"])
	if err := s.groups.SetMembers(r.Context(), g.ID, included, excluded); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/groups/"+g.Slug+"/edit?flash=Members+updated", http.StatusFound)
}

func splitCSVField(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseInt64s(ss []string) []int64 {
	out := make([]int64, 0, len(ss))
	for _, s := range ss {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.userError(w, "invalid form")
		return
	}
	pollHours, _ := strconv.Atoi(r.FormValue("poll_hours"))
	ttlDays, _ := strconv.Atoi(r.FormValue("ttl_days"))
	graceDays, _ := strconv.Atoi(r.FormValue("grace_days"))
	metaTTLRaw := strings.TrimSpace(r.FormValue("metadata_ttl_days"))

	if pollHours > 0 {
		seconds := int((time.Duration(pollHours) * time.Hour).Seconds())
		_ = s.settings.Set(r.Context(), settings.KeyDefaultPollInterval, strconv.Itoa(seconds))
	}
	if ttlDays > 0 {
		_ = s.settings.Set(r.Context(), settings.KeyTTLDays, strconv.Itoa(ttlDays))
	}
	if graceDays > 0 {
		_ = s.settings.Set(r.Context(), settings.KeyGraceDays, strconv.Itoa(graceDays))
	}
	// Metadata TTL accepts 0 explicitly (= unlimited) and skips on empty.
	if metaTTLRaw != "" {
		if n, err := strconv.Atoi(metaTTLRaw); err == nil && n >= 0 {
			_ = s.settings.Set(r.Context(), settings.KeyMetadataTTLDays, strconv.Itoa(n))
		}
	}
	s.reload()
	http.Redirect(w, r, "/settings?flash=Saved", http.StatusFound)
}

// --- helpers ---

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render", "template", name, "err", err)
	}
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.log.Error("server error", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) userError(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}

func pathInt(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v := r.PathValue(name)
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func escape(s string) string {
	r := strings.NewReplacer(" ", "+", "&", "%26", "?", "%3F", "#", "%23", "+", "%2B")
	return r.Replace(s)
}

func removeIfPresent(path string) error {
	return safeRemove(path)
}

// mediaURLFor turns an absolute file path under mediaRoot into a public
// URL via the configured storage backend. Today the backend is LocalFS
// and the URL is "/media/...". An S3 backend would return a presigned URL.
//
// Free function (vs. method) because templates carry it as a func value.
func mediaURLFor(mediaRoot, absPath string) string {
	key := storage.KeyFor(mediaRoot, absPath)
	if key == "" {
		return ""
	}
	// Templates are bound at server init with a closure that knows the
	// active blob. This package-level fallback handles legacy callers
	// (e.g. handlers that pass mediaRoot rather than the closure) and
	// always emits the /media/-prefixed local URL.
	return "/media/" + key
}

// thumbURL is bound at server init so templates can compute media URLs from
// a Video without knowing the media root.
type thumbResolver func(v video.Video) string

var funcMap = template.FuncMap{
	"dict": func(pairs ...any) (map[string]any, error) {
		if len(pairs)%2 != 0 {
			return nil, fmt.Errorf("dict needs even number of args")
		}
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			k, ok := pairs[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict key %d not a string", i)
			}
			m[k] = pairs[i+1]
		}
		return m, nil
	},
	"slice": func(items ...any) []any { return items },
	"joinTags": func(tags []string) string { return strings.Join(tags, ", ") },
	"pollHours": func(seconds int64) int64 {
		return seconds / int64(time.Hour.Seconds())
	},
	"humanTime": func(t any) string {
		var tt time.Time
		switch v := t.(type) {
		case time.Time:
			tt = v
		case *time.Time:
			if v == nil {
				return ""
			}
			tt = *v
		case int64:
			tt = time.Unix(v, 0)
		default:
			return ""
		}
		if tt.IsZero() {
			return ""
		}
		return tt.Format("Jan 2, 2006 3:04pm")
	},
	"duration": func(secs any) string {
		var s int64
		switch v := secs.(type) {
		case int64:
			s = v
		case int:
			s = int64(v)
		case float64:
			s = int64(v)
		default:
			return ""
		}
		if s <= 0 {
			return ""
		}
		m := s / 60
		r := s % 60
		return fmt.Sprintf("%d:%02d", m, r)
	},
}
