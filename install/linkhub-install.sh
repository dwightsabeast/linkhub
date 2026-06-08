#!/usr/bin/env bash
# Copyright (c) 2021-2026 community-scripts ORG
# Author: dwightsabeast
# License: MIT | https://github.com/dwightsabeast/linkhub/raw/main/LICENSE
# Source: https://github.com/dwightsabeast/linkhub

source /dev/stdin <<<"$FUNCTIONS_FILE_PATH"
color
verb_ip6
catch_errors
setting_up_container
network_check
update_os

msg_info "Installing Dependencies"
$STD apk add --no-cache curl ca-certificates util-linux
msg_ok "Installed Dependencies"

# ── Fetch release ───────────────────────────────────────────────────
RELEASE=$(curl -fsSL https://api.github.com/repos/dwightsabeast/linkhub/releases/latest | grep '"tag_name":' | cut -d '"' -f4)

msg_info "Installing LinkHub ${RELEASE}"

# Create system user
addgroup -S linkhub 2>/dev/null || true
adduser -S -D -H -G linkhub -h /var/lib/linkhub linkhub 2>/dev/null || true

# Directory layout
mkdir -p /opt/linkhub/static/assets
mkdir -p /var/lib/linkhub/assets
mkdir -p /etc/linkhub

# Download and extract. Retry + checksum-verify: a just-pushed release
# can expose its tag via /releases/latest a beat before the tarball
# finishes uploading (the download 404s), and asset fetches sometimes
# 429/500. Retry a few times rather than failing the whole install.
cd /tmp
BASE="https://github.com/dwightsabeast/linkhub/releases/download/${RELEASE}"
TARBALL="linkhub-linux-amd64.tar.gz"
rm -f "${TARBALL}" "${TARBALL}.sha256"
DL_OK=""
for attempt in 1 2 3 4 5; do
  if curl -fsSL "${BASE}/${TARBALL}" -o "${TARBALL}" &&
    curl -fsSL "${BASE}/${TARBALL}.sha256" -o "${TARBALL}.sha256" &&
    sha256sum -c "${TARBALL}.sha256" >/dev/null 2>&1; then
    DL_OK="yes"
    break
  fi
  rm -f "${TARBALL}" "${TARBALL}.sha256"
  sleep 15
done
if [ -z "${DL_OK}" ]; then
  msg_error "Could not download a valid LinkHub ${RELEASE} tarball after 5 tries."
  exit 1
fi
$STD tar xzf "${TARBALL}"
mv linkhub /opt/linkhub/linkhub
mv linkhub-hash /opt/linkhub/linkhub-hash
cp -r static/* /opt/linkhub/static/
chmod 755 /opt/linkhub/linkhub /opt/linkhub/linkhub-hash

# Default content — only laid down on a clean install
if [ ! -f /var/lib/linkhub/config.json ]; then
  cp config.default.json /var/lib/linkhub/config.json
  cp /opt/linkhub/static/assets/avatar.svg /var/lib/linkhub/assets/avatar.svg
fi

chown -R linkhub:linkhub /var/lib/linkhub
echo "${RELEASE}" >/opt/linkhub/.version
rm -rf /tmp/linkhub-linux-amd64.tar.gz /tmp/linkhub-linux-amd64.tar.gz.sha256 /tmp/static /tmp/config.default.json

msg_ok "Installed LinkHub ${RELEASE}"

# ── Environment file ────────────────────────────────────────────────
msg_info "Configuring LinkHub"

cat >/etc/linkhub/linkhub.env <<'ENV'
LINKHUB_DATA_DIR=/var/lib/linkhub
LINKHUB_STATIC_DIR=/opt/linkhub/static
LINKHUB_LISTEN=0.0.0.0:8080
AUTH_MODE=trust_proxy
ENV

chmod 640 /etc/linkhub/linkhub.env
chown root:linkhub /etc/linkhub/linkhub.env

msg_ok "Configured LinkHub"

# ── OpenRC service ──────────────────────────────────────────────────
msg_info "Creating Service"

cat >/etc/init.d/linkhub <<'RC'
#!/sbin/openrc-run
name="LinkHub"
description="LinkHub link aggregator"
command="/opt/linkhub/linkhub"
command_user="linkhub:linkhub"
command_background=true
pidfile="/run/linkhub.pid"
output_log="/var/log/linkhub.log"
error_log="/var/log/linkhub.log"

depend() {
  need net
}

start_pre() {
  set -a
  . /etc/linkhub/linkhub.env
  set +a
  export LINKHUB_DATA_DIR LINKHUB_STATIC_DIR LINKHUB_LISTEN AUTH_MODE BASIC_AUTH_USER BASIC_AUTH_HASH
  checkpath -d -m 0750 -o linkhub:linkhub /var/lib/linkhub
}
RC
chmod +x /etc/init.d/linkhub

$STD rc-update add linkhub default
touch /var/log/linkhub.log
chown linkhub:linkhub /var/log/linkhub.log
$STD rc-service linkhub start

msg_ok "Created Service"

# ── Cleanup ─────────────────────────────────────────────────────────
motd_ssh
customize

msg_info "Cleaning up"
$STD apk cache clean
msg_ok "Cleaned"
