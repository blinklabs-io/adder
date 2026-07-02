# macOS `.pkg` installer

This directory builds a signed and notarized macOS installer package that
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
`/Applications/Adder.app/Contents/MacOS/adder`. The script also refuses to
overwrite a pre-existing `/usr/local/bin/adder` that isn't already ours (a
Homebrew formula, another tool, or a user symlink), leaving it untouched.

To uninstall: `sudo rm -rf /Applications/Adder.app /usr/local/bin/adder`.

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

# Local, ad-hoc signed (dev): app runs AND notifications work (see note below).
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
| `ADHOC`              | optional     | _(unset → skip)_                          | `1`/`true` → ad-hoc sign the `.app` when `SIGNING_IDENTITY` is unset. Local/dev only; not notarizable. Needed for working notifications (see below). Ignored when `SIGNING_IDENTITY` is set. |
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

- **`SIGNING_IDENTITY` unset** → binaries and `.app` are not code-signed (warns).
- **`INSTALLER_IDENTITY` unset** → the unsigned `.pkg` is copied to the final
  name and `productsign` is skipped (warns). Notarization is then also skipped.
- **No notary credentials** (`NOTARY_PROFILE`, or `APPLE_ID` + `APPLE_APP_PASSWORD`
  + `TEAM_ID`) → notarization and stapling are skipped (warns).

So a plain local run always yields a working **unsigned** pkg, while CI with the
secrets set yields a **signed + notarized + stapled** pkg.

### Ad-hoc signing and notifications (`ADHOC=1`)

A fully **unsigned** local build installs and launches, but the tray's
notification permission prompt never appears. macOS notification authorization
keys off the bundle's code-signing identity: on Apple Silicon the Go linker
already stamps each binary with an automatic ad-hoc signature, but that does
**not** bind `Info.plist` or seal `Resources/`, so the bundle reports
`Identifier=a.out` and the notification center refuses to prompt.

`ADHOC=1` runs `codesign --force --sign -` on the binaries **and the bundle**
(inside-out), which binds `Info.plist` and gives the app its real
`io.blinklabs.adder` identifier. That is enough for first-run notification
authorization to fire. The `.pkg` itself stays unsigned (ad-hoc cannot satisfy
`productsign`/notarization), so install it via `sudo installer -pkg <pkg>
-target /` and expect `spctl --type install` to reject it — that "accepted"
verdict only comes from the CI signed + notarized build. `ADHOC` is ignored when
`SIGNING_IDENTITY` is set (the real Developer ID signature supersedes it).

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

## Known / benign: AppleDouble entries in the payload

`pkgutil --payload-files` on the built pkg lists `._*` (AppleDouble) entries
alongside the real files, e.g. `._adder`, `._Info.plist`. This is normal
`pkgbuild` behavior — these carry extended attributes into the cpio payload and
are reconstructed onto the real files at install time; no `._*` files land on
disk. Apple's own packages contain them.

Relatedly, macOS (Sequoia and later) stamps a kernel-managed
`com.apple.provenance` extended attribute on Go-built binaries that `xattr -c`
cannot remove. It is benign and tolerated by notarization. The build strips all
*removable* xattrs (quarantine, resource forks, Finder info) before signing,
which are the attributes that actually break `codesign` sealing.

## CI wiring

The release workflow `.github/workflows/publish.yml` runs `build-pkg.sh` from
the `build-binaries` matrix on a native macOS runner per arch (arm64 on
`macos-15`, amd64 on `macos-15-intel`) and uploads each signed + notarized `.pkg` as a
release asset. Building inline on native runners avoids cross-compiling the
CGO/Fyne `adder-tray`. Each darwin job:

1. Imports both Developer ID certificates into a temporary keychain via
   `security create-keychain` / `security import` (Application cert under
   `APPLE_CERTIFICATE`, Installer cert under `APPLE_INSTALLER_CERTIFICATE`).
2. Stores notarytool credentials with `xcrun notarytool store-credentials`
   under the profile name `adder-notary`.
3. Exports `SIGNING_IDENTITY`, `INSTALLER_IDENTITY`, `TEAM_ID`,
   `NOTARY_PROFILE`, `VERSION`, `ARCH` and runs `./packaging/macos/build-pkg.sh`.
4. Asserts `spctl -a -vv --type install dist/adder-*.pkg` reports both
   `accepted` and `source=Notarized Developer ID`; fails the job otherwise.
5. Uploads `dist/adder-<version>-darwin-arm64.pkg` as a release asset and
   attests it with `actions/attest`.

### Required GitHub Actions secrets

| Secret                                   | Format                                | Used for                                  |
| ---------------------------------------- | ------------------------------------- | ----------------------------------------- |
| `APPLE_CERTIFICATE`                      | base64 of Developer ID Application `.p12` | `codesign` binaries + `.app` |
| `APPLE_CERTIFICATE_PASSWORD`             | string                                | password for the Application `.p12` |
| `APPLE_INSTALLER_CERTIFICATE`            | base64 of Developer ID Installer `.p12`   | `productsign` the `.pkg` |
| `APPLE_INSTALLER_CERTIFICATE_PASSWORD`   | string                                | password for the Installer `.p12` |
| `APPLE_KEYCHAIN_PASSWORD`                | string                                | password of the temporary CI keychain |
| `APPLE_ID`                               | email                                 | notarytool auth |
| `APPLE_APP_SPECIFIC_PASSWORD`            | app-specific password                 | notarytool auth |
| `APPLE_TEAM_ID`                          | 10-char team ID                       | notarytool auth + identity string |

To export the Installer cert as a `.p12` from the keychain on a Mac that
already has it installed:

```bash
security export -k login.keychain -t identities -f pkcs12 \
    -P "<password>" -o installer.p12 \
    "Developer ID Installer: Blink Labs Software (<TEAM_ID>)"
base64 -i installer.p12 | pbcopy   # paste into the GitHub secret
```

> Note: the `adder-tray` build requires CGO (Fyne). Build on a native macOS
> runner for the target architecture; cross-compiling CGO needs a matching SDK
> and toolchain.
