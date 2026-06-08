// Package auth provides four middleware modes for protecting the
// admin surface: "trust_proxy" (no-op, the upstream proxy is
// responsible), "basic" (HTTP Basic with a bcrypt hash), "form" (a
// styled login page backed by an in-memory session cookie), and
// "none" (open, with a logged warning at startup).
package auth

import (
	"crypto/subtle"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Mode is the operating mode parsed from AUTH_MODE.
type Mode int

const (
	ModeTrustProxy Mode = iota
	ModeBasic
	ModeForm
	ModeNone
)

// sessionCookieName is the cookie that carries the form-auth session
// token. HttpOnly + SameSite=Lax; see setSessionCookie.
const sessionCookieName = "linkhub_session"

//go:embed login.html
var loginHTML string

// loginTmpl is the parsed sign-in page, rendered for GET /login and
// for failed POST /login. Parsed once at package init; a parse error
// here is a build-time bug in login.html, so panic is appropriate.
var loginTmpl = template.Must(template.New("login").Parse(loginHTML))

// ParseMode turns the env-var string into a Mode. Returns an error
// for anything unrecognized so the install script's spelling is held
// to account.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "trust_proxy":
		return ModeTrustProxy, nil
	case "basic":
		return ModeBasic, nil
	case "form":
		return ModeForm, nil
	case "none":
		return ModeNone, nil
	default:
		return 0, fmt.Errorf("unknown AUTH_MODE %q (want trust_proxy, basic, form, or none)", s)
	}
}

// bcryptCost is the work factor for hashes we generate in-app (the
// account-settings password change). Matches cmd/linkhub-hash. 72 is
// bcrypt's hard input limit; we refuse longer rather than silently
// truncate.
const (
	bcryptCost     = 12
	bcryptMaxBytes = 72
)

// Config holds the resolved auth settings. Build via NewConfig.
type Config struct {
	Mode Mode
	user string // env BASIC_AUTH_USER — bootstrap fallback for ModeForm
	hash []byte // env BASIC_AUTH_HASH — bootstrap fallback for ModeForm

	// sessions is the live session set for ModeForm. nil in every
	// other mode — guard accesses on Mode == ModeForm.
	sessions *sessionStore

	// credentials is the data-dir credential store for ModeForm. Once
	// the admin changes the login from the UI it is written here and
	// takes precedence over the env user/hash. nil outside ModeForm.
	credentials *credStore
}

// NewConfig validates inputs and returns an auth Config.
//
// user/hash come from BASIC_AUTH_USER / BASIC_AUTH_HASH. For ModeBasic
// they are required (env is the only credential source). For ModeForm
// they are an optional *bootstrap*: the persisted credential at
// credPath (auth.json in the data dir) takes precedence if present, so
// an install that only ever set the password in-app needs no env
// credential. credPath is ignored outside ModeForm.
func NewConfig(mode Mode, user, hash, credPath string) (*Config, error) {
	c := &Config{Mode: mode}

	// Validate env creds when they're the authoritative source (basic)
	// or when they were supplied as a form-mode bootstrap.
	envProvided := user != "" || hash != ""
	if mode == ModeBasic || (mode == ModeForm && envProvided) {
		if user == "" {
			return nil, errors.New("BASIC_AUTH_USER is empty")
		}
		if hash == "" {
			return nil, errors.New("BASIC_AUTH_HASH is empty")
		}
		// Sniff for the bcrypt prefix so we fail loudly at boot rather
		// than at first login attempt.
		if !looksLikeBcrypt(hash) {
			return nil, errors.New("BASIC_AUTH_HASH does not look like a bcrypt hash")
		}
		c.user = user
		c.hash = []byte(hash)
	}

	if mode == ModeForm {
		c.sessions = newSessionStore(defaultSessionTTL)
		store, err := newCredStore(credPath)
		if err != nil {
			return nil, err
		}
		c.credentials = store
	}
	return c, nil
}

// looksLikeBcrypt reports whether s has a recognized bcrypt prefix.
func looksLikeBcrypt(s string) bool {
	return strings.HasPrefix(s, "$2a$") ||
		strings.HasPrefix(s, "$2b$") ||
		strings.HasPrefix(s, "$2y$")
}

// Middleware wraps next with the configured auth check. On unauth it
// writes 401 (basic) or just calls through (trust_proxy / none).
func (c *Config) Middleware(next http.Handler) http.Handler {
	switch c.Mode {
	case ModeTrustProxy, ModeNone:
		// trust_proxy: the request reaching us has been gated
		// upstream. We literally have no way to verify this — that's
		// the deal. none: explicit user opt-in to no auth.
		return next
	case ModeBasic:
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || !c.verify(user, pass) {
				c.challenge(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	case ModeForm:
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail closed if form auth has no credential at all (no
			// auth.json and no BASIC_AUTH_*). This is a misconfiguration
			// — never silently let the admin through.
			if _, _, ok := c.currentCreds(); !ok {
				http.Error(w, "admin auth is not configured", http.StatusInternalServerError)
				return
			}
			if cookie, err := r.Cookie(sessionCookieName); err == nil &&
				c.sessions.valid(cookie.Value) {
				next.ServeHTTP(w, r)
				return
			}
			// No valid session. A browser navigating to /admin gets
			// bounced to the login page (preserving where it was
			// headed); an XHR to /api/* gets a clean 401 so admin.js
			// can react without trying to render an HTML login page
			// into a fetch().
			if isAPIRequest(r) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			redirectToLogin(w, r)
		})
	}
	// Defensive: should never happen, but if Mode is somehow invalid
	// we deny everything rather than fail open.
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "auth misconfigured", http.StatusInternalServerError)
	})
}

func (c *Config) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="LinkHub admin", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

// ── credential resolution ────────────────────────────────────────

// currentCreds returns the authoritative username + bcrypt hash and
// whether any credential is configured. The data-dir store wins; the
// env bootstrap is the fallback.
func (c *Config) currentCreds() (user, hash string, ok bool) {
	if c.credentials != nil {
		if cr, found := c.credentials.get(); found {
			return cr.Username, cr.Hash, true
		}
	}
	if c.user != "" && len(c.hash) > 0 {
		return c.user, string(c.hash), true
	}
	return "", "", false
}

// verify checks a username + password against the current credential
// in constant time (for the username) plus bcrypt (for the password).
func (c *Config) verify(user, pass string) bool {
	wantUser, wantHash, ok := c.currentCreds()
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) == 1
	passOK := bcrypt.CompareHashAndPassword([]byte(wantHash), []byte(pass)) == nil
	return userOK && passOK
}

// VerifyPassword checks pass against the current password only (no
// username). Used to confirm the current password before an
// account change.
func (c *Config) VerifyPassword(pass string) bool {
	_, wantHash, ok := c.currentCreds()
	if !ok {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(wantHash), []byte(pass)) == nil
}

// CurrentUsername returns the configured admin username, or "" if none.
func (c *Config) CurrentUsername() string {
	u, _, _ := c.currentCreds()
	return u
}

// UpdateCredentials persists a new username and (optionally) password
// to the data-dir store. An empty newPassword keeps the current hash —
// useful for a username-only change. Only valid in ModeForm.
func (c *Config) UpdateCredentials(username, newPassword string) error {
	if c.credentials == nil {
		return errors.New("credential store is unavailable")
	}
	if username == "" {
		return errors.New("username is empty")
	}
	_, curHash, _ := c.currentCreds()
	hash := curHash
	if newPassword != "" {
		h, err := hashPassword(newPassword)
		if err != nil {
			return err
		}
		hash = h
	}
	if hash == "" {
		return errors.New("no password set")
	}
	return c.credentials.save(username, hash)
}

// RotateSessions invalidates every live session and mints a fresh one,
// returning its token. Called after a password change so other devices
// are logged out while the active admin keeps a valid (re-issued)
// session. Only valid in ModeForm.
func (c *Config) RotateSessions() (string, error) {
	if c.sessions == nil {
		return "", errors.New("no session store")
	}
	c.sessions.destroyAll()
	return c.sessions.create()
}

// SetSessionCookie writes the session cookie for token onto w. Thin
// exported wrapper so the server can set the cookie after rotating
// sessions without duplicating cookie attributes.
func (c *Config) SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	setSessionCookie(w, r, token, int(defaultSessionTTL.Seconds()))
}

// hashPassword bcrypt-hashes pw at our standard cost, refusing empty or
// over-limit inputs (mirrors cmd/linkhub-hash).
func hashPassword(pw string) (string, error) {
	if len(pw) == 0 {
		return "", errors.New("password is empty")
	}
	if len(pw) > bcryptMaxBytes {
		return "", fmt.Errorf("password is %d bytes; bcrypt's limit is %d", len(pw), bcryptMaxBytes)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// LoginHandler serves the form-auth sign-in page (GET) and processes a
// submitted credential (POST). It is only meaningful in ModeForm; in
// every other mode there is no login page, so we send the caller to
// /admin and let that mode's middleware (or lack of one) take over.
//
// Register it on the *public* mux — it must be reachable without a
// session, which is the whole point.
func (c *Config) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if c.Mode != ModeForm {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Already signed in? Skip the form.
		if cookie, err := r.Cookie(sessionCookieName); err == nil &&
			c.sessions.valid(cookie.Value) {
			http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
			return
		}
		c.renderLogin(w, "", safeNext(r.URL.Query().Get("next")))

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			c.renderLogin(w, "Malformed form submission.", "/admin")
			return
		}
		user := r.PostForm.Get("username")
		pass := r.PostForm.Get("password")
		next := safeNext(r.PostForm.Get("next"))

		// One generic error for either a bad username or bad password so
		// we don't disclose which field was wrong.
		if !c.verify(user, pass) {
			w.WriteHeader(http.StatusUnauthorized)
			c.renderLogin(w, "Incorrect username or password.", next)
			return
		}

		token, err := c.sessions.create()
		if err != nil {
			log.Printf("auth: session create: %v", err)
			http.Error(w, "could not start session", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, r, token, int(defaultSessionTTL.Seconds()))
		http.Redirect(w, r, next, http.StatusSeeOther)

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// LogoutHandler destroys the current session (if any) and clears the
// cookie, then sends the browser to the login page. A no-op cookie
// or missing session is fine — logout is idempotent. Outside ModeForm
// there's nothing to log out of, so we just bounce to /admin.
func (c *Config) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c.Mode != ModeForm {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		c.sessions.destroy(cookie.Value)
	}
	clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// renderLogin writes the sign-in page with an optional error banner.
// errMsg is empty on a fresh GET and non-empty after a failed POST.
func (c *Config) renderLogin(w http.ResponseWriter, errMsg, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	data := struct {
		Error string
		Next  string
	}{Error: errMsg, Next: next}
	if err := loginTmpl.Execute(w, data); err != nil {
		log.Printf("auth: login render: %v", err)
	}
}

// ModeString returns the canonical AUTH_MODE token for the configured
// mode. Used by the admin API so the UI can show a logout button only
// when form auth is active.
func (c *Config) ModeString() string {
	switch c.Mode {
	case ModeTrustProxy:
		return "trust_proxy"
	case ModeBasic:
		return "basic"
	case ModeForm:
		return "form"
	case ModeNone:
		return "none"
	default:
		return "unknown"
	}
}

// LogStartup announces the chosen mode at boot. Useful in journalctl
// when debugging "why is /admin not asking me for a password".
func (c *Config) LogStartup() {
	switch c.Mode {
	case ModeTrustProxy:
		log.Printf("auth: trust_proxy — admin is unauthenticated at the binary; ensure your reverse proxy gates /admin")
	case ModeBasic:
		log.Printf("auth: basic — admin requires HTTP Basic Auth (user=%q)", c.user)
	case ModeForm:
		if user, _, ok := c.currentCreds(); ok {
			src := "BASIC_AUTH_* env"
			if c.credentials != nil && c.credentials.configured() {
				src = "auth.json"
			}
			log.Printf("auth: form — admin requires login at /login (user=%q via %s, session TTL %s)", user, src, defaultSessionTTL)
		} else {
			log.Printf("auth: form — WARNING: no credential configured (no auth.json, no BASIC_AUTH_*); admin is locked until one is set")
		}
	case ModeNone:
		log.Printf("auth: none — admin is open. Do not expose this LXC publicly without an upstream gate.")
	}
}

// ── form-mode helpers ────────────────────────────────────────────

// isAPIRequest distinguishes an admin XHR (/api/*) from a browser
// navigation to /admin. Used to choose 401 vs. redirect on unauth.
func isAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}

// redirectToLogin sends an unauthenticated navigation to /login,
// stashing the originally-requested path in ?next= so a successful
// login lands back where the user was headed.
func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Path)
	http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
}

// safeNext sanitizes a post-login redirect target. We only ever
// redirect to a local admin path, never to an attacker-supplied
// absolute URL (open-redirect defense). Anything that isn't a clean
// in-app /admin… path collapses to /admin.
func safeNext(next string) string {
	if next == "" {
		return "/admin"
	}
	// Reject absolute URLs, protocol-relative URLs, and anything not
	// rooted at our admin surface.
	if !strings.HasPrefix(next, "/") ||
		strings.HasPrefix(next, "//") ||
		!strings.HasPrefix(next, "/admin") {
		return "/admin"
	}
	return next
}

// setSessionCookie writes the session cookie. HttpOnly keeps it away
// from JS; SameSite=Lax blocks it on cross-site POSTs (CSRF defense
// for the cookie-authenticated /api writes); Secure is set when the
// request arrived over TLS so local http dev still works.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

// clearSessionCookie expires the session cookie on the client.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

// isHTTPS reports whether the original client connection was TLS,
// trusting X-Forwarded-Proto since LinkHub is designed to run behind a
// reverse proxy / tunnel that terminates TLS upstream.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
