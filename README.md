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

## Three auth modes

The public page (`GET /`) is always public. Everything else — `/admin` and the write endpoints under `/api/` — runs through one of these:

- **`trust_proxy`** *(recommended)* — the binary trusts your reverse proxy or Cloudflare Access policy. No auth in the binary itself. This is the right choice if you're already running a tunnel.
- **`basic`** — built-in HTTP Basic Auth with a bcrypt-hashed password. Set during install.
- **`form`** — a styled login page at `/login` backed by a bcrypt-hashed password and an in-memory session cookie. Use the same `BASIC_AUTH_USER` / `BASIC_AUTH_HASH` as `basic`; you get a real sign-in screen and a logout button in the admin instead of the browser's Basic Auth prompt. Sessions are process-local, so a restart signs everyone out.
- **`none`** — no auth. Only safe on a fully private network, and the binary will warn you about it at every boot.

The mode is set via `AUTH_MODE` in `/etc/linkhub/linkhub.env`. Switching later means editing that file and restarting the service.

## Install on Proxmox VE

The fast path. Run on the Proxmox host shell as root:

```sh
bash -c "$(curl -fsSL https://raw.githubusercontent.com/dwightsabeast/linkhub/main/ct/linkhub.sh)"
```

The script will prompt for a container ID, hostname, resources, and the auth mode. It creates an unprivileged Alpine LXC, downloads the latest release tarball, lays down the default config, registers an OpenRC service, and prints the bound URL when it finishes.

After install you'll have:

- An LXC running on the IP the script printed
- The service answering on port 8080 inside the container
- Default content visible at the IP — placeholder links and a generated avatar

Point your reverse proxy or Cloudflare Tunnel at `http://<lxc-ip>:8080` and open `/admin` to start editing.

If you picked `trust_proxy`, remember: the binary itself doesn't gate `/admin`. Your proxy must. With Cloudflare Tunnel this is one Access policy on the `/admin` path.

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

## Configuration reference

Environment variables read at startup:

- `LINKHUB_DATA_DIR` — where `config.json` and `assets/` live. Default `/var/lib/linkhub`.
- `LINKHUB_STATIC_DIR` — where the shipped templates and CSS live. Default `/opt/linkhub/static`.
- `LINKHUB_LISTEN` — bind address. Default `0.0.0.0:8080`.
- `AUTH_MODE` — `trust_proxy`, `basic`, `form`, or `none`. Default `trust_proxy`.
- `BASIC_AUTH_USER` / `BASIC_AUTH_HASH` — required when `AUTH_MODE` is `basic` or `form`.

Length budgets enforced by the server on save: name 32, tagline 60, bio 240, location 60, link label 36, link description 60, footer 80, meta title 100, meta description 200. Up to 12 primary links and 12 social pills.

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
├── web/
│   ├── public/               public profile template, styles, default avatar
│   └── admin/                admin shell HTML, CSS, JS
├── scripts/
│   └── linkhub.sh            Proxmox VE installer
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
- **No tests yet.** The `make test` target works, the suite is empty.

## License

MIT. See `LICENSE`.
