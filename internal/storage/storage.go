// Package storage abstracts the media-blob backing store. v0.5 ships a
// LocalFS impl that mirrors today's on-disk layout. The S3 impl is a stub
// — when we want hosted deployments, fill in the methods and the rest of
// the system stays unchanged because everywhere that touches files goes
// through this interface.
package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Blob is the small surface needed by ingest, the player, and the TTL
// sweeper. Keys are forward-slash paths relative to the backend's root
// (e.g. "natgeo/SHORTCODE.mp4"); they are NOT absolute filesystem paths.
type Blob interface {
	// Put stores content at key. Existing data at key is replaced.
	Put(ctx context.Context, key string, r io.Reader) error
	// Open returns a reader for the blob at key.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Remove deletes the blob at key. Missing keys are not an error.
	Remove(ctx context.Context, key string) error
	// PublicURL returns a URL the user's browser can use directly. May
	// be a relative URL served by us, or a presigned S3 URL, etc.
	PublicURL(key string) string
}

// KeyFor turns an absolute file path under root (which may include trailing
// separators or symlinks) into a forward-slash blob key. Used at the seam
// where the database still holds absolute paths produced by yt-dlp.
//
// Returns "" if path is empty or doesn't sit under root — the caller should
// fall back to its own URL generation in that case.
func KeyFor(root, path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// LocalFS stores blobs under Root on the local filesystem. Keys are joined
// to Root and confined via filepath.Clean to avoid traversal.
type LocalFS struct {
	Root      string
	URLPrefix string // e.g. "/media", what the HTTP server already serves
}

func (l LocalFS) abs(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, string(filepath.Separator)+"..") {
		return "", errors.New("invalid key")
	}
	return filepath.Join(l.Root, clean), nil
}

func (l LocalFS) Put(_ context.Context, key string, r io.Reader) error {
	p, err := l.abs(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (l LocalFS) Open(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.abs(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (l LocalFS) Remove(_ context.Context, key string) error {
	p, err := l.abs(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l LocalFS) PublicURL(key string) string {
	prefix := l.URLPrefix
	if prefix == "" {
		prefix = "/media"
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(filepath.ToSlash(key), "/")
}

// S3 is a stub. Wire in github.com/aws/aws-sdk-go-v2 (or any
// S3-compatible client) when we want hosted storage.
type S3 struct {
	Bucket   string
	Region   string
	Endpoint string // optional, for non-AWS S3-compatible services
}

func (S3) Put(context.Context, string, io.Reader) error           { return ErrNotConfigured }
func (S3) Open(context.Context, string) (io.ReadCloser, error)    { return nil, ErrNotConfigured }
func (S3) Remove(context.Context, string) error                   { return ErrNotConfigured }
func (S3) PublicURL(string) string                                { return "" }

var ErrNotConfigured = errors.New("S3 storage not configured (stub)")
