// Package web serves the feedrig HTTP UI.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/enrich"
	"github.com/ianfoo/feedrig/internal/ingest"
	"github.com/ianfoo/feedrig/internal/video"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	creators  *creator.Store
	videos    *video.Store
	enrich    *enrich.Store
	ingest    *ingest.Service
	mediaRoot string
	tpl       *template.Template
	log       *slog.Logger
}

func NewServer(creators *creator.Store, videos *video.Store, en *enrich.Store, ing *ingest.Service, mediaRoot string, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	srv := &Server{creators: creators, videos: videos, enrich: en, ingest: ing, mediaRoot: mediaRoot, log: log}
	tpl, err := template.New("").Funcs(funcMap).Funcs(template.FuncMap{
		"thumbURL": func(v video.Video) string {
			if !v.ThumbnailPath.Valid {
				return ""
			}
			return mediaURLFor(srv.mediaRoot, v.ThumbnailPath.String)
		},
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	srv.tpl = tpl
	return srv, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Media files served from disk; relative paths only, no traversal.
	mux.Handle("GET /media/", http.StripPrefix("/media/", safeFileServer(s.mediaRoot)))

	mux.HandleFunc("GET /{$}", s.redirect("/creators"))
	mux.HandleFunc("GET /creators", s.listCreators)
	mux.HandleFunc("POST /creators", s.addCreator)
	mux.HandleFunc("POST /creators/bulk", s.bulkAddCreators)
	mux.HandleFunc("GET /creators/{id}", s.creatorDetail)
	mux.HandleFunc("POST /creators/{id}/fetch", s.fetchNew)
	mux.HandleFunc("POST /creators/{id}/add-url", s.addByURL)
	mux.HandleFunc("POST /creators/{id}/update", s.updateCreator)
	mux.HandleFunc("POST /creators/{id}/delete", s.deleteCreator)

	mux.HandleFunc("GET /videos/{id}", s.player)
	mux.HandleFunc("POST /videos/{id}/position", s.savePosition)
	mux.HandleFunc("POST /videos/{id}/save", s.saveVideo)
	mux.HandleFunc("POST /videos/{id}/delete", s.deleteVideo)

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

func (s *Server) deleteCreator(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := s.creators.Delete(r.Context(), id); err != nil {
		s.serverError(w, err)
		return
	}
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

	mediaURL := mediaURLFor(s.mediaRoot, v.FilePath)
	thumbURL := ""
	if v.ThumbnailPath.Valid {
		thumbURL = mediaURLFor(s.mediaRoot, v.ThumbnailPath.String)
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
	// Best-effort file cleanup; tolerate missing files.
	if v.FilePath != "" {
		_ = removeIfPresent(v.FilePath)
	}
	if v.ThumbnailPath.Valid {
		_ = removeIfPresent(v.ThumbnailPath.String)
	}
	if err := s.videos.SetState(r.Context(), id, video.StatePendingDeletion); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/creators/%d?flash=Deleted", creatorID), http.StatusFound)
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

// mediaURLFor turns an absolute file path under mediaRoot into a /media/... URL.
func mediaURLFor(mediaRoot, absPath string) string {
	rel, err := filepath.Rel(mediaRoot, absPath)
	if err != nil {
		return ""
	}
	return "/media/" + filepath.ToSlash(rel)
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
	"humanTime": func(t any) string {
		var tt time.Time
		switch v := t.(type) {
		case time.Time:
			tt = v
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
