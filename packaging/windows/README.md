# Windows `.msi` installer

This directory builds a signed Windows installer (`.msi`) that installs **both**
the `adder` CLI (`adder.exe`) and the `adder-tray` GUI (`adder-tray.exe`) under
`%ProgramFiles%\Adder\`, with a Start Menu shortcut for the tray.

```
packaging/windows/
├── adder.wxs       # WiX v4 source (product, both binaries, shortcut, ARP)
├── build-msi.ps1   # main build script (build → wix build → jsign)
└── README.md       # this file
```

## Installed layout

```
%ProgramFiles%\Adder\adder.exe          # CLI binary
%ProgramFiles%\Adder\adder-tray.exe     # tray GUI binary
Start Menu\Programs\Adder               # shortcut → adder-tray.exe
```

Double-clicking the Start Menu shortcut launches the tray/wizard GUI. The CLI
ships alongside it in the same `Adder` directory. An Add/Remove Programs (ARP)
entry is created with publisher **Blink Labs Software**, contact, and the
support / about URL pointing at <https://github.com/blinklabs-io/adder>.

## What the installer does NOT do

- It does **not** register a Scheduled Task. Service registration is owned by
  the `adder-tray` first-run wizard (which knows the user's chosen config and
  uses `schtasks.exe` at `tray/setup/service_windows.go`). This keeps the
  installer generic and mirrors the macOS installer's "no LaunchAgent" stance.

## WiX version

This uses **WiX v4** (`wix build`, single command), not the older v3
`candle`/`light` pair. v4 is installed as a .NET tool and pinned in the build
script (`WIX_VERSION`, default `4.0.5`):

```powershell
dotnet tool install --global wix --version 4.0.5
wix extension add -g WixToolset.Util.wixext   # not required by this .wxs
```

v4 collapses `<Product>`/`<Package>` into a single `<Package>`, uses the
`http://wixtoolset.org/schemas/v4/wxs` namespace, and provides
`<StandardDirectory>` for well-known folders — all reflected in `adder.wxs`.

## Building

```powershell
# Local, unsigned (dev): signing warns and skips.
pwsh ./packaging/windows/build-msi.ps1

# Signed (CI / release): set VERSION and the jsign env vars below.
$env:VERSION        = "0.39.1"
$env:JSIGN_KEYSTORE = "https://adder-kv.vault.azure.net"
$env:JSIGN_STORETYPE= "AZUREKEYVAULT"
$env:JSIGN_STOREPASS= "<oauth-token-or-credential>"
$env:JSIGN_ALIAS    = "adder-ev-cert"
pwsh ./packaging/windows/build-msi.ps1
```

The artifact is written to `dist\adder-<version>-windows-<arch>.msi`
(e.g. `dist\adder-0.39.1-windows-amd64.msi`).

## Environment variables

| Variable          | Required for | Default                                   | Purpose |
| ----------------- | ------------ | ----------------------------------------- | ------- |
| `VERSION`         | optional     | `git describe --tags --always --dirty` (leading `v` stripped) | Installer version. The MSI `ProductVersion` must be ≤3 dot-separated integers, so the script sanitizes to the first `a.b.c`; for releases set a clean semver. |
| `ARCH`            | optional     | `amd64`                                   | `amd64` or `arm64`. Maps to `GOARCH` and the msi name. |
| `WIX_VERSION`     | optional     | `4.0.5`                                   | WiX toolset version to install/use. |
| `ICON_SRC`        | optional     | `.github\assets\adder.ico`                | App icon embedded for ARP and the shortcut. |
| `DIST_DIR`        | optional     | `<repo>\dist`                             | Where the final `.msi` is written. |
| `BUILD_DIR`       | optional     | `<repo>\build\windows`                    | Scratch build/staging directory. |
| `ADDER_EXE`       | optional     | _(unset → script runs `go build`)_        | Path to a prebuilt `adder.exe` (typically already code-signed). When both this and `ADDER_TRAY_EXE` resolve to existing files, the script's internal `go build` step is skipped and these files are packaged directly. CI uses this to feed in individually-signed binaries — the MSI signature only covers the OLE compound, not the embedded PEs. |
| `ADDER_TRAY_EXE`  | optional     | _(unset → script runs `go build`)_        | Path to a prebuilt `adder-tray.exe`. See `ADDER_EXE`. |
| `JSIGN_KEYSTORE`  | signing      | _(unset → skip)_                          | Keystore reference: cloud HSM/KMS name (e.g. Key Vault URL), PKCS#11 config, or `.p12` path holding the EV certificate. |
| `JSIGN_STOREPASS` | signing      | _(unset → skip)_                          | Keystore / token password or cloud credential. |
| `JSIGN_STORETYPE` | signing      | _(unset)_                                 | jsign storetype: `AZUREKEYVAULT`, `GOOGLECLOUD`, `AWS`, `DIGICERTONE`, `PKCS11`, `PKCS12`, … |
| `JSIGN_ALIAS`     | signing      | _(unset)_                                 | Certificate / key alias within the keystore. |
| `JSIGN_TSAURL`    | optional     | `http://timestamp.digicert.com`           | RFC 3161 timestamp authority URL (CI timestamps the signature). |
| `JSIGN_CERTFILE`  | optional     | _(unset → chain inferred from keystore)_  | Path to a PEM file with the full certificate chain (intermediates + root). Needed when the keystore holds only the leaf cert (typical for cloud HSM/KMS-backed EV keys). |
| `JSIGN_JAR`       | optional     | _(unset → use `jsign` on PATH)_           | Path to `jsign.jar` (invoked via `java -jar`) if no `jsign` launcher is on PATH. |

### Skip-when-unset behavior

The script uses `Set-StrictMode` + `$ErrorActionPreference = 'Stop'` and treats
all signing vars as optional:

- **`JSIGN_KEYSTORE` or `JSIGN_STOREPASS` unset** → the `.msi` is left unsigned
  and `jsign` is skipped (warns). A plain local run always yields a working
  **unsigned** msi; CI with the secrets set yields a **signed + timestamped** msi.

## Binary build commands

Mirrors the repo `Makefile`:

```powershell
# adder CLI: pure Go.
$env:CGO_ENABLED="0"; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -ldflags "-s -w -X '<module>/internal/version.Version=...' -X '<module>/internal/version.CommitHash=...'" -tags nodbus -o adder.exe ./cmd/adder

# adder-tray GUI: requires cgo (Fyne → go-gl/OpenGL).
$env:CGO_ENABLED="1"; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -ldflags "..." -o adder-tray.exe ./cmd/adder-tray
```

> **Toolchain requirement (important):** `adder-tray.exe` **cannot be
> cross-compiled** from macOS/Linux. Fyne pulls in `go-gl/gl`, whose files are
> excluded unless cgo is enabled, so a C compiler (mingw-w64 `gcc` for `amd64`)
> must be on `PATH`. Build on a **native Windows runner**. The `adder` CLI is
> pure Go and *can* be cross-built, but the script builds both on the Windows
> runner for consistency (the direct analog of the macOS installer building on a
> native macOS runner for the target arch).

`arm64` is supported via `ARCH=arm64`; it needs an `aarch64` Windows toolchain
for the tray build.

## Signing with jsign

The MSI is signed with the existing EV certificate using
[jsign](https://github.com/ebourg/jsign). MSI is an OLE compound file, which
jsign signs natively (no need for `signtool` on the build host). The exact
invocation the script runs:

```bash
jsign \
  --keystore   "$JSIGN_KEYSTORE" \
  --storepass  "$JSIGN_STOREPASS" \
  --storetype  "$JSIGN_STORETYPE" \
  --alias      "$JSIGN_ALIAS" \
  --tsaurl     "$JSIGN_TSAURL" \
  --name       "Adder" \
  --url        "https://github.com/blinklabs-io/adder" \
  adder-<version>-windows-amd64.msi
```

The `--tsaurl` timestamps the signature so it remains valid after the
certificate expires (CI performs the timestamping at sign time).

Because EV code-signing keys live on an HSM, `JSIGN_STORETYPE` selects the
backend (e.g. `AZUREKEYVAULT`, `GOOGLECLOUD`, `AWS`, `DIGICERTONE`, or `PKCS11`
for a physical/cloud token). For a software `.p12` (non-EV / testing), use
`JSIGN_STORETYPE=PKCS12` with `JSIGN_KEYSTORE` pointing at the `.p12` file.

## Verifying the release artifact

On a Windows host with the Windows SDK (`signtool`):

```cmd
signtool verify /pa adder-<version>-windows-amd64.msi
```

`/pa` uses the default authentication-code policy and should report a valid
signature chain plus a countersignature (timestamp).

## Validation: local vs. CI

`adder.wxs` is validated for **XML well-formedness** locally
(`xmllint --noout packaging/windows/adder.wxs`). Its **WiX/ICE semantics**
(64-bit folder resolution, component keypaths, per-machine/per-user
consistency, upgrade plumbing) are only checked by `wix build`, which runs ICE
validation by default — that happens on the Windows CI runner, not on macOS.
The `.wxs` is written to be ICE-clean (single component per shortcut, `HKMU`
keypath under a `perMachine` scope, `Bitness="always64"`), but the authoritative
lint is the CI `wix build` step.

## CI wiring

Implemented in [`.github/workflows/publish.yml`](../../.github/workflows/publish.yml)
on the `build-binaries` job, for the `windows-latest`/amd64 and
`windows-11-arm`/arm64 matrix rows, gated on tag pushes (`refs/tags/v*.*.*`).

The flow per Windows row:

1. `make build` + `make build-tray` produce `adder.exe` and `adder-tray.exe`
   at the workspace root (CGO toolchain via MSYS2 `MINGW64` / `CLANGARM64`).
2. **Sign Windows binaries** — downloads `jsign-6.0.jar` (SHA256-pinned),
   decodes `CERTIFICATE_CHAIN` to `codesign-chain.pem`, then loops jsign over
   both `.exe` files (RFC3161 timestamp via GlobalSign), using a masked
   `gcloud auth print-access-token` as `--storepass`.
3. **Install WiX toolset** — `dotnet tool install --global wix --version 4.0.5`.
4. **Build and sign MSI** — invokes `pwsh ./packaging/windows/build-msi.ps1`
   with `ADDER_EXE` / `ADDER_TRAY_EXE` pointing at the just-signed binaries
   (so the script skips its internal `go build`), and the `JSIGN_*` env vars
   mapped from the existing `CERTIFICATE_*` secrets and a fresh access token.
   Cleans up `jsign.jar` + `codesign-chain.pem` after.
5. **Verify MSI signature** — locates `signtool.exe` under the preinstalled
   Windows SDK and runs `signtool verify /pa /v <msi>`; fails the job on a
   bad chain or missing countersignature.
6. **Upload MSI release asset** — uploads
   `dist\adder-<version>-windows-<arch>.msi` to the draft release, naming the
   asset `adder-<RELEASE_TAG>-windows-<arch>.msi` (the existing convention).
7. **Attest MSI** — `actions/attest@v4` produces a build provenance
   attestation for the `.msi`.

Reuses the existing repository secrets (no new ones required):
`CERTIFICATE_KEYSTORE`, `CERTIFICATE_STORE_TYPE`, `CERTIFICATE_KEY_NAME`,
`CERTIFICATE_CHAIN`, `CERTIFICATE_SA_CREDENTIALS`. These map onto the
`JSIGN_*` env vars consumed by `build-msi.ps1`.
