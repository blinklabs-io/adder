#!/bin/bash
set -e

# bundle-macos-uninstall.sh - Uninstalls AdderTray and cleans up macOS services and artifacts.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

APP_NAME="AdderTray"
DEST_APP="/Applications/${APP_NAME}.app"
LAUNCH_AGENT_LABEL="io.blinklabs.adder"
LAUNCH_AGENT_FILE="${HOME}/Library/LaunchAgents/${LAUNCH_AGENT_LABEL}.plist"
CONFIG_DIR="${HOME}/Library/Application Support/Adder"
LOG_DIR="${HOME}/Library/Logs/Adder"

PURGE_DATA=false

for arg in "$@"; do
    case "$arg" in
        --purge|--all|-a)
            PURGE_DATA=true
            ;;
        --help|-h)
            echo "Usage: $0 [--purge|--all]"
            echo ""
            echo "Options:"
            echo "  --purge, --all    Also remove configuration (${CONFIG_DIR}) and logs (${LOG_DIR})"
            exit 0
            ;;
        *)
            echo "Unknown argument: $arg"
            echo "Usage: $0 [--purge|--all]"
            exit 1
            ;;
    esac
done

echo "--- Stopping running instances ---"
pkill -f "${APP_NAME}" 2>/dev/null || true
pkill -f "Contents/MacOS/adder" 2>/dev/null || true
pkill -f "adder-tray" 2>/dev/null || true

ERRORS=0

echo "--- Removing from Login Items ---"
if login_err=$(osascript -e 'tell application "System Events" to delete (every login item whose name is "AdderTray" or name is "Adder")' 2>&1); then
    :
else
    echo "Warning: Failed to remove login items via osascript: ${login_err}" >&2
    ERRORS=$((ERRORS + 1))
fi

echo "--- Unloading and removing LaunchAgent ---"
if command -v launchctl >/dev/null 2>&1; then
    USER_ID=$(id -u)
    if bootout_out=$(launchctl bootout "gui/${USER_ID}/${LAUNCH_AGENT_LABEL}" 2>&1); then
        :
    else
        if echo "${bootout_out}" | grep -Eqi "No such process|Could not find service|service not found"; then
            :
        else
            echo "Warning: launchctl bootout failed: ${bootout_out}" >&2
            ERRORS=$((ERRORS + 1))
        fi
    fi

    if disable_out=$(launchctl disable "gui/${USER_ID}/${LAUNCH_AGENT_LABEL}" 2>&1); then
        :
    else
        if echo "${disable_out}" | grep -Eqi "No such process|Could not find service|service not found"; then
            :
        else
            echo "Warning: launchctl disable failed: ${disable_out}" >&2
            ERRORS=$((ERRORS + 1))
        fi
    fi
fi

if [ -f "${LAUNCH_AGENT_FILE}" ]; then
    rm -f "${LAUNCH_AGENT_FILE}"
    echo "Removed LaunchAgent: ${LAUNCH_AGENT_FILE}"
fi

echo "--- Removing installed App Bundles ---"
for app_path in "${DEST_APP}" "/Applications/Adder.app"; do
    if [ -d "${app_path}" ]; then
        rm -rf "${app_path}"
        echo "Removed: ${app_path}"
    fi
done

echo "--- Cleaning local build artifacts ---"
rm -f adder adder-tray
rm -rf "${APP_NAME}.app"

if [ "${PURGE_DATA}" = true ]; then
    echo "--- Purging configurations and logs ---"
    if [ -d "${CONFIG_DIR}" ]; then
        rm -rf "${CONFIG_DIR}"
        echo "Removed configuration directory: ${CONFIG_DIR}"
    fi
    if [ -d "${LOG_DIR}" ]; then
        rm -rf "${LOG_DIR}"
        echo "Removed log directory: ${LOG_DIR}"
    fi
else
    echo "--- Notice: Configuration and logs were preserved ---"
    echo "Config: ${CONFIG_DIR}"
    echo "Logs:   ${LOG_DIR}"
    echo "To remove them as well, run: $0 --purge"
fi

if [ "${ERRORS}" -gt 0 ]; then
    echo "--- WARNING: AdderTray uninstall completed with ${ERRORS} error(s) ---" >&2
    exit 1
fi

echo "--- SUCCESS: AdderTray uninstalled successfully ---"
