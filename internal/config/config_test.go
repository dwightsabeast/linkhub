package config

import (
	"strings"
	"testing"
)

// validConfig is the minimum that passes Validate: a name, one link,
// and whatever applyDefaults fills in. Tests mutate a copy of it so
// each case isolates one rule.
func validConfig() Config {
	c := Config{
		Profile: Profile{Name: "Test Studio"},
		Links: []Link{
			{Label: "Site", URL: "https://example.com", Icon: "link"},
		},
	}
	applyDefaults(&c)
	return c
}

func TestValidConfigPasses(t *testing.T) {
	c := validConfig()
	if err := Validate(&c); err != nil {
		t.Fatalf("baseline config failed validation: %v", err)
	}
}

// ── asset references ─────────────────────────────────────────────
//
// profile.avatar and meta.favicon are rendered into a src/href the
// visitor's browser fetches. An off-site value would disclose every
// visitor's IP to that host on every page load while /privacy went on
// describing a site that makes no third-party requests, so the schema
// refuses anything that is not a local path.

func TestAssetRefAcceptsLocalPaths(t *testing.T) {
	good := []string{
		"",
		"/assets/avatar.svg",
		"/assets/avatar.png?v=1735689600",
		"/static/favicon.svg",
		"/assets/nested/dir/image.webp",
	}
	for _, ref := range good {
		if err := checkAssetRef("profile.avatar", ref); err != nil {
			t.Errorf("checkAssetRef(%q) = %v, want nil", ref, err)
		}
	}
}

func TestAssetRefRejectsOffSiteAndTraversal(t *testing.T) {
	bad := []string{
		"https://tracker.example/pixel.png",
		"http://tracker.example/pixel.png",
		"//tracker.example/pixel.png",
		"data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
		"javascript:alert(1)",
		"assets/avatar.svg",
		"/assets/../../etc/passwd",
		"/assets/./avatar.svg",
	}
	for _, ref := range bad {
		if err := checkAssetRef("profile.avatar", ref); err == nil {
			t.Errorf("checkAssetRef(%q) = nil, want a validation error", ref)
		}
	}
}

func TestValidateRejectsOffSiteAvatarAndFavicon(t *testing.T) {
	c := validConfig()
	c.Profile.Avatar = "https://tracker.example/pixel.png"
	err := Validate(&c)
	if err == nil {
		t.Fatal("an off-site avatar passed validation")
	}
	assertField(t, err, "profile.avatar")

	c = validConfig()
	c.Meta.Favicon = "https://tracker.example/pixel.ico"
	err = Validate(&c)
	if err == nil {
		t.Fatal("an off-site favicon passed validation")
	}
	assertField(t, err, "meta.favicon")
}

// ── length budgets ───────────────────────────────────────────────

func TestValidateLengthBoundaries(t *testing.T) {
	// A two-byte rune, to prove the budgets count runes and not bytes.
	const wide = "é"

	tests := []struct {
		name    string
		mutate  func(*Config)
		field   string
		wantErr bool
	}{
		{"name at limit", func(c *Config) { c.Profile.Name = strings.Repeat("a", maxName) }, "", false},
		{"name over limit", func(c *Config) { c.Profile.Name = strings.Repeat("a", maxName+1) }, "profile.name", true},
		{"name empty", func(c *Config) { c.Profile.Name = "" }, "profile.name", true},
		{"tagline at limit", func(c *Config) { c.Profile.Tagline = strings.Repeat("a", maxTagline) }, "", false},
		{"tagline over limit", func(c *Config) { c.Profile.Tagline = strings.Repeat("a", maxTagline+1) }, "profile.tagline", true},
		{"bio over limit", func(c *Config) { c.Profile.Bio = strings.Repeat("a", maxBio+1) }, "profile.bio", true},
		{"footer over limit", func(c *Config) { c.Footer.Text = strings.Repeat("a", maxFooter+1) }, "footer.text", true},
		{"meta title over limit", func(c *Config) { c.Meta.Title = strings.Repeat("a", maxMetaTitle+1) }, "meta.title", true},
		{"multibyte name at limit", func(c *Config) { c.Profile.Name = strings.Repeat(wide, maxName) }, "", false},
		{"multibyte name over limit", func(c *Config) { c.Profile.Name = strings.Repeat(wide, maxName+1) }, "profile.name", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			err := Validate(&c)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want a validation error on %s, got nil", tc.field)
				}
				assertField(t, err, tc.field)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ── shape rules ──────────────────────────────────────────────────

func TestOnlyOneLinkMayBeFeatured(t *testing.T) {
	c := validConfig()
	c.Links = []Link{
		{Label: "One", URL: "https://example.com/1", Icon: "link", Featured: true},
		{Label: "Two", URL: "https://example.com/2", Icon: "link"},
	}
	if err := Validate(&c); err != nil {
		t.Fatalf("one featured link should be fine: %v", err)
	}

	c.Links[1].Featured = true
	err := Validate(&c)
	if err == nil {
		t.Fatal("two featured links passed validation")
	}
	assertField(t, err, "links")
}

func TestTooManyLinks(t *testing.T) {
	c := validConfig()
	c.Links = nil
	for i := 0; i < maxLinks; i++ {
		c.Links = append(c.Links, Link{Label: "L", URL: "https://example.com", Icon: "link"})
	}
	if err := Validate(&c); err != nil {
		t.Fatalf("%d links should be fine: %v", maxLinks, err)
	}
	c.Links = append(c.Links, Link{Label: "L", URL: "https://example.com", Icon: "link"})
	if err := Validate(&c); err == nil {
		t.Fatalf("%d links passed validation", maxLinks+1)
	}
}

func TestValidateRejectsUnknownIcon(t *testing.T) {
	c := validConfig()
	c.Links[0].Icon = "not-a-real-icon"
	err := Validate(&c)
	if err == nil {
		t.Fatal("an unknown icon name passed validation")
	}
	assertField(t, err, "links[0].icon")
}

func TestCheckURLSchemes(t *testing.T) {
	good := []string{
		"https://example.com",
		"http://example.com/path?q=1",
		"mailto:someone@example.com",
		"tel:+15551234567",
	}
	for _, u := range good {
		if err := checkURL("links[0].url", u); err != nil {
			t.Errorf("checkURL(%q) = %v, want nil", u, err)
		}
	}
	// javascript: and data: are the reason this allowlist exists — the
	// value lands in an href on the public page.
	bad := []string{
		"",
		"javascript:alert(1)",
		"JavaScript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"https://",
		"mailto:",
		"ftp://example.com",
	}
	for _, u := range bad {
		if err := checkURL("links[0].url", u); err == nil {
			t.Errorf("checkURL(%q) = nil, want a validation error", u)
		}
	}
}

func TestCheckHex(t *testing.T) {
	for _, v := range []string{"#000000", "#FFFFFF", "#3D5A4C", "#abcdef"} {
		if err := checkHex("theme.accent", v); err != nil {
			t.Errorf("checkHex(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range []string{"", "#FFF", "3D5A4C", "#3D5A4G", "#3D5A4CC", "rgb(0,0,0)"} {
		if err := checkHex("theme.accent", v); err == nil {
			t.Errorf("checkHex(%q) = nil, want a validation error", v)
		}
	}
}

func TestBannerTextRequiredOnlyWhenEnabled(t *testing.T) {
	c := validConfig()
	c.Banner.Enabled = false
	c.Banner.Text = ""
	if err := Validate(&c); err != nil {
		t.Fatalf("an empty disabled banner should be fine: %v", err)
	}

	c.Banner.Enabled = true
	err := Validate(&c)
	if err == nil {
		t.Fatal("an enabled banner with no text passed validation")
	}
	assertField(t, err, "banner.text")

	c.Banner.Text = "   "
	if err := Validate(&c); err == nil {
		t.Fatal("an enabled banner with whitespace-only text passed validation")
	}

	c.Banner.Text = "Shooting today"
	if err := Validate(&c); err != nil {
		t.Fatalf("an enabled banner with text failed: %v", err)
	}
}

// ── privacy block ────────────────────────────────────────────────

func TestPrivacyContact(t *testing.T) {
	good := []string{
		"",
		"privacy@example.com",
		"mailto:privacy@example.com",
		"https://example.com/privacy",
	}
	for _, v := range good {
		if err := checkPrivacyContact("privacy.contact", v); err != nil {
			t.Errorf("checkPrivacyContact(%q) = %v, want nil", v, err)
		}
	}
	bad := []string{
		"privacy at example.com",
		"privacy@",
		"@example.com",
		"privacy@example",
		"a@b@example.com",
		"https://",
	}
	for _, v := range bad {
		if err := checkPrivacyContact("privacy.contact", v); err == nil {
			t.Errorf("checkPrivacyContact(%q) = nil, want a validation error", v)
		}
	}
}

// An unclassified snippet must read as analytics — the fail-safe
// direction, so an upgrade from a pre-privacy config withholds it
// rather than firing it at someone who asked us not to.
func TestEffectiveSnippetCategoryFailsSafe(t *testing.T) {
	tests := []struct {
		name     string
		snippet  string
		category string
		want     string
		suppress bool
	}{
		{"no snippet at all", "", SnippetAnalytics, SnippetNone, false},
		{"whitespace snippet", "   ", SnippetAdvertising, SnippetNone, false},
		{"unclassified snippet", "<script></script>", "", SnippetAnalytics, true},
		{"analytics", "<script></script>", SnippetAnalytics, SnippetAnalytics, true},
		{"advertising", "<script></script>", SnippetAdvertising, SnippetAdvertising, true},
		{"essential is never withheld", "<script></script>", SnippetEssential, SnippetEssential, false},
		{"declared none", "<script></script>", SnippetNone, SnippetNone, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				Meta:    Meta{HeadSnippet: tc.snippet},
				Privacy: Privacy{SnippetCategory: tc.category},
			}
			if got := c.EffectiveSnippetCategory(); got != tc.want {
				t.Errorf("EffectiveSnippetCategory() = %q, want %q", got, tc.want)
			}
			if got := c.SnippetIsSuppressible(); got != tc.suppress {
				t.Errorf("SnippetIsSuppressible() = %v, want %v", got, tc.suppress)
			}
		})
	}
}

func TestSellsOrShares(t *testing.T) {
	c := Config{Meta: Meta{HeadSnippet: "<script></script>"}}
	c.Privacy.SnippetCategory = SnippetAdvertising
	if !c.SellsOrShares() {
		t.Error("an advertising snippet must trigger the sale/share opt-out link")
	}
	c.Privacy.SnippetCategory = SnippetAnalytics
	if c.SellsOrShares() {
		t.Error("an analytics snippet is not a sale or share")
	}
	// No snippet means nothing is shared no matter what is declared.
	c = Config{Privacy: Privacy{SnippetCategory: SnippetAdvertising}}
	if c.SellsOrShares() {
		t.Error("an empty snippet cannot be a sale or share")
	}
}

func TestPrivacyEffectiveDate(t *testing.T) {
	c := validConfig()
	c.Privacy.Effective = "2026-08-25"
	if err := Validate(&c); err != nil {
		t.Fatalf("a well-formed date failed: %v", err)
	}
	for _, bad := range []string{"25-08-2026", "2026/08/25", "August 25 2026", "2026-13-01"} {
		c.Privacy.Effective = bad
		if err := Validate(&c); err == nil {
			t.Errorf("effective date %q passed validation", bad)
		}
	}
}

// ── decoding ─────────────────────────────────────────────────────

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	_, err := DecodeStrict(strings.NewReader(`{"profile":{"name":"x"},"nope":1}`))
	if err == nil {
		t.Fatal("DecodeStrict accepted an unknown field; a typo from the admin would be silently dropped")
	}
}

func TestDecodeStrictAcceptsKnownFields(t *testing.T) {
	c, err := DecodeStrict(strings.NewReader(`{"profile":{"name":"x"},"theme":{"mode":"dark"}}`))
	if err != nil {
		t.Fatalf("DecodeStrict rejected a valid document: %v", err)
	}
	if c.Profile.Name != "x" || c.Theme.Mode != "dark" {
		t.Errorf("decoded %+v, want name=x mode=dark", c)
	}
}

func assertField(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		return
	}
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error %v is not a *ValidationError", err)
	}
	if verr.Field != want {
		t.Errorf("error field = %q, want %q", verr.Field, want)
	}
}
