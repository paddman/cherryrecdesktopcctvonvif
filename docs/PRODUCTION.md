# Production deployment

## Security baseline

Production mode requires credentials unless `insecure_allow_no_auth` is explicitly set to `true`. Keep that switch `false` outside an isolated lab.

The ONVIF HTTP endpoints accept HTTP Digest authentication and WS-Security UsernameToken PasswordDigest. RTSP client access is configured for Digest authentication. The local FFmpeg publisher is anonymous only from loopback (`127.0.0.1` / `::1`) and has publish permission only for the configured stream path.

Use a unique password of at least 12 characters. The installer generates a longer random password by default and restricts the install directory ACL to the installing user and SYSTEM.

Firewall rules installed by `install.ps1` are scoped to `LocalSubnet`. Do not expose UDP 3702, TCP 8088, RTSP, or RTP ports directly to the public internet. Use a VPN for remote viewing.

## Why Scheduled Task instead of Windows Service

Windows services execute in Session 0. The desktop being recorded lives in the interactive user session. A Session-0 service is therefore the wrong execution model for `gdigrab` desktop capture. The installer creates a highest-privilege Scheduled Task triggered at user logon and configured to restart on failure.

## Runtime dependencies

`scripts/bootstrap.ps1` downloads pinned Windows binaries and verifies their SHA-256 before use:

- MediaMTX 1.21.0
- FFmpeg 9.0.1 essentials build

Do not replace them with arbitrary binaries in production without testing `cherrycctv.exe -check` and the target NVR/VMS.

## Recording durability

FFmpeg captures the desktop exactly once. When the substream is enabled, that single capture is split into main and lower-resolution H.264 outputs before both are published to MediaMTX. Only the main path is recorded by default. MediaMTX performs fragmented-MP4 recording. `recordPartDuration` is set to 1 second, limiting the normal crash-recovery loss window to approximately the currently open part. Recording age retention is handled by MediaMTX and `max_recording_gb` adds a second disk quota guard in CherryCCTV.

## Snapshot cache

A long-lived FFmpeg reader consumes the loopback main RTSP stream and refreshes an in-memory JPEG cache at `snapshot_refresh_ms`. HTTP snapshot requests are served from that cache, so NVR thumbnail polling does not create a new FFmpeg process for every request.

## Health monitoring

- `GET /healthz`: agent HTTP process is alive.
- `GET /readyz`: H.264 publisher is currently online.

A production monitor should alert when `/readyz` remains non-200 for more than a few restart cycles.

## Multi-NIC hosts

Set `advertise_ip` explicitly on hosts with VPN, Hyper-V, VMware, Docker, multiple LANs, or multiple default routes. WS-Discovery binds the interface matching that address.

## ONVIF scope

This project implements the ONVIF device/media operations needed by common NVR/VMS discovery and H.264 streaming workflows, including discovery, device information, capabilities, Media1 profiles, a Media2 interoperability baseline, encoder/source metadata, profile-aware stream URIs and snapshot URI. The default configuration exposes a main profile and a lower-bandwidth substream profile.

It is **not an ONVIF-certified product** and must not be marketed with an ONVIF profile conformance claim until it passes the official ONVIF device test tooling and the product has completed the applicable ONVIF conformance process. Profile S is also in deprecation; new compatibility work should target Profile T behavior.

## Acceptance gate before rollout

1. `cherrycctv.exe -config config.json -check` passes.
2. `/healthz` returns 200.
3. `/readyz` returns 200 for at least 30 minutes.
4. NVR discovers the device via ONVIF and authenticates successfully.
5. NVR opens both main and sub RTSP profiles with Digest auth and maintains a 24-hour stream soak test.
6. Media2-capable clients can call GetProfiles and GetStreamUri for both profiles.
7. Snapshot polling for at least 30 minutes does not spawn one FFmpeg process per HTTP request and returns fresh JPEG data.
8. Recordings survive forced termination/restart and remain playable.
9. Retention removes old data and disk quota never exceeds the configured threshold for sustained periods.
10. Reboot + user logon automatically restarts the agent.
11. Multi-NIC deployments verify that XAddr and RTSP URI contain the intended management IP.
12. Credentials are unique per endpoint/device and not shared across customer installations.
