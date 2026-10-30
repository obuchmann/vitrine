package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Comments on marked elements (docs/adr/0005-comments-on-elements.md). A page
// with comments.js reports "comment" and "resolve" events; the agent answers
// with Reply, says what it is doing with Status and points at an element with
// Point. Replies are events of type "reply", stored acknowledged: the agent
// wrote them, so a fetch never hands them back.

var (
	ErrReservedType  = errors.New(`reserved event type "reply": only the agent answers threads, with the reply tool`)
	ErrInvalidState  = errors.New("invalid state: answered, question or done")
	ErrUnknownThread = errors.New("unknown thread: no comment with this thread id; pass target to start a new thread")
	ErrNoTarget      = errors.New("target is required: the data-c id of the element")
	ErrEmptyText     = errors.New("empty text")
)

// ReplyType is the event type of agent replies. Pages cannot send it.
const ReplyType = "reply"

// ReplyStates are the states an agent can give a thread.
var ReplyStates = []string{"answered", "question", "done"}

// Agent tells the page whether an agent is around. vitrine knows the first
// two without the agent's help: a waiting fetch or an agent stream means it
// listens, a fetch that handed out events means it is reading them.
type Agent struct {
	Listening bool   `json:"listening"`
	Reading   bool   `json:"reading,omitempty"`
	Working   string `json:"working,omitempty" jsonschema:"what the agent said with set_status"`
}

func agentLocked(p *prototype) Agent {
	return Agent{Listening: p.listeners > 0, Reading: p.reading, Working: p.working}
}

// Listen marks an agent as listening until the returned func is called;
// delivered says whether it was handed events. nil means the prototype is
// unknown.
func (s *Store) Listen(name string) func(delivered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return nil
	}
	p.listeners++
	p.reading, p.working = false, ""
	s.pushTabsLocked(p, Push{Kind: "agent", Data: agentLocked(p)})
	return func(delivered bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		p.listeners--
		if delivered {
			p.reading = true
		}
		s.pushTabsLocked(p, Push{Kind: "agent", Data: agentLocked(p)})
	}
}

// isThreadEvent reports whether ev belongs to a comment thread: a reply, or a
// comment or resolve that names its thread and element. Pages may send
// "comment" with their own data; those are not threads.
func isThreadEvent(ev Event) bool {
	switch ev.Type {
	case ReplyType:
		return true
	case "comment", "resolve":
		var d struct{ Thread, Target string }
		return json.Unmarshal(ev.Data, &d) == nil && d.Thread != "" && d.Target != ""
	}
	return false
}

// Threads returns every event of the comment threads of a prototype, oldest
// first.
func (s *Store) Threads(name string) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return nil, ErrUnknown
	}
	return threadsLocked(p), nil
}

func threadsLocked(p *prototype) []Event {
	out := []Event{}
	for _, ev := range p.events {
		if isThreadEvent(ev) {
			out = append(out, ev)
		}
	}
	return out
}

// ReplyInput answers a thread or, with only Target, starts one.
type ReplyInput struct {
	Thread string `json:"thread,omitempty"`
	Target string `json:"target,omitempty"`
	Text   string `json:"text"`
	State  string `json:"state"`
}

// Reply stores the agent's answer as an acknowledged "reply" event and pushes
// it to the open tabs. The page shows each event id only once.
func (s *Store) Reply(name string, in ReplyInput) (Event, error) {
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" {
		return Event{}, ErrEmptyText
	}
	if !slices.Contains(ReplyStates, in.State) {
		return Event{}, ErrInvalidState
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return Event{}, ErrUnknown
	}
	switch {
	case in.Thread != "":
		target := threadTargetLocked(p, in.Thread)
		if target == "" {
			return Event{}, ErrUnknownThread
		}
		in.Target = target
	case in.Target != "":
		in.Thread = fmt.Sprintf("a%d", p.nextSeq+1)
	default:
		return Event{}, errors.New("pass thread to answer a comment, or target to start a thread")
	}
	data, _ := json.Marshal(in)
	ev, err := s.emitLocked(p, ReplyType, data, 0, false)
	if err != nil {
		return Event{}, err
	}
	p.ackedAfter[ev.Seq] = true
	p.compactAcksLocked()
	// A lost write only means the agent gets its reply once more.
	if err := s.writeMeta(p); err != nil {
		s.log.Error("saving reply ack", "name", name, "err", err)
	}
	return ev, nil
}

// threadTargetLocked finds the element of a thread from its events.
func threadTargetLocked(p *prototype, thread string) string {
	for _, ev := range p.events {
		if !isThreadEvent(ev) {
			continue
		}
		var d struct{ Thread, Target string }
		if json.Unmarshal(ev.Data, &d) == nil && d.Thread == thread && d.Target != "" {
			return d.Target
		}
	}
	return ""
}

// Status sets what the agent says it is doing. It shows until the agent
// listens again; an empty text clears it. Returns the open tabs reached.
func (s *Store) Status(name, text string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return 0, ErrUnknown
	}
	p.working, p.reading = strings.TrimSpace(text), false
	return s.pushTabsLocked(p, Push{Kind: "agent", Data: agentLocked(p)}), nil
}

// Point scrolls open tabs to an element and highlights it with a note.
// Returns the open tabs reached.
func (s *Store) Point(name, target, text string) (int, error) {
	if target == "" {
		return 0, ErrNoTarget
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.protos[name]
	if !ok {
		return 0, ErrUnknown
	}
	return s.pushTabsLocked(p, Push{Kind: "point", Data: map[string]string{"target": target, "text": text}}), nil
}
