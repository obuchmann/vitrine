package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/obuchmann/vitrine/internal/client"
	"github.com/obuchmann/vitrine/internal/service"
	"github.com/obuchmann/vitrine/internal/statedir"
)

const serviceUsage = `Usage:
  vitrine service install     run vitrine in the background at every login
  vitrine service uninstall   stop it and remove it
  vitrine service status      show whether it is installed and running
`

func serviceCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, serviceUsage)
		return errors.New("missing subcommand")
	}
	sub := args[0]
	fs := flag.NewFlagSet("service "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "where the token and prototypes live (default: per-user state directory, or $"+statedir.EnvOverride+")")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := serviceConfig(*stateDir)
	if err != nil {
		return err
	}
	c := &client.Client{BaseURL: "http://127.0.0.1:" + DefaultPort}

	switch sub {
	case "install":
		if err := service.Install(cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Installed for %s and started.\n", cfg.Manager())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for {
			if v, err := c.Health(ctx); err == nil {
				fmt.Fprintf(stdout, "vitrine %s answers at %s and starts at every login.\n", v, c.BaseURL)
				return nil
			}
			select {
			case <-ctx.Done():
				// A daemon started by vitrine mcp may hold the port; the
				// service manager retries until it is free.
				fmt.Fprintf(stdout, "vitrine does not answer at %s yet. Run \"vitrine doctor\" to see why.\n", c.BaseURL)
				return nil
			case <-time.After(200 * time.Millisecond):
			}
		}
	case "uninstall":
		if err := service.Uninstall(cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed from %s. vitrine mcp still starts vitrine when needed.\n", cfg.Manager())
		return nil
	case "status":
		st, err := service.Query(cfg)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, serviceLine(st))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if v, err := c.Health(ctx); err == nil {
			fmt.Fprintf(stdout, "vitrine %s answers at %s\n", v, c.BaseURL)
		} else {
			fmt.Fprintf(stdout, "nothing answers at %s\n", c.BaseURL)
		}
		return nil
	}
	fmt.Fprint(stderr, serviceUsage)
	return fmt.Errorf("unknown subcommand %q", sub)
}

// serviceLine describes a service status in one line.
func serviceLine(st service.Status) string {
	if !st.Installed {
		return st.Manager + ": not installed"
	}
	s := st.Manager + ": installed (" + st.File + ")"
	if st.Active != "" {
		s += ", " + st.Active
	}
	return s
}

// serviceConfig describes the service for this user and binary. The state
// directory is written into the service, so a $VITRINE_STATE_DIR set now
// also applies there.
func serviceConfig(stateDir string) (service.Config, error) {
	cfg := service.Config{GOOS: runtime.GOOS, WSL: wslInfo(os.Getenv, os.ReadFile) != "", UID: strconv.Itoa(os.Getuid())}
	var err error
	if cfg.Exe, err = os.Executable(); err != nil {
		return cfg, err
	}
	if stateDir == "" {
		if stateDir, err = statedir.Default(); err != nil {
			return cfg, err
		}
	}
	if cfg.StateDir, err = filepath.Abs(stateDir); err != nil {
		return cfg, err
	}
	if cfg.ConfigDir, err = os.UserConfigDir(); err != nil && runtime.GOOS == "linux" {
		return cfg, err
	}
	if cfg.Home, err = os.UserHomeDir(); err != nil {
		return cfg, err
	}
	if u, err := user.Current(); err == nil {
		cfg.User = u.Username // DOMAIN\user on Windows
	} else if runtime.GOOS == "windows" {
		return cfg, err
	}
	cfg.Run = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	return cfg, nil
}
