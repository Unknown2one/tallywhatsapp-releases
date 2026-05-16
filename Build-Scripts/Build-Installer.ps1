#requires -Version 5.1
<#
.SYNOPSIS
  Builds TallyWhatsApp end-to-end: Go binaries → C# DLL → WiX MSI.

.DESCRIPTION
  Run from anywhere. The script discovers the repo root via its own
  location, lays everything down under `installer\build`, then calls
  `wix build` to produce TallyWhatsApp-<version>.msi at the repo root.

  Production runs add -Sign and an EV cert path so each binary plus the
  MSI is Authenticode-signed before publication.

.PARAMETER Version
  Marketing + MSI version. Defaults to a 0.0.<unix-time-mod-65535>
  development stamp; CI overrides this with the release tag.

.PARAMETER LicensePublicKey
  Base64-encoded Ed25519 public key the service uses to verify license
  files. CI feeds this from a sealed secret. Empty for dev builds.

.PARAMETER IssuerURL
  Activation issuer endpoint baked into the service.

.PARAMETER Configuration
  C# build configuration. Almost always Release.

.PARAMETER Sign
  When set, signs every binary with $env:CODESIGN_PFX +
  $env:CODESIGN_PASSWORD using signtool.

.EXAMPLE
  .\Build-Installer.ps1 -Version 1.0.3 -LicensePublicKey $env:LIC_PUB
#>
[CmdletBinding()]
param(
    [string]$Version = $null,
    [string]$LicensePublicKey = "bphDsUV1/0leuMCoBoA0zgnmZGHqTqePQWcZiedOJmI=",
    [string]$UpdatePublicKey = "",
    [string]$IssuerURL = "https://script.google.com/macros/s/AKfycbxBmiVpDrBzbfH572ITzT8TEtZ3b2hYR9MgKHF-7Zt-oiIOUhW-ned5Wf0-hi7QQHrD/exec",
    [string]$ManifestURL = "https://updates.variantstudio.in/stable/manifest.json",
    [string]$Configuration = "Release",
    [switch]$Sign
)

$ErrorActionPreference = "Stop"

# ── Helpers ────────────────────────────────────────────────────────────
# Defined first because PowerShell binds function calls at parse time
# inside script bodies — moving these to the bottom is a runtime error.

function Get-MSBuildPath {
    $vswhere = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe"
    if (Test-Path $vswhere) {
        $vsPath = & $vswhere -latest -requires Microsoft.Component.MSBuild -property installationPath
        if ($vsPath) {
            $candidate = Join-Path $vsPath "MSBuild\Current\Bin\MSBuild.exe"
            if (Test-Path $candidate) { return $candidate }
        }
    }
    $legacy = "$env:WINDIR\Microsoft.NET\Framework64\v4.0.30319\MSBuild.exe"
    if (Test-Path $legacy) { return $legacy }
    throw "MSBuild not found. Install Visual Studio Build Tools or the .NET Framework 4 SDK."
}

function Get-SignToolPath {
    $candidates = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending
    if ($candidates) { return $candidates[0].FullName }
    throw "signtool.exe not found. Install the Windows 10 SDK."
}

# Resolve the repo layout once. $PSScriptRoot is build-scripts\; the
# tallywa Go module lives one level up.
$RepoRoot       = Resolve-Path (Join-Path $PSScriptRoot "..")
$GoModule       = Join-Path $RepoRoot "tallywa"
$InstallerDir   = Join-Path $GoModule "installer"
$BuildDir       = Join-Path $InstallerDir "build"
$TDLSource      = Join-Path $RepoRoot "Tally-TDL"
$DotNetProject  = Join-Path $RepoRoot "Tally-COM-Interface\TallyWhatsappsender\TallyWhatsappsender.csproj"

if (-not $Version) {
    $Version = "0.0." + (([DateTimeOffset]::UtcNow.ToUnixTimeSeconds()) % 65535)
}

Write-Host "TallyWhatsApp build" -ForegroundColor Cyan
Write-Host "  Version       : $Version"
Write-Host "  Configuration : $Configuration"
Write-Host "  Sign binaries : $Sign"
Write-Host ""

# Fresh build dir each run. We never reuse stale outputs because file-
# version changes inside the WiX harvest can silently keep old binaries.
if (Test-Path $BuildDir) { Remove-Item -Recurse -Force $BuildDir }
New-Item -ItemType Directory -Path $BuildDir | Out-Null
New-Item -ItemType Directory -Path (Join-Path $BuildDir "TDL") | Out-Null

# ── Step 1: Go binaries ────────────────────────────────────────────────
Write-Host "[1/4] Compiling Go binaries..." -ForegroundColor Yellow

$ldflags = @(
    "-s -w",
    "-X main.Version=$Version",
    "-X main.LicensePublicKey=$LicensePublicKey",
    "-X main.DefaultIssuerURL=$IssuerURL"
) -join " "

Push-Location $GoModule
try {
    $env:GOOS   = "windows"
    $env:GOARCH = "amd64"

    & go build -trimpath -ldflags="$ldflags" -o (Join-Path $BuildDir "tallywa-svc.exe") ./cmd/tallywa-svc
    if ($LASTEXITCODE -ne 0) { throw "go build tallywa-svc failed" }

    & go build -trimpath -ldflags="-s -w -X main.version=$Version -H windowsgui" -o (Join-Path $BuildDir "tallywa-tray.exe") ./cmd/tallywa-tray
    if ($LASTEXITCODE -ne 0) { throw "go build tallywa-tray failed" }

    & go build -trimpath -ldflags="-s -w" -o (Join-Path $BuildDir "tallywa-installer-helper.exe") ./cmd/tallywa-installer-helper
    if ($LASTEXITCODE -ne 0) { throw "go build tallywa-installer-helper failed" }

    $updaterLdflags = @(
        "-s -w",
        "-X main.Version=$Version",
        "-X main.UpdatePublicKey=$UpdatePublicKey",
        "-X main.DefaultManifestURL=$ManifestURL"
    ) -join " "
    & go build -trimpath -ldflags="$updaterLdflags" -o (Join-Path $BuildDir "tallywa-updater.exe") ./cmd/tallywa-updater
    if ($LASTEXITCODE -ne 0) { throw "go build tallywa-updater failed" }
}
finally { Pop-Location }

# ── Step 2: C# COM DLL ─────────────────────────────────────────────────
Write-Host "[2/4] Compiling C# COM DLL..." -ForegroundColor Yellow

$msbuild = Get-MSBuildPath
# /p:RegisterForComInterop=false disables the post-build regasm step
# (which needs admin and would write to HKCR on the build machine).
# COM registration happens at install time via the MSI custom action.
& $msbuild $DotNetProject "/p:Configuration=$Configuration" "/p:RegisterForComInterop=false" "/t:Rebuild" "/v:minimal"
if ($LASTEXITCODE -ne 0) { throw "MSBuild failed for the COM DLL" }

$dllSrc = Join-Path (Split-Path $DotNetProject) "bin\$Configuration\TallyWhatsappsender.dll"
Copy-Item $dllSrc (Join-Path $BuildDir "TallyWhatsappsender.dll") -Force

# ── Step 3: TDL files ──────────────────────────────────────────────────
Write-Host "[3/4] Staging TDL files..." -ForegroundColor Yellow

# The MSI ships only the bridge-driven TDL files. Old SalesWhatsapp /
# ReceiptWhatsapp / LedgerWhatsapp.tdl are deliberately excluded — the
# tally.ini patcher only adds entries for files that are actually here.
@("voucher_send.tdl", "_loader.tdl") | ForEach-Object {
    Copy-Item (Join-Path $TDLSource $_) (Join-Path (Join-Path $BuildDir "TDL") $_) -Force
}

# ── Step 4: Optional code signing ──────────────────────────────────────
if ($Sign) {
    Write-Host "[3.5/4] Signing binaries..." -ForegroundColor Yellow
    $signtool = Get-SignToolPath
    $pfx      = $env:CODESIGN_PFX
    $password = $env:CODESIGN_PASSWORD
    if (-not $pfx -or -not (Test-Path $pfx)) {
        throw "CODESIGN_PFX env var must point to the EV cert PFX file."
    }
    foreach ($bin in @("tallywa-svc.exe", "tallywa-tray.exe", "tallywa-installer-helper.exe", "tallywa-updater.exe", "TallyWhatsappsender.dll")) {
        & $signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 `
            /f $pfx /p $password (Join-Path $BuildDir $bin)
        if ($LASTEXITCODE -ne 0) { throw "signtool failed on $bin" }
    }
}

# ── Step 5: WiX MSI ────────────────────────────────────────────────────
Write-Host "[4/4] Building MSI with WiX..." -ForegroundColor Yellow

Push-Location $InstallerDir
try {
    # Pin extensions to the WiX 5 line. The version must match the wix
    # tool itself — `wix extension add Foo` (no version) grabs the latest
    # NuGet release, which since late 2024 is v7 and incompatible with v5.
    Write-Host "  Adding WiX extensions..." -ForegroundColor DarkGray
    & wix extension add WixToolset.Util.wixext/5.0.2
    if ($LASTEXITCODE -ne 0) { throw "wix extension add Util failed" }
    & wix extension add WixToolset.Firewall.wixext/5.0.2
    if ($LASTEXITCODE -ne 0) { throw "wix extension add Firewall failed" }

    $msiOut = Join-Path $RepoRoot "TallyWhatsApp-$Version.msi"
    & wix build Product.wxs `
        -ext WixToolset.Util.wixext `
        -ext WixToolset.Firewall.wixext `
        -d "ProductVersion=$Version" `
        -d "BuildDir=$BuildDir" `
        -arch x64 `
        -out $msiOut
    if ($LASTEXITCODE -ne 0) { throw "wix build failed" }

    if ($Sign) {
        Write-Host "Signing MSI..." -ForegroundColor Yellow
        & (Get-SignToolPath) sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 `
            /f $env:CODESIGN_PFX /p $env:CODESIGN_PASSWORD $msiOut
        if ($LASTEXITCODE -ne 0) { throw "signtool failed on MSI" }
    }

    Write-Host ""
    Write-Host "Built $msiOut" -ForegroundColor Green
}
finally { Pop-Location }
