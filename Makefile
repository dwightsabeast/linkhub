# Makefile — LinkHub
#
# Targets you'll actually use day-to-day:
#
#   make             — same as `make build`
#   make build       — build both binaries into ./bin
#   make dev         — build + lay out web/static + run the server
#                      bound to 127.0.0.1:8080 with AUTH_MODE=trust_proxy
#   make tarball     — assemble dist/linkhub-linux-amd64.tar.gz exactly
#                      as the GitHub release workflow does
#   make clean       — remove ./bin and ./dist
#   make tidy        — go mod tidy
#   make fmt         — gofmt -w on Go sources
#   make vet         — go vet ./...
#
# Notes on layout. The install script and the running server both
# expect a single flat static directory (`/opt/linkhub/static/`) that
# contains everything from web/public/ and web/admin/ side by side
# (admin.html, admin.js, admin.css, styles.css, favicon.svg,
# index.html.tmpl, assets/avatar.svg). The repo keeps them split for
# editing sanity. `make dev-assets` and `make tarball` both perform
# the same flatten — same source of truth, two consumers.

# ─── Tooling ───────────────────────────────────────────────────────
GO        ?= go
GOOS      ?= linux
GOARCH    ?= amd64
CGO       ?= 0

# Version stamp. Prefer an explicit `git describe`; fall back to
# "dev" so unstamped local builds still report something useful via
# `linkhub --version` (Round 4 wires the flag into cmd/linkhub).
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   ?= -s -w -X main.version=$(VERSION)

# Where everything lands.
BIN_DIR   := bin
DIST_DIR  := dist
WEB_DIR   := web
STATIC_DEV  := $(WEB_DIR)/static
STATIC_DIST := $(DIST_DIR)/static

# Source manifests for the flatten step. Keep these in alphabetical
# order so a `git diff` of the Makefile reads as a checklist.
PUBLIC_FILES := \
	$(WEB_DIR)/public/favicon.svg \
	$(WEB_DIR)/public/index.html.tmpl \
	$(WEB_DIR)/public/styles.css

PUBLIC_ASSETS := \
	$(WEB_DIR)/public/assets/avatar.svg

ADMIN_FILES := \
	$(WEB_DIR)/admin/admin.css \
	$(WEB_DIR)/admin/admin.html \
	$(WEB_DIR)/admin/admin.js

# ─── Default ───────────────────────────────────────────────────────
.PHONY: all
all: build

# ─── Build ─────────────────────────────────────────────────────────
.PHONY: build linkhub linkhub-hash
build: linkhub linkhub-hash

linkhub:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=$(CGO) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build -trimpath -ldflags '$(LDFLAGS)' \
		-o $(BIN_DIR)/linkhub ./cmd/linkhub

linkhub-hash:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=$(CGO) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build -trimpath -ldflags '$(LDFLAGS)' \
		-o $(BIN_DIR)/linkhub-hash ./cmd/linkhub-hash

# ─── Dev run ───────────────────────────────────────────────────────
# Lays out a flat web/static/ from web/public + web/admin and runs
# the server pointed at it, with a local data dir seeded from
# config.default.json. Subsequent `make dev` invocations preserve
# the data dir so your edits stick.

.PHONY: dev dev-assets dev-data

dev: build dev-assets dev-data
	@echo "→ http://127.0.0.1:8080  (admin: /admin)"
	LINKHUB_DATA_DIR=$(CURDIR)/data \
	LINKHUB_STATIC_DIR=$(CURDIR)/$(STATIC_DEV) \
	LINKHUB_LISTEN=127.0.0.1:8080 \
	AUTH_MODE=trust_proxy \
	$(BIN_DIR)/linkhub

# Flatten web/public + web/admin into web/static. Idempotent; cp -f
# overwrites stale copies but doesn't touch the data dir.
dev-assets:
	@rm -rf $(STATIC_DEV)
	@mkdir -p $(STATIC_DEV)/assets
	@cp -f $(PUBLIC_FILES)  $(STATIC_DEV)/
	@cp -f $(PUBLIC_ASSETS) $(STATIC_DEV)/assets/
	@cp -f $(ADMIN_FILES)   $(STATIC_DEV)/

# Seed ./data on first run only. Never overwrites — your local
# edits are safe across `make dev` cycles.
dev-data:
	@if [ ! -f data/config.json ]; then \
		mkdir -p data/assets; \
		cp config.default.json data/config.json; \
		cp $(WEB_DIR)/public/assets/avatar.svg data/assets/avatar.svg; \
		echo "seeded ./data with defaults"; \
	fi

# ─── Release tarball ───────────────────────────────────────────────
# Produces dist/linkhub-linux-amd64.tar.gz with the exact layout the
# install script expects to extract:
#
#   linkhub
#   linkhub-hash
#   config.default.json
#   static/
#     admin.html
#     admin.css
#     admin.js
#     index.html.tmpl
#     styles.css
#     favicon.svg
#     assets/
#       avatar.svg
#
# A sibling .sha256 file is written next to the tarball.

.PHONY: tarball
tarball: build
	@rm -rf $(DIST_DIR)
	@mkdir -p $(STATIC_DIST)/assets
	@cp $(BIN_DIR)/linkhub      $(DIST_DIR)/linkhub
	@cp $(BIN_DIR)/linkhub-hash $(DIST_DIR)/linkhub-hash
	@cp config.default.json     $(DIST_DIR)/config.default.json
	@cp $(PUBLIC_FILES)         $(STATIC_DIST)/
	@cp $(PUBLIC_ASSETS)        $(STATIC_DIST)/assets/
	@cp $(ADMIN_FILES)          $(STATIC_DIST)/
	@cd $(DIST_DIR) && tar czf linkhub-linux-amd64.tar.gz \
		linkhub linkhub-hash config.default.json static
	@cd $(DIST_DIR) && sha256sum linkhub-linux-amd64.tar.gz \
		> linkhub-linux-amd64.tar.gz.sha256
	@echo "→ $(DIST_DIR)/linkhub-linux-amd64.tar.gz"
	@cat $(DIST_DIR)/linkhub-linux-amd64.tar.gz.sha256

# ─── Hygiene ───────────────────────────────────────────────────────
.PHONY: clean tidy fmt vet test

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) $(STATIC_DEV)

tidy:
	$(GO) mod tidy

fmt:
	gofmt -w cmd internal

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...
