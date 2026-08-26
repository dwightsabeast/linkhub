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
# newt provides whiptail for the admin-login prompt; if there's no TTY
# the install falls back to a generated password, so it's best-effort.
$STD apk add --no-cache curl ca-certificates util-linux newt
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

# ── Admin login ─────────────────────────────────────────────────────
# New installs default to form auth (a styled login page). Prompt for
# the admin username + password up front so the admin surface is gated
# from the very first boot. If there's no interactive terminal we can't
# prompt, so we generate a random password and surface it at the end —
# setup never blocks, and the admin is still protected.
msg_info "Configuring LinkHub admin login"

LH_ADMIN_USER=""
LH_ADMIN_PASS=""
if [ -t 0 ] && command -v whiptail >/dev/null 2>&1; then
  LH_ADMIN_USER=$(whiptail --title "LinkHub Admin" \
    --inputbox "Admin username for the LinkHub login page:" 9 60 "admin" \
    3>&1 1>&2 2>&3) || LH_ADMIN_USER=""
  LH_ADMIN_PASS=$(whiptail --title "LinkHub Admin" \
    --passwordbox "Admin password (leave blank to auto-generate):" 9 60 \
    3>&1 1>&2 2>&3) || LH_ADMIN_PASS=""
fi

[ -z "${LH_ADMIN_USER}" ] && LH_ADMIN_USER="admin"

LH_GENERATED_PASS=""
if [ -z "${LH_ADMIN_PASS}" ]; then
  # 36 hex chars from the kernel RNG. No symbols, so it reads cleanly off
  # the summary and can't trip shell quoting anywhere downstream.
  LH_ADMIN_PASS=$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')
  LH_GENERATED_PASS="${LH_ADMIN_PASS}"
fi

# Hash with the binary we just installed. printf '%s' avoids folding a
# trailing newline into the password.
LH_ADMIN_HASH=$(printf '%s' "${LH_ADMIN_PASS}" | /opt/linkhub/linkhub-hash)

# ── Environment file ────────────────────────────────────────────────
# Unquoted heredoc so the variables expand; the values are wrapped in
# *literal* single quotes in the file so the bcrypt hash's $ signs
# survive the re-sourcing the OpenRC service does at boot.
cat >/etc/linkhub/linkhub.env <<ENV
LINKHUB_DATA_DIR=/var/lib/linkhub
LINKHUB_STATIC_DIR=/opt/linkhub/static
LINKHUB_LISTEN=0.0.0.0:8080
AUTH_MODE=form
BASIC_AUTH_USER='${LH_ADMIN_USER}'
BASIC_AUTH_HASH='${LH_ADMIN_HASH}'
ENV

chmod 640 /etc/linkhub/linkhub.env
chown root:linkhub /etc/linkhub/linkhub.env

msg_ok "Configured LinkHub admin login (user: ${LH_ADMIN_USER})"

if [ -n "${LH_GENERATED_PASS}" ]; then
  # Persist the generated password where the operator can retrieve it,
  # and print it. They should change it from the admin page after login.
  printf 'LinkHub admin\n  user: %s\n  password: %s\n' \
    "${LH_ADMIN_USER}" "${LH_GENERATED_PASS}" >/root/linkhub.creds
  chmod 600 /root/linkhub.creds
  msg_ok "Generated a random admin password (saved to /root/linkhub.creds)"
  echo "  LinkHub admin user:     ${LH_ADMIN_USER}"
  echo "  LinkHub admin password: ${LH_GENERATED_PASS}"
  echo "  Change it from the admin page after you sign in."
fi

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

# ── Log rotation ────────────────────────────────────────────────────
# The server logs a line per request and OpenRC points both stdout and
# stderr at one file, so without rotation /var/log/linkhub.log grows
# without bound. The default container has 1 GB of disk, and the way
# that ends is the daemon losing its ability to write config.json —
# a full disk takes down editing, not just logging.
#
# busybox provides logrotate on Alpine but no cron fragment for it, so
# install both the config and a daily hook.
msg_info "Configuring log rotation"

$STD apk add --no-cache logrotate

cat >/etc/logrotate.d/linkhub <<'ROTATE'
/var/log/linkhub.log {
    daily
    rotate 7
    maxsize 10M
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    su linkhub linkhub
    create 0640 linkhub linkhub
}
ROTATE
chmod 644 /etc/logrotate.d/linkhub

# copytruncate above means we never have to signal or restart the
# daemon to reopen its file — it keeps writing to the same descriptor
# and logrotate truncates underneath it. Losing a few lines mid-rotate
# is an acceptable trade for never bouncing the service.
if [ -d /etc/periodic/daily ] && [ ! -f /etc/periodic/daily/logrotate ]; then
  cat >/etc/periodic/daily/logrotate <<'CRON'
#!/bin/sh
exec /usr/sbin/logrotate /etc/logrotate.conf
CRON
  chmod +x /etc/periodic/daily/logrotate
fi

msg_ok "Configured log rotation"

# ── Cleanup ─────────────────────────────────────────────────────────
motd_ssh
customize

msg_info "Cleaning up"
$STD apk cache clean
msg_ok "Cleaned"
