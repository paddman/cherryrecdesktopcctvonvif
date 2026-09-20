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

## RTP ONVIF metadata

When `metadata_enabled` is true, FFmpeg publishes H.264 only to loopback raw paths such as `screen_raw`. A Go RTSP relay reads the raw path and republishes the public `screen` path with two media tracks:

- H.264 video, forwarded without re-encoding.
- `application` RTP metadata advertised as `vnd.onvif.metadata/90000` with a configurable dynamic payload type (default 107).

Each metadata RTP packet contains one closed `tt:MetaDataStream` XML document and sets the RTP marker bit. The first document carries an Initialized VideoLoss property; later readiness transitions carry Changed events. Heartbeat documents are emitted at `metadata_interval_ms`.

Recording intentionally remains on the raw H.264 path. This keeps fragmented-MP4 recording independent from the generic RTP metadata track while preserving the existing `recordings/screen` storage layout.

## Snapshot cache

A long-lived FFmpeg reader consumes the loopback main RTSP stream and refreshes an in-memory JPEG cache at `snapshot_refresh_ms`. HTTP snapshot requests are served from that cache, so NVR thumbnail polling does not create a new FFmpeg process for every request.

## ONVIF events

The agent exposes `/onvif/events_service` and supports PullPoint subscriptions. The baseline event topic is `tns1:VideoSource/VideoLoss`; a readiness transition produces a Changed property event, and `SetSynchronizationPoint` produces the current state with PropertyOperation=Initialized. PullPoint subscriptions expire and support Renew and Unsubscribe.

## Text OSD

Media2 supports a single plain-text OSD applied to the `screen_source` video source configuration. The renderer uses FFmpeg `drawtext` with `textfile=...:reload=1`, so CreateOSD, SetOSD and DeleteOSD update the visible main and substream without restarting the encoder. The supported position is intentionally limited to UpperLeft and the supported font size is the configured `osd_font_size`; GetOSDOptions advertises only those capabilities.

## Health monitoring

- `GET /healthz`: agent HTTP process is alive.
- `GET /readyz`: H.264 publisher is currently online.

A production monitor should alert when `/readyz` remains non-200 for more than a few restart cycles.

## Multi-NIC hosts

Set `advertise_ip` explicitly on hosts with VPN, Hyper-V, VMware, Docker, multiple LANs, or multiple default routes. WS-Discovery binds the interface matching that address.

## ONVIF scope

This project implements the ONVIF device/media operations needed by common NVR/VMS discovery and H.264 streaming workflows, including discovery, device information, capabilities, Media1 profiles, a Media2 interoperability baseline, PullPoint event handling, text OSD, Media1/Media2 MetadataConfiguration discovery, RTP ONVIF metadata, encoder/source metadata, profile-aware stream URIs and snapshot URI. The default configuration exposes a main profile and a lower-bandwidth substream profile.

It is **not an ONVIF-certified product** and must not be marketed with an ONVIF profile conformance claim until it passes the official ONVIF device test tooling and the product has completed the applicable ONVIF conformance process. Profile S is also in deprecation; new compatibility work should target Profile T behavior.

## Acceptance gate before rollout

1. `cherrycctv.exe -config config.json -check` passes.
2. `/healthz` returns 200.
3. `/readyz` returns 200 for at least 30 minutes.
4. NVR discovers the device via ONVIF and authenticates successfully.
5. NVR opens both main and sub RTSP profiles with Digest auth and maintains a 24-hour stream soak test.
6. Media2-capable clients can call GetProfiles and GetStreamUri for both profiles.
7. Snapshot polling for at least 30 minutes does not spawn one FFmpeg process per HTTP request and returns fresh JPEG data.
8. Create a PullPoint subscription, call SetSynchronizationPoint, and verify PullMessages returns the current VideoLoss property state.
9. Force the capture offline/online and verify the active PullPoint receives a Changed VideoLoss event.
10. Create, read, update and delete the Media2 text OSD and verify the visible overlay changes on both main and substream.
11. Recordings survive forced termination/restart and remain playable.
12. Retention removes old data and disk quota never exceeds the configured threshold for sustained periods.
13. Reboot + user logon automatically restarts the agent.
14. Multi-NIC deployments verify that XAddr and RTSP URI contain the intended management IP.
15. Credentials are unique per endpoint/device and not shared across customer installations.
16. Inspect the public RTSP SDP and verify each enabled profile contains both H.264 and `vnd.onvif.metadata/90000` tracks.
17. Capture the metadata RTP track and verify payload type is dynamic, clock rate is 90000, marker is set on closed XML documents, and the XML root is `tt:MetaDataStream`.
18. Verify fMP4 recording remains playable from the raw H.264 source path while public NVR clients receive the metadata-enriched path.
19. Do not claim Profile T conformance until the remaining mandatory test cases pass the official ONVIF tooling.
