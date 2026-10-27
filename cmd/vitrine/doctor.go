package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/obuchmann/vitrine/internal/auth"
	"github.com/obuchmann/vitrine/internal/browser"
	"github.com/obuchmann/vitrine/internal/client"
	"github.com/obuchmann/vitrine/internal/service"
	"github.com/obuchmann/vitrine/internal/statedir"
)

// doctorCommand prints what vitrine finds on this machine. The bug report
// template asks for its output.
func doctorCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rawURL := fs.String("url", "http://127.0.0.1:"+DefaultPort, "the vitrine to check")
	stateDir := fs.String("state-dir", "", "the state directory to check (default: per-user state directory, or $"+statedir.EnvOverride+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := serviceConfig(*stateDir)
	if err != nil {
		return err
	}
	c := &client.Client{BaseURL: strings.TrimSuffix(*rawURL, "/")}
	env := doctorEnv{
		version:  version,
		platform: runtime.GOOS + "/" + runtime.GOARCH,
		goos:     runtime.GOOS,
		wsl:      wslInfo(os.Getenv, os.ReadFile),
		stateDir: svc.StateDir,
		baseURL:  c.BaseURL,
		health:   c.Health,
		service:  func() (service.Status, error) { return service.Query(svc) },
		opener:   browser.Opener,
	}
	if n := doctor(stdout, env); n > 0 {
		return fmt.Errorf("%d problem(s) found, marked with !", n)
	}
	return nil
}

type doctorEnv struct {
	version  string
	platform string // e.g. linux/amd64
	goos     string
	wsl      string // e.g. "WSL2 (Ubuntu)", empty outside WSL
	stateDir string
	baseURL  string
	health   func(context.Context) (string, error)
	service  func() (service.Status, error)
	opener   func() (string, error)
}

// doctor writes one line per check and returns the number of problems.
func doctor(w io.Writer, env doctorEnv) int {
	problems := 0
	line := func(ok bool, label, format string, a ...any) {
		mark := " "
		if !ok {
			mark = "!"
			problems++
		}
		fmt.Fprintf(w, "%s %-10s %s\n", mark, label, fmt.Sprintf(format, a...))
	}

	platform := env.platform
	if env.wsl != "" {
		platform += ", " + env.wsl
	}
	line(true, "vitrine", "%s (%s)", env.version, platform)

	if fi, err := os.Stat(env.stateDir); err == nil && fi.IsDir() {
		line(true, "state", "%s", env.stateDir)
	} else {
		line(true, "state", "%s (created on first start)", env.stateDir)
	}

	authFile := filepath.Join(env.stateDir, auth.FileName)
	switch fi, err := os.Stat(authFile); {
	case errors.Is(err, fs.ErrNotExist):
		line(true, "token", "%s (created on first start)", authFile)
	case err != nil:
		line(false, "token", "%s: %v", authFile, err)
	case env.goos == "windows":
		line(true, "token", "%s (protected by the profile ACL of %%LOCALAPPDATA%%)", authFile)
	case fi.Mode().Perm()&0o077 != 0:
		line(false, "token", "%s has mode %04o: other users can read it. Run: chmod 600 %q", authFile, fi.Mode().Perm(), authFile)
	default:
		line(true, "token", "%s (mode %04o)", authFile, fi.Mode().Perm())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	switch v, err := env.health(ctx); {
	case err == nil && v != env.version && v != "dev" && env.version != "dev":
		line(false, "server", "vitrine %s answers at %s, but this is %s. Restart it: \"vitrine service install\" for the service; "+
			"a vitrine started by vitrine mcp exits after 30 minutes without requests.", v, env.baseURL, env.version)
	case err == nil:
		line(true, "server", "vitrine %s answers at %s", v, env.baseURL)
	case errors.Is(err, client.ErrNotVitrine):
		line(false, "server", "another program uses %s. Stop it, or run vitrine on another port (vitrine mcp -url, vitrine serve -addr).", env.baseURL)
	default:
		line(true, "server", "nothing answers at %s (vitrine mcp starts vitrine when needed)", env.baseURL)
	}

	switch st, err := env.service(); {
	case errors.Is(err, service.ErrUnsupported):
		line(true, "service", "not supported on this platform")
	case err != nil:
		line(false, "service", "%v", err)
	default:
		line(true, "service", "%s", serviceLine(st))
	}

	switch o, err := env.opener(); {
	case err == nil:
		line(true, "browser", "%s", o)
	case errors.Is(err, browser.ErrNoDesktop):
		line(true, "browser", "none: no desktop session, agents give you the URL instead")
	default:
		line(false, "browser", "%v", err)
	}

	logPath := filepath.Join(env.stateDir, logFile)
	if fi, err := os.Stat(logPath); err == nil {
		line(true, "log", "%s (%d KB)", logPath, (fi.Size()+1023)/1024)
	}
	return problems
}

// wslInfo describes the WSL environment, or returns "" outside WSL.
func wslInfo(getenv func(string) string, readFile func(string) ([]byte, error)) string {
	distro := getenv("WSL_DISTRO_NAME")
	if distro == "" {
		return ""
	}
	kind := "WSL"
	if b, err := readFile("/proc/sys/kernel/osrelease"); err == nil {
		if strings.Contains(strings.ToLower(string(b)), "microsoft-standard") {
			kind = "WSL2"
		} else {
			kind = "WSL1"
		}
	}
	return kind + " (" + distro + ")"
}
