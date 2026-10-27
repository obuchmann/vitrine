package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/obuchmann/vitrine/internal/auth"
	"github.com/obuchmann/vitrine/internal/browser"
	"github.com/obuchmann/vitrine/internal/client"
	"github.com/obuchmann/vitrine/internal/mcptools"
	"github.com/obuchmann/vitrine/internal/statedir"
	"github.com/obuchmann/vitrine/internal/store"
)

// DefaultIdleExit is how long a daemon started by vitrine mcp waits without
// requests before it exits. Everything is on disk, so it can be short.
const DefaultIdleExit = 30 * time.Minute

// logFile is where a daemon started by vitrine mcp writes its log.
const logFile = "vitrine.log"

// mcpCommand serves MCP on stdin and stdout and calls the vitrine at -url,
// starting it if it is local and not running (docs/adr/0001-runtime-modes.md).
func mcpCommand(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rawURL := fs.String("url", "http://127.0.0.1:"+DefaultPort, "the vitrine to use; a local one is started when needed")
	noOpen := fs.Bool("no-open", false, "do not open published prototypes in the browser")
	stateDir := fs.String("state-dir", "", "where the token and prototypes live (default: per-user state directory, or $"+statedir.EnvOverride+")")
	idleExit := fs.Duration("idle-exit", DefaultIdleExit, "a started daemon exits after this long without requests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := configureMCP(*rawURL, *stateDir, os.Getenv("VITRINE_TOKEN"))
	if err != nil {
		return err
	}
	c := &client.Client{BaseURL: cfg.baseURL, Token: cfg.token}
	if cfg.local {
		d := &daemon{client: c, stateDir: cfg.stateDir, addr: cfg.addr, idleExit: *idleExit}
		c.Start = d.start
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	opts := mcptools.Options{Client: c, Dir: dir, Version: version}
	if !*noOpen {
		opts.Open = browser.Open
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "vitrine", Version: version}, &mcp.ServerOptions{
		Instructions: mcptools.Instructions(runtime.GOOS, cfg.baseURL, cfg.authFile, store.DefaultLimits().DefaultTTL),
	})
	mcptools.Register(server, opts)
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

type mcpConfig struct {
	baseURL  string
	local    bool   // a loopback vitrine that vitrine mcp may start
	addr     string // -addr for the started daemon; empty for the default
	stateDir string
	token    string
	authFile string // empty if the token came from the environment
}

func configureMCP(rawURL, stateDir, envToken string) (mcpConfig, error) {
	cfg := mcpConfig{token: envToken}
	u, err := url.Parse(strings.TrimSuffix(rawURL, "/"))
	if err != nil || u.Host == "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return cfg, fmt.Errorf("invalid -url %q: want e.g. http://127.0.0.1:%s", rawURL, DefaultPort)
	}
	cfg.baseURL = u.String()
	cfg.local = u.Scheme == "http" && isLoopback(u.Hostname())
	if cfg.local && u.Host != "127.0.0.1:"+DefaultPort {
		port := u.Port()
		if port == "" {
			port = "80"
		}
		cfg.addr = net.JoinHostPort(u.Hostname(), port)
	}
	if !cfg.local {
		if cfg.token == "" {
			return cfg, fmt.Errorf("%s is not on this machine: set VITRINE_TOKEN", cfg.baseURL)
		}
		return cfg, nil
	}
	// A daemon started from here needs its state directory even if the
	// token comes from the environment.
	if stateDir == "" {
		if stateDir, err = statedir.Default(); err != nil {
			return cfg, err
		}
	}
	if cfg.stateDir, err = filepath.Abs(stateDir); err != nil {
		return cfg, err
	}
	if cfg.token != "" {
		return cfg, nil
	}
	// The daemon reads the same file; whoever comes first creates it.
	cfg.authFile = filepath.Join(cfg.stateDir, auth.FileName)
	if cfg.token, err = auth.LoadOrCreate(cfg.authFile); err != nil {
		return cfg, fmt.Errorf("token file: %w", err)
	}
	return cfg, nil
}

// daemon starts vitrine serve in the background when nothing answers. Several
// vitrine mcp processes may race; the one that binds the port wins, and the
// others find it on their next health check.
type daemon struct {
	client   *client.Client
	stateDir string
	addr     string
	idleExit time.Duration

	mu sync.Mutex
}

const startTimeout = 10 * time.Second

func (d *daemon) start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.client.Health(ctx); err == nil {
		return nil // started by another call in the meantime
	} else if errors.Is(err, client.ErrNotVitrine) {
		return fmt.Errorf("%w; use -url to point vitrine mcp elsewhere", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"serve", "-state-dir", d.stateDir, "-idle-exit", d.idleExit.String()}
	if d.addr != "" {
		args = append(args, "-addr", d.addr)
	}
	logPath := filepath.Join(d.stateDir, logFile)
	log, err := openLog(logPath)
	if err != nil {
		return err
	}
	defer log.Close() // the daemon has its own handle
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Dir = d.stateDir // do not keep the agent's directory busy
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting vitrine: %w", err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()

	deadline := time.Now().Add(startTimeout)
	for {
		if _, err := d.client.Health(ctx); err == nil {
			return nil
		}
		select {
		case <-exited:
			// It may have lost the race against another daemon.
			if _, err := d.client.Health(ctx); err == nil {
				return nil
			}
			return fmt.Errorf("vitrine could not start, see %s", logPath)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("vitrine did not answer within %s, see %s", startTimeout, logPath)
		}
	}
}

// openLog appends to the daemon log and starts it over once it is large.
func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		os.Rename(path, path+".1") // fails on Windows while a daemon writes it; then the log keeps growing
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
}
