// Package browser opens a URL in the user's browser
// (docs/adr/0004-platforms-and-distribution.md).
package browser

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNoDesktop means there is no browser to open, e.g. over SSH. Callers
// show the URL instead.
var ErrNoDesktop = errors.New("no desktop session to open a browser in")

// Opener names the program that opens URLs on this machine, or returns why
// there is none.
func Opener() (string, error) {
	args, err := command(runtime.GOOS, os.Getenv, exec.LookPath, "")
	if err != nil {
		return "", err
	}
	return strings.Join(args[:len(args)-1], " "), nil
}

// Open starts the browser and does not wait for it. The browser never gets
// the caller's stdin or stdout, which carry MCP.
func Open(url string) error {
	args, err := command(runtime.GOOS, os.Getenv, exec.LookPath, url)
	if err != nil {
		return err
	}
	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// command returns the program and arguments that open url:
//
//	Windows       rundll32 url.dll,FileProtocolHandler <url>
//	macOS         open <url>
//	WSL           wslview <url>, else explorer.exe <url>: the Windows browser
//	Linux, BSD    xdg-open <url>, if a display is set
func command(goos string, getenv func(string) string, lookPath func(string) (string, error), url string) ([]string, error) {
	switch goos {
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}, nil
	case "darwin":
		return []string{"open", url}, nil
	}
	if getenv("WSL_DISTRO_NAME") != "" {
		if _, err := lookPath("wslview"); err == nil {
			return []string{"wslview", url}, nil
		}
		if _, err := lookPath("explorer.exe"); err == nil {
			return []string{"explorer.exe", url}, nil
		}
		return nil, errors.New("neither wslview nor explorer.exe found: install wslu, or enable Windows interop in WSL")
	}
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return nil, ErrNoDesktop
	}
	if _, err := lookPath("xdg-open"); err != nil {
		return nil, errors.New("xdg-open not found: install xdg-utils")
	}
	return []string{"xdg-open", url}, nil
}
