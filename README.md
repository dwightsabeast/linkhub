# LinkHub

A self-hosted, single-binary link aggregator. A calm, paper-warm public profile page plus a small admin tool for editing it. No build step, no database, no SaaS.

The visual language and design system live in a separate repo: **[linkhub-design](https://github.com/dwightsabeast/linkhub-design)**. This repo is the production app.

## What you get

A single Go binary serves three things from one process:

- A **public profile page** at `/` — server-rendered, zero JS, ~3 KB of HTML.
- An **admin / config builder** at `/admin` — a small vanilla-JS SPA for editing the profile.
- A **JSON API** under `/api/` for the admin to read and write `config.json`.

State lives on disk: `config.json` plus a tiny `assets/` directory for the avatar. No database. Atomic writes mean the public page is never reading a half-saved config.

The binary idles at under 30 MB of RAM. The default Proxmox LXC the install script provisions has 128 MB and 1 GB of disk, which is generous.

## Four auth modes

The public page (`GET /`) is always public. Everything else — `/admin` and the write endpoints under `/api/` — runs through one of these:

- **`form`** *(default for new installs)* — a styled login page at `/login` backed by a bcrypt-hashed password and an in-memory session cookie. You set the username and password during install; once signed in you can change them from the **Admin account** card in the admin (no file editing, no restart). There's a logout button in the admin. Sessions are process-local, so a restart signs everyone out.
- **`trust_proxy`** — the binary trusts your reverse proxy or Cloudflare Access policy. No auth in the binary itself. The right choice if you're already running a tunnel with an Access policy on `/admin`.
- **`basic`** — built-in HTTP Basic Auth with a bcrypt-hashed password (the browser's native prompt, no logout). Credentials come from `BASIC_AUTH_USER` / `BASIC_AUTH_HASH`.
- **`none`** — no auth. Only safe on a fully private network, and the binary will warn you about it at every boot.

The mode is set via `AUTH_MODE` in `/etc/linkhub/linkhub.env`. Switching later means editing that file and restarting the service.

**Where form credentials live.** The install writes the initial username/password into `BASIC_AUTH_USER` / `BASIC_AUTH_HASH` in the env file. The first time you change them from the admin, they're saved to `auth.json` in the data dir (`/var/lib/linkhub/auth.json`) — which the daemon can rewrite, unlike the root-owned env file. From then on `auth.json` is authoritative and later edits to the env credentials are ignored.

## Install on Proxmox VE

The fast path. Run on the Proxmox host shell as root:

```sh
bash -c "$(curl -fsSL https://raw.githubusercontent.com/dwightsabeast/linkhub/main/ct/linkhub.sh)"
```

The script will prompt for a container ID, hostname, resources, and the **admin username and password** for the login page. It creates an unprivileged Alpine LXC, downloads the latest release tarball, lays down the default config, registers an OpenRC service, and prints the bound URL when it finishes. (If the installer can't show a prompt, it generates a random password and prints it — and saves it to `/root/linkhub.creds` — so the admin is still protected.)

After install you'll have:

- An LXC running on the IP the script printed
- The service answering on port 8080 inside the container
- Default content visible at the IP — placeholder links and a generated avatar
- The admin at `/admin` gated by the login you set (form auth)

Point your reverse proxy or Cloudflare Tunnel at `http://<lxc-ip>:8080` and open `/admin` to start editing. You'll be sent to `/login` first; sign in with the credentials from install. To change them later, use the **Admin account** card in the admin — or switch to `trust_proxy` if you'd rather gate `/admin` entirely at your proxy.

## Install manually (other Linux)

For non-Proxmox hosts. Grab the release tarball:

```sh
curl -fsSL -o linkhub.tar.gz \
  https://github.com/dwightsabeast/linkhub/releases/latest/download/linkhub-linux-amd64.tar.gz
tar xzf linkhub.tar.gz
```

You'll have:

```
linkhub                 # the server binary
linkhub-hash            # bcrypt CLI for the basic auth mode
config.default.json     # starter content
static/                 # HTML, CSS, JS, default avatar
```

Decide where state lives (the script uses `/var/lib/linkhub` by convention):

```sh
sudo mkdir -p /opt/linkhub /var/lib/linkhub/assets /etc/linkhub
sudo cp linkhub linkhub-hash /opt/linkhub/
sudo cp -r static /opt/linkhub/
sudo cp config.default.json /var/lib/linkhub/config.json
sudo cp static/assets/avatar.svg /var/lib/linkhub/assets/
```

Write `/etc/linkhub/linkhub.env`. Pick one auth block:

```sh
# trust_proxy — your reverse proxy gates /admin
LINKHUB_DATA_DIR=/var/lib/linkhub
LINKHUB_STATIC_DIR=/opt/linkhub/static
LINKHUB_LISTEN=0.0.0.0:8080
AUTH_MODE=trust_proxy
```

```sh
# basic — built-in Basic Auth
LINKHUB_DATA_DIR=/var/lib/linkhub
LINKHUB_STATIC_DIR=/opt/linkhub/static
LINKHUB_LISTEN=0.0.0.0:8080
AUTH_MODE=basic
BASIC_AUTH_USER=admin
BASIC_AUTH_HASH=<paste-hash-here>
```

```sh
# form — styled login page + session cookie (same credentials as basic)
LINKHUB_DATA_DIR=/var/lib/linkhub
LINKHUB_STATIC_DIR=/opt/linkhub/static
LINKHUB_LISTEN=0.0.0.0:8080
AUTH_MODE=form
BASIC_AUTH_USER=admin
BASIC_AUTH_HASH=<paste-hash-here>
```

For `basic` and `form`, generate the hash by piping the password through `linkhub-hash`:

```sh
echo -n 'your-password' | /opt/linkhub/linkhub-hash
```

That's a bcrypt cost-12 hash; the binary fails fast at boot if the value doesn't look like one.

Then wire up your init system. A minimal systemd unit:

```ini
[Unit]
Description=LinkHub
After=network.target

[Service]
Type=simple
EnvironmentFile=/etc/linkhub/linkhub.env
ExecStart=/opt/linkhub/linkhub
User=linkhub
Group=linkhub
Restart=on-failure
RestartSec=2s

[Install]
WantedBy=multi-user.target
```

Create the user (`useradd -r -s /usr/sbin/nologin linkhub`), `chown -R linkhub:linkhub /var/lib/linkhub`, then `systemctl enable --now linkhub`.

## Editing your page

Open `/admin`. The page is a single-column form with five voice presets at the top — pick one as a starting point or build from scratch. The form covers profile copy, the avatar, meta tags, accent color, primary links, social pills, and footer. The generated `config.json` is shown live below the form.

Save is explicit. The button at the bottom is the only thing that writes to disk. Until you press it, edits live only in your browser. The status pill in the page header tells you when there are unsaved changes.

A couple of behaviors worth knowing:

- **Avatar uploads happen immediately.** Picking a file POSTs it directly to `/api/avatar`, which writes it to disk and updates the avatar URL in your config. This is one of the few admin actions that doesn't wait for "Save."
- **Presets replace profile, links, social, and accent.** They preserve meta, footer, and avatar — those are site identity, not voice. The admin asks before applying.
- **Only one link can be featured.** The admin enforces this client-side and the server enforces it on save.
- **Reload from server discards your unsaved edits.** If you have two tabs open, last save wins. Don't do that.

## Privacy and tracking (US)

LinkHub ships a privacy notice and a working opt-out, built for the way US
state privacy law actually works. Twenty states now have comprehensive privacy
laws in force, and **none of them requires an opt-in cookie banner** the way
GDPR does. The US model is *notice plus opt-out*. What twelve of those states
— California, Colorado, Connecticut, Delaware, Maryland, Minnesota, Montana,
Nebraska, New Hampshire, New Jersey, Oregon, and Texas — *do* require is that
your site detect and honor a universal opt-out signal automatically.

So that's what the binary does.

**Global Privacy Control is honored unconditionally.** Any request carrying
`Sec-GPC: 1` is treated as an opt-out on the spot — no banner, no confirmation
step, nothing for the visitor to click. The older `DNT: 1` header is honored the
same way. There is deliberately **no setting to turn this off**: in a dozen
states it isn't optional, and a switch for it would only ever be a footgun.
The machine-readable declaration is published at `/.well-known/gpc.json`.

**What "opted out" actually changes.** For that request the server:

- omits your `<head>` snippet, if it's classified as analytics or advertising;
- omits the Google Fonts `<link>` tags, so no request reaches Google;
- serves the page from local font stacks instead.

Suppression is server-side. Nothing gets rendered and then hidden, so there is
no pre-consent window in which a pixel has already fired — which is the exact
fact pattern behind the CIPA "pen register" suits (~4,000 filed in California
by mid-2026, plus ~800 in Florida).

**The notice.** `/privacy` is generated from live config, so it describes what
your site actually loads rather than boilerplate that drifts. It's linked from
the footer of every profile page, carries the opt-out control as a plain form
POST (no JavaScript), and never loads web fonts itself — reading a privacy
notice shouldn't cost you a third-party request.

**Fill these in** under **Privacy & tracking** in the admin:

| Field | Why it matters |
| --- | --- |
| Who runs this site | The "we" of the notice |
| Privacy contact | Every state law expects a way to reach you about a request |
| Snippet category | Decides whether your snippet is withheld on opt-out |
| Name the tracker | "Categories of third parties" is a required disclosure |
| Fonts | `system` removes the Google request for everyone |
| Retention / effective date | Optional; the date also feeds `gpc.json` |

An unclassified snippet is treated as **analytics** — the fail-safe reading, so
an upgrade from an older config withholds it rather than firing it at someone
who asked you not to. Classify it as `advertising` and the footer link changes
to **"Your Privacy Choices"**, the phrase California expects from a site that
sells or shares personal information.

Cookies, in full — there are two, both strictly necessary, neither shared:

| Cookie | Set when | Lifetime |
| --- | --- | --- |
| `linkhub_privacy_optout` | A visitor uses the opt-out control | 1 year |
| `linkhub_session` | The owner signs in at `/login` (form auth only) | 12 hours |

Two things this does **not** do. It doesn't make you compliant on its own —
it gives you accurate notice, a real opt-out, and automatic GPC handling, but
you still have to fill in who you are and describe what you've installed. And
the notice text is generated, not lawyer-reviewed; read it once at `/privacy`
before you rely on it. If you operate at a scale where a state AG might come
asking, get it reviewed.

The California Privacy Protection Agency publishes a registered opt-out icon.
LinkHub renders its own toggle glyph rather than an approximation of that mark:
the regulation requires the link *text*, and the icon is optional. If you want
the official artwork, serve it from `/assets` and reference it yourself.

## Configuration reference

Environment variables read at startup:

- `LINKHUB_DATA_DIR` — where `config.json` and `assets/` live. Default `/var/lib/linkhub`.
- `LINKHUB_STATIC_DIR` — where the shipped templates and CSS live. Default `/opt/linkhub/static`.
- `LINKHUB_LISTEN` — bind address. Default `0.0.0.0:8080`.
- `AUTH_MODE` — `trust_proxy`, `basic`, `form`, or `none`. The binary defaults to `trust_proxy` when unset; the installer writes `form` for new installs.
- `BASIC_AUTH_USER` / `BASIC_AUTH_HASH` — required for `basic`; the initial credential (bootstrap) for `form`. Once you change a `form` login from the admin, `auth.json` in the data dir takes over and these are ignored. **Quote the hash with single quotes** in the env file — it contains `$`, which the shell would otherwise expand on load.

`profile.avatar` and `meta.favicon` must be **local paths** (`/assets/…`,
`/static/…`). The server rejects an absolute URL on save: the public page
renders these into a `src`/`href` the visitor's browser fetches, so an
off-site value would disclose every visitor's IP to that host on every page
load — while `/privacy`, which is generated from this same config, went on
describing a site that makes no third-party requests. Use the **Upload
new…** button rather than pasting a URL.

Length budgets enforced by the server on save: name 32, tagline 60, bio 240, location 60, link label 36, link description 60, footer 80, meta title 100, meta description 200. Up to 12 primary links and 12 social pills. Privacy fields: operator 80, contact 120, tracker name 160, retention 300.

The `privacy` block in `config.json` — all fields optional, all editable from the admin:

```json
"privacy": {
  "operator": "Your Studio",
  "contact": "privacy@example.com",
  "snippetCategory": "analytics",
  "snippetDescription": "Plausible Analytics, self-hosted",
  "fontSource": "google",
  "retention": "",
  "effective": "2026-08-25"
}
```

`snippetCategory` is one of `none`, `essential`, `analytics`, `advertising` (empty is read as `analytics`). `fontSource` is `google` or `system`. `effective` is `YYYY-MM-DD`.

**Upgrading.** The release tarball now includes `static/privacy.html.tmpl`, and the binary parses it at startup. If you upgrade by dropping in only the new binary, it will refuse to boot with a template-not-found error — replace the whole `static/` directory, which is what the install script does anyway.

## Building from source

```sh
git clone https://github.com/dwightsabeast/linkhub.git
cd linkhub
make build
```

That produces `bin/linkhub` and `bin/linkhub-hash`. Both binaries are static (CGO disabled) and stripped (`-s -w`). Version stamped from `git describe`.

To run locally against a freshly seeded data directory:

```sh
make dev
```

This flattens `web/public/` and `web/admin/` into `web/static/`, seeds `./data/` from `config.default.json`, and runs the server on `127.0.0.1:8080` with `AUTH_MODE=trust_proxy`. Subsequent `make dev` runs preserve `./data/` so your edits stick.

To produce the same release tarball the GitHub Action does:

```sh
make tarball
# → dist/linkhub-linux-amd64.tar.gz (+ .sha256)
```

Other useful targets: `make tidy`, `make fmt`, `make vet`, `make test`, `make clean`.

## Project layout

```
.
├── cmd/
│   ├── linkhub/              server entry point
│   └── linkhub-hash/         bcrypt CLI for the install script
├── internal/
│   ├── auth/                 auth middleware (three modes)
│   ├── config/               config schema, atomic load/save, validation
│   └── server/               router, handlers, icon SVG paths
│       └── privacy.go        GPC/opt-out detection, /privacy, gpc.json
├── web/
│   ├── public/               profile + privacy templates, styles, avatar
│   └── admin/                admin shell HTML, CSS, JS
├── ct/
│   └── linkhub.sh            Proxmox VE wrapper (create LXC, update)
├── install/
│   └── linkhub-install.sh    in-container install steps
├── config.default.json       starter content shipped in the tarball
├── Makefile
└── .github/workflows/release.yml
```

The split between `web/public/` and `web/admin/` is for editing sanity; both are flattened into a single `static/` directory by `make dev` and `make tarball`. The running binary expects that flat layout under `LINKHUB_STATIC_DIR`.

## Releasing

Push a tag matching `v*`:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The `release` workflow builds `linkhub-linux-amd64.tar.gz` (plus its `.sha256`) and attaches both to the GitHub Release the tag created. The install script always pulls from `/releases/latest/download/`, so a new tag is what makes a new install pick up changes.

## Status

This is a working v0. The core path — install, edit, render — is complete. Things deliberately out of scope for now and called out so they're not surprises:

- **Linux/amd64 only.** ARM is a one-line workflow change when needed.
- **Static assets live on disk.** They're not embedded in the binary via `embed.FS`. Trade-off is that `LINKHUB_STATIC_DIR` has to point at the shipped tree; gain is that you can hot-edit a CSS file on the running LXC for debugging.
- **Fonts via Google Fonts CDN.** Self-hosting is a follow-up. The `@import` at the top of both stylesheets is the lightweight choice for now.
- **No ETag/If-Match on `/api/config`.** Two browser tabs editing concurrently → last save wins. Fine for a single-user tool; would matter if it ever became multi-user.
- **Tests cover the logic, not the HTTP surface.** `make test` runs unit
  tests for config validation, the store's concurrent writes, the opt-out
  precedence matrix, the open-redirect guard, login rate limiting, and the
  three-way icon-set sync. There are no end-to-end handler tests yet.
  CI runs `gofmt`, `go vet`, `go test -race`, `make build`, and
  `govulncheck` on every push.

## License

MIT. See `LICENSE`.
