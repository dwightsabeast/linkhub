package server

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dwightsabeast/linkhub/internal/config"
)

// The icon set is maintained by hand in three places: the validation
// allowlist (internal/config/icons.go), the SVG paths that actually
// render (internal/server/icons.go), and ICON_NAMES in the admin UI
// (web/admin/admin.js). Nothing links them, and the comments in the Go
// files name their source of truth as ui_kits/linkhub/Icon.jsx — which
// lives in a different repository entirely.
//
// The failure mode is silent in both directions: a name in the
// allowlist with no path renders an empty <svg> and logs nothing, and a
// name the admin offers but the validator rejects fails the operator's
// save with "unknown icon name" for a glyph the UI just showed them.
//
// internalIcons are rendered by the templates directly rather than
// being selectable, so they belong in the path map and are expected to
// be absent from the other two lists.
var internalIcons = map[string]bool{
	"arrow":           true,
	"privacy-choices": true,
}

func TestEveryValidIconHasAPath(t *testing.T) {
	for _, name := range config.IconNames() {
		if _, ok := iconPaths[name]; !ok {
			t.Errorf("icon %q is accepted by config.ValidIcon but has no path in iconPaths; "+
				"a link using it would render an empty <svg>", name)
		}
	}
}

func TestEveryPathIsAValidIcon(t *testing.T) {
	for name := range iconPaths {
		if internalIcons[name] {
			continue
		}
		if !config.ValidIcon(name) {
			t.Errorf("iconPaths has %q but config.ValidIcon rejects it; "+
				"either add it to the allowlist or to internalIcons here", name)
		}
	}
}

func TestIconSVGRendersWellFormedMarkup(t *testing.T) {
	for _, name := range config.IconNames() {
		svg := string(iconSVG(name))
		if svg == "" {
			t.Errorf("iconSVG(%q) returned empty", name)
			continue
		}
		if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
			t.Errorf("iconSVG(%q) is not a well-formed element: %.60s", name, svg)
		}
	}
}

func TestIconSVGIsEmptyForUnknownName(t *testing.T) {
	if got := iconSVG("definitely-not-an-icon"); got != "" {
		t.Errorf("iconSVG on an unknown name returned %q, want empty", got)
	}
}

var (
	// adminIconRe pulls the ICON_NAMES array out of admin.js. The admin
	// ships as plain JS with no build step, so reading the source is the
	// only way to reach the list — and a mismatch is a bug the operator
	// sees, so it earns the slight fragility.
	adminIconRe = regexp.MustCompile(`(?s)const ICON_NAMES\s*=\s*\[(.*?)\]`)

	// lineCommentRe strips the // group headings the array is organized
	// with. They sit on their own lines ahead of the names, so splitting
	// on commas first would swallow the following name along with them.
	lineCommentRe = regexp.MustCompile(`(?m)//.*$`)
)

func TestAdminIconListMatchesServer(t *testing.T) {
	const path = "../../web/admin/admin.js"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("admin.js not readable (%v); skipping cross-check", err)
	}

	m := adminIconRe.FindSubmatch(src)
	if m == nil {
		t.Fatalf("could not find ICON_NAMES in %s — if the declaration was "+
			"renamed, update adminIconRe", path)
	}

	admin := map[string]bool{}
	for _, raw := range strings.Split(lineCommentRe.ReplaceAllString(string(m[1]), ""), ",") {
		if name := strings.Trim(strings.TrimSpace(raw), `"'`); name != "" {
			admin[name] = true
		}
	}
	if len(admin) == 0 {
		t.Fatal("parsed zero names out of ICON_NAMES")
	}

	for _, name := range config.IconNames() {
		if !admin[name] {
			t.Errorf("%q is a valid icon but the admin never offers it", name)
		}
	}
	for name := range admin {
		if !config.ValidIcon(name) {
			t.Errorf("the admin offers %q but the server rejects it on save", name)
		}
	}
}
