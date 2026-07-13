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

Write-Host "Next: sharetoai login"
