# Cherry Desktop CCTV ONVIF

Turn a Windows desktop into a CCTV-like network video source.

The app continuously captures the desktop with FFmpeg, records rolling MP4 segments, publishes a low-latency H.264 RTSP stream through MediaMTX, and exposes a minimal ONVIF Device/Media service with WS-Discovery so compatible NVR/VMS software can discover the computer like a network camera.

## Architecture

```text
Windows Desktop
   | gdigrab
   v
 FFmpeg --------------------> recordings/*.mp4
   |
   +---- H.264/RTSP publish ----> MediaMTX :8554/screen
                                      ^
                                      |
NVR/VMS -- ONVIF discovery --> Cherry ONVIF :3702 UDP
NVR/VMS -- ONVIF SOAP -------> Cherry ONVIF :8088 TCP
NVR/VMS -- RTSP -------------> MediaMTX :8554 TCP
```

Important: ONVIF is used for discovery/device/media metadata. The actual video transport is RTSP/RTP, which is also how normal ONVIF CCTV cameras commonly expose their stream.

## Implemented MVP

- Windows desktop capture using FFmpeg `gdigrab`
- H.264 streaming over RTSP/TCP
- Continuous segmented MP4 recording
- Automatic retention cleanup by age
- ONVIF WS-Discovery responder on UDP 3702
- ONVIF Device service
  - GetDeviceInformation
  - GetSystemDateAndTime
  - GetCapabilities
  - GetServices
- ONVIF Media service
  - GetProfiles
  - GetProfile
  - GetStreamUri
- `/healthz` health endpoint

## Requirements

- Windows 10/11 or Windows Server with an interactive desktop session
- Go 1.23+ to build
- FFmpeg for Windows with `gdigrab` and `libx264`
- MediaMTX

Place `ffmpeg.exe` and `mediamtx.exe` next to the application or configure their paths in `config.json`.

## Build

```powershell
Copy-Item config.example.json config.json
go build -o cherrycctv.exe ./cmd/cherrycctv
.\cherrycctv.exe -config .\config.json
```

The default endpoints are:

```text
ONVIF Device: http://<PC-IP>:8088/onvif/device_service
ONVIF Media : http://<PC-IP>:8088/onvif/media_service
RTSP        : rtsp://<PC-IP>:8554/screen
Discovery   : UDP 239.255.255.250:3702
```

To test RTSP directly:

```powershell
ffplay rtsp://<PC-IP>:8554/screen
```

Run ONVIF without starting FFmpeg/MediaMTX:

```powershell
.\cherrycctv.exe -config .\config.json -no-capture
```

## Windows Firewall

Allow inbound UDP 3702 and TCP 8088/8554. See `scripts/install.ps1` for example commands.

## Configuration

`advertise_ip` should be set explicitly on PCs with multiple NICs, VPNs, Hyper-V, VMware, or Docker adapters. If left blank, the app picks the first usable non-loopback IPv4 address.

`segment_seconds` controls recording file length. `retention_days` removes old files periodically. Set `recording_enabled` to `false` if the machine should only act as a live screen camera.

## Current limits

This is an MVP, not yet a full ONVIF Profile S/T implementation. Authentication, Events, Replay/Search, multiple monitors, hardware encoders, audio capture, PTZ-style desktop region control, tray UI, Windows Service packaging, and ONVIF Recording/Search services are not implemented yet.

For a production NVR target, the next milestone should add WS-Security UsernameToken, dynamic monitor resolution in Media profiles, NVIDIA/Intel/AMD hardware encoder selection, a single-capture fan-out pipeline to avoid capturing the desktop twice, and an indexed recording database with playback/replay URI support.
