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

# build-msi.ps1 - build a (optionally signed) Windows .msi installer containing
# both the `adder` CLI (adder.exe) and the `adder-tray` GUI (adder-tray.exe),
# installed under %ProgramFiles%\Adder\, with a Start Menu shortcut for the tray.
#
# The script is fully parameterized via environment variables. Signing is
# SKIPPED with a clear warning when the jsign credentials are not provided, so
# it produces an UNSIGNED msi locally and a signed msi in CI. See
# packaging/windows/README.md for the full env-var contract and verification.
#
# Run on a NATIVE Windows runner: adder-tray uses Fyne (CGO -> go-gl/OpenGL)
# and cannot be cross-compiled. A C toolchain (e.g. mingw-w64 gcc) must be on
# PATH. The CLI is pure Go and could be cross-built, but we build both here for
# consistency (this mirrors the macOS installer building on a native runner).

#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

function Write-Log  { param([string]$m) Write-Host "==> $m" -ForegroundColor Blue }
function Write-Warn { param([string]$m) Write-Warning $m }
function Die        { param([string]$m) Write-Error $m; exit 1 }

# ---------------------------------------------------------------------------
# Configuration (all overridable via environment)
# ---------------------------------------------------------------------------

# Resolve repository paths relative to this script so it works from any CWD.
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot  = (Resolve-Path (Join-Path $ScriptDir '..\..')).Path

# Go module path, used for the version ldflags (mirrors the Makefile).
$GoModule = ((Select-String -Path (Join-Path $RepoRoot 'go.mod') -Pattern '^module').Line -split '\s+')[1]

# Version string. Defaults to the git tag/description; strips a leading "v".
# NOTE: MSI ProductVersion must be <=3 dot-separated integers (a.b.c). The raw
# `git describe` form (e.g. 0.39.1-36-g1234567) is NOT a valid MSI version, so
# for releases set VERSION to a clean semver. We sanitize for local dev builds.
$Version = $env:VERSION
if ([string]::IsNullOrEmpty($Version)) {
    $Version = (& git -C $RepoRoot describe --tags --always --dirty 2>$null)
    if ([string]::IsNullOrEmpty($Version)) { $Version = '0.0.0' }
}
$Version = $Version -replace '^v', ''

# Sanitize to the first 3 numeric dotted fields for the MSI ProductVersion.
$MsiVersion = '0.0.0'
$m = [regex]::Match($Version, '^(\d+(?:\.\d+){0,2})')
if ($m.Success) { $MsiVersion = $m.Value }
if ($MsiVersion -ne $Version) {
    Write-Warn "VERSION '$Version' is not a valid MSI version; using '$MsiVersion' for the MSI ProductVersion. Set VERSION to a clean semver for releases."
}

# Commit hash for the version ldflags.
$CommitHash = (& git -C $RepoRoot rev-parse --short HEAD 2>$null)
if ([string]::IsNullOrEmpty($CommitHash)) { $CommitHash = 'unknown' }

# Target architecture: amd64 (default) or arm64.
$Arch = $env:ARCH
if ([string]::IsNullOrEmpty($Arch)) { $Arch = 'amd64' }
# $MsiArch feeds the output filename (release convention: amd64/arm64).
# $WixArch is the WiX `-arch` platform token, which uses x64/arm64 (NOT amd64).
switch ($Arch) {
    'amd64' { $GoArch = 'amd64'; $MsiArch = 'amd64'; $WixArch = 'x64' }
    'x86_64'{ $GoArch = 'amd64'; $MsiArch = 'amd64'; $WixArch = 'x64' }
    'arm64' { $GoArch = 'arm64'; $MsiArch = 'arm64'; $WixArch = 'arm64' }
    default { Die "unsupported ARCH '$Arch' (use amd64 or arm64)" }
}

# Pinned WiX toolset version (installed as a dotnet global/local tool in CI).
$WixVersion = if ($env:WIX_VERSION) { $env:WIX_VERSION } else { '4.0.5' }

# Output / work locations.
$DistDir  = if ($env:DIST_DIR)  { $env:DIST_DIR }  else { Join-Path $RepoRoot 'dist' }
$BuildDir = if ($env:BUILD_DIR) { $env:BUILD_DIR } else { Join-Path $RepoRoot 'build\windows' }
$BinDir   = Join-Path $BuildDir 'bin'
$MsiName  = "adder-$Version-windows-$MsiArch.msi"
$FinalMsi = Join-Path $DistDir $MsiName

# Pre-built binary inputs. When BOTH ADDER_EXE and ADDER_TRAY_EXE point at
# existing files, the Build-Binaries step is skipped and these files are
# packaged directly. CI uses this to feed in binaries that have already been
# individually code-signed by an earlier step (signing inside the MSI requires
# that the embedded PEs were signed BEFORE the MSI was built; jsign of the MSI
# itself does not sign the contents).
$AdderExeIn     = $env:ADDER_EXE
$AdderTrayExeIn = $env:ADDER_TRAY_EXE

# Icon source (already present in the repo).
$IconSrc = if ($env:ICON_SRC) { $env:ICON_SRC } else { Join-Path $RepoRoot '.github\assets\adder.ico' }

# Signing (jsign) parameters - empty => skip signing with a warning.
#   JSIGN_JAR        path to jsign.jar (or rely on a `jsign` launcher on PATH)
#   JSIGN_KEYSTORE   keystore: a cloud HSM/KMS name, PKCS#11 config, or .p12 path
#   JSIGN_STOREPASS  keystore / token password (or cloud credential token)
#   JSIGN_STORETYPE  e.g. AZUREKEYVAULT, GOOGLECLOUD, AWS, DIGICERTONE, PKCS11, PKCS12
#   JSIGN_ALIAS      certificate/key alias within the keystore
#   JSIGN_TSAURL     timestamp authority URL (CI timestamps the sig)
#   JSIGN_TSMODE     timestamp protocol: RFC3161 (default) or AUTHENTICODE.
#                    Must match the TSA: RFC3161-only endpoints (e.g. GlobalSign
#                    r6advanced1) return HTTP 415 under the AUTHENTICODE default.
#   JSIGN_CERTFILE   optional PEM with the full cert chain (intermediates + root)
#   JSIGN_ALG        signature digest algorithm (default SHA-256)
#   JSIGN_TSRETRIES  jsign's native timestamping retries (default 3) - TSAs flake
#   JSIGN_TSRETRYWAIT seconds jsign waits between timestamp retries (default 10)
$JsignJar      = $env:JSIGN_JAR
$JsignKeystore = $env:JSIGN_KEYSTORE
$JsignStorePass= $env:JSIGN_STOREPASS
$JsignStoreType= $env:JSIGN_STORETYPE
$JsignAlias    = $env:JSIGN_ALIAS
$JsignCertFile = $env:JSIGN_CERTFILE
$JsignTsaUrl   = if ($env:JSIGN_TSAURL)  { $env:JSIGN_TSAURL }  else { 'http://timestamp.digicert.com' }
$JsignTsMode   = if ($env:JSIGN_TSMODE)  { $env:JSIGN_TSMODE }  else { 'RFC3161' }
$JsignAlg        = if ($env:JSIGN_ALG)         { $env:JSIGN_ALG }              else { 'SHA-256' }
$JsignTsRetries  = if ($env:JSIGN_TSRETRIES)   { [int]$env:JSIGN_TSRETRIES }   else { 3 }
$JsignTsRetryWait= if ($env:JSIGN_TSRETRYWAIT) { [int]$env:JSIGN_TSRETRYWAIT } else { 10 }

# ---------------------------------------------------------------------------
# 1. Build both binaries
# ---------------------------------------------------------------------------

function Build-Binaries {
    Write-Log "Building binaries (version=$Version, commit=$CommitHash, arch=$GoArch)"

    # Mirror the Makefile's version ldflags pattern exactly.
    $ldflags = "-s -w " +
        "-X '$GoModule/internal/version.Version=$Version' " +
        "-X '$GoModule/internal/version.CommitHash=$CommitHash'"

    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

    $env:GOOS   = 'windows'
    $env:GOARCH = $GoArch

    # adder CLI: CGO disabled, nodbus build tag (matches Makefile $(BINARIES)).
    Write-Log "Building adder.exe (CGO_ENABLED=0 -tags nodbus)"
    $env:CGO_ENABLED = '0'
    & go build -ldflags $ldflags -tags nodbus `
        -o (Join-Path $BinDir 'adder.exe') (Join-Path $RepoRoot 'cmd\adder')
    if ($LASTEXITCODE -ne 0) { Die 'adder.exe build failed' }

    # adder-tray GUI: CGO enabled for Fyne (matches Makefile build-tray).
    # NOTE: Fyne pulls in go-gl/OpenGL, which REQUIRES cgo and a C compiler
    # (mingw-w64 gcc). This cannot be cross-compiled from macOS/Linux; build
    # on a native Windows runner with gcc on PATH.
    Write-Log "Building adder-tray.exe (CGO_ENABLED=1)"
    $env:CGO_ENABLED = '1'
    & go build -ldflags $ldflags `
        -o (Join-Path $BinDir 'adder-tray.exe') (Join-Path $RepoRoot 'cmd\adder-tray')
    if ($LASTEXITCODE -ne 0) { Die 'adder-tray.exe build failed (need cgo + a C toolchain such as mingw-w64 gcc)' }
}

# ---------------------------------------------------------------------------
# 2. Build the MSI with the WiX toolchain
# ---------------------------------------------------------------------------

function Build-Msi {
    Write-Log "Building MSI with WiX v$WixVersion"
    New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

    if (-not (Get-Command wix -ErrorAction SilentlyContinue)) {
        Die "WiX 'wix' command not found. Install the pinned toolset with: dotnet tool install --global wix --version $WixVersion"
    }

    # Prefer caller-supplied (typically pre-signed) binaries; fall back to the
    # binaries Build-Binaries placed under $BinDir.
    $AdderExeSrc     = if ($AdderExeIn)     { $AdderExeIn }     else { Join-Path $BinDir 'adder.exe' }
    $AdderTrayExeSrc = if ($AdderTrayExeIn) { $AdderTrayExeIn } else { Join-Path $BinDir 'adder-tray.exe' }
    if (-not (Test-Path $AdderExeSrc))     { Die "adder.exe not found at $AdderExeSrc" }
    if (-not (Test-Path $AdderTrayExeSrc)) { Die "adder-tray.exe not found at $AdderTrayExeSrc" }

    & wix build (Join-Path $ScriptDir 'adder.wxs') `
        -arch $WixArch `
        -d "Version=$MsiVersion" `
        -d "AdderExe=$AdderExeSrc" `
        -d "AdderTrayExe=$AdderTrayExeSrc" `
        -d "AdderIco=$IconSrc" `
        -o $FinalMsi
    if ($LASTEXITCODE -ne 0) { Die 'wix build failed' }

    Write-Log "Built: $FinalMsi"
}

# ---------------------------------------------------------------------------
# 3. Sign the MSI with the EV certificate via jsign (or skip with a warning)
# ---------------------------------------------------------------------------

function Sign-Msi {
    if ([string]::IsNullOrEmpty($JsignKeystore) -or [string]::IsNullOrEmpty($JsignStorePass)) {
        Write-Warn "JSIGN_KEYSTORE / JSIGN_STOREPASS unset - SKIPPING code signing (jsign)."
        Write-Warn "Producing an UNSIGNED installer: $FinalMsi"
        Write-Warn "It will trigger SmartScreen / 'Unknown publisher' on other machines."
        return
    }

    Write-Log "Signing MSI with jsign (storetype=$JsignStoreType, alias=$JsignAlias)"

    # Build the jsign argument list. `--alg` and `--tsmode` are pinned rather
    # than left to jsign defaults; `--tsretries`/`--tsretrywait` are jsign's
    # native timestamp retry (the TSA is the part that flakes).
    $jsignArgs = @(
        '--keystore',    $JsignKeystore,
        '--storepass',   $JsignStorePass,
        '--alg',         $JsignAlg,
        '--tsaurl',      $JsignTsaUrl,
        '--tsmode',      $JsignTsMode,
        '--tsretries',   $JsignTsRetries,
        '--tsretrywait', $JsignTsRetryWait,
        '--name',        'Adder',
        '--url',         'https://github.com/blinklabs-io/adder'
    )
    if (-not [string]::IsNullOrEmpty($JsignStoreType)) { $jsignArgs += @('--storetype', $JsignStoreType) }
    if (-not [string]::IsNullOrEmpty($JsignAlias))     { $jsignArgs += @('--alias', $JsignAlias) }
    if (-not [string]::IsNullOrEmpty($JsignCertFile))  { $jsignArgs += @('--certfile', $JsignCertFile) }
    $jsignArgs += $FinalMsi

    if (-not [string]::IsNullOrEmpty($JsignJar)) {
        & java -jar $JsignJar @jsignArgs
    } elseif (Get-Command jsign -ErrorAction SilentlyContinue) {
        & jsign @jsignArgs
    } else {
        Die "jsign not found: set JSIGN_JAR to jsign.jar or install a 'jsign' launcher on PATH."
    }
    if ($LASTEXITCODE -ne 0) { Die 'jsign signing failed' }

    # Independently confirm the MSI is signed, chain-trusted AND timestamped.
    # (jsign can exit 0 while the timestamp step silently failed.) Windows-only
    # cmdlet; skipped on non-Windows dev hosts where signing is not exercised.
    if (Get-Command Get-AuthenticodeSignature -ErrorAction SilentlyContinue) {
        $sig = Get-AuthenticodeSignature $FinalMsi
        if ($sig.Status -ne 'Valid') { Die "MSI signature not valid: $($sig.Status)" }
        if ($null -eq $sig.TimeStamperCertificate) { Die 'MSI signature is not timestamped' }
        Write-Log "Verified MSI signature: Valid, timestamped"
    }

    Write-Log "Signed. Verify on Windows with: signtool verify /pa $MsiName"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

Write-Log 'Adder Windows installer build'
Write-Log "  version : $Version (msi: $MsiVersion)"
Write-Log "  arch    : $MsiArch (GOARCH=$GoArch)"
Write-Log "  output  : $FinalMsi"

if ($AdderExeIn -and $AdderTrayExeIn -and (Test-Path $AdderExeIn) -and (Test-Path $AdderTrayExeIn)) {
    Write-Log "Skipping go build: using prebuilt binaries"
    Write-Log "  adder.exe      : $AdderExeIn"
    Write-Log "  adder-tray.exe : $AdderTrayExeIn"
} else {
    Build-Binaries
}
Build-Msi
Sign-Msi

Write-Log "Done: $FinalMsi"
if ([string]::IsNullOrEmpty($JsignKeystore) -or [string]::IsNullOrEmpty($JsignStorePass)) {
    Write-Warn 'This is an UNSIGNED build (local/dev). It will warn under SmartScreen on other machines.'
} else {
    Write-Log "Verify with: signtool verify /pa $MsiName"
}
