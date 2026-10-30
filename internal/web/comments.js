// comments.js: comments on marked elements. vitrine adds this script next to
// hub.js when <html> carries data-comments
// (docs/adr/0005-comments-on-elements.md).
//
//   <section data-c="flow" data-c-label="Flow">…</section>
//
// Every element with data-c gets a bookmark. Tapping it opens the threads
// below the element; the person writes a comment, the agent answers with the
// MCP tool reply. Events to the agent:
//   comment {thread, target, label, text, screen}
//   resolve {thread, target, label, open_question}
// From vitrine (hub.js forwards them): threads (all on connect), thread (one
// new comment, resolve or reply), point and agent.
//
// The UI text is English. A prototype sets its own as JSON in the attribute,
// e.g. <html data-comments='{"send":"…","listening":"…"}'>;
// the keys are those of TEXT below.
// Optional: an element with data-c-status shows the agent status; without
// one a small badge sits in the bottom left corner.
(() => {
  "use strict";
  const hub = window.hub;
  if (!hub) return;
  const KEY = `vitrine-c:${hub.name}`;

  const TEXT = {
    open: "open", question: "question for you", answered: "answered", done: "done",
    you: "You", agent: "Agent", sending: "sending", notSent: "not sent",
    comments: "Comments on", close: "Close", reply: "Reply", resolve: "Resolve",
    send: "Send comment", sendNew: "Send new comment",
    placeholder: "What should the agent know here?", replyPlaceholder: "Answer the agent",
    noAgent: "No agent is listening", listening: "Agent is listening", reading: "Agent is reading …",
    newReply: "Reply on", lookHere: "Have a look at this.", seen: "Got it",
    orphans: "Comments on removed parts", loaded: "Loaded version",
  };
  try {
    const own = document.documentElement.dataset.comments || "";
    if (own.trim().startsWith("{")) {
      for (const [k, v] of Object.entries(JSON.parse(own))) if (k in TEXT && typeof v === "string") TEXT[k] = v;
    }
  } catch (e) { console.warn("vitrine: data-comments is not a JSON object of texts", e); }
  const LABEL = { open: TEXT.open, question: TEXT.question, answered: TEXT.answered, done: TEXT.done };
  const ICON = {
    pen: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13.5 6.5l4 4"/></svg>',
    close: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>',
    eye: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/></svg>',
  };
  // Elements that cannot hold the bookmark or the threads.
  const NO_CHILDREN = new Set(["IMG", "INPUT", "TEXTAREA", "SELECT", "VIDEO", "AUDIO", "CANVAS", "IFRAME", "BR", "HR"]);
  const NO_BLOCK = new Set(["TABLE", "THEAD", "TBODY", "TFOOT", "TR", "UL", "OL", "DL"]);

  const css = `
.vc-band,.vc-panel,.vc-status,.vc-note,.vc-orphans{font:16px/1.45 system-ui,-apple-system,"Segoe UI",Ubuntu,sans-serif;color:#1e2a36;text-align:left;letter-spacing:normal;text-transform:none}
.vc-band svg,.vc-panel svg,.vc-note svg{width:18px;height:18px;fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round;flex:none}
.vc-band{position:absolute;top:0;right:-44px;z-index:5;width:34px;min-height:50px;margin:0;padding:8px 0 14px;border:0;border-radius:4px 4px 0 0;background:#e9edf1;color:#5b6772;display:grid;justify-items:center;align-content:start;font-weight:700;font-size:14px;cursor:pointer;clip-path:polygon(0 0,100% 0,100% 100%,50% calc(100% - 9px),0 100%)}
.vc-band.vc-inside{right:4px;top:-2px;width:28px;min-height:40px;padding:6px 0 12px;font-size:13px}
.vc-band.vc-inside svg{width:15px;height:15px}
.vc-band.vc-has{background:#1b6b57;color:#fff}
.vc-band.vc-question{background:#b86a00;color:#fff}
.vc-band.vc-done{background:#9aa4ad;color:#fff}
.vc-band.vc-new{animation:vc-pulse 1s 2}
.vc-open{background-color:#e3f1ec!important;box-shadow:0 0 0 8px #e3f1ec!important;border-radius:8px}
.vc-panel{display:block;box-sizing:border-box;width:auto;margin:12px 0;padding:4px 14px 14px;background:#fff;border:2px solid #1b6b57;border-radius:12px;grid-column:1/-1;flex-basis:100%}
.vc-panel *,.vc-note *{box-sizing:border-box}
.vc-head{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:6px 0 4px;font-weight:700;font-size:15px}
.vc-x{min-width:44px;min-height:44px;border:0;background:none;color:inherit;display:grid;place-items:center;border-radius:22px;cursor:pointer}
.vc-thread{display:grid;gap:6px;padding:10px 0;border-top:1px solid #d9dee3}
.vc-head+.vc-thread{border-top:0}
.vc-state{justify-self:start;font-size:13px;font-weight:600;padding:2px 8px;border-radius:10px;background:#f2f5f8}
.vc-state.vc-s-open{background:#e8ebfb;color:#3346b8}
.vc-state.vc-s-question{background:#fff1dc;color:#b86a00}
.vc-state.vc-s-answered{background:#e3f1ec;color:#1b6b57}
.vc-state.vc-s-done{color:#5b6772}
.vc-msg{padding:8px 11px;border-radius:12px;max-width:92%;font-size:16px;white-space:pre-wrap;overflow-wrap:anywhere}
.vc-msg small{display:block;font-size:12px;opacity:.75;margin-bottom:2px}
.vc-msg.vc-user{background:#1e2a36;color:#fff;justify-self:end;border-bottom-right-radius:4px}
.vc-msg.vc-agent{background:#f2f5f8;justify-self:start;border-bottom-left-radius:4px}
.vc-msg.vc-ask{background:#fff1dc;box-shadow:inset 3px 0 0 #b86a00}
.vc-msg.vc-pending{opacity:.6}
.vc-msg.vc-failed{outline:2px solid #c0392b}
.vc-form{display:grid;gap:8px;margin:0}
.vc-form textarea{display:block;width:100%;min-height:72px;margin:0;resize:vertical;padding:10px 12px;border:1.5px solid #d9dee3;border-radius:10px;background:#fff;color:#1e2a36;font:16px/1.4 inherit;font-family:inherit}
.vc-row{display:flex;gap:8px;flex-wrap:wrap;justify-content:flex-end}
.vc-btn{display:inline-flex;align-items:center;min-height:40px;margin:0;padding:0 14px;border-radius:20px;border:1.5px solid #1e2a36;background:#fff;color:#1e2a36;font:600 15px/1 inherit;font-family:inherit;cursor:pointer}
.vc-btn.vc-primary{background:#1e2a36;color:#fff}
.vc-status{position:fixed;left:12px;bottom:calc(12px + env(safe-area-inset-bottom,0px));z-index:2147483000;display:flex;align-items:center;gap:8px;max-width:calc(100vw - 24px);padding:6px 12px;border-radius:16px;background:rgba(255,255,255,.95);box-shadow:0 2px 10px rgba(30,42,54,.18);font-size:14px}
[data-c-status] .vc-status{position:static;box-shadow:none;background:none;padding:0}
.vc-status span:last-child{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.vc-dot{width:10px;height:10px;border-radius:50%;background:#9aa4ad;flex:none}
.vc-listening .vc-dot{background:#2e9d5b;box-shadow:0 0 0 4px rgba(46,157,91,.18)}
.vc-busy .vc-dot{background:#b86a00;animation:vc-blink 1.2s ease-in-out infinite}
.vc-point{box-shadow:0 0 0 3px #b86a00!important;animation:vc-pulse 1s ease-in-out 3}
.vc-note{display:flex;flex-wrap:wrap;gap:10px;align-items:flex-start;grid-column:1/-1;flex-basis:100%;background:#fff1dc;border-radius:10px;padding:10px 12px;margin:0 0 10px}
.vc-note p{margin:0;flex:1 1 12em;min-width:0}
.vc-orphans{max-width:42rem;margin:24px auto;padding:0 16px}
.vc-orphans h2{font-size:18px;margin:0 0 8px}
.vc-band:focus-visible,.vc-x:focus-visible,.vc-btn:focus-visible,.vc-form textarea:focus-visible{outline:3px solid #3346b8;outline-offset:2px}
@keyframes vc-pulse{50%{box-shadow:0 0 0 8px rgba(184,106,0,.25)}}
@keyframes vc-blink{50%{opacity:.35}}
@media (prefers-reduced-motion:reduce){.vc-band,.vc-point,.vc-busy .vc-dot{animation:none!important}}`;
  document.head.insertAdjacentHTML("beforeend", `<style id="vc-style">${css}</style>`);

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const marked = () => [...document.querySelectorAll("[data-c]")].filter(usable);
  const byId = (id) => document.querySelector(`[data-c="${CSS.escape(id)}"]`);
  const labelOf = (el) => (el.dataset.cLabel || el.querySelector("h1,h2,h3,h4,figcaption,legend,b,strong")?.textContent || el.dataset.c).trim().slice(0, 80);
  const time = (at) => { const d = new Date(at); return isNaN(d) ? "" : d.toLocaleTimeString(document.documentElement.lang || undefined, { hour: "2-digit", minute: "2-digit" }); };
  const warned = new Set();
  function usable(el) {
    if (!NO_CHILDREN.has(el.tagName) && el.namespaceURI === "http://www.w3.org/1999/xhtml") return true;
    if (!warned.has(el)) { warned.add(el); console.warn("vitrine: data-c belongs on a container (div, section, figure, li), not on", el); }
    return false;
  }

  // Drafts and the scroll position survive a reload in sessionStorage. If the
  // browser blocks it, comments work without.
  let storageWarned = false;
  function storage(fn, fallback) {
    try { return fn(sessionStorage); } catch (e) {
      if (!storageWarned) { storageWarned = true; console.warn("vitrine: sessionStorage unavailable, drafts are not kept", e); }
      return fallback;
    }
  }
  const load = (k) => storage((st) => JSON.parse(st.getItem(`${KEY}:${k}`) || "null"), null);
  const save = (k, v) => storage((st) => v === null ? st.removeItem(`${KEY}:${k}`) : st.setItem(`${KEY}:${k}`, JSON.stringify(v)));

  const threads = new Map();
  const drafts = load("drafts") || {};
  let openTarget = null;
  let note = null; // {target, text} from point_to
  let agent = { listening: false };
  let offlineSince = 0;
  const fresh = new Set();

  // ---- Threads ----
  function add(ev) {
    const d = ev.data || {};
    if (!d.thread) return null;
    let t = threads.get(d.thread);
    if (!t) {
      const el = d.target && byId(d.target);
      t = { id: d.thread, target: d.target, label: d.label || (el ? labelOf(el) : d.target), items: [], state: "open" };
      threads.set(d.thread, t);
    }
    if (ev.type === "resolve") { t.state = "done"; return null; }
    if (ev.id && t.items.some((i) => i.id === ev.id)) return null; // in a snapshot and pushed: show once
    if (ev.id && ev.type === "comment") {
      // This tab's own comment, sent before vitrine gave it an id.
      const mine = t.items.find((i) => !i.id && i.from === "user" && i.text === d.text);
      if (mine) { mine.id = ev.id; mine.pending = mine.failed = false; return null; }
    }
    const it = { id: ev.id, from: ev.type === "reply" ? "agent" : "user", text: d.text, at: ev.at, state: d.state, pending: !!ev.pending };
    t.items.push(it);
    t.state = it.from === "user" ? "open" : (d.state || "answered");
    return it;
  }
  const threadsOf = (id) => [...threads.values()].filter((t) => t.target === id);
  const live = (t) => t.state !== "done";
  function targetState(id) {
    const ts = threadsOf(id);
    if (!ts.length) return "";
    if (ts.some((t) => t.state === "question")) return "vc-question";
    return ts.some(live) ? "vc-has" : "vc-done";
  }
  const newThread = () => "t" + Date.now().toString(36) + Math.random().toString(36).slice(2, 5);

  async function send(target, text, threadId) {
    const el = byId(target);
    const data = { thread: threadId || newThread(), target, label: el ? labelOf(el) : target, text, screen: `${innerWidth}x${innerHeight}` };
    const it = add({ type: "comment", data, at: new Date().toISOString(), pending: true });
    render();
    const ok = await hub.emit("comment", data);
    if (it) { it.pending = false; it.failed = !ok; }
    render();
  }
  async function resolve(t) {
    const openQuestion = t.state === "question";
    t.state = "done";
    render();
    await hub.emit("resolve", { thread: t.id, target: t.target, label: t.label, open_question: openQuestion });
  }

  // ---- Markup ----
  function threadHTML(t) {
    const msgs = t.items.map((it) => {
      const cls = ["vc-msg", it.from === "agent" ? "vc-agent" : "vc-user", it.from === "agent" && it.state === "question" ? "vc-ask" : "",
        it.pending ? "vc-pending" : "", it.failed ? "vc-failed" : ""].join(" ");
      const who = it.from === "agent" ? TEXT.agent : TEXT.you;
      const extra = it.pending ? `, ${TEXT.sending}` : it.failed ? `, ${TEXT.notSent}` : "";
      return `<div class="${cls}"><small>${esc(who)} ${time(it.at)}${esc(extra)}</small>${esc(it.text)}</div>`;
    }).join("");
    return `<div class="vc-thread"><span class="vc-state vc-s-${t.state}">${esc(LABEL[t.state] || t.state)}</span>${msgs}${live(t) ? formHTML(`t:${t.id}`, TEXT.reply, t.id) : ""}</div>`;
  }
  function formHTML(key, label, threadId) {
    return `<form class="vc-form" data-vc-key="${esc(key)}"${threadId ? ` data-vc-thread="${esc(threadId)}"` : ""}>
      <textarea data-vc-key="${esc(key)}" rows="2" aria-label="${esc(label)}" placeholder="${esc(threadId ? TEXT.replyPlaceholder : TEXT.placeholder)}">${esc(drafts[key] || "")}</textarea>
      <div class="vc-row">${threadId ? `<button type="button" class="vc-btn" data-vc-done="${esc(threadId)}">${esc(TEXT.resolve)}</button>` : ""}<button type="submit" class="vc-btn vc-primary">${esc(label)}</button></div></form>`;
  }
  function panelHTML(el) {
    const id = el.dataset.c, ts = threadsOf(id);
    return `<div class="vc vc-panel" role="region" aria-label="${esc(TEXT.comments)} ${esc(labelOf(el))}">
      <div class="vc-head"><span>${esc(TEXT.comments)} ${esc(labelOf(el))}</span><button type="button" class="vc-x" data-vc-close aria-label="${esc(TEXT.close)}">${ICON.close}</button></div>
      ${ts.map(threadHTML).join("")}
      <div class="vc-thread">${formHTML(`new:${id}`, ts.length ? TEXT.sendNew : TEXT.send)}</div></div>`;
  }
  function statusHTML() {
    let cls = "", text = TEXT.noAgent;
    if (agent.working) { cls = "vc-busy"; text = agent.working; }
    else if (agent.reading) { cls = "vc-busy"; text = TEXT.reading; }
    else if (agent.listening || Date.now() - offlineSince < 8000) { cls = "vc-listening"; text = TEXT.listening; }
    return `<div class="vc vc-status ${cls}" role="status" aria-live="polite"><span class="vc-dot"></span><span>${esc(text)}</span></div>`;
  }

  // ---- Layout ----
  function innerWidth_(el) {
    const cs = getComputedStyle(el);
    return el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
  }
  // Narrow: shares its row with neighbours, e.g. two variants side by side.
  function narrow(el) {
    const p = el.parentElement;
    return !!p && p !== document.body && el.getBoundingClientRect().width < innerWidth_(p) * 0.9;
  }
  // Threads open inside a wide element, below the whole row for a narrow one.
  function placePanel(el, html) {
    if (!narrow(el) && !NO_BLOCK.has(el.tagName)) return el.insertAdjacentHTML("beforeend", html);
    let row = narrow(el) ? el.parentElement : el;
    while (row.parentElement && row.parentElement !== document.body && narrow(row)) row = row.parentElement;
    row.insertAdjacentHTML("afterend", html);
  }

  function clear() {
    document.querySelectorAll(".vc").forEach((n) => n.remove());
    document.querySelectorAll(".vc-open,.vc-point").forEach((n) => n.classList.remove("vc-open", "vc-point"));
  }

  // A bookmark inside an element covers its right edge, e.g. a quantity or a
  // button. The element gets that much more padding while it carries one;
  // the original inline padding comes back otherwise.
  const RESERVE = 36;
  const pads = new WeakMap();
  function reserve(el, on) {
    if (!pads.has(el)) pads.set(el, { inline: el.style.paddingRight, base: parseFloat(getComputedStyle(el).paddingRight) || 0 });
    const p = pads.get(el);
    el.style.paddingRight = on ? `${p.base + RESERVE}px` : p.inline;
  }
  // A marked element nested in another marked one with (nearly) the same right
  // edge carries its bookmark inside, so the two bookmarks do not overlap.
  function sharesEdge(el, r) {
    const outer = el.parentElement?.closest("[data-c]");
    return !!outer && usable(outer) && outer.getBoundingClientRect().right - r.right < RESERVE + 4;
  }
  // The fixed status badge must not cover the end of the page, e.g. a note
  // from point_to on the last element.
  let bodyPad = null;
  function reserveBottom(badge) {
    if (bodyPad === null) bodyPad = { inline: document.body.style.paddingBottom, base: parseFloat(getComputedStyle(document.body).paddingBottom) || 0 };
    document.body.style.paddingBottom = badge ? `${bodyPad.base + badge.offsetHeight + 24}px` : bodyPad.inline;
  }

  function render() {
    const focus = document.activeElement?.dataset?.vcKey;
    const caret = focus ? document.activeElement.selectionStart : 0;
    clear();
    const vw = document.documentElement.clientWidth;
    for (const el of marked()) {
      const id = el.dataset.c;
      if (getComputedStyle(el).position === "static") el.style.position = "relative";
      const r = el.getBoundingClientRect();
      const inside = narrow(el) || r.right + 44 > vw - 4 || sharesEdge(el, r);
      reserve(el, inside);
      const n = threadsOf(id).filter(live).length;
      const open = openTarget === id;
      el.insertAdjacentHTML("beforeend", `<button type="button" class="vc vc-band ${inside ? "vc-inside" : ""} ${targetState(id)} ${fresh.has(id) ? "vc-new" : ""}" data-vc-band="${esc(id)}" aria-expanded="${open}" aria-label="${esc(TEXT.comments)} ${esc(labelOf(el))}">${n || ICON.pen}</button>`);
      if (open) { el.classList.add("vc-open"); placePanel(el, panelHTML(el)); }
    }
    if (note) {
      const el = byId(note.target);
      if (el) {
        el.classList.add("vc-point");
        const html = `<div class="vc vc-note" role="status">${ICON.eye}<p><b>${esc(TEXT.agent)}:</b> ${esc(note.text)}</p><button type="button" class="vc-btn" data-vc-seen>${esc(TEXT.seen)}</button></div>`;
        // In a narrow element the note would push its row apart: put it below the row.
        if (narrow(el) || NO_BLOCK.has(el.tagName)) placePanel(el, html); else el.insertAdjacentHTML("afterbegin", html);
      }
    }
    const orphans = [...threads.values()].filter((t) => !byId(t.target));
    if (orphans.length) {
      document.body.insertAdjacentHTML("beforeend", `<section class="vc vc-orphans"><h2>${esc(TEXT.orphans)}</h2>${orphans.map((t) => `<p><b>${esc(t.label)}</b></p>${threadHTML(t)}`).join("")}</section>`);
    }
    const slot = document.querySelector("[data-c-status]");
    (slot || document.body).insertAdjacentHTML("beforeend", statusHTML());
    reserveBottom(slot ? null : document.querySelector(".vc-status"));
    if (focus) {
      const ta = document.querySelector(`textarea[data-vc-key="${CSS.escape(focus)}"]`);
      if (ta) { ta.focus({ preventScroll: true }); ta.setSelectionRange(caret, caret); }
    }
  }

  function scrollTo_(id) {
    const el = byId(id);
    if (!el) return;
    const calm = matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollIntoView({ block: "center", behavior: calm ? "auto" : "smooth" });
  }

  // ---- Input ----
  document.addEventListener("input", (e) => {
    const k = e.target.dataset?.vcKey;
    if (k) { drafts[k] = e.target.value; save("drafts", drafts); }
  });
  document.addEventListener("submit", (e) => {
    const f = e.target.closest?.("form.vc-form");
    if (!f) return;
    e.preventDefault();
    const key = f.dataset.vcKey, text = (drafts[key] || "").trim();
    if (!text) return;
    delete drafts[key];
    save("drafts", drafts);
    const thread = f.dataset.vcThread;
    send(thread ? threads.get(thread).target : key.slice(4), text, thread);
  });
  document.addEventListener("click", (e) => {
    const t = e.target;
    const band = t.closest?.("[data-vc-band]");
    if (band) {
      const id = band.dataset.vcBand;
      openTarget = openTarget === id ? null : id;
      fresh.delete(id);
      render();
      if (openTarget) document.querySelector(`textarea[data-vc-key="new:${CSS.escape(id)}"]`)?.focus({ preventScroll: true });
      return;
    }
    if (t.closest?.("[data-vc-close]")) { openTarget = null; return render(); }
    const done = t.closest?.("[data-vc-done]");
    if (done) return resolve(threads.get(done.dataset.vcDone));
    if (t.closest?.("[data-vc-seen]")) { note = null; return render(); }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && openTarget) { openTarget = null; render(); }
  });

  // ---- From the agent ----
  // On every (re)connect: all threads. Comments this tab could not send yet
  // stay, so their text is not lost.
  hub.on("threads", (evs) => {
    const unsent = [...threads.values()].flatMap((t) => t.items.filter((i) => !i.id && i.from === "user").map((i) => ({ t, i })));
    threads.clear();
    evs.forEach(add);
    for (const { t, i } of unsent) {
      const now = threads.get(t.id) || threads.set(t.id, { ...t, items: [], state: "open" }).get(t.id);
      if (!now.items.some((x) => x.from === "user" && x.text === i.text)) { now.items.push(i); now.state = "open"; }
    }
    render();
  });
  hub.on("thread", (ev) => {
    const it = add(ev);
    const d = ev.data || {};
    if (it && it.from === "agent" && openTarget !== d.target) {
      fresh.add(d.target);
      const el = byId(d.target);
      hub.toast(`${TEXT.newReply} ${el ? labelOf(el) : d.target}`);
    }
    render();
  });
  hub.on("point", (d) => { note = { target: d.target, text: d.text || TEXT.lookHere }; render(); scrollTo_(d.target); });
  // Presence changes often. Only the badge is replaced, so the open panel and
  // a comment being typed (IME, autocorrect on phones) stay untouched.
  function renderStatus() {
    const old = document.querySelector(".vc-status");
    if (!old) return render();
    old.insertAdjacentHTML("afterend", statusHTML());
    old.remove();
  }
  hub.on("agent", (a) => {
    if (agent.listening && !a.listening) offlineSince = Date.now();
    agent = a;
    renderStatus();
    // Show "nobody listens" only after a pause: agents re-listen right away.
    if (!a.listening) setTimeout(renderStatus, 8500);
  });

  // When hub.js reloads for a new version (the page has no "version"
  // handler of its own), the page comes back at the same spot with the same
  // threads open.
  hub.on("reload", () => {
    const top = marked().find((e) => e.getBoundingClientRect().bottom > 60);
    save("pos", { id: top?.dataset.c, y: top?.getBoundingClientRect().top, open: openTarget, from: hub.version });
  });
  function restore() {
    const s = load("pos");
    if (!s) return;
    save("pos", null);
    openTarget = s.open;
    render();
    const el = s.id && byId(s.id);
    if (el) window.scrollTo(0, el.getBoundingClientRect().top + scrollY - s.y);
    if (s.from && s.from < hub.version) hub.toast(`${TEXT.loaded} ${hub.version}`);
  }

  // The threads arrive with the stream (hub.on("threads") above).
  render();
  restore();
  // Layout depends on width (bookmark inside or outside).
  let resizing;
  addEventListener("resize", () => { clearTimeout(resizing); resizing = setTimeout(render, 150); });
})();
