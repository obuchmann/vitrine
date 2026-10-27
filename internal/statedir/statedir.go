// Package statedir finds the per-user directory where vitrine keeps its token
// and prototypes (docs/adr/0004-platforms-and-distribution.md).
package statedir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// EnvOverride replaces the platform default, e.g. for tests or a second instance.
const EnvOverride = "VITRINE_STATE_DIR"

// Default returns the state directory for the current user and platform:
//
//	Linux, WSL  $XDG_STATE_HOME/vitrine, else ~/.local/state/vitrine
//	macOS       ~/Library/Application Support/vitrine
//	Windows     %LOCALAPPDATA%\vitrine
func Default() (string, error) {
	if d := os.Getenv(EnvOverride); d != "" {
		return filepath.Abs(d)
	}
	return forOS(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func forOS(goos string, getenv func(string) string, home func() (string, error)) (string, error) {
	switch goos {
	case "windows":
		if d := getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "vitrine"), nil
		}
		return "", errors.New("%LOCALAPPDATA% is not set; set " + EnvOverride)
	case "darwin":
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, "Library", "Application Support", "vitrine"), nil
	default:
		// The XDG spec says to ignore relative paths.
		if d := getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
			return filepath.Join(d, "vitrine"), nil
		}
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".local", "state", "vitrine"), nil
	}
}
