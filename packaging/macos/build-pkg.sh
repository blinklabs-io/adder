#!/bin/bash
# Copyright 2026 Blink Labs Software
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# build-pkg.sh - build a (optionally signed and notarized) macOS .pkg installer
# containing both the `adder` CLI and the `adder-tray` GUI, packaged inside an
# Adder.app bundle installed to /Applications.
#
# The script is fully parameterized via environment variables. Signing,
# notarization, and stapling are SKIPPED with a clear warning when the relevant
# credentials are not provided, so it produces an UNSIGNED pkg locally and a
# signed + notarized pkg in CI. See packaging/macos/README.md for the full
# env-var contract and the verification steps.

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration (all overridable via environment)
# ---------------------------------------------------------------------------

# Resolve repository paths relative to this script so it works from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Go module path, used for the version ldflags (mirrors the Makefile).
GOMODULE="$(grep '^module' "${REPO_ROOT}/go.mod" | awk '{ print $2 }')"

# Version string. Defaults to the git tag/description; strips a leading "v".
if [ -z "${VERSION:-}" ]; then
    VERSION="$(git -C "${REPO_ROOT}" describe --tags --always --dirty 2>/dev/null || echo "0.0.0")"
fi
VERSION="${VERSION#v}"

# Commit hash for the version ldflags.
COMMIT_HASH="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo "unknown")"

# Target architecture: arm64 (default on Apple Silicon) or amd64/x86_64.
# PKG_ARCH uses Go's arch naming so the pkg filename matches the CI matrix
# `arch` verbatim, keeping publish.yml's spctl/upload/attest arch-agnostic.
ARCH="${ARCH:-$(uname -m)}"
# HOST_ARCH is the Apple arch name (x86_64/arm64) for the distribution.xml
# hostArchitectures attribute, so each single-arch pkg advertises only the arch
# it actually contains (Installer then blocks a wrong-arch install).
case "${ARCH}" in
    arm64|aarch64) GOARCH="arm64";  PKG_ARCH="arm64";  HOST_ARCH="arm64" ;;
    x86_64|amd64)  GOARCH="amd64";  PKG_ARCH="amd64";  HOST_ARCH="x86_64" ;;
    *) echo "ERROR: unsupported ARCH '${ARCH}' (use arm64 or amd64)" >&2; exit 1 ;;
esac

# Signing / notarization identities (empty => skip the corresponding step).
#   SIGNING_IDENTITY    "Developer ID Application: ..."  - signs binaries + .app
#   INSTALLER_IDENTITY  "Developer ID Installer: ..."    - signs the .pkg
#   TEAM_ID             Apple Developer Team ID (10 chars)
#   NOTARY_PROFILE      `notarytool store-credentials` keychain profile name
# Apple ID fallback (used only when NOTARY_PROFILE is unset):
#   APPLE_ID            Apple ID email
#   APPLE_APP_PASSWORD  app-specific password for that Apple ID
SIGNING_IDENTITY="${SIGNING_IDENTITY:-}"
INSTALLER_IDENTITY="${INSTALLER_IDENTITY:-}"
TEAM_ID="${TEAM_ID:-}"
NOTARY_PROFILE="${NOTARY_PROFILE:-}"
APPLE_ID="${APPLE_ID:-}"
APPLE_APP_PASSWORD="${APPLE_APP_PASSWORD:-}"

# Bundle identity constants.
BUNDLE_ID="io.blinklabs.adder"
APP_NAME="Adder"
# Pre-lowercased for artifact filenames (kept as a constant rather than using
# `${APP_NAME,,}`, which is bash 4+ syntax and breaks on macOS' default bash
# 3.2 used by `macos-latest` GitHub runners).
APP_NAME_LC="adder"

# Output / work locations.
DIST_DIR="${DIST_DIR:-${REPO_ROOT}/dist}"
BUILD_DIR="${BUILD_DIR:-${REPO_ROOT}/build/macos}"
PKG_NAME="${APP_NAME_LC}-${VERSION}-darwin-${PKG_ARCH}.pkg"
FINAL_PKG="${DIST_DIR}/${PKG_NAME}"
COMPONENT_PKG="${BUILD_DIR}/${APP_NAME_LC}-component.pkg"
UNSIGNED_PKG="${BUILD_DIR}/${APP_NAME_LC}-unsigned.pkg"

# Icon source (already present in the repo).
ICON_SRC="${ICON_SRC:-${REPO_ROOT}/.github/assets/Adder.icns}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARNING:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 1. Build both binaries
# ---------------------------------------------------------------------------

build_binaries() {
    log "Building binaries (version=${VERSION}, commit=${COMMIT_HASH}, arch=${GOARCH})"

    # Mirror the Makefile's version ldflags pattern exactly.
    local ldflags="-s -w \
-X '${GOMODULE}/internal/version.Version=${VERSION}' \
-X '${GOMODULE}/internal/version.CommitHash=${COMMIT_HASH}'"

    mkdir -p "${BUILD_DIR}/bin"

    # adder CLI: CGO disabled, nodbus build tag (matches Makefile $(BINARIES)).
    log "Building adder CLI (CGO_ENABLED=0 -tags nodbus)"
    CGO_ENABLED=0 GOOS=darwin GOARCH="${GOARCH}" \
        go build -ldflags "${ldflags}" -tags nodbus \
        -o "${BUILD_DIR}/bin/adder" \
        "${REPO_ROOT}/cmd/adder"

    # adder-tray GUI: CGO enabled for Fyne (matches Makefile build-tray).
    # NOTE: cross-compiling CGO requires the matching toolchain/SDK; CI must run
    # on the target arch (or have a configured cross toolchain).
    log "Building adder-tray GUI (CGO_ENABLED=1)"
    CGO_ENABLED=1 GOOS=darwin GOARCH="${GOARCH}" \
        go build -ldflags "${ldflags}" \
        -o "${BUILD_DIR}/bin/adder-tray" \
        "${REPO_ROOT}/cmd/adder-tray"
}

# ---------------------------------------------------------------------------
# 2. Assemble the Adder.app bundle
# ---------------------------------------------------------------------------

assemble_app() {
    log "Assembling ${APP_NAME}.app bundle"

    local app_root="${BUILD_DIR}/root/Applications/${APP_NAME}.app"
    local contents="${app_root}/Contents"
    local macos="${contents}/MacOS"
    local resources="${contents}/Resources"

    rm -rf "${BUILD_DIR}/root"
    mkdir -p "${macos}" "${resources}"

    # Both binaries keep their real names inside MacOS/.
    install -m 0755 "${BUILD_DIR}/bin/adder"      "${macos}/adder"
    install -m 0755 "${BUILD_DIR}/bin/adder-tray" "${macos}/adder-tray"

    # Icon -> Resources/adder.icns (CFBundleIconFile = "adder"). A missing icon
    # is fatal for a signed/release build (an iconless bundle ships broken), but
    # only a warning for local/ad-hoc dev where the asset may be intentionally
    # absent.
    if [ -f "${ICON_SRC}" ]; then
        install -m 0644 "${ICON_SRC}" "${resources}/adder.icns"
    elif [ -n "${SIGNING_IDENTITY}" ]; then
        die "Icon not found at ${ICON_SRC}; refusing to build a release bundle without it."
    else
        warn "Icon not found at ${ICON_SRC}; bundle will ship without an icon."
        warn "Provide an .icns at that path (or set ICON_SRC) for a complete app."
    fi

    # Info.plist with version tokens substituted.
    sed "s/__VERSION__/${VERSION}/g" \
        "${SCRIPT_DIR}/Info.plist" > "${contents}/Info.plist"

    # Strip removable extended attributes before signing. Quarantine flags,
    # resource forks, and Finder info on a Developer-ID-signed bundle break
    # codesign sealing, so clear them defensively. Note: macOS stamps a
    # kernel-managed com.apple.provenance xattr on Go-built binaries that
    # xattr cannot remove; it is benign and tolerated by notarization.
    xattr -cr "${app_root}" || true

    APP_BUNDLE="${app_root}"
}

# ---------------------------------------------------------------------------
# 3. Sign the .app bundle + inner binaries (Developer ID Application)
# ---------------------------------------------------------------------------

sign_app() {
    local contents="${APP_BUNDLE}/Contents"

    # Developer ID path: real code signing for distribution (CI / release).
    if [ -n "${SIGNING_IDENTITY}" ]; then
        log "Code signing with hardened runtime: ${SIGNING_IDENTITY}"

        # Sign inner Mach-O binaries first, then the bundle (inside-out).
        codesign --force --timestamp --options runtime \
            --sign "${SIGNING_IDENTITY}" \
            "${contents}/MacOS/adder"
        codesign --force --timestamp --options runtime \
            --sign "${SIGNING_IDENTITY}" \
            "${contents}/MacOS/adder-tray"

        codesign --force --timestamp --options runtime \
            --sign "${SIGNING_IDENTITY}" \
            "${APP_BUNDLE}"

        log "Verifying code signature"
        codesign --verify --deep --strict --verbose=2 "${APP_BUNDLE}"
        return 0
    fi

    # Ad-hoc path (ADHOC=1): sign the BUNDLE with the null identity ("-").
    # Unlike the Go linker's per-binary ad-hoc stub, signing the bundle binds
    # Info.plist so the app gets its real identifier (io.blinklabs.adder, not
    # "a.out") - macOS keys notification authorization off that, so the
    # first-run prompt never appears otherwise. Not notarizable; local/dev only.
    if [ "${ADHOC:-0}" = "1" ] || [ "${ADHOC:-}" = "true" ]; then
        warn "Ad-hoc signing (codesign -s -): LOCAL/DEV ONLY, not notarizable."
        # inside-out: binaries first, then the bundle
        codesign --force --sign - "${contents}/MacOS/adder"
        codesign --force --sign - "${contents}/MacOS/adder-tray"
        codesign --force --sign - "${APP_BUNDLE}"
        log "Verifying ad-hoc signature"
        codesign --verify --strict --verbose=2 "${APP_BUNDLE}"
        return 0
    fi

    warn "SIGNING_IDENTITY unset - SKIPPING code signing of binaries and .app."
    warn "The resulting pkg will NOT pass notarization or Gatekeeper."
    warn "For a runnable local build with working notifications, set ADHOC=1."
    return 0
}

# ---------------------------------------------------------------------------
# 4. Build component pkg + wrap with productbuild + distribution.xml
# ---------------------------------------------------------------------------

build_pkg() {
    log "Building component package (pkgbuild)"
    mkdir -p "${BUILD_DIR}" "${DIST_DIR}"

    # Generate a component plist with BundleIsRelocatable=false so macOS does
    # NOT silently retarget the install to any pre-existing Adder.app on disk
    # (Spotlight-indexed dev builds, prior installs, etc.). Without this the
    # postinstall script runs against the wrong /Applications path and the
    # install fails. See `man pkgbuild` ("Component Property List").
    local component_plist="${BUILD_DIR}/component.plist"
    pkgbuild --analyze --root "${BUILD_DIR}/root" "${component_plist}" >/dev/null
    /usr/libexec/PlistBuddy -c "Set :0:BundleIsRelocatable false" "${component_plist}"

    pkgbuild \
        --root "${BUILD_DIR}/root" \
        --component-plist "${component_plist}" \
        --identifier "${BUNDLE_ID}" \
        --version "${VERSION}" \
        --install-location "/" \
        --scripts "${SCRIPT_DIR}/scripts" \
        "${COMPONENT_PKG}"

    log "Building product archive (productbuild)"
    # Substitute tokens into a working copy of distribution.xml.
    local dist_xml="${BUILD_DIR}/distribution.xml"
    sed -e "s/__VERSION__/${VERSION}/g" \
        -e "s/__HOST_ARCH__/${HOST_ARCH}/g" \
        -e "s|__COMPONENT_PKG__|$(basename "${COMPONENT_PKG}")|g" \
        "${SCRIPT_DIR}/distribution.xml" > "${dist_xml}"

    productbuild \
        --distribution "${dist_xml}" \
        --package-path "${BUILD_DIR}" \
        "${UNSIGNED_PKG}"
}

# ---------------------------------------------------------------------------
# 5. Sign the pkg (Developer ID Installer) or pass through unsigned
# ---------------------------------------------------------------------------

sign_pkg() {
    mkdir -p "${DIST_DIR}"
    if [ -z "${INSTALLER_IDENTITY}" ]; then
        warn "INSTALLER_IDENTITY unset - SKIPPING pkg signing (productsign)."
        warn "Producing an UNSIGNED installer: ${FINAL_PKG}"
        # productsign cannot sign in place; here we simply move the unsigned pkg.
        cp -f "${UNSIGNED_PKG}" "${FINAL_PKG}"
        return 0
    fi

    log "Signing pkg with Developer ID Installer: ${INSTALLER_IDENTITY}"
    # productsign requires distinct input and output paths.
    productsign --sign "${INSTALLER_IDENTITY}" \
        "${UNSIGNED_PKG}" "${FINAL_PKG}"

    log "Verifying pkg signature"
    pkgutil --check-signature "${FINAL_PKG}"
}

# ---------------------------------------------------------------------------
# 6. Notarize + staple
# ---------------------------------------------------------------------------

notarize_pkg() {
    # Notarization only makes sense for a signed pkg.
    if [ -z "${INSTALLER_IDENTITY}" ]; then
        warn "Pkg is unsigned - SKIPPING notarization and stapling."
        return 0
    fi

    # Build notarytool auth arguments from whichever credential set is present.
    local -a notary_auth=()
    if [ -n "${NOTARY_PROFILE}" ]; then
        notary_auth=(--keychain-profile "${NOTARY_PROFILE}")
    elif [ -n "${APPLE_ID}" ] && [ -n "${APPLE_APP_PASSWORD}" ] && [ -n "${TEAM_ID}" ]; then
        notary_auth=(--apple-id "${APPLE_ID}" \
                     --password "${APPLE_APP_PASSWORD}" \
                     --team-id "${TEAM_ID}")
    else
        warn "No notarization credentials (NOTARY_PROFILE, or APPLE_ID + \
APPLE_APP_PASSWORD + TEAM_ID) - SKIPPING notarization and stapling."
        return 0
    fi

    log "Submitting to Apple notarization service (notarytool, --wait)"
    xcrun notarytool submit "${FINAL_PKG}" "${notary_auth[@]}" --wait

    log "Stapling notarization ticket"
    xcrun stapler staple "${FINAL_PKG}"

    log "Validating stapled ticket"
    xcrun stapler validate "${FINAL_PKG}"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
    log "Adder macOS installer build"
    log "  version : ${VERSION}"
    log "  arch    : ${PKG_ARCH} (GOARCH=${GOARCH})"
    log "  output  : ${FINAL_PKG}"

    build_binaries
    assemble_app
    sign_app
    build_pkg
    sign_pkg
    notarize_pkg

    log "Done: ${FINAL_PKG}"
    if [ -z "${INSTALLER_IDENTITY}" ]; then
        warn "This is an UNSIGNED, UN-notarized build (local/dev)."
        warn "It will be blocked by Gatekeeper on other machines."
    else
        log "Verify with: spctl -a -vv --type install \"${FINAL_PKG}\""
    fi
}

main "$@"
