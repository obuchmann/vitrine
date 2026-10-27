package service

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// recorder is a Runner that records commands and fails those in fail.
type recorder struct {
	calls []string
	fail  map[string]bool
	out   map[string]string
}

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, cmd)
	if r.fail[cmd] {
		return []byte("failed"), errors.New("exit status 1")
	}
	return []byte(r.out[cmd]), nil
}

func newConfig(t *testing.T, goos string) (Config, *recorder) {
	t.Helper()
	dir := t.TempDir()
	r := &recorder{fail: map[string]bool{}, out: map[string]string{}}
	return Config{
		GOOS:      goos,
		Exe:       filepath.Join(dir, "bin", "vitrine"),
		StateDir:  filepath.Join(dir, "state 100%"),
		ConfigDir: filepath.Join(dir, "config"),
		Home:      filepath.Join(dir, "home"),
		UID:       "501",
		User:      `DESKTOP\me`,
		Run:       r.run,
	}, r
}

func TestSystemd(t *testing.T) {
	c, r := newConfig(t, "linux")
	c.Exe, c.StateDir = "/home/me/.local/bin/vitrine", "/home/me/state 100%"
	if err := Install(c); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(c.unitPath())
	if err != nil {
		t.Fatal(err)
	}
	wantExec := `ExecStart="/home/me/.local/bin/vitrine" "serve" "-state-dir" "/home/me/state 100%%"`
	if !strings.Contains(string(unit), wantExec+"\n") || !strings.Contains(string(unit), "Restart=on-failure") {
		t.Fatalf("unit:\n%s\nwant %s", unit, wantExec)
	}
	want := "systemctl --user daemon-reload|systemctl --user daemon-reload|systemctl --user enable vitrine.service|systemctl --user restart vitrine.service"
	if got := strings.Join(r.calls, "|"); got != want {
		t.Fatalf("calls\n got %s\nwant %s", got, want)
	}

	r.out["systemctl --user is-active vitrine.service"] = "active\n"
	if st, _ := Query(c); !st.Installed || st.Active != "active" || st.Manager != "systemd --user" {
		t.Fatalf("status: %+v", st)
	}

	r.calls = nil
	if err := Uninstall(c); err != nil {
		t.Fatal(err)
	}
	if exists(c.unitPath()) || strings.Join(r.calls, "|") != "systemctl --user disable --now vitrine.service|systemctl --user daemon-reload" {
		t.Fatalf("uninstall left %v, calls %v", exists(c.unitPath()), r.calls)
	}
	if st, _ := Query(c); st.Installed {
		t.Fatalf("status after uninstall: %+v", st)
	}
}

func TestSystemdQuote(t *testing.T) {
	for in, want := range map[string]string{
		`/a b/c`: `"/a b/c"`,
		`50%`:    `"50%%"`,
		`a\b"c`:  `"a\\b\"c"`,
	} {
		if got := systemdQuote(in); got != want {
			t.Errorf("systemdQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSystemdMissingOnWSL(t *testing.T) {
	c, r := newConfig(t, "linux")
	c.WSL = true
	r.fail["systemctl --user daemon-reload"] = true
	if err := Install(c); err == nil || !strings.Contains(err.Error(), "/etc/wsl.conf") {
		t.Fatalf("got %v", err)
	}
	if exists(c.unitPath()) {
		t.Fatal("unit written without systemd")
	}
}

func TestLaunchd(t *testing.T) {
	c, r := newConfig(t, "darwin")
	c.Exe = "/opt/homebrew/bin/vitrine"
	if err := Install(c); err != nil {
		t.Fatal(err)
	}
	plist, err := os.ReadFile(c.plistPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{}
	if err := xml.Unmarshal(plist, &doc); err != nil {
		t.Fatalf("plist is not XML: %v\n%s", err, plist)
	}
	for _, want := range []string{"<string>" + Label + "</string>", "<string>/opt/homebrew/bin/vitrine</string>", "<string>-state-dir</string>",
		"<key>RunAtLoad</key>", filepath.Join(c.StateDir, "vitrine.log")} {
		if !strings.Contains(string(plist), want) {
			t.Errorf("plist misses %s:\n%s", want, plist)
		}
	}
	want := "launchctl bootout gui/501/" + Label + "|launchctl bootstrap gui/501 " + c.plistPath()
	if got := strings.Join(r.calls, "|"); got != want {
		t.Fatalf("calls\n got %s\nwant %s", got, want)
	}

	r.out["launchctl print gui/501/"+Label] = "\tstate = running\n"
	if st, _ := Query(c); !st.Installed || st.Active != "active" {
		t.Fatalf("status: %+v", st)
	}
	if err := Uninstall(c); err != nil || exists(c.plistPath()) {
		t.Fatalf("uninstall: %v", err)
	}
}

func TestTaskScheduler(t *testing.T) {
	c, r := newConfig(t, "windows")
	c.Exe = `C:\Users\me\AppData\Local\Programs\vitrine\vitrine.exe`
	c.StateDir = filepath.Join(t.TempDir(), "My State") // must exist for the task file
	var task string
	c.Run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 4 && args[0] == "/Create" {
			b, err := os.ReadFile(args[4])
			if err != nil {
				t.Fatal(err)
			}
			task = decodeUTF16(t, b)
		}
		return r.run(name, args...)
	}
	if err := Install(c); err != nil {
		t.Fatal(err)
	}
	var doc struct{}
	if err := xml.Unmarshal([]byte(strings.Replace(task, `encoding="UTF-16"`, "", 1)), &doc); err != nil {
		t.Fatalf("task is not XML: %v\n%s", err, task)
	}
	for _, want := range []string{"<UserId>DESKTOP\\me</UserId>", "<LogonTrigger>", "<Command>" + c.Exe + "</Command>",
		"<Arguments>serve -state-dir &#34;" + c.StateDir + "&#34; -wait-port</Arguments>", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>"} {
		if !strings.Contains(task, want) {
			t.Errorf("task misses %s:\n%s", want, task)
		}
	}
	if len(r.calls) != 3 || r.calls[0] != "schtasks /End /TN vitrine" || !strings.HasPrefix(r.calls[1], "schtasks /Create /TN vitrine /XML ") || r.calls[2] != "schtasks /Run /TN vitrine" {
		t.Fatalf("calls %v", r.calls)
	}
	if entries, _ := os.ReadDir(c.StateDir); len(entries) != 0 {
		t.Fatalf("task file left behind: %v", entries)
	}

	if st, _ := Query(c); !st.Installed || st.Active != "" {
		t.Fatalf("status: %+v", st)
	}
	r.calls = nil
	if err := Uninstall(c); err != nil || strings.Join(r.calls, "|") != "schtasks /Query /TN vitrine|schtasks /End /TN vitrine|schtasks /Delete /TN vitrine /F" {
		t.Fatalf("uninstall: %v %v", err, r.calls)
	}
	r.fail["schtasks /Query /TN vitrine"] = true
	if st, _ := Query(c); st.Installed {
		t.Fatalf("status when missing: %+v", st)
	}
}

func decodeUTF16(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE || len(b)%2 != 0 {
		t.Fatalf("not UTF-16LE with BOM: % x", b[:min(len(b), 8)])
	}
	units := make([]uint16, 0, len(b)/2-1)
	for i := 2; i < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

func TestWindowsQuote(t *testing.T) {
	for in, want := range map[string]string{
		`serve`:               `serve`,
		`C:\a b\c`:            `"C:\a b\c"`,
		`C:\a b\`:             `"C:\a b\\"`,
		`say "hi"`:            `"say \"hi\""`,
		`a\"b`:                `"a\\\"b"`,
		``:                    `""`,
		`C:\no\spaces\needed`: `C:\no\spaces\needed`,
	} {
		if got := windowsQuote(in); got != want {
			t.Errorf("windowsQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRefusesTemporaryBuildAndUnknownPlatform(t *testing.T) {
	c, _ := newConfig(t, "linux")
	c.Exe = "/tmp/go-build123/b001/exe/vitrine"
	if err := Install(c); err == nil || !strings.Contains(err.Error(), "go install") {
		t.Fatalf("go run binary: %v", err)
	}
	c, _ = newConfig(t, "plan9")
	if err := Install(c); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("plan9: %v", err)
	}
}
