// Package auth provides three middleware modes for protecting the
// admin surface: "trust_proxy" (no-op, the upstream proxy is
// responsible), "basic" (HTTP Basic with a bcrypt hash), and "none"
// (open, with a logged warning at startup).
package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Mode is the operating mode parsed from AUTH_MODE.
type Mode int

const (
	ModeTrustProxy Mode = iota
	ModeBasic
	ModeNone
)

// ParseMode turns the env-var string into a Mode. Returns an error
// for anything unrecognized so the install script's spelling is held
// to account.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "trust_proxy":
		return ModeTrustProxy, nil
	case "basic":
		return ModeBasic, nil
	case "none":
		return ModeNone, nil
	default:
		return 0, fmt.Errorf("unknown AUTH_MODE %q (want trust_proxy, basic, or none)", s)
	}
}

// Config holds the resolved auth settings. Build via NewConfig.
type Config struct {
	Mode Mode
	user string
	hash []byte
}

// NewConfig validates inputs and returns an auth Config. user/hash
// are only consulted when Mode == ModeBasic; for the other modes they
// are ignored (and may be empty).
func NewConfig(mode Mode, user, hash string) (*Config, error) {
	c := &Config{Mode: mode}
	if mode == ModeBasic {
		if user == "" {
			return nil, errors.New("BASIC_AUTH_USER is empty")
		}
		if hash == "" {
			return nil, errors.New("BASIC_AUTH_HASH is empty")
		}
		// Sniff for the bcrypt prefix so we fail loudly at boot rather
		// than at first login attempt.
		if !strings.HasPrefix(hash, "$2a$") &&
			!strings.HasPrefix(hash, "$2b$") &&
			!strings.HasPrefix(hash, "$2y$") {
			return nil, errors.New("BASIC_AUTH_HASH does not look like a bcrypt hash")
		}
		c.user = user
		c.hash = []byte(hash)
	}
	return c, nil
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
			if !ok {
				c.challenge(w)
				return
			}
			// Compare username with constant time too — len-leak is
			// minor here but cheap to avoid.
			userOK := subtle.ConstantTimeCompare([]byte(user), []byte(c.user)) == 1
			passOK := bcrypt.CompareHashAndPassword(c.hash, []byte(pass)) == nil
			if !userOK || !passOK {
				c.challenge(w)
				return
			}
			next.ServeHTTP(w, r)
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

// LogStartup announces the chosen mode at boot. Useful in journalctl
// when debugging "why is /admin not asking me for a password".
func (c *Config) LogStartup() {
	switch c.Mode {
	case ModeTrustProxy:
		log.Printf("auth: trust_proxy — admin is unauthenticated at the binary; ensure your reverse proxy gates /admin")
	case ModeBasic:
		log.Printf("auth: basic — admin requires HTTP Basic Auth (user=%q)", c.user)
	case ModeNone:
		log.Printf("auth: none — admin is open. Do not expose this LXC publicly without an upstream gate.")
	}
}
