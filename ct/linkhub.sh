#!/usr/bin/env bash
# Copyright (c) 2021-2026 community-scripts ORG
# Author: dwightsabeast
# License: MIT | https://github.com/dwightsabeast/linkhub/raw/main/LICENSE
# Source: https://github.com/dwightsabeast/linkhub

source <(curl -fsSL https://raw.githubusercontent.com/community-scripts/ProxmoxVE/main/misc/build.func | sed 's|community-scripts/ProxmoxVE/main/install/|dwightsabeast/linkhub/main/install/|g')

APP="LinkHub"
var_tags="${var_tags:-links;homepage}"
var_cpu="${var_cpu:-1}"
var_ram="${var_ram:-128}"
var_disk="${var_disk:-1}"
var_os="${var_os:-alpine}"
var_version="${var_version:-3.23}"
var_unprivileged="${var_unprivileged:-1}"

header_info "$APP"
variables
color
catch_errors

function update_script() {
  header_info

  if [ ! -f /opt/linkhub/linkhub ]; then
    msg_error "No ${APP} Installation Found!"
    exit
  fi

  RELEASE=$(curl -fsSL https://api.github.com/repos/dwightsabeast/linkhub/releases/latest | grep '"tag_name":' | cut -d '"' -f4)
  if [ "${RELEASE}" != "$(cat /opt/linkhub/.version 2>/dev/null)" ] || [ ! -f /opt/linkhub/.version ]; then
    # Download and checksum-verify into a staging area *before* touching
    # the running service. A just-pushed release can expose its tag via
    # /releases/latest a beat before the tarball finishes uploading (the
    # download 404s), and GitHub asset fetches occasionally 429/500.
    # Retrying here — and stopping LinkHub only once we hold a verified
    # tarball — means a flaky download can never leave the site down.
    local base="https://github.com/dwightsabeast/linkhub/releases/download/${RELEASE}"
    local tarball="linkhub-linux-amd64.tar.gz"
    cd /tmp
    rm -rf "${tarball}" "${tarball}.sha256" linkhub-stage

    msg_info "Downloading ${APP} ${RELEASE}"
    local ok="" attempt
    for attempt in 1 2 3 4 5; do
      if curl -fsSL "${base}/${tarball}" -o "${tarball}" &&
        curl -fsSL "${base}/${tarball}.sha256" -o "${tarball}.sha256" &&
        sha256sum -c "${tarball}.sha256" >/dev/null 2>&1; then
        ok="yes"
        break
      fi
      rm -f "${tarball}" "${tarball}.sha256"
      sleep 15
    done
    if [ -z "${ok}" ]; then
      msg_error "Could not fetch a valid ${APP} ${RELEASE} tarball after 5 tries; the running install was left untouched."
      exit 1
    fi
    mkdir -p linkhub-stage
    tar xzf "${tarball}" -C linkhub-stage
    msg_ok "Downloaded ${APP} ${RELEASE}"

    msg_info "Stopping ${APP}"
    rc-service linkhub stop
    msg_ok "Stopped ${APP}"

    msg_info "Updating ${APP} to ${RELEASE}"
    mv linkhub-stage/linkhub /opt/linkhub/linkhub
    mv linkhub-stage/linkhub-hash /opt/linkhub/linkhub-hash
    cp -r linkhub-stage/static/* /opt/linkhub/static/
    chmod 755 /opt/linkhub/linkhub /opt/linkhub/linkhub-hash
    echo "${RELEASE}" >/opt/linkhub/.version
    rm -rf "/tmp/${tarball}" "/tmp/${tarball}.sha256" /tmp/linkhub-stage
    msg_ok "Updated ${APP} to ${RELEASE}"

    msg_info "Starting ${APP}"
    rc-service linkhub start
    msg_ok "Started ${APP}"
  else
    msg_ok "No update required. ${APP} is already at ${RELEASE}"
  fi
  exit 0
}

start
build_container
description

msg_ok "Completed successfully!\n"
echo -e "${CREATING}${GN}${APP} setup has been successfully initialized!${CL}"
echo -e "${INFO}${YW} Access it using the following IP:${CL}"
echo -e "${TAB}${GATEWAY}${BGN}http://${IP}:8080${CL}"
echo -e "${TAB}${GATEWAY}${BGN}http://${IP}:8080/admin${CL}"
