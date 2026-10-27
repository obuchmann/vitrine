package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	EventToken   string            `json:"event_token"`
}

func (s *Store) protoDir(name string) string {
	repo, rest, _ := strings.Cut(name, "/")
	return filepath.Join(s.dir, repo, rest)
}

// writePrototype writes HTML and metadata of a new or republished prototype.
func (s *Store) writePrototype(p *prototype) error {
	dir := s.protoDir(p.name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, htmlFile), p.html); err != nil {
		return err
	}
	return s.writeMeta(p)
}

func (s *Store) writeMeta(p *prototype) error {
	m := meta{
		Name: p.name, Version: p.version, Context: p.context,
		PublishedAt: p.publishedAt.UTC(), LastActivity: p.lastActivity.UTC(),
		NextSeq: p.nextSeq, Acked: p.acked, EventToken: p.eventToken,
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
	f, err := os.OpenFile(filepath.Join(s.protoDir(p.name), eventsFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
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
			return err
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
		subs: map[chan Push]struct{}{},
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
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), s.limits.MaxEventBytes+64<<10)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			// A line cut off by a crash: everything before it is intact.
			s.log.Warn("ignoring damaged event line", "name", p.name, "err", err)
			continue
		}
		p.events = append(p.events, ev)
		p.eventBytes += len(ev.Data)
		p.nextSeq = max(p.nextSeq, ev.Seq)
		if ev.At.After(p.lastActivity) {
			p.lastActivity = ev.At
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(p.events) > s.limits.MaxEvents {
		for _, old := range p.events[:len(p.events)-s.limits.MaxEvents] {
			p.eventBytes -= len(old.Data)
		}
		p.events = p.events[len(p.events)-s.limits.MaxEvents:]
	}
	return nil
}
