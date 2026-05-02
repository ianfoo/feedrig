package enrich

import (
	"context"

	"github.com/ianfoo/feedrig/internal/ingest"
)

// CommentSinkAdapter bridges the ingest layer's DownloadedComment shape to
// our domain Comment + the store's ReplaceComments. Defined here so
// internal/ingest doesn't need to import internal/enrich (avoids a cycle).
type CommentSinkAdapter struct {
	Store *Store
}

// Compile-time check: this satisfies ingest.CommentSink.
var _ ingest.CommentSink = (*CommentSinkAdapter)(nil)

func (a *CommentSinkAdapter) ReplaceComments(ctx context.Context, videoID int64, in []ingest.DownloadedComment) error {
	out := make([]Comment, 0, len(in))
	for _, c := range in {
		dc := Comment{Author: c.Author, Text: c.Text, Likes: c.Likes}
		if !c.PostedAt.IsZero() {
			t := c.PostedAt
			dc.PostedAt = &t
		}
		out = append(out, dc)
	}
	return a.Store.ReplaceComments(ctx, videoID, out)
}
