package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/obuchmann/vitrine/internal/auth"
	"github.com/obuchmann/vitrine/internal/browser"
	"github.com/obuchmann/vitrine/internal/client"
	"github.com/obuchmann/vitrine/internal/service"
)

func healthy(v string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return v, nil }
}

func testEnv(t *testing.T) doctorEnv {
	t.Helper()
	dir := t.TempDir()
	if _, err := auth.LoadOrCreate(filepath.Join(dir, auth.FileName)); err != nil {
		t.Fatal(err)
	}
	return doctorEnv{
		version:  "0.1.0",
		platform: "linux/amd64",
		goos:     runtime.GOOS,
		stateDir: dir,
		baseURL:  "http://127.0.0.1:8720",
		health:   healthy("0.1.0"),
		service: func() (service.Status, error) {
			return service.Status{Manager: "systemd --user", File: "/u/vitrine.service", Installed: true, Active: "active"}, nil
		},
		opener: func() (string, error) { return "xdg-open", nil },
	}
}

func runDoctor(env doctorEnv) (string, int) {
	var b strings.Builder
	n := doctor(&b, env)
	return b.String(), n
}

func TestDoctorAllGood(t *testing.T) {
	env := testEnv(t)
	env.wsl = "WSL2 (Ubuntu)"
	out, n := runDoctor(env)
	for _, want := range []string{
		"vitrine    0.1.0 (linux/amd64, WSL2 (Ubuntu))",
		"state      " + env.stateDir,
		"token      " + filepath.Join(env.stateDir, "auth"),
		"server     vitrine 0.1.0 answers at http://127.0.0.1:8720",
		"service    systemd --user: installed (/u/vitrine.service), active",
		"browser    xdg-open",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if n != 0 || strings.Contains(out, "!") {
		t.Fatalf("%d problems:\n%s", n, out)
	}
	if strings.Contains(out, "vitrine_") {
		t.Fatalf("doctor prints the token:\n%s", out)
	}
}

func TestDoctorProblems(t *testing.T) {
	env := testEnv(t)
	env.health = healthy("0.0.9")
	env.opener = func() (string, error) { return "", errors.New("xdg-open not found: install xdg-utils") }
	env.service = func() (service.Status, error) { return service.Status{}, errors.New("systemctl failed") }
	if runtime.GOOS != "windows" {
		os.Chmod(filepath.Join(env.stateDir, "auth"), 0o644)
	}
	out, n := runDoctor(env)
	want := 3
	if runtime.GOOS != "windows" {
		want = 4
		if !strings.Contains(out, "! token") || !strings.Contains(out, "chmod 600") {
			t.Errorf("open token file not reported:\n%s", out)
		}
	}
	if n != want || !strings.Contains(out, "! server     vitrine 0.0.9 answers") || !strings.Contains(out, "! browser") || !strings.Contains(out, "! service") {
		t.Fatalf("%d problems, want %d:\n%s", n, want, out)
	}
}

func TestDoctorServerStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		health  func(context.Context) (string, error)
		want    string
		problem bool
	}{
		{"not running", func(context.Context) (string, error) { return "", errors.New("connection refused") }, "nothing answers", false},
		{"other program", func(context.Context) (string, error) { return "", fmt.Errorf("%w: status 404", client.ErrNotVitrine) }, "another program uses", true},
		{"dev build", healthy("dev"), "vitrine dev answers", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			env.health = tc.health
			out, n := runDoctor(env)
			if !strings.Contains(out, tc.want) || (n > 0) != tc.problem {
				t.Fatalf("%d problems:\n%s", n, out)
			}
		})
	}
}

func TestDoctorFreshMachine(t *testing.T) {
	env := testEnv(t)
	env.stateDir = filepath.Join(env.stateDir, "missing")
	env.opener = func() (string, error) { return "", browser.ErrNoDesktop }
	env.service = func() (service.Status, error) { return service.Status{}, service.ErrUnsupported }
	out, n := runDoctor(env)
	if n != 0 || strings.Count(out, "created on first start") != 2 || !strings.Contains(out, "no desktop session") || !strings.Contains(out, "not supported") {
		t.Fatalf("%d problems:\n%s", n, out)
	}
}

func TestWSLInfo(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	release := func(s string) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return []byte(s), nil }
	}
	if got := wslInfo(env(nil), release("")); got != "" {
		t.Errorf("outside WSL: %q", got)
	}
	ubuntu := env(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	if got := wslInfo(ubuntu, release("5.15.167.4-microsoft-standard-WSL2\n")); got != "WSL2 (Ubuntu)" {
		t.Errorf("WSL2: %q", got)
	}
	if got := wslInfo(ubuntu, release("4.4.0-19041-Microsoft\n")); got != "WSL1 (Ubuntu)" {
		t.Errorf("WSL1: %q", got)
	}
}
