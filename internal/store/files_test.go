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
	fetchNow(t, s, "a/b", 0, "choice")
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

func TestRestartKeepsAcksAfterSkippedEvents(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	s := openStore(t, dir, DefaultLimits(), c)
	publish(t, s, "a/b")
	emit(t, s, "a/b", "rating", false)
	emit(t, s, "a/b", "choice", false)
	emit(t, s, "a/b", "choice", false)
	first := fetchNow(t, s, "a/b", 0, "choice") // delivers 2 and 3
	fetchNow(t, s, "a/b", 2, "choice")          // acknowledges 2

	// The fetch was handed out before the restart; its cursor still counts.
	r := openStore(t, dir, DefaultLimits(), c)
	res := fetchNow(t, r, "a/b", first.Cursor, "choice")
	if len(res.Events) != 0 {
		t.Fatalf("acknowledged choices delivered again: %+v", res.Events)
	}
	res = fetchNow(t, r, "a/b", 0)
	if len(res.Events) != 1 || res.Events[0].Type != "rating" {
		t.Fatalf("skipped rating after restart: %+v", res.Events)
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

	// The next event must not be glued to the damaged line.
	emit(t, r, "a/b", "comment", false)
	r = openStore(t, dir, DefaultLimits(), c)
	if evs, _ := r.Events("a/b", nil, 0); len(evs) != 2 || evs[1].Type != "comment" {
		t.Fatalf("event after damaged line lost: %+v", evs)
	}
}

func TestUnreadableRepoIsSkipped(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Now()}
	s := openStore(t, dir, DefaultLimits(), c)
	publish(t, s, "a/good")
	repo := filepath.Join(dir, "c")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(repo, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(repo, 0o700) })
	if _, err := os.ReadDir(repo); err == nil {
		t.Skip("the directory is still readable (running as root or on Windows)")
	}

	r := openStore(t, dir, DefaultLimits(), c)
	if _, err := r.Get("a/good"); err != nil {
		t.Fatal(err)
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

func TestPublishOverLeftoverDirectoryStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Now()}
	s := openStore(t, dir, DefaultLimits(), c)
	publish(t, s, "a/b")
	emit(t, s, "a/b", "old", false)
	emit(t, s, "a/b", "old", false)
	os.WriteFile(filepath.Join(dir, "a", "b", metaFile), []byte("{"), 0o600)

	r := openStore(t, dir, DefaultLimits(), c)
	publish(t, r, "a/b")
	if ev := emit(t, r, "a/b", "new", false); ev.Seq != 1 {
		t.Fatalf("seq %d, want 1", ev.Seq)
	}
	r = openStore(t, dir, DefaultLimits(), c)
	if evs, _ := r.Events("a/b", nil, 0); len(evs) != 1 || evs[0].Type != "new" {
		t.Fatalf("events of the old prototype came back: %+v", evs)
	}
}

func TestFailedRepublishKeepsPreviousHTML(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, DefaultLimits(), &clock{t: time.Now()})
	publish(t, s, "a/b")
	// A directory in place of meta.json makes the metadata write fail.
	meta := filepath.Join(dir, "a", "b", metaFile)
	os.Remove(meta)
	os.MkdirAll(filepath.Join(meta, "x"), 0o700)
	if _, _, err := s.Publish(PublishInput{Name: "a/b", HTML: []byte("<p>v2</p>")}); err == nil {
		t.Fatal("publish succeeded although meta.json cannot be written")
	}
	html, err := os.ReadFile(filepath.Join(dir, "a", "b", htmlFile))
	if err != nil || string(html) != "<html><body>x</body></html>" {
		t.Fatalf("html on disk %q %v, want version 1", html, err)
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
