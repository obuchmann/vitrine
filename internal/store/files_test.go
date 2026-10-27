package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartKeepsPrototypesInboxAndAcks(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	s := openStore(t, dir, DefaultLimits(), c)
	ten := 10 * 24 * time.Hour
	if _, _, err := s.Publish(PublishInput{Name: "a/b", HTML: []byte("<p>v1</p>"), KeepFor: &ten, Context: map[string]string{"branch": "x"}}); err != nil {
		t.Fatal(err)
	}
	publish(t, s, "a/b") // version 2
	first := emit(t, s, "a/b", "choice", false)
	emit(t, s, "a/b", "comment", false)
	fetchNow(t, s, "a/b", first.Seq) // acknowledge the choice only
	page, _ := s.Page("a/b")

	// A new process on the same directory, e.g. after the idle daemon exited.
	c.add(time.Hour)
	r := openStore(t, dir, DefaultLimits(), c)
	info, err := r.Get("a/b")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != 2 || info.KeepFor != "10d" || info.Context["branch"] != "x" || info.Events != 2 || info.Unread != 1 {
		t.Fatalf("after restart: %+v", info)
	}
	res := fetchNow(t, r, "a/b", 0)
	if len(res.Events) != 1 || res.Events[0].Type != "comment" {
		t.Fatalf("unacknowledged after restart: %+v", res.Events)
	}
	if !r.CheckEventToken("a/b", page.EventToken) {
		t.Fatal("event token changed across restart: open tabs would lose their inbox")
	}
	if ev := emit(t, r, "a/b", "next", false); ev.Seq != 3 {
		t.Fatalf("seq after restart %d, want 3", ev.Seq)
	}
}

func TestRestartRecoversActivityFromEvents(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	s := openStore(t, dir, DefaultLimits(), c)
	publish(t, s, "a/b")
	c.add(48 * time.Hour)
	emit(t, s, "a/b", "choice", false) // meta.json is not rewritten for events
	c.add(48 * time.Hour)
	r := openStore(t, dir, DefaultLimits(), c)
	if gone := r.Sweep(); len(gone) != 0 {
		t.Fatalf("swept %v although the last event is 2 days old", gone)
	}
}

func TestDamagedEventLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Now()}
	s := openStore(t, dir, DefaultLimits(), c)
	publish(t, s, "a/b")
	emit(t, s, "a/b", "choice", false)
	f, err := os.OpenFile(filepath.Join(dir, "a", "b", eventsFile), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"seq":2,"type":"cut off`) // a crash in the middle of a write
	f.Close()

	r := openStore(t, dir, DefaultLimits(), c)
	evs, err := r.Events("a/b", nil, 0)
	if err != nil || len(evs) != 1 || evs[0].Type != "choice" {
		t.Fatalf("events %+v %v", evs, err)
	}
}

func TestUnreadablePrototypeIsSkipped(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Now()}
	s := openStore(t, dir, DefaultLimits(), c)
	publish(t, s, "a/good")
	publish(t, s, "a/bad")
	os.WriteFile(filepath.Join(dir, "a", "bad", metaFile), []byte("{"), 0o600)

	r := openStore(t, dir, DefaultLimits(), c)
	if _, err := r.Get("a/good"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("a/bad"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("damaged prototype loaded: %v", err)
	}
}

func TestDeleteRemovesFiles(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, DefaultLimits(), &clock{t: time.Now()})
	publish(t, s, "a/one")
	publish(t, s, "a/two")
	if err := s.Delete("a/one"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "one")); !os.IsNotExist(err) {
		t.Fatalf("a/one still on disk: %v", err)
	}
	if err := s.Delete("a/two"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Fatalf("empty repo directory left behind: %v", err)
	}
}

func TestCompactionKeepsSeq(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Now()}
	lim := DefaultLimits()
	lim.MaxEvents = 3
	s := openStore(t, dir, lim, c)
	publish(t, s, "a/b")
	for range 5 {
		emit(t, s, "a/b", "click", true)
	}
	r := openStore(t, dir, lim, c)
	evs, _ := r.Events("a/b", nil, 0)
	if len(evs) != 3 || evs[0].Seq != 3 {
		t.Fatalf("after restart kept %d events from seq %d", len(evs), evs[0].Seq)
	}
	if ev, _ := r.Emit("a/b", "click", json.RawMessage(`{}`), 0, true); ev.Seq != 6 {
		t.Fatalf("seq %d, want 6", ev.Seq)
	}
}

func TestNamesArePortableDirectories(t *testing.T) {
	for _, bad := range []string{"con/x", "a/nul", "a/aux.html", "lpt1/b", "a/b.", "a/.b", "a/-b"} {
		if ValidName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"my-app/login", "a/b.v2", "console/x", "a/con-test", "a1/b_c"} {
		if !ValidName(good) {
			t.Errorf("%q rejected", good)
		}
	}
}
