#!/usr/bin/env bash
# ==============================================================================
#  BrokeInRealTime — One-Command Installer & Service Orchestrator
#  Automates installation, path configuration, and systemd/launchd startup.
# ==============================================================================

set -eo pipefail

REPO="mahin273/BrokeInRealTime"
DEFAULT_PORT=8088
DEFAULT_BIN_DIR="${HOME}/.local/bin"
PORT="${PORT:-$DEFAULT_PORT}"
BIN_DIR="${BIN_DIR:-$DEFAULT_BIN_DIR}"
NO_SERVICE=false
UNINSTALL=false

# ANSI color codes
BOLD="\033[1m"
GREEN="\033[32m"
CYAN="\033[36m"
YELLOW="\033[33m"
RED="\033[31m"
RESET="\033[0m"

log_info()    { echo -e "${CYAN}ℹ${RESET} $1"; }
log_success() { echo -e "${GREEN}✓${RESET} $1"; }
log_warn()    { echo -e "${YELLOW}⚠${RESET} $1"; }
log_error()   { echo -e "${RED}✖${RESET} $1"; }

show_banner() {
  echo -e "${BOLD}${CYAN}"
  cat << 'EOF'
    ____             __         ____        ____             __   ______ _               
   / __ )_________  / /_____   /  _/___    / __ \___  ____ _/ /  /_  __/(_)___ ___  ___  
  / __  / ___/ __ \/ //_/ _ \  / // __ \  / /_/ / _ \/ __ `/ /    / /  / / __ `__ \/ _ \ 
 / /_/ / /  / /_/ / ,< /  __/_/ // / / / / _, _/  __/ /_/ / /    / /  / / / / / / /  __/ 
/_____/_/   \____/_/|_|\___/___//_/ /_/ /_/ |_|\___/\__,_/_/    /_/  /_/_/ /_/ /_/\___/  
EOF
  echo -e "               ${RESET}${BOLD}Antigravity Realtime Local Telemetry Daemon${RESET}\n"
}

usage() {
  echo "Usage: install.sh [OPTIONS]"
  echo ""
  echo "Options:"
  echo "  --port <port>       Port to bind the dashboard (default: 8088)"
  echo "  --bin-dir <path>    Binary installation directory (default: ~/.local/bin)"
  echo "  --no-service        Install binary only without systemd/launchd service"
  echo "  --uninstall         Cleanly remove service, binary, and autostart config"
  echo "  --help, -h          Show this help message"
  exit 0
}

# Parse flags
while [[ $# -gt 0 ]]; do
  case "$1" in
    --port)
      PORT="$2"
      shift 2
      ;;
    --bin-dir)
      BIN_DIR="$2"
      shift 2
      ;;
    --no-service)
      NO_SERVICE=true
      shift
      ;;
    --uninstall)
      UNINSTALL=true
      shift
      ;;
    --help|-h)
      usage
      ;;
    *)
      log_warn "Unknown option: $1"
      usage
      ;;
  esac
done

show_banner

# --- Detect OS & Architecture ---
detect_platform() {
  KERNEL="$(uname -s)"
  case "${KERNEL}" in
    Linux*)   OS="linux" ;;
    Darwin*)  OS="darwin" ;;
    *)        log_error "Unsupported Operating System: ${KERNEL}"; exit 1 ;;
  esac

  ARCH_RAW="$(uname -m)"
  case "${ARCH_RAW}" in
    x86_64|amd64)   ARCH="amd64" ;;
    aarch64|arm64)  ARCH="arm64" ;;
    *)              ARCH="${ARCH_RAW}" ;;
  esac

  log_info "Detected Platform: ${BOLD}${OS}/${ARCH}${RESET}"
}

# --- Uninstall Routine ---
do_uninstall() {
  log_info "Initiating uninstallation of BrokeInRealTime..."

  if [[ "${OS}" == "linux" ]]; then
    if command -v systemctl >/dev/null 2>&1; then
      log_info "Stopping and disabling systemd user service..."
      systemctl --user stop brokeinrealtime 2>/dev/null || true
      systemctl --user disable brokeinrealtime 2>/dev/null || true
      rm -f "${HOME}/.config/systemd/user/brokeinrealtime.service"
      systemctl --user daemon-reload 2>/dev/null || true
    fi
  elif [[ "${OS}" == "darwin" ]]; then
    PLIST="${HOME}/Library/LaunchAgents/com.user.brokeinrealtime.plist"
    if [[ -f "${PLIST}" ]]; then
      log_info "Unloading macOS launchd agent..."
      launchctl unload "${PLIST}" 2>/dev/null || true
      rm -f "${PLIST}"
    fi
  fi

  TARGET_BIN="${BIN_DIR}/broke-daemon"
  if [[ -f "${TARGET_BIN}" ]]; then
    rm -f "${TARGET_BIN}"
    log_success "Removed binary: ${TARGET_BIN}"
  fi

  log_success "Uninstallation complete. BrokeInRealTime has been removed."
  exit 0
}

detect_platform

if [[ "${UNINSTALL}" == "true" ]]; then
  do_uninstall
fi

# Ensure bin dir exists
mkdir -p "${BIN_DIR}"

TMP_DIR="$(mktemp -d /tmp/broke-install.XXXXXX)"
cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

# --- Step 1: Acquire Binary ---
TARGET_BIN="${BIN_DIR}/broke-daemon"
INSTALLED=false

# Case A: Running directly inside existing BrokeInRealTime repository checkout
if [[ -f "./cmd/broke-daemon/main.go" ]]; then
  log_info "Detected local source repository. Building directly with Go..."
  if command -v go >/dev/null 2>&1; then
    go build -ldflags="-s -w" -o "${TARGET_BIN}" ./cmd/broke-daemon
    chmod 755 "${TARGET_BIN}"
    INSTALLED=true
    log_success "Compiled and installed binary to ${BOLD}${TARGET_BIN}${RESET}"
  elif [[ -f "./broke-daemon" ]]; then
    install -m 755 "./broke-daemon" "${TARGET_BIN}"
    INSTALLED=true
    log_success "Copied prebuilt binary to ${BOLD}${TARGET_BIN}${RESET}"
  fi
fi

# Case B: Download release tarball from GitHub
if [[ "${INSTALLED}" == "false" ]]; then
  TARBALL="broke-daemon-${OS}-${ARCH}.tar.gz"
  RELEASE_URL="https://github.com/${REPO}/releases/latest/download/${TARBALL}"
  log_info "Attempting to download precompiled binary for ${OS}/${ARCH}..."

  if curl -fsSL -o "${TMP_DIR}/${TARBALL}" "${RELEASE_URL}" 2>/dev/null; then
    log_info "Extracting release asset..."
    tar -xzf "${TMP_DIR}/${TARBALL}" -C "${TMP_DIR}"
    if [[ -f "${TMP_DIR}/broke-daemon" ]]; then
      install -m 755 "${TMP_DIR}/broke-daemon" "${TARGET_BIN}"
      INSTALLED=true
      log_success "Downloaded & installed binary to ${BOLD}${TARGET_BIN}${RESET}"
    fi
  else
    log_warn "Precompiled release binary not available on GitHub Releases."
  fi
fi

# Case C: Fallback to Git Clone & Go Build
if [[ "${INSTALLED}" == "false" ]]; then
  log_info "Building from source via Go toolchain..."
  if ! command -v go >/dev/null 2>&1; then
    log_error "Go 1.22+ is required to build from source, but 'go' was not found in PATH."
    log_error "Install Go from https://go.dev/dl or package manager, then re-run this script."
    exit 1
  fi
  if ! command -v git >/dev/null 2>&1; then
    log_error "Git is required to clone source code, but 'git' was not found in PATH."
    exit 1
  fi

  log_info "Cloning https://github.com/${REPO}.git..."
  git clone --depth 1 "https://github.com/${REPO}.git" "${TMP_DIR}/repo"
  (
    cd "${TMP_DIR}/repo"
    go build -ldflags="-s -w" -o "${TARGET_BIN}" ./cmd/broke-daemon
  )
  chmod 755 "${TARGET_BIN}"
  INSTALLED=true
  log_success "Successfully built and installed ${BOLD}${TARGET_BIN}${RESET}"
fi

# --- Step 2: Ensure ~/.local/bin is in PATH ---
check_path() {
  if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
    log_warn "${BIN_DIR} is not in your current PATH."
    SHELL_PROFILE=""
    if [[ -f "${HOME}/.bashrc" ]]; then
      SHELL_PROFILE="${HOME}/.bashrc"
    elif [[ -f "${HOME}/.zshrc" ]]; then
      SHELL_PROFILE="${HOME}/.zshrc"
    fi

    if [[ -n "${SHELL_PROFILE}" ]] && ! grep -q "${BIN_DIR}" "${SHELL_PROFILE}" 2>/dev/null; then
      echo "export PATH=\"${BIN_DIR}:\$PATH\"" >> "${SHELL_PROFILE}"
      log_success "Added ${BIN_DIR} to ${SHELL_PROFILE}"
    else
      echo -e "  To run broke-daemon manually, add to your shell profile:"
      echo -e "  ${BOLD}export PATH=\"${BIN_DIR}:\$PATH\"${RESET}"
    fi
  fi
}
check_path

# --- Step 3: Install & Start Service ---
setup_service() {
  if [[ "${NO_SERVICE}" == "true" ]]; then
    log_info "Skipping background service setup (--no-service flag enabled)."
    return
  fi

  if [[ "${OS}" == "linux" ]]; then
    if command -v systemctl >/dev/null 2>&1; then
      SYSTEMD_DIR="${HOME}/.config/systemd/user"
      mkdir -p "${SYSTEMD_DIR}"
      SERVICE_FILE="${SYSTEMD_DIR}/brokeinrealtime.service"

      cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=BrokeInRealTime Antigravity Local Telemetry Daemon
After=network.target

[Service]
Type=simple
ExecStart=${TARGET_BIN} --port ${PORT}
Restart=always
RestartSec=5s
Environment=PATH=${BIN_DIR}:/usr/local/bin:/usr/bin

PrivateTmp=true
ProtectSystem=full
NoNewPrivileges=true

[Install]
WantedBy=default.target
EOF

      log_info "Configuring systemd user service..."
      systemctl --user daemon-reload
      systemctl --user enable --now brokeinrealtime
      log_success "Started systemd service ${BOLD}brokeinrealtime.service${RESET}"
    else
      log_warn "systemd not detected. You can launch BrokeInRealTime directly with:"
      echo -e "  ${BOLD}${TARGET_BIN} --port ${PORT} &${RESET}"
    fi

  elif [[ "${OS}" == "darwin" ]]; then
    LAUNCH_DIR="${HOME}/Library/LaunchAgents"
    mkdir -p "${LAUNCH_DIR}"
    PLIST_FILE="${LAUNCH_DIR}/com.user.brokeinrealtime.plist"

    cat > "${PLIST_FILE}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.user.brokeinrealtime</string>
    <key>ProgramArguments</key>
    <array>
        <string>${TARGET_BIN}</string>
        <string>--port</string>
        <string>${PORT}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/brokeinrealtime.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/brokeinrealtime.err</string>
</dict>
</plist>
EOF

    log_info "Configuring macOS launchd agent..."
    launchctl unload "${PLIST_FILE}" 2>/dev/null || true
    launchctl load -w "${PLIST_FILE}"
    log_success "Loaded macOS launchd agent ${BOLD}com.user.brokeinrealtime${RESET}"
  fi
}
setup_service

# --- Step 4: Healthcheck ---
verify_health() {
  log_info "Verifying daemon health on http://127.0.0.1:${PORT}..."
  MAX_RETRIES=6
  COUNT=0
  UP=false

  while [[ ${COUNT} -lt ${MAX_RETRIES} ]]; do
    if curl -s "http://127.0.0.1:${PORT}/api/snapshot" >/dev/null 2>&1; then
      UP=true
      break
    fi
    sleep 0.8
    COUNT=$((COUNT + 1))
  done

  if [[ "${UP}" == "true" ]]; then
    log_success "BrokeInRealTime daemon is up and healthy!"
  else
    log_warn "Daemon is starting up or bound to a custom address. Check status manually."
  fi
}

if [[ "${NO_SERVICE}" == "false" ]]; then
  verify_health
fi

echo ""
echo -e "${BOLD}${GREEN}================================================================${RESET}"
echo -e "${BOLD}${GREEN}  BrokeInRealTime has been successfully installed! 🚀${RESET}"
echo -e "${BOLD}${GREEN}================================================================${RESET}"
echo ""
echo -e "  🌐 ${BOLD}Real-Time Dashboard:${RESET}  ${CYAN}http://127.0.0.1:${PORT}${RESET}"
echo -e "  📁 ${BOLD}Binary Location:${RESET}      ${TARGET_BIN}"
if [[ "${OS}" == "linux" && "${NO_SERVICE}" == "false" ]]; then
  echo -e "  🔍 ${BOLD}Service Status:${RESET}       systemctl --user status brokeinrealtime"
  echo -e "  📜 ${BOLD}Live Logs:${RESET}            journalctl --user -u brokeinrealtime -f"
  echo -e "  🔄 ${BOLD}Restart Daemon:${RESET}       systemctl --user restart brokeinrealtime"
fi
echo -e "  🗑️ ${BOLD}Uninstall Command:${RESET}    curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash -s -- --uninstall"
echo ""
