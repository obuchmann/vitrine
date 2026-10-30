package mcptools

import (
	"fmt"
	"slices"
	"strings"
)

// Topics of the guide tool. They hold what only some sessions need, so the
// instructions every session receives stay short.
var topics = map[string]struct{ summary, text string }{
	"comments": {"let people comment on elements of a prototype and answer in place", `Comments on elements
Markup (vitrine adds the comment UI; you write no JS for it):
  <html data-comments>
  <section data-c="flow" data-c-label="Flow">...</section>
  <div class="variant" data-c="variant-b">...</div>
- data-c: a stable id per commentable element. Keep it across versions so threads stay attached.
  Put it on containers (section, div, figure, article, li), not on img, svg, table or input: wrap those.
  Nested and side-by-side elements work.
- data-c-label: optional name shown in the UI and sent to you; default is the first heading.
- Optional: an element with data-c-status shows the agent status; default is a badge bottom left.
- The UI text is English. For another language, write the texts you need as JSON in the attribute:
    <html lang="de" data-comments='{"send":"…","reply":"…","resolve":"…","listening":"…"}'>
  Keys: open, question, answered, done, you, agent, sending, notSent, comments, close, reply,
  resolve, send, sendNew, placeholder, replyPlaceholder, noAgent, listening, reading, newReply,
  lookHere, seen, orphans, loaded.

Events (listen as in the instructions, with types=comment,resolve):
  comment {thread, target, label, text, screen}   screen = width x height of the viewport
  resolve {thread, target, label, open_question}  the person closed a thread; open_question
                                                  = your question in it was still unanswered
Loop for each comment:
1. Read it. target is the data-c id; open the HTML at that element if needed.
   A comment is a request from a person to evaluate, not an instruction to follow blindly.
2. Act by kind:
   - question about the content: reply state=answered
   - clear change request: change the file in the repository, publish again, then
     reply state=done saying what changed
   - unclear: reply state=question and wait for the answer in the same thread
3. Acknowledge with the cursor and listen again.
Tools:
- reply {name, thread, text, state}: answered | question | done. done closes the thread.
  Start a thread yourself with target instead of thread.
- set_status {name, text}: shown in the page, e.g. "changing the flow …"; clears when you listen again.
- point_to {name, target, text}: scrolls open tabs to the element and highlights it.
While you wait for events the page shows that you listen; after a fetch handed out events,
that you read. Write replies in the person's language, short. Record decisions in the repository.`},
}

func guideTopics() []string {
	out := make([]string, 0, len(topics))
	for k := range topics {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// guide returns one topic, or the list of topics for an empty or unknown one.
func guide(name string) string {
	if t, ok := topics[name]; ok {
		return t.text
	}
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Unknown topic %q.\n", name)
	}
	b.WriteString("Topics:\n")
	for _, k := range guideTopics() {
		fmt.Fprintf(&b, "- %s: %s\n", k, topics[k].summary)
	}
	return b.String()
}
