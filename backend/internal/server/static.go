package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// staticSPAHandler serves files from staticDir, falling back to index.html
// for any path that doesn't match a file on disk (the SPA fallback).
//
// API routes (anything that ServeMux already routes to a more-specific
// handler) bypass this entirely because ServeMux's longest-prefix match
// picks them first.
func staticSPAHandler(staticDir string) http.Handler {
	fs := http.Dir(staticDir)
	indexPath := filepath.Join(staticDir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Defence-in-depth: refuse to serve any Connect-style API path. The
		// mux should already route these to a service handler before reaching
		// this fallback, but a misregistered path shouldn't leak through.
		if strings.Contains(r.URL.Path, "/stillhouse.") {
			http.NotFound(w, r)
			return
		}

		clean := filepath.Clean(r.URL.Path)
		if clean == "/" || clean == "." {
			http.ServeFile(w, r, indexPath)
			return
		}
		full := filepath.Join(staticDir, clean)
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			http.FileServer(fs).ServeHTTP(w, r)
			return
		}
		// Unknown path — let the SPA's router handle it client-side.
		http.ServeFile(w, r, indexPath)
	})
}
