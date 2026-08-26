package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dwightsabeast/linkhub/internal/config"
)

// Limits applied to incoming requests. Avatar is the only meaningful
// upload; everything else is a few KB of JSON.
const (
	maxConfigBytes  = 32 << 10  // 32 KB
	maxAvatarBytes  = 2 << 20   // 2 MB
	maxFaviconBytes = 512 << 10 // 512 KB — favicons should be small
)

// indexData is the template payload. We pass cfg by value (the Store
// already deep-copies on Get) plus a few derived fields.
type indexData struct {
	config.Config
	Year              int
	IconPath          template.JS
	RawHeadSnippet    template.HTML
	BannerDurationSec int

	// UseWebFonts reports whether this response may link out to Google
	// Fonts. False when the operator chose system fonts or when this
	// visitor has opted out; the CSS font stacks fall back on their own.
	UseWebFonts bool
	// FontsURL is the Google Fonts stylesheet URL. Only meaningful when
	// UseWebFonts is true.
	FontsURL template.URL

	// PrivacyLinkLabel is the footer link text. California wants the
	// specific phrase "Your Privacy Choices" (with the standard icon)
	// on sites that sell or share personal information; a site that
	// only measures gets a plain "Privacy" link to the same notice.
	PrivacyLinkLabel string
	// ShowOptOutIcon pairs the standardized opt-out toggle glyph with
	// the link. Only used in the sell/share case.
	ShowOptOutIcon bool
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Store.Get()

	// Everything below this line depends on the visitor's opt-out
	// state, so the response must not be cached across visitors with
	// different signals.
	markPrivacyVary(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	out := optedOut(r)

	// The head snippet is withheld from an opted-out visitor whenever
	// it's classified as analytics or advertising. An "essential"
	// snippet always renders — suppressing it would break the site,
	// and strictly necessary processing needs no opt-out.
	snippet := cfg.Meta.HeadSnippet
	if out && cfg.SnippetIsSuppressible() {
		snippet = ""
	}

	// Google Fonts is a third-party request that discloses the
	// visitor's IP address and User-Agent before they've read a word
	// of the notice. An opt-out signal takes it off the page entirely.
	useWebFonts := cfg.Privacy.FontSource != config.FontSourceSystem && !out

	label := "Privacy"
	showIcon := false
	if cfg.SellsOrShares() {
		label = "Your Privacy Choices"
		showIcon = true
	}

	// Buffer the render so a template error doesn't leave a half-
	// written body on the wire.
	data := indexData{
		Config:            cfg,
		Year:              time.Now().Year(),
		RawHeadSnippet:    template.HTML(snippet),
		BannerDurationSec: bannerDurationSec(cfg.Banner.Speed),
		UseWebFonts:       useWebFonts,
		FontsURL: googleFontsURL(
			cfg.Theme.FontDisplay, cfg.Theme.FontBody, cfg.Theme.FontMono),
		PrivacyLinkLabel: label,
		ShowOptOutIcon:   showIcon,
	}
	var buf bytes.Buffer
	if err := s.tpl.Execute(&buf, data); err != nil {
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

// handleSession reports the active auth mode to the admin UI. The page
// reaches this only after passing the auth middleware, so a 200 here
// already implies an authenticated admin; the body just tells admin.js
// whether to surface the logout button (form mode) or hide it (every
// other mode, where there's no session to end).
func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request) {
	mode := s.opts.Auth.ModeString()
	writeJSON(w, http.StatusOK, map[string]any{
		"authMode":  mode,
		"canLogout": mode == "form",
	})
}

// maxUsernameRunes caps the admin username. Generous — it's a label,
// not a security boundary.
const maxUsernameRunes = 64

// minPasswordBytes is the floor for an admin password set via the UI.
// bcrypt's ceiling (72 bytes) is enforced in auth.hashPassword.
const minPasswordBytes = 8

// handleGetAccount returns the current admin username (never the hash)
// so the Account card can display it. Form mode only — registered
// behind the admin auth middleware.
func (s *Server) handleGetAccount(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"username": s.opts.Auth.CurrentUsername(),
	})
}

// accountUpdate is the PUT /api/account body. All fields optional
// except currentPassword; an empty newPassword means "username only".
type accountUpdate struct {
	CurrentPassword string `json:"currentPassword"`
	Username        string `json:"username"`
	NewPassword     string `json:"newPassword"`
}

// handleSetAccount changes the admin username and/or password. It
// requires the current password, validates the inputs, persists via
// the auth credential store, and — on a password change — rotates all
// sessions (logging out other devices) and re-issues the cookie for
// the current admin so they aren't kicked out.
func (s *Server) handleSetAccount(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var in accountUpdate
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}

	// Confirm the current password before allowing any change.
	if !s.opts.Auth.VerifyPassword(in.CurrentPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "current password is incorrect",
		})
		return
	}

	// Resolve the target username: keep the current one if not supplied.
	username := strings.TrimSpace(in.Username)
	if username == "" {
		username = s.opts.Auth.CurrentUsername()
	}
	if n := utf8.RuneCountInString(username); n < 1 || n > maxUsernameRunes {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "username must be 1–64 characters",
		})
		return
	}
	if hasControlRunes(username) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "username contains invalid characters",
		})
		return
	}

	changingPassword := in.NewPassword != ""
	if changingPassword {
		if len(in.NewPassword) < minPasswordBytes {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "new password must be at least 8 characters",
			})
			return
		}
		if len(in.NewPassword) > 72 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "new password is too long (max 72 bytes)",
			})
			return
		}
	}

	if err := s.opts.Auth.UpdateCredentials(username, in.NewPassword); err != nil {
		log.Printf("account update: %v", err)
		http.Error(w, "could not save credentials", http.StatusInternalServerError)
		return
	}

	// A password change revokes every existing session. Re-issue one for
	// the admin making the change so this tab stays signed in.
	if changingPassword {
		token, err := s.opts.Auth.RotateSessions()
		if err != nil {
			log.Printf("account rotate sessions: %v", err)
			// The credential is already saved; report success but the
			// admin may need to log in again.
			writeJSON(w, http.StatusOK, map[string]string{
				"status":   "ok",
				"username": username,
				"note":     "password changed; please sign in again",
			})
			return
		}
		s.opts.Auth.SetSessionCookie(w, r, token)
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"username": username,
	})
}

// hasControlRunes reports whether s contains any control characters.
func hasControlRunes(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
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

// allowedFaviconTypes mirrors allowedAvatarTypes but also permits
// ICO, the traditional favicon format.
var allowedFaviconTypes = map[string]string{
	"image/png":                ".png",
	"image/svg+xml":            ".svg",
	"image/x-icon":             ".ico",
	"image/vnd.microsoft.icon": ".ico",
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

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFaviconBytes)

	ctype := r.Header.Get("Content-Type")
	ext, ok := allowedFaviconTypes[ctype]
	if !ok {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{
			"error": "Content-Type must be one of: image/png, image/svg+xml, image/x-icon",
		})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "favicon too large (max 512 KB)",
		})
		return
	}
	if !sniffFaviconMatches(ctype, body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "file contents do not match the declared image type",
		})
		return
	}

	assetsDir := filepath.Join(s.opts.DataDir, "assets")
	tmpFile, err := os.CreateTemp(assetsDir, ".favicon-*"+ext)
	if err != nil {
		log.Printf("favicon tempfile: %v", err)
		http.Error(w, "could not save favicon", http.StatusInternalServerError)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(body); err != nil {
		tmpFile.Close()
		http.Error(w, "could not save favicon", http.StatusInternalServerError)
		return
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		http.Error(w, "could not save favicon", http.StatusInternalServerError)
		return
	}
	tmpFile.Close()
	_ = os.Chmod(tmpPath, 0o644)

	finalName := "favicon" + ext
	finalPath := filepath.Join(assetsDir, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		log.Printf("favicon rename: %v", err)
		http.Error(w, "could not save favicon", http.StatusInternalServerError)
		return
	}

	// Remove stale favicons in other formats.
	for _, e := range []string{".png", ".svg", ".ico"} {
		if e == ext {
			continue
		}
		_ = os.Remove(filepath.Join(assetsDir, "favicon"+e))
	}

	faviconRef := "/assets/" + finalName + "?v=" + strconv.FormatInt(time.Now().Unix(), 10)
	if err := s.opts.Store.SetFavicon(faviconRef); err != nil {
		log.Printf("set favicon ref: %v", err)
		http.Error(w, "saved but could not update config", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"favicon": faviconRef,
	})
}

// sniffFaviconMatches validates magic bytes for favicon uploads.
func sniffFaviconMatches(ctype string, b []byte) bool {
	if len(b) < 4 {
		return false
	}
	switch ctype {
	case "image/png":
		return len(b) >= 8 && bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "image/svg+xml":
		head := b
		if len(head) > 1024 {
			head = head[:1024]
		}
		return bytes.Contains(bytes.ToLower(head), []byte("<svg"))
	case "image/x-icon", "image/vnd.microsoft.icon":
		// ICO magic: 00 00 01 00 (icon) or 00 00 02 00 (cursor)
		return b[0] == 0 && b[1] == 0 && (b[2] == 1 || b[2] == 2) && b[3] == 0
	}
	return false
}

// bannerDurationSec maps the 1–10 speed slider to a CSS marquee
// duration in seconds: 1 (slow) → 40s, 10 (fast) → 4s. The public
// template exposes this as the --banner-duration custom property.
func bannerDurationSec(speed int) int {
	if speed < 1 {
		speed = 1
	}
	if speed > 10 {
		speed = 10
	}
	return 44 - speed*4
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
		log.Printf("writeJSON: %v", err)
	}
}
