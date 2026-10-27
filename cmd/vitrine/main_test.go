package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/obuchmann/vitrine/internal/store"
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

func TestServeWaitsForPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := held.Addr().String()
	defer func(d time.Duration) { portRetry = d }(portRetry)
	portRetry = 20 * time.Millisecond
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(store.Options{Dir: t.TempDir(), Limits: store.DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	cfg := serveConfig{addrs: []string{addr}}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })

	if err := run(cfg, h, st, 0, false, log); err == nil || !isAddrInUse(err) {
		t.Fatalf("without -wait-port: %v", err)
	}
	done := make(chan error, 1)
	// The idle exit ends the server once the test is done.
	go func() { done <- run(cfg, h, st, time.Second, true, log) }()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("gave up while the port was in use: %v", err)
	default:
	}
	held.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get("http://" + addr + "/")
		if err == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not listening after the port was freed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit when idle")
	}
}
