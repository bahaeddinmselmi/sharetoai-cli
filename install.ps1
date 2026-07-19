# Installs the latest sharetoai CLI release for Windows.
# Usage: irm https://sharetoai.app/install.ps1 | iex
$ErrorActionPreference = "Stop"

$Repo = "bahaeddinmselmi/sharetoai-cli"
$InstallDir = if ($env:SHARETOAI_INSTALL_DIR) { $env:SHARETOAI_INSTALL_DIR } else { "$env:LOCALAPPDATA\sharetoai" }
$Asset = "sharetoai-windows-amd64.exe"
$Url = "https://github.com/$Repo/releases/latest/download/$Asset"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$Dest = Join-Path $InstallDir "sharetoai.exe"

Write-Host "Downloading $Asset..."
Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing

Write-Host "Installed to $Dest"

$UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$UserPath;$InstallDir", "User")
    $env:PATH = "$env:PATH;$InstallDir"
    Write-Host "Added $InstallDir to your PATH (restart your terminal for it to take effect elsewhere)."
}

# Another sharetoai binary earlier on PATH (e.g. a leftover `go install`
# build) would silently shadow the one just installed -- every future
# `sharetoai` command would keep running the old one with no indication
# anything is wrong. Catch that now, while the fix is obvious, instead of
# leaving the user to debug a missing command later.
$resolved = Get-Command sharetoai -All -ErrorAction SilentlyContinue
if ($resolved -and $resolved[0].Source -ne $Dest) {
    Write-Host ""
    Write-Host "Warning: another 'sharetoai' was found earlier on your PATH:"
    Write-Host "  $($resolved[0].Source)"
    Write-Host "The version you just installed at $Dest will not run until you"
    Write-Host "remove the other one or move $InstallDir earlier in your PATH."
}

Write-Host "Next: sharetoai login"
