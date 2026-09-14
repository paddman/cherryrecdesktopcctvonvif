param(
  [string]$InstallDir = "$env:ProgramData\CherryDesktopCCTV",
  [string]$Username = "admin",
  [string]$Password = ""
)

$ErrorActionPreference = "Stop"

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Run install.ps1 from an elevated PowerShell window (Run as Administrator)."
}

$sourceRoot = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $sourceRoot "cherrycctv.exe"))) {
  throw "cherrycctv.exe not found in $sourceRoot"
}

if (-not (Test-Path (Join-Path $sourceRoot "ffmpeg.exe")) -or -not (Test-Path (Join-Path $sourceRoot "mediamtx.exe"))) {
  & (Join-Path $PSScriptRoot "bootstrap.ps1") -Destination $sourceRoot
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir "recordings") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir "logs") | Out-Null

foreach ($name in @("cherrycctv.exe", "ffmpeg.exe", "mediamtx.exe", "mediamtx.yml")) {
  Copy-Item (Join-Path $sourceRoot $name) (Join-Path $InstallDir $name) -Force
}

$configPath = Join-Path $InstallDir "config.json"
$newCredential = $false
if (-not (Test-Path $configPath)) {
  if ([string]::IsNullOrWhiteSpace($Password)) {
    $bytes = New-Object byte[] 24
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $Password = [Convert]::ToBase64String($bytes).TrimEnd('=')
    $newCredential = $true
  }
  if ($Password.Length -lt 12) { throw "Password must be at least 12 characters." }

  $cfg = Get-Content (Join-Path $sourceRoot "config.example.json") -Raw | ConvertFrom-Json
  $cfg.recording_dir = (Join-Path $InstallDir "recordings")
  $cfg.ffmpeg_path = (Join-Path $InstallDir "ffmpeg.exe")
  $cfg.mediamtx_path = (Join-Path $InstallDir "mediamtx.exe")
  $cfg.mediamtx_config = (Join-Path $InstallDir "mediamtx.yml")
  $cfg.log_file = (Join-Path $InstallDir "logs\cherrycctv.log")
  $cfg.onvif_username = $Username
  $cfg.onvif_password = $Password
  $cfg.onvif_password_env = ""
  $cfg.rtsp_username = $Username
  $cfg.rtsp_password = $Password
  $cfg.rtsp_password_env = ""
  $cfg.insecure_allow_no_auth = $false
  $json = $cfg | ConvertTo-Json -Depth 10
  [IO.File]::WriteAllText($configPath, $json, (New-Object Text.UTF8Encoding($false)))
}

# Restrict local files because config.json contains the ONVIF/RTSP credential.
$userId = $identity.Name
& icacls $InstallDir /inheritance:r /grant:r "${userId}:(OI)(CI)F" "SYSTEM:(OI)(CI)F" /T /C | Out-Null

$rules = @(
  @{ Name = "Cherry CCTV ONVIF Discovery"; Protocol = "UDP"; Port = 3702 },
  @{ Name = "Cherry CCTV ONVIF"; Protocol = "TCP"; Port = 8088 },
  @{ Name = "Cherry CCTV RTSP"; Protocol = "TCP"; Port = 8554 },
  @{ Name = "Cherry CCTV RTP UDP"; Protocol = "UDP"; Port = "8000-8001" }
)
foreach ($rule in $rules) {
  Get-NetFirewallRule -DisplayName $rule.Name -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
  New-NetFirewallRule -DisplayName $rule.Name -Direction Inbound -Protocol $rule.Protocol -LocalPort $rule.Port -RemoteAddress LocalSubnet -Action Allow | Out-Null
}

$exe = Join-Path $InstallDir "cherrycctv.exe"
$action = New-ScheduledTaskAction -Execute $exe -Argument "-config `"$configPath`"" -WorkingDirectory $InstallDir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $userId
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $userId -LogonType Interactive -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName "CherryDesktopCCTV" -Action $action -Trigger $trigger -Principal $taskPrincipal -Settings $settings -Force | Out-Null

& $exe -config $configPath -check
Start-ScheduledTask -TaskName "CherryDesktopCCTV"

Write-Host ""
Write-Host "Cherry Desktop CCTV installed: $InstallDir"
Write-Host "ONVIF device: http://<PC-IP>:8088/onvif/device_service"
Write-Host "RTSP stream: rtsp://<PC-IP>:8554/screen"
Write-Host "Username: $Username"
if ($newCredential) {
  Write-Host "Generated password: $Password"
  Write-Warning "Store this password now. The installer will not print it again on upgrades."
} else {
  Write-Host "Existing config/credentials were preserved." 
}
Write-Host ""
Write-Host "The agent is intentionally a logon Scheduled Task, not a Windows Service: desktop capture must run in the interactive user session."
