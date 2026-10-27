// Package service registers vitrine serve as a per-user background service
// (docs/adr/0001-runtime-modes.md, docs/adr/0004-platforms-and-distribution.md):
//
//	Linux, WSL  systemd --user unit
//	macOS       launchd agent
//	Windows     Task Scheduler task at logon
//
// Nothing here needs administrator rights.
package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Name is the systemd unit and Task Scheduler task name; Label is the
// launchd label.
const (
	Name  = "vitrine"
	Label = "io.github.obuchmann.vitrine"
)

// ErrUnsupported means the platform has no service manager vitrine can use.
var ErrUnsupported = errors.New("no supported service manager")

// Runner runs a command and returns its combined output.
type Runner func(name string, args ...string) ([]byte, error)

// Config describes the service to install.
type Config struct {
	GOOS      string
	Exe       string // absolute path of the vitrine binary
	StateDir  string
	ConfigDir string // Linux: $XDG_CONFIG_HOME or ~/.config
	Home      string // macOS: the user's home
	UID       string // macOS: the user's numeric id
	User      string // Windows: DOMAIN\user
	WSL       bool
	Run       Runner
}

// Status is what the service manager knows.
type Status struct {
	Manager   string // e.g. "systemd --user"
	File      string // the unit, plist or task name
	Installed bool
	Active    string // "active", "inactive" or "" if the manager cannot tell
}

// Manager names the service manager of the platform.
func (c Config) Manager() string {
	switch c.GOOS {
	case "linux":
		return "systemd --user"
	case "darwin":
		return "launchd"
	case "windows":
		return "Task Scheduler"
	}
	return ""
}

// Install writes the service definition, enables it for every login and
// starts it, replacing an earlier installation.
func Install(c Config) error {
	if strings.Contains(c.Exe, "go-build") {
		return fmt.Errorf("%s is a temporary build: install vitrine first, e.g. with go install", c.Exe)
	}
	switch c.GOOS {
	case "linux":
		return c.installSystemd()
	case "darwin":
		return c.installLaunchd()
	case "windows":
		return c.installTask()
	}
	return fmt.Errorf("%w on %s: run vitrine serve yourself, or use vitrine mcp", ErrUnsupported, c.GOOS)
}

// Uninstall stops the service and removes its definition.
func Uninstall(c Config) error {
	switch c.GOOS {
	case "linux":
		return c.uninstallSystemd()
	case "darwin":
		return c.uninstallLaunchd()
	case "windows":
		return c.uninstallTask()
	}
	return fmt.Errorf("%w on %s", ErrUnsupported, c.GOOS)
}

// Query reports whether the service is installed and running.
func Query(c Config) (Status, error) {
	switch c.GOOS {
	case "linux":
		return c.statusSystemd(), nil
	case "darwin":
		return c.statusLaunchd(), nil
	case "windows":
		return c.statusTask(), nil
	}
	return Status{}, fmt.Errorf("%w on %s", ErrUnsupported, c.GOOS)
}

func (c Config) serveArgs() []string {
	return []string{"serve", "-state-dir", c.StateDir}
}

func (c Config) logFile() string { return filepath.Join(c.StateDir, "vitrine.log") }

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runErr adds the command and its output to an error.
func runErr(err error, out []byte, name string, args ...string) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg != "" {
		msg = ": " + msg
	}
	return fmt.Errorf("%s %s: %w%s", name, strings.Join(args, " "), err, msg)
}
