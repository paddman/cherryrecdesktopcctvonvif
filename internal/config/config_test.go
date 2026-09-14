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
