package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yourhandle/linkhub/internal/config"
)

// Limits applied to incoming requests. Avatar is the only meaningful
// upload; everything else is a few KB of JSON.
const (
	maxConfigBytes = 32 << 10 // 32 KB
	maxAvatarBytes = 2 << 20  // 2 MB
)

// indexData is the template payload. We pass cfg by value (the Store
// already deep-copies on Get) plus a few derived fields.
type indexData struct {
	Cfg      config.Config
	Year     int
	IconPath template.JS // not used today; reserved for inline-SVG path
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Store.Get()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Buffer the render so a template error doesn't leave a half-
	// written body on the wire.
	var buf bytes.Buffer
	if err := s.tpl.Execute(&buf, indexData{
		Cfg:  cfg,
		Year: time.Now().Year(),
	}); err != nil {
		log.Printf("index render: %v", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	_, _ = io.Copy(w, &buf)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// handleAdmin serves the admin SPA shell. The page itself is a single
// HTML file plus admin.js / admin.css from /static; we just read it
// off disk and stream it. We don't template into it — the admin
// fetches /api/config on load.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.opts.StaticDir, "admin.html")
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "admin.html missing", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, f)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.opts.Store.Get()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigBytes)
	cfg, err := config.DecodeStrict(r.Body)
	if err != nil {
		var verr *config.ValidationError
		if errors.As(err, &verr) {
			writeJSON(w, http.StatusBadRequest, verr)
			return
		}
		http.Error(w, "request too large or malformed", http.StatusBadRequest)
		return
	}
	if err := s.opts.Store.Save(cfg); err != nil {
		var verr *config.ValidationError
		if errors.As(err, &verr) {
			writeJSON(w, http.StatusBadRequest, verr)
			return
		}
		log.Printf("save config: %v", err)
		http.Error(w, "could not save config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// allowedAvatarTypes maps Content-Type to the file extension we'll
// write. Magic-byte sniffing in handleAvatar must agree with this.
var allowedAvatarTypes = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
}

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes)

	// We accept the upload as raw body with Content-Type set, not
	// multipart. The admin sends a fetch() with the file as body.
	// Less code, smaller request, no boundary parsing.
	ctype := r.Header.Get("Content-Type")
	ext, ok := allowedAvatarTypes[ctype]
	if !ok {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{
			"error": "Content-Type must be one of: image/png, image/jpeg, image/webp, image/svg+xml",
		})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "avatar too large (max 2 MB)",
		})
		return
	}
	if !sniffMatches(ctype, body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "file contents do not match the declared image type",
		})
		return
	}

	// Write atomically into the data dir's assets folder. We name
	// avatar.<ext> so the public template's reference stays stable
	// per format; we also clean up any other-format avatars left
	// behind from a prior upload.
	assetsDir := filepath.Join(s.opts.DataDir, "assets")
	tmpFile, err := os.CreateTemp(assetsDir, ".avatar-*"+ext)
	if err != nil {
		log.Printf("avatar tempfile: %v", err)
		http.Error(w, "could not save avatar", http.StatusInternalServerError)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(body); err != nil {
		tmpFile.Close()
		http.Error(w, "could not save avatar", http.StatusInternalServerError)
		return
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		http.Error(w, "could not save avatar", http.StatusInternalServerError)
		return
	}
	tmpFile.Close()
	_ = os.Chmod(tmpPath, 0o644)

	finalName := "avatar" + ext
	finalPath := filepath.Join(assetsDir, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		log.Printf("avatar rename: %v", err)
		http.Error(w, "could not save avatar", http.StatusInternalServerError)
		return
	}

	// Remove stale avatars in other formats so /assets/ doesn't
	// accumulate them. Only avatar.{png,jpg,webp,svg} are managed.
	for _, e := range []string{".png", ".jpg", ".webp", ".svg"} {
		if e == ext {
			continue
		}
		_ = os.Remove(filepath.Join(assetsDir, "avatar"+e))
	}

	// Cache-bust by appending ?v=<unix> to the avatar reference in
	// config. The browser will see a fresh URL on next render.
	avatarRef := "/assets/" + finalName + "?v=" + strconv.FormatInt(time.Now().Unix(), 10)
	if err := s.opts.Store.SetAvatar(avatarRef); err != nil {
		log.Printf("set avatar ref: %v", err)
		http.Error(w, "saved but could not update config", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"avatar": avatarRef,
	})
}

// sniffMatches checks magic bytes against the declared Content-Type.
// SVG is a textual format so we look for "<svg" or "<?xml" rather
// than a binary signature.
func sniffMatches(ctype string, b []byte) bool {
	if len(b) < 8 {
		return false
	}
	switch ctype {
	case "image/png":
		return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return bytes.HasPrefix(b, []byte("\xff\xd8\xff"))
	case "image/webp":
		// "RIFF????WEBP"
		return bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
	case "image/svg+xml":
		// SVG can start with BOM, whitespace, <?xml, <!DOCTYPE, or <svg.
		// We check the first 1 KB lowercased for "<svg".
		head := b
		if len(head) > 1024 {
			head = head[:1024]
		}
		lower := bytes.ToLower(head)
		return bytes.Contains(lower, []byte("<svg"))
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Already committed status; just log.
		log.Printf("encode response: %v", err)
	}
}

// iconSVG is a template helper: takes an icon name, returns the inner
// SVG path string verbatim. Names not in the set return the "link"
// fallback to match Icon.jsx behavior.
//
// The template wraps the result in <svg>…</svg> so the helper only
// needs to emit the inner contents. Returning template.HTML marks it
// safe to interpolate without escaping.
func iconSVG(name string) template.HTML {
	if path, ok := iconPaths[name]; ok {
		return template.HTML(path)
	}
	return template.HTML(iconPaths["link"])
}

// fileSize returns a human-readable size, used in error messages. We
// keep it because it's tiny and it makes admin error UX nicer.
func fileSize(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	if n < k*k {
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
}
