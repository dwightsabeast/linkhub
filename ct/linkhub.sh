#!/usr/bin/env bash
# Copyright (c) 2021-2026 community-scripts ORG
# Author: dwightsabeast
# License: MIT | https://github.com/dwightsabeast/linkhub/raw/main/LICENSE
# Source: https://github.com/dwightsabeast/linkhub

source <(curl -fsSL https://raw.githubusercontent.com/community-scripts/ProxmoxVE/main/misc/build.func)

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
    msg_info "Stopping ${APP}"
    rc-service linkhub stop
    msg_ok "Stopped ${APP}"

    msg_info "Updating ${APP} to ${RELEASE}"
    cd /tmp
    curl -fsSL "https://github.com/dwightsabeast/linkhub/releases/download/${RELEASE}/linkhub-linux-amd64.tar.gz" -o linkhub.tar.gz
    tar xzf linkhub.tar.gz
    mv linkhub /opt/linkhub/linkhub
    mv linkhub-hash /opt/linkhub/linkhub-hash
    cp -r static/* /opt/linkhub/static/
    chmod 755 /opt/linkhub/linkhub /opt/linkhub/linkhub-hash
    echo "${RELEASE}" >/opt/linkhub/.version
    rm -rf /tmp/linkhub.tar.gz /tmp/static /tmp/config.default.json
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
