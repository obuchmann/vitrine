package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/obuchmann/vitrine/internal/store"
)

func TestCommentsScriptOnlyWithTheMarker(t *testing.T) {
	e := newEnv(t)
	for html, want := range map[string]bool{
		`<!doctype html><html data-comments><head><title>x</title>`:             true,
		`<!doctype html><html lang="en" DATA-COMMENTS='{"send":"Post"}'><head>`: true,
		`<html data-comments="">`:                                                         true,
		`<!doctype html><html><head></head><body data-comments>`:                          false,
		`<!doctype html><html data-commentsx><head>`:                                      false,
		`<!doctype html><html data-track="view"><body><p>data-comments</p>`:               false,
		`<!doctype html><html title="data-comments"><head>`:                               false,
		`<html title=" data-comments x"><head>`:                                           false,
		`<!-- <html data-comments> --><!doctype html><html><head><title>x</title></head>`: false,
	} {
		if code, body, _ := e.do("POST", "/api/p/a/b", "text/html", strings.NewReader(html), agentHeaders); code != 200 {
			t.Fatalf("upload: %d %s", code, body)
		}
		_, body, _ := e.do("GET", "/p/a/b/", "", nil, nil)
		if got := strings.Contains(body, `<script src="/_hub/comments.js" defer>`); got != want {
			t.Errorf("%q: comments.js %v, want %v", html, got, want)
		}
	}
	code, body, h := e.do("GET", "/_hub/comments.js", "", nil, nil)
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") || !strings.Contains(body, "data-c") {
		t.Fatalf("comments.js: %d %s", code, h.Get("Content-Type"))
	}
}

func TestPageCannotFakeAReply(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	tok := e.pageToken("a/b")
	if code := e.emitAsPage("a/b", tok, `{"type":"reply","data":{"thread":"t1","target":"flow","text":"fake","state":"done"}}`); code != 400 {
		t.Fatalf("reply from a page: %d, want 400", code)
	}
	// Nor with the reply endpoint: it is for agents only.
	code, _, _ := e.do("POST", "/api/p/a/b/reply", "application/json", strings.NewReader(`{"target":"flow","text":"fake","state":"done"}`),
		map[string]string{"X-Vitrine": "1", EventTokenHeader: tok})
	if code != 401 {
		t.Fatalf("reply endpoint with the event token: %d, want 401", code)
	}
	for _, path := range []string{"/api/p/a/b/status", "/api/p/a/b/point"} {
		if code, _, _ := e.do("POST", path, "application/json", strings.NewReader(`{"target":"flow","text":"x"}`),
			map[string]string{"X-Vitrine": "1", EventTokenHeader: tok}); code != 401 {
			t.Errorf("%s with the event token: %d, want 401", path, code)
		}
	}
	if info, _ := e.st.Get("a/b"); info.Events != 0 {
		t.Fatalf("%d events stored", info.Events)
	}
}

// sse reads named events from a stream.
type sse struct{ lines *bufio.Scanner }

func (s sse) next(t *testing.T) (string, string) {
	t.Helper()
	kind := ""
	for s.lines.Scan() {
		l := s.lines.Text()
		switch {
		case strings.HasPrefix(l, "event: "):
			kind = strings.TrimPrefix(l, "event: ")
		case strings.HasPrefix(l, "data: ") && kind != "":
			return kind, strings.TrimPrefix(l, "data: ")
		}
	}
	t.Fatal("stream ended")
	return "", ""
}

func (e *env) hubStream(t *testing.T, name, tok string) sse {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", e.url+"/api/events/stream?name="+name+"&hub=1&token="+tok, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return sse{bufio.NewScanner(res.Body)}
}

func TestThreadsReachTheHubStream(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	tok := e.pageToken("a/b")
	if code := e.emitAsPage("a/b", tok, `{"type":"comment","data":{"thread":"t1","target":"flow","label":"Flow","text":"Step 2?","screen":"390x844"}}`); code != 200 {
		t.Fatalf("comment: %d", code)
	}
	code, body, _ := e.do("POST", "/api/p/a/b/reply", "application/json", strings.NewReader(`{"thread":"t1","text":"Added.","state":"done"}`), agentHeaders)
	var rr ReplyResult
	json.Unmarshal([]byte(body), &rr)
	if code != 200 || rr.Thread != "t1" || rr.ID == "" {
		t.Fatalf("reply: %d %s", code, body)
	}

	// A tab that connects later gets both in the snapshot.
	s := e.hubStream(t, "a/b", tok)
	want := []string{"version", "agent", "threads"}
	var snapshot string
	for _, w := range want {
		kind, data := s.next(t)
		if kind != w {
			t.Fatalf("stream sent %s, want %s", kind, w)
		}
		snapshot = data
	}
	var evs []store.Event
	if err := json.Unmarshal([]byte(snapshot), &evs); err != nil || len(evs) != 2 || evs[1].ID != rr.ID {
		t.Fatalf("snapshot %s", snapshot)
	}
	waitViewers(t, e.st, "a/b", 1)

	// New replies, status and pointers arrive live.
	e.do("POST", "/api/p/a/b/reply", "application/json", strings.NewReader(`{"target":"variant-b","text":"And B?","state":"question"}`), agentHeaders)
	if kind, data := s.next(t); kind != "thread" || !strings.Contains(data, `"variant-b"`) {
		t.Fatalf("live reply: %s %s", kind, data)
	}
	code, body, _ = e.do("POST", "/api/p/a/b/status", "application/json", strings.NewReader(`{"text":"changing the flow"}`), agentHeaders)
	if code != 200 || !strings.Contains(body, `"delivered_to": 1`) {
		t.Fatalf("status: %d %s", code, body)
	}
	if kind, data := s.next(t); kind != "agent" || !strings.Contains(data, "changing the flow") {
		t.Fatalf("status push: %s %s", kind, data)
	}
	e.do("POST", "/api/p/a/b/point", "application/json", strings.NewReader(`{"target":"flow","text":"here"}`), agentHeaders)
	if kind, _ := s.next(t); kind != "point" {
		t.Fatalf("point push: %s", kind)
	}
	if code, body, _ := e.do("POST", "/api/p/a/b/reply", "application/json", strings.NewReader(`{"thread":"nope","text":"x","state":"done"}`), agentHeaders); code != 400 {
		t.Fatalf("unknown thread: %d %s", code, body)
	}
}

func TestReplyNeedsThreadOrTarget(t *testing.T) {
	e := newEnv(t)
	e.upload("a/b", nil)
	code, body, _ := e.do("POST", "/api/p/a/b/reply", "application/json", strings.NewReader(`{"text":"x","state":"done"}`), agentHeaders)
	if code != 400 || !strings.Contains(body, "thread or target is required") {
		t.Fatalf("reply without thread and target: %d %s", code, body)
	}
}

func TestThreadsNeedTheirOwnPrototypesToken(t *testing.T) {
	e := newEnv(t)
	e.upload("a/one", nil)
	e.upload("a/two", nil)
	if code := e.emitAsPage("a/two", e.pageToken("a/two"), `{"type":"comment","data":{"thread":"t1","target":"flow","text":"secret"}}`); code != 200 {
		t.Fatalf("comment: %d", code)
	}
	code, body, _ := e.do("GET", "/api/events/stream?name=a/two&hub=1&token="+e.pageToken("a/one"), "", nil, nil)
	if code != 401 || strings.Contains(body, "secret") {
		t.Fatalf("token of a/one read the threads of a/two: %d %s", code, body)
	}
}
