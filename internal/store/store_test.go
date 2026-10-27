package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	return openStore(t, t.TempDir(), DefaultLimits(), c), c
}

func openStore(t *testing.T, dir string, lim Limits, c *clock) *Store {
	t.Helper()
	s, err := Open(Options{Dir: dir, Limits: lim, Now: c.now})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func publish(t *testing.T, s *Store, name string) Info {
	t.Helper()
	info, _, err := s.Publish(PublishInput{Name: name, HTML: []byte("<html><body>x</body></html>")})
	if err != nil {
		t.Fatalf("publish %s: %v", name, err)
	}
	return info
}

func emit(t *testing.T, s *Store, name, typ string, auto bool) Event {
	t.Helper()
	ev, err := s.Emit(name, typ, json.RawMessage(`{"v":1}`), 0, auto)
	if err != nil {
		t.Fatalf("emit %s: %v", typ, err)
	}
	return ev
}

func fetchNow(t *testing.T, s *Store, name string, ack int64, types ...string) FetchResult {
	t.Helper()
	res, err := s.Fetch(context.Background(), name, types, ack, time.Millisecond)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return res
}

func TestPublishValidatesAndCountsVersions(t *testing.T) {
	s, _ := newStore(t)
	for _, bad := range []string{"my-app", "My-App/x", "a/b/c", "/x", "a/"} {
		if _, _, err := s.Publish(PublishInput{Name: bad, HTML: []byte("x")}); !errors.Is(err, ErrInvalidName) {
			t.Errorf("name %q: got %v, want ErrInvalidName", bad, err)
		}
	}
	if _, _, err := s.Publish(PublishInput{Name: "a/b"}); !errors.Is(err, ErrEmptyContent) {
		t.Errorf("empty html: got %v", err)
	}
	big := make([]byte, s.Limits().MaxHTMLBytes+1)
	if _, _, err := s.Publish(PublishInput{Name: "a/b", HTML: big}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big html: got %v", err)
	}
	if v := publish(t, s, "my-app/login").Version; v != 1 {
		t.Errorf("first version %d", v)
	}
	emit(t, s, "my-app/login", "choice", false)
	info := publish(t, s, "my-app/login")
	if info.Version != 2 || info.Unread != 1 {
		t.Errorf("republish: version %d unread %d, want 2 and 1 (inbox stays)", info.Version, info.Unread)
	}
}

func TestInboxRedeliversUntilAcknowledged(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	emit(t, s, "a/b", "choice", false)

	first := fetchNow(t, s, "a/b", 0)
	if len(first.Events) != 1 || first.Cursor != 1 {
		t.Fatalf("first fetch: %+v", first)
	}
	// The session died before it processed the event. A new session does not
	// know the cursor and must get the event again.
	again := fetchNow(t, s, "a/b", 0)
	if len(again.Events) != 1 || again.Events[0].Type != "choice" {
		t.Fatalf("new session got %+v, want the choice again", again.Events)
	}
	if info, _ := s.Get("a/b"); info.Unread != 1 {
		t.Fatalf("unread before ack: %d", info.Unread)
	}
	// The session that processed it acknowledges with the cursor.
	emit(t, s, "a/b", "comment", false)
	next := fetchNow(t, s, "a/b", again.Cursor)
	if len(next.Events) != 1 || next.Events[0].Type != "comment" {
		t.Fatalf("after ack: %+v", next.Events)
	}
	if info, _ := s.Get("a/b"); info.Unread != 1 {
		t.Fatalf("unread after ack of the choice: %d, want 1 (the comment)", info.Unread)
	}
	// An ack beyond the newest event acknowledges nothing that does not exist.
	fetchNow(t, s, "a/b", 999)
	emit(t, s, "a/b", "done", false)
	if res := fetchNow(t, s, "a/b", 0); len(res.Events) != 1 || res.Events[0].Type != "done" {
		t.Fatalf("event after a too large ack: %+v", res.Events)
	}
}

func TestInboxFiltersTypesAndSkipsAutoInUnread(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	emit(t, s, "a/b", "view", true)
	emit(t, s, "a/b", "click", true)
	if info, _ := s.Get("a/b"); info.Unread != 0 || info.Events != 2 {
		t.Fatalf("auto events: unread %d events %d", info.Unread, info.Events)
	}
	emit(t, s, "a/b", "choice", false)
	res := fetchNow(t, s, "a/b", 0, "choice", "comment")
	if len(res.Events) != 1 || res.Events[0].Type != "choice" {
		t.Fatalf("filtered fetch: %+v", res.Events)
	}
	all, _ := s.Events("a/b", nil, 0)
	if len(all) != 3 {
		t.Fatalf("history: %d events", len(all))
	}
}

func TestFetchWaitsForTheNextEvent(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	done := make(chan FetchResult)
	go func() {
		res, err := s.Fetch(context.Background(), "a/b", []string{"choice"}, 0, 5*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	time.Sleep(20 * time.Millisecond)
	emit(t, s, "a/b", "click", true) // filtered out, must not wake the reader with nothing
	emit(t, s, "a/b", "choice", false)
	select {
	case res := <-done:
		if len(res.Events) != 1 || res.Events[0].Type != "choice" {
			t.Fatalf("woke with %+v", res.Events)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not wake up")
	}
}

func TestFetchTimesOutEmpty(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	res, err := s.Fetch(context.Background(), "a/b", nil, 0, 10*time.Millisecond)
	if err != nil || !res.TimedOut || len(res.Events) != 0 {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestFetchReportsRemovalWhileWaiting(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	errc := make(chan error)
	go func() {
		_, err := s.Fetch(context.Background(), "a/b", nil, 0, 5*time.Second)
		errc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := s.Delete("a/b"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("got %v, want ErrUnknown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not notice the removal")
	}
}

func TestSweepUsesLastActivityAndKeepFor(t *testing.T) {
	s, c := newStore(t)
	publish(t, s, "a/short")
	ten := 10 * 24 * time.Hour
	if _, _, err := s.Publish(PublishInput{Name: "a/long", HTML: []byte("x"), KeepFor: &ten}); err != nil {
		t.Fatal(err)
	}
	c.add(48 * time.Hour)
	emit(t, s, "a/short", "view", true) // activity resets the clock
	c.add(48 * time.Hour)
	if gone := s.Sweep(); len(gone) != 0 {
		t.Fatalf("swept %v after 2 days of inactivity", gone)
	}
	c.add(25 * time.Hour)
	if gone := s.Sweep(); len(gone) != 1 || gone[0] != "a/short" {
		t.Fatalf("swept %v, want a/short", gone)
	}
	c.add(6 * 24 * time.Hour)
	if gone := s.Sweep(); len(gone) != 1 || gone[0] != "a/long" {
		t.Fatalf("swept %v, want a/long", gone)
	}
}

func TestKeepForIsCapped(t *testing.T) {
	s, _ := newStore(t)
	year := 365 * 24 * time.Hour
	info, warning, err := s.Publish(PublishInput{Name: "a/b", HTML: []byte("x"), KeepFor: &year})
	if err != nil {
		t.Fatal(err)
	}
	if info.KeepFor != "30d" || warning == "" {
		t.Fatalf("keep_for %q warning %q", info.KeepFor, warning)
	}
}

func TestEvictionPrefersExpiredThenUnprotected(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	lim := DefaultLimits()
	lim.MaxTotalBytes = 30
	s := openStore(t, t.TempDir(), lim, c)
	ten := 10 * 24 * time.Hour
	mustPublish := func(name string, n int, keep *time.Duration) {
		t.Helper()
		if _, _, err := s.Publish(PublishInput{Name: name, HTML: make([]byte, n), KeepFor: keep}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	mustPublish("a/protected", 10, &ten)
	c.add(time.Hour)
	mustPublish("a/old", 10, nil)
	c.add(time.Hour)
	mustPublish("a/new", 10, nil)
	mustPublish("a/next", 10, nil) // must evict a/old, the oldest without keep_for
	if _, err := s.Get("a/old"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("a/old still there")
	}
	if _, err := s.Get("a/protected"); err != nil {
		t.Fatalf("a/protected evicted before unprotected ones")
	}
	if _, _, err := s.Publish(PublishInput{Name: "a/huge", HTML: make([]byte, 40)}); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("got %v, want ErrStoreFull", err)
	}
}

func TestEventsAreBounded(t *testing.T) {
	c := &clock{t: time.Now()}
	lim := DefaultLimits()
	lim.MaxEvents = 3
	s := openStore(t, t.TempDir(), lim, c)
	publish(t, s, "a/b")
	for range 5 {
		emit(t, s, "a/b", "click", true)
	}
	evs, _ := s.Events("a/b", nil, 0)
	if len(evs) != 3 || evs[0].Seq != 3 {
		t.Fatalf("kept %d events starting at %d", len(evs), evs[0].Seq)
	}
	if _, err := s.Emit("a/b", "Bad Type", nil, 0, false); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("bad type: %v", err)
	}
}

func TestSubscribersGetPushesAndClose(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	ch, cancel, err := s.Subscribe("a/b")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if n, _ := s.Message("a/b", "hello", nil); n != 1 {
		t.Fatalf("message reached %d tabs", n)
	}
	if p := <-ch; p.Kind != "message" {
		t.Fatalf("got %s", p.Kind)
	}
	publish(t, s, "a/b")
	if p := <-ch; p.Kind != "version" {
		t.Fatalf("got %s", p.Kind)
	}
	if err := s.Delete("a/b"); err != nil {
		t.Fatal(err)
	}
	if p := <-ch; p.Kind != "gone" {
		t.Fatalf("got %s", p.Kind)
	}
	if _, open := <-ch; open {
		t.Fatal("channel still open after removal")
	}
}

func TestDurations(t *testing.T) {
	for in, want := range map[string]time.Duration{"10d": 240 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "x", "-1d", "1w"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if FormatDuration(240*time.Hour) != "10d" || FormatDuration(90*time.Minute) != "1h30m0s" {
		t.Error("format")
	}
}
