package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func comment(t *testing.T, s *Store, name, thread, target, text string) Event {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"thread": thread, "target": target, "label": target, "text": text, "screen": "390x844"})
	ev, err := s.Emit(name, "comment", data, 0, false)
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	return ev
}

func TestRepliesAreStoredAcknowledgedAndSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	s := openStore(t, dir, DefaultLimits(), c)
	publish(t, s, "a/b")
	comment(t, s, "a/b", "t1", "flow", "Is step 2 missing?")
	res := fetchNow(t, s, "a/b", 0)
	if len(res.Events) != 1 {
		t.Fatalf("comment not delivered: %+v", res.Events)
	}
	ev, err := s.Reply("a/b", ReplyInput{Thread: "t1", Text: " Added it. ", State: "done"})
	if err != nil {
		t.Fatal(err)
	}
	var d ReplyInput
	json.Unmarshal(ev.Data, &d)
	if ev.Type != ReplyType || d.Target != "flow" || d.Text != "Added it." || d.State != "done" {
		t.Fatalf("reply %+v data %+v", ev, d)
	}
	// The agent processed the comment; its own reply never comes back.
	if res := fetchNow(t, s, "a/b", res.Cursor); len(res.Events) != 0 || res.UnreadLeft != 0 {
		t.Fatalf("after reply: %+v", res)
	}
	// Even a session that never acknowledged the comment only gets the comment.
	r := openStore(t, dir, DefaultLimits(), c)
	if info, _ := r.Get("a/b"); info.Unread != 0 || info.Events != 2 {
		t.Fatalf("after restart: %+v", info)
	}
	if res := fetchNow(t, r, "a/b", 0); len(res.Events) != 0 {
		t.Fatalf("after restart, fetch returns %+v", res.Events)
	}
	threads, err := r.Threads("a/b")
	if err != nil || len(threads) != 2 || threads[0].Type != "comment" || threads[1].ID != ev.ID {
		t.Fatalf("threads after restart: %+v %v", threads, err)
	}
}

func TestReplyBeforeAckKeepsCommentUnread(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	comment(t, s, "a/b", "t1", "flow", "Why?")
	if _, err := s.Reply("a/b", ReplyInput{Thread: "t1", Text: "Because.", State: "answered"}); err != nil {
		t.Fatal(err)
	}
	res := fetchNow(t, s, "a/b", 0)
	if len(res.Events) != 1 || res.Events[0].Type != "comment" {
		t.Fatalf("the unacknowledged comment must stay: %+v", res.Events)
	}
}

func TestReplyValidates(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	comment(t, s, "a/b", "t1", "flow", "?")
	for _, c := range []struct {
		in   ReplyInput
		want error
	}{
		{ReplyInput{Thread: "t1", Text: " ", State: "done"}, ErrEmptyText},
		{ReplyInput{Thread: "t1", Text: "x", State: "closed"}, ErrInvalidState},
		{ReplyInput{Thread: "nope", Text: "x", State: "done"}, ErrUnknownThread},
	} {
		if _, err := s.Reply("a/b", c.in); !errors.Is(err, c.want) {
			t.Errorf("%+v: got %v, want %v", c.in, err, c.want)
		}
	}
	if _, err := s.Reply("a/b", ReplyInput{Text: "x", State: "done"}); !errors.Is(err, ErrNoThread) {
		t.Errorf("reply without thread and target: %v", err)
	}
	if _, err := s.Reply("x/y", ReplyInput{Thread: "t1", Text: "x", State: "done"}); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown prototype: %v", err)
	}
	ev, err := s.Reply("a/b", ReplyInput{Target: "variant-b", Text: "What about B?", State: "question"})
	if err != nil {
		t.Fatal(err)
	}
	var d ReplyInput
	json.Unmarshal(ev.Data, &d)
	if d.Thread == "" || d.Target != "variant-b" {
		t.Fatalf("started thread %+v", d)
	}
	if _, err := s.Reply("a/b", ReplyInput{Thread: d.Thread, Text: "Still there?", State: "question"}); err != nil {
		t.Fatalf("answering the agent's own thread: %v", err)
	}
}

func TestPagesCannotSendReplies(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	if _, err := s.Emit("a/b", ReplyType, json.RawMessage(`{"thread":"t1","target":"flow","text":"fake","state":"done"}`), 0, false); !errors.Is(err, ErrReservedType) {
		t.Fatalf("got %v, want ErrReservedType", err)
	}
}

func TestThreadsLeaveOutOtherComments(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	// A v0.1 page with its own comment format.
	if _, err := s.Emit("a/b", "comment", json.RawMessage(`{"text":"nice"}`), 0, false); err != nil {
		t.Fatal(err)
	}
	emit(t, s, "a/b", "choice", false)
	comment(t, s, "a/b", "t1", "flow", "?")
	if _, err := s.Emit("a/b", "resolve", json.RawMessage(`{"thread":"t1","target":"flow","open_question":false}`), 0, false); err != nil {
		t.Fatal(err)
	}
	threads, _ := s.Threads("a/b")
	if len(threads) != 2 || threads[0].Type != "comment" || threads[1].Type != "resolve" {
		t.Fatalf("threads %+v", threads)
	}
}

func TestThreadPushes(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	tab, cancelTab, _ := s.Subscribe("a/b", true)
	defer cancelTab()
	agent, cancelAgent, _ := s.Subscribe("a/b", false)
	defer cancelAgent()
	comment(t, s, "a/b", "t1", "flow", "?")
	if p := <-tab; p.Kind != "event" {
		t.Fatalf("tab got %s first", p.Kind)
	}
	if p := <-tab; p.Kind != "thread" {
		t.Fatalf("tab got %s, want thread", p.Kind)
	}
	if p := <-agent; p.Kind != "event" {
		t.Fatalf("agent stream got %s, want event", p.Kind)
	}
	<-agent // the thread push, which the web layer drops for agents
	if _, err := s.Reply("a/b", ReplyInput{Thread: "t1", Text: "Yes.", State: "answered"}); err != nil {
		t.Fatal(err)
	}
	if p := <-tab; p.Kind != "thread" {
		t.Fatalf("tab got %s for the reply, want thread", p.Kind)
	}
	if p := <-agent; p.Kind != "thread" {
		t.Fatalf("agent stream got %s for the reply: the agent must not get its reply as an event", p.Kind)
	}
}

func TestPresence(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	tab, cancel, _ := s.Subscribe("a/b", true)
	defer cancel()
	agentOf := func(want string) Agent {
		t.Helper()
		p := <-tab
		if p.Kind != want {
			t.Fatalf("tab got %s, want %s", p.Kind, want)
		}
		if p.Kind != "agent" {
			return Agent{}
		}
		return p.Data.(Agent)
	}

	done := s.Listen("a/b")
	if a := agentOf("agent"); !a.Listening {
		t.Fatalf("listening: %+v", a)
	}
	if info, _ := s.Get("a/b"); !info.Agent.Listening {
		t.Fatalf("info while listening: %+v", info.Agent)
	}
	done(false)
	if a := agentOf("agent"); a.Listening || a.Reading {
		t.Fatalf("after an empty wait: %+v", a)
	}

	emit(t, s, "a/b", "choice", false)
	agentOf("event")
	fetchNow(t, s, "a/b", 0)
	agentOf("agent")
	if a := agentOf("agent"); a.Listening || !a.Reading {
		t.Fatalf("after a fetch with events: %+v", a)
	}
	if n, err := s.Status("a/b", " changing the flow "); err != nil || n != 1 {
		t.Fatalf("status: %d %v", n, err)
	}
	if a := agentOf("agent"); a.Working != "changing the flow" || a.Reading {
		t.Fatalf("status: %+v", a)
	}
	s.Listen("a/b")
	if a := agentOf("agent"); a.Working != "" || !a.Listening {
		t.Fatalf("listening again clears the status: %+v", a)
	}

	if n, err := s.Point("a/b", "flow", "here"); err != nil || n != 1 {
		t.Fatalf("point: %d %v", n, err)
	}
	if p := <-tab; p.Kind != "point" {
		t.Fatalf("tab got %s, want point", p.Kind)
	}
	if _, err := s.Point("a/b", "", "x"); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("point without target: %v", err)
	}
	if s.Listen("x/y") != nil {
		t.Fatal("listening to an unknown prototype")
	}
}

func TestAgentThreadIdsCannotBeClaimedByAPage(t *testing.T) {
	s, _ := newStore(t)
	publish(t, s, "a/b")
	// A page guesses the next ids and claims them for another element.
	for i := 1; i <= 5; i++ {
		comment(t, s, "a/b", fmt.Sprintf("a%d", i), "elsewhere", "claimed")
	}
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		ev, err := s.Reply("a/b", ReplyInput{Target: "flow", Text: "About this?", State: "question"})
		if err != nil {
			t.Fatal(err)
		}
		var d ReplyInput
		json.Unmarshal(ev.Data, &d)
		if seen[d.Thread] || threadTargetLocked(s.protos["a/b"], d.Thread) != "flow" {
			t.Fatalf("thread %q reused or attached elsewhere", d.Thread)
		}
		seen[d.Thread] = true
	}
}
