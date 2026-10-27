package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// On disk, every prototype is one directory:
//
//	<dir>/<repo>/<name>/index.html
//	<dir>/<repo>/<name>/meta.json     everything except HTML and events
//	<dir>/<repo>/<name>/events.jsonl  one event per line, append-only
const (
	htmlFile   = "index.html"
	metaFile   = "meta.json"
	eventsFile = "events.jsonl"
)

type meta struct {
	Name         string            `json:"name"`
	Version      int               `json:"version"`
	Context      map[string]string `json:"context,omitempty"`
	KeepFor      string            `json:"keep_for,omitempty"`
	PublishedAt  time.Time         `json:"published_at"`
	LastActivity time.Time         `json:"last_activity"`
	NextSeq      int64             `json:"next_seq"`
	Acked        int64             `json:"acked"`
	AckedAfter   []int64           `json:"acked_after,omitempty"` // acknowledged one by one after acked
	Delivered    []int64           `json:"delivered,omitempty"`   // handed out by a fetch, not yet acknowledged
	EventToken   string            `json:"event_token"`
}

func (s *Store) protoDir(name string) string {
	repo, rest, _ := strings.Cut(name, "/")
	return filepath.Join(s.dir, repo, rest)
}

// writePrototype writes HTML and metadata of a new or republished prototype.
// oldHTML is the HTML of the previous version, nil for a new prototype. If the
// metadata cannot be written, the previous HTML is put back so HTML and
// version on disk still belong together.
func (s *Store) writePrototype(p *prototype, oldHTML []byte) error {
	dir := s.protoDir(p.name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if oldHTML == nil {
		// Left over from a prototype that could not be loaded or removed: its
		// events would mix with the new ones.
		if err := os.Remove(filepath.Join(dir, eventsFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	html := filepath.Join(dir, htmlFile)
	if err := writeFileAtomic(html, p.html); err != nil {
		return err
	}
	if err := s.writeMeta(p); err != nil {
		if oldHTML != nil {
			if rerr := writeFileAtomic(html, oldHTML); rerr != nil {
				s.log.Error("restoring previous html", "name", p.name, "err", rerr)
			}
		}
		return err
	}
	return nil
}

func sortedSeqs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for seq := range set {
		out = append(out, seq)
	}
	slices.Sort(out)
	return out
}

func (s *Store) writeMeta(p *prototype) error {
	m := meta{
		Name: p.name, Version: p.version, Context: p.context,
		PublishedAt: p.publishedAt.UTC(), LastActivity: p.lastActivity.UTC(),
		NextSeq: p.nextSeq, Acked: p.acked, EventToken: p.eventToken,
		AckedAfter: sortedSeqs(p.ackedAfter), Delivered: sortedSeqs(p.delivered),
	}
	if p.keepFor > 0 {
		m.KeepFor = FormatDuration(p.keepFor)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.protoDir(p.name), metaFile), append(b, '\n'))
}

// appendEvent adds one line and syncs it, so an event the sender got an
// answer for survives a crash.
func (s *Store) appendEvent(p *prototype, ev Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.protoDir(p.name), eventsFile), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// A crash or failed write can leave a line without its newline. Start a
	// new line, so the event is not glued to the damaged one and lost with it.
	torn, err := endsTorn(f)
	if err != nil {
		f.Close()
		return err
	}
	if torn {
		line = append([]byte{'\n'}, line...)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// endsTorn reports whether f is not empty and does not end with a newline.
func endsTorn(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// writeEvents replaces the event file with the events kept in memory.
func (s *Store) writeEvents(p *prototype) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, ev := range p.events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return writeFileAtomic(filepath.Join(s.protoDir(p.name), eventsFile), buf.Bytes())
}

func (s *Store) removeFiles(name string) error {
	dir := s.protoDir(name)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// Remove the repo directory once its last prototype is gone.
	repoDir := filepath.Dir(dir)
	if entries, err := os.ReadDir(repoDir); err == nil && len(entries) == 0 {
		if err := os.Remove(repoDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// writeFileAtomic replaces path in one step: a reader sees the old or the new
// content, never a partial file.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// load reads every prototype below s.dir. A prototype that cannot be read is
// logged and skipped, so one damaged directory does not stop vitrine.
func (s *Store) load() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	repos, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if !repo.IsDir() || !validSegment(repo.Name()) {
			continue
		}
		names, err := os.ReadDir(filepath.Join(s.dir, repo.Name()))
		if err != nil {
			s.log.Warn("skipping stored repository", "repo", repo.Name(), "err", err)
			continue
		}
		for _, n := range names {
			name := repo.Name() + "/" + n.Name()
			if !n.IsDir() || !ValidName(name) {
				continue
			}
			p, err := s.loadPrototype(name)
			if err != nil {
				s.log.Warn("skipping stored prototype", "name", name, "err", err)
				continue
			}
			s.protos[name] = p
		}
	}
	return nil
}

func (s *Store) loadPrototype(name string) (*prototype, error) {
	dir := s.protoDir(name)
	b, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", metaFile, err)
	}
	if m.Name != name || m.EventToken == "" {
		return nil, fmt.Errorf("%s does not belong to %s", metaFile, name)
	}
	html, err := os.ReadFile(filepath.Join(dir, htmlFile))
	if err != nil {
		return nil, err
	}
	p := &prototype{
		name: name, html: html, version: m.Version, context: m.Context,
		publishedAt: m.PublishedAt, lastActivity: m.LastActivity, savedAt: m.LastActivity,
		eventToken: m.EventToken, nextSeq: m.NextSeq, acked: m.Acked,
		ackedAfter: map[int64]bool{}, delivered: map[int64]bool{},
		subs: map[chan Push]bool{}, changed: make(chan struct{}),
	}
	for _, seq := range m.AckedAfter {
		p.ackedAfter[seq] = true
	}
	for _, seq := range m.Delivered {
		p.delivered[seq] = true
	}
	if m.KeepFor != "" {
		if p.keepFor, err = ParseDuration(m.KeepFor); err != nil {
			return nil, err
		}
	}
	if err := s.loadEvents(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) loadEvents(p *prototype) error {
	f, err := os.Open(filepath.Join(s.protoDir(p.name), eventsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var ev Event
			if uerr := json.Unmarshal(line, &ev); uerr != nil {
				// A line cut off by a crash: everything before it is intact.
				s.log.Warn("ignoring damaged event line", "name", p.name, "err", uerr)
			} else {
				p.events = append(p.events, ev)
				p.eventBytes += len(ev.Data)
				p.nextSeq = max(p.nextSeq, ev.Seq)
				if ev.At.After(p.lastActivity) {
					p.lastActivity = ev.At
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	// Only if the limit was lowered; Emit keeps the inbox within it.
	s.dropEventsLocked(p, len(p.events)-s.limits.MaxEvents)
	return nil
}
