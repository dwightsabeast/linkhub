package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// safeNext is the open-redirect guard on the post-login bounce. Anything
// that isn't a clean in-app /admin path must collapse to /admin.
func TestSafeNext(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "/admin"},
		{"plain admin", "/admin", "/admin"},
		{"admin subpath", "/admin/settings", "/admin/settings"},
		{"admin with query", "/admin?tab=links", "/admin?tab=links"},
		{"absolute http", "http://evil.example/admin", "/admin"},
		{"absolute https", "https://evil.example/admin", "/admin"},
		{"protocol relative", "//evil.example/admin", "/admin"},
		{"protocol relative admin-ish", "//evil.example/adminX", "/admin"},
		{"backslash trick", `\evil.example/admin`, "/admin"},
		{"relative", "admin", "/admin"},
		{"other rooted path", "/privacy", "/admin"},
		{"root", "/", "/admin"},
		{"scheme relative to admin", "/admin@evil.example", "/admin@evil.example"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeNext(tc.in); got != tc.want {
				t.Errorf("safeNext(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"", ModeTrustProxy, false},
		{"trust_proxy", ModeTrustProxy, false},
		{"  TRUST_PROXY  ", ModeTrustProxy, false},
		{"basic", ModeBasic, false},
		{"form", ModeForm, false},
		{"none", ModeNone, false},
		{"nope", 0, true},
		{"trustproxy", 0, true},
	}
	for _, tc := range tests {
		got, err := ParseMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q) returned %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLooksLikeBcrypt(t *testing.T) {
	valid := []string{
		"$2a$12$abcdefghijklmnopqrstuv",
		"$2b$12$abcdefghijklmnopqrstuv",
		"$2y$12$abcdefghijklmnopqrstuv",
	}
	for _, h := range valid {
		if !looksLikeBcrypt(h) {
			t.Errorf("looksLikeBcrypt(%q) = false, want true", h)
		}
	}
	invalid := []string{"", "plaintext", "$1$md5crypt", "2a$12$x", "$2c$12$x"}
	for _, h := range invalid {
		if looksLikeBcrypt(h) {
			t.Errorf("looksLikeBcrypt(%q) = true, want false", h)
		}
	}
}

// ── rate limiting ────────────────────────────────────────────────

func TestLoginGuardBlocksAfterMaxFailures(t *testing.T) {
	g := newLoginGuard()
	const key = "203.0.113.7"

	for i := 0; i < maxFailures; i++ {
		if _, blocked := g.blocked(key); blocked {
			t.Fatalf("blocked after only %d failures, want %d", i, maxFailures)
		}
		g.recordFailure(key)
	}

	wait, blocked := g.blocked(key)
	if !blocked {
		t.Fatalf("not blocked after %d failures", maxFailures)
	}
	if wait <= 0 || wait > failureWindow {
		t.Errorf("retry-after %v is outside (0, %v]", wait, failureWindow)
	}
}

func TestLoginGuardIsPerClient(t *testing.T) {
	g := newLoginGuard()
	for i := 0; i < maxFailures; i++ {
		g.recordFailure("198.51.100.1")
	}
	if _, blocked := g.blocked("198.51.100.1"); !blocked {
		t.Fatal("the failing client should be blocked")
	}
	if _, blocked := g.blocked("198.51.100.2"); blocked {
		t.Error("an unrelated client was blocked — the operator would be locked out by someone else's attack")
	}
}

func TestLoginGuardClearOnSuccess(t *testing.T) {
	g := newLoginGuard()
	const key = "203.0.113.9"
	for i := 0; i < maxFailures-1; i++ {
		g.recordFailure(key)
	}
	g.clear(key)
	for i := 0; i < maxFailures-1; i++ {
		if _, blocked := g.blocked(key); blocked {
			t.Fatalf("blocked at attempt %d after a successful sign-in reset the count", i)
		}
		g.recordFailure(key)
	}
}

func TestLoginGuardWindowExpires(t *testing.T) {
	g := newLoginGuard()
	const key = "203.0.113.11"
	for i := 0; i < maxFailures; i++ {
		g.recordFailure(key)
	}
	if _, blocked := g.blocked(key); !blocked {
		t.Fatal("expected block")
	}
	// Age the window out rather than sleeping for the real duration.
	g.mu.Lock()
	g.failures[key].expires = time.Now().Add(-time.Second)
	g.mu.Unlock()

	if _, blocked := g.blocked(key); blocked {
		t.Error("still blocked after the window elapsed")
	}
}

func TestLoginGuardConcurrencyCeiling(t *testing.T) {
	g := newLoginGuard()
	for i := 0; i < maxConcurrent; i++ {
		if !g.acquire() {
			t.Fatalf("acquire %d/%d failed while the ceiling should still allow it", i+1, maxConcurrent)
		}
	}
	if g.acquire() {
		t.Error("acquire succeeded past the ceiling; bcrypt work is unbounded")
	}
	g.release()
	if !g.acquire() {
		t.Error("acquire failed after a release freed a slot")
	}
}

func TestClientKey(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"no proxy header", "192.0.2.5:54321", "", "192.0.2.5"},
		{"single forwarded", "10.0.0.1:8080", "203.0.113.4", "203.0.113.4"},
		{"forwarded chain takes the client", "10.0.0.1:8080", "203.0.113.4, 10.0.0.9", "203.0.113.4"},
		{"forwarded with spaces", "10.0.0.1:8080", "  203.0.113.4  ", "203.0.113.4"},
		{"empty forwarded falls back", "192.0.2.5:1234", "   ", "192.0.2.5"},
		{"ipv6 remote", "[2001:db8::1]:443", "", "2001:db8::1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/login", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientKey(r); got != tc.want {
				t.Errorf("clientKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

// checkPassword must refuse without running the comparison once a
// client is blocked — that is the whole point of ordering the guard
// ahead of bcrypt rather than behind it.
func TestCheckPasswordSkipsCompareWhenBlocked(t *testing.T) {
	c := &Config{Mode: ModeForm, guard: newLoginGuard()}
	const key = "203.0.113.20"

	compares := 0
	always := func() bool { compares++; return false }

	for i := 0; i < maxFailures; i++ {
		if _, retry := c.checkPassword(key, always); retry != 0 {
			t.Fatalf("unexpected retry at attempt %d", i)
		}
	}
	if compares != maxFailures {
		t.Fatalf("ran %d comparisons over %d attempts", compares, maxFailures)
	}

	ok, retry := c.checkPassword(key, always)
	if ok {
		t.Error("checkPassword reported success while blocked")
	}
	if retry <= 0 {
		t.Error("checkPassword did not ask the caller to retry later")
	}
	if compares != maxFailures {
		t.Errorf("ran bcrypt %d times; a blocked attempt must not pay for the hash", compares-maxFailures)
	}
}

func TestCheckPasswordWithoutGuardStillWorks(t *testing.T) {
	// trust_proxy and none never build a guard; checkPassword must not
	// panic if it is somehow reached in those modes.
	c := &Config{Mode: ModeTrustProxy}
	ok, retry := c.checkPassword("k", func() bool { return true })
	if !ok || retry != 0 {
		t.Errorf("checkPassword without a guard = (%v, %v), want (true, 0)", ok, retry)
	}
}
