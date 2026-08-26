package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// newTestStore writes cfg to a temp dir and opens a Store on it.
func newTestStore(t *testing.T, raw string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, path
}

const minimalConfig = `{"profile":{"name":"Test"},"links":[],"social":[]}`

func TestNewAppliesDefaults(t *testing.T) {
	s, _ := newTestStore(t, minimalConfig)
	c := s.Get()

	if c.Theme.Mode != "auto" {
		t.Errorf("theme.mode = %q, want auto", c.Theme.Mode)
	}
	if c.Profile.Avatar != "/assets/avatar.svg" {
		t.Errorf("profile.avatar = %q, want the bundled default", c.Profile.Avatar)
	}
	if c.Privacy.SnippetCategory != SnippetAnalytics {
		t.Errorf("privacy.snippetCategory = %q, want %q (the fail-safe reading)",
			c.Privacy.SnippetCategory, SnippetAnalytics)
	}
	if c.Links == nil || c.Social == nil {
		t.Error("nil slices should be normalized to empty ones so the template ranges cleanly")
	}
}

// reload must tolerate a key it doesn't know. A config written by a
// newer release and then rolled back would otherwise stop the daemon
// booting at all — an unknown field is not a reason to refuse service.
func TestNewToleratesUnknownFields(t *testing.T) {
	raw := `{"profile":{"name":"Test"},"somethingFromTheFuture":{"a":1}}`
	s, _ := newTestStore(t, raw)
	if got := s.Get().Profile.Name; got != "Test" {
		t.Errorf("profile.name = %q, want Test", got)
	}
}

func TestNewRejectsMissingFile(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("New on a missing file returned nil error; it must fail loudly rather than invent state")
	}
}

func TestNewRejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); err == nil {
		t.Fatal("New accepted malformed JSON")
	}
}

func TestGetReturnsIndependentSlices(t *testing.T) {
	s, _ := newTestStore(t, minimalConfig)
	c := validConfig()
	if err := s.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := s.Get()
	if len(got.Links) == 0 {
		t.Fatal("expected at least one link")
	}
	got.Links[0].Label = "mutated by the caller"

	if s.Get().Links[0].Label == "mutated by the caller" {
		t.Error("Get handed out a slice sharing the cached backing array")
	}
}

func TestSaveRejectsInvalidConfig(t *testing.T) {
	s, path := newTestStore(t, minimalConfig)
	before, _ := os.ReadFile(path)

	c := validConfig()
	c.Profile.Name = "" // required
	if err := s.Save(c); err == nil {
		t.Fatal("Save accepted a config with no name")
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("a rejected Save still wrote to disk")
	}
}

func TestSavePersistsAndSwaps(t *testing.T) {
	s, path := newTestStore(t, minimalConfig)

	c := validConfig()
	c.Profile.Tagline = "Independent media"
	if err := s.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := s.Get().Profile.Tagline; got != "Independent media" {
		t.Errorf("in-memory tagline = %q", got)
	}

	var onDisk Config
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("on-disk config is not valid JSON: %v", err)
	}
	if onDisk.Profile.Tagline != "Independent media" {
		t.Errorf("on-disk tagline = %q", onDisk.Profile.Tagline)
	}
}

func TestSetAvatarAndFavicon(t *testing.T) {
	s, path := newTestStore(t, minimalConfig)

	if err := s.SetAvatar("/assets/avatar.png?v=1"); err != nil {
		t.Fatalf("SetAvatar: %v", err)
	}
	if err := s.SetFavicon("/assets/favicon.ico?v=2"); err != nil {
		t.Fatalf("SetFavicon: %v", err)
	}

	got := s.Get()
	if got.Profile.Avatar != "/assets/avatar.png?v=1" {
		t.Errorf("avatar = %q", got.Profile.Avatar)
	}
	// The second write must not have rolled back the first.
	if got.Meta.Favicon != "/assets/favicon.ico?v=2" {
		t.Errorf("favicon = %q", got.Meta.Favicon)
	}

	var onDisk Config
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Profile.Avatar != got.Profile.Avatar || onDisk.Meta.Favicon != got.Meta.Favicon {
		t.Errorf("disk and memory disagree: disk avatar=%q favicon=%q, memory avatar=%q favicon=%q",
			onDisk.Profile.Avatar, onDisk.Meta.Favicon, got.Profile.Avatar, got.Meta.Favicon)
	}
}

// Writers used to release the lock before writing to disk, so two that
// interleaved could land on disk in the opposite order from memory —
// the process would serve one config and restart into another. Run this
// with -race; the assertion at the end is that whatever memory holds is
// exactly what disk holds.
func TestConcurrentWritesLeaveDiskAndMemoryAgreeing(t *testing.T) {
	s, path := newTestStore(t, minimalConfig)

	base := validConfig()
	if err := s.Save(base); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			switch n % 3 {
			case 0:
				c := base
				c.Profile.Tagline = "tagline"
				_ = s.Save(c)
			case 1:
				_ = s.SetAvatar("/assets/avatar.png?v=" + strconv.Itoa(n))
			case 2:
				_ = s.SetFavicon("/assets/favicon.png?v=" + strconv.Itoa(n))
			}
		}(i)
	}
	// Readers run alongside to catch a torn read under -race.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Get()
		}()
	}
	wg.Wait()

	mem := s.Get()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk Config
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("on-disk config is not valid JSON after concurrent writes: %v", err)
	}
	if disk.Profile.Avatar != mem.Profile.Avatar {
		t.Errorf("avatar diverged: disk=%q memory=%q", disk.Profile.Avatar, mem.Profile.Avatar)
	}
	if disk.Meta.Favicon != mem.Meta.Favicon {
		t.Errorf("favicon diverged: disk=%q memory=%q", disk.Meta.Favicon, mem.Meta.Favicon)
	}
}

// atomicWrite must leave no stray temp files behind on success.
func TestAtomicWriteCleansUp(t *testing.T) {
	s, path := newTestStore(t, minimalConfig)
	if err := s.Save(validConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("unexpected leftover file %q in the data dir", e.Name())
		}
	}
}
