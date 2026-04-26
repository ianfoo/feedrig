package ingest

import (
	"context"
	"errors"
)

// NoopDiscoverer always returns ErrNotConfigured. Useful when the user
// wants to disable scheduled polling and rely entirely on manual URL paste.
type NoopDiscoverer struct{}

func (NoopDiscoverer) Recent(_ context.Context, _ string) ([]string, error) {
	return nil, ErrNotConfigured
}

var ErrNotConfigured = errors.New("discovery disabled")
