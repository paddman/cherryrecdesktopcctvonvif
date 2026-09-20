package config

import "testing"

func TestValidateProductionAuth(t *testing.T) {
	c := Default()
	c.ONVIFPassword = "a-strong-password"
	c.RTSPUsername = c.ONVIFUsername
	c.RTSPPassword = c.ONVIFPassword
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid config: %v", err)
	}
}

func TestValidateRejectsWeakPassword(t *testing.T) {
	c := Default()
	c.ONVIFPassword = "short"
	c.RTSPUsername = c.ONVIFUsername
	c.RTSPPassword = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("expected weak password to fail")
	}
}

func TestValidateInsecureModeForDev(t *testing.T) {
	c := Default()
	c.InsecureNoAuth = true
	if err := c.Validate(); err != nil {
		t.Fatalf("insecure dev mode should validate: %v", err)
	}
}

func TestValidateRejectsInvalidSubstream(t *testing.T) {
	c := Default()
	c.InsecureNoAuth = true
	c.SubstreamPath = c.RTSPPath
	if err := c.Validate(); err == nil {
		t.Fatal("expected duplicate main/substream RTSP path to fail")
	}
}

func TestDefaultSubstreamAndSnapshotSettings(t *testing.T) {
	c := Default()
	c.InsecureNoAuth = true
	if err := c.Validate(); err != nil {
		t.Fatalf("default substream settings should validate: %v", err)
	}
	if !c.SubstreamEnabled || c.SubstreamPath == "" || c.SnapshotRefreshMS != 1000 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.OSDTextFile == "" || c.OSDFontFile == "" || c.OSDFontSize != 24 {
		t.Fatalf("unexpected OSD defaults: %+v", c)
	}
}


func TestValidateRejectsInvalidOSDFontSize(t *testing.T) {
	c := Default()
	c.InsecureNoAuth = true
	c.OSDFontSize = 4
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid OSD font size to fail")
	}
}
