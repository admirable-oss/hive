# Installs hive on Windows (amd64): the latest release, or $env:HIVE_VERSION.
#
#   irm https://raw.githubusercontent.com/admirable-oss/hive/main/distribution/install.ps1 | iex
#
# It downloads the release .zip, checks it against the release's
# checksums.txt, puts hive.exe in %LOCALAPPDATA%\Programs\Hive (or
# $env:HIVE_INSTALL_DIR) and adds that to your user PATH. `hive update`
# keeps it current afterwards (and also checks the release signature).
#
# Remove it again with:  & ([scriptblock]::Create((irm <this url>))) -Uninstall
# (-RemoveData also deletes %LOCALAPPDATA%\Hive and %APPDATA%\Hive).
[CmdletBinding()]
param(
    [switch]$Uninstall,
    [switch]$RemoveData
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repository = 'admirable-oss/hive'
$InstallDir = if ($env:HIVE_INSTALL_DIR) { $env:HIVE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\Hive' }
$Exe = Join-Path $InstallDir 'hive.exe'

function Write-Step($msg) { Write-Host "  > $msg" }

function Set-UserPath([string]$dir, [bool]$add) {
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @($current -split ';' | Where-Object { $_ -and ($_.TrimEnd('\') -ine $dir.TrimEnd('\')) })
    if ($add) { $parts += $dir }
    [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
}

if ($Uninstall) {
    if (Test-Path $Exe) {
        try { & $Exe stop *> $null } catch { }
        try { & $Exe daemon uninstall *> $null } catch { }
    }
    Remove-Item -Force -ErrorAction SilentlyContinue $Exe, "$Exe.old"
    if ((Test-Path $InstallDir) -and -not (Get-ChildItem $InstallDir)) { Remove-Item $InstallDir }
    Set-UserPath $InstallDir $false
    if ($RemoveData) {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue (Join-Path $env:LOCALAPPDATA 'Hive'), (Join-Path $env:APPDATA 'Hive')
        Write-Step 'removed hive and its data'
    } else {
        Write-Step "removed hive; its data stays in $(Join-Path $env:LOCALAPPDATA 'Hive') (-RemoveData deletes it)"
    }
    return
}

if ([Environment]::Is64BitOperatingSystem -eq $false -or $env:PROCESSOR_ARCHITECTURE -eq 'ARM64') {
    throw 'hive releases for Windows are built for amd64 (x64) only'
}
if ([Environment]::OSVersion.Version.Build -lt 17763) {
    throw 'hive needs Windows 10 1809 (build 17763) or later, for ConPTY and AF_UNIX sockets'
}

$api = "https://api.github.com/repos/$Repository/releases/latest"
if ($env:HIVE_VERSION) { $api = "https://api.github.com/repos/$Repository/releases/tags/$($env:HIVE_VERSION)" }
$headers = @{ 'User-Agent' = 'hive-installer'; 'Accept' = 'application/vnd.github+json' }
$release = Invoke-RestMethod -Headers $headers -Uri $api
$tag = $release.tag_name
$version = $tag.TrimStart('v')
$zipName = "hive_${version}_windows_amd64.zip"
$zipAsset = $release.assets | Where-Object name -eq $zipName
$sumsAsset = $release.assets | Where-Object name -eq 'checksums.txt'
if (-not $zipAsset -or -not $sumsAsset) { throw "release $tag has no $zipName" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("hive-install-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Step "downloading hive $tag"
    $zip = Join-Path $tmp $zipName
    Invoke-WebRequest -Headers $headers -Uri $zipAsset.browser_download_url -OutFile $zip
    $sums = (Invoke-WebRequest -Headers $headers -Uri $sumsAsset.browser_download_url).Content
    if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
    $want = ($sums -split "`n" | Where-Object { $_ -match "\s\*?$([regex]::Escape($zipName))\s*$" } | Select-Object -First 1) -split '\s+' | Select-Object -First 1
    $got = (Get-FileHash -Algorithm SHA256 $zip).Hash
    if (-not $want -or $got -ine $want) { throw "$zipName does not match its checksum; not installing it" }
    Write-Step 'checksum verified'

    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    if (Test-Path $Exe) {
        # A running hive.exe can be renamed but not replaced.
        Remove-Item -Force -ErrorAction SilentlyContinue "$Exe.old"
        Move-Item -Force $Exe "$Exe.old"
    }
    Copy-Item (Join-Path $tmp 'hive.exe') $Exe
    Set-UserPath $InstallDir $true
    $env:Path = "$env:Path;$InstallDir"
    Write-Step "installed $(& $Exe version) at $Exe"
    Write-Host "`nOpen a new terminal, then get started with: hive"
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
}
