param(
  [string]$InstallDir = "$env:ProgramData\CherryDesktopCCTV"
)

$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path "$InstallDir\recordings" | Out-Null

Write-Host "Cherry Desktop CCTV install directory: $InstallDir"
Write-Host "Required files in the install directory:"
Write-Host "  cherrycctv.exe"
Write-Host "  ffmpeg.exe"
Write-Host "  mediamtx.exe"
Write-Host "  mediamtx.yml"
Write-Host "  config.json"
Write-Host ""
Write-Host "Windows Firewall ports to allow:"
Write-Host "  UDP 3702  - ONVIF WS-Discovery"
Write-Host "  TCP 8088  - ONVIF SOAP"
Write-Host "  TCP 8554  - RTSP"
Write-Host ""
Write-Host "Example firewall commands (run as Administrator):"
Write-Host 'New-NetFirewallRule -DisplayName "Cherry CCTV ONVIF Discovery" -Direction Inbound -Protocol UDP -LocalPort 3702 -Action Allow'
Write-Host 'New-NetFirewallRule -DisplayName "Cherry CCTV ONVIF" -Direction Inbound -Protocol TCP -LocalPort 8088 -Action Allow'
Write-Host 'New-NetFirewallRule -DisplayName "Cherry CCTV RTSP" -Direction Inbound -Protocol TCP -LocalPort 8554 -Action Allow'
