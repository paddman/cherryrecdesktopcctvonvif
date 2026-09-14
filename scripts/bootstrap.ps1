param(
  [string]$Destination = (Split-Path -Parent $PSScriptRoot)
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$mediaVersion = "1.21.0"
$mediaUrl = "https://github.com/bluenviron/mediamtx/releases/download/v$mediaVersion/mediamtx_v${mediaVersion}_windows_amd64.zip"
$mediaSha256 = "8a58a9b8c25ee99a96c23dc0a17f39ace3072c01d2e148329073c64ddf83493d"

$ffmpegVersion = "9.0.1"
$ffmpegUrl = "https://www.gyan.dev/ffmpeg/builds/packages/ffmpeg-${ffmpegVersion}-essentials_build.zip"
$ffmpegSha256 = "fec81ae03971d9dd4be3ebe02e263bd2ec1d789483f931bdba5f5715e65da2e9"

New-Item -ItemType Directory -Force -Path $Destination | Out-Null
$temp = Join-Path ([IO.Path]::GetTempPath()) ("cherrycctv-bootstrap-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $temp | Out-Null

function Get-VerifiedFile {
  param([string]$Url, [string]$OutFile, [string]$Sha256)
  Write-Host "Downloading $Url"
  Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $OutFile
  $actual = (Get-FileHash -Algorithm SHA256 -Path $OutFile).Hash.ToLowerInvariant()
  if ($actual -ne $Sha256.ToLowerInvariant()) {
    throw "SHA256 mismatch for $OutFile. expected=$Sha256 actual=$actual"
  }
}

try {
  $mediaZip = Join-Path $temp "mediamtx.zip"
  Get-VerifiedFile -Url $mediaUrl -OutFile $mediaZip -Sha256 $mediaSha256
  $mediaDir = Join-Path $temp "mediamtx"
  Expand-Archive -Path $mediaZip -DestinationPath $mediaDir -Force
  Copy-Item (Join-Path $mediaDir "mediamtx.exe") (Join-Path $Destination "mediamtx.exe") -Force

  $ffmpegZip = Join-Path $temp "ffmpeg.zip"
  Get-VerifiedFile -Url $ffmpegUrl -OutFile $ffmpegZip -Sha256 $ffmpegSha256
  $ffmpegDir = Join-Path $temp "ffmpeg"
  Expand-Archive -Path $ffmpegZip -DestinationPath $ffmpegDir -Force
  $ffmpegExe = Get-ChildItem -Path $ffmpegDir -Filter "ffmpeg.exe" -Recurse | Select-Object -First 1
  if (-not $ffmpegExe) { throw "ffmpeg.exe was not found in downloaded archive" }
  Copy-Item $ffmpegExe.FullName (Join-Path $Destination "ffmpeg.exe") -Force

  Write-Host "Verified runtime dependencies installed to $Destination"
  Write-Host "  MediaMTX $mediaVersion"
  Write-Host "  FFmpeg $ffmpegVersion"
}
finally {
  Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
}
