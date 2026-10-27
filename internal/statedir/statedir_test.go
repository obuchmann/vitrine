package statedir

import (
	"path/filepath"
	"testing"
)

func TestForOS(t *testing.T) {
	home := func() (string, error) { return filepath.Join("/", "home", "u"), nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", nil, filepath.Join("/", "home", "u", ".local", "state", "vitrine")},
		{"linux", map[string]string{"XDG_STATE_HOME": "/xdg"}, filepath.Join("/xdg", "vitrine")},
		{"linux", map[string]string{"XDG_STATE_HOME": "relative"}, filepath.Join("/", "home", "u", ".local", "state", "vitrine")},
		{"freebsd", nil, filepath.Join("/", "home", "u", ".local", "state", "vitrine")},
		{"darwin", nil, filepath.Join("/", "home", "u", "Library", "Application Support", "vitrine")},
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
