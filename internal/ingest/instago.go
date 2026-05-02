package ingest

import (
	"context"
	"fmt"

	"github.com/siongui/instago"
)

// InstagoDiscoverer enumerates a creator's recent post shortcodes via
// siongui/instago's no-login profile scrape. Stale upstream — expect
// breakage when Instagram changes their HTML.
type InstagoDiscoverer struct{}

func (InstagoDiscoverer) Recent(ctx context.Context, handle string) ([]string, error) {
	type result struct {
		codes []string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		codes, err := instago.GetRecentPostCodeNoLogin(handle)
		ch <- result{codes, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("instago: %w", r.err)
		}
		return r.codes, nil
	}
}
