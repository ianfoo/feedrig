package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/groups"
	"github.com/ianfoo/feedrig/internal/video"
)

// JSON API surface. Mirrors the most useful HTML routes so a future SPA
// (React/Vite/Tailwind, Capacitor-wrappable) can consume the same data.
// Versioned under /api/v1/ — when we ship breaking changes, bump.

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/creators", s.apiListCreators)
	mux.HandleFunc("POST /api/v1/creators", s.apiAddCreator)
	mux.HandleFunc("DELETE /api/v1/creators/{id}", s.apiDeleteCreator)

	mux.HandleFunc("GET /api/v1/videos/{id}", s.apiGetVideo)
	mux.HandleFunc("POST /api/v1/videos/{id}/position", s.savePosition) // reuse: returns 204
	mux.HandleFunc("POST /api/v1/videos/{id}/save", s.apiSaveVideo)
	mux.HandleFunc("POST /api/v1/videos/{id}/delete", s.apiDeleteVideo)

	mux.HandleFunc("GET /api/v1/groups", s.apiListGroups)
	mux.HandleFunc("GET /api/v1/groups/{slug}/feed", s.apiGroupFeed)
	mux.HandleFunc("GET /api/v1/search", s.apiSearch)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---- DTOs ----

type creatorDTO struct {
	ID            int64  `json:"id"`
	Handle        string `json:"handle"`
	DisplayName   string `json:"display_name,omitempty"`
	ProfileURL    string `json:"profile_url"`
	AddedAt       int64  `json:"added_at"`
	LastFetchedAt int64  `json:"last_fetched_at,omitempty"`
}

func toCreatorDTO(c creator.Creator) creatorDTO {
	d := creatorDTO{
		ID:          c.ID,
		Handle:      c.Handle,
		DisplayName: c.DisplayName,
		ProfileURL:  c.ProfileURL,
		AddedAt:     c.AddedAt.Unix(),
	}
	if c.LastFetchedAt != nil {
		d.LastFetchedAt = c.LastFetchedAt.Unix()
	}
	return d
}

type videoDTO struct {
	ID              int64    `json:"id"`
	CreatorID       int64    `json:"creator_id"`
	ExternalID      string   `json:"external_id"`
	URL             string   `json:"url"`
	Title           string   `json:"title,omitempty"`
	Description     string   `json:"description,omitempty"`
	DurationSeconds int64    `json:"duration_seconds,omitempty"`
	PostedAt        int64    `json:"posted_at,omitempty"`
	DownloadedAt    int64    `json:"downloaded_at"`
	State           string   `json:"state"`
	MediaURL        string   `json:"media_url"`
	ThumbnailURL    string   `json:"thumbnail_url,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Transcript      string   `json:"transcript,omitempty"`
}

func (s *Server) toVideoDTO(v video.Video) videoDTO {
	d := videoDTO{
		ID:              v.ID,
		CreatorID:       v.CreatorID,
		ExternalID:      v.ExternalID,
		URL:             v.URL,
		Title:           v.Title,
		Description:     v.Description,
		DurationSeconds: v.DurationSeconds,
		DownloadedAt:    v.DownloadedAt.Unix(),
		State:           string(v.State),
		MediaURL:        s.publicURL(v.FilePath),
	}
	if v.PostedAt != nil {
		d.PostedAt = v.PostedAt.Unix()
	}
	if v.ThumbnailPath != "" {
		d.ThumbnailURL = s.publicURL(v.ThumbnailPath)
	}
	return d
}

type groupDTO struct {
	ID            int64    `json:"id"`
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	RecencyDays   int      `json:"recency_days"`
	IncludeTags   []string `json:"include_tags,omitempty"`
	ExcludeTags   []string `json:"exclude_tags,omitempty"`
	LastVisitedAt int64    `json:"last_visited_at,omitempty"`
}

func toGroupDTO(g groups.Group) groupDTO {
	d := groupDTO{
		ID:          g.ID,
		Slug:        g.Slug,
		Name:        g.Name,
		RecencyDays: g.RecencyDays,
		IncludeTags: g.IncludeTags,
		ExcludeTags: g.ExcludeTags,
	}
	if g.LastVisitedAt != nil {
		d.LastVisitedAt = g.LastVisitedAt.Unix()
	}
	return d
}

// ---- handlers ----

func (s *Server) apiListCreators(w http.ResponseWriter, r *http.Request) {
	cs, err := s.creators.List(r.Context(), creator.SortHandle)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]creatorDTO, len(cs))
	for i, c := range cs {
		out[i] = toCreatorDTO(c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiAddCreator(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	c, err := s.creators.Add(r.Context(), body.Handle, body.DisplayName)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, creator.ErrExists) {
			status = http.StatusConflict
		}
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toCreatorDTO(*c))
}

func (s *Server) apiDeleteCreator(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.creators.Delete(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiGetVideo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	v, err := s.videos.Get(r.Context(), id)
	if errors.Is(err, video.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d := s.toVideoDTO(*v)
	if tags, _ := s.enrich.TagsForVideo(r.Context(), v.ID); len(tags) > 0 {
		d.Tags = make([]string, len(tags))
		for i, t := range tags {
			d.Tags[i] = t.Name
		}
	}
	if sm, _ := s.enrich.GetSummary(r.Context(), v.ID); sm != nil {
		d.Summary = sm.Summary
	}
	if t, _ := s.enrich.GetTranscript(r.Context(), v.ID); t != nil {
		d.Transcript = t.Text
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) apiSaveVideo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.videos.SetState(r.Context(), id, video.StateSaved); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiDeleteVideo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.videos.SetState(r.Context(), id, video.StatePendingDeletion); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiListGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.groups.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]groupDTO, len(gs))
	for i, g := range gs {
		out[i] = toGroupDTO(g)
	}
	writeJSON(w, http.StatusOK, out)
}

type searchHit struct {
	VideoID   int64  `json:"video_id"`
	Title     string `json:"title"`
	Creator   string `json:"creator"`
	Field     string `json:"matched_in"`
	Excerpt   string `json:"excerpt"`
	State     string `json:"state"`
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []searchHit{})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	hits, err := s.enrich.Search(r.Context(), q, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]searchHit, 0, len(hits))
	creatorCache := map[int64]string{}
	for _, h := range hits {
		handle, ok := creatorCache[h.CreatorID]
		if !ok {
			if c, err := s.creators.Get(r.Context(), h.CreatorID); err == nil {
				handle = c.Handle
				creatorCache[h.CreatorID] = handle
			}
		}
		state := ""
		if v, err := s.videos.Get(r.Context(), h.VideoID); err == nil {
			state = string(v.State)
		}
		out = append(out, searchHit{
			VideoID: h.VideoID, Title: h.Title, Creator: handle,
			Field: h.Field, Excerpt: h.Excerpt, State: state,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiGroupFeed(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := groups.FeedQuery{OnlyUnseen: r.URL.Query().Get("unseen") == "1"}
	if t := r.URL.Query().Get("tag"); t != "" {
		q.TagsAny = []string{strings.ToLower(t)}
	}
	vids, err := s.groups.Feed(r.Context(), g, q)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]videoDTO, len(vids))
	for i, v := range vids {
		out[i] = s.toVideoDTO(v)
	}
	_ = s.groups.MarkVisited(r.Context(), g.ID)
	writeJSON(w, http.StatusOK, map[string]any{"group": toGroupDTO(*g), "videos": out})
}
