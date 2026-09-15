#!/bin/bash
set -e

# bundle-macos.sh - Creates a local Adder.app bundle for visual verification.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

APP_NAME="AdderTray"
BUNDLE_DIR="${APP_NAME}.app"
CONTENTS_DIR="${BUNDLE_DIR}/Contents"
MACOS_DIR="${CONTENTS_DIR}/MacOS"
RESOURCES_DIR="${CONTENTS_DIR}/Resources"

echo "--- Terminating running instances ---"
# Best-effort terminate any running tray or engine to prevent "text file busy" locks
pkill -f "${APP_NAME}" || true
pkill -f "Contents/MacOS/adder" || true
pkill -f "adder-tray" || true

echo "--- Cleaning old builds ---"
rm -f adder adder-tray
rm -rf "${BUNDLE_DIR}"

VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo "dev")}"

echo "--- Building Adder Binaries (${VERSION}) ---"
make build VERSION="${VERSION}"
make build-tray VERSION="${VERSION}"

echo "--- Creating App Bundle Structure ---"
mkdir -p "${MACOS_DIR}"
mkdir -p "${RESOURCES_DIR}"

echo "--- Copying Assets ---"
# Copy binaries - Rename tray to match App Name for macOS recognition
cp adder "${MACOS_DIR}/adder"
cp adder-tray "${MACOS_DIR}/${APP_NAME}"

# Copy Icon
if [ -f ".github/assets/Adder.icns" ]; then
    cp ".github/assets/Adder.icns" "${RESOURCES_DIR}/icon.icns"
else
    echo "Warning: Adder.icns not found in .github/assets/"
fi

GIT_VERSION="${VERSION}"
STRIPPED_VERSION="${VERSION#v}"
if [[ "${STRIPPED_VERSION}" =~ ^[0-9]+(\.[0-9]+)+ ]]; then
    BASE_VERSION="${BASH_REMATCH[0]}"
else
    BASE_VERSION="1.0.0"
fi
TIMESTAMP=$(date +%Y%m%d.%H%M%S)
GIT_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo "nohash")
BUILD_VER="${BASE_VERSION}"

echo "--- Generating Info.plist (Version: ${BASE_VERSION}, Build: ${BUILD_VER}) ---"
cat <<EOF > "${CONTENTS_DIR}/Info.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIconFile</key>
    <string>icon</string>
    <key>CFBundleIdentifier</key>
    <string>io.blinklabs.adder.tray.v1</string>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>
    <string>${APP_NAME}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>${BASE_VERSION}</string>
    <key>CFBundleVersion</key>
    <string>${BUILD_VER}</string>
    <key>AdderGitVersion</key>
    <string>${GIT_VERSION}</string>
    <key>LSUIElement</key>
    <false/>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>NSUserNotificationAlertStyle</key>
    <string>alert</string>
</dict>
</plist>
EOF

chmod +x "${MACOS_DIR}/adder"
chmod +x "${MACOS_DIR}/${APP_NAME}"

echo "--- Ad-Hoc Signing Bundle ---"
# Ad-hoc sign the binaries and the bundle to prevent Fyne's AppleScript fallback
codesign --force --deep --sign - "${BUNDLE_DIR}"

# Force macOS to re-read bundle metadata
touch "${BUNDLE_DIR}"

echo "--- Installing to /Applications ---"
# Move to Applications to fix notification icon and Show button associations
DEST_APP="/Applications/${BUNDLE_DIR}"

if [ ! -w "/Applications" ]; then
    echo "Error: /Applications is not writable. Please run with sudo or fix permissions."
    exit 1
fi

rm -rf "${DEST_APP}"
cp -R "${BUNDLE_DIR}" /Applications/

echo "--- SUCCESS: ${APP_NAME}.app installed to /Applications ---"
echo "To verify the installed build version (QA), run:"
echo "  defaults read \"${DEST_APP}/Contents/Info\" CFBundleVersion"
echo ""
echo "To run with the correct icon and functional notifications, use:"
echo "  open \"${DEST_APP}\""
