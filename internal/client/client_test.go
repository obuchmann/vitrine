package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/obuchmann/vitrine/internal/store"
	"github.com/obuchmann/vitrine/internal/web"
)

const token = "vitrine_test-token"

func newServer(t *testing.T) (*Client, *store.Store, *httptest.Server) {
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
	return &Client{BaseURL: srv.URL, Token: token}, st, srv
}

func TestPublishAndInbox(t *testing.T) {
	c, st, _ := newServer(t)
	ctx := context.Background()
	res, err := c.Publish(ctx, PublishInput{Name: "a/b", HTML: []byte("<p>hi</p>"), KeepFor: "10d", Context: map[string]string{"branch": "feat/x"}})
	if err != nil || res.Version != 1 || res.URL != c.URL("a/b") || res.KeepFor != "10d" {
		t.Fatalf("publish: %+v %v", res, err)
	}
	info, err := c.Get(ctx, "a/b")
	if err != nil || info.Context["branch"] != "feat/x" {
		t.Fatalf("get: %+v %v", info, err)
	}
	if list, err := c.List(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}

	st.Emit("a/b", "choice", []byte(`{"variant":"B"}`), 1, false)
	st.Emit("a/b", "comment", []byte(`"nice"`), 1, false)
	got, err := c.Next(ctx, "a/b", []string{"choice"}, 0, time.Second)
	if err != nil || len(got.Events) != 1 || got.Events[0].Type != "choice" || got.UnreadLeft != 1 {
		t.Fatalf("next: %+v %v", got, err)
	}
	got, err = c.Next(ctx, "a/b", nil, got.Cursor, 0)
	if err != nil || len(got.Events) != 1 || got.Events[0].Type != "comment" {
		t.Fatalf("next after ack: %+v %v", got, err)
	}
	got, err = c.Next(ctx, "a/b", nil, got.Cursor, 0)
	if err != nil || !got.TimedOut || len(got.Events) != 0 {
		t.Fatalf("next when all read: %+v %v", got, err)
	}
	if evs, err := c.Events(ctx, "a/b", nil, 0); err != nil || len(evs) != 2 {
		t.Fatalf("events: %+v %v", evs, err)
	}
	if n, err := c.Message(ctx, "a/b", "thanks", nil); err != nil || n != 0 {
		t.Fatalf("message: %d %v", n, err)
	}
	if res, err := c.Extend(ctx, "a/b", "2d"); err != nil || res.KeepFor != "2d" {
		t.Fatalf("extend: %+v %v", res, err)
	}
	if err := c.Delete(ctx, "a/b"); err != nil {
		t.Fatal(err)
	}
	var apiErr *Error
	if _, err := c.Get(ctx, "a/b"); !errors.As(err, &apiErr) || apiErr.Code != "unknown_prototype" || apiErr.Status != 404 || apiErr.Hint == "" {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestNextReportsRemovalWhileWaiting(t *testing.T) {
	c, st, _ := newServer(t)
	ctx := context.Background()
	if _, err := c.Publish(ctx, PublishInput{Name: "a/b", HTML: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		st.Delete("a/b")
	}()
	var apiErr *Error
	if _, err := c.Next(ctx, "a/b", nil, 0, 10*time.Second); !errors.As(err, &apiErr) || apiErr.Code != "unknown_prototype" {
		t.Fatalf("got %v", err)
	}
}

func TestWrongTokenIsUnauthorized(t *testing.T) {
	c, _, _ := newServer(t)
	c.Token = "wrong"
	var apiErr *Error
	if _, err := c.List(context.Background()); !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("got %v", err)
	}
}

func TestStartIsCalledWhenNothingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	c := &Client{BaseURL: "http://" + addr, Token: token}
	starts := 0
	c.Start = func(context.Context) error {
		starts++
		return errors.New("cannot start")
	}
	if _, err := c.List(context.Background()); err == nil || err.Error() != "cannot start" || starts != 1 {
		t.Fatalf("got %v after %d starts", err, starts)
	}

	// Start brings a server up on that address; the request is retried.
	c.Start = func(context.Context) error {
		starts++
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "[]") })}
		go srv.Serve(ln)
		t.Cleanup(func() { srv.Close() })
		return nil
	}
	if list, err := c.List(context.Background()); err != nil || len(list) != 0 || starts != 2 {
		t.Fatalf("got %v %v after %d starts", list, err, starts)
	}
}

func TestHealth(t *testing.T) {
	c, _, _ := newServer(t)
	if v, err := c.Health(context.Background()); err != nil || v != "test" {
		t.Fatalf("health: %q %v", v, err)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	defer other.Close()
	c.BaseURL = other.URL
	if _, err := c.Health(context.Background()); !errors.Is(err, ErrNotVitrine) {
		t.Fatalf("other server: %v", err)
	}
}
