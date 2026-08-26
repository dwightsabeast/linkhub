package auth

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Password checking is the only unauthenticated work in LinkHub that
// costs real CPU: bcrypt at cost 12 is ~250ms by design, and verify()
// runs it even for an unknown username so the timing doesn't leak
// which half was wrong. The LXC the installer provisions has one core.
// Without a bound, a few requests per second against /login stop the
// *public* page answering — and nothing slows a password guess either.
//
// loginGuard bounds both, in two layers, because they defend different
// things:
//
//	failures — a per-client counter. maxFailures wrong passwords inside
//	           failureWindow and that client gets 429s until the window
//	           rolls off. This is the brute-force defense.
//	inFlight — a hard ceiling on concurrent bcrypt comparisons, keyed on
//	           nothing at all. This is the CPU defense, and it holds no
//	           matter how many distinct clients are involved.
//
// maxConcurrent is 6 rather than 1 or 2 because basic mode re-verifies
// on every request: opening /admin fires the page plus /api/session,
// /api/account and /api/config, so four concurrent comparisons is
// ordinary legitimate traffic. Six leaves headroom and still bounds the
// worst case to something the 30s WriteTimeout can drain.
const (
	maxFailures   = 5
	failureWindow = 15 * time.Minute
	maxConcurrent = 6
)

// loginGuard is the rate limiter described above. The zero value is not
// usable; build one with newLoginGuard.
type loginGuard struct {
	mu       sync.Mutex
	failures map[string]*failureCount

	// inFlight is a counting semaphore. A token is held only for the
	// duration of one bcrypt comparison.
	inFlight chan struct{}
}

// failureCount tracks one client's recent failures and when the window
// they belong to expires.
type failureCount struct {
	n       int
	expires time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		failures: make(map[string]*failureCount),
		inFlight: make(chan struct{}, maxConcurrent),
	}
}

// blocked reports whether key has spent its attempts, and if so how
// long until the window rolls off. An expired window is dropped as a
// side effect, so the map self-prunes on access.
func (g *loginGuard) blocked(key string) (retryAfter time.Duration, blocked bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	f, ok := g.failures[key]
	if !ok {
		return 0, false
	}
	if time.Now().After(f.expires) {
		delete(g.failures, key)
		return 0, false
	}
	if f.n < maxFailures {
		return 0, false
	}
	return time.Until(f.expires), true
}

// recordFailure counts one failed attempt against key. The window is
// anchored on the first failure, not the most recent one, so a slow
// grinder can't hold itself blocked forever by continuing to try — it
// gets a fresh five attempts once the original window elapses.
func (g *loginGuard) recordFailure(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gcLocked()

	now := time.Now()
	f, ok := g.failures[key]
	if !ok || now.After(f.expires) {
		g.failures[key] = &failureCount{n: 1, expires: now.Add(failureWindow)}
		return
	}
	f.n++
}

// clear forgets a client's failures. Called on a successful sign-in so
// one fat-fingered password doesn't count against the next session.
func (g *loginGuard) clear(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.failures, key)
}

// acquire takes an in-flight token if one is free, reporting false
// immediately rather than queueing. Callers that get false must not run
// bcrypt; they should 429. Blocking instead would just move the
// exhaustion from CPU to held connections.
func (g *loginGuard) acquire() bool {
	select {
	case g.inFlight <- struct{}{}:
		return true
	default:
		return false
	}
}

// release returns an in-flight token. Always pair with a true acquire.
func (g *loginGuard) release() {
	select {
	case <-g.inFlight:
	default:
	}
}

// gcLocked drops expired windows. Called under g.mu from recordFailure,
// which is the only path that grows the map.
func (g *loginGuard) gcLocked() {
	now := time.Now()
	for k, f := range g.failures {
		if now.After(f.expires) {
			delete(g.failures, k)
		}
	}
}

// clientKey identifies the requester for rate-limiting purposes.
//
// LinkHub is designed to sit behind a reverse proxy or tunnel, so
// RemoteAddr is usually the proxy's address and keying on it would make
// the per-client counter global — one attacker would lock the operator
// out of their own admin. We take the leftmost X-Forwarded-For entry
// when there is one: the client as the proxy saw it. (The same
// trust-the-proxy assumption isHTTPS already makes for
// X-Forwarded-Proto.)
//
// That header is client-settable, so an attacker can rotate it and
// sidestep the counter. That is exactly why the concurrency ceiling
// sits above it and is keyed on nothing: the counter stops an ordinary
// password guesser, and the ceiling stops the CPU exhaustion that
// spoofing would otherwise buy.
func clientKey(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := xff
		if i := strings.IndexByte(first, ','); i >= 0 {
			first = first[:i]
		}
		if v := strings.TrimSpace(first); v != "" {
			return v
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// retryMessage renders a wait as something a person can act on. Always
// rounds up: telling someone to wait "0 minutes" is worse than useless.
func retryMessage(d time.Duration) string {
	mins := int((d + time.Minute - 1) / time.Minute)
	if mins <= 1 {
		return "Too many sign-in attempts. Try again in a minute."
	}
	return "Too many sign-in attempts. Try again in " +
		strconv.Itoa(mins) + " minutes."
}

// retryAfterSeconds renders a wait for the Retry-After header, which
// takes whole seconds and must be at least 1.
func retryAfterSeconds(d time.Duration) string {
	secs := int(d.Seconds()) + 1
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
