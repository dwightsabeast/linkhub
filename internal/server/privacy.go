package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dwightsabeast/linkhub/internal/config"
)

// ── Opt-out signals ──────────────────────────────────────────────
//
// LinkHub honors three signals, in this order of authority:
//
//   1. Sec-GPC: 1        — Global Privacy Control, a browser-level
//                          universal opt-out. Twelve states (CA, CO,
//                          CT, DE, MD, MN, MT, NE, NH, NJ, OR, TX)
//                          require a site to detect and apply this
//                          automatically, with no banner and no extra
//                          step from the visitor.
//   2. DNT: 1            — the older Do Not Track header. Not legally
//                          mandated anywhere, but it is an unambiguous
//                          request from the visitor and costs us
//                          nothing to respect, so we do.
//   3. the opt-out cookie — this site's own "Your Privacy Choices"
//                          control, for visitors whose browser sends
//                          neither header.
//
// A header signal always wins over the cookie. A visitor arriving with
// GPC set cannot be silently opted back in by a stale cookie, and we
// never ask them to reconfirm — that would defeat the point of a
// universal signal.

// optOutCookieName carries this site's own opt-out choice. "1" means
// opted out of non-essential tracking; "0" means the visitor explicitly
// chose to allow it. Absent means "no choice recorded".
const optOutCookieName = "linkhub_privacy_optout"

// optOutCookieMaxAge is how long a recorded choice persists. A year is
// the usual floor for a preference a business is obliged to remember;
// shorter would mean re-asking a visitor who already answered.
const optOutCookieMaxAge = 365 * 24 * 60 * 60

// gpcHeaderSet reports whether the request carries Sec-GPC: 1. The GPC
// spec defines exactly one meaningful value, "1"; anything else
// (including "0") is not an opt-out signal.
func gpcHeaderSet(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("Sec-GPC")) == "1"
}

// dntHeaderSet reports whether the request carries DNT: 1.
func dntHeaderSet(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("DNT")) == "1"
}

// cookieOptOut reports the visitor's recorded site-level choice:
// true/false when a choice exists, and ok=false when none does.
func cookieOptOut(r *http.Request) (choice, ok bool) {
	c, err := r.Cookie(optOutCookieName)
	if err != nil {
		return false, false
	}
	switch c.Value {
	case "1":
		return true, true
	case "0":
		return false, true
	}
	return false, false
}

// optedOut is the single question the rest of the server asks: may we
// run non-essential tracking for this request?
func optedOut(r *http.Request) bool {
	if gpcHeaderSet(r) || dntHeaderSet(r) {
		return true
	}
	choice, ok := cookieOptOut(r)
	return ok && choice
}

// setOptOutCookie records a privacy choice. This cookie is itself
// strictly necessary — it exists only to remember that the visitor
// asked not to be tracked, which is a purpose every US state law
// permits without consent (and effectively requires, since the choice
// has to persist). HttpOnly because no script needs to read it.
func setOptOutCookie(w http.ResponseWriter, r *http.Request, out bool) {
	value := "0"
	if out {
		value = "1"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     optOutCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   optOutCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
}

// requestIsHTTPS reports whether the original client connection was
// TLS, trusting X-Forwarded-Proto because LinkHub is designed to sit
// behind a reverse proxy or tunnel that terminates TLS upstream.
// Mirrors the helper in internal/auth.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// markPrivacyVary tells caches that this response depends on the
// visitor's opt-out signals. Without it a shared cache in front of
// LinkHub could hand a tracked response to someone sending GPC, or
// vice versa. Every page whose body varies with optedOut() must set it.
func markPrivacyVary(w http.ResponseWriter) {
	w.Header().Set("Vary", "Sec-GPC, DNT, Cookie")
}

// ── /.well-known/gpc.json ────────────────────────────────────────

// gpcSupport is the site-level GPC support resource. Publishing it is
// how a site declares, machine-readably, that it acts on the signal.
type gpcSupport struct {
	GPC        bool   `json:"gpc"`
	LastUpdate string `json:"lastUpdate"`
}

// handleGPCWellKnown serves /.well-known/gpc.json. lastUpdate reports
// the privacy notice's effective date when the operator has set one,
// falling back to the date this process started so the field is never
// empty or fabricated.
func (s *Server) handleGPCWellKnown(w http.ResponseWriter, _ *http.Request) {
	cfg := s.opts.Store.Get()
	last := cfg.Privacy.Effective
	if last == "" {
		last = s.startDate
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(gpcSupport{GPC: true, LastUpdate: last})
}

// ── /privacy ─────────────────────────────────────────────────────

// privacyData is the payload for privacy.html.tmpl. It carries the
// resolved facts rather than raw config so the template stays free of
// policy decisions.
type privacyData struct {
	config.Config

	Year int

	// OptedOut is the effective answer for this visitor right now.
	OptedOut bool
	// GPCActive means the browser itself sent the signal, so the
	// on-page control is informational — there is nothing to toggle.
	GPCActive bool
	// DNTActive means the older DNT header was the reason.
	DNTActive bool
	// Saved is set after a successful POST so the page can confirm.
	// "out", "in", or "".
	Saved string

	// SnippetCategory is the effective classification of the head
	// snippet ("none" when there is no snippet at all).
	SnippetCategory string
	// SnippetSuppressed reports that a snippet exists but is being
	// held back right now because the visitor opted out.
	SnippetSuppressed bool
	// SaleOrShare mirrors config.SellsOrShares(). Named differently from
	// the method to avoid a field/method collision on the embedded
	// Config, which the template resolver would have to disambiguate.
	SaleOrShare bool

	// GoogleFontsConfigured is the operator's setting; GoogleFontsActive
	// is whether the third-party request actually happens for this
	// visitor (it does not when they have opted out).
	GoogleFontsConfigured bool
	GoogleFontsActive     bool

	// HasNonEssential reports whether this site loads anything a
	// visitor could meaningfully opt out of. When it's false the notice
	// says so instead of offering a control that does nothing.
	HasNonEssential bool

	// HasContact / HasOperator gate the notice's identifying copy.
	HasContact  bool
	HasOperator bool
	// ContactHref is the resolved href for the contact — a bare email
	// address becomes a mailto: link, a URL is used as-is.
	ContactHref string
}

// contactHref turns an operator-supplied contact into a usable href.
// The validator has already confirmed it's an email, a mailto:, or an
// http(s) URL, so this only has to add the scheme a bare address lacks.
func contactHref(contact string) string {
	c := strings.TrimSpace(contact)
	if c == "" {
		return ""
	}
	lower := strings.ToLower(c)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") {
		return c
	}
	return "mailto:" + c
}

// handlePrivacy renders the privacy notice. The page is generated from
// live config so it describes what the site actually loads rather than
// boilerplate that drifts out of date, and it reflects this visitor's
// current opt-out state.
func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Store.Get()
	out := optedOut(r)

	cat := cfg.EffectiveSnippetCategory()
	suppressible := cfg.SnippetIsSuppressible()
	hasSnippet := cat != config.SnippetNone
	googleConfigured := cfg.Privacy.FontSource != config.FontSourceSystem

	saved := ""
	switch r.URL.Query().Get("saved") {
	case "out":
		saved = "out"
	case "in":
		saved = "in"
	}

	data := privacyData{
		Config:                cfg,
		Year:                  time.Now().Year(),
		OptedOut:              out,
		GPCActive:             gpcHeaderSet(r),
		DNTActive:             dntHeaderSet(r),
		Saved:                 saved,
		SnippetCategory:       cat,
		SnippetSuppressed:     hasSnippet && out && suppressible,
		SaleOrShare:           cfg.SellsOrShares(),
		GoogleFontsConfigured: googleConfigured,
		GoogleFontsActive:     googleConfigured && !out,
		HasNonEssential:       googleConfigured || (hasSnippet && suppressible),
		HasContact:            strings.TrimSpace(cfg.Privacy.Contact) != "",
		HasOperator:           strings.TrimSpace(cfg.Privacy.Operator) != "",
		ContactHref:           contactHref(cfg.Privacy.Contact),
	}

	markPrivacyVary(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	var buf bytes.Buffer
	if err := s.privacyTpl.Execute(&buf, data); err != nil {
		log.Printf("privacy render: %v", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	_, _ = io.Copy(w, &buf)
}

// handlePrivacyChoices records an opt-out choice submitted from the
// notice page and sends the visitor back to it.
//
// This is a plain form POST with no CSRF token, which is a deliberate
// call: the public page carries no JavaScript, the endpoint is
// unauthenticated, and the worst a forged request can do is set or
// clear one visitor's own tracking preference. Adding a token would
// mean adding session state to an otherwise stateless public page for
// no security gain. The cookie is SameSite=Lax regardless.
func (s *Server) handlePrivacyChoices(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return
	}

	// Anything that is not an explicit "allow" is treated as an opt-out.
	// A visitor whose browser sends GPC stays opted out regardless of
	// what this cookie says — optedOut() checks the header first — but
	// we still record the choice so it survives if they later turn the
	// browser signal off.
	out := r.PostForm.Get("choice") != "allow"
	setOptOutCookie(w, r, out)

	dest := "/privacy?saved=in"
	if out {
		dest = "/privacy?saved=out"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
