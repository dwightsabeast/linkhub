// Package server wires HTTP routes for LinkHub. It serves the
// public profile (a single Go-templated HTML page), the admin SPA
// (vanilla JS, no framework), and a small JSON API for the admin to
// read/write config and upload an avatar.
package server

import (
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/dwightsabeast/linkhub/internal/auth"
	"github.com/dwightsabeast/linkhub/internal/config"
)

// Options carries everything needed to construct a Server. Built in
// main() from environment variables.
type Options struct {
	Store     *config.Store
	Auth      *auth.Config
	StaticDir string // /opt/linkhub/static
	DataDir   string // /var/lib/linkhub
}

// Server holds the resolved templates and handler dependencies. It
// is used as an http.Handler.
type Server struct {
	opts Options
	tpl  *template.Template
}

// New parses templates and returns a ready-to-serve Server.
func New(opts Options) (*Server, error) {
	tplPath := filepath.Join(opts.StaticDir, "index.html.tmpl")
	tpl, err := template.New("index.html.tmpl").
		Funcs(template.FuncMap{
			"iconSVG": iconSVG,
		}).
		ParseFiles(tplPath)
	if err != nil {
		return nil, err
	}
	return &Server{opts: opts, tpl: tpl}, nil
}

// Handler returns the configured router as an http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public surface (no auth).
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Static assets shipped with the release tarball. The template
	// references /static/styles.css, /static/admin.js, etc.
	staticFS := http.FileServer(http.Dir(s.opts.StaticDir))
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		cacheControl(noDirListing(staticFS), "public, max-age=3600")))

	// User-uploaded avatars and any other on-disk assets that aren't
	// part of the release tarball. Served read-only.
	assetsFS := http.FileServer(http.Dir(filepath.Join(s.opts.DataDir, "assets")))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/",
		cacheControl(noDirListing(assetsFS), "public, max-age=300")))

	// Admin surface — gated by auth middleware.
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin", s.handleAdmin)
	adminMux.HandleFunc("GET /admin/", s.handleAdmin)
	adminMux.HandleFunc("GET /api/config", s.handleGetConfig)
	adminMux.HandleFunc("PUT /api/config", s.handleSetConfig)
	adminMux.HandleFunc("POST /api/avatar", s.handleAvatar)
	adminMux.HandleFunc("POST /api/favicon", s.handleFavicon)

	mux.Handle("/admin", s.opts.Auth.Middleware(adminMux))
	mux.Handle("/admin/", s.opts.Auth.Middleware(adminMux))
	mux.Handle("/api/", s.opts.Auth.Middleware(adminMux))

	return logRequests(mux)
}

// noDirListing wraps a file server to refuse directory index pages.
// http.FileServer happily lists /assets/ otherwise.
func noDirListing(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// cacheControl sets a Cache-Control header on every response that
// passes through. We keep it short on /assets so an avatar swap
// becomes visible quickly.
func cacheControl(h http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		h.ServeHTTP(w, r)
	})
}

// logRequests is a minimal access log: method, path, status, bytes.
// Production deployments behind Cloudflare Tunnel get logs upstream
// too, but local debugging benefits from these.
func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lrw := &loggingResponseWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(lrw, r)
		log.Printf("%s %s %d %dB", r.Method, r.URL.Path, lrw.status, lrw.bytes)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (l *loggingResponseWriter) WriteHeader(s int) {
	l.status = s
	l.ResponseWriter.WriteHeader(s)
}

func (l *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := l.ResponseWriter.Write(b)
	l.bytes += n
	return n, err
}
