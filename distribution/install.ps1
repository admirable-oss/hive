# Installs hive on Windows (amd64 or arm64): the latest release, or $env:HIVE_VERSION.
#
#   irm https://raw.githubusercontent.com/admirable-oss/hive/main/distribution/install.ps1 | iex
#
# It downloads the release .zip, checks checksums.txt's signature with the
# release key (ssh-keygen, from the OpenSSH client Windows includes) and the
# .zip against checksums.txt, puts hive.exe in %LOCALAPPDATA%\Programs\Hive
# (or $env:HIVE_INSTALL_DIR) and adds that to your user PATH. `hive update`
# keeps it current afterwards. $env:HIVE_ALLOW_UNVERIFIED = '1' installs with
# the checksum alone where ssh-keygen is missing or too old; a signature that
# does not verify always stops the install.
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

# The release key (go run ./tools/release-sign allowed-signers <key>).
$AllowedSigners = 'hive-release namespaces="hive-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILHcqXlEfCE8NSz4faSoF7WWzUrakzdnGNT4fFEWlNm1'
# Releases published before SSH signatures existed; any other release
# without one is refused.
$UnsignedReleases = @('v1.0.0-rc.1')

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

if (-not [Environment]::Is64BitOperatingSystem) { throw 'hive needs 64-bit Windows (amd64 or arm64)' }
# The OS's architecture, not this PowerShell's: x64 PowerShell on an ARM64
# machine reports AMD64 in PROCESSOR_ARCHITECTURE.
$osArch = try { [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { $env:PROCESSOR_ARCHITECTURE }
$arch = if ($osArch -match 'Arm64') { 'arm64' } else { 'amd64' }
if ([Environment]::OSVersion.Version.Build -lt 17763) {
    throw 'hive needs Windows 10 1809 (build 17763) or later, for ConPTY and AF_UNIX sockets'
}

$api = "https://api.github.com/repos/$Repository/releases/latest"
if ($env:HIVE_VERSION) { $api = "https://api.github.com/repos/$Repository/releases/tags/$($env:HIVE_VERSION)" }
$headers = @{ 'User-Agent' = 'hive-installer'; 'Accept' = 'application/vnd.github+json' }
$release = Invoke-RestMethod -Headers $headers -Uri $api
$tag = $release.tag_name
$version = $tag.TrimStart('v')
$zipName = "hive_${version}_windows_${arch}.zip"
$zipAsset = $release.assets | Where-Object name -eq $zipName
$sumsAsset = $release.assets | Where-Object name -eq 'checksums.txt'
if (-not $zipAsset -or -not $sumsAsset) { throw "release $tag has no $zipName" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("hive-install-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Step "downloading hive $tag"
    $zip = Join-Path $tmp $zipName
    Invoke-WebRequest -Headers $headers -Uri $zipAsset.browser_download_url -OutFile $zip
    $sumsFile = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest -Headers $headers -Uri $sumsAsset.browser_download_url -OutFile $sumsFile
    $sums = [IO.File]::ReadAllText($sumsFile)

    if ($UnsignedReleases -contains $tag) {
        Write-Warning "$tag was published before release signatures for installers; checking its checksum only"
    } else {
        $sigAsset = $release.assets | Where-Object name -eq 'checksums.txt.sshsig'
        if (-not $sigAsset) { throw "$tag has no checksums.txt.sshsig; refusing an unsigned release" }
        $sigFile = "$sumsFile.sshsig"
        Invoke-WebRequest -Headers $headers -Uri $sigAsset.browser_download_url -OutFile $sigFile
        $keygen = Get-Command ssh-keygen -ErrorAction SilentlyContinue
        $unverifiable = $null
        if (-not $keygen) {
            $unverifiable = 'ssh-keygen (the OpenSSH client) is not installed'
        } else {
            $signers = Join-Path $tmp 'allowed_signers'
            [IO.File]::WriteAllText($signers, "$AllowedSigners`n")
            $verify = Start-Process -FilePath $keygen.Source -NoNewWindow -Wait -PassThru `
                -ArgumentList @('-Y', 'verify', '-f', "`"$signers`"", '-I', 'hive-release', '-n', 'hive-release', '-s', "`"$sigFile`"") `
                -RedirectStandardInput $sumsFile -RedirectStandardOutput (Join-Path $tmp 'verify.out') -RedirectStandardError (Join-Path $tmp 'verify.err')
            if ($verify.ExitCode -eq 0) {
                Write-Step 'release signature verified'
            } elseif ((Get-Content -Raw (Join-Path $tmp 'verify.err')) -match 'illegal option|unknown option|invalid option') {
                $unverifiable = 'this ssh-keygen is older than OpenSSH 8.1'
            } else {
                throw "the signature of $tag's checksums.txt does not verify with the hive release key; not installing it"
            }
        }
        if ($unverifiable) {
            if ($env:HIVE_ALLOW_UNVERIFIED -ne '1') {
                throw "$unverifiable, so the release signature cannot be checked. Install the OpenSSH client (Settings > Optional features), or set `$env:HIVE_ALLOW_UNVERIFIED = '1' to rely on the checksum alone"
            }
            Write-Warning "$unverifiable; installing with the checksum only (HIVE_ALLOW_UNVERIFIED=1)"
        }
    }
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
