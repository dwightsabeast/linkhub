// Package config defines LinkHub's on-disk config schema and provides
// thread-safe load/save with atomic writes.
//
// The Store is the source of truth at runtime: HTTP handlers read it
// under RLock for rendering and write through Save() which validates,
// persists atomically, and swaps the in-memory copy.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// Profile is the header block of the public page.
type Profile struct {
	Name     string `json:"name"`
	Tagline  string `json:"tagline"`
	Bio      string `json:"bio"`
	Avatar   string `json:"avatar"`
	AvatarSize int    `json:"avatarSize,omitempty"`
	AvatarShape int    `json:"avatarShape,omitempty"` // 0 = square, 50 = circle
	Location string `json:"location"`
}

// Theme controls accent + light/dark behavior.
type Theme struct {
	Mode       string `json:"mode"`       // "auto" | "light" | "dark"
	Accent     string `json:"accent"`     // hex, light theme
	AccentDark string `json:"accentDark"` // hex, dark theme
	FontDisplay string `json:"fontDisplay,omitempty"` // e.g. "Fraunces"
    FontBody    string `json:"fontBody,omitempty"`    // e.g. "Geist"
    FontMono    string `json:"fontMono,omitempty"`    // e.g. "JetBrains Mono"
}

// Meta is HTML <head> content.
type Meta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Favicon     string `json:"favicon"`    // path to custom favicon; empty = bundled default

}

// Link is a primary link card.
type Link struct {
	Label       string `json:"label"`
	URL         string `json:"url"`
	Icon        string `json:"icon"`
	Description string `json:"description,omitempty"`
	Featured    bool   `json:"featured,omitempty"`
}

// Social is a pill in the social row.
type Social struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

// Footer is the page footer.
type Footer struct {
	ShowYear bool   `json:"showYear"`
	Text     string `json:"text"`
}

// Config is the full document at /var/lib/linkhub/config.json.
type Config struct {
	Profile Profile  `json:"profile"`
	Theme   Theme    `json:"theme"`
	Meta    Meta     `json:"meta"`
	Links   []Link   `json:"links"`
	Social  []Social `json:"social"`
	Footer  Footer   `json:"footer"`
}

// Length budgets from README → CONTENT FUNDAMENTALS. Enforced on save.
const (
	maxName            = 32
	maxTagline         = 60
	maxBio             = 240
	maxLocation        = 60
	maxLinkLabel       = 36
	maxLinkDescription = 60
	maxFooter          = 80
	maxLinks           = 12 // soft cap; visual rhythm degrades past ~8
	maxSocial          = 12
	maxMetaTitle       = 100
	maxMetaDescription = 200
)

// Store is the runtime cache of Config plus its on-disk path.
// Reads (RLock) and writes (Lock) are mediated by mu.
type Store struct {
	mu   sync.RWMutex
	cfg  Config
	path string
}

// New loads config.json from path. If the file is missing, the caller
// is responsible for laying down a default first — New does not invent.
func New(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) reload() error {
	f, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var c Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	// Tolerant on load: fill defaults for fields that may be missing
	// after an upgrade. We never refuse to boot.
	applyDefaults(&c)

	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	return nil
}

// Get returns a deep-ish copy of the current config. Slices are
// re-allocated so the caller can't mutate the cached state through
// shared backing arrays.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Links = append([]Link(nil), s.cfg.Links...)
	c.Social = append([]Social(nil), s.cfg.Social...)
	return c
}

// Save validates incoming config, writes it atomically, and swaps the
// in-memory cache. Returns ValidationError for caller-correctable
// problems and a wrapped io error for everything else.
func (s *Store) Save(c Config) error {
	if err := Validate(&c); err != nil {
		return err
	}
	if err := atomicWrite(s.path, &c); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	return nil
}

// SetAvatar updates only the profile.avatar field. This is the path
// /api/avatar takes after writing the file: it doesn't trust the
// admin to round-trip the whole config through an upload handler.
func (s *Store) SetAvatar(avatar string) error {
	s.mu.Lock()
	c := s.cfg
	c.Profile.Avatar = avatar
	s.cfg = c
	s.mu.Unlock()
	// Re-fetch under RLock semantics is fine because we hold no lock
	// here; atomicWrite is its own critical section by virtue of
	// rename(2) being atomic on the FS.
	return atomicWrite(s.path, &c)
}

// SetFavicon updates only the meta.favicon field. Same pattern as
// SetAvatar: the favicon upload handler writes the file to disk,
// then calls this to update the config reference.
func (s *Store) SetFavicon(favicon string) error {
	s.mu.Lock()
	c := s.cfg
	c.Meta.Favicon = favicon
	s.cfg = c
	s.mu.Unlock()
	return atomicWrite(s.path, &c)
}

// atomicWrite writes c to a sibling .tmp file, fsyncs it, then
// renames over path. Within a single filesystem this is atomic per
// POSIX; readers will see either the old file or the new one.
func atomicWrite(path string, c *Config) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// If anything below fails, try not to leave a turd behind.
	defer os.Remove(tmpPath)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
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
	// Match the data dir's permissions (linkhub:linkhub 0750 by default).
	_ = os.Chmod(tmpPath, 0o640)
	return os.Rename(tmpPath, path)
}

// applyDefaults fills empty optional fields so a partially-populated
// config (e.g. after upgrading and a new field is added) still
// renders something sensible.
func applyDefaults(c *Config) {
	if c.Theme.Mode == "" {
		c.Theme.Mode = "auto"
	}
	if c.Theme.Accent == "" {
		c.Theme.Accent = "#3D5A4C"
	}
	if c.Theme.AccentDark == "" {
		c.Theme.AccentDark = "#8FB3A4"
	}
	if c.Theme.FontDisplay == "" {
    	c.Theme.FontDisplay = "Fraunces"
	}
	if c.Theme.FontBody == "" {
    	c.Theme.FontBody = "Geist"
	}
	if c.Theme.FontMono == "" {
    	c.Theme.FontMono = "JetBrains Mono"
	}
	if c.Profile.Avatar == "" {
		c.Profile.Avatar = "/assets/avatar.svg"
	}
	if c.Profile.AvatarSize == 0 {
    	c.Profile.AvatarSize = 96
	}
	if c.Profile.AvatarShape == 0 {
    	c.Profile.AvatarShape = 50
	}
	if c.Meta.Favicon == "" {
		c.Meta.Favicon = "/static/favicon.svg"
	}
	if c.Links == nil {
		c.Links = []Link{}
	}
	if c.Social == nil {
		c.Social = []Social{}
	}
}

// ValidationError reports a user-correctable problem with submitted
// config. Field is dotted JSON path (e.g. "links[2].label").
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Validate enforces the length budgets and shape rules from the
// README's CONTENT FUNDAMENTALS section. Returns a *ValidationError
// for the first problem found; callers can JSON-encode it directly.
func Validate(c *Config) error {
	if err := checkLen("profile.name", c.Profile.Name, 1, maxName); err != nil {
		return err
	}
	if err := checkLen("profile.tagline", c.Profile.Tagline, 0, maxTagline); err != nil {
		return err
	}
	if err := checkLen("profile.bio", c.Profile.Bio, 0, maxBio); err != nil {
		return err
	}
	if err := checkLen("profile.location", c.Profile.Location, 0, maxLocation); err != nil {
		return err
	}
	if err := checkLen("meta.title", c.Meta.Title, 0, maxMetaTitle); err != nil {
		return err
	}
	if err := checkLen("meta.description", c.Meta.Description, 0, maxMetaDescription); err != nil {
		return err
	}
	if err := checkLen("footer.text", c.Footer.Text, 0, maxFooter); err != nil {
		return err
	}

	if err := checkHex("theme.accent", c.Theme.Accent); err != nil {
		return err
	}
	if err := checkHex("theme.accentDark", c.Theme.AccentDark); err != nil {
		return err
	}
	if c.Profile.AvatarSize < 48 || c.Profile.AvatarSize > 200 {
    return &ValidationError{Field: "profile.avatarSize", Message: "must be between 48 and 200"}
	}
	if c.Profile.AvatarShape < 1 || c.Profile.AvatarShape > 50 {
    return &ValidationError{Field: "profile.avatarShape", Message: "must be between 1 and 50"}
	}
	if !allowedFonts[c.Theme.FontBody] {
    	return &ValidationError{Field: "theme.fontBody", Message: "unsupported font"}
	}
	if !allowedFonts[c.Theme.FontMono] {
    	return &ValidationError{Field: "theme.fontMono", Message: "unsupported font"}
	}
	switch c.Theme.Mode {
	case "auto", "light", "dark":
	default:
		return &ValidationError{Field: "theme.mode", Message: "must be auto, light, or dark"}
	}
	var allowedFonts = map[string]bool{
    	"Fraunces": true, "Geist": true, "JetBrains Mono": true,
    	"Inter": true, "Lora": true, "Playfair Display": true,
    	"Space Grotesk": true, "IBM Plex Sans": true,
    	"IBM Plex Mono": true, "Fira Code": true,
    	"Source Serif 4": true, "DM Sans": true,
    	"DM Serif Display": true, "Sora": true,
    	// add more as desired
	}

	if !allowedFonts[c.Theme.FontDisplay] {
    	return &ValidationError{Field: "theme.fontDisplay", Message: "unsupported font"}
	}
	// same for FontBody, FontMono
	if len(c.Links) > maxLinks {
		return &ValidationError{Field: "links", Message: fmt.Sprintf("too many links (max %d)", maxLinks)}
	}
	featured := 0
	for i, l := range c.Links {
		base := fmt.Sprintf("links[%d]", i)
		if err := checkLen(base+".label", l.Label, 1, maxLinkLabel); err != nil {
			return err
		}
		if err := checkLen(base+".description", l.Description, 0, maxLinkDescription); err != nil {
			return err
		}
		if err := checkURL(base+".url", l.URL); err != nil {
			return err
		}
		if !ValidIcon(l.Icon) {
			return &ValidationError{Field: base + ".icon", Message: "unknown icon name"}
		}
		if l.Featured {
			featured++
		}
	}
	if featured > 1 {
		return &ValidationError{Field: "links", Message: "only one link may be featured"}
	}

	if len(c.Social) > maxSocial {
		return &ValidationError{Field: "social", Message: fmt.Sprintf("too many social links (max %d)", maxSocial)}
	}
	for i, s := range c.Social {
		base := fmt.Sprintf("social[%d]", i)
		if !ValidIcon(s.Platform) {
			return &ValidationError{Field: base + ".platform", Message: "unknown platform"}
		}
		if err := checkURL(base+".url", s.URL); err != nil {
			return err
		}
	}
	return nil
}

func checkLen(field, v string, min, max int) error {
	n := utf8.RuneCountInString(v)
	if n < min {
		return &ValidationError{Field: field, Message: "is required"}
	}
	if n > max {
		return &ValidationError{Field: field, Message: fmt.Sprintf("too long (max %d chars)", max)}
	}
	return nil
}

func checkHex(field, v string) error {
	if len(v) != 7 || v[0] != '#' {
		return &ValidationError{Field: field, Message: "must be a 7-char hex color (#rrggbb)"}
	}
	for _, r := range v[1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return &ValidationError{Field: field, Message: "must be a 7-char hex color (#rrggbb)"}
		}
	}
	return nil
}

// checkURL accepts http(s)://, mailto:, tel:. We deliberately don't
// allow javascript: or data: — the public page renders these as href
// attributes and we don't want to ship an XSS vector.
func checkURL(field, raw string) error {
	if raw == "" {
		return &ValidationError{Field: field, Message: "is required"}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return &ValidationError{Field: field, Message: "is not a valid URL"}
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return &ValidationError{Field: field, Message: "is missing a host"}
		}
	case "mailto", "tel":
		if u.Opaque == "" {
			return &ValidationError{Field: field, Message: "is empty"}
		}
	default:
		return &ValidationError{Field: field, Message: "scheme must be http(s), mailto, or tel"}
	}
	return nil
}

// DecodeStrict reads a Config from r, rejecting unknown fields. Used
// by the PUT /api/config handler.
func DecodeStrict(r io.Reader) (Config, error) {
	var c Config
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		var verr *ValidationError
		if errors.As(err, &verr) {
			return c, err
		}
		return c, &ValidationError{Field: "_root", Message: err.Error()}
	}
	return c, nil
}
