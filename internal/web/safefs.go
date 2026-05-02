package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// safeFileServer serves files from root, refusing any request whose cleaned
// path escapes root via "..".
func safeFileServer(root string) http.Handler {
	abs, _ := filepath.Abs(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(r.URL.Path)
		if strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(abs, clean)
		fullAbs, _ := filepath.Abs(full)
		if !strings.HasPrefix(fullAbs, abs+string(filepath.Separator)) && fullAbs != abs {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, fullAbs)
	})
}

// safeRemove deletes path if it exists; returns nil on missing.
func safeRemove(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(path)
}
