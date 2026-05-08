package server

// iconPaths is a verbatim port of ICON_PATHS from
// ui_kits/linkhub/Icon.jsx. The values are the inner SVG content for
// each icon — the template wraps them in <svg width=… height=… …>.
//
// The set must stay in sync with internal/config/icons.go (which is
// the validation allowlist). Add a glyph here, add it there too.
var iconPaths = map[string]string{
	// ── Internal: link-row arrow ────────────────────────────────────
	// Not part of the user-facing icon set; rendered by the public
	// template as the trailing diagonal arrow on every link card.
	// Mirrors ARROW_INNER from ui_kits/linkhub/Icon.jsx.
	"arrow": `<line x1="7" y1="17" x2="17" y2="7"/><polyline points="9 7 17 7 17 15"/>`,

	// ── Generic ─────────────────────────────────────────────────────
	"link":          `<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`,
	"external-link": `<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>`,
	"globe":         `<circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>`,
	"mail":          `<path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/>`,
	"phone":         `<path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72 12.84 12.84 0 0 0 .7 2.81 2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45 12.84 12.84 0 0 0 2.81.7A2 2 0 0 1 22 16.92z"/>`,
	"shopping-bag":  `<path d="M6 2 3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4z"/><line x1="3" y1="6" x2="21" y2="6"/><path d="M16 10a4 4 0 0 1-8 0"/>`,
	"shopping-cart": `<circle cx="9" cy="21" r="1"/><circle cx="20" cy="21" r="1"/><path d="M1 1h4l2.68 13.39a2 2 0 0 0 2 1.61h9.72a2 2 0 0 0 2-1.61L23 6H6"/>`,
	"book-open":     `<path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"/><path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"/>`,
	"file-text":     `<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/>`,
	"rss":           `<path d="M4 11a9 9 0 0 1 9 9"/><path d="M4 4a16 16 0 0 1 16 16"/><circle cx="5" cy="19" r="1"/>`,
	"music":         `<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>`,
	"play":          `<polygon points="5 3 19 12 5 21 5 3"/>`,
	"video":         `<polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/>`,
	"heart":         `<path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>`,
	"star":          `<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>`,
	"calendar":      `<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>`,
	"map-pin":       `<path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"/><circle cx="12" cy="10" r="3"/>`,
	"briefcase":     `<rect x="2" y="7" width="20" height="14" rx="2" ry="2"/><path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16"/>`,
	"user":          `<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>`,
	"users":         `<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>`,
	"image":         `<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>`,
	"camera":        `<path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/>`,
	"mic":           `<path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="23"/><line x1="8" y1="23" x2="16" y2="23"/>`,
	"send":          `<line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/>`,
	"download":      `<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>`,
	"code":          `<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>`,
	"qr":            `<rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><line x1="14" y1="14" x2="17" y2="14"/><line x1="14" y1="18" x2="14" y2="21"/><line x1="18" y1="18" x2="21" y2="18"/><line x1="20" y1="14" x2="21" y2="14"/><line x1="20" y1="20" x2="21" y2="21"/>`,
	"coffee":        `<path d="M18 8h1a4 4 0 0 1 0 8h-1"/><path d="M2 8h16v9a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4z"/><line x1="6" y1="1" x2="6" y2="4"/><line x1="10" y1="1" x2="10" y2="4"/><line x1="14" y1="1" x2="14" y2="4"/>`,

	// ── Brand marks (simplified — see Icon.jsx) ────────────────────
	"instagram":    `<rect x="2" y="2" width="20" height="20" rx="5" ry="5"/><path d="M16 11.37A4 4 0 1 1 12.63 8 4 4 0 0 1 16 11.37z"/><line x1="17.5" y1="6.5" x2="17.51" y2="6.5"/>`,
	"twitter":      `<path d="M23 3a10.9 10.9 0 0 1-3.14 1.53 4.48 4.48 0 0 0-7.86 3v1A10.66 10.66 0 0 1 3 4s-4 9 5 13a11.64 11.64 0 0 1-7 2c9 5 20 0 20-11.5a4.5 4.5 0 0 0-.08-.83A7.72 7.72 0 0 0 23 3z"/>`,
	"x":            `<path d="M4 4l16 16M20 4 4 20" stroke-width="2.2"/>`,
	"youtube":      `<path d="M22.54 6.42a2.78 2.78 0 0 0-1.94-2C18.88 4 12 4 12 4s-6.88 0-8.6.46a2.78 2.78 0 0 0-1.94 2A29 29 0 0 0 1 11.75a29 29 0 0 0 .46 5.33A2.78 2.78 0 0 0 3.4 19c1.72.46 8.6.46 8.6.46s6.88 0 8.6-.46a2.78 2.78 0 0 0 1.94-2 29 29 0 0 0 .46-5.25 29 29 0 0 0-.46-5.33z"/><polygon points="9.75 15.02 15.5 11.75 9.75 8.48 9.75 15.02"/>`,
	"facebook":     `<path d="M18 2h-3a5 5 0 0 0-5 5v3H7v4h3v8h4v-8h3l1-4h-4V7a1 1 0 0 1 1-1h3z"/>`,
	"linkedin":     `<path d="M16 8a6 6 0 0 1 6 6v7h-4v-7a2 2 0 0 0-4 0v7h-4v-7a6 6 0 0 1 6-6z"/><rect x="2" y="9" width="4" height="12"/><circle cx="4" cy="4" r="2"/>`,
	"github":       `<path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"/>`,
	"gitlab":       `<path d="M21.94 13.11 12 22 2.06 13.11a1 1 0 0 1-.32-1.11L3.5 6.5l2 6h13l2-6 1.76 5.5a1 1 0 0 1-.32 1.11z"/>`,
	"reddit":       `<circle cx="12" cy="12" r="10"/><circle cx="9" cy="12" r="1"/><circle cx="15" cy="12" r="1"/><path d="M8 15.5c1 1 2.5 1.5 4 1.5s3-.5 4-1.5"/><line x1="12" y1="6" x2="12" y2="9"/><circle cx="12" cy="5" r="1.2"/>`,
	"discord":      `<path d="M19 5a18 18 0 0 0-4-1.4L14.5 5a14 14 0 0 0-5 0L9 3.6A18 18 0 0 0 5 5a17 17 0 0 0-3 13 18 18 0 0 0 5 2l1-2a11 11 0 0 1-2-1l.5-.5a13 13 0 0 0 11 0l.5.5a11 11 0 0 1-2 1l1 2a18 18 0 0 0 5-2 17 17 0 0 0-3-13z"/><circle cx="9" cy="13" r="1.5"/><circle cx="15" cy="13" r="1.5"/>`,
	"mastodon":     `<path d="M21 12.5c0 4-2.5 5-7 5l-2-1c0 2 1 3 3 3l2-.2-.5 1.7c-1 .5-2.5 1-4.5 1-5 0-7-3.5-7-9V8c0-3 2-5 5-5h6c3 0 5 2 5 5z"/><path d="M16 13V9.5a1.5 1.5 0 0 0-3 0V12h-2V9.5a1.5 1.5 0 0 0-3 0V13"/>`,
	"threads":      `<path d="M16.5 11.5C15 10 13 10 12 11s-1 3 0 4 3 1 4 0M8 7c2-2 5-2 7 0s2 5 0 7-1 4 0 5"/><circle cx="12" cy="12" r="10"/>`,
	"bluesky":      `<path d="M6 4c3 1 5 4 6 7 1-3 3-6 6-7 2 0 3 2 3 4 0 3-2 5-3 6 1 1 2 2 2 4s-2 3-3 3-3-1-5-4c-2 3-4 4-5 4s-3-1-3-3 1-3 2-4c-1-1-3-3-3-6 0-2 1-4 3-4z"/>`,
	"tiktok":       `<path d="M9 12a4 4 0 1 0 4 4V4c1 2 3 4 6 4"/>`,
	"twitch":       `<path d="M21 2H3v16h5v4l4-4h5l4-4z"/><line x1="11" y1="11" x2="11" y2="7"/><line x1="16" y1="11" x2="16" y2="7"/>`,
	"spotify":      `<circle cx="12" cy="12" r="10"/><path d="M7 9c4-1 8-1 11 1M7.5 12.5c3-1 7-1 10 1M8 16c2.5-1 5-1 7 0"/>`,
	"soundcloud":   `<path d="M2 14v-2M5 16V8M8 18V6M11 18V4M14 18v-7c0-2 2-4 4-4s4 2 4 4v3a4 4 0 0 1-4 4z"/>`,
	"patreon":      `<circle cx="15" cy="9" r="6"/><line x1="5" y1="3" x2="5" y2="21" stroke-width="3"/>`,
	"kofi":         `<path d="M2 8h16v6a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4z"/><path d="M18 8h2a3 3 0 0 1 0 6h-2"/><path d="M7 5c1-1 2-1 3 0s2 1 3 0"/>`,
	"buymeacoffee": `<path d="M5 8h14l-1 12a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2zM7 8c0-2 1-4 5-4s5 2 5 4"/>`,
	"etsy":         `<path d="M6 4h12v3l-1 1h-7v3h6v3h-6v4h7l1 1v3H6z"/>`,
	"amazon":       `<path d="M3 17c5 4 13 4 18 0M3 20c5 4 13 4 18 0"/><path d="M9 4h6a3 3 0 0 1 0 6h-3v4"/>`,
	"telegram":     `<path d="M21 4 2 11l6 2 2 6 4-4 5 4z"/>`,
	"whatsapp":     `<path d="M21 12a9 9 0 1 1-3.5-7l3.5-1-1 3.5A9 9 0 0 1 21 12z"/><path d="M8 10c0 4 2 6 6 6l1-2-2-1-1 1c-1 0-2-1-2-2l1-1-1-2z"/>`,
	"signal":       `<circle cx="12" cy="12" r="10"/><path d="M8 8 6 6M16 8l2-2M8 16l-2 2M16 16l2 2"/>`,
	"matrix":       `<path d="M3 3v18M21 3v18M5 6v12M19 6v12M9 9c0-1 1-2 2-2s2 1 2 2v6"/>`,
	"rss-feed":     `<path d="M4 11a9 9 0 0 1 9 9"/><path d="M4 4a16 16 0 0 1 16 16"/><circle cx="5" cy="19" r="1"/>`,
}
