// Package webui serves the browser application built from web/ into static/app.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/sudatti624/AmuOS/internal/security"
)

//go:embed all:static
var files embed.FS

// Pages are the client-side routes that return the application shell. Other
// paths keep the server's audited 404 handling.
var Pages = []string{"/", "/setup", "/login", "/invite", "/hosts", "/activity", "/users", "/settings"}

// noncePlaceholder is written into index.html by the web build (html.cspNonce).
const noncePlaceholder = "AMUOS_CSP_NONCE"

// CSP allows only same-origin scripts, styles and API calls. Dialog components
// insert <style> elements, which are allowed only with this response's nonce.
func CSP(nonce string) string {
	style := "style-src 'self'"
	if nonce != "" {
		style += " 'nonce-" + nonce + "'"
	}
	return "default-src 'none'; script-src 'self'; " + style + "; img-src 'self' data:; font-src 'self'; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}

var contentTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".woff2": "font/woff2",
	".json":  "application/json",
}

func app() fs.FS {
	sub, err := fs.Sub(files, "static/app")
	if err != nil {
		return nil
	}
	return sub
}

// Built reports whether the binary contains a built application.
func Built() bool {
	_, err := fs.Stat(app(), "index.html")
	return err == nil
}

// Shell returns index.html for client-side routes.
func Shell(w http.ResponseWriter, _ *http.Request) {
	index, err := fs.ReadFile(app(), "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("The web UI is not included in this build. Build web/ before compiling the control plane.\n"))
		return
	}
	nonce := security.Token()
	w.Header().Set("Content-Security-Policy", CSP(nonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(bytes.ReplaceAll(index, []byte(noncePlaceholder), []byte(nonce)))
}

// Asset serves hashed build files under /assets/ and top-level icons.
// notFound keeps missing files on the audited rejection path.
func Asset(notFound http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		contentType, known := contentTypes[path.Ext(name)]
		if !known || !fs.ValidPath(name) || strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
			notFound(w, r)
			return
		}
		data, err := fs.ReadFile(app(), name)
		if err != nil {
			notFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", CSP(""))
		w.Header().Set("Content-Type", contentType)
		if strings.HasPrefix(name, "assets/") {
			// Build file names contain content hashes.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		_, _ = w.Write(data)
	}
}
