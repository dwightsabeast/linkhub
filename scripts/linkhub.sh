#!/usr/bin/env bash
# linkhub.sh — Proxmox VE installer for LinkHub.
#
# Usage (on the Proxmox host shell):
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/dwightsabeast/linkhub/main/scripts/linkhub.sh)"
#
# Creates an unprivileged Alpine LXC, installs the LinkHub binary,
# default content, and a systemd-style service (we use OpenRC because
# Alpine; the systemd discussion in the project README is for non-
# Alpine deploys), and prints the bound URL when finished.
#
# This script targets Proxmox VE 8+. It uses `pveam`, `pct`, and
# expects you to be running as root on the PVE host.
#
# Inspired by github.com/community-scripts/ProxmoxVE — same structural
# style (HEADER block, prompts via whiptail or fallback to read,
# colored progress).

set -euo pipefail

# ── Style ───────────────────────────────────────────────────────────
# We avoid `tput` so this runs identically over SSH on a fresh PVE
# install. ANSI escapes only.
readonly C_GREEN=$'\033[0;32m'
readonly C_YELLOW=$'\033[0;33m'
readonly C_RED=$'\033[0;31m'
readonly C_DIM=$'\033[2m'
readonly C_RESET=$'\033[0m'

msg()   { printf "%s==>%s %s\n" "$C_GREEN"  "$C_RESET" "$*"; }
warn()  { printf "%s!! %s%s\n"  "$C_YELLOW" "$*"        "$C_RESET" >&2; }
die()   { printf "%sx  %s%s\n"  "$C_RED"    "$*"        "$C_RESET" >&2; exit 1; }
note()  { printf "%s   %s%s\n"  "$C_DIM"    "$*"        "$C_RESET"; }

# ── Defaults & config ───────────────────────────────────────────────
APP_NAME="linkhub"
ALPINE_TEMPLATE="alpine-3.23-default_20260116_amd64.tar.xz"
ALPINE_RELEASE="3.23"
DEFAULT_CT_ID=""        # auto-pick next free if empty
DEFAULT_HOSTNAME="linkhub"
DEFAULT_DISK_GB=1
DEFAULT_RAM_MB=128      # LinkHub idles at <30 MB; 128 is comfy headroom
DEFAULT_CORES=1
DEFAULT_BRIDGE="vmbr0"

# Where to fetch the binary + assets. Override RELEASE_URL=... to
# point at a fork.
RELEASE_URL="${RELEASE_URL:-https://github.com/dwightsabeast/linkhub/releases/latest/download}"

# ── Sanity checks ───────────────────────────────────────────────────
[[ $EUID -eq 0 ]]      || die "Must run as root."
command -v pct &>/dev/null || die "pct not found — this script is for Proxmox VE hosts."
command -v pveam &>/dev/null || die "pveam not found — this script is for Proxmox VE hosts."

# ── Prompts ─────────────────────────────────────────────────────────
# We try whiptail (matches the PVE TUI feel) and fall back to plain
# read if it's not installed. Same questions either way.
have_whiptail() { command -v whiptail &>/dev/null; }

prompt() {
  # prompt VAR "Question" "default"
  local var=$1 q=$2 def=${3:-}
  local val
  if have_whiptail; then
    val=$(whiptail --inputbox "$q" 10 70 "$def" --title "LinkHub install" 3>&1 1>&2 2>&3) \
      || die "Cancelled."
  else
    read -rp "$q [$def]: " val || die "Cancelled."
    [[ -z $val ]] && val=$def
  fi
  printf -v "$var" '%s' "$val"
}

prompt_pw() {
  # prompt_pw VAR "Question"
  # Always read from terminal directly — never via whiptail, which
  # would echo to a debug log under some configurations.
  local var=$1 q=$2 v
  printf "%s: " "$q" >&2
  read -rs v
  printf "\n" >&2
  printf -v "$var" '%s' "$v"
}

prompt_choice() {
  # prompt_choice VAR "Question" choice1 choice2 ...
  local var=$1 q=$2; shift 2
  local choices=("$@")
  local val
  if have_whiptail; then
    # Build (tag, item) pairs for whiptail menu.
    local args=()
    local i=1
    for c in "${choices[@]}"; do args+=("$i" "$c"); ((i++)); done
    local idx
    idx=$(whiptail --menu "$q" 16 70 6 "${args[@]}" --title "LinkHub install" 3>&1 1>&2 2>&3) \
      || die "Cancelled."
    val="${choices[$((idx-1))]}"
  else
    printf "%s\n" "$q" >&2
    local i=1
    for c in "${choices[@]}"; do printf "  %d) %s\n" "$i" "$c" >&2; ((i++)); done
    local pick
    read -rp "Choose [1]: " pick
    [[ -z $pick ]] && pick=1
    val="${choices[$((pick-1))]}"
  fi
  printf -v "$var" '%s' "$val"
}

# ── Pick a free CTID ────────────────────────────────────────────────
next_ctid() {
  local n=200
  while pct status "$n" &>/dev/null; do ((n++)); done
  echo "$n"
}

# ── Header ──────────────────────────────────────────────────────────
clear
cat <<'BANNER'
   __ _      __   __         __
  / /(_)__  / /__/ /  __ __ / /
 / // / _ \/  '_/ _ \/ // // _ \
/_//_/_//_/_/\_\_//_/\_,_/_.__/

 LinkHub installer — Proxmox edition
BANNER
echo

msg "Gathering install settings"

CTID="$DEFAULT_CT_ID"
[[ -z $CTID ]] && CTID=$(next_ctid)
prompt CTID       "Container ID"               "$CTID"
prompt HOSTNAME   "Hostname"                   "$DEFAULT_HOSTNAME"
prompt DISK_GB    "Disk size (GB)"             "$DEFAULT_DISK_GB"
prompt RAM_MB     "RAM (MB)"                   "$DEFAULT_RAM_MB"
prompt CORES      "CPU cores"                  "$DEFAULT_CORES"
prompt BRIDGE     "Network bridge"             "$DEFAULT_BRIDGE"
prompt PUBLIC_HOST "Public hostname (e.g. links.example.com)" ""

msg "How will you protect the admin page?"
prompt_choice AUTH_MODE \
  "Pick the auth mode for /admin" \
  "trust_proxy — the binary trusts your reverse proxy (Cloudflare Access, Authelia, etc.). Recommended." \
  "basic       — built-in HTTP Basic Auth with a password set right now." \
  "none        — no auth. Only safe on a fully private network."

# Strip the descriptive tail; we only need the keyword.
AUTH_MODE="${AUTH_MODE%% *}"

case "$AUTH_MODE" in
  basic)
    prompt BASIC_USER "Admin username" "admin"
    prompt_pw BASIC_PW "Admin password (input hidden)"
    [[ -z $BASIC_PW ]] && die "Empty password."
    ;;
  none)
    warn "Auth mode 'none' was selected. /admin will be open to anyone who can reach the LXC."
    warn "Only proceed if this LXC is on a fully private network."
    ;;
esac

# ── Storage selection ───────────────────────────────────────────────
# Prefer 'local-lvm' if it exists, fall back to 'local'.
STORAGE="local-lvm"
pvesm status 2>/dev/null | awk 'NR>1{print $1}' | grep -qx local-lvm || STORAGE="local"
prompt STORAGE "Storage" "$STORAGE"

# ── Template ────────────────────────────────────────────────────────
msg "Ensuring Alpine $ALPINE_RELEASE template is present"
TEMPLATE_PATH="local:vztmpl/$ALPINE_TEMPLATE"
if ! pveam list local 2>/dev/null | grep -q "$ALPINE_TEMPLATE"; then
  pveam update >/dev/null
  # Resolve the latest Alpine default template name from the
  # available list — the date suffix changes over time.
  RESOLVED=$(pveam available --section system 2>/dev/null \
    | awk -v r="$ALPINE_RELEASE" '$2 ~ ("^alpine-" r "-default") { print $2 }' \
    | sort -r | head -n1)
  [[ -n $RESOLVED ]] || die "Could not find an Alpine $ALPINE_RELEASE template via pveam."
  ALPINE_TEMPLATE="$RESOLVED"
  TEMPLATE_PATH="local:vztmpl/$ALPINE_TEMPLATE"
  pveam download local "$ALPINE_TEMPLATE" >/dev/null
fi
note "Using template: $ALPINE_TEMPLATE"

# ── Create the LXC ──────────────────────────────────────────────────
msg "Creating LXC $CTID ($HOSTNAME) on $STORAGE"
pct create "$CTID" "$TEMPLATE_PATH" \
  --hostname "$HOSTNAME" \
  --cores "$CORES" \
  --memory "$RAM_MB" \
  --rootfs "$STORAGE:$DISK_GB" \
  --net0 "name=eth0,bridge=$BRIDGE,ip=dhcp" \
  --features "nesting=0,keyctl=0" \
  --unprivileged 1 \
  --onboot 1 \
  --start 0 \
  >/dev/null

msg "Starting the container"
pct start "$CTID"

# Wait for network. Alpine's DHCP usually settles in <2s, but be
# patient up to 30s for slower bridges.
for i in $(seq 1 30); do
  if pct exec "$CTID" -- ip -4 -o addr show dev eth0 2>/dev/null | grep -q 'inet '; then
    break
  fi
  sleep 1
done

LXC_IP=$(pct exec "$CTID" -- ip -4 -o addr show dev eth0 \
  | awk '{ split($4, a, "/"); print a[1] }' | head -n1)
[[ -n $LXC_IP ]] || die "Container started but did not get an IP."
note "Container IP: $LXC_IP"

# ── Inner install ───────────────────────────────────────────────────
# We push a sub-script into the container and run it via `pct exec`.
# This keeps everything visible in this one file rather than a second
# round trip to GitHub.

msg "Installing LinkHub inside the container"

INNER_SCRIPT=$(cat <<INNER
#!/bin/sh
set -eu

RELEASE_URL="$RELEASE_URL"
AUTH_MODE="$AUTH_MODE"
BASIC_USER="${BASIC_USER:-}"

# Alpine packages: curl for download, ca-certificates for HTTPS,
# util-linux for agetty (console autologin).
apk add --no-cache curl ca-certificates util-linux >/dev/null

# Layout:
#   /opt/linkhub/{linkhub,linkhub-hash}     — binaries
#   /opt/linkhub/static/                    — shipped chrome
#   /var/lib/linkhub/{config.json,assets/}  — mutable state
#   /etc/linkhub/linkhub.env                — environment
#   /etc/init.d/linkhub                     — OpenRC service
addgroup -S linkhub 2>/dev/null || true
adduser  -S -D -H -G linkhub -h /var/lib/linkhub linkhub 2>/dev/null || true

mkdir -p /opt/linkhub/static/assets
mkdir -p /var/lib/linkhub/assets
mkdir -p /etc/linkhub

# Download the release tarball. The tarball ships:
#   linkhub                  (stripped binary)
#   linkhub-hash             (companion hash tool)
#   static/styles.css
#   static/admin.html
#   static/admin.js
#   static/favicon.svg
#   static/assets/avatar.svg
#   config.default.json
echo "  -> downloading release"
cd /tmp
curl -fsSL "\$RELEASE_URL/linkhub-linux-amd64.tar.gz" -o linkhub.tar.gz
tar xzf linkhub.tar.gz
mv linkhub /opt/linkhub/linkhub
mv linkhub-hash /opt/linkhub/linkhub-hash
mv static/* /opt/linkhub/static/
chmod 755 /opt/linkhub/linkhub /opt/linkhub/linkhub-hash

# Default content — only laid down on a clean install.
if [ ! -f /var/lib/linkhub/config.json ]; then
  cp config.default.json /var/lib/linkhub/config.json
  cp /opt/linkhub/static/assets/avatar.svg /var/lib/linkhub/assets/avatar.svg
fi

chown -R linkhub:linkhub /var/lib/linkhub
rm -rf /tmp/linkhub.tar.gz /tmp/static /tmp/config.default.json

# ── Console autologin ───────────────────────────────────────────────
# Alpine's default getty requires a login. Community-scripts style:
# patch /etc/inittab via an OpenRC local.d script so the change
# survives container restarts (Proxmox's Alpine.pm can overwrite
# /etc/inittab on boot; running the sed at local.d time re-applies).
passwd -d root >/dev/null 2>&1
mkdir -p /etc/local.d
cat > /etc/local.d/autologin.start <<'AUTOLOGIN'
#!/bin/sh
sed -i 's|^tty1::respawn:.*|tty1::respawn:/sbin/agetty --autologin root --noclear tty1 38400 linux|' /etc/inittab
kill -HUP 1
AUTOLOGIN
chmod +x /etc/local.d/autologin.start
touch /root/.hushlogin
rc-update add local default >/dev/null 2>&1
/etc/local.d/autologin.start

# Build the env file. Mode-specific settings appended below.
cat > /etc/linkhub/linkhub.env <<ENV
LINKHUB_DATA_DIR=/var/lib/linkhub
LINKHUB_STATIC_DIR=/opt/linkhub/static
LINKHUB_LISTEN=0.0.0.0:8080
AUTH_MODE=\$AUTH_MODE
ENV

INNER

# Append basic-auth credentials inside the heredoc-stuffed inner
# script. We do it from outside so the password never appears in the
# inner-script body that gets rendered into the LXC.
if [[ $AUTH_MODE == basic ]]; then
  # We can't run linkhub-hash on the host (different libc target).
  # Instead, generate the hash *inside* the LXC after the binary is
  # in place. Pass the password via a tempfile only the inner script
  # can read, and clean it up.
  INNER_SCRIPT="$INNER_SCRIPT
echo \"BASIC_AUTH_USER=\$BASIC_USER\" >> /etc/linkhub/linkhub.env
echo -n \"\$(cat /tmp/lh-pw)\" | /opt/linkhub/linkhub-hash > /tmp/lh-hash
echo \"BASIC_AUTH_HASH=\$(cat /tmp/lh-hash)\" >> /etc/linkhub/linkhub.env
shred -u /tmp/lh-pw 2>/dev/null || rm -f /tmp/lh-pw
shred -u /tmp/lh-hash 2>/dev/null || rm -f /tmp/lh-hash
"
  # Push the password into the container via a tempfile.
  pct exec "$CTID" -- sh -c 'umask 077; cat > /tmp/lh-pw' <<<"$BASIC_PW"
  unset BASIC_PW
fi

# Append the OpenRC service + start.
INNER_SCRIPT="$INNER_SCRIPT

# OpenRC service — no systemd on Alpine.
cat > /etc/init.d/linkhub <<'RC'
#!/sbin/openrc-run
name=\"LinkHub\"
description=\"LinkHub link aggregator\"
command=\"/opt/linkhub/linkhub\"
command_user=\"linkhub:linkhub\"
command_background=true
pidfile=\"/run/linkhub.pid\"
output_log=\"/var/log/linkhub.log\"
error_log=\"/var/log/linkhub.log\"
depend() { need net; }
start_pre() {
  set -a
  . /etc/linkhub/linkhub.env
  set +a
  export LINKHUB_DATA_DIR LINKHUB_STATIC_DIR LINKHUB_LISTEN AUTH_MODE BASIC_AUTH_USER BASIC_AUTH_HASH
  checkpath -d -m 0750 -o linkhub:linkhub /var/lib/linkhub
}
RC
chmod +x /etc/init.d/linkhub

# OpenRC reads env at start_pre time; it doesn't natively load
# EnvironmentFile-style files, so we source it there. Lock the env
# file down: it holds the bcrypt-equivalent hash.
chmod 640 /etc/linkhub/linkhub.env
chown root:linkhub /etc/linkhub/linkhub.env

rc-update add linkhub default >/dev/null
rc-service linkhub start
"

# Push the inner script and run it.
pct exec "$CTID" -- sh -c 'cat > /tmp/install-inner.sh' <<<"$INNER_SCRIPT"
pct exec "$CTID" -- sh -e /tmp/install-inner.sh
pct exec "$CTID" -- rm -f /tmp/install-inner.sh

# ── Finish ──────────────────────────────────────────────────────────
echo
msg "LinkHub is up."
echo
note "  LXC ID:        $CTID"
note "  LXC IP:        $LXC_IP"
note "  Backend URL:   http://$LXC_IP:8080  (point your reverse proxy here)"
note "  Public host:   ${PUBLIC_HOST:-not set}"
note "  Admin URL:     http://$LXC_IP:8080/admin"
note "  Auth mode:     $AUTH_MODE"
note "  Data dir:      /var/lib/linkhub  (inside the LXC)"
note "  Service:       rc-service linkhub {start|stop|restart|status}"
echo

case "$AUTH_MODE" in
  trust_proxy)
    cat <<EOF
Next steps:
  1. Point your Cloudflare Tunnel (or other reverse proxy) at
       http://$LXC_IP:8080
  2. In Cloudflare Zero Trust, add an Access policy on the
     /admin path of $PUBLIC_HOST so only you can reach it.
  3. Open https://$PUBLIC_HOST/admin and start editing.
EOF
    ;;
  basic)
    cat <<EOF
Next steps:
  1. Point your Cloudflare Tunnel (or other reverse proxy) at
       http://$LXC_IP:8080
  2. Open https://$PUBLIC_HOST/admin — your browser will prompt for
     the username and password you set during install.
EOF
    ;;
  none)
    cat <<EOF
Next steps:
  1. Point your reverse proxy at http://$LXC_IP:8080
  2. /admin is unauthenticated. Treat the LXC IP as a secret and
     consider switching to AUTH_MODE=trust_proxy or basic by editing
     /etc/linkhub/linkhub.env inside the LXC and running
       rc-service linkhub restart
EOF
    ;;
esac
