package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed static
var staticFiles embed.FS

const contentSecurityPolicy = "default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

var pageAliases = map[string]string{
	"":    "index.html",
	"doc": "doc.html",
}

type pageHandler struct{}

func (h *pageHandler) redirectToRoot(w http.ResponseWriter, r *http.Request) {
	if !allowPageMethod(w, r) {
		return
	}
	target := "/ui/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func (h *pageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !allowPageMethod(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	if alias, ok := pageAliases[name]; ok {
		name = alias
	}
	h.serveFile(w, r, name)
}

// serveFile bypasses http.FileServer so directories are never listed and
// index.html is never redirected away from its explicit path.
func (h *pageHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	content, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	setPageHeaders(w.Header())
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
}

func allowPageMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

func setPageHeaders(header http.Header) {
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cache-Control", "no-cache")
}
