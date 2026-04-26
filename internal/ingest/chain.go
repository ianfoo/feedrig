package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ChainDiscoverer tries each underlying Discoverer in order and returns the
// first successful (non-empty, non-error) result. Empty results count as
// success — they may genuinely mean the creator has no posts.
type ChainDiscoverer struct {
	Steps []NamedDiscoverer
	Log   *slog.Logger
}

type NamedDiscoverer struct {
	Name string
	Disc Discoverer
}

func (c ChainDiscoverer) Recent(ctx context.Context, handle string) ([]string, error) {
	if len(c.Steps) == 0 {
		return nil, errors.New("no discoverers configured")
	}
	var errs []string
	for _, s := range c.Steps {
		codes, err := s.Disc.Recent(ctx, handle)
		if err == nil {
			if c.Log != nil {
				c.Log.Info("discovery succeeded", "discoverer", s.Name, "handle", handle, "count", len(codes))
			}
			return codes, nil
		}
		if c.Log != nil {
			c.Log.Warn("discovery failed", "discoverer", s.Name, "handle", handle, "err", err)
		}
		errs = append(errs, fmt.Sprintf("%s: %v", s.Name, err))
	}
	return nil, fmt.Errorf("all discoverers failed: %s", joinSemi(errs))
}

func joinSemi(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}
