package statedir

import (
	"path/filepath"
	"testing"
)

func TestForOS(t *testing.T) {
	// Paths are built with the host's rules, so use a directory that is
	// absolute on every platform the tests run on.
	h := t.TempDir()
	xdg := filepath.Join(h, "xdg")
	home := func() (string, error) { return h, nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", nil, filepath.Join(h, ".local", "state", "vitrine")},
		{"linux", map[string]string{"XDG_STATE_HOME": xdg}, filepath.Join(xdg, "vitrine")},
		{"linux", map[string]string{"XDG_STATE_HOME": "relative"}, filepath.Join(h, ".local", "state", "vitrine")},
		{"freebsd", nil, filepath.Join(h, ".local", "state", "vitrine")},
		{"darwin", nil, filepath.Join(h, "Library", "Application Support", "vitrine")},
		{"windows", map[string]string{"LOCALAPPDATA": "lad"}, filepath.Join("lad", "vitrine")},
	} {
		got, err := forOS(tc.goos, env(tc.env), home)
		if err != nil || got != tc.want {
			t.Errorf("%s %v: got %q %v, want %q", tc.goos, tc.env, got, err, tc.want)
		}
	}
	if _, err := forOS("windows", env(nil), home); err == nil {
		t.Error("windows without LOCALAPPDATA: no error")
	}
}

func TestOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvOverride, dir)
	if got, err := Default(); err != nil || got != dir {
		t.Fatalf("got %q %v", got, err)
	}
}
