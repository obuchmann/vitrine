package service

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// task is the Task Scheduler definition: start at the user's logon with
// the user's normal rights, no time limit, and restart after a failure,
// e.g. while a daemon started by vitrine mcp still holds the port.
func (c Config) task() string {
	var args []string
	for _, a := range c.serveArgs() {
		args = append(args, windowsQuote(a))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>vitrine: HTML prototypes for your agents</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%[1]s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%[1]s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%[2]s</Command>
      <Arguments>%[3]s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(c.User), xmlEscape(c.Exe), xmlEscape(strings.Join(args, " ")))
}

// windowsQuote quotes an argument for CommandLineToArgvW when needed.
func windowsQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
		case '"':
			// Backslashes before a quote are doubled, and the quote escaped.
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
			b.WriteRune(r)
			continue
		default:
			slashes = 0
		}
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// utf16File encodes the task as schtasks expects: UTF-16LE with a BOM.
func utf16File(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 2, 2+2*len(units))
	out[0], out[1] = 0xFF, 0xFE
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func (c Config) schtasks(args ...string) error {
	out, err := c.Run("schtasks", args...)
	return runErr(err, out, "schtasks", args...)
}

func (c Config) installTask() error {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(c.StateDir, "task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(utf16File(c.task()))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	c.schtasks("/End", "/TN", Name) // not running on a first install
	if err := c.schtasks("/Create", "/TN", Name, "/XML", f.Name(), "/F"); err != nil {
		return err
	}
	return c.schtasks("/Run", "/TN", Name)
}

func (c Config) uninstallTask() error {
	if !c.statusTask().Installed {
		return nil
	}
	c.schtasks("/End", "/TN", Name)
	return c.schtasks("/Delete", "/TN", Name, "/F")
}

// statusTask leaves Active empty: schtasks prints the state in the language
// of the system, so callers check the port instead.
func (c Config) statusTask() Status {
	_, err := c.Run("schtasks", "/Query", "/TN", Name)
	return Status{Manager: c.Manager(), File: `Task Scheduler\` + Name, Installed: err == nil}
}
