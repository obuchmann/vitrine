package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/obuchmann/vitrine/internal/store"
)

const (
	token = "vitrine_test-token"
	page  = `<!doctype html><html data-track="view"><head><title>x</title></head><body><button id="b">B</button></body></html>`
)

type env struct {
	t    *testing.T
	url  string
	host string
	st   *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(store.Options{Dir: t.TempDir(), Limits: store.DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	host := srv.Listener.Addr().String()
	ws := &Server{Store: st, PublicURL: "http://" + host, Token: token, AuthFile: "/state/auth",
		AllowedHosts: []string{host}, Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv.Config.Handler = ws.Handler()
	srv.Start()
	t.Cleanup(srv.Close)
	return &env{t: t, url: srv.URL, host: host, st: st}
}

var agentHeaders = map[string]string{"Authorization": "Bearer " + token, "X-Vitrine": "1"}

// do sends a request and returns status and body. Missing headers are not added.
func (e *env) do(method, path, contentType string, body io.Reader, headers map[string]string) (int, string, http.Header) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.url+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

func (e *env) agent(method, path string) (int, string) {
	e.t.Helper()
	code, body, _ := e.do(method, path, "", nil, agentHeaders)
	return code, body
}

func (e *env) upload(name string, fields map[string]string) map[string]any {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("html", "index.html")
	fw.Write([]byte(page))
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()
	code, resp, _ := e.do("POST", "/api/p/"+name, mw.FormDataContentType(), &body, agentHeaders)
	var out map[string]any
	json.Unmarshal([]byte(resp), &out)
	if code != 200 {
		e.t.Fatalf("upload %s: %d %s", name, code, resp)
	}
	return out
}

var tokenRE = regexp.MustCompile(`data-token="([0-9a-f]+)"`)

// pageToken loads a prototype like a browser and returns its event token.
func (e *env) pageToken(name string) string {
	e.t.Helper()
	code, body, _ := e.do("GET", "/p/"+name+"/", "", nil, nil)
	m := tokenRE.FindStringSubmatch(body)
	if code != 200 || m == nil {
		e.t.Fatalf("page %s: %d %s", name, code, body)
	}
	return m[1]
}

func (e *env) emitAsPage(name, tok, body string) int {
	e.t.Helper()
	code, _, _ := e.do("POST", "/api/events/"+name, "application/json", strings.NewReader(body),
		map[string]string{"X-Vitrine": "1", EventTokenHeader: tok})
	return code
}

func TestUploadServeAndInbox(t *testing.T) {
	e := newEnv(t)
	out := e.upload("my-app/login", map[string]string{"keep_for": "10d", "context": `{"branch":"feat/login"}`})
	if out["url"] != e.url+"/p/my-app/login/" || out["version"].(float64) != 1 || out["keep_for"] != "10d" {
		t.Fatalf("upload result %v", out)
	}

	// Raw body upload replaces it: version 2.
	if code, body, _ := e.do("POST", "/api/p/my-app/login", "text/html", strings.NewReader(page), agentHeaders); code != 200 {
		t.Fatalf("raw upload: %d %s", code, body)
	}
	code, body, _ := e.do("GET", "/p/my-app/login/", "", nil, nil)
	if code != 200 || !strings.Contains(body, `<head><script src="/_hub/hub.js" data-name="my-app/login" data-version="2" data-token="`) {
		t.Fatalf("served page %d: %s", code, body)
	}
	if code, _, _ := e.do("GET", "/_hub/hub.js", "", nil, nil); code != 200 {
		t.Fatalf("hub.js: %d", code)
	}

	tok := e.pageToken("my-app/login")
	if c := e.emitAsPage("my-app/login", tok, `{"type":"view","auto":true}`); c != 200 {
		t.Fatalf("auto event: %d", c)
	}
	if c := e.emitAsPage("my-app/login", tok, `{"type":"choice","data":{"variant":"B"},"version":2}`); c != 200 {
		t.Fatalf("choice: %d", c)
	}

	code, body = e.agent("GET", "/api/events/next?name=my-app/login&types=choice&timeout=1")
	var fr store.FetchResult
	if err := json.Unmarshal([]byte(body), &fr); err != nil || code != 200 {
		t.Fatalf("next: %d %s", code, body)
	}
	var compact bytes.Buffer
	if len(fr.Events) == 1 {
		json.Compact(&compact, fr.Events[0].Data)
	}
	if len(fr.Events) != 1 || fr.Events[0].Type != "choice" || compact.String() != `{"variant":"B"}` {
		t.Fatalf("next events %+v", fr.Events)
	}
	// Not acknowledged: a new session gets it again.
	_, body = e.agent("GET", "/api/events/next?name=my-app/login&types=choice&timeout=1")
	json.Unmarshal([]byte(body), &fr)
	if len(fr.Events) != 1 {
		t.Fatalf("redelivery: %s", body)
	}
	// Acknowledged: nothing left, the call times out empty.
	_, body = e.agent("GET", "/api/events/next?name=my-app/login&types=choice&timeout=1&ack=2")
	json.Unmarshal([]byte(body), &fr)
	if len(fr.Events) != 0 || !fr.TimedOut {
		t.Fatalf("after ack: %s", body)
	}

	code, body, _ = e.do("GET", "/", "", nil, nil)
	if code != 200 || !strings.Contains(body, "my-app/login") {
		t.Fatalf("overview %d", code)
	}
}

func TestNextWakesUpOnEvent(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	tok := e.pageToken("a/b")
	done := make(chan string)
	go func() {
		_, body := e.agent("GET", "/api/events/next?name=a/b&types=choice&timeout=10")
		done <- body
	}()
	time.Sleep(50 * time.Millisecond)
	e.emitAsPage("a/b", tok, `{"type":"choice","data":"A"}`)
	select {
	case body := <-done:
		if !strings.Contains(body, `"choice"`) {
			t.Fatalf("got %s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("next did not return")
	}
}

func TestUnknownAndInvalid(t *testing.T) {
	e := newEnv(t)
	if code, body := e.agent("GET", "/api/events/next?name=a/missing"); code != 404 || !strings.Contains(body, "unknown_prototype") {
		t.Fatalf("next unknown: %d %s", code, body)
	}
	if code, _, _ := e.do("GET", "/p/a/missing/", "", nil, nil); code != 404 {
		t.Fatalf("page unknown: %d", code)
	}
	if code, _, _ := e.do("POST", "/api/p/My-App/x", "text/html", strings.NewReader(page), agentHeaders); code != 400 {
		t.Fatalf("invalid name: %d", code)
	}
	e.upload("a/b", nil)
	if c := e.emitAsPage("a/b", e.pageToken("a/b"), `{"type":"Bad Type"}`); c != 400 {
		t.Fatalf("invalid type: %d", c)
	}
}

func TestStreamForHubGetsMessagesAndVersions(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	tok := e.pageToken("a/b")
	if code, _, _ := e.do("GET", "/api/events/stream?name=a/b&hub=1&token=wrong", "", nil, nil); code != 401 {
		t.Fatalf("hub stream with a wrong token: %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.url+"/api/events/stream?name=a/b&hub=1&token="+tok, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := bufio.NewScanner(res.Body)
	lines.Scan() // ": vitrine"
	waitViewers(t, e.st, "a/b", 1)
	code, body, _ := e.do("POST", "/api/p/a/b/message", "application/json", strings.NewReader(`{"message":"Thanks!"}`), agentHeaders)
	if code != 200 || !strings.Contains(body, `"delivered_to": 1`) {
		t.Fatalf("message: %d %s", code, body)
	}
	e.upload("a/b", nil)
	var got []string
	for lines.Scan() && len(got) < 3 {
		if l := lines.Text(); strings.HasPrefix(l, "event: ") {
			got = append(got, strings.TrimPrefix(l, "event: "))
		}
	}
	if strings.Join(got, ",") != "version,message,version" {
		t.Fatalf("stream kinds %v", got)
	}
}

func TestMessageNeedsAgentAndText(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	msg := `{"message":"hi"}`
	if code, _, _ := e.do("POST", "/api/p/a/b/message", "application/json", strings.NewReader(msg),
		map[string]string{"X-Vitrine": "1", EventTokenHeader: e.pageToken("a/b")}); code != 401 {
		t.Fatalf("page token sends a message: %d", code)
	}
	if code, _, _ := e.do("POST", "/api/p/a/b/message", "application/json", strings.NewReader(`{}`), agentHeaders); code != 400 {
		t.Fatalf("empty message: %d", code)
	}
	if code, body, _ := e.do("POST", "/api/p/a/b/message", "application/json", strings.NewReader(msg), agentHeaders); code != 200 || !strings.Contains(body, `"delivered_to": 0`) {
		t.Fatalf("message without tabs: %d %s", code, body)
	}
	if code, _, _ := e.do("POST", "/api/p/x/y/message", "application/json", strings.NewReader(msg), agentHeaders); code != 404 {
		t.Fatalf("unknown prototype: %d", code)
	}
}

func waitViewers(t *testing.T, st *store.Store, name string, n int) {
	t.Helper()
	for range 100 {
		if info, _ := st.Get(name); info.Viewers == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %d viewers", n)
}

// The attacks from docs/adr/0002-authentication.md.

func TestWebsiteCannotUploadWithAForm(t *testing.T) {
	e := newEnv(t)
	// A hidden form on another site: a simple request, no custom headers.
	code, _, _ := e.do("POST", "/api/p/a/evil", "multipart/form-data; boundary=x",
		strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"html\"\r\n\r\n<p>evil</p>\r\n--x--\r\n"),
		map[string]string{"Origin": "https://evil.example"})
	if code != 401 {
		t.Fatalf("form upload: %d, want 401", code)
	}
	if _, err := e.st.Get("a/evil"); err == nil {
		t.Fatal("prototype was created")
	}
}

func TestWebsiteCannotInjectEvents(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	// No event token, a wrong one, or a form-encoded body.
	for _, h := range []map[string]string{
		{"X-Vitrine": "1"},
		{"X-Vitrine": "1", EventTokenHeader: "0000"},
		{EventTokenHeader: e.pageToken("a/b")}, // token but no X-Vitrine marker
	} {
		code, _, _ := e.do("POST", "/api/events/a/b", "application/json", strings.NewReader(`{"type":"choice"}`), h)
		if code != 401 {
			t.Errorf("headers %v: %d, want 401", h, code)
		}
	}
	code, _, _ := e.do("POST", "/api/events/a/b", "text/plain", strings.NewReader(`{"type":"choice"}`),
		map[string]string{"X-Vitrine": "1", EventTokenHeader: e.pageToken("a/b")})
	if code != 415 {
		t.Errorf("text/plain event: %d, want 415", code)
	}
	if info, _ := e.st.Get("a/b"); info.Events != 0 {
		t.Fatalf("%d events injected", info.Events)
	}
}

func TestWebsiteCannotAcknowledge(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	e.emitAsPage("a/b", e.pageToken("a/b"), `{"type":"choice"}`)
	// <img src="http://127.0.0.1:8720/api/events/next?name=a/b&ack=999">
	if code, _, _ := e.do("GET", "/api/events/next?name=a/b&ack=999&timeout=0", "", nil, nil); code != 401 {
		t.Fatalf("ack without token: %d", code)
	}
	if info, _ := e.st.Get("a/b"); info.Unread != 1 {
		t.Fatalf("unread %d, want 1: the event was acknowledged", info.Unread)
	}
}

func TestDNSRebindingIsRejected(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	// evil.example resolves to 127.0.0.1, but the browser sends its own name.
	h := map[string]string{"Host": "evil.example:" + strings.Split(e.host, ":")[1]}
	for _, path := range []string{"/", "/p/a/b/", "/api/health"} {
		if code, _, _ := e.do("GET", path, "", nil, h); code != 403 {
			t.Errorf("%s with a foreign Host: %d, want 403", path, code)
		}
	}
	// Even with the token: the Host check comes first.
	h["Authorization"], h["X-Vitrine"] = "Bearer "+token, "1"
	if code, _, _ := e.do("GET", "/api/p", "", nil, h); code != 403 {
		t.Errorf("agent request with a foreign Host: %d, want 403", code)
	}
}

func TestPrototypeCannotUseAnotherPrototypesToken(t *testing.T) {
	e := newEnv(t)
	e.upload("a/one", nil)
	e.upload("a/two", nil)
	if c := e.emitAsPage("a/two", e.pageToken("a/one"), `{"type":"choice"}`); c != 401 {
		t.Fatalf("token of a/one accepted for a/two: %d", c)
	}
}

func TestAgentNeedsTokenAndMarker(t *testing.T) {
	e := newEnv(t)
	for _, h := range []map[string]string{
		nil,
		{"Authorization": "Bearer " + token}, // no X-Vitrine
		{"X-Vitrine": "1"},                   // no token
		{"Authorization": "Bearer x", "X-Vitrine": "1"}, // wrong token
	} {
		code, body, _ := e.do("GET", "/api/p", "", nil, h)
		if code != 401 || !strings.Contains(body, "/state/auth") {
			t.Errorf("headers %v: %d %s", h, code, body)
		}
	}
	if code, _ := e.agent("GET", "/api/p"); code != 200 {
		t.Fatalf("agent: %d", code)
	}
}

func TestNoCORS(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	preflight := map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "POST",
		"Access-Control-Request-Headers": "x-vitrine, content-type"}
	for _, path := range []string{"/api/events/a/b", "/api/p/a/b", "/api/events/next"} {
		_, _, hdr := e.do("OPTIONS", path, "", nil, preflight)
		for k := range hdr {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s answered a preflight with %s", path, k)
			}
		}
	}
	_, _, hdr := e.do("GET", "/api/health", "", nil, map[string]string{"Origin": "https://evil.example"})
	if hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS header on /api/health")
	}
}

func TestHealthIsOpen(t *testing.T) {
	e := newEnv(t)
	code, body, _ := e.do("GET", "/api/health", "", nil, nil)
	if code != 200 || !strings.Contains(body, `"service": "vitrine"`) {
		t.Fatalf("health: %d %s", code, body)
	}
}

func TestHubIsInjectedAfterDoctypeWithoutHead(t *testing.T) {
	e := newEnv(t)
	for html, prefix := range map[string]string{
		"<!DOCTYPE html><title>x</title><p>hi":                                     "<!DOCTYPE html><script src=",
		"<!-- c -->\n<!doctype html><p>hi":                                         "<!-- c -->\n<!doctype html><script src=",
		"<p>hi</p>":                                                                "<script src=",
		"<!--\n generated\n-->\n<!doctype html><p>hi":                              "<!--\n generated\n-->\n<!doctype html><script src=",
		"<!-- <head> -->\n<!DOCTYPE html>\n<html><HEAD lang=\"a>b\"><title>":       "<!-- <head> -->\n<!DOCTYPE html>\n<html><HEAD lang=\"a>b\"><script src=",
		"<!doctype html><html data-track=\"view\"><body><script>'<head>'</script>": "<!doctype html><html data-track=\"view\"><script src=",
		"\xef\xbb\xbf<!doctype html><header>x</header>":                            "\xef\xbb\xbf<!doctype html><script src=",
	} {
		if code, body, _ := e.do("POST", "/api/p/a/b", "text/html", strings.NewReader(html), agentHeaders); code != 200 {
			t.Fatalf("upload: %d %s", code, body)
		}
		if _, body, _ := e.do("GET", "/p/a/b/", "", nil, nil); !strings.HasPrefix(body, prefix) {
			t.Errorf("%q served as %q", html, body)
		}
	}
}
