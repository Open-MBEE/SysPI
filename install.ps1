<#
.SYNOPSIS
Installs syspi, the SysML Package Installer, from a GitHub release.

.DESCRIPTION
Windows PowerShell 5.1 or PowerShell 7, on Windows:

    irm https://raw.githubusercontent.com/Open-MBEE/SysPI/main/install.ps1 | iex

With options, save it first (a piped script takes none; the environment
variables below configure it instead):

    irm https://raw.githubusercontent.com/Open-MBEE/SysPI/main/install.ps1 -OutFile install.ps1
    .\install.ps1 -Version v0.1.0

The release's archive (syspi-windows-amd64.zip; the tar.gz on Linux and macOS
under PowerShell 7) is downloaded with its SHA256SUMS.txt manifest, checked
against the manifest before anything is installed, and the installed binary is
run once to confirm it reports the release it came from. This script installs
for the current user and touches only the user PATH.

Needs: PowerShell 5.1+ (Windows) or PowerShell 7 (elsewhere, with tar).

.PARAMETER Version
Release tag (v0.1.0) or `latest` (default). [SYSPI_VERSION]

.PARAMETER InstallDir
Where the binary goes; default $env:LOCALAPPDATA\Programs\SysPI on Windows,
~/.local/bin elsewhere. [SYSPI_INSTALL_DIR]

.PARAMETER Os
Stage another platform's build instead of this machine's (windows, linux or
darwin); it is not run.

.PARAMETER Arch
Architecture of the build (amd64 or arm64); default this machine's.

.PARAMETER BaseUrl
Where the releases are, for a mirror; default
https://github.com/Open-MBEE/SysPI/releases. [SYSPI_DOWNLOAD_BASE]

.PARAMETER NoPath
Leave the user PATH alone (Windows only; elsewhere PATH is never edited).

.PARAMETER DryRun
Print what would be installed and from where, then exit.

.LINK
https://github.com/Open-MBEE/SysPI#install
#>
# Progress is written to the host on purpose: it is for the person running the
# installer, not for a pipeline, and `irm | iex` has no caller to receive output.
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '')]
[CmdletBinding()]
param(
    [string]$Version = $(if ($env:SYSPI_VERSION) { $env:SYSPI_VERSION } else { 'latest' }),
    [string]$InstallDir = $env:SYSPI_INSTALL_DIR,
    [ValidateSet('', 'windows', 'linux', 'darwin')]
    [string]$Os = '',
    [ValidateSet('', 'amd64', 'arm64')]
    [string]$Arch = '',
    [string]$BaseUrl = $(if ($env:SYSPI_DOWNLOAD_BASE) { $env:SYSPI_DOWNLOAD_BASE } else { 'https://github.com/Open-MBEE/SysPI/releases' }),
    [switch]$NoPath,
    [switch]$VerifySignature,
    [switch]$DryRun
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
# Invoke-WebRequest on Windows PowerShell 5.1 is slow while it draws progress.
$ProgressPreference = 'SilentlyContinue'

$RepoUrl = 'https://github.com/Open-MBEE/SysPI'
# The identity the checksum manifest is signed with: the release workflow, run on the tag.
$ReleaseOidcIssuer = 'https://token.actions.githubusercontent.com'
$WantSignature = [bool]$VerifySignature
$ReleaseIdentityRegexp = '^https://github\.com/Open-MBEE/SysPI/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
$GoInstallHint = "'go install github.com/Open-MBEE/SysPI/cmd/syspi@latest' builds one for this machine"

# Run as a file the script exits with the message; piped into Invoke-Expression it
# throws instead, since exit there would close the caller's session.
$RunAsFile = [bool]$PSCommandPath
function Fail([string]$Message) {
    if (-not $RunAsFile) { throw "error: $Message" }
    [Console]::Error.WriteLine("error: $Message")
    exit 1
}

# --- which version ----------------------------------------------------------

switch -Regex ($Version) {
    '^latest$' { break }
    '^v\d+\.\d+\.\d+' { break }
    '^\d+\.\d+\.\d+' { $Version = "v$Version"; break }
    default { Fail "-Version must be a release tag like v0.1.0 or 'latest', not '$Version'" }
}

$BaseUrl = $BaseUrl.TrimEnd('/')

# --- the platform -----------------------------------------------------------

# $IsWindows exists only from PowerShell 6; Windows PowerShell is always on Windows.
function Test-PlatformFlag([string]$Name) { [bool](Get-Variable -Name $Name -ValueOnly -ErrorAction SilentlyContinue) }
$HostIsWindows = ($PSVersionTable.PSEdition -eq 'Desktop') -or ($PSVersionTable.PSVersion.Major -lt 6) -or (Test-PlatformFlag IsWindows)
$HostOs = if ($HostIsWindows) { 'windows' } elseif (Test-PlatformFlag IsMacOS) { 'darwin' } elseif (Test-PlatformFlag IsLinux) { 'linux' } else { '' }

$HostArch = ''
try {
    $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
} catch {
    $osArch = $env:PROCESSOR_ARCHITECTURE
}
switch -Regex ($osArch) {
    '^(X64|AMD64)$' { $HostArch = 'amd64' }
    '^(Arm64|ARM64)$' { $HostArch = 'arm64' }
}

if (-not $Os) {
    $Os = $HostOs
    if (-not $Os) { Fail "unsupported operating system: releases are built for Windows, Linux and macOS; see $RepoUrl#install for building from source" }
}
if (-not $Arch) {
    $Arch = $HostArch
    if (-not $Arch) { Fail "unsupported architecture '$osArch': releases are built for amd64 and arm64; $GoInstallHint" }
}
$Platform = "$Os-$Arch"
$IsHostPlatform = ($Os -eq $HostOs) -and ($Arch -eq $HostArch)
$Exe = if ($Os -eq 'windows') { '.exe' } else { '' }
$Archive = if ($Os -eq 'windows') { "syspi-$Platform.zip" } else { "syspi-$Platform.tar.gz" }
$Manifest = 'SHA256SUMS.txt'

# --- where it goes ----------------------------------------------------------

if (-not $InstallDir) {
    if ($HostIsWindows) {
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\SysPI'
    } else {
        $InstallDir = Join-Path $HOME '.local/bin'
    }
}
$Destination = Join-Path $InstallDir "syspi$Exe"

# --- which release ----------------------------------------------------------

if ($BaseUrl -like 'https://*' -or $BaseUrl -like 'http://*') {
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch {
        $null = $_  # the runtime already speaks TLS 1.2 or later
    }
}

function Resolve-Latest {
    # The redirect GitHub serves for /releases/latest names the tag before anything
    # is downloaded; a mirror without it still works through latest/download/.
    try {
        $request = [System.Net.WebRequest]::Create("$BaseUrl/latest")
        $request.Method = 'HEAD'
        $request.AllowAutoRedirect = $false
        $response = $request.GetResponse()
        try {
            $location = $response.Headers['Location']
        } finally {
            $response.Close()
        }
        if ($location -match '/tag/([^/]+)$') { return $Matches[1] }
    } catch {
        $null = $_  # no redirect to read
    }
    return $null
}

$Tag = $Version
if ($Version -eq 'latest') {
    $Tag = Resolve-Latest
    $AssetDir = if ($Tag) { "$BaseUrl/download/$Tag" } else { "$BaseUrl/latest/download" }
} else {
    $AssetDir = "$BaseUrl/download/$Version"
}
function Get-AssetUrl([string]$Asset) { "$AssetDir/$Asset" }
$ReleaseName = if ($Tag) { $Tag } else { 'the latest release' }

# --- download and verify ----------------------------------------------------

$Work = Join-Path ([System.IO.Path]::GetTempPath()) ("syspi-install-" + [System.IO.Path]::GetRandomFileName())

function Get-Asset([string]$Asset) {
    $url = Get-AssetUrl $Asset
    $destination = Join-Path $Work $Asset
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $destination
    } catch {
        if (Test-Path -LiteralPath $destination) { Remove-Item -LiteralPath $destination -Force }
        Fail "could not download $url ($($_.Exception.Message)); is $ReleaseName a published release with a $Platform build? See $BaseUrl"
    }
}

# Fetches the manifest and, with -VerifySignature, checks its cosign bundle before
# anything is read from it.
function Get-Manifest {
    Get-Asset $Manifest
    if (-not $WantSignature) { return }
    if (-not (Get-Command cosign -ErrorAction SilentlyContinue)) { Fail 'cosign is needed for -VerifySignature; see https://docs.sigstore.dev/cosign/system_config/installation/' }
    Get-Asset "$Manifest.bundle"
    & cosign verify-blob (Join-Path $Work $Manifest) --bundle (Join-Path $Work "$Manifest.bundle") `
        --certificate-oidc-issuer $ReleaseOidcIssuer --certificate-identity-regexp $ReleaseIdentityRegexp 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "$Manifest is not signed by the SysPI release workflow" }
    Write-Host "  signature of $Manifest verified"
}

function Get-ManifestDigest([string]$Asset) {
    # The asset's distinct digests in the manifest, none when it is not listed;
    # a line repeated verbatim counts once.
    $digests = @()
    foreach ($line in Get-Content -LiteralPath (Join-Path $Work $Manifest)) {
        $fields = $line.Trim() -split '\s+', 2
        if ($fields.Count -eq 2 -and $fields[1].TrimStart('*') -eq $Asset) { $digests += $fields[0].ToLowerInvariant() }
    }
    return @($digests | Select-Object -Unique)
}

function Test-Checksum([string]$Asset) {
    $expected = @(Get-ManifestDigest $Asset)
    if ($expected.Count -eq 0) { Fail "$Manifest of $ReleaseName does not list $Asset; the release has no $Platform build" }
    if ($expected.Count -gt 1) { Fail "$Manifest of $ReleaseName lists $Asset with more than one digest ($($expected -join ' ')); the manifest is corrupt or tampered with" }
    $expected = $expected[0]
    $actual = (Get-FileHash -LiteralPath (Join-Path $Work $Asset) -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { Fail "$Asset does not match $Manifest (expected $expected, got $actual); the download is corrupt or tampered with" }
    Write-Host "  $Asset verified"
}

Write-Host "Installing syspi ($ReleaseName) for $Platform"
Write-Host "  binary:   $Destination"
Write-Host "  from:     $(Get-AssetUrl $Archive)"
if ($DryRun) {
    Write-Host 'Dry run: nothing installed.'
    return
}

New-Item -ItemType Directory -Path $Work -Force | Out-Null
try {
    Write-Host 'Downloading...'
    Get-Manifest
    Get-Asset $Archive
    Test-Checksum $Archive

    # --- install ------------------------------------------------------------

    $Extracted = Join-Path $Work 'unpacked'
    New-Item -ItemType Directory -Path $Extracted -Force | Out-Null
    if ($Archive -like '*.zip') {
        Expand-Archive -LiteralPath (Join-Path $Work $Archive) -DestinationPath $Extracted -Force
    } else {
        if (-not (Get-Command tar -ErrorAction SilentlyContinue)) { Fail "tar is needed to extract $Archive" }
        & tar -xzf (Join-Path $Work $Archive) -C $Extracted
        if ($LASTEXITCODE -ne 0) { Fail "tar could not extract $Archive" }
    }
    $Source = Join-Path $Extracted "syspi$Exe"
    if (-not (Test-Path -LiteralPath $Source -PathType Leaf)) { Fail "$Archive has no syspi$Exe" }

    try {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    } catch {
        Fail "cannot create $InstallDir ($($_.Exception.Message)); pass -InstallDir for a writable location"
    }
    # Copy beside the destination first, so an interrupted copy never leaves a
    # half-written binary under the final name.
    $Staged = "$Destination.tmp.$PID"
    Copy-Item -LiteralPath $Source -Destination $Staged -Force
    if (-not $HostIsWindows) {
        & chmod 0755 $Staged
        if ($LASTEXITCODE -ne 0) { Fail "chmod could not mark $Staged executable" }
    }
    Move-Item -LiteralPath $Staged -Destination $Destination -Force
} finally {
    Remove-Item -LiteralPath $Work -Recurse -Force -ErrorAction SilentlyContinue
}

# --- check ------------------------------------------------------------------

if ($IsHostPlatform) {
    # Windows PowerShell turns a native command's stderr into a terminating
    # error under 'Stop', so the preference is relaxed for the run itself.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        # Every line is read before the first is kept: a pipeline Select-Object
        # stops early kills the binary before its exit status is recorded.
        $lines = @(& $Destination version 2>&1 | ForEach-Object { "$_" })
        $exit = $LASTEXITCODE
        $reported = ($lines | Select-Object -First 1) -join ''
    } finally {
        $ErrorActionPreference = $previous
    }
    if ($exit -ne 0) { Fail "$Destination does not run: $reported" }
    if ($Tag -and " $reported " -notlike "* $Tag *") {
        Fail "$Destination reports '$reported', not $Tag"
    }
    Write-Host "  $reported"
} else {
    Write-Host "  staged for $Platform, not run on this $HostOs-$HostArch machine"
}

Write-Host "Installed: $Destination"

# --- PATH -------------------------------------------------------------------

$Separator = [System.IO.Path]::PathSeparator
$OnPath = @(($env:PATH -split [regex]::Escape($Separator)) | ForEach-Object { $_.TrimEnd('\', '/') }) -contains $InstallDir.TrimEnd('\', '/')
if ($HostIsWindows -and -not $NoPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $userEntries = @(if ($userPath) { $userPath -split ';' } else { @() }) | ForEach-Object { $_.TrimEnd('\') }
    if ($userEntries -notcontains $InstallDir.TrimEnd('\')) {
        $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Host "Added $InstallDir to the user PATH; new terminals will see it."
    }
    if (-not $OnPath) { $env:PATH = "$InstallDir$Separator$env:PATH" }
} elseif (-not $OnPath) {
    Write-Warning "$InstallDir is not on PATH; add it to your profile."
}
$Found = Get-Command "syspi$Exe" -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if ($Found -and $Found.Source -ne $Destination) {
    Write-Warning "'syspi' on PATH is $($Found.Source), which comes before $Destination"
}
