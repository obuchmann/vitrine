package mcptools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Family OS")
	sub := filepath.Join(root, "web", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// A worktree has a .git file instead of a directory.
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ dir, file, name, want string }{
		{root, filepath.Join(root, "prototypes", "Shopping List", "index.html"), "", "family-os/shopping-list"},
		{sub, filepath.Join(sub, "login.html"), "", "family-os/login"},
		{sub, filepath.Join(sub, "x.html"), "checkout", "family-os/checkout"},
		{sub, filepath.Join(sub, "x.html"), "other/thing", "other/thing"},
	} {
		got, err := resolveName(tc.dir, tc.file, tc.name)
		if err != nil || got != tc.want {
			t.Errorf("resolveName(%q, %q, %q) = %q, %v; want %q", tc.dir, tc.file, tc.name, got, err, tc.want)
		}
	}

	// The file's repository wins over the working directory's.
	other := filepath.Join(t.TempDir(), "vitrine-test")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveName(sub, filepath.Join(other, "prototypes", "wahl", "index.html"), ""); err != nil || got != "vitrine-test/wahl" {
		t.Errorf("file in another repository: %q, %v", got, err)
	}

	outside := t.TempDir()
	// A file outside any repository gets the working directory's.
	if got, err := resolveName(sub, filepath.Join(outside, "x.html"), ""); err != nil || got != "family-os/x" {
		t.Errorf("file outside a repository: %q, %v", got, err)
	}
	if _, err := resolveName(outside, filepath.Join(outside, "a.html"), ""); err == nil || !strings.Contains(err.Error(), "<repo>/<name>") {
		t.Errorf("outside a repository: %v", err)
	}
	if _, err := resolveName(outside, filepath.Join(outside, "a.html"), "Bad/Name"); err == nil {
		t.Error("invalid full name accepted")
	}
	if _, err := resolveName(root, filepath.Join(root, "con.html"), ""); err == nil || !strings.Contains(err.Error(), "pass name explicitly") {
		t.Errorf("reserved name: %v", err)
	}
}

func TestWaitCommand(t *testing.T) {
	for _, tc := range []struct{ goos, authFile, want string }{
		{"linux", "/home/me/.local/state/vitrine/auth", `curl -s -H "@/home/me/.local/state/vitrine/auth" 'http://127.0.0.1:8720/api/events/next?`},
		{"darwin", "/Users/me/Library/Application Support/vitrine/auth", `curl -s -H "@/Users/me/Library/Application Support/vitrine/auth" '`},
		{"windows", `C:\Users\me\AppData\Local\vitrine\auth`, `curl.exe -s -H "@C:\Users\me\AppData\Local\vitrine\auth" '`},
		{"linux", "", `curl -s -H "Authorization: Bearer $VITRINE_TOKEN" -H "X-Vitrine: 1" '`},
		{"windows", "", `curl.exe -s -H "Authorization: Bearer $env:VITRINE_TOKEN" -H "X-Vitrine: 1" '`},
	} {
		if got := WaitCommand(tc.goos, "http://127.0.0.1:8720", tc.authFile); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s %q:\n got %s\nwant %s…", tc.goos, tc.authFile, got, tc.want)
		}
	}
}
