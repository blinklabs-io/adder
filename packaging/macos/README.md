# macOS `.pkg` installer

This directory builds a macOS installer package, optionally signed and notarized, that
installs **both** the `adder` CLI and the `adder-tray` GUI inside a single
`Adder.app` bundle.

```
packaging/macos/
├── build-pkg.sh        # main build script (build → bundle → pkg → sign → notarize → staple)
├── distribution.xml    # productbuild distribution definition
├── Info.plist          # Adder.app metadata (CFBundleIdentifier = io.blinklabs.adder)
├── README.md           # this file
└── scripts/
    ├── preinstall      # quit a running instance before replacing the app
    └── postinstall     # refresh Launch Services; symlink CLI onto $PATH
```

## Installed layout

```
/Applications/Adder.app/Contents/MacOS/adder          # CLI binary
/Applications/Adder.app/Contents/MacOS/adder-tray     # tray GUI binary
/Applications/Adder.app/Contents/Info.plist
/Applications/Adder.app/Contents/Resources/adder.icns
/usr/local/bin/adder                                  # symlink → ...MacOS/adder
```

`CFBundleExecutable` is `adder-tray`, so double-clicking `Adder.app` launches the
tray/wizard GUI. The CLI ships alongside it in the same `MacOS/` directory.

The `postinstall` script creates `/usr/local/bin/adder` as a symlink to the
in-bundle binary so `adder` is on `$PATH` for shell use, creating
`/usr/local/bin` if needed. This is **best-effort**: it never fails the install
(the primary payload is the GUI app in `/Applications`). If `/usr/local` is
read-only — locked-down or MDM-managed Macs — the symlink is skipped with a
warning and the CLI remains runnable at
`/Applications/Adder.app/Contents/MacOS/adder`. The script preserves an existing non-Adder path when its target exists.
A dangling symlink can be replaced by its absent-path check.

To uninstall, disable configured autostart through the tray and quit it. Remove
`/Applications/Adder.app`; remove `/usr/local/bin/adder` only after checking
that the symlink points into that bundle. User configuration and logs remain
in their separate directories.

## What the installer does NOT do

- It does **not** register a LaunchAgent. Service registration is owned by the
  first-run wizard in `adder-tray`, which knows the user's chosen config. This
  keeps the installer generic.
- It does **not** add a Login Item. Start-at-login is offered by the tray's
  first-run wizard, which asks the user and manages the Login Item.

## Building

```bash
# Local, unsigned (dev): signing/notarization steps warn and skip.
./packaging/macos/build-pkg.sh

# Local, ad-hoc signed (dev): signs binaries and bundle.
ADHOC=1 ./packaging/macos/build-pkg.sh   # or: make pkg-macos-adhoc

# Signed + notarized (CI / release): set the env vars below.
SIGNING_IDENTITY="Developer ID Application: Blink Labs Software (<TEAM_ID>)" \
INSTALLER_IDENTITY="Developer ID Installer: Blink Labs Software (<TEAM_ID>)" \
TEAM_ID="<TEAM_ID>" \
NOTARY_PROFILE="adder-notary" \
VERSION="0.42.0" \
ARCH="arm64" \
  ./packaging/macos/build-pkg.sh
```

The artifact is written to `dist/adder-<version>-darwin-<arch>.pkg`
(e.g. `dist/adder-0.42.0-darwin-arm64.pkg`).

## Environment variables

| Variable             | Required for | Default                                   | Purpose |
| -------------------- | ------------ | ----------------------------------------- | ------- |
| `VERSION`            | optional     | `git describe --tags --always --dirty` (leading `v` stripped) | Installer / app version. For releases set a clean semver (e.g. `0.42.0`); `CFBundleVersion` must be ≤3 dot-separated integers, so the raw `git describe` form (`0.42.0-36-g…`) is only suitable for local dev builds. |
| `ARCH`               | optional     | `uname -m`                                | Accepts `arm64`/`aarch64` or `amd64`/`x86_64`. Maps to `GOARCH`; the pkg filename uses Go arch naming (`arm64`/`amd64`) to match the CI matrix `arch`. |
| `ADHOC`              | optional     | _(unset → skip)_                          | `1`/`true` → ad-hoc sign the `.app` when `SIGNING_IDENTITY` is unset. Local/dev only; not notarizable. See the local notification setup below. Ignored when `SIGNING_IDENTITY` is set. |
| `SIGNING_IDENTITY`   | code signing | _(unset → skip)_                          | **Developer ID Application** identity. Signs the binaries and `.app` with hardened runtime. |
| `INSTALLER_IDENTITY` | pkg signing  | _(unset → skip)_                          | **Developer ID Installer** identity. Signs the `.pkg` via `productsign`. |
| `TEAM_ID`            | notarization | _(unset)_                                 | Apple Developer Team ID. Required for the Apple-ID notarization fallback. |
| `NOTARY_PROFILE`     | notarization | _(unset)_                                 | `notarytool` keychain profile name (preferred auth). |
| `APPLE_ID`           | notarization | _(unset)_                                 | Apple ID email (fallback auth, used only if `NOTARY_PROFILE` unset). |
| `APPLE_APP_PASSWORD` | notarization | _(unset)_                                 | App-specific password for `APPLE_ID` (fallback auth). |
| `ICON_SRC`           | optional     | `.github/assets/Adder.icns`               | Source `.icns` copied to `Resources/adder.icns`. |
| `DIST_DIR`           | optional     | `<repo>/dist`                             | Where the final `.pkg` is written. |
| `BUILD_DIR`          | optional     | `<repo>/build/macos`                      | Scratch build/staging directory. |

### Skip-when-unset behavior

The script uses `set -euo pipefail` and treats all signing/notary vars as
optional (`${VAR:-}`):

- **`SIGNING_IDENTITY` unset** → release signing is skipped; `ADHOC=1` still enables local ad-hoc signing.
- **`INSTALLER_IDENTITY` unset** → the unsigned `.pkg` is copied to the final
  name and `productsign` is skipped (warns). Notarization is then also skipped.
- **No notary credentials** (`NOTARY_PROFILE`, or `APPLE_ID` + `APPLE_APP_PASSWORD`
  + `TEAM_ID`) → notarization and stapling are skipped (warns).

Without credentials the build produces an unsigned package. Signing and
notarization run only when configured and must succeed before the script
reports completion.

### Ad-hoc signing and notifications (`ADHOC=1`)

The ad-hoc path signs both binaries and then the bundle, binding its metadata
for local notification setup. It does not produce a notarized release. Use the
wizard's test notification to check OS permission on the installed bundle.

## Required credentials / secrets

To produce a release artifact you need an Apple Developer account with:

1. A **Developer ID Application** certificate (signs binaries / `.app`).
2. A **Developer ID Installer** certificate (signs the `.pkg`).
3. Notarization credentials, either:
   - a `notarytool` keychain profile, or
   - an Apple ID + app-specific password + Team ID.

Both certificates must be importable into the keychain on the build host.

### Setting up the notary keychain profile (preferred)

```bash
xcrun notarytool store-credentials "adder-notary" \
    --apple-id "you@example.com" \
    --team-id "<TEAM_ID>" \
    --password "app-specific-password"
```

Then pass `NOTARY_PROFILE=adder-notary`.

## Toolchain commands used

The script invokes the standard Xcode command-line tools:

```bash
# Build (mirrors the repo Makefile):
CGO_ENABLED=0 GOOS=darwin GOARCH=$GOARCH go build -ldflags "..." -tags nodbus -o adder      ./cmd/adder
CGO_ENABLED=1 GOOS=darwin GOARCH=$GOARCH go build -ldflags "..."              -o adder-tray ./cmd/adder-tray

# Sign binaries + bundle (hardened runtime, secure timestamp):
codesign --force --timestamp --options runtime --sign "$SIGNING_IDENTITY" Adder.app/Contents/MacOS/adder
codesign --force --timestamp --options runtime --sign "$SIGNING_IDENTITY" Adder.app/Contents/MacOS/adder-tray
codesign --force --timestamp --options runtime --sign "$SIGNING_IDENTITY" Adder.app
codesign --verify --deep --strict --verbose=2 Adder.app

# Component pkg + product archive:
pkgbuild --root build/macos/root --identifier io.blinklabs.adder --version "$VERSION" \
         --install-location / --scripts packaging/macos/scripts adder-component.pkg
productbuild --distribution distribution.xml --package-path build/macos adder-unsigned.pkg

# Sign the pkg (distinct input/output paths required):
productsign --sign "$INSTALLER_IDENTITY" adder-unsigned.pkg dist/adder-<version>-darwin-<arch>.pkg
pkgutil --check-signature dist/adder-<version>-darwin-<arch>.pkg

# Notarize (--wait) and staple:
xcrun notarytool submit dist/adder-<version>-darwin-<arch>.pkg --keychain-profile "$NOTARY_PROFILE" --wait
#   or: xcrun notarytool submit ... --apple-id "$APPLE_ID" --password "$APPLE_APP_PASSWORD" --team-id "$TEAM_ID" --wait
xcrun stapler staple   dist/adder-<version>-darwin-<arch>.pkg
xcrun stapler validate dist/adder-<version>-darwin-<arch>.pkg
```

## Verifying the release artifact

After a signed + notarized build, Gatekeeper should accept the installer:

```bash
spctl -a -vv --type install dist/adder-0.42.0-darwin-arm64.pkg
# => ... source=Notarized Developer ID
# => ... accepted
```


> Note: the `adder-tray` build requires CGO (Fyne). Build on a native macOS
> runner for the target architecture; cross-compiling CGO needs a matching SDK
> and toolchain.
