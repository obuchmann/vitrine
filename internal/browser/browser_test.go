package browser

import (
	"errors"
	"slices"
	"testing"
)

func TestCommand(t *testing.T) {
	const u = "http://127.0.0.1:8720/p/a/b/"
	found := func(string) (string, error) { return "/usr/bin/x", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	for _, tc := range []struct {
		name   string
		goos   string
		env    map[string]string
		look   func(string) (string, error)
		want   []string
		errIs  error
		anyErr bool
	}{
		{name: "windows", goos: "windows", look: missing, want: []string{"rundll32", "url.dll,FileProtocolHandler", u}},
		{name: "macos", goos: "darwin", look: missing, want: []string{"open", u}},
		{name: "wsl", goos: "linux", env: map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, look: found, want: []string{"wslview", u}},
		{name: "wsl without wslu", goos: "linux", env: map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, look: missing, anyErr: true},
		{name: "x11", goos: "linux", env: map[string]string{"DISPLAY": ":0"}, look: found, want: []string{"xdg-open", u}},
		{name: "wayland", goos: "freebsd", env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, look: found, want: []string{"xdg-open", u}},
		{name: "no xdg-open", goos: "linux", env: map[string]string{"DISPLAY": ":0"}, look: missing, anyErr: true},
		{name: "ssh", goos: "linux", look: found, errIs: ErrNoDesktop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := command(tc.goos, func(k string) string { return tc.env[k] }, tc.look, u)
			switch {
			case tc.errIs != nil:
				if !errors.Is(err, tc.errIs) {
					t.Fatalf("err = %v, want %v", err, tc.errIs)
				}
			case tc.anyErr:
				if err == nil {
					t.Fatalf("got %v, want an error", got)
				}
			case err != nil || !slices.Equal(got, tc.want):
				t.Fatalf("got %v %v, want %v", got, err, tc.want)
			}
		})
	}
}
