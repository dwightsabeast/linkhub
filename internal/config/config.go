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
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Profile is the header block of the public page.
type Profile struct {
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Bio         string `json:"bio"`
	Avatar      string `json:"avatar"`
	AvatarSize  int    `json:"avatarSize,omitempty"`
	AvatarShape int    `json:"avatarShape,omitempty"` // 0 = square, 50 = circle
	Location    string `json:"location"`
}

// Theme controls accent + light/dark behavior.
type Theme struct {
	Mode        string `json:"mode"`                  // "auto" | "light" | "dark"
	Accent      string `json:"accent"`                // hex, light theme
	AccentDark  string `json:"accentDark"`            // hex, dark theme
	FontDisplay string `json:"fontDisplay,omitempty"` // e.g. "Fraunces"
	FontBody    string `json:"fontBody,omitempty"`    // e.g. "Geist"
	FontMono    string `json:"fontMono,omitempty"`    // e.g. "JetBrains Mono"
}

// Meta is HTML <head> content.
type Meta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Favicon     string `json:"favicon"`               // path to custom favicon; empty = bundled default
	HeadSnippet string `json:"headSnippet,omitempty"` // custom HTML injected before </head>

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

// Banner is an optional announcement bar rendered at the very top of
// the public page. Used for time-sensitive notices ("shooting today")
// that don't warrant editing the profile copy.
type Banner struct {
	Enabled    bool   `json:"enabled"`
	Text       string `json:"text"`
	Background string `json:"background"`      // hex, bar background
	TextColor  string `json:"textColor"`       // hex, bar text
	Scroll     bool   `json:"scroll"`          // marquee on/off
	Speed      int    `json:"speed,omitempty"` // 1 (slow) … 10 (fast)
}

// Privacy carries the operator-supplied facts the /privacy notice is
// generated from, plus the two switches that decide what the public
// page is allowed to load.
//
// US state privacy law is a notice-and-opt-out regime, not the EU's
// opt-in one: none of the comprehensive state laws requires a consent
// banner before non-essential cookies load. What twelve of them *do*
// require is that a site detect and honor a universal opt-out signal
// (Global Privacy Control) automatically. That detection is
// unconditional in LinkHub — see internal/server/privacy.go — so
// there is deliberately no "honorGPC" switch here to turn it off.
type Privacy struct {
	// Operator is who runs the site — the "we" of the privacy notice.
	// Empty renders the notice in an impersonal voice.
	Operator string `json:"operator,omitempty"`

	// Contact is where privacy-rights requests go: an email address or
	// an https URL. Empty means the notice says so plainly rather than
	// inventing a channel that doesn't exist.
	Contact string `json:"contact,omitempty"`

	// SnippetCategory classifies what meta.headSnippet actually is, so
	// the opt-out machinery knows whether suppressing it is correct and
	// the notice can describe it honestly. One of:
	//
	//   none         — the snippet does no tracking (or there is none)
	//   essential    — strictly necessary; never suppressed
	//   analytics    — measurement; suppressed on opt-out
	//   advertising  — targeted ads / sale-or-share; suppressed on opt-out
	//
	// Defaults to "analytics" when unset. That's the fail-safe reading:
	// an unclassified snippet gets suppressed for opted-out visitors
	// rather than quietly firing. An operator whose snippet is genuinely
	// benign has to say so explicitly.
	SnippetCategory string `json:"snippetCategory,omitempty"`

	// SnippetDescription names what the snippet actually is and who
	// receives the data — "Plausible Analytics, self-hosted" or
	// "Meta Pixel". The notice can't introspect arbitrary HTML, and
	// "categories of third parties" is a required disclosure in every
	// state, so without this the notice can only say that *something*
	// third-party is loaded. Empty falls back to that vaguer wording.
	SnippetDescription string `json:"snippetDescription,omitempty"`

	// FontSource decides where the public page's typefaces come from.
	// "google" fetches from fonts.googleapis.com, which discloses every
	// visitor's IP address and User-Agent to Google; "system" uses the
	// local font stack and makes no third-party request at all. Google
	// is the default (it's the existing behavior) but it is always
	// downgraded to "system" for an opted-out visitor.
	FontSource string `json:"fontSource,omitempty"`

	// Retention describes how long request logs and config data are
	// kept. Free text — the operator knows their own backup policy.
	Retention string `json:"retention,omitempty"`

	// Effective is the notice's effective date, YYYY-MM-DD. Empty
	// suppresses the dateline.
	Effective string `json:"effective,omitempty"`
}

// Config is the full document at /var/lib/linkhub/config.json.
type Config struct {
	Profile Profile  `json:"profile"`
	Theme   Theme    `json:"theme"`
	Meta    Meta     `json:"meta"`
	Links   []Link   `json:"links"`
	Social  []Social `json:"social"`
	Footer  Footer   `json:"footer"`
	Banner  Banner   `json:"banner"`
	Privacy Privacy  `json:"privacy"`
}

// Snippet category tokens. Kept as constants because both the
// validator and the server's suppression logic switch on them.
const (
	SnippetNone        = "none"
	SnippetEssential   = "essential"
	SnippetAnalytics   = "analytics"
	SnippetAdvertising = "advertising"
)

// Font source tokens.
const (
	FontSourceGoogle = "google"
	FontSourceSystem = "system"
)

// EffectiveSnippetCategory reports how the head snippet should be
// treated. An empty snippet is always "none" no matter what the
// operator selected, so the notice never describes a tracker that
// isn't actually on the page.
func (c *Config) EffectiveSnippetCategory() string {
	if strings.TrimSpace(c.Meta.HeadSnippet) == "" {
		return SnippetNone
	}
	if c.Privacy.SnippetCategory == "" {
		return SnippetAnalytics
	}
	return c.Privacy.SnippetCategory
}

// SnippetIsSuppressible reports whether the head snippet must be held
// back from a visitor who has opted out (via GPC or the site's own
// opt-out). Essential snippets and absent ones always render.
func (c *Config) SnippetIsSuppressible() bool {
	switch c.EffectiveSnippetCategory() {
	case SnippetAnalytics, SnippetAdvertising:
		return true
	}
	return false
}

// SellsOrShares reports whether the configured tracking amounts to
// the "sale/sharing" or "targeted advertising" that triggers the
// dedicated opt-out link obligations in California and the other
// opt-out states.
func (c *Config) SellsOrShares() bool {
	return c.EffectiveSnippetCategory() == SnippetAdvertising
}

// Length budgets from README → CONTENT FUNDAMENTALS. Enforced on save.
const (
	maxName            = 32
	maxTagline         = 60
	maxBio             = 240
	maxLocation        = 60
	maxLinkLabel       = 36
	maxLinkDescription = 60
	maxHeadSnippet     = 2000
	maxFooter          = 80
	maxLinks           = 12 // soft cap; visual rhythm degrades past ~8
	maxSocial          = 12
	maxMetaTitle       = 100
	maxMetaDescription = 200
	maxBannerText      = 120
	maxPrivacyOperator = 80
	maxPrivacyContact  = 120
	maxPrivacyReten    = 300
	maxPrivacySnipDesc = 160
)

// Store is the runtime cache of Config plus its on-disk path.
// Reads (RLock) and writes (Lock) are mediated by mu.
type Store struct {
	// writeMu serializes writers end to end. A writer holds it across
	// both the disk write and the in-memory swap, so two concurrent
	// writes can never land on disk in one order and in memory in the
	// other — which is what happened when SetAvatar released mu before
	// calling atomicWrite. Readers take only mu, so a save never blocks
	// a page render.
	writeMu sync.Mutex

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
	// Deliberately *not* DisallowUnknownFields. This decoder runs at
	// boot against whatever is on disk, which includes a config written
	// by a newer release than the one now starting. Refusing to boot on
	// an unrecognized key turns a rollback into an outage. The admin's
	// write path keeps the strict decoder (see DecodeStrict), where
	// rejecting a typo is the correct answer.
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
//
// make+copy rather than append-to-nil: appending zero elements to a nil
// slice yields nil, which would send "links": null over /api/config for
// a profile that simply has no links yet. applyDefaults guarantees both
// are non-nil on load and the JSON contract should keep them that way.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Links = make([]Link, len(s.cfg.Links))
	copy(c.Links, s.cfg.Links)
	c.Social = make([]Social, len(s.cfg.Social))
	copy(c.Social, s.cfg.Social)
	return c
}

// Save validates incoming config, writes it atomically, and swaps the
// in-memory cache. Returns ValidationError for caller-correctable
// problems and a wrapped io error for everything else.
func (s *Store) Save(c Config) error {
	if err := Validate(&c); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
	return s.mutate(func(c *Config) { c.Profile.Avatar = avatar })
}

// SetFavicon updates only the meta.favicon field. Same pattern as
// SetAvatar: the favicon upload handler writes the file to disk,
// then calls this to update the config reference.
func (s *Store) SetFavicon(favicon string) error {
	return s.mutate(func(c *Config) { c.Meta.Favicon = favicon })
}

// mutate applies fn to a copy of the live config, persists the result,
// and swaps it in — all under writeMu so a concurrent Save can't
// interleave and leave disk and memory disagreeing about which write
// came last. fn must touch only scalar fields; the copy shares its
// Links/Social backing arrays with the cached config.
//
// Validate is deliberately not run here. These setters write a value
// the upload handler just constructed, not operator input, and running
// the full validator would let unrelated pre-existing config problems
// fail an avatar upload.
func (s *Store) mutate(fn func(*Config)) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	c := s.cfg
	s.mu.RUnlock()

	fn(&c)
	if err := atomicWrite(s.path, &c); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	return nil
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
	if c.Banner.Background == "" {
		c.Banner.Background = "#3D5A4C"
	}
	if c.Banner.TextColor == "" {
		c.Banner.TextColor = "#FFFFFF"
	}
	if c.Banner.Speed == 0 {
		c.Banner.Speed = 6
	}
	// Fail-safe: an unclassified snippet is treated as analytics, so an
	// upgrade from a pre-privacy config suppresses it on opt-out rather
	// than firing it at a visitor who asked us not to.
	if c.Privacy.SnippetCategory == "" {
		c.Privacy.SnippetCategory = SnippetAnalytics
	}
	if c.Privacy.FontSource == "" {
		c.Privacy.FontSource = FontSourceGoogle
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
	if err := checkAssetRef("profile.avatar", c.Profile.Avatar); err != nil {
		return err
	}
	if err := checkAssetRef("meta.favicon", c.Meta.Favicon); err != nil {
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
	var allowedFonts = map[string]bool{
		"Fraunces": true, "Geist": true, "JetBrains Mono": true,
		"Inter": true, "Lora": true, "Playfair Display": true,
		"Space Grotesk": true, "IBM Plex Sans": true,
		"IBM Plex Mono": true, "Fira Code": true,
		"Source Serif 4": true, "DM Sans": true,
		"DM Serif Display": true, "Sora": true,
	}

	if !allowedFonts[c.Theme.FontDisplay] {
		return &ValidationError{Field: "theme.fontDisplay", Message: "unsupported font"}
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
	if len(c.Links) > maxLinks {
		return &ValidationError{Field: "links", Message: fmt.Sprintf("too many links (max %d)", maxLinks)}
	}
	if err := checkLen("meta.headSnippet", c.Meta.HeadSnippet, 0, maxHeadSnippet); err != nil {
		return err
	}

	// Banner. Colors and speed are always validated because
	// applyDefaults guarantees they're populated; text is only
	// required when the banner is actually turned on.
	if err := checkLen("banner.text", c.Banner.Text, 0, maxBannerText); err != nil {
		return err
	}
	if c.Banner.Enabled && len(strings.TrimSpace(c.Banner.Text)) == 0 {
		return &ValidationError{Field: "banner.text", Message: "is required when the banner is enabled"}
	}
	if err := checkHex("banner.background", c.Banner.Background); err != nil {
		return err
	}
	if err := checkHex("banner.textColor", c.Banner.TextColor); err != nil {
		return err
	}
	if c.Banner.Speed < 1 || c.Banner.Speed > 10 {
		return &ValidationError{Field: "banner.speed", Message: "must be between 1 and 10"}
	}

	// Privacy. Every field is optional — an empty category or font
	// source means "use the default", which applyDefaults fills in on
	// the next load. We validate the tokens only when they're present.
	if err := checkLen("privacy.operator", c.Privacy.Operator, 0, maxPrivacyOperator); err != nil {
		return err
	}
	if err := checkLen("privacy.contact", c.Privacy.Contact, 0, maxPrivacyContact); err != nil {
		return err
	}
	if err := checkLen("privacy.retention", c.Privacy.Retention, 0, maxPrivacyReten); err != nil {
		return err
	}
	if err := checkLen("privacy.snippetDescription", c.Privacy.SnippetDescription, 0, maxPrivacySnipDesc); err != nil {
		return err
	}
	if err := checkPrivacyContact("privacy.contact", c.Privacy.Contact); err != nil {
		return err
	}
	switch c.Privacy.SnippetCategory {
	case "", SnippetNone, SnippetEssential, SnippetAnalytics, SnippetAdvertising:
	default:
		return &ValidationError{
			Field:   "privacy.snippetCategory",
			Message: "must be none, essential, analytics, or advertising",
		}
	}
	switch c.Privacy.FontSource {
	case "", FontSourceGoogle, FontSourceSystem:
	default:
		return &ValidationError{Field: "privacy.fontSource", Message: "must be google or system"}
	}
	if c.Privacy.Effective != "" {
		if _, err := time.Parse("2006-01-02", c.Privacy.Effective); err != nil {
			return &ValidationError{Field: "privacy.effective", Message: "must be a date in YYYY-MM-DD form"}
		}
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

// checkAssetRef validates a reference to an image the public page will
// load on the visitor's behalf: profile.avatar and meta.favicon. Both
// are rendered straight into a src/href the browser fetches, so an
// off-site value here would hand every visitor's IP and User-Agent to a
// third party on every page load — silently, and while /privacy went on
// describing a site that makes no third-party requests. That is the one
// way the generated notice can become false, so the schema refuses it.
//
// We accept only a rooted local path: what the upload handler writes
// ("/assets/avatar.png"), plus the "?v=" cache-buster it appends, and
// the bundled defaults under /static. Empty is fine — applyDefaults
// fills both fields on the next load.
func checkAssetRef(field, raw string) error {
	if raw == "" {
		return nil
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return &ValidationError{
			Field:   field,
			Message: "must be a local path starting with / (an off-site URL would disclose every visitor's IP to that host)",
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return &ValidationError{Field: field, Message: "is not a valid path"}
	}
	// url.Parse leaves both empty for a plain "/a/b". A value that
	// still carries either smuggled a scheme or an authority past the
	// prefix check above.
	if u.Scheme != "" || u.Host != "" {
		return &ValidationError{Field: field, Message: "must not include a scheme or a host"}
	}
	if u.Path != path.Clean(u.Path) {
		return &ValidationError{Field: field, Message: "must not contain '.' or '..' segments"}
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

// checkPrivacyContact accepts an empty contact, a bare email address,
// a mailto: URL, or an http(s) URL. This is the address a consumer
// uses to exercise a legal right, so a typo'd value is worse than a
// blank one — we'd rather refuse than render a dead channel into the
// notice. The email check is deliberately loose (one @, a dot in the
// domain, no spaces): full RFC 5322 validation rejects real addresses
// and buys nothing here.
func checkPrivacyContact(field, raw string) error {
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return &ValidationError{Field: field, Message: "must not contain spaces"}
	}
	if u, err := url.Parse(raw); err == nil {
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			if u.Host == "" {
				return &ValidationError{Field: field, Message: "is missing a host"}
			}
			return nil
		case "mailto":
			if u.Opaque == "" {
				return &ValidationError{Field: field, Message: "is empty"}
			}
			return nil
		}
	}
	at := strings.Index(raw, "@")
	if at > 0 && at == strings.LastIndex(raw, "@") {
		if domain := raw[at+1:]; strings.Contains(domain, ".") &&
			!strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".") {
			return nil
		}
	}
	return &ValidationError{
		Field:   field,
		Message: "must be an email address or an http(s) URL",
	}
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
