/* ============================================================
   admin.js — LinkHub config builder

   Vanilla JS, no framework, no build step. Loads on the admin
   shell (admin.html) and is responsible for:

     - fetching /api/config and rendering form state
     - binding inputs back to a working copy of the config
     - rendering / reordering / removing primary links and socials
     - applying voice presets (with a confirm modal)
     - uploading avatars to /api/avatar
     - tracking "dirty" state and surfacing it in the status pill
     - explicit save via PUT /api/config
     - copy / download / reload-from-server actions
     - theme toggle persisted to localStorage

   Server-side validation is authoritative. We do light client-side
   sanity checks (featured-link mutex, hex color shape) but rely on
   the server's *config.ValidationError to surface the canonical
   field path on save failure.
   ============================================================ */

(() => {
  "use strict";

  /* ── Constants ─────────────────────────────────────────────── */

  // Mirrors internal/config/icons.go. Keep in sync when adding
  // glyphs server-side. Hardcoded here rather than fetched from
  // /api/icons because (a) the server doesn't ship that endpoint
  // and (b) the set is stable enough that a release-time mirror is
  // fine. The server is still authoritative — invalid names are
  // rejected by Validate().
  const ICON_NAMES = [
    // Generic
    "link", "external-link", "globe", "mail", "phone",
    "shopping-bag", "shopping-cart", "book-open", "file-text",
    "rss", "music", "play", "video", "heart", "star",
    "calendar", "map-pin", "briefcase", "user", "users",
    "image", "camera", "mic", "send", "download",
    "code", "qr", "coffee",
    // Brand marks
    "instagram", "twitter", "x", "youtube", "facebook",
    "linkedin", "github", "gitlab", "reddit", "discord",
    "mastodon", "threads", "bluesky", "tiktok", "twitch",
    "spotify", "soundcloud", "patreon", "kofi",
    "buymeacoffee", "etsy", "amazon", "telegram",
    "whatsapp", "signal", "matrix",
    // Aliases
    "rss-feed",
  ];

  const THEME_KEY = "linkhub-admin-theme";
  const STATUS_CLEAR_MS = 2400;

  // Avatar upload — must mirror handleAvatar's allowedAvatarTypes.
  const AVATAR_ACCEPT = ["image/png", "image/jpeg", "image/webp", "image/svg+xml"];
  const AVATAR_MAX_BYTES = 2 * 1024 * 1024;
  // Favicon upload — must mirror handleFavicon's allowedFaviconTypes.
  const FAVICON_ACCEPT = ["image/png", "image/svg+xml", "image/x-icon", "image/vnd.microsoft.icon"];
  const FAVICON_MAX_BYTES = 512 * 1024;

  /* ── Voice presets ─────────────────────────────────────────────
     Each preset replaces profile / links / social / theme.accent
     when applied. meta and footer are intentionally preserved —
     those are site-identity, not voice. The modal copy in
     admin.html ("will overwrite the profile, links, social row,
     and accent") is the contract.

     Five flavors covering common shapes a single binary might
     wear: solo creator, indie media co (the immediate use case),
     small business, dev/portfolio, and a stripped-down "links
     only" baseline for when the preset itself is a starting
     point and everything will be edited.
     ──────────────────────────────────────────────────────────── */
  const PRESETS = [
    {
      id: "media-co",
      name: "Media company",
      description: "Show, newsletter, store",
      accent: "#3D5A4C",
      profile: {
        name: "Your Studio",
        tagline: "Independent media",
        bio: "Stories, essays, and a weekly newsletter from a one-person studio.",
        location: "On the internet",
      },
      links: [
        { label: "Latest episode", url: "https://example.com/latest", icon: "play",
          description: "Listen wherever you get podcasts", featured: true },
        { label: "Newsletter", url: "https://example.com/newsletter", icon: "mail",
          description: "Sundays, ~5 minutes" },
        { label: "Archive", url: "https://example.com/archive", icon: "book-open" },
        { label: "Shop", url: "https://example.com/shop", icon: "shopping-bag" },
      ],
      social: [
        { platform: "youtube",   url: "https://youtube.com/@you" },
        { platform: "spotify",   url: "https://open.spotify.com/show/you" },
        { platform: "instagram", url: "https://instagram.com/you" },
        { platform: "rss",       url: "https://example.com/feed.xml" },
      ],
    },
    {
      id: "creator",
      name: "Solo creator",
      description: "Personal hub, social-first",
      accent: "#A05A2C",
      profile: {
        name: "Your Name",
        tagline: "Writer · Photographer",
        bio: "Notes from the field, occasional essays, and the work I'm currently making.",
        location: "Somewhere",
      },
      links: [
        { label: "Portfolio", url: "https://example.com", icon: "image", featured: true,
          description: "Selected work" },
        { label: "Substack", url: "https://example.substack.com", icon: "file-text" },
        { label: "Tip jar", url: "https://buymeacoffee.com/you", icon: "coffee" },
      ],
      social: [
        { platform: "instagram", url: "https://instagram.com/you" },
        { platform: "threads",   url: "https://threads.net/@you" },
        { platform: "bluesky",   url: "https://bsky.app/profile/you.bsky.social" },
      ],
    },
    {
      id: "business",
      name: "Small business",
      description: "Storefront, contact, hours",
      accent: "#5A3D7A",
      profile: {
        name: "Your Business",
        tagline: "Open weekdays",
        bio: "What we do, where to find us, and how to get in touch.",
        location: "City, ST",
      },
      links: [
        { label: "Shop online", url: "https://example.com/shop", icon: "shopping-cart",
          featured: true, description: "Free shipping over $50" },
        { label: "Book an appointment", url: "https://example.com/book", icon: "calendar" },
        { label: "Visit us", url: "https://maps.example.com", icon: "map-pin",
          description: "123 Main St" },
        { label: "Contact", url: "mailto:hello@example.com", icon: "mail" },
      ],
      social: [
        { platform: "instagram", url: "https://instagram.com/you" },
        { platform: "facebook",  url: "https://facebook.com/you" },
      ],
    },
    {
      id: "developer",
      name: "Developer",
      description: "Code, writing, contact",
      accent: "#2C5A8A",
      profile: {
        name: "Your Name",
        tagline: "Software engineer",
        bio: "I build small, sharp tools. Currently at $COMPANY.",
        location: "Remote",
      },
      links: [
        { label: "GitHub", url: "https://github.com/you", icon: "github",
          description: "Open source", featured: true },
        { label: "Blog", url: "https://example.com/blog", icon: "file-text" },
        { label: "Resume", url: "https://example.com/resume.pdf", icon: "download" },
        { label: "Email", url: "mailto:hello@example.com", icon: "mail" },
      ],
      social: [
        { platform: "github",   url: "https://github.com/you" },
        { platform: "linkedin", url: "https://linkedin.com/in/you" },
        { platform: "x",        url: "https://x.com/you" },
      ],
    },
    {
      id: "minimal",
      name: "Minimal",
      description: "Bare scaffold to edit",
      accent: "#3D5A4C",
      profile: {
        name: "Your Name",
        tagline: "",
        bio: "",
        location: "",
      },
      links: [
        { label: "Website", url: "https://example.com", icon: "globe" },
      ],
      social: [],
    },
  ];

  /* ── State ─────────────────────────────────────────────────────
     `cfg` is the working copy bound to the form. `saved` is the
     snapshot of what's on disk — diff against it for dirty state.
     Both are full Config objects matching internal/config/Config.
     ──────────────────────────────────────────────────────────── */
  const state = {
    cfg: null,
    saved: null,
    pendingPresetId: null, // set when modal is open
    statusTimer: null,
  };

  /* ── DOM refs (resolved after DOMContentLoaded) ─────────────── */
  let dom = {};

  /* ── Boot ──────────────────────────────────────────────────── */

  document.addEventListener("DOMContentLoaded", () => {
    resolveDom();
    initTheme();
    populateIconDatalist();
    renderPresetButtons();
    bindGlobalControls();
    loadConfig().catch((err) => {
      console.error(err);
      setStatus("error loading config", "is-error", { sticky: true });
    });

    // Catch unsaved-changes navigation. Only fires after the user
    // has interacted with the page (browsers gate beforeunload on
    // user gesture), but that's fine — that's also when "dirty" can
    // be true.
    window.addEventListener("beforeunload", (e) => {
      if (isDirty()) {
        e.preventDefault();
        // Modern browsers ignore the message text, but setting
        // returnValue is still required to trigger the dialog.
        e.returnValue = "";
      }
    });
  });

  function resolveDom() {
    dom = {
      // Top chrome
      themeToggle: document.getElementById("theme-toggle"),
      status: document.getElementById("admin-status"),

      // Profile
      profileName:     document.getElementById("profile-name"),
      profileTagline:  document.getElementById("profile-tagline"),
      profileBio:      document.getElementById("profile-bio"),
      profileLocation: document.getElementById("profile-location"),

      // Avatar
      avatarPreview:   document.getElementById("avatar-preview"),
      avatarFile:      document.getElementById("avatar-file"),
      avatarUploadBtn: document.getElementById("avatar-upload-btn"),
      avatarResetBtn:  document.getElementById("avatar-reset-btn"),
      avatarSize:      document.getElementById("avatar-size"),
      avatarShape:     document.getElementById("avatar-shape"),

      // Favicon
      faviconPreview:   document.getElementById("favicon-preview"),
      faviconFile:      document.getElementById("favicon-file"),
      faviconUploadBtn: document.getElementById("favicon-upload-btn"),
      faviconResetBtn:  document.getElementById("favicon-reset-btn"),

      // Meta + theme
      metaTitle:       document.getElementById("meta-title"),
      metaDescription: document.getElementById("meta-description"),
      accent:          document.getElementById("theme-accent"),
      accentColor:     document.getElementById("theme-accent-color"),
      accentDark:      document.getElementById("theme-accent-dark"),
      accentDarkColor: document.getElementById("theme-accent-dark-color"),

      // Repeated rows
      linkList:    document.getElementById("link-list"),
      socialList:  document.getElementById("social-list"),
      addLinkBtn:  document.getElementById("add-link-btn"),
      addSocialBtn: document.getElementById("add-social-btn"),

      // Footer
      footerShowYear: document.getElementById("footer-show-year"),
      footerText:     document.getElementById("footer-text"),

      // Generated output + actions
      configOutput:   document.getElementById("config-output"),
      saveBtn:        document.getElementById("save-btn"),
      copyBtn:        document.getElementById("copy-btn"),
      downloadBtn:    document.getElementById("download-btn"),
      reloadBtn:      document.getElementById("reload-btn"),

      // Datalist + presets + modal + toast + templates
      iconOptions:      document.getElementById("icon-options"),
      presetRow:        document.getElementById("preset-row"),
      presetModal:      document.getElementById("preset-modal"),
      presetModalName:  document.getElementById("preset-modal-name"),
      presetConfirmBtn: document.getElementById("preset-confirm-btn"),
      presetCancelBtn:  document.getElementById("preset-cancel-btn"),
      toast:            document.getElementById("toast"),

      tmplLinkRow:     document.getElementById("tmpl-link-row"),
      tmplSocialRow:   document.getElementById("tmpl-social-row"),
      tmplPresetButton: document.getElementById("tmpl-preset-button"),
    };
  }

  /* ── Theme ─────────────────────────────────────────────────── */

  function initTheme() {
    const stored = localStorage.getItem(THEME_KEY);
    const theme = stored === "dark" || stored === "light" ? stored : "light";
    setTheme(theme);
    dom.themeToggle.addEventListener("click", () => {
      const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
      setTheme(next);
      localStorage.setItem(THEME_KEY, next);
    });
  }

  function setTheme(theme) {
    document.documentElement.dataset.theme = theme;
    // Swap the icon glyph: moon for light (click → go dark), sun
    // for dark (click → go light). Keep a11y label in sync too.
    const dark = theme === "dark";
    dom.themeToggle.setAttribute(
      "aria-label",
      dark ? "Switch to light theme" : "Switch to dark theme"
    );
    dom.themeToggle.innerHTML = dark
      ? `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor"
              stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
           <circle cx="12" cy="12" r="4"/>
           <line x1="12" y1="2" x2="12" y2="5"/>
           <line x1="12" y1="19" x2="12" y2="22"/>
           <line x1="2" y1="12" x2="5" y2="12"/>
           <line x1="19" y1="12" x2="22" y2="12"/>
           <line x1="4.9" y1="4.9" x2="7" y2="7"/>
           <line x1="17" y1="17" x2="19.1" y2="19.1"/>
           <line x1="4.9" y1="19.1" x2="7" y2="17"/>
           <line x1="17" y1="7" x2="19.1" y2="4.9"/>
         </svg>`
      : `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor"
              stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
           <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>
         </svg>`;
  }

  /* ── Icon datalist ─────────────────────────────────────────── */

  function populateIconDatalist() {
    const frag = document.createDocumentFragment();
    for (const name of ICON_NAMES) {
      const opt = document.createElement("option");
      opt.value = name;
      frag.appendChild(opt);
    }
    dom.iconOptions.appendChild(frag);
  }

  /* ── Presets ───────────────────────────────────────────────── */

  function renderPresetButtons() {
    const frag = document.createDocumentFragment();
    for (const p of PRESETS) {
      const btn = dom.tmplPresetButton.content.firstElementChild.cloneNode(true);
      btn.dataset.presetId = p.id;
      btn.querySelector(".admin-preset-swatch").style.background = p.accent;
      btn.querySelector(".admin-preset-name").textContent = p.name;
      btn.querySelector(".admin-preset-desc").textContent = p.description;
      btn.addEventListener("click", () => requestPresetSwitch(p.id));
      frag.appendChild(btn);
    }
    dom.presetRow.appendChild(frag);
  }

  function requestPresetSwitch(id) {
    const preset = PRESETS.find((p) => p.id === id);
    if (!preset) return;

    // Always confirm. Even if the form looks "clean," users may have
    // edited and reloaded — better one extra click than a silent
    // wipeout.
    state.pendingPresetId = id;
    dom.presetModalName.textContent = preset.name;
    dom.presetModal.hidden = false;
  }

  function applyPendingPreset() {
    const preset = PRESETS.find((p) => p.id === state.pendingPresetId);
    state.pendingPresetId = null;
    dom.presetModal.hidden = true;
    if (!preset) return;

    // Replace profile / links / social / accent. Preserve meta,
    // footer, theme.mode, theme.accentDark, profile.avatar.
    state.cfg.profile = {
      ...state.cfg.profile,
      name:     preset.profile.name,
      tagline:  preset.profile.tagline,
      bio:      preset.profile.bio,
      location: preset.profile.location,
      // avatar intentionally preserved
    };
    state.cfg.theme.accent = preset.accent;
    state.cfg.links  = deepClone(preset.links);
    state.cfg.social = deepClone(preset.social);

    renderAll();
    markActivePreset(preset.id);
    setStatus(`loaded "${preset.name}"`, "is-saved");
  }

  function markActivePreset(id) {
    for (const btn of dom.presetRow.querySelectorAll("[data-preset-id]")) {
      btn.classList.toggle("is-active", btn.dataset.presetId === id);
    }
  }

  /* ── Global controls (top buttons, modal, toast) ──────────── */

  function bindGlobalControls() {
    // Save / copy / download / reload
    dom.saveBtn.addEventListener("click", () => {
      saveConfig().catch((err) => {
        console.error(err);
        setStatus("save failed", "is-error", { sticky: true });
      });
    });
    dom.copyBtn.addEventListener("click", copyConfigJSON);
    dom.downloadBtn.addEventListener("click", downloadConfigJSON);
    dom.reloadBtn.addEventListener("click", () => {
      if (isDirty() && !confirm("Discard unsaved changes and reload from server?")) return;
      loadConfig().catch((err) => {
        console.error(err);
        setStatus("reload failed", "is-error", { sticky: true });
      });
    });

    // Favicon
    dom.faviconUploadBtn.addEventListener("click", () => dom.faviconFile.click());
    dom.faviconFile.addEventListener("change", handleFaviconSelect);
    dom.faviconResetBtn.addEventListener("click", handleFaviconReset);

    // Repeated-row add buttons
    dom.addLinkBtn.addEventListener("click", () => {
      state.cfg.links.push(blankLink());
      renderLinks();
      onFormChange();
    });
    dom.addSocialBtn.addEventListener("click", () => {
      state.cfg.social.push(blankSocial());
      renderSocials();
      onFormChange();
    });

    // Avatar
    dom.avatarUploadBtn.addEventListener("click", () => dom.avatarFile.click());
    dom.avatarFile.addEventListener("change", handleAvatarSelect);
    dom.avatarResetBtn.addEventListener("click", handleAvatarReset);

    // Preset modal
    dom.presetConfirmBtn.addEventListener("click", applyPendingPreset);
    dom.presetCancelBtn.addEventListener("click", () => {
      state.pendingPresetId = null;
      dom.presetModal.hidden = true;
    });
    // Click backdrop to dismiss. Don't dismiss when clicking inside
    // the modal body itself.
    dom.presetModal.addEventListener("click", (e) => {
      if (e.target === dom.presetModal) {
        state.pendingPresetId = null;
        dom.presetModal.hidden = true;
      }
    });
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && !dom.presetModal.hidden) {
        state.pendingPresetId = null;
        dom.presetModal.hidden = true;
      }
    });

    // Direct top-level field bindings (non-repeated). The data-bind
    // attribute is used by the HTML; we resolve it once here.
    bindFlatField(dom.profileName,     "profile.name");
    bindFlatField(dom.profileTagline,  "profile.tagline");
    bindFlatField(dom.profileBio,      "profile.bio");
    bindFlatField(dom.profileLocation, "profile.location");
    dom.avatarSize.addEventListener("change", () => {
      state.cfg.profile.avatarSize = Number(dom.avatarSize.value);
      onFormChange();
    });
    dom.avatarShape.addEventListener("input", () => {
      const val = Number(dom.avatarShape.value);
      state.cfg.profile.avatarShape = val;
      updateAvatarPreviewShape(val);
      onFormChange();
    });
    bindFlatField(dom.metaTitle,        "meta.title");
    bindFlatField(dom.metaDescription,  "meta.description");
    bindFlatField(dom.footerText,       "footer.text");
    bindFlatField(dom.footerShowYear,   "footer.showYear");

    // Accent: text + color inputs are mirrored. Editing one updates
    // the other, both write to state.theme.accent.
    bindAccentPair(dom.accent,     dom.accentColor,     "accent");
    bindAccentPair(dom.accentDark, dom.accentDarkColor, "accentDark");
  }

  function bindFlatField(el, path) {
    const isCheckbox = el.type === "checkbox";
    el.addEventListener("input", () => {
      setByPath(state.cfg, path, isCheckbox ? el.checked : el.value);
      onFormChange();
    });
    if (isCheckbox) {
      // Some browsers fire 'change' but not 'input' for checkboxes.
      el.addEventListener("change", () => {
        setByPath(state.cfg, path, el.checked);
        onFormChange();
      });
    }
  }

  function bindAccentPair(textEl, colorEl, key) {
    const update = (val) => {
      // Normalize "#rgb" → "#rrggbb" for the color input, which
      // requires 7 chars. Tolerant on input, strict on storage.
      const norm = normalizeHex(val);
      state.cfg.theme[key] = norm;
      // Keep both inputs in sync without re-firing events.
      if (textEl.value !== norm) textEl.value = norm;
      if (colorEl.value !== norm) colorEl.value = norm;
      onFormChange();
    };
    textEl.addEventListener("input", () => update(textEl.value));
    colorEl.addEventListener("input", () => update(colorEl.value));
  }

  function normalizeHex(v) {
    if (!v) return "#000000";
    let s = v.trim().toLowerCase();
    if (s[0] !== "#") s = "#" + s;
    // #rgb → #rrggbb
    if (/^#[0-9a-f]{3}$/.test(s)) {
      s = "#" + s[1] + s[1] + s[2] + s[2] + s[3] + s[3];
    }
    // Anything else — return as-is and let server validation catch.
    return s;
  }

  /* ── Load / save ───────────────────────────────────────────── */

  async function loadConfig() {
    setStatus("loading…", "is-saving");
    const res = await fetch("/api/config", { headers: { "Accept": "application/json" } });
    if (!res.ok) {
      throw new Error(`GET /api/config: ${res.status}`);
    }
    const cfg = await res.json();
    state.cfg = normalizeConfig(cfg);
    state.saved = deepClone(state.cfg);
    renderAll();
    setStatus("loaded", "is-saved");
  }

  async function saveConfig() {
    setStatus("saving…", "is-saving");
    const res = await fetch("/api/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(state.cfg),
    });

    if (res.ok) {
      state.saved = deepClone(state.cfg);
      setStatus("saved", "is-saved");
      return;
    }

    // 400 with JSON body is a ValidationError from the server.
    // Other statuses are plain text.
    if (res.status === 400) {
      let verr = null;
      try { verr = await res.json(); } catch (_) { /* fall through */ }
      if (verr && verr.field && verr.message) {
        flagInvalidField(verr.field);
        setStatus(`${verr.field}: ${verr.message}`, "is-error", { sticky: true });
        toast(`fix "${verr.field}" and try again`);
        return;
      }
    }
    const text = await res.text().catch(() => "");
    throw new Error(`PUT /api/config: ${res.status} ${text}`);
  }

  // flagInvalidField: scroll to and focus the input matching the
  // dotted path returned by the server. Best-effort — paths like
  // "links[2].label" map to the second link card's label input.
  function flagInvalidField(path) {
    // profile.name | meta.title | footer.text | theme.accent
    const flat = {
      "profile.name":      dom.profileName,
      "profile.tagline":   dom.profileTagline,
      "profile.bio":       dom.profileBio,
      "profile.location":  dom.profileLocation,
      "meta.title":        dom.metaTitle,
      "meta.description":  dom.metaDescription,
      "footer.text":       dom.footerText,
      "theme.accent":      dom.accent,
      "theme.accentDark":  dom.accentDark,
    };
    if (flat[path]) {
      flat[path].focus();
      flat[path].scrollIntoView({ behavior: "smooth", block: "center" });
      return;
    }
    // links[N].field or social[N].field
    const m = path.match(/^(links|social)\[(\d+)\]\.(\w+)$/);
    if (m) {
      const [, kind, idx, field] = m;
      const list = kind === "links" ? dom.linkList : dom.socialList;
      const row = list.children[Number(idx)];
      if (!row) return;
      const input = row.querySelector(`[data-field="${field}"]`);
      if (input) {
        input.focus();
        input.scrollIntoView({ behavior: "smooth", block: "center" });
      }
    }
  }

  /* ── Avatar upload ─────────────────────────────────────────── */

  async function handleAvatarSelect() {
    const file = dom.avatarFile.files && dom.avatarFile.files[0];
    if (!file) return;

    if (!AVATAR_ACCEPT.includes(file.type)) {
      toast("avatar must be PNG, JPEG, WebP, or SVG");
      dom.avatarFile.value = "";
      return;
    }
    if (file.size > AVATAR_MAX_BYTES) {
      toast(`avatar too large (max 2 MB; this is ${formatBytes(file.size)})`);
      dom.avatarFile.value = "";
      return;
    }

    setStatus("uploading avatar…", "is-saving");
    try {
      const res = await fetch("/api/avatar", {
        method: "POST",
        headers: { "Content-Type": file.type },
        body: file,
      });
      if (!res.ok) {
        let msg = `upload failed (${res.status})`;
        try {
          const body = await res.json();
          if (body && body.error) msg = body.error;
        } catch (_) { /* fall through */ }
        throw new Error(msg);
      }
      const body = await res.json();
      // Server is authoritative — it picked the URL (with cache-bust)
      // and already wrote it into config. Mirror it locally and
      // bump the saved snapshot's avatar so this isn't a "dirty"
      // edit; it's already on disk.
      state.cfg.profile.avatar = body.avatar;
      if (state.saved) state.saved.profile.avatar = body.avatar;
      dom.avatarPreview.src = body.avatar;
      renderConfigOutput(); // avatar field changed
      setStatus("avatar uploaded", "is-saved");
    } catch (err) {
      console.error(err);
      setStatus(err.message || "avatar upload failed", "is-error", { sticky: true });
    } finally {
      // Reset the file input so re-selecting the same file fires change.
      dom.avatarFile.value = "";
    }
  }

  function handleAvatarReset() {
    // Local-only: point the config at the bundled default. Doesn't
    // delete previously-uploaded files on disk; that's fine —
    // they're harmless leftovers in /var/lib/linkhub/assets/ and a
    // future upload of the same format will overwrite.
    state.cfg.profile.avatar = "/static/assets/avatar.svg";
    dom.avatarPreview.src = state.cfg.profile.avatar;
    renderConfigOutput();
    onFormChange();
  }

/* ── Favicon upload ────────────────────────────────────────── */

  async function handleFaviconSelect() {
    const file = dom.faviconFile.files && dom.faviconFile.files[0];
    if (!file) return;

    if (!FAVICON_ACCEPT.includes(file.type)) {
      toast("favicon must be PNG, SVG, or ICO");
      dom.faviconFile.value = "";
      return;
    }
    if (file.size > FAVICON_MAX_BYTES) {
      toast(`favicon too large (max 512 KB; this is ${formatBytes(file.size)})`);
      dom.faviconFile.value = "";
      return;
    }

    setStatus("uploading favicon…", "is-saving");
    try {
      const res = await fetch("/api/favicon", {
        method: "POST",
        headers: { "Content-Type": file.type },
        body: file,
      });
      if (!res.ok) {
        let msg = `upload failed (${res.status})`;
        try {
          const body = await res.json();
          if (body && body.error) msg = body.error;
        } catch (_) { /* fall through */ }
        throw new Error(msg);
      }
      const body = await res.json();
      state.cfg.meta.favicon = body.favicon;
      if (state.saved) state.saved.meta.favicon = body.favicon;
      dom.faviconPreview.src = body.favicon;
      renderConfigOutput();
      setStatus("favicon uploaded", "is-saved");
    } catch (err) {
      console.error(err);
      setStatus(err.message || "favicon upload failed", "is-error", { sticky: true });
    } finally {
      dom.faviconFile.value = "";
    }
  }

  function handleFaviconReset() {
    state.cfg.meta.favicon = "/static/favicon.svg";
    dom.faviconPreview.src = state.cfg.meta.favicon;
    renderConfigOutput();
    onFormChange();
  }

  /* ── Render: full + per-section ────────────────────────────── */

  function renderAll() {
    renderProfile();
    renderAvatar();
    renderMetaAndTheme();
    renderFavicon();
    renderLinks();
    renderSocials();
    renderFooter();
    renderConfigOutput();
    updateDirtyIndicator();
  }

  function renderProfile() {
    dom.profileName.value     = state.cfg.profile.name     || "";
    dom.profileTagline.value  = state.cfg.profile.tagline  || "";
    dom.profileBio.value      = state.cfg.profile.bio      || "";
    dom.profileLocation.value = state.cfg.profile.location || "";
    dom.avatarSize.value = String(state.cfg.profile.avatarSize || 96);
    dom.avatarShape.value = String(state.cfg.profile.avatarShape || 50);
    updateAvatarPreviewShape(state.cfg.profile.avatarShape || 50);
  }

  function renderAvatar() {
    const a = state.cfg.profile.avatar || "/static/assets/avatar.svg";
    dom.avatarPreview.src = a;
  }

  function renderFavicon() {
    const f = state.cfg.meta.favicon || "/static/favicon.svg";
    dom.faviconPreview.src = f;
  }

  function renderMetaAndTheme() {
    dom.metaTitle.value       = state.cfg.meta.title       || "";
    dom.metaDescription.value = state.cfg.meta.description || "";

    const a = normalizeHex(state.cfg.theme.accent || "#3D5A4C");
    const ad = normalizeHex(state.cfg.theme.accentDark || "#8FB3A4");
    dom.accent.value = a;          dom.accentColor.value = a;
    dom.accentDark.value = ad;     dom.accentDarkColor.value = ad;
  }

  function renderFooter() {
    dom.footerShowYear.checked = !!state.cfg.footer.showYear;
    dom.footerText.value = state.cfg.footer.text || "";
  }

  /* ── Render: primary links ─────────────────────────────────── */

  function renderLinks() {
    dom.linkList.innerHTML = "";
    const frag = document.createDocumentFragment();
    state.cfg.links.forEach((link, idx) => {
      frag.appendChild(buildLinkRow(link, idx));
    });
    dom.linkList.appendChild(frag);
  }

  function buildLinkRow(link, idx) {
    const node = dom.tmplLinkRow.content.firstElementChild.cloneNode(true);
    node.dataset.idx = String(idx);
    const ttl = node.querySelector(".admin-item-ttl");
    ttl.textContent = link.label || `Link ${idx + 1}`;

    const fields = ["label", "url", "description", "icon", "featured"];
    for (const f of fields) {
      const input = node.querySelector(`[data-field="${f}"]`);
      if (!input) continue;
      if (f === "featured") {
        input.checked = !!link.featured;
        input.addEventListener("change", () => {
          // One-featured mutex: when this gets checked, uncheck all
          // others. Server enforces this too, but the UX should
          // never let a user submit a state the server will reject.
          if (input.checked) {
            for (const other of state.cfg.links) other.featured = false;
            link.featured = true;
            renderLinks(); // re-render to clear other checkboxes
          } else {
            link.featured = false;
          }
          onFormChange();
        });
      } else {
        input.value = link[f] || "";
        input.addEventListener("input", () => {
          link[f] = input.value;
          if (f === "label") ttl.textContent = link[f] || `Link ${idx + 1}`;
          renderConfigOutput();
          updateDirtyIndicator();
        });
      }
    }

    bindRowControls(node, "links", idx);
    return node;
  }

  /* ── Render: social pills ──────────────────────────────────── */

  function renderSocials() {
    dom.socialList.innerHTML = "";
    const frag = document.createDocumentFragment();
    state.cfg.social.forEach((s, idx) => {
      frag.appendChild(buildSocialRow(s, idx));
    });
    dom.socialList.appendChild(frag);
  }

  function buildSocialRow(soc, idx) {
    const node = dom.tmplSocialRow.content.firstElementChild.cloneNode(true);
    node.dataset.idx = String(idx);

    for (const f of ["platform", "url"]) {
      const input = node.querySelector(`[data-field="${f}"]`);
      input.value = soc[f] || "";
      input.addEventListener("input", () => {
        soc[f] = input.value;
        renderConfigOutput();
        updateDirtyIndicator();
      });
    }

    bindRowControls(node, "social", idx);
    return node;
  }

  /* ── Row controls: up / down / remove ──────────────────────── */

  function bindRowControls(node, kind, idx) {
    const list = kind === "links" ? state.cfg.links : state.cfg.social;
    const rerender = kind === "links" ? renderLinks : renderSocials;

    node.querySelector('[data-action="up"]').addEventListener("click", () => {
      if (idx === 0) return;
      [list[idx - 1], list[idx]] = [list[idx], list[idx - 1]];
      rerender();
      onFormChange();
    });
    node.querySelector('[data-action="down"]').addEventListener("click", () => {
      if (idx >= list.length - 1) return;
      [list[idx + 1], list[idx]] = [list[idx], list[idx + 1]];
      rerender();
      onFormChange();
    });
    node.querySelector('[data-action="remove"]').addEventListener("click", () => {
      // Cheap confirm — no modal for this. Removals are recoverable
      // by reloading from server before saving.
      if (!confirm(`Remove this ${kind === "links" ? "link" : "social profile"}?`)) return;
      list.splice(idx, 1);
      rerender();
      onFormChange();
    });
  }

  /* ── Output panel + dirty state + status ───────────────────── */

  function renderConfigOutput() {
    dom.configOutput.textContent = JSON.stringify(state.cfg, null, 2);
  }

  function onFormChange() {
    renderConfigOutput();
    updateDirtyIndicator();
  }

  function updateAvatarPreviewShape(shape) {
    const r = shape + "%";
    const wrap = dom.avatarPreview.closest(".admin-avatar-preview");
    if (wrap) wrap.style.borderRadius = r;
    dom.avatarPreview.style.borderRadius = r;
  }

  function updateDirtyIndicator() {
    if (!state.saved) return;
    if (isDirty()) {
      // Don't trample an "is-error" or "is-saving" status. Only
      // reflect dirty state when the slot is otherwise clean.
      if (!dom.status.classList.contains("is-error") &&
          !dom.status.classList.contains("is-saving")) {
        setStatus("unsaved changes", "", { sticky: true });
      }
      dom.saveBtn.disabled = false;
    } else {
      // Clean. Clear "unsaved changes" but leave success/error
      // messages alone if they're sticky-displayed.
      if (dom.status.textContent === "unsaved changes") {
        setStatus("", "");
      }
    }
  }

  function isDirty() {
    if (!state.saved || !state.cfg) return false;
    return JSON.stringify(state.cfg) !== JSON.stringify(state.saved);
  }

  function setStatus(text, cls, opts) {
    opts = opts || {};
    if (state.statusTimer) {
      clearTimeout(state.statusTimer);
      state.statusTimer = null;
    }
    dom.status.textContent = text;
    dom.status.classList.remove("is-saving", "is-saved", "is-error");
    if (cls) dom.status.classList.add(cls);
    if (!opts.sticky && text && cls === "is-saved") {
      state.statusTimer = setTimeout(() => {
        // After the success message expires, reflect dirty state if any.
        if (isDirty()) setStatus("unsaved changes", "", { sticky: true });
        else setStatus("", "");
      }, STATUS_CLEAR_MS);
    }
  }

  function toast(msg) {
    dom.toast.textContent = msg;
    dom.toast.hidden = false;
    setTimeout(() => { dom.toast.hidden = true; }, 2400);
  }

  /* ── Copy / download ───────────────────────────────────────── */

  async function copyConfigJSON() {
    const text = JSON.stringify(state.cfg, null, 2);
    try {
      await navigator.clipboard.writeText(text);
      toast("config copied to clipboard");
    } catch (_) {
      // Fallback: select the <pre> contents.
      const range = document.createRange();
      range.selectNodeContents(dom.configOutput);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
      toast("press ⌘/Ctrl+C to copy");
    }
  }

  function downloadConfigJSON() {
    const blob = new Blob([JSON.stringify(state.cfg, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "config.json";
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  /* ── Helpers ───────────────────────────────────────────────── */

  // normalizeConfig fills the same defaults internal/config.go does
  // on load. We mirror them client-side so a freshly-loaded config
  // missing optional fields (e.g. older files, or the "minimal"
  // preset) renders without empty/undefined cells.
  function normalizeConfig(c) {
    c = c || {};
    c.profile = c.profile || {};
    c.theme   = c.theme   || {};
    c.meta    = c.meta    || {};
    c.footer  = c.footer  || {};
    c.links   = Array.isArray(c.links)  ? c.links  : [];
    c.social  = Array.isArray(c.social) ? c.social : [];

    if (!c.theme.mode)       c.theme.mode = "auto";
    if (!c.theme.accent)     c.theme.accent = "#3D5A4C";
    if (!c.theme.accentDark) c.theme.accentDark = "#8FB3A4";
    if (!c.profile.avatar)   c.profile.avatar = "/assets/avatar.svg";
    if (!c.profile.avatarSize)  c.profile.avatarSize = 96;
    if (!c.profile.avatarShape) c.profile.avatarShape = 50;
    return c;
  }

  function blankLink() {
    return { label: "", url: "", icon: "link", description: "", featured: false };
  }
  function blankSocial() {
    return { platform: "", url: "" };
  }

  function setByPath(obj, path, value) {
    const parts = path.split(".");
    let node = obj;
    for (let i = 0; i < parts.length - 1; i++) {
      node = node[parts[i]] = node[parts[i]] || {};
    }
    node[parts[parts.length - 1]] = value;
  }

  function deepClone(v) {
    return JSON.parse(JSON.stringify(v));
  }

  function formatBytes(n) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  }
})();
