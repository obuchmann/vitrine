package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (c Config) unitPath() string {
	return filepath.Join(c.ConfigDir, "systemd", "user", Name+".service")
}

// unit is the systemd --user unit. It restarts vitrine when it fails, e.g.
// while a daemon started by vitrine mcp still holds the port.
func (c Config) unit() string {
	args := append([]string{c.Exe}, c.serveArgs()...)
	for i, a := range args {
		args[i] = systemdQuote(a)
	}
	return fmt.Sprintf(`[Unit]
Description=vitrine: HTML prototypes for your agents
Documentation=https://github.com/obuchmann/vitrine

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=default.target
`, strings.Join(args, " "))
}

// systemdQuote quotes a word for ExecStart, where % starts a specifier.
func systemdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s)
	return `"` + s + `"`
}

func (c Config) systemctl(args ...string) error {
	args = append([]string{"--user"}, args...)
	out, err := c.Run("systemctl", args...)
	return runErr(err, out, "systemctl", args...)
}

func (c Config) installSystemd() error {
	if err := c.systemctl("daemon-reload"); err != nil {
		hint := "is systemd running for your user?"
		if c.WSL {
			hint = "enable systemd in /etc/wsl.conf ([boot] systemd=true) and restart WSL, or keep using vitrine mcp, which starts vitrine when needed"
		}
		return fmt.Errorf("%w\n%s", err, hint)
	}
	if err := writeFile(c.unitPath(), []byte(c.unit())); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", Name + ".service"}, {"restart", Name + ".service"}} {
		if err := c.systemctl(args...); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) uninstallSystemd() error {
	if !exists(c.unitPath()) {
		return nil
	}
	c.systemctl("disable", "--now", Name+".service") // may already be stopped
	if err := os.Remove(c.unitPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return c.systemctl("daemon-reload")
}

func (c Config) statusSystemd() Status {
	st := Status{Manager: c.Manager(), File: c.unitPath(), Installed: exists(c.unitPath())}
	if !st.Installed {
		return st
	}
	out, _ := c.Run("systemctl", "--user", "is-active", Name+".service")
	if s := strings.TrimSpace(string(out)); s != "" {
		st.Active = s
	}
	return st
}
