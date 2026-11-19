$ErrorActionPreference = 'Stop'

if (!$env:RUNNER_TEMP -or !$env:GITHUB_PATH) {
    throw 'RUNNER_TEMP and GITHUB_PATH must be set'
}
$root = Join-Path $env:RUNNER_TEMP 'syspi-vcs'
New-Item -ItemType Directory -Path $root -Force | Out-Null

function Install-VcsTools($url, $sha256, $bin, $tools) {
    $asset = Split-Path -Leaf $url
    $msi = Join-Path $root $asset
    curl.exe --fail --silent --show-error --location --retry 3 --max-time 60 --output $msi $url
    if ($LASTEXITCODE -ne 0) { throw "Could not download $asset (curl exit $LASTEXITCODE)" }
    if ((Get-FileHash -LiteralPath $msi -Algorithm SHA256).Hash -ne $sha256) {
        throw "SHA-256 mismatch for $asset"
    }

    $target = Join-Path $root ([IO.Path]::GetFileNameWithoutExtension($asset))
    $log = "$target.log"
    $args = '/a "{0}" /qn TARGETDIR="{1}" /L*v "{2}"' -f $msi, $target, $log
    $extract = Start-Process msiexec.exe -ArgumentList $args -Wait -PassThru -WindowStyle Hidden
    if ($extract.ExitCode -ne 0) {
        if (Test-Path -LiteralPath $log) { Get-Content -LiteralPath $log -Tail 40 }
        throw "Could not unpack $asset (msiexec exit $($extract.ExitCode))"
    }

    $binPath = Join-Path $target $bin
    foreach ($tool in $tools) {
        $exe = Join-Path $binPath $tool
        if (!(Test-Path -LiteralPath $exe -PathType Leaf)) { throw "$asset did not provide $tool" }
        & $exe --version | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "$tool --version failed (exit $LASTEXITCODE)" }
    }
    Add-Content -LiteralPath $env:GITHUB_PATH -Value $binPath -Encoding utf8
}

Install-VcsTools 'https://www.mercurial-scm.org/release/windows/mercurial-7.1.2-x64.msi' `
    '8D702D0ACAD169D52FD64924B36A0D2D5F9908ED44E005DDC0D6F893FFE334DA' 'PFiles/Mercurial' @('hg.exe')
Install-VcsTools 'https://sliksvn.com/pub/Slik-Subversion-1.14.5-x64.msi' `
    '21AA09EC9DB9868CD56A378C44D276643171BCF5FCD8AA4877DEE60C2B756200' 'PFiles/bin' @('svn.exe', 'svnadmin.exe')
