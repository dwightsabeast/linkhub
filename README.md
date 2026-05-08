# LinkHub

Self-hosted, single-binary link aggregator. A calm, paper-warm public profile page plus a small admin tool for editing it. No build step, no database, no SaaS.

The visual language and design system live in a separate repo: **[linkhub-design](https://github.com/dwightsabeast/linkhub-design)**. This repo is the production app.

---

## What this is

A single Go binary serves three things from one process:

- A **public profile page** at `/` — server-rendered, zero JS, ~3 KB of HTML.
- An **admin / config builder** at `/admin` — a small vanilla-JS SPA for editing the profile.
- A **JSON API** under `/api/` for the admin to read and write `config.json`.

Three auth modes for `/admin` and the write endpoints:

- **`trust_proxy`** *(recommended)* — the binary trusts your reverse proxy or Cloudflare Access policy. No auth in the binary itself.
- **`basic`** — built-in HTTP Basic Auth, password set at install.
- **`none`** — no auth. Only safe on a fully private network.

The public page (`GET /`) is always public.

---

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
│   └── admin/                admin shell HTML + CSS
└── scripts/
    └── linkhub.sh            Proxmox VE installer
```

---

## Status

This is a work-in-progress implementation. The Go server (Rounds 1 + 1.5) is complete and builds cleanly. The web templates and CSS (Round 2) are in place. Still to come:

- **Round 3** — `web/admin/admin.js`, the admin SPA's logic
- **Round 4** — `Makefile`, GitHub Actions release workflow, `config.default.json`, full README rewrite

Until Round 4 lands, the install script in `scripts/linkhub.sh` will not have a release tarball to fetch — that step will fail until the GitHub Action runs on a tagged release.

---

## Building locally

```sh
go mod tidy
go build -o bin/linkhub ./cmd/linkhub
go build -o bin/linkhub-hash ./cmd/linkhub-hash
```

To run the server against a local data directory:

```sh
mkdir -p ./data ./web/public/assets
cp ./web/public/assets/avatar.svg ./data/assets/avatar.svg
# (you'll also need a starter config.json in ./data — Round 4 ships one)

LINKHUB_DATA_DIR=./data \
LINKHUB_STATIC_DIR=./web \
LINKHUB_LISTEN=127.0.0.1:8080 \
AUTH_MODE=trust_proxy \
./bin/linkhub
```

---

## License

TBD.
