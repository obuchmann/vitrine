package mcptools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/obuchmann/vitrine/internal/browser"
	"github.com/obuchmann/vitrine/internal/client"
	"github.com/obuchmann/vitrine/internal/store"
	"github.com/obuchmann/vitrine/internal/web"
)

const (
	token = "vitrine_test-token"
	page  = `<!doctype html><html><head><title>x</title></head><body><button>B</button></body></html>`
)

type env struct {
	t      *testing.T
	st     *store.Store
	cs     *mcp.ClientSession
	repo   string   // a git repository and the agent's working directory
	opened []string // URLs opened in the browser
}

// newEnv connects an MCP client to the tools, which call a vitrine over HTTP.
func newEnv(t *testing.T, open func(string) error) *env {
	return newEnvVersion(t, open, "")
}

func newEnvVersion(t *testing.T, open func(string) error, version string) *env {
	t.Helper()
	st, err := store.Open(store.Options{Dir: t.TempDir(), Limits: store.DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	host := srv.Listener.Addr().String()
	ws := &web.Server{Store: st, PublicURL: "http://" + host, Token: token, AllowedHosts: []string{host},
		Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv.Config.Handler = ws.Handler()
	srv.Start()
	t.Cleanup(srv.Close)

	e := &env{t: t, st: st, repo: filepath.Join(t.TempDir(), "My App")}
	if err := os.MkdirAll(filepath.Join(e.repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if open == nil {
		open = func(u string) error { e.opened = append(e.opened, u); return nil }
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "vitrine", Version: "test"},
		&mcp.ServerOptions{Instructions: Instructions("linux", srv.URL, "/state/auth", time.Hour)})
	Register(server, Options{Client: &client.Client{BaseURL: srv.URL, Token: token}, Dir: e.repo, Open: open, Version: version})

	ctx := context.Background()
	st1, st2 := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	e.cs, err = mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, st2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.cs.Close() })
	return e
}

func (e *env) write(rel, content string) {
	e.t.Helper()
	p := filepath.Join(e.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// call runs a tool and returns its structured result, or the error text.
func (e *env) call(name string, args map[string]any) (map[string]any, string) {
	e.t.Helper()
	res, err := e.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return nil, res.Content[0].(*mcp.TextContent).Text
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out, ""
}

func (e *env) mustCall(name string, args map[string]any) map[string]any {
	e.t.Helper()
	out, errText := e.call(name, args)
	if errText != "" {
		e.t.Fatalf("%s: %s", name, errText)
	}
	return out
}

func TestTools(t *testing.T) {
	e := newEnv(t, nil)
	tools, err := e.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"publish_prototype", "extend_prototype", "list_prototypes", "get_prototype",
		"wait_for_events", "list_events", "send_to_prototype", "delete_prototype"} {
		if !slices.Contains(names, want) {
			t.Errorf("tool %s missing in %v", want, names)
		}
	}
	instr := e.cs.InitializeResult().Instructions
	if !strings.Contains(instr, `curl -s -H "@/state/auth"`) || strings.Contains(instr, token) {
		t.Errorf("instructions must name the token file, not the token:\n%s", instr)
	}
}

func TestPublishReadsTheFileAndOpensTheBrowserOnce(t *testing.T) {
	e := newEnv(t, nil)
	e.write("prototypes/login/index.html", page)

	pub := e.mustCall("publish_prototype", map[string]any{"path": "prototypes/login/index.html", "keep_for": "60d",
		"context": map[string]string{"branch": "feat/login"}})
	if pub["name"] != "my-app/login" || pub["version"].(float64) != 1 || pub["keep_for"] != "30d" || pub["warning"] == nil {
		t.Fatalf("publish: %v", pub)
	}
	if pub["browser"] != "opened" || len(e.opened) != 1 || e.opened[0] != pub["url"] {
		t.Fatalf("browser: %v %v", pub["browser"], e.opened)
	}
	if p, _ := e.st.Page("my-app/login"); string(p.HTML) != page {
		t.Fatalf("stored HTML %q", p.HTML)
	}

	again := e.mustCall("publish_prototype", map[string]any{"path": filepath.Join(e.repo, "prototypes", "login", "index.html")})
	if again["version"].(float64) != 2 || again["browser"] != nil || len(e.opened) != 1 {
		t.Fatalf("second publish: %v, opened %v", again, e.opened)
	}
	if info, _ := e.st.Get("my-app/login"); info.Context["branch"] != "feat/login" {
		t.Fatalf("context lost: %v", info.Context)
	}

	if _, errText := e.call("publish_prototype", map[string]any{"path": "missing.html"}); errText == "" {
		t.Fatal("publishing a missing file succeeded")
	}
	if _, errText := e.call("publish_prototype", map[string]any{}); !strings.Contains(errText, "path") {
		t.Fatalf("no path: %q", errText)
	}
}

func TestPublishWithoutDesktopTellsTheAgent(t *testing.T) {
	e := newEnv(t, func(string) error { return browser.ErrNoDesktop })
	e.write("a.html", page)
	pub := e.mustCall("publish_prototype", map[string]any{"path": "a.html"})
	if !strings.Contains(pub["browser"].(string), "Give the user the URL") {
		t.Fatalf("publish: %v", pub)
	}
}

func TestPublishWarnsAboutAnotherServerVersion(t *testing.T) {
	e := newEnvVersion(t, nil, "0.2.0") // the server is "test"
	e.write("a.html", page)
	pub := e.mustCall("publish_prototype", map[string]any{"path": "a.html"})
	if w, _ := pub["warning"].(string); !strings.Contains(w, "vitrine test is running, but vitrine mcp is 0.2.0") {
		t.Fatalf("publish: %v", pub)
	}
	if again := e.mustCall("publish_prototype", map[string]any{"path": "a.html"}); again["warning"] != nil {
		t.Fatalf("warned twice: %v", again)
	}
}

func TestInboxOverMCP(t *testing.T) {
	e := newEnv(t, nil)
	e.write("p.html", page)
	e.mustCall("publish_prototype", map[string]any{"path": "p.html", "name": "demo/p"})

	go func() {
		time.Sleep(100 * time.Millisecond)
		e.st.Emit("demo/p", "choice", json.RawMessage(`{"variant":"A"}`), 1, false)
	}()
	got := e.mustCall("wait_for_events", map[string]any{"name": "demo/p", "types": []string{"choice"}, "timeout_s": 5})
	evs := got["events"].([]any)
	if len(evs) != 1 || got["cursor"].(float64) != 1 || evs[0].(map[string]any)["data"].(map[string]any)["variant"] != "A" {
		t.Fatalf("wait: %v", got)
	}

	// Not acknowledged yet: a new session gets it again.
	list := e.mustCall("list_prototypes", nil)
	first := list["prototypes"].([]any)[0].(map[string]any)
	if first["unread"].(float64) != 1 || !strings.HasSuffix(first["url"].(string), "/p/demo/p/") {
		t.Fatalf("list before ack: %v", first)
	}
	again := e.mustCall("wait_for_events", map[string]any{"name": "demo/p", "timeout_s": 1})
	if len(again["events"].([]any)) != 1 {
		t.Fatalf("unacknowledged event not delivered again: %v", again)
	}
	done := e.mustCall("wait_for_events", map[string]any{"name": "demo/p", "ack": 1, "timeout_s": 1})
	if done["timed_out"] != true {
		t.Fatalf("after ack: %v", done)
	}
	if info, _ := e.st.Get("demo/p"); info.Unread != 0 {
		t.Fatalf("unread after ack: %d", info.Unread)
	}
	if evs := e.mustCall("list_events", map[string]any{"name": "demo/p"}); len(evs["events"].([]any)) != 1 {
		t.Fatalf("list_events: %v", evs)
	}

	if sent := e.mustCall("send_to_prototype", map[string]any{"name": "demo/p", "message": "Thanks", "data": map[string]int{"v": 2}}); sent["delivered_to"].(float64) != 0 {
		t.Fatalf("send: %v", sent)
	}
	if ext := e.mustCall("extend_prototype", map[string]any{"name": "demo/p", "keep_for": "5d"}); ext["keep_for"] != "5d" {
		t.Fatalf("extend: %v", ext)
	}
	e.mustCall("delete_prototype", map[string]any{"name": "demo/p"})
	if _, errText := e.call("get_prototype", map[string]any{"name": "demo/p"}); !strings.Contains(errText, "unknown_prototype") || !strings.Contains(errText, "Publish it again") {
		t.Fatalf("get after delete: %q", errText)
	}
}
