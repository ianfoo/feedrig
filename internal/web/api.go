package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/groups"
	"github.com/ianfoo/feedrig/internal/settings"
	"github.com/ianfoo/feedrig/internal/stats"
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

	// Library + worker stats. Cheap, ~30s cached.
	mux.HandleFunc("GET /api/v1/stats", s.apiStats)

	// Settings (global tunables: poll cadence, TTL, grace, metadata TTL).
	mux.HandleFunc("GET /api/v1/settings", s.apiGetSettings)
	mux.HandleFunc("PUT /api/v1/settings", s.apiPutSettings)

	// Per-creator detail + actions.
	mux.HandleFunc("GET /api/v1/creators/{id}/videos", s.apiCreatorVideos)
	mux.HandleFunc("POST /api/v1/creators/{id}/fetch", s.apiCreatorFetch)
	mux.HandleFunc("POST /api/v1/creators/{id}/cadence", s.apiCreatorCadence)
	mux.HandleFunc("POST /api/v1/creators/fetch-all", s.apiFetchAll)
	mux.HandleFunc("POST /api/v1/creators/bulk", s.apiBulkAddCreators)

	// Groups CRUD + members + reorder.
	mux.HandleFunc("POST /api/v1/groups", s.apiCreateGroup)
	mux.HandleFunc("GET /api/v1/groups/{slug}", s.apiGetGroup)
	mux.HandleFunc("PUT /api/v1/groups/{slug}", s.apiUpdateGroup)
	mux.HandleFunc("DELETE /api/v1/groups/{slug}", s.apiDeleteGroup)
	mux.HandleFunc("POST /api/v1/groups/{slug}/members", s.apiSetGroupMembers)
	mux.HandleFunc("POST /api/v1/groups/{slug}/reorder", s.apiReorderGroup)

	// Pending-deletion review.
	mux.HandleFunc("GET /api/v1/pending", s.apiPendingList)
	mux.HandleFunc("POST /api/v1/videos/{id}/restore", s.apiRestoreVideo)
	mux.HandleFunc("POST /api/v1/videos/{id}/redownload", s.apiRedownloadVideo)
}

func (s *Server) apiStats(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	snap := s.stats.Latest(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"media_bytes":       snap.MediaBytes,
		"media_bytes_human": stats.HumanBytes(snap.MediaBytes),
		"video_count":       snap.VideoCount,
		"saved_count":       snap.SavedCount,
		"archived_count":    snap.ArchivedCount,
		"pending_count":     snap.PendingCount,
		"generated_at":      snap.GeneratedAt.Unix(),
	})
}

// --- Settings ---

func (s *Server) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	pollInt := s.settings.PollInterval(r.Context())
	ttl, grace := s.settings.TTL(r.Context())
	metaTTL := s.settings.MetadataTTL(r.Context())
	out := map[string]any{
		"poll_hours":  int(pollInt.Hours()),
		"ttl_days":    settings.DaysFromDuration(ttl),
		"grace_days":  settings.DaysFromDuration(grace),
	}
	if metaTTL > 0 {
		out["metadata_ttl_days"] = settings.DaysFromDuration(metaTTL)
	} else {
		out["metadata_ttl_days"] = 0
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiPutSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PollHours       *int `json:"poll_hours"`
		TTLDays         *int `json:"ttl_days"`
		GraceDays       *int `json:"grace_days"`
		MetadataTTLDays *int `json:"metadata_ttl_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.PollHours != nil && *body.PollHours > 0 {
		seconds := int((time.Duration(*body.PollHours) * time.Hour).Seconds())
		_ = s.settings.Set(r.Context(), settings.KeyDefaultPollInterval, strconv.Itoa(seconds))
	}
	if body.TTLDays != nil && *body.TTLDays > 0 {
		_ = s.settings.Set(r.Context(), settings.KeyTTLDays, strconv.Itoa(*body.TTLDays))
	}
	if body.GraceDays != nil && *body.GraceDays > 0 {
		_ = s.settings.Set(r.Context(), settings.KeyGraceDays, strconv.Itoa(*body.GraceDays))
	}
	if body.MetadataTTLDays != nil && *body.MetadataTTLDays >= 0 {
		_ = s.settings.Set(r.Context(), settings.KeyMetadataTTLDays, strconv.Itoa(*body.MetadataTTLDays))
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

// --- Per-creator videos + actions ---

func (s *Server) apiCreatorVideos(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	includeAll := r.URL.Query().Get("include_archived") == "1"
	var vids []video.Video
	if includeAll {
		vids, err = s.videos.ListForCreatorAll(r.Context(), id)
	} else {
		vids, err = s.videos.ListForCreator(r.Context(), id, false)
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]videoDTO, len(vids))
	for i, v := range vids {
		d := s.toVideoDTO(v)
		if tags, _ := s.enrich.TagsForVideo(r.Context(), v.ID); len(tags) > 0 {
			d.Tags = make([]string, len(tags))
			for j, t := range tags {
				d.Tags[j] = t.Name
			}
		}
		if sm, _ := s.enrich.GetSummary(r.Context(), v.ID); sm != nil {
			d.Summary = sm.Summary
		}
		out[i] = d
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiCreatorFetch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	c, err := s.creators.Get(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	started := s.startFetch(c)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": started, "creator": c.Handle})
}

func (s *Server) apiFetchAll(w http.ResponseWriter, r *http.Request) {
	cs, err := s.creators.List(r.Context(), creator.SortHandle)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	started, skipped := 0, 0
	for i := range cs {
		c := cs[i]
		if s.startFetch(&c) {
			started++
		} else {
			skipped++
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"started": started, "skipped": skipped})
}

func (s *Server) apiCreatorCadence(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		PollHours int `json:"poll_hours"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var seconds int64
	if body.PollHours > 0 {
		seconds = int64((time.Duration(body.PollHours) * time.Hour).Seconds())
	}
	if err := s.creators.SetPollIntervalSeconds(r.Context(), id, seconds); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiBulkAddCreators(w http.ResponseWriter, r *http.Request) {
	var body struct {
		List string `json:"list"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	res := s.creators.BulkAdd(r.Context(), body.List)
	if len(res.Added) > 0 {
		s.reload()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added":   len(res.Added),
		"skipped": len(res.Skipped),
		"failed":  len(res.Failures),
	})
}

// --- Groups CRUD ---

type groupBody struct {
	Name        string   `json:"name"`
	RecencyDays int      `json:"recency_days"`
	IncludeTags []string `json:"include_tags"`
	ExcludeTags []string `json:"exclude_tags"`
}

func (s *Server) apiCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body groupBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	g, err := s.groups.Create(r.Context(), body.Name, body.RecencyDays, body.IncludeTags, body.ExcludeTags)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toGroupDTO(*g))
}

func (s *Server) apiGetGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	members, _ := s.groups.Members(r.Context(), g.ID)
	type memberDTO struct {
		CreatorID int64 `json:"creator_id"`
		Excluded  bool  `json:"excluded"`
	}
	mds := make([]memberDTO, len(members))
	for i, m := range members {
		mds[i] = memberDTO{CreatorID: m.CreatorID, Excluded: m.Excluded}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":   toGroupDTO(*g),
		"members": mds,
	})
}

func (s *Server) apiUpdateGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body groupBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.groups.Update(r.Context(), g.ID, body.Name, body.RecencyDays, body.IncludeTags, body.ExcludeTags); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiDeleteGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.groups.Delete(r.Context(), g.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiSetGroupMembers(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body struct {
		Include []int64 `json:"include"`
		Exclude []int64 `json:"exclude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.groups.SetMembers(r.Context(), g.ID, body.Include, body.Exclude); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiReorderGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body struct {
		Dir string `json:"dir"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	dir := -1
	if body.Dir == "down" {
		dir = 1
	}
	if err := s.groups.Reorder(r.Context(), g.ID, dir); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Pending review ---

func (s *Server) apiPendingList(w http.ResponseWriter, r *http.Request) {
	vids, err := s.videos.ListByState(r.Context(), video.StatePendingDeletion)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]videoDTO, len(vids))
	creatorCache := map[int64]string{}
	for i, v := range vids {
		d := s.toVideoDTO(v)
		// Inline the creator handle into the DTO via Tags as a hack? No —
		// the SPA already calls /api/v1/creators to hydrate; client-side
		// join.
		_ = creatorCache
		out[i] = d
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiRestoreVideo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.videos.SetState(r.Context(), id, video.StateActive); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiRedownloadVideo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad id")
		return
	}
	v, err := s.videos.Get(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	go func() {
		ctx := s.backgroundCtx()
		if err := s.ingest.Redownload(ctx, v); err != nil {
			s.log.Warn("api redownload", "video_id", id, "err", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
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
