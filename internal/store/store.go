// Package store keeps prototypes, their inbox and their readers.
//
// Everything is written through to plain files below one directory and loaded
// again by Open, so a restart loses neither prototypes nor unacknowledged
// events (docs/adr/0003-storage.md). Only one process may use a directory at a
// time; the single instance rule (docs/adr/0001-runtime-modes.md) ensures that.
// Every method is safe for concurrent use.
package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnknown      = errors.New("unknown_prototype")
	ErrInvalidName  = errors.New("invalid name: expected <repo>/<name> in lower case, e.g. my-app/login")
	ErrInvalidType  = errors.New("invalid event type: lower case letters, digits, '.', '_' or '-', at most 40 characters")
	ErrTooLarge     = errors.New("too large")
	ErrStoreFull    = errors.New("store full")
	ErrEmptyContent = errors.New("empty html")
)

var (
	segmentRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	typeRE    = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,39}$`)
)

// windowsReserved are device names Windows refuses as file names, with or
// without an extension. Names map to directories, so they are invalid on
// every platform to keep state directories portable.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidName reports whether name has the form <repo>/<name> and can be used
// as a directory on every supported platform.
func ValidName(name string) bool {
	repo, rest, ok := strings.Cut(name, "/")
	return ok && validSegment(repo) && validSegment(rest)
}

func validSegment(s string) bool {
	if !segmentRE.MatchString(s) || strings.HasSuffix(s, ".") {
		return false
	}
	base, _, _ := strings.Cut(s, ".")
	return !windowsReserved[base]
}

// Limits bound what one store may hold.
type Limits struct {
	DefaultTTL    time.Duration // lifetime after the last activity without keep_for
	MaxKeepFor    time.Duration // upper bound for keep_for
	MaxHTMLBytes  int           // per prototype
	MaxEvents     int           // per prototype; the oldest fall out first
	MaxEventBytes int           // per event payload
	MaxTotalBytes int           // all HTML plus all event payloads
}

// DefaultLimits are the documented starting values.
func DefaultLimits() Limits {
	return Limits{
		DefaultTTL:    72 * time.Hour,
		MaxKeepFor:    30 * 24 * time.Hour,
		MaxHTMLBytes:  2 << 20,
		MaxEvents:     10000,
		MaxEventBytes: 64 << 10,
		MaxTotalBytes: 200 << 20,
	}
}

// Event is one interaction a prototype reported.
type Event struct {
	Seq     int64           `json:"seq"`
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	Version int             `json:"version"`
	Auto    bool            `json:"auto,omitempty"`
	At      time.Time       `json:"at"`
}

// Push is sent to the open tabs and streams of a prototype.
type Push struct {
	Kind string `json:"kind"` // "event", "message", "version" or "gone"
	Data any    `json:"data"`
}

// Info describes a prototype without its HTML.
type Info struct {
	Name         string            `json:"name"`
	Version      int               `json:"version"`
	Bytes        int               `json:"bytes"`
	PublishedAt  time.Time         `json:"published_at"`
	LastActivity time.Time         `json:"last_activity"`
	ExpiresAt    time.Time         `json:"expires_at"`
	KeepFor      string            `json:"keep_for,omitempty"`
	Unread       int               `json:"unread"`
	Events       int               `json:"events"`
	Viewers      int               `json:"viewers"`
	Context      map[string]string `json:"context,omitempty"`
}

// Page is what a browser gets: the HTML, its version and the token that lets
// the page report events into its own inbox.
type Page struct {
	HTML       []byte
	Version    int
	EventToken string
}

type prototype struct {
	name         string
	html         []byte
	version      int
	context      map[string]string
	keepFor      time.Duration
	publishedAt  time.Time
	lastActivity time.Time
	savedAt      time.Time // lastActivity as last written to disk
	eventToken   string

	events     []Event
	eventBytes int
	nextSeq    int64
	acked      int64 // every event up to this seq is acknowledged

	subs map[chan Push]struct{}
}

func (p *prototype) bytes() int { return len(p.html) + p.eventBytes }

// Options configure Open.
type Options struct {
	Dir    string // where prototypes are stored; created if missing
	Limits Limits
	Now    func() time.Time // nil means time.Now
	Log    *slog.Logger     // nil discards; reports files that could not be read or written
}

// Store holds all prototypes.
type Store struct {
	dir    string
	limits Limits
	now    func() time.Time
	log    *slog.Logger

	mu      sync.Mutex
	protos  map[string]*prototype
	changed chan struct{} // closed and replaced on every new event
}

// Open loads every prototype stored in o.Dir.
func Open(o Options) (*Store, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Store{dir: o.Dir, limits: o.Limits, now: o.Now, log: o.Log, protos: map[string]*prototype{}, changed: make(chan struct{})}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Limits returns the configured limits.
func (s *Store) Limits() Limits { return s.limits }

// ClampKeepFor caps keep_for at the configured maximum. capped is true if it did.
func (s *Store) ClampKeepFor(d time.Duration) (clamped time.Duration, capped bool) {
	if d > s.limits.MaxKeepFor {
		return s.limits.MaxKeepFor, true
	}
	if d < 0 {
		return 0, false
	}
	return d, false
}

// PublishInput is what an agent uploads. A nil KeepFor or Context keeps the
// previous value of an existing prototype.
type PublishInput struct {
	Name    string
	HTML    []byte
	KeepFor *time.Duration
	Context map[string]string
}

// Publish adds a prototype or replaces its HTML. The version counts up, the
// inbox and the event token stay.
func (s *Store) Publish(in PublishInput) (Info, string, error) {
	if !ValidName(in.Name) {
		return Info{}, "", ErrInvalidName
	}
	if len(in.HTML) == 0 {
		return Info{}, "", ErrEmptyContent
	}
	if len(in.HTML) > s.limits.MaxHTMLBytes {
		return Info{}, "", fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, len(in.HTML), s.limits.MaxHTMLBytes)
	}
	var warning string
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	old, exists := s.protos[in.Name]
	grow := len(in.HTML)
	if exists {
		grow -= len(old.html)
	}
	if err := s.makeRoomLocked(grow, in.Name, now); err != nil {
		return Info{}, "", err
	}

	// Build the next state, write it, and only then make it visible, so a
	// failed write leaves the previous version intact.
	var p prototype
	if exists {
		p = *old
	} else {
		p = prototype{name: in.Name, subs: map[chan Push]struct{}{}, eventToken: newToken()}
	}
	p.html = slices.Clone(in.HTML)
	p.version++
	p.publishedAt = now
	p.lastActivity = now
	if in.KeepFor != nil {
		d, capped := s.ClampKeepFor(*in.KeepFor)
		p.keepFor = d
		if capped {
			warning = fmt.Sprintf("keep_for capped at %s", FormatDuration(d))
		}
	}
	if in.Context != nil {
		p.context = cloneMap(in.Context)
	}
	if err := s.writePrototype(&p); err != nil {
		if !exists {
			s.removeFiles(in.Name)
		}
		return Info{}, "", err
	}
	p.savedAt = now
	if exists {
		*old = p
	} else {
		s.protos[in.Name] = &p
	}
	s.pushLocked(&p, Push{Kind: "version", Data: map[string]int{"version": p.version}})
	return s.infoLocked(s.protos[in.Name]), warning, nil
}

// makeRoomLocked evicts other prototypes until grow more bytes fit: expired
// ones first, then the longest unused without keep_for, then the rest.
func (s *Store) makeRoomLocked(grow int, keep string, now time.Time) error {
	total := 0
	for _, p := range s.protos {
		total += p.bytes()
	}
	if total+grow <= s.limits.MaxTotalBytes {
		return nil
	}
	var cands []*prototype
	for _, p := range s.protos {
		if p.name != keep {
			cands = append(cands, p)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		ae, be := !s.expiresLocked(a).After(now), !s.expiresLocked(b).After(now)
		if ae != be {
			return ae
		}
		if (a.keepFor > 0) != (b.keepFor > 0) {
			return a.keepFor == 0
		}
		return a.lastActivity.Before(b.lastActivity)
	})
	for _, p := range cands {
		if total+grow <= s.limits.MaxTotalBytes {
			break
		}
		total -= p.bytes()
		s.removeLocked(p, "evicted")
	}
	if total+grow > s.limits.MaxTotalBytes {
		return ErrStoreFull
	}
	return nil
}

// Page returns what a browser shows and counts as activity.
func (s *Store) Page(name string) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return Page{}, ErrUnknown
	}
	s.touchLocked(p)
	return Page{HTML: p.html, Version: p.version, EventToken: p.eventToken}, nil
}

// CheckEventToken reports whether token is the event token of name.
func (s *Store) CheckEventToken(name, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	return ok && token != "" && subtle.ConstantTimeCompare([]byte(p.eventToken), []byte(token)) == 1
}

// Get describes one prototype.
func (s *Store) Get(name string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return Info{}, ErrUnknown
	}
	return s.infoLocked(p), nil
}

// List describes all prototypes, the most recently active first.
func (s *Store) List() []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Info, 0, len(s.protos))
	for _, p := range s.protos {
		out = append(out, s.infoLocked(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity.After(out[j].LastActivity) })
	return out
}

// Delete removes a prototype at once.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return ErrUnknown
	}
	s.removeLocked(p, "deleted")
	return nil
}

// Extend sets keep_for without uploading again and counts as activity.
func (s *Store) Extend(name string, keepFor time.Duration) (Info, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return Info{}, "", ErrUnknown
	}
	d, capped := s.ClampKeepFor(keepFor)
	next := *p
	next.keepFor = d
	next.lastActivity = s.now()
	if err := s.writeMeta(&next); err != nil {
		return Info{}, "", err
	}
	next.savedAt = next.lastActivity
	*p = next
	warning := ""
	if capped {
		warning = fmt.Sprintf("keep_for capped at %s", FormatDuration(d))
	}
	return s.infoLocked(p), warning, nil
}

// Emit records an event of a prototype. version is the version the page
// showed; 0 means the current one. The event is on disk before Emit returns.
func (s *Store) Emit(name, typ string, data json.RawMessage, version int, auto bool) (Event, error) {
	if !typeRE.MatchString(typ) {
		return Event{}, ErrInvalidType
	}
	if len(data) > s.limits.MaxEventBytes {
		return Event{}, fmt.Errorf("%w: event data %d bytes, at most %d", ErrTooLarge, len(data), s.limits.MaxEventBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return Event{}, ErrUnknown
	}
	now := s.now()
	if err := s.makeRoomLocked(len(data), name, now); err != nil {
		return Event{}, err
	}
	if version <= 0 {
		version = p.version
	}
	seq := p.nextSeq + 1
	ev := Event{Seq: seq, ID: fmt.Sprintf("evt_%d", seq), Type: typ, Data: slices.Clone(data),
		Version: version, Auto: auto, At: now.UTC()}
	if err := s.appendEvent(p, ev); err != nil {
		return Event{}, err
	}
	p.nextSeq = seq
	p.events = append(p.events, ev)
	p.eventBytes += len(ev.Data)
	if len(p.events) > s.limits.MaxEvents {
		// Drop a tenth more than needed so the file is not rewritten on every event.
		keep := max(s.limits.MaxEvents-s.limits.MaxEvents/10, 1)
		for _, old := range p.events[:len(p.events)-keep] {
			p.eventBytes -= len(old.Data)
		}
		p.events = slices.Clone(p.events[len(p.events)-keep:])
		if err := s.writeEvents(p); err != nil {
			s.log.Error("compacting events", "name", name, "err", err)
		}
	}
	// The event carries its time, so Open recovers the activity from it.
	p.lastActivity = now
	p.savedAt = now
	s.pushLocked(p, Push{Kind: "event", Data: ev})
	close(s.changed)
	s.changed = make(chan struct{})
	return ev, nil
}

// FetchResult is what Fetch hands out. Cursor is the seq of the last returned
// event; passing it as ack to the next fetch acknowledges the batch.
type FetchResult struct {
	Events     []Event `json:"events"`
	Cursor     int64   `json:"cursor"`
	UnreadLeft int     `json:"unread_left"`
	TimedOut   bool    `json:"timed_out,omitempty"`
}

// Fetch is the inbox of a prototype. ack acknowledges every event up to that
// seq (the cursor of the previous fetch; 0 acknowledges nothing). Fetch then
// returns every unacknowledged event whose type is in types (all if empty).
// With no such event it waits up to wait.
//
// Only an explicit ack marks events as read. A session that dies before it
// processed a batch does not know the cursor, so the next session gets the
// same events again: delivered at least once, never lost. Acknowledging a
// cursor also acknowledges skipped events of other types before it.
func (s *Store) Fetch(ctx context.Context, name string, types []string, ack int64, wait time.Duration) (FetchResult, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		p, ok := s.protos[name]
		if !ok {
			s.mu.Unlock()
			return FetchResult{}, ErrUnknown
		}
		if acked := min(ack, p.nextSeq); acked > p.acked {
			p.acked = acked
			// A lost write only means the events are delivered once more.
			if err := s.writeMeta(p); err != nil {
				s.log.Error("saving ack", "name", name, "err", err)
			}
		}
		var out []Event
		for _, ev := range p.events {
			if ev.Seq > p.acked && (len(types) == 0 || slices.Contains(types, ev.Type)) {
				out = append(out, ev)
			}
		}
		if len(out) > 0 {
			cursor := out[len(out)-1].Seq
			res := FetchResult{Events: out, Cursor: cursor, UnreadLeft: countUnread(p.events, cursor)}
			s.mu.Unlock()
			return res, nil
		}
		changed := s.changed
		res := FetchResult{Events: []Event{}, Cursor: p.acked, UnreadLeft: countUnread(p.events, p.acked), TimedOut: true}
		s.mu.Unlock()

		select {
		case <-changed:
		case <-deadline.C:
			return res, nil
		case <-ctx.Done():
			return FetchResult{}, ctx.Err()
		}
	}
}

// Events returns every stored event after since, read or not.
func (s *Store) Events(name string, types []string, since int64) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return nil, ErrUnknown
	}
	out := []Event{}
	for _, ev := range p.events {
		if ev.Seq > since && (len(types) == 0 || slices.Contains(types, ev.Type)) {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Message sends a message to every open tab of a prototype and returns how
// many received it.
func (s *Store) Message(name, text string, data json.RawMessage) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return 0, ErrUnknown
	}
	return s.pushLocked(p, Push{Kind: "message", Data: map[string]any{"message": text, "data": data}}), nil
}

// Subscribe streams the pushes of a prototype until cancel is called or the
// prototype is removed (the channel is then closed).
func (s *Store) Subscribe(name string) (<-chan Push, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return nil, nil, ErrUnknown
	}
	ch := make(chan Push, 32)
	p.subs[ch] = struct{}{}
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := p.subs[ch]; ok {
			delete(p.subs, ch)
			close(ch)
		}
	}
	return ch, cancel, nil
}

// Sweep removes every expired prototype and returns their names.
func (s *Store) Sweep() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var gone []string
	for _, p := range s.protos {
		if !s.expiresLocked(p).After(now) {
			gone = append(gone, p.name)
			s.removeLocked(p, "expired")
		}
	}
	sort.Strings(gone)
	return gone
}

func (s *Store) removeLocked(p *prototype, reason string) {
	s.pushLocked(p, Push{Kind: "gone", Data: map[string]string{"reason": reason}})
	for ch := range p.subs {
		close(ch)
	}
	p.subs = map[chan Push]struct{}{}
	delete(s.protos, p.name)
	if err := s.removeFiles(p.name); err != nil {
		s.log.Error("removing files", "name", p.name, "err", err)
	}
	// Wake waiting fetches so they report the prototype as unknown.
	close(s.changed)
	s.changed = make(chan struct{})
}

// touchLocked records activity. It is written to disk at most once a minute;
// losing the last minute only shortens the lifetime by that much.
func (s *Store) touchLocked(p *prototype) {
	p.lastActivity = s.now()
	if p.lastActivity.Sub(p.savedAt) < time.Minute {
		return
	}
	if err := s.writeMeta(p); err != nil {
		s.log.Error("saving activity", "name", p.name, "err", err)
		return
	}
	p.savedAt = p.lastActivity
}

// pushLocked delivers to every subscriber without blocking; a subscriber that
// does not keep up misses the push.
func (s *Store) pushLocked(p *prototype, push Push) int {
	n := 0
	for ch := range p.subs {
		select {
		case ch <- push:
			n++
		default:
		}
	}
	return n
}

func (s *Store) expiresLocked(p *prototype) time.Time {
	return p.lastActivity.Add(max(s.limits.DefaultTTL, p.keepFor))
}

func (s *Store) infoLocked(p *prototype) Info {
	info := Info{
		Name: p.name, Version: p.version, Bytes: p.bytes(),
		PublishedAt: p.publishedAt.UTC(), LastActivity: p.lastActivity.UTC(), ExpiresAt: s.expiresLocked(p).UTC(),
		Unread: countUnread(p.events, p.acked), Events: len(p.events), Viewers: len(p.subs), Context: cloneMap(p.context),
	}
	if p.keepFor > 0 {
		info.KeepFor = FormatDuration(p.keepFor)
	}
	return info
}

// countUnread counts events after seq that a prototype reported itself.
// Automatically tracked views and clicks are not counted.
func countUnread(events []Event, seq int64) int {
	n := 0
	for _, ev := range events {
		if ev.Seq > seq && !ev.Auto {
			n++
		}
	}
	return n
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b) // never fails, see crypto/rand
	return hex.EncodeToString(b)
}
