package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/capture"
	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
	"github.com/paddman/cherryrecdesktopcctvonvif/internal/logging"
	"github.com/paddman/cherryrecdesktopcctvonvif/internal/onvif"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.json", "path to JSON config")
	noCapture := flag.Bool("no-capture", false, "run ONVIF services only")
	check := flag.Bool("check", false, "validate configuration and runtime dependencies, then exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cherrycctv %s\n", version)
		return
	}

	log.SetFlags(log.LstdFlags | log.LUTC | log.Lmicroseconds)
	log.SetPrefix("cherrycctv ")

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	rotate, err := logging.New(cfg.LogFile, cfg.LogMaxMB, cfg.LogBackups)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}
	defer rotate.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, rotate))

	ip := cfg.AdvertiseIP
	if ip == "" {
		ip, err = firstLANIPv4()
		if err != nil {
			log.Fatalf("detect LAN IP: %v", err)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	mgr := capture.New(cfg)
	if *check {
		if *noCapture {
			log.Printf("configuration valid; capture dependency checks skipped")
			return
		}
		if err := mgr.Preflight(ctx); err != nil {
			log.Fatalf("preflight failed: %v", err)
		}
		log.Printf("configuration and runtime dependencies are valid")
		return
	}

	ready := mgr.Ready
	if *noCapture {
		ready = func() bool { return true }
	}
	srv := onvif.New(cfg, ip, ready)
	errCh := make(chan error, 3)

	go func() { errCh <- srv.Run(ctx) }()
	go func() { errCh <- srv.RunDiscovery(ctx) }()
	if !*noCapture {
		go func() { errCh <- mgr.Run(ctx, ip) }()
	}

	authMode := "digest+wsse"
	if cfg.InsecureNoAuth {
		authMode = "INSECURE-NO-AUTH"
	}
	log.Printf("version=%s device=%q onvif=%s rtsp=%s auth=%s", version, cfg.DeviceName, srv.DeviceURL(), srv.StreamURL(), authMode)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			log.Printf("fatal component error: %v", err)
			cancel()
		}
	}
}

func firstLANIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	var fallback string
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
			v4 := ip.To4()
			if v4 == nil || v4.IsLoopback() || (v4[0] == 169 && v4[1] == 254) {
				continue
			}
			if fallback == "" {
				fallback = v4.String()
			}
			if v4.IsPrivate() {
				return v4.String(), nil
			}
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("no usable IPv4 address found; set advertise_ip in config")
}
