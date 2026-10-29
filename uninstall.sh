#!/usr/bin/env bash
# ==============================================================================
#  BrokeInRealTime — Clean Uninstaller
#  Stops and disables background services, removes daemon binaries, and cleans up.
# ==============================================================================

set -eo pipefail

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

echo -e "${BOLD}${CYAN}"
echo "  BrokeInRealTime — Uninstaller"
echo -e "${RESET}"

# Detect OS
KERNEL="$(uname -s)"
case "${KERNEL}" in
  Linux*)   OS="linux" ;;
  Darwin*)  OS="darwin" ;;
  *)        OS="unknown" ;;
esac

log_info "Initiating complete removal of BrokeInRealTime..."

# 1. Stop and remove background service
if [[ "${OS}" == "linux" ]]; then
  if command -v systemctl >/dev/null 2>&1; then
    log_info "Stopping and disabling systemd user service..."
    systemctl --user stop brokeinrealtime 2>/dev/null || true
    systemctl --user disable brokeinrealtime 2>/dev/null || true
    
    SERVICE_FILE="${HOME}/.config/systemd/user/brokeinrealtime.service"
    if [[ -f "${SERVICE_FILE}" ]]; then
      rm -f "${SERVICE_FILE}"
      log_success "Removed service file: ${SERVICE_FILE}"
    fi
    systemctl --user daemon-reload 2>/dev/null || true
  fi
elif [[ "${OS}" == "darwin" ]]; then
  PLIST="${HOME}/Library/LaunchAgents/com.user.brokeinrealtime.plist"
  if [[ -f "${PLIST}" ]]; then
    log_info "Unloading and removing macOS launchd agent..."
    launchctl unload "${PLIST}" 2>/dev/null || true
    rm -f "${PLIST}"
    log_success "Removed launchd plist: ${PLIST}"
  fi
fi

# 2. Remove binary executables
CANDIDATE_PATHS=(
  "${BIN_DIR:-${HOME}/.local/bin}/broke-daemon"
  "${HOME}/.local/bin/broke-daemon"
  "/usr/local/bin/broke-daemon"
)

REMOVED_ANY=false
for BIN in "${CANDIDATE_PATHS[@]}"; do
  if [[ -f "${BIN}" ]]; then
    rm -f "${BIN}"
    log_success "Removed binary: ${BIN}"
    REMOVED_ANY=true
  fi
done

if [[ "${REMOVED_ANY}" == "false" ]]; then
  log_info "No broke-daemon binary found in standard paths."
fi

# 3. Completion confirmation
echo ""
echo -e "${BOLD}${GREEN}================================================================${RESET}"
echo -e "${BOLD}${GREEN}  BrokeInRealTime has been completely and cleanly uninstalled.${RESET}"
echo -e "${BOLD}${GREEN}================================================================${RESET}"
echo ""
