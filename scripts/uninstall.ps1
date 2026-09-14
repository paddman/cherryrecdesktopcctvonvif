param(
  [string]$InstallDir = "$env:ProgramData\CherryDesktopCCTV",
  [switch]$DeleteRecordings
)

$ErrorActionPreference = "Stop"
Unregister-ScheduledTask -TaskName "CherryDesktopCCTV" -Confirm:$false -ErrorAction SilentlyContinue
foreach ($name in @("Cherry CCTV ONVIF Discovery", "Cherry CCTV ONVIF", "Cherry CCTV RTSP", "Cherry CCTV RTP UDP")) {
  Get-NetFirewallRule -DisplayName $name -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
}

if ($DeleteRecordings) {
  Remove-Item -Recurse -Force $InstallDir -ErrorAction SilentlyContinue
  Write-Host "Removed application and recordings: $InstallDir"
} else {
  foreach ($name in @("cherrycctv.exe", "ffmpeg.exe", "mediamtx.exe", "mediamtx.yml")) {
    Remove-Item (Join-Path $InstallDir $name) -Force -ErrorAction SilentlyContinue
  }
  Write-Host "Application removed. Config, logs and recordings preserved in $InstallDir"
}
