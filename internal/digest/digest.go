// Package digest renders per-group digests for human review and notifies
// them via configured channels. Today: HTML email body suitable for SMTP
// delivery; the same shape can feed Slack/webhook later.
package digest

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/enrich"
	"github.com/ianfoo/feedrig/internal/groups"
	"github.com/ianfoo/feedrig/internal/notify"
	"github.com/ianfoo/feedrig/internal/video"
)

// Renderer assembles a Message (subject + html + text) for a given group
// using the current state of the database.
type Renderer struct {
	Creators *creator.Store
	Videos   *video.Store
	Enrich   *enrich.Store
	Groups   *groups.Store
	// BaseURL prefixes the per-video links in the rendered HTML, e.g.
	// "https://feedrig.example.com". Required for clickable emails.
	BaseURL string
}

type item struct {
	Title    string
	Creator  string
	URL      string
	Posted   time.Time
	Tags     []string
	Summary  string
	Duration int64
}

type tplData struct {
	GroupName string
	Generated time.Time
	BaseURL   string
	Items     []item
}

// Render builds the digest for `g`. If `onlyUnseen` is true, only videos
// posted after the group's last_visited_at are included; otherwise it uses
// the group's full recency window.
func (r *Renderer) Render(ctx context.Context, g *groups.Group, onlyUnseen bool) (notify.Message, error) {
	q := groups.FeedQuery{OnlyUnseen: onlyUnseen, Limit: 100}
	vids, err := r.Groups.Feed(ctx, g, q)
	if err != nil {
		return notify.Message{}, err
	}

	creatorCache := map[int64]string{}
	items := make([]item, len(vids))
	for i, v := range vids {
		handle := creatorCache[v.CreatorID]
		if handle == "" {
			if c, err := r.Creators.Get(ctx, v.CreatorID); err == nil {
				handle = c.Handle
				creatorCache[v.CreatorID] = handle
			}
		}
		var posted time.Time
		if v.PostedAt.Valid {
			posted = time.Unix(v.PostedAt.Int64, 0)
		} else {
			posted = v.DownloadedAt
		}
		summary := ""
		if sm, _ := r.Enrich.GetSummary(ctx, v.ID); sm != nil {
			summary = sm.Summary
		} else if v.Description.Valid {
			summary = v.Description.String
		}
		var tagNames []string
		if tags, _ := r.Enrich.TagsForVideo(ctx, v.ID); len(tags) > 0 {
			tagNames = make([]string, len(tags))
			for j, t := range tags {
				tagNames[j] = t.Name
			}
		}
		title := v.Title.String
		if title == "" {
			title = "(untitled)"
		}
		items[i] = item{
			Title:    title,
			Creator:  handle,
			URL:      strings.TrimRight(r.BaseURL, "/") + fmt.Sprintf("/videos/%d", v.ID),
			Posted:   posted,
			Tags:     tagNames,
			Summary:  summary,
			Duration: nullableInt(v.DurationSeconds.Valid, v.DurationSeconds.Int64),
		}
	}

	data := tplData{
		GroupName: g.Name,
		Generated: time.Now(),
		BaseURL:   r.BaseURL,
		Items:     items,
	}

	var htmlBuf, textBuf bytes.Buffer
	if err := htmlTpl.Execute(&htmlBuf, data); err != nil {
		return notify.Message{}, fmt.Errorf("html render: %w", err)
	}
	if err := textTpl.Execute(&textBuf, data); err != nil {
		return notify.Message{}, fmt.Errorf("text render: %w", err)
	}

	subject := fmt.Sprintf("feedrig: %s digest — %d new", g.Name, len(items))
	if onlyUnseen && len(items) == 0 {
		subject = fmt.Sprintf("feedrig: %s digest — nothing new", g.Name)
	}

	return notify.Message{
		Subject:  subject,
		HTMLBody: htmlBuf.String(),
		TextBody: textBuf.String(),
	}, nil
}

func nullableInt(valid bool, v int64) int64 {
	if !valid {
		return 0
	}
	return v
}

// Email-friendly HTML: inline styles only; clients strip <style> blocks.
var htmlTpl = template.Must(template.New("html").Funcs(template.FuncMap{
	"humanTime": func(t time.Time) string { return t.Format("Mon Jan 2 · 3:04pm") },
	"duration": func(s int64) string {
		if s <= 0 {
			return ""
		}
		return fmt.Sprintf("%d:%02d", s/60, s%60)
	},
}).Parse(`<!doctype html>
<html><body style="font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;background:#f6f7f9;color:#202124;margin:0;padding:1.25rem;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:720px;margin:0 auto;background:#fff;border-radius:10px;border:1px solid #e6e8eb;">
  <tr><td style="padding:1.25rem 1.5rem;border-bottom:1px solid #e6e8eb;">
    <h1 style="margin:0;font-size:1.3rem;">{{.GroupName}} · digest</h1>
    <p style="margin:0.2rem 0 0;color:#5f6368;font-size:0.9rem;">{{len .Items}} item{{if ne (len .Items) 1}}s{{end}} · generated {{humanTime .Generated}}</p>
  </td></tr>
  {{range .Items}}
  <tr><td style="padding:1rem 1.5rem;border-bottom:1px solid #e6e8eb;">
    <a href="{{.URL}}" style="font-size:1.05rem;font-weight:600;color:#1967d2;text-decoration:none;">{{.Title}}</a>
    <div style="color:#5f6368;font-size:0.85rem;margin-top:0.2rem;">@{{.Creator}} · {{humanTime .Posted}}{{with duration .Duration}} · {{.}}{{end}}</div>
    {{if .Tags}}<div style="margin:0.4rem 0;">{{range .Tags}}<span style="display:inline-block;background:#eef2f6;color:#5f6368;border-radius:999px;padding:0.1rem 0.5rem;font-size:0.75rem;margin-right:0.25rem;">{{.}}</span>{{end}}</div>{{end}}
    {{with .Summary}}<p style="margin:0.4rem 0 0;line-height:1.45;">{{.}}</p>{{end}}
  </td></tr>
  {{else}}
  <tr><td style="padding:1rem 1.5rem;color:#5f6368;">Nothing new in this window.</td></tr>
  {{end}}
  <tr><td style="padding:0.75rem 1.5rem;color:#5f6368;font-size:0.8rem;">Sent by feedrig — your curated, scroll-resistant feed.</td></tr>
</table>
</body></html>`))

var textTpl = template.Must(template.New("text").Funcs(template.FuncMap{
	"humanTime": func(t time.Time) string { return t.Format("Mon Jan 2 3:04pm") },
}).Parse(`{{.GroupName}} digest — {{len .Items}} item{{if ne (len .Items) 1}}s{{end}}
Generated {{humanTime .Generated}}

{{range .Items}}— {{.Title}}
  @{{.Creator}} · {{humanTime .Posted}}
  {{.URL}}
  {{with .Summary}}{{.}}{{end}}

{{else}}Nothing new in this window.

{{end}}--
Sent by feedrig.
`))
