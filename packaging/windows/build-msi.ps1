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

# Mesa3D software OpenGL bundling. The Fyne tray needs OpenGL 2.1+; VM /
# headless / RDP hosts (e.g. VirtualBox) often have no hardware OpenGL driver,
# so we bundle Mesa's llvmpipe software renderer (opengl32.dll loader +
# libgallium_wgl.dll megadriver) next to adder-tray.exe.
#   BUNDLE_MESA=0    disable bundling (GUI then needs a host OpenGL driver)
#   MESA_VERSION     pinned mesa-dist-win release tag (default below)
#   MESA_SHA256      expected SHA-256 of the downloaded archive (default matches
#                    MESA_VERSION below). Set this when overriding MESA_VERSION.
#   MESA_OPENGL_DIR  use DLLs from an already-extracted dir instead of download
# amd64 only: upstream (pal1000/mesa-dist-win) ships no arm64 build, so bundling
# is skipped for arm64 with a warning.
$BundleMesa   = if ($null -ne $env:BUNDLE_MESA) { $env:BUNDLE_MESA } else { '1' }
$MesaVersion  = if ($env:MESA_VERSION)  { $env:MESA_VERSION }  else { '26.1.3' }
# SHA-256 of mesa3d-26.1.3-release-mingw.7z. A downloaded archive is verified
# against this before extraction so a tampered/MITM'd payload cannot be baked
# into the signed MSI. When bumping MESA_VERSION, set MESA_SHA256 to the new
# archive's hash (a mismatch fails the build rather than shipping unverified
# binaries).
$MesaSha256   = if ($env:MESA_SHA256) { $env:MESA_SHA256 } else { '80d5add64254c839b4c784bdab6a2b504e448675604b0fe54a9bce3c543303a7' }
$MesaOpenGlDir = $env:MESA_OPENGL_DIR

# Resolved, staged Mesa file paths (empty => not bundled; the .wxs guards on
# these being non-empty). Populated by Resolve-Mesa.
$script:MesaOpenGl  = ''
$script:MesaGallium = ''
$script:MesaLicense = ''

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
    # -H=windowsgui links against the GUI subsystem so the tray does not
    # spawn a console window on launch (the CLI keeps its console).
    Write-Log "Building adder-tray.exe (CGO_ENABLED=1, -H=windowsgui)"
    $env:CGO_ENABLED = '1'
    $trayLdflags = "$ldflags -H=windowsgui"
    & go build -ldflags $trayLdflags `
        -o (Join-Path $BinDir 'adder-tray.exe') (Join-Path $RepoRoot 'cmd\adder-tray')
    if ($LASTEXITCODE -ne 0) { Die 'adder-tray.exe build failed (need cgo + a C toolchain such as mingw-w64 gcc)' }
}

# ---------------------------------------------------------------------------
# 1b. Fetch + stage Mesa3D software OpenGL (llvmpipe) next to the tray
# ---------------------------------------------------------------------------

function Resolve-Mesa {
    if ($BundleMesa -in @('0', 'false', 'no', 'off')) {
        Write-Warn "BUNDLE_MESA=$BundleMesa - NOT bundling Mesa software OpenGL. The GUI will require a host OpenGL driver."
        return
    }
    if ($GoArch -ne 'amd64') {
        Write-Warn "No upstream arm64 Mesa build available - skipping Mesa bundling for $MsiArch. The GUI will require a host OpenGL driver."
        return
    }

    # $BinDir is the staging area; it may not exist yet when prebuilt binaries
    # were supplied (Build-Binaries was skipped).
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

    # Source of the extracted Mesa tree: an override dir, else download+extract.
    $mesaRoot = $MesaOpenGlDir
    if ([string]::IsNullOrEmpty($mesaRoot)) {
        # Prefer the mingw build: it statically links its C runtime, so it does
        # NOT depend on the MSVC redistributable that a bare VM may lack (the
        # msvc build does). It requires SSSE3, which every x64 CPU since ~2006
        # has, so it is safe for VirtualBox and other VMs.
        $asset   = "mesa3d-$MesaVersion-release-mingw.7z"
        $url     = "https://github.com/pal1000/mesa-dist-win/releases/download/$MesaVersion/$asset"
        $mesaDir = Join-Path $BuildDir 'mesa'
        $archive = Join-Path $mesaDir $asset
        $mesaRoot = Join-Path $mesaDir 'extracted'

        New-Item -ItemType Directory -Force -Path $mesaDir | Out-Null
        Write-Log "Downloading Mesa $MesaVersion ($asset)"
        # Windows PowerShell 5.1 defaults to TLS 1.0/1.1, which GitHub rejects;
        # force TLS 1.2. Suppressing the progress bar speeds large downloads.
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        $prevProgress = $ProgressPreference
        $ProgressPreference = 'SilentlyContinue'
        try {
            Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing
        } finally {
            $ProgressPreference = $prevProgress
        }

        # Verify integrity before extracting into the (signed) MSI. A mismatch
        # means the release asset changed or the download was tampered with;
        # fail rather than package unverified binaries.
        if ([string]::IsNullOrEmpty($MesaSha256)) {
            Die "MESA_SHA256 is empty; refusing to package an unverified Mesa archive. Set MESA_SHA256 for $asset (or BUNDLE_MESA=0)."
        }
        $actualHash = (Get-FileHash -Path $archive -Algorithm SHA256).Hash.ToLower()
        if ($actualHash -ne $MesaSha256.ToLower()) {
            Die "Mesa archive hash mismatch for $asset`nexpected $($MesaSha256.ToLower())`ngot      $actualHash"
        }
        Write-Log "Verified Mesa archive SHA-256"

        # GitHub Windows runners ship 7-Zip; accept either launcher name.
        $sevenZip = Get-Command 7z -ErrorAction SilentlyContinue
        if (-not $sevenZip) { $sevenZip = Get-Command 7za -ErrorAction SilentlyContinue }
        if (-not $sevenZip) { Die "7-Zip (7z/7za) not found on PATH; required to extract $asset" }
        if (Test-Path $mesaRoot) { Remove-Item -Recurse -Force $mesaRoot }
        New-Item -ItemType Directory -Force -Path $mesaRoot | Out-Null
        Write-Log "Extracting $asset"
        & $sevenZip.Source x $archive "-o$mesaRoot" -y | Out-Null
        if ($LASTEXITCODE -ne 0) { Die "extracting $asset failed" }
    } else {
        Write-Log "Using MESA_OPENGL_DIR=$mesaRoot"
    }

    # Locate the 64-bit DLLs. Prefer files under an 'x64' path segment (the
    # archive separates x64/x86), falling back to the first match anywhere.
    $opengl  = Find-MesaFile $mesaRoot 'opengl32.dll'
    $gallium = Find-MesaFile $mesaRoot 'libgallium_wgl.dll'
    if (-not $opengl)  { Die "opengl32.dll not found under $mesaRoot" }
    if (-not $gallium) { Die "libgallium_wgl.dll not found under $mesaRoot (needed since Mesa 21.3.0)" }

    # Stage next to adder-tray.exe so the .wxs File sources resolve there.
    Copy-Item $opengl  (Join-Path $BinDir 'opengl32.dll')        -Force
    Copy-Item $gallium (Join-Path $BinDir 'libgallium_wgl.dll')  -Force
    $script:MesaOpenGl  = Join-Path $BinDir 'opengl32.dll'
    $script:MesaGallium = Join-Path $BinDir 'libgallium_wgl.dll'

    # MIT license text (best-effort; components in the .wxs are optional).
    $license = Get-ChildItem -Path $mesaRoot -Recurse -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match '(?i)licen[cs]e|copying' } |
        Select-Object -First 1
    if ($license) {
        Copy-Item $license.FullName (Join-Path $BinDir 'OpenGL-Mesa-LICENSE.txt') -Force
        $script:MesaLicense = Join-Path $BinDir 'OpenGL-Mesa-LICENSE.txt'
    } else {
        Write-Warn "Mesa license file not found under $mesaRoot; not bundling a license file."
    }

    Write-Log "Staged Mesa software OpenGL (opengl32.dll + libgallium_wgl.dll)"
}

# Find-MesaFile returns the best matching path for $name under $root, preferring
# a match whose path contains an 'x64' segment (64-bit binaries).
function Find-MesaFile {
    param([string]$root, [string]$name)
    $found = Get-ChildItem -Path $root -Recurse -File -Filter $name -ErrorAction SilentlyContinue
    if (-not $found) { return $null }
    $x64 = $found | Where-Object { $_.FullName -match '(?i)[\\/]x64[\\/]' } | Select-Object -First 1
    if ($x64) { return $x64.FullName }
    return ($found | Select-Object -First 1).FullName
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

    # The Mesa vars are ALWAYS passed; adder.wxs guards its Mesa components on
    # them != "NONE". A sentinel (not an empty string) is used to avoid relying
    # on `wix -d Name=` empty-value parsing and undefined-variable handling.
    $mesaOpenGl  = if ($script:MesaOpenGl)  { $script:MesaOpenGl }  else { 'NONE' }
    $mesaGallium = if ($script:MesaGallium) { $script:MesaGallium } else { 'NONE' }
    $mesaLicense = if ($script:MesaLicense) { $script:MesaLicense } else { 'NONE' }
    $wixArgs = @(
        'build', (Join-Path $ScriptDir 'adder.wxs'),
        '-arch', $WixArch,
        '-d', "Version=$MsiVersion",
        '-d', "AdderExe=$AdderExeSrc",
        '-d', "AdderTrayExe=$AdderTrayExeSrc",
        '-d', "AdderIco=$IconSrc",
        '-d', "MesaOpenGl=$mesaOpenGl",
        '-d', "MesaGallium=$mesaGallium",
        '-d', "MesaLicense=$mesaLicense",
        '-o', $FinalMsi
    )
    & wix @wixArgs
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
Resolve-Mesa
Build-Msi
Sign-Msi

Write-Log "Done: $FinalMsi"
if ([string]::IsNullOrEmpty($JsignKeystore) -or [string]::IsNullOrEmpty($JsignStorePass)) {
    Write-Warn 'This is an UNSIGNED build (local/dev). It will warn under SmartScreen on other machines.'
} else {
    Write-Log "Verify with: signtool verify /pa $MsiName"
}
