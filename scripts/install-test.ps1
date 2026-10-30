<#
.SYNOPSIS
Runs install.ps1 against a fake release served from this machine.

.DESCRIPTION
Called by install-test.sh with the server it started; run alone (on Windows CI) it builds
the fake releases with build-release-artifacts.sh under Git Bash and starts the server itself.
Needs Go, python3 and, when run alone, bash (Git for Windows has it).
#>
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '')]
[CmdletBinding()]
param(
    [string]$BaseUrl,
    [string]$Mirror,
    [string]$Good = 'v9.9.1',
    [string]$Older = 'v9.9.0',
    [string]$Mismatch = 'v9.9.2',
    [string]$Tampered = 'v9.9.3',
    [string]$Partial = 'v9.9.6',
    [string]$Foreign = 'linux-arm64'
)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Root = Split-Path -Parent $PSScriptRoot
$Installer = Join-Path $Root 'install.ps1'
$Work = Join-Path ([System.IO.Path]::GetTempPath()) ('syspi-install-test-' + [System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $Work -Force | Out-Null
$Host_ = "$(go env GOOS)-$(go env GOARCH)"
$Archive = if ($Host_ -like 'windows-*') { "syspi-$Host_.zip" } else { "syspi-$Host_.tar.gz" }
$Server = $null

function Release([string]$Tag, [string]$Stamp = $Tag) {
    $dir = Join-Path $Work "site/releases/download/$Tag"
    $build = Join-Path $Work "build-$Tag"
    $env:SOURCE_DATE_EPOCH = '1700000000'
    & bash (Join-Path $Root 'scripts/build-release-artifacts.sh') $Stamp $build | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "build-release-artifacts.sh failed for $Tag" }
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    Get-ChildItem -LiteralPath $build | Move-Item -Destination $dir
}

try {
    if (-not $BaseUrl) {
        Release $Good
        Release $Older
        Release $Mismatch $Good
        Release $Tampered
        Release $Partial
        $manifest = Join-Path $Work "site/releases/download/$Tampered/SHA256SUMS.txt"
        $lines = Get-Content -LiteralPath $manifest | ForEach-Object { if ($_ -like "*  $Archive") { '00000000' + $_.Substring(8) } else { $_ } }
        [IO.File]::WriteAllText($manifest, ($lines -join "`n") + "`n")
        $manifest = Join-Path $Work "site/releases/download/$Partial/SHA256SUMS.txt"
        $lines = Get-Content -LiteralPath $manifest | Where-Object { $_ -notlike "*syspi-$Foreign.*" }
        [IO.File]::WriteAllText($manifest, ($lines -join "`n") + "`n")
        New-Item -ItemType Directory -Path (Join-Path $Work 'site/mirror/latest') -Force | Out-Null
        Copy-Item -LiteralPath (Join-Path $Work "site/releases/download/$Good") -Destination (Join-Path $Work 'site/mirror/latest/download') -Recurse

        $portFile = Join-Path $Work 'port'
        $Server = Start-Process -FilePath python3 -ArgumentList @((Join-Path $Root 'scripts/install-test-server.py'), (Join-Path $Work 'site'), $Good) -RedirectStandardOutput $portFile -PassThru -NoNewWindow
        $port = $null
        foreach ($attempt in 1..50) {
            if ((Test-Path -LiteralPath $portFile) -and (Get-Content -LiteralPath $portFile -ErrorAction SilentlyContinue)) { $port = @(Get-Content -LiteralPath $portFile)[0].Trim(); break }
            Start-Sleep -Milliseconds 100
        }
        if (-not $port) { throw 'the release server did not start' }
        $BaseUrl = "http://127.0.0.1:$port/releases"
        $Mirror = "http://127.0.0.1:$port/mirror"
    }

    $failures = 0
    $n = 0
    $script:out = ''
    $script:ok = $false
    $script:dir = ''

    function Run([string[]]$Arguments) {
        $script:n++
        $script:dir = Join-Path $Work "install-$script:n"
        $expanded = @($Arguments | ForEach-Object { $_ -replace '@P@', $script:dir })
        if ($expanded -notcontains '-BaseUrl') { $expanded = @('-BaseUrl', $BaseUrl) + $expanded }
        $previous = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            $script:out = (& pwsh -NoProfile -File $Installer -NoPath @expanded 2>&1 | ForEach-Object { "$_" }) -join "`n"
            $script:ok = ($LASTEXITCODE -eq 0)
        } finally {
            $ErrorActionPreference = $previous
        }
    }
    function Ok([string]$Name, [string[]]$Arguments) {
        Run $Arguments
        if (-not $script:ok) { Write-Host "FAIL $Name"; Write-Host $script:out; $script:failures++; return $false }
        Write-Host "ok   $Name"
        return $true
    }
    function Fails([string]$Name, [string]$Message, [string[]]$Arguments) {
        Run $Arguments
        if ($script:ok -or $script:out -notlike "*$Message*") { Write-Host "FAIL ${Name}: expected a failure mentioning '$Message'"; Write-Host $script:out; $script:failures++; return }
        Write-Host "ok   $Name"
    }
    function Said([string]$Text) { if ($script:out -notlike "*$Text*") { Write-Host "FAIL: expected output to mention '$Text'"; Write-Host $script:out; $script:failures++ } }
    function Reports([string]$Binary, [string]$Tag) {
        $got = (& $Binary version 2>&1 | ForEach-Object { "$_" }) -join ''
        if ($got -ne "syspi $Tag") { Write-Host "FAIL: $Binary reports '$got', not 'syspi $Tag'"; $script:failures++ }
    }
    $exe = if ($Host_ -like 'windows-*') { '.exe' } else { '' }
    $script:failures = 0

    if (Ok 'release by tag' @('-Version', $Good, '-InstallDir', '@P@')) {
        Said "$Archive verified"
        Reports (Join-Path $script:dir "syspi$exe") $Good
    }
    if (Ok 'latest through the redirect' @('-InstallDir', '@P@')) {
        Said "Installing syspi ($Good)"
        Reports (Join-Path $script:dir "syspi$exe") $Good
    }
    if (Ok 'latest on a mirror without the redirect' @('-BaseUrl', $Mirror, '-InstallDir', '@P@')) {
        Said 'Installing syspi (the latest release)'
    }
    if (Ok 'dry run' @('-Version', $Older, '-InstallDir', '@P@', '-DryRun')) {
        Said 'Dry run: nothing installed.'
        if (Test-Path -LiteralPath (Join-Path $script:dir "syspi$exe")) { Write-Host 'FAIL: dry run installed'; $script:failures++ }
    }
    if (Ok 'foreign platform is staged, not run' @('-Version', $Good, '-InstallDir', '@P@', '-Os', $Foreign.Split('-')[0], '-Arch', $Foreign.Split('-')[1])) {
        Said "staged for $Foreign"
    }
    Fails 'tampered archive' 'does not match SHA256SUMS.txt' @('-Version', $Tampered, '-InstallDir', '@P@')
    Fails 'binary reporting another version' "reports 'syspi $Good', not $Mismatch" @('-Version', $Mismatch, '-InstallDir', '@P@')
    Fails 'platform the release lacks' "does not list syspi-$Foreign" @('-Version', $Partial, '-InstallDir', '@P@', '-Os', $Foreign.Split('-')[0], '-Arch', $Foreign.Split('-')[1])
    Fails 'unknown release' 'could not download' @('-Version', 'v0.0.0-none', '-InstallDir', '@P@')
    Fails 'bad version' '-Version must be a release tag' @('-Version', 'nightly', '-InstallDir', '@P@')

    if ($script:failures -gt 0) { Write-Host "install-test.ps1: $script:failures failure(s)"; exit 1 }
    Write-Host 'install-test.ps1: all cases passed'
    # The last native command was an installer expected to fail; do not hand its code to the caller.
    exit 0
} finally {
    if ($Server) { Stop-Process -Id $Server.Id -Force -ErrorAction SilentlyContinue }
    Remove-Item -LiteralPath $Work -Recurse -Force -ErrorAction SilentlyContinue
}
