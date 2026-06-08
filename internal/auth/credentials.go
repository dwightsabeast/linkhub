package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Credentials is the persisted form-auth login: a username and a
// bcrypt hash of the password. Stored as auth.json in the data dir so
// the daemon — which can't write the root-owned env file — can change
// it at runtime from the admin UI.
type Credentials struct {
	Username string `json:"username"`
	Hash     string `json:"hash"` // bcrypt
}

// credStore is the on-disk credential file plus its in-memory cache.
// Reads take RLock, the single writer (save) takes Lock. A nil cached
// creds means "no auth.json yet" — the caller falls back to the env
// bootstrap credential.
type credStore struct {
	mu    sync.RWMutex
	path  string
	creds *Credentials
}

// newCredStore loads auth.json from path if it exists. A missing file
// is not an error — it just means the store is unconfigured and the
// env credential (if any) is authoritative. A present-but-corrupt file
// is an error: better to fail loudly at boot than to silently ignore a
// credential the operator believes is in effect.
func newCredStore(path string) (*credStore, error) {
	s := &credStore{path: path}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var c Credentials
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Username == "" || c.Hash == "" {
		return nil, fmt.Errorf("%s: username and hash must both be set", path)
	}
	s.creds = &c
	return s, nil
}

// get returns the current credentials and whether the store is
// configured. The returned pointer is a copy so callers can't mutate
// the cache.
func (s *credStore) get() (Credentials, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.creds == nil {
		return Credentials{}, false
	}
	return *s.creds, true
}

// configured reports whether a credential has been persisted.
func (s *credStore) configured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.creds != nil
}

// save writes username+hash atomically (temp file → fsync → rename,
// mirroring config.atomicWrite) and swaps the in-memory cache. The
// file holds a bcrypt hash, not a plaintext password, but we still
// restrict it to 0600 since it's the keys to the admin surface.
func (s *credStore) save(username, hash string) error {
	c := Credentials{Username: username, Hash: hash}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".auth-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&c); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmpPath, 0o600)
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}

	s.mu.Lock()
	s.creds = &c
	s.mu.Unlock()
	return nil
}
