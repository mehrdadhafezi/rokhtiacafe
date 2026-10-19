package httpx

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// Static serves the embedded frontend build (docs/ARCHITECTURE.md §3, §4):
//   - "/" redirects to the customer menu;
//   - /menu… without a file → index.html (customer app), /admin… → admin/index.html (staff PWA);
//   - existing files are served with long-lived caching for hashed assets and no-cache for
//     HTML, service workers and the manifest, so a new release takes effect on the next load;
//   - text assets are gzip-compressed (static files only: API responses are never compressed).
type Static struct {
	fsys    fs.FS
	modTime time.Time
	mu      sync.Mutex
	gz      map[string][]byte
}

// NewStatic serves fsys; modTime is reported as Last-Modified (the build time is unknown, so the
// process start time is used and ETags are left to the hashed file names).
func NewStatic(fsys fs.FS, modTime time.Time) *Static {
	return &Static{fsys: fsys, modTime: modTime, gz: map[string][]byte{}}
}

// Ready reports whether a frontend build is embedded (both entry points present).
func (s *Static) Ready() bool {
	for _, f := range []string{"index.html", "admin/index.html"} {
		if _, err := fs.Stat(s.fsys, f); err != nil {
			return false
		}
	}
	return true
}

func init() {
	// Not in every OS mime table; the manifest must have the right type to be installable.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
}

// ServeHTTP implements http.Handler for GET and HEAD.
func (s *Static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		Error(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	p := r.URL.Path
	switch p {
	case "/":
		http.Redirect(w, r, "/menu", http.StatusFound)
		return
	case "/admin":
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		return
	}
	name := strings.TrimPrefix(path.Clean(p), "/")
	if strings.HasSuffix(p, "/") && name != "" {
		name += "/"
	}
	if s.isFile(name) {
		s.serveFile(w, r, name)
		return
	}
	// Client-side routes of the two apps. Paths that look like files (have an extension)
	// are real 404s, so a missing asset never comes back as HTML.
	if path.Ext(name) == "" {
		switch {
		case name == "menu" || strings.HasPrefix(name, "menu/"):
			s.serveFile(w, r, "index.html")
			return
		case strings.HasPrefix(name, "admin/"):
			s.serveFile(w, r, "admin/index.html")
			return
		}
	}
	Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
}

func (s *Static) isFile(name string) bool {
	if name == "" || strings.HasSuffix(name, "/") || strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
		return false // no directory listings, no dotfiles (e.g. .vite/manifest.json)
	}
	st, err := fs.Stat(s.fsys, name)
	return err == nil && !st.IsDir()
}

// cacheControl: content-hashed bundles never change; everything else must be revalidated.
func cacheControl(name string) string {
	switch {
	case strings.HasPrefix(name, "assets/"):
		return "public, max-age=31536000, immutable"
	case strings.HasSuffix(name, ".html"), strings.HasSuffix(name, "sw.js"), strings.HasSuffix(name, ".webmanifest"),
		strings.HasPrefix(name, "admin/workbox-"):
		return "no-cache"
	default:
		return "public, max-age=86400"
	}
}

func compressible(ctype string) bool {
	for _, t := range []string{"text/", "application/javascript", "application/json", "application/manifest+json", "image/svg+xml"} {
		if strings.HasPrefix(ctype, t) {
			return true
		}
	}
	return false
}

func (s *Static) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		Error(w, http.StatusInternalServerError, "INTERNAL", "internal error")
		return
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = http.DetectContentType(data)
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", cacheControl(name))
	if name == "admin/sw.js" {
		h.Set("Service-Worker-Allowed", "/admin/")
	}
	if compressible(ctype) && len(data) > 1024 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		data = s.gzipped(name, data)
	}
	http.ServeContent(w, r, "", s.modTime, bytes.NewReader(data))
}

// gzipped compresses each file once; the embedded build is immutable for the process lifetime.
func (s *Static) gzipped(name string, data []byte) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.gz[name]; ok {
		return b
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = io.Copy(zw, bytes.NewReader(data))
	_ = zw.Close()
	s.gz[name] = buf.Bytes()
	return s.gz[name]
}
