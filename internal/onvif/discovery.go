package onvif

import (
    "context"
    "fmt"
    "log"
    "net"
    "regexp"
    "strings"
)

var messageIDPattern = regexp.MustCompile(`<[^>]*MessageID[^>]*>([^<]+)</[^>]*MessageID>`) 

func (s *Server) RunDiscovery(ctx context.Context) error {
    addr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:3702")
    if err != nil { return err }
    conn, err := net.ListenMulticastUDP("udp4", nil, addr)
    if err != nil { return fmt.Errorf("listen WS-Discovery multicast: %w", err) }
    defer conn.Close()
    _ = conn.SetReadBuffer(64 * 1024)
    log.Printf("ONVIF WS-Discovery listening on 239.255.255.250:3702")

    go func() {
        <-ctx.Done()
        _ = conn.Close()
    }()

    buf := make([]byte, 64*1024)
    for {
        n, remote, err := conn.ReadFromUDP(buf)
        if err != nil {
            if ctx.Err() != nil { return nil }
            return err
        }
        msg := string(buf[:n])
        if !strings.Contains(msg, "Probe") { continue }

        relatesTo := ""
        if m := messageIDPattern.FindStringSubmatch(msg); len(m) == 2 { relatesTo = m[1] }
        response := s.probeMatches(relatesTo)
        if _, err := conn.WriteToUDP([]byte(response), remote); err != nil {
            log.Printf("WS-Discovery response error: %v", err)
        }
    }
}

func (s *Server) probeMatches(relatesTo string) string {
    rel := ""
    if relatesTo != "" {
        rel = `<a:RelatesTo>` + xmlEsc(relatesTo) + `</a:RelatesTo>`
    }
    return `<?xml version="1.0" encoding="UTF-8"?>` +
        `<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">` +
        `<e:Header><a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</a:Action>` + rel +
        `<a:MessageID>urn:uuid:cherry-desktop-cctv</a:MessageID><d:AppSequence InstanceId="1" MessageNumber="1"/></e:Header>` +
        `<e:Body><d:ProbeMatches><d:ProbeMatch><a:EndpointReference><a:Address>urn:uuid:cherry-desktop-cctv</a:Address></a:EndpointReference>` +
        `<d:Types>dn:NetworkVideoTransmitter</d:Types><d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/name/` + xmlEsc(strings.ReplaceAll(s.cfg.DeviceName, " ", "_")) + `</d:Scopes>` +
        `<d:XAddrs>` + s.DeviceURL() + `</d:XAddrs><d:MetadataVersion>1</d:MetadataVersion></d:ProbeMatch></d:ProbeMatches></e:Body></e:Envelope>`
}
