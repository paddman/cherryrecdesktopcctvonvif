# Cherry Desktop CCTV ONVIF

Cherry Desktop CCTV turns an interactive Windows desktop into a continuously recorded H.264 network-video source that can be discovered by ONVIF-capable NVR/VMS software.

## Production architecture

```text
Windows interactive desktop
        |
        | FFmpeg gdigrab (single capture)
        v
   raw H.264 publishers (loopback only)
        |
        +---- /screen_raw --------+---- fMP4 recording --> recordings/screen/*
        |                         |
        |                         v
        |                  Go RTP metadata relay
        |                         |
        |                         +---- H264/90000
        |                         +---- vnd.onvif.metadata/90000
        |                         |
        |                         v
        |                    public /screen ----> NVR / VMS
        |
        +---- /screen_sub_raw ----> metadata relay ----> public /screen_sub

NVR / VMS -- WS-Discovery --> UDP 3702
NVR / VMS -- ONVIF SOAP ---> TCP 8088 (Device + Media1 + Media2 + Events)
NVR / VMS -- PullPoint -----> TCP 8088 (/onvif/pullpoint/<id>)
NVR / VMS -- RTSP ---------> TCP 8554 / RTP UDP
```

The design deliberately separates ONVIF control/discovery from video transport. ONVIF returns the media URI; MediaMTX carries the H.264 stream and performs crash-tolerant fragmented-MP4 recording.

## Production hardening included

- HTTP Digest authentication for ONVIF
- WS-Security UsernameToken PasswordDigest compatibility with replay rejection
- RTSP Digest authentication for NVR/VMS readers
- local publisher restricted to loopback and publish-only permission
- stable per-device WS-Discovery UUID derived from serial number
- WS-Discovery Probe and Resolve responses
- dynamic video profiles from configured resolution/FPS/bitrate
- ONVIF Media1 plus a Media2 interoperability baseline for profiles, stream URI, snapshot URI and encoder discovery
- ONVIF Event Service with PullPoint subscriptions, synchronization points, renew/unsubscribe and VideoLoss property events
- RTP ONVIF metadata track using dynamic payload type, 90 kHz clock, closed `tt:MetaDataStream` XML documents and VideoLoss EventStream data
- Media1/Media2 metadata configuration discovery attached to both public stream profiles
- Media2 text OSD lifecycle (Create/Get/Set/Delete/GetOptions) backed by a live FFmpeg drawtext text file
- main + substream ONVIF profiles backed by one desktop capture pipeline
- one long-lived snapshot cache worker instead of spawning FFmpeg for every snapshot request
- ONVIF device/media operations used by common NVRs, including stream and snapshot URI
- single desktop capture with dual H.264 encode outputs when substream is enabled
- automatic NVIDIA NVENC, Intel QSV, AMD AMF, then x264 fallback probing
- FFmpeg and MediaMTX watchdog restart with exponential backoff
- MediaMTX fMP4 recording with 1-second record parts
- age retention plus maximum recording-size guard
- `/healthz` and `/readyz`
- rotating agent logs
- pinned runtime bootstrap with SHA-256 verification
- Windows logon Scheduled Task installer with restart policy
- LocalSubnet-scoped Windows Firewall rules
- CI format, vet, unit-test and Windows-build gate

## Install on Windows 10/11 or Windows Server Desktop Experience

Download the `cherry-desktop-cctv-windows` artifact from GitHub Actions, extract it, then open **PowerShell as Administrator** in the extracted directory:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\scripts\install.ps1
```

The installer downloads and verifies pinned FFmpeg and MediaMTX binaries when they are not already present. It generates a strong password for the ONVIF/RTSP user on first install and prints that password once.

The default endpoints are:

```text
ONVIF Device  http://<PC-IP>:8088/onvif/device_service
ONVIF Media   http://<PC-IP>:8088/onvif/media_service
ONVIF Media2  http://<PC-IP>:8088/onvif/media2_service
ONVIF Events  http://<PC-IP>:8088/onvif/events_service
Snapshot      http://<PC-IP>:8088/snapshot.jpg
RTSP main     rtsp://<PC-IP>:8554/screen
RTSP sub      rtsp://<PC-IP>:8554/screen_sub
Health        http://<PC-IP>:8088/healthz
Readiness     http://<PC-IP>:8088/readyz
Discovery     UDP 239.255.255.250:3702
```

Use the same username/password created by the installer for ONVIF and RTSP in the NVR/VMS.

## Manual run

For a manual installation, copy `config.example.json` to `config.json`, provide strong credentials either in that file or environment variables, place `ffmpeg.exe` and `mediamtx.exe` alongside the agent, then run:

```powershell
$env:CHERRY_ONVIF_PASSWORD = "replace-with-a-long-unique-password"
$env:CHERRY_RTSP_PASSWORD = $env:CHERRY_ONVIF_PASSWORD
.\cherrycctv.exe -config .\config.json -check
.\cherrycctv.exe -config .\config.json
```

Direct RTSP test:

```powershell
ffplay -rtsp_transport tcp rtsp://admin@<PC-IP>:8554/screen
```

FFplay will prompt or can be supplied a password depending on the build/client. Avoid placing passwords directly in shell history.

## Important configuration

- `advertise_ip`: set explicitly on multi-NIC/VPN/Hyper-V/VMware hosts.
- `width`, `height`, `offset_x`, `offset_y`: desktop region exposed as the camera.
- `fps`: main capture/output frame rate.
- `video_bitrate`: main stream bitrate, e.g. `4000k` or `8m`.
- `substream_enabled`: enable the lower-bandwidth second ONVIF/RTSP profile.
- `substream_path`, `substream_width`, `substream_height`, `substream_fps`, `substream_bitrate`: substream settings.
- `snapshot_refresh_ms`: refresh interval for the long-lived in-memory JPEG snapshot cache.
- `osd_text_file`: persistent text file used by the ONVIF Media2 OSD API and live FFmpeg overlay.
- `osd_font_file`, `osd_font_size`: font used for the text OSD. The current baseline intentionally exposes only a single UpperLeft plain-text OSD so the advertised options exactly match what the renderer can apply.
- `metadata_enabled`: publish an ONVIF metadata RTP track alongside H.264 on each public RTSP profile.
- `metadata_interval_ms`: interval between closed metadata XML documents; defaults to 1000 ms.
- `metadata_payload_type`: dynamic RTP payload type for `vnd.onvif.metadata/90000`; defaults to 107 and must be 96-127.
- `encoder`: `auto`, `h264_nvenc`, `h264_qsv`, `h264_amf`, or `libx264`.
- `segment_seconds`: fMP4 recording segment duration.
- `retention_days`: MediaMTX time-based retention.
- `max_recording_gb`: second disk-usage safety guard.
- `log_file`, `log_max_mb`, `log_backups`: bounded agent logging.
- `insecure_allow_no_auth`: laboratory escape hatch only. Keep `false` in production.

## Why this is not installed as a Windows Service

Desktop capture needs the interactive user's desktop. Windows Services run in Session 0 and cannot reliably record that desktop. The production installer therefore uses Task Scheduler at user logon with automatic restart. Turning this into a Session-0 service would be impressively official-looking and functionally wrong, a classic enterprise achievement.

## ONVIF status

This implementation targets practical ONVIF discovery, Device/Media1 control, a Media2 interoperability baseline, PullPoint events, text OSD, H.264 streaming, and an ONVIF RTP metadata track on public RTSP profiles. The metadata relay advertises `vnd.onvif.metadata/90000` and emits closed `tt:MetaDataStream` documents with VideoLoss property events. It is **not ONVIF-certified** and does not claim Profile T conformance. RTP metadata support removes one major gap, but official ONVIF device testing and the remaining mandatory Profile T behavior still determine conformance. Before marketing it as conformant, run the applicable official ONVIF device test suite and complete the ONVIF conformance process.

Profile S is being deprecated, so new compatibility work should target Profile T behavior.

See [`docs/PRODUCTION.md`](docs/PRODUCTION.md) for the deployment and acceptance checklist.
