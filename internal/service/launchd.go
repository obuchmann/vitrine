package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (c Config) plistPath() string {
	return filepath.Join(c.Home, "Library", "LaunchAgents", Label+".plist")
}

func (c Config) domain() string { return "gui/" + c.UID }

// plist is the launchd agent. launchd starts it again unless it exited
// cleanly, at most every 10 s.
func (c Config) plist() string {
	var args strings.Builder
	for _, a := range append([]string{c.Exe}, c.serveArgs()...) {
		fmt.Fprintf(&args, "\n\t\t<string>%s</string>", xmlEscape(a))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>%s
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, Label, args.String(), xmlEscape(c.logFile()), xmlEscape(c.logFile()))
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (c Config) launchctl(args ...string) error {
	out, err := c.Run("launchctl", args...)
	return runErr(err, out, "launchctl", args...)
}

func (c Config) installLaunchd() error {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil { // for the log
		return err
	}
	if err := writeFile(c.plistPath(), []byte(c.plist())); err != nil {
		return err
	}
	c.launchctl("bootout", c.domain()+"/"+Label) // not loaded on a first install
	return c.launchctl("bootstrap", c.domain(), c.plistPath())
}

func (c Config) uninstallLaunchd() error {
	if !exists(c.plistPath()) {
		return nil
	}
	c.launchctl("bootout", c.domain()+"/"+Label)
	if err := os.Remove(c.plistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (c Config) statusLaunchd() Status {
	st := Status{Manager: c.Manager(), File: c.plistPath(), Installed: exists(c.plistPath())}
	if !st.Installed {
		return st
	}
	out, err := c.Run("launchctl", "print", c.domain()+"/"+Label)
	switch {
	case err != nil:
		st.Active = "not loaded"
	case strings.Contains(string(out), "state = running"):
		st.Active = "active"
	default:
		st.Active = "inactive"
	}
	return st
}
