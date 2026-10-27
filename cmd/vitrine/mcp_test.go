package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConfigureMCP(t *testing.T) {
	dir := t.TempDir()
	cfg, err := configureMCP("http://127.0.0.1:8720/", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.baseURL != "http://127.0.0.1:8720" || !cfg.local || cfg.addr != "" || cfg.authFile != filepath.Join(dir, "auth") || cfg.token == "" {
		t.Fatalf("default: %+v", cfg)
	}
	// vitrine serve finds the same token.
	served, err := configure("", "", dir, "")
	if err != nil || served.token != cfg.token {
		t.Fatalf("serve token %q, mcp token %q, %v", served.token, cfg.token, err)
	}

	cfg, err = configureMCP("http://localhost:9000", dir, "")
	if err != nil || !cfg.local || cfg.addr != "localhost:9000" {
		t.Fatalf("other port: %+v %v", cfg, err)
	}

	if _, err := configureMCP("https://vitrine.example", dir, ""); err == nil || !strings.Contains(err.Error(), "VITRINE_TOKEN") {
		t.Fatalf("remote without token: %v", err)
	}
	cfg, err = configureMCP("https://vitrine.example", dir, "secret")
	if err != nil || cfg.local || cfg.token != "secret" || cfg.authFile != "" {
		t.Fatalf("remote: %+v %v", cfg, err)
	}

	for _, bad := range []string{"127.0.0.1:8720", "ftp://x", "http://127.0.0.1:8720/sub"} {
		if _, err := configureMCP(bad, dir, ""); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestIdleTracker(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(0, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	release := make(chan struct{})
	started := make(chan struct{})
	idle := newIdleTracker(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			close(started)
			<-release
		}
	}), clock)

	advance(time.Minute)
	if got := idle.idleFor(); got != time.Minute {
		t.Fatalf("idle since start: %v", got)
	}
	go idle.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/wait", nil))
	<-started
	advance(time.Hour)
	if got := idle.idleFor(); got != 0 {
		t.Fatalf("idle during a request: %v", got)
	}
	close(release)
	for func() bool { idle.mu.Lock(); defer idle.mu.Unlock(); return idle.inflight > 0 }() {
		time.Sleep(time.Millisecond)
	}
	advance(time.Second)
	if got := idle.idleFor(); got != time.Second {
		t.Fatalf("idle after the request: %v", got)
	}
}

// TestMCPStartsDaemon runs the real binary the way an agent does: vitrine mcp
// starts a daemon, the daemon outlives the session and exits when idle, and
// the next session starts it again with everything still there.
func TestMCPStartsDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH")
	}
	bin := filepath.Join(t.TempDir(), "vitrine")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command(goBin, "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	state := t.TempDir()
	repo := filepath.Join(t.TempDir(), "e2e-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "demo.html"), []byte(`<!doctype html><html><head></head><body>demo</body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	base := "http://" + addr

	listening := func() bool {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
		}
		return err == nil
	}
	// Dialing does not count as a request, so it does not keep the daemon awake.
	waitStopped := func() {
		t.Helper()
		for range 300 {
			if !listening() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		log, _ := os.ReadFile(filepath.Join(state, logFile))
		t.Fatalf("daemon did not exit when idle; log:\n%s", log)
	}
	t.Cleanup(waitStopped) // the state directory cannot be removed while it runs on Windows

	session := func() *mcp.ClientSession {
		t.Helper()
		cmd := exec.Command(bin, "mcp", "-url", base, "-no-open", "-state-dir", state, "-idle-exit", "2s")
		cmd.Dir = repo
		cmd.Stderr = os.Stderr
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
			Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	call := func(cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			log, _ := os.ReadFile(filepath.Join(state, logFile))
			t.Fatalf("%s: %s\nlog:\n%s", name, res.Content[0].(*mcp.TextContent).Text, log)
		}
		b, _ := json.Marshal(res.StructuredContent)
		var out map[string]any
		json.Unmarshal(b, &out)
		return out
	}

	if listening() {
		t.Fatal("something already listens on", addr)
	}
	cs := session()
	if !strings.Contains(cs.InitializeResult().Instructions, filepath.Join(state, "auth")) {
		t.Error("instructions do not name the token file")
	}
	pub := call(cs, "publish_prototype", map[string]any{"path": "demo.html"})
	if pub["name"] != "e2e-repo/demo" || pub["url"] != base+"/p/e2e-repo/demo/" {
		t.Fatalf("publish: %v", pub)
	}
	res, err := http.Get(base + "/p/e2e-repo/demo/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "/_hub/hub.js") {
		t.Fatalf("page: %d %s", res.StatusCode, body)
	}
	if res, err := http.Get(base + "/api/p"); err != nil || res.StatusCode != 401 {
		t.Fatalf("API without token: %v %v", res, err)
	}
	cs.Close()

	if !listening() {
		t.Fatal("daemon ended with the session")
	}
	waitStopped()

	cs = session()
	defer cs.Close()
	list := call(cs, "list_prototypes", nil)
	if protos := list["prototypes"].([]any); len(protos) != 1 || protos[0].(map[string]any)["name"] != "e2e-repo/demo" {
		t.Fatalf("after restart: %v", list)
	}
}
