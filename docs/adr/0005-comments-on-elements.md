# 0005: Comments on marked elements

- Status: proposed
- Date: 2026-10-30

## Context

Today a person can only answer a prototype through what the agent built into it: buttons that call `hub.emit`. Feedback on a detail ("this step is missing", "too much text here") needs a free-text field that every agent rebuilds in every prototype, and the agent has no way to answer at that place.

Reviewing a document or a layout works better when a person can comment on a specific element and the agent answers right there. Proposed for v0.2:

- **Markup only.** `<html data-comments>` turns the feature on. vitrine then injects `comments.js` next to `hub.js`. Every element with `data-c="<id>"` gets a bookmark at its edge. Tapping it opens the threads below the element. The agent writes no JS for this.
- **Stable ids.** `data-c` stays the same across versions, so threads stay attached. Threads of ids that disappeared are listed at the end of the page.
- **Page events:**
  - `comment {thread, target, label, text, screen}`
  - `resolve {thread, target, label, open_question}`: the person closed a thread. `open_question` says that a question from the agent in it was still unanswered.
- **New MCP tools:**
  - `reply {name, thread | target, text, state}` with the state `answered`, `question` or `done`. `done` closes the thread. With `target` instead of `thread`, the agent starts a thread itself.
  - `set_status {name, text}`: what the agent is doing, shown in the page until it listens again.
  - `point_to {name, target, text}`: scrolls open tabs to an element and highlights it.
- **Agent presence.** The page shows "Agent is listening", "Agent is reading …" or the status text. vitrine knows the first two without the agent's help: a waiting fetch means it listens, and a fetch that handed out events means it reads.
- **A new version reloads at the same scroll position,** and open threads stay open.

This touches several earlier decisions:

- **[ADR 0002](0002-authentication.md):** the event token "permits only `POST /api/events/<this prototype>`". But a page must read the threads after a reload, and other tabs must see new comments.
- **[ADR 0003](0003-storage.md):** threads must survive a daemon restart like everything else. The state directory is still not an archive.
- **Inbox (v0.1):** agent replies must not count as unread or come back to the agent. No ack rule may change.
- **[ADR 0004](0004-platforms-and-distribution.md) and agent clients:** the feature must not depend on one client's background mechanism.
- **MCP `instructions`:** every session receives them, including sessions that never build a prototype. They are already about 25 lines long.

## Decision

### 1. Reading threads: a snapshot on the hub stream

When `hub.js` connects to `GET /api/events/stream?hub=1&token=…`, vitrine already sends the current version. It now also sends `event: threads` with every `comment`, `reply` and `resolve` event of this prototype. New ones follow as `event: thread`. The stream reconnects when a tab becomes visible again, so the snapshot also covers missed updates.

- The event token stays the only credential of a page, and it reaches only its own prototype. **No new endpoint, no CORS.**
- The event token now permits reading its threads. The accepted gap in ADR 0002 extends accordingly: whoever can load the page can read its comments, not only write into its inbox.
- Paused tabs (beyond `MaxTabs`) get no snapshot. They are paused anyway, and they resume with the snapshot.

### 2. Storage: replies are events, stored acknowledged

- Comments, resolves and replies are ordinary events in `events.jsonl`. **The file format does not change**, and no new file is added.
- `reply` events are written by the agent and are marked acknowledged at once. Fetch never returns them, and they never count as unread. `list_events` still shows them.
- Each reply has the event id. The page shows each id only once, even when the reply arrives both as a push and in a snapshot.
- Threads live and expire with their prototype. Decisions belong in the repository, as before.
- When an inbox is full, acknowledged events are dropped first ([ADR 0003](0003-storage.md)). Old replies can then disappear from a thread. We accept this: it takes 10,000 events on one prototype.
- Presence (`listening`, `reading`, the status text) is memory only. It describes the running process and is empty after a restart.

### 3. Reserved event type: `reply`

A page cannot send events of type `reply`: vitrine refuses them with `invalid_type`. Otherwise any page with the event token could fake an agent answer. `comment` and `resolve` remain open for pages, because they are what pages send. A `comment` event without `thread` and `target` is not a thread and does not appear in the snapshot. Pages from v0.1 that already send `comment` with their own data keep working.

### 4. Agent manual: a `guide` tool

- A new tool `guide {topic}` returns a short how-to for one topic, and the list of topics without one.
- The topic `comments` holds the markup, the events, the tools and the loop: read the comment, act, reply, acknowledge, listen again.
- The `instructions` get one line: "For comments on elements, call guide comments." The other rules stay as they are in v0.1.
- `reply`, `set_status` and `point_to` keep one-line descriptions that refer to `guide comments`.
- A tool works in every MCP client, including clients that do not show `instructions`.

### 5. Presence for every way of listening

`wait_for_events`, the background `GET /api/events/next` and an agent's `GET /api/events/stream` all count as listening. Between two `wait_for_events` calls there is a short gap. The page therefore shows "listening" for a few more seconds after a fetch ends, so the label does not flicker.

### 6. Platforms and language

- `comments.js` runs in the browser only and has no platform-specific code.
- The layout works from phone width up, because remote mode can be used from a phone.
- The UI text is in English. Agents write replies in the language of the person.
- vitrine renders all comment and reply text as text, never as HTML.
- vitrine does not log comment or reply text.

## Consequences

- People can give feedback on a detail without the agent building a form, and the agent can answer, ask back and point at a place.
- v0.1 clients, prototypes and inbox semantics keep working. New: a field `agent` in `get_prototype` and `list_prototypes`, the push kinds `threads`, `thread`, `reply`, `point` and `agent`, and the reserved type `reply`.
- The event token can now read a little: the threads of its own prototype.
- Sessions without a prototype receive one more line of `instructions`, not a whole manual.
- A comment is text from a person, and it reaches the agent's context, just like any other event. The guide tells the agent to treat it as a request to evaluate, not as an instruction to follow blindly.
- Tested with Claude Code. Other clients use the same tools, but are untested until the manual test matrix covers them.

## Alternatives considered

- **Read threads with the agent token from the page.** Rejected. The page must never hold the agent token (ADR 0002).
- **A new read endpoint `GET /api/threads/<prototype>` with the event token.** Possible, with the same access as the snapshot. It adds an API route and a fetch on every page load, and the stream must still carry live updates. The snapshot does both on one connection.
- **Embed the threads in the served HTML.** Rejected. A tab that was hidden or paused would not see newer comments without a reload.
- **Store replies as unread automatic events.** Rejected. Fetch returns automatic events, so agents would get their own replies back.
- **A separate `threads.json` per prototype.** Rejected. A second write path for the same data, and the events already hold everything.
- **Prefix the types (`vitrine.comment`).** Rejected for the page types: `comment` is already the example type in the v0.1 instructions. Only `reply` needs protection.
- **Shrink `instructions` to three lines, and move publish and listen into `guide` too.** Postponed. It changes what every v0.1 client receives. This should be a separate decision once more clients are tested.
- **MCP prompts or resources instead of a `guide` tool.** Rejected. Fewer clients support them, and an agent cannot fetch them on its own in every client.
- **A general chat with the agent.** Rejected. Comments refer to an element. What the agent is doing only appears in the status line.
