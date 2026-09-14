package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "net"
    "os"
    "os/signal"

    "github.com/paddman/cherryrecdesktopcctvonvif/internal/capture"
    "github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
    "github.com/paddman/cherryrecdesktopcctvonvif/internal/onvif"
)

func main() {
    configPath := flag.String("config", "config.json", "path to JSON config")
    noCapture := flag.Bool("no-capture", false, "run ONVIF services only")
    flag.Parse()

    cfg, err := config.Load(*configPath)
    if err != nil {
        log.Fatalf("load config: %v", err)
    }

    ip := cfg.AdvertiseIP
    if ip == "" {
        ip, err = firstLANIPv4()
        if err != nil {
            log.Fatalf("detect LAN IP: %v", err)
        }
    }

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()

    srv := onvif.New(cfg, ip)
    go func() {
        if err := srv.RunDiscovery(ctx); err != nil && ctx.Err() == nil {
            log.Printf("WS-Discovery stopped: %v", err)
        }
    }()
    go func() {
        if err := srv.Run(); err != nil && ctx.Err() == nil {
            log.Printf("ONVIF HTTP stopped: %v", err)
            cancel()
        }
    }()

    log.Printf("device=%q onvif=%s rtsp=%s", cfg.DeviceName, srv.DeviceURL(), srv.StreamURL())

    if *noCapture {
        <-ctx.Done()
        return
    }

    mgr := capture.New(cfg)
    if err := mgr.Run(ctx, ip); err != nil && ctx.Err() == nil {
        log.Fatalf("capture service: %v", err)
    }
}

func firstLANIPv4() (string, error) {
    ifaces, err := net.Interfaces()
    if err != nil {
        return "", err
    }
    for _, iface := range ifaces {
        if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
            continue
        }
        addrs, _ := iface.Addrs()
        for _, a := range addrs {
            var ip net.IP
            switch v := a.(type) {
            case *net.IPNet:
                ip = v.IP
            case *net.IPAddr:
                ip = v.IP
            }
            if ip == nil || ip.IsLoopback() || ip.To4() == nil {
                continue
            }
            v4 := ip.To4()
            if v4[0] == 169 && v4[1] == 254 {
                continue
            }
            return v4.String(), nil
        }
    }
    return "", fmt.Errorf("no usable IPv4 address found; set advertise_ip in config")
}
