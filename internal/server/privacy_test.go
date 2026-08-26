package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// optedOut is the single question the renderer asks before deciding
// whether to emit the head snippet and the Google Fonts link. The
// README makes a legal claim about its precedence — a header signal
// always wins over the cookie, and a visitor sending GPC can never be
// opted back in by a stale cookie — so the matrix is worth pinning.
func TestOptedOut(t *testing.T) {
	tests := []struct {
		name   string
		gpc    string
		dnt    string
		cookie string // "" means no cookie set
		want   bool
	}{
		{"no signals at all", "", "", "", false},
		{"gpc on", "1", "", "", true},
		{"dnt on", "", "1", "", true},
		{"both headers on", "1", "1", "", true},
		{"cookie opt-out", "", "", "1", true},
		{"cookie opt-in", "", "", "0", false},

		// A header must override a contrary cookie in both directions.
		{"gpc beats an opt-in cookie", "1", "", "0", true},
		{"dnt beats an opt-in cookie", "", "1", "0", true},
		{"opt-out cookie survives absent headers", "", "", "1", true},

		// GPC defines exactly one meaningful value.
		{"gpc zero is not a signal", "0", "", "", false},
		{"gpc other value is not a signal", "yes", "", "", false},
		{"dnt zero is not a signal", "", "0", "", false},
		{"gpc with whitespace still counts", " 1 ", "", "", true},

		// An unrecognized cookie value means "no choice recorded".
		{"garbage cookie is no choice", "", "", "maybe", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.gpc != "" {
				r.Header.Set("Sec-GPC", tc.gpc)
			}
			if tc.dnt != "" {
				r.Header.Set("DNT", tc.dnt)
			}
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: optOutCookieName, Value: tc.cookie})
			}
			if got := optedOut(r); got != tc.want {
				t.Errorf("optedOut() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContactHref(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"privacy@example.com", "mailto:privacy@example.com"},
		{"  privacy@example.com  ", "mailto:privacy@example.com"},
		{"mailto:privacy@example.com", "mailto:privacy@example.com"},
		{"MAILTO:privacy@example.com", "MAILTO:privacy@example.com"},
		{"https://example.com/privacy", "https://example.com/privacy"},
		{"http://example.com/privacy", "http://example.com/privacy"},
	}
	for _, tc := range tests {
		if got := contactHref(tc.in); got != tc.want {
			t.Errorf("contactHref(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// markPrivacyVary is what stops a shared cache handing a tracked
// response to someone who sent GPC. Every field it names must be there.
func TestMarkPrivacyVary(t *testing.T) {
	w := httptest.NewRecorder()
	markPrivacyVary(w)
	got := w.Header().Get("Vary")
	for _, want := range []string{"Sec-GPC", "DNT", "Cookie"} {
		if !strings.Contains(got, want) {
			t.Errorf("Vary = %q, missing %q", got, want)
		}
	}
}

func TestBannerDurationSec(t *testing.T) {
	tests := []struct {
		speed int
		want  int
	}{
		{1, 40},
		{6, 20},
		{10, 4},
		{0, 40},  // clamped up to 1
		{-5, 40}, // clamped up to 1
		{99, 4},  // clamped down to 10
	}
	for _, tc := range tests {
		if got := bannerDurationSec(tc.speed); got != tc.want {
			t.Errorf("bannerDurationSec(%d) = %d, want %d", tc.speed, got, tc.want)
		}
	}
}
