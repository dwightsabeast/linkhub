package config

// validIcons is the set of icon names accepted in Link.Icon and
// Social.Platform. It mirrors the keys of ICON_PATHS in
// ui_kits/linkhub/Icon.jsx — keep them in sync when adding glyphs.
//
// The README states "51 total" (28 generic + 23 brand marks). The
// actual Icon.jsx ships 28 generic + 26 brand marks + 1 alias for
// 55 entries. We mirror Icon.jsx, not the README — Icon.jsx is what
// renders, so it's the source of truth. README should be reconciled.
var validIcons = map[string]struct{}{
	// Generic (28).
	"link": {}, "external-link": {}, "globe": {}, "mail": {}, "phone": {},
	"shopping-bag": {}, "shopping-cart": {}, "book-open": {}, "file-text": {},
	"rss": {}, "music": {}, "play": {}, "video": {}, "heart": {}, "star": {},
	"calendar": {}, "map-pin": {}, "briefcase": {}, "user": {}, "users": {},
	"image": {}, "camera": {}, "mic": {}, "send": {}, "download": {},
	"code": {}, "qr": {}, "coffee": {},

	// Brand marks (26, intentionally simplified — see Icon.jsx).
	"instagram": {}, "twitter": {}, "x": {}, "youtube": {}, "facebook": {},
	"linkedin": {}, "github": {}, "gitlab": {}, "reddit": {}, "discord": {},
	"mastodon": {}, "threads": {}, "bluesky": {}, "tiktok": {}, "twitch": {},
	"spotify": {}, "soundcloud": {}, "patreon": {}, "kofi": {},
	"buymeacoffee": {}, "etsy": {}, "amazon": {}, "telegram": {},
	"whatsapp": {}, "signal": {}, "matrix": {},

	// Aliases.
	"rss-feed": {},
}

// ValidIcon reports whether name is in the bundled icon set.
func ValidIcon(name string) bool {
	_, ok := validIcons[name]
	return ok
}
