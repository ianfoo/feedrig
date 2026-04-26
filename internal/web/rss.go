package web

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ianfoo/feedrig/internal/groups"
)

// rssDoc is the minimal RSS 2.0 envelope. Adding fields like
// <itunes:*> later for podcast-style consumption is straightforward.
type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Generator   string    `xml:"generator"`
	PubDate     string    `xml:"pubDate"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

func (s *Server) groupRSS(w http.ResponseWriter, r *http.Request) {
	g, err := s.groups.GetBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, groups.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	vids, err := s.groups.Feed(r.Context(), g, groups.FeedQuery{Limit: 100})
	if err != nil {
		s.serverError(w, err)
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	base := scheme + "://" + r.Host

	doc := rssDoc{
		Version: "2.0",
		Channel: rssChannel{
			Title:       fmt.Sprintf("feedrig: %s", g.Name),
			Link:        fmt.Sprintf("%s/groups/%s", base, g.Slug),
			Description: fmt.Sprintf("Smart-playlist feed for %s. Recency window: %d days.", g.Name, g.RecencyDays),
			Generator:   "feedrig",
			PubDate:     time.Now().UTC().Format(time.RFC1123Z),
		},
	}

	for _, v := range vids {
		var posted time.Time
		if v.PostedAt.Valid {
			posted = time.Unix(v.PostedAt.Int64, 0)
		} else {
			posted = v.DownloadedAt
		}
		desc := ""
		if sm, _ := s.enrich.GetSummary(r.Context(), v.ID); sm != nil {
			desc = sm.Summary
		} else if v.Description.Valid {
			desc = v.Description.String
		}
		title := "(untitled)"
		if v.Title.Valid && v.Title.String != "" {
			title = v.Title.String
		}
		doc.Channel.Items = append(doc.Channel.Items, rssItem{
			Title:       title,
			Link:        fmt.Sprintf("%s/videos/%d", base, v.ID),
			GUID:        fmt.Sprintf("%s/videos/%d", base, v.ID),
			Description: desc,
			PubDate:     posted.UTC().Format(time.RFC1123Z),
		})
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		s.log.Error("rss encode", "err", err)
	}
}
