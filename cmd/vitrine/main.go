// Command vitrine hosts HTML prototypes and tells agents what people do in
// them. See README.md and docs/adr/.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/obuchmann/vitrine/internal/auth"
	"github.com/obuchmann/vitrine/internal/statedir"
	"github.com/obuchmann/vitrine/internal/store"
	"github.com/obuchmann/vitrine/internal/web"
)

var version = "dev" // set with -ldflags "-X main.version=…"

// DefaultPort is where agents and browsers find the local vitrine.
const DefaultPort = "8720"

const usage = `vitrine hosts HTML prototypes and tells your agent what people do in them.

Usage:
  vitrine serve [flags]   run the server in the foreground
  vitrine version         print the version

Run "vitrine serve -h" for the flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:], os.Stderr)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vitrine:", err)
		os.Exit(1)
	}
}

type serveConfig struct {
	addrs     []string // listen addresses
	publicURL string
	hosts     []string // accepted Host headers
	stateDir  string
	token     string
	authFile  string // empty if the token came from the environment
}

func serve(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "", "listen address (default 127.0.0.1:"+DefaultPort+" and [::1]:"+DefaultPort+")")
	publicURL := fs.String("public-url", "", "URL people open, required when listening beyond loopback")
	stateDir := fs.String("state-dir", "", "where the token and prototypes live (default: per-user state directory, or $"+statedir.EnvOverride+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := configure(*addr, *publicURL, *stateDir, os.Getenv("VITRINE_TOKEN"))
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	st, err := store.Open(store.Options{Dir: filepath.Join(cfg.stateDir, "prototypes"), Limits: store.DefaultLimits(), Log: log})
	if err != nil {
		return err
	}
	ws := &web.Server{Store: st, PublicURL: cfg.publicURL, Token: cfg.token, AuthFile: cfg.authFile,
		AllowedHosts: cfg.hosts, Version: version, Log: log}
	return run(cfg, ws.Handler(), st, log)
}

// configure applies the defaults and the rule that vitrine only listens
// beyond loopback with an explicit token (docs/adr/0002-authentication.md).
func configure(addr, publicURL, stateDir, envToken string) (serveConfig, error) {
	cfg := serveConfig{token: envToken}
	var err error
	if stateDir == "" {
		if stateDir, err = statedir.Default(); err != nil {
			return cfg, err
		}
	}
	if cfg.stateDir, err = filepath.Abs(stateDir); err != nil {
		return cfg, err
	}

	port := DefaultPort
	loopback := true
	if addr == "" {
		cfg.addrs = []string{"127.0.0.1:" + port, "[::1]:" + port}
	} else {
		host, p, err := net.SplitHostPort(addr)
		if err != nil {
			return cfg, fmt.Errorf("invalid -addr %q: %w", addr, err)
		}
		port, loopback = p, isLoopback(host)
		cfg.addrs = []string{addr}
	}
	cfg.hosts = []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port}
	cfg.publicURL = "http://127.0.0.1:" + port
	if publicURL != "" {
		u, err := url.Parse(strings.TrimSuffix(publicURL, "/"))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return cfg, fmt.Errorf("invalid -public-url %q: want e.g. https://vitrine.example.com", publicURL)
		}
		cfg.publicURL = u.String()
		cfg.hosts = append(cfg.hosts, u.Host)
	}

	if !loopback {
		if cfg.token == "" {
			return cfg, fmt.Errorf("refusing to listen on %s without a token: set VITRINE_TOKEN", addr)
		}
		if publicURL == "" {
			return cfg, fmt.Errorf("listening on %s needs -public-url, the address people open", addr)
		}
	}
	if cfg.token == "" {
		cfg.authFile = filepath.Join(cfg.stateDir, auth.FileName)
		if cfg.token, err = auth.LoadOrCreate(cfg.authFile); err != nil {
			return cfg, fmt.Errorf("token file: %w", err)
		}
	}
	return cfg, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func run(cfg serveConfig, h http.Handler, st *store.Store, log *slog.Logger) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	var lns []net.Listener
	for i, a := range cfg.addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			// The default also tries IPv6 loopback, which some systems lack.
			if i > 0 && len(lns) > 0 {
				log.Debug("not listening", "addr", a, "err", err)
				continue
			}
			for _, l := range lns {
				l.Close()
			}
			if isAddrInUse(err) {
				return fmt.Errorf("%s is in use: is vitrine already running? (%w)", a, err)
			}
			return err
		}
		lns = append(lns, ln)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go sweep(ctx, st, log)

	errc := make(chan error, len(lns))
	for _, ln := range lns {
		go func() { errc <- srv.Serve(ln) }()
		log.Info("vitrine listening", "addr", ln.Addr().String())
	}
	log.Info("vitrine ready", "url", cfg.publicURL, "state", cfg.stateDir, "version", version)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Long polls and streams do not end by themselves; everything that matters
	// is already on disk.
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func sweep(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, name := range st.Sweep() {
				log.Info("expired", "name", name)
			}
		}
	}
}
