package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// defaultSessionTTL is how long a form-auth login stays valid before
// the cookie is rejected and the admin is bounced back to /login. It's
// deliberately generous — this is a single-admin tool, not a bank —
// but bounded so a stolen cookie doesn't live forever.
const defaultSessionTTL = 12 * time.Hour

// sessionStore is an in-memory set of live session tokens keyed to
// their expiry. It's only used in ModeForm; the other modes never
// construct one. State is process-local: a restart logs everyone out,
// which is acceptable for a self-hosted single-admin surface and keeps
// us free of any on-disk session file to secure or rotate.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
	ttl      time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
	}
}

// create mints a fresh random token, records it with a fresh expiry,
// and returns it. The token is 32 bytes of crypto/rand rendered as hex
// — 256 bits of entropy, unguessable. An error here means the system
// RNG failed, which is fatal-adjacent; callers surface it as a 500.
func (s *sessionStore) create() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.sessions[token] = time.Now().Add(s.ttl)
	return token, nil
}

// valid reports whether token names a live, unexpired session. An
// expired entry is dropped as a side effect so the map self-prunes on
// access even between gc passes.
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.sessions, token)
		return false
	}
	return true
}

// destroy removes a token if present. Idempotent — logging out twice,
// or logging out an already-expired session, is a no-op.
func (s *sessionStore) destroy(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// gcLocked drops every expired entry. Called under s.mu from create()
// so the map doesn't grow without bound as sessions age out; valid()
// also prunes on access. There's no background goroutine — login is
// infrequent enough that opportunistic cleanup is plenty.
func (s *sessionStore) gcLocked() {
	now := time.Now()
	for token, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, token)
		}
	}
}
