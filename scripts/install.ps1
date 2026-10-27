# Installs vitrine on Windows from the GitHub releases:
#
#   irm https://github.com/obuchmann/vitrine/releases/latest/download/install.ps1 | iex
#
# No administrator rights needed. Works in Windows PowerShell 5.1 and
# PowerShell 7. Environment:
#   VITRINE_VERSION       a release, e.g. v0.1.0 (default: the latest)
#   VITRINE_INSTALL_DIR   where vitrine.exe goes (default: %LOCALAPPDATA%\Programs\vitrine)
#   VITRINE_DOWNLOAD_URL  where the release files are, e.g. for tests

# A script block keeps settings and variables out of the caller's session.
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # makes Invoke-WebRequest fast in 5.1
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'obuchmann/vitrine'
    $dir = if ($env:VITRINE_INSTALL_DIR) { $env:VITRINE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\vitrine' }
    $download = if ($env:VITRINE_DOWNLOAD_URL) { $env:VITRINE_DOWNLOAD_URL }
                elseif ($env:VITRINE_VERSION) { "https://github.com/$repo/releases/download/$env:VITRINE_VERSION" }
                else { "https://github.com/$repo/releases/latest/download" }

    $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($cpu) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "vitrine install: unsupported CPU $cpu" }
    }
    $zip = "vitrine_windows_$arch.zip"

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('vitrine-' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Downloading $download/$zip"
        Invoke-WebRequest -UseBasicParsing -Uri "$download/$zip" -OutFile (Join-Path $tmp $zip)
        Invoke-WebRequest -UseBasicParsing -Uri "$download/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
        $want = $null
        foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
            $hash, $file = $line -split '\s+', 2
            if ($file -eq $zip) { $want = $hash.ToLowerInvariant() }
        }
        if (-not $want) { throw "vitrine install: $zip is not in checksums.txt" }
        $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $zip)).Hash.ToLowerInvariant()
        if ($got -ne $want) { throw "vitrine install: checksum mismatch for $zip" }

        Expand-Archive -Path (Join-Path $tmp $zip) -DestinationPath (Join-Path $tmp 'x') -Force
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $exe = Join-Path $dir 'vitrine.exe'
        # A running exe cannot be overwritten or deleted, but it can be renamed.
        # Copies from earlier upgrades that still run stay until a later install.
        $old = $null
        if (Test-Path $exe) {
            Get-ChildItem -Path $dir -Filter 'vitrine.exe.old*' | Remove-Item -Force -ErrorAction SilentlyContinue
            $old = "$exe.old"
            if (Test-Path $old) { $old = "$exe.old-" + [guid]::NewGuid().ToString('N') }
            Move-Item $exe $old
        }
        Copy-Item -Path (Join-Path $tmp 'x\vitrine.exe') -Destination $exe
        if ($old) { Remove-Item -Force -ErrorAction SilentlyContinue $old }
    } finally {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
    }

    $version = & $exe version
    Write-Host "Installed vitrine $version to $exe"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @(if ($userPath) { $userPath -split ';' | Where-Object { $_ } })
    if ($parts -notcontains $dir) {
        [Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User')
        $env:Path = "$env:Path;$dir"
        Write-Host "Added $dir to your PATH. Open a new terminal to use it everywhere."
    }

    Write-Host ''
    Write-Host 'Next, register vitrine with your agent, e.g. for Claude Code:'
    Write-Host '  claude mcp add --scope user vitrine -- vitrine mcp'
    Write-Host 'Other agents: https://github.com/obuchmann/vitrine'
    Write-Host 'If vitrine was already running, restart it: "vitrine doctor" tells you how.'
}
