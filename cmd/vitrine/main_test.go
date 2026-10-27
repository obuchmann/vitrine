package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestConfigureDefaultsToLoopbackWithTokenFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := configure("", "", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.addrs, []string{"127.0.0.1:8720", "[::1]:8720"}) || cfg.publicURL != "http://127.0.0.1:8720" {
		t.Fatalf("defaults: %+v", cfg)
	}
	if cfg.authFile != filepath.Join(dir, "auth") || cfg.token == "" {
		t.Fatalf("token file: %+v", cfg)
	}
	if b, _ := os.ReadFile(cfg.authFile); !strings.Contains(string(b), cfg.token) {
		t.Fatal("token not written to the token file")
	}
	for _, h := range []string{"127.0.0.1:8720", "localhost:8720", "[::1]:8720"} {
		if !slices.Contains(cfg.hosts, h) {
			t.Errorf("host %s not allowed", h)
		}
	}
}

func TestConfigureRefusesOpenNetworkWithoutToken(t *testing.T) {
	if _, err := configure("0.0.0.0:8720", "https://v.example", t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "VITRINE_TOKEN") {
		t.Fatalf("got %v", err)
	}
	if _, err := configure("0.0.0.0:8720", "", t.TempDir(), "secret"); err == nil || !strings.Contains(err.Error(), "public-url") {
		t.Fatalf("got %v", err)
	}
	cfg, err := configure("0.0.0.0:8720", "https://v.example/", t.TempDir(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.authFile != "" || cfg.token != "secret" || cfg.publicURL != "https://v.example" || !slices.Contains(cfg.hosts, "v.example") {
		t.Fatalf("network config: %+v", cfg)
	}
}

func TestConfigureLoopbackAddresses(t *testing.T) {
	for _, a := range []string{"localhost:9000", "127.0.0.1:9000", "[::1]:9000"} {
		cfg, err := configure(a, "", t.TempDir(), "")
		if err != nil || cfg.publicURL != "http://127.0.0.1:9000" {
			t.Errorf("%s: %+v %v", a, cfg, err)
		}
	}
}
