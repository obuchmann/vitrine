// hub.js: vitrine injects this script into every prototype it serves.
//
//   hub.emit(type, data)    report an interaction; the prototype decides what
//                           counts. type: lower case, e.g. "choice", "comment"
//   hub.on("message", fn)   messages an agent sends to open tabs; without a
//                           handler hub.js shows them as a toast
//   hub.on("version", fn)   a new version is available; without a handler
//                           hub.js reloads the page
//   hub.toast(text)         show a short note
//
// Automatic tracking is opt-in on the <html> element:
//   <html data-track="view,click,input">
(() => {
  "use strict";
  const me = document.currentScript;
  const name = me.dataset.name;
  const version = Number(me.dataset.version);
  const token = me.dataset.token;
  const handlers = {};

  function post(type, data, auto) {
    return fetch(`/api/events/${name}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Vitrine": "1", "X-Vitrine-Event-Token": token },
      body: JSON.stringify({ type, data: data === undefined ? null : data, version, auto: !!auto }),
      keepalive: true,
    }).then((r) => {
      if (!r.ok) console.warn("vitrine: event not stored", type, r.status);
      return r.ok;
    }, (e) => {
      console.warn("vitrine: event not sent", type, e);
      return false;
    });
  }

  let toastBox;
  function toast(text) {
    if (!toastBox) {
      toastBox = document.createElement("div");
      toastBox.style.cssText = "position:fixed;left:50%;bottom:16px;transform:translateX(-50%);z-index:2147483647;" +
        "display:grid;gap:6px;max-width:min(90vw,520px);font:14px/1.4 system-ui,sans-serif";
      document.body.appendChild(toastBox);
    }
    const el = document.createElement("div");
    el.textContent = text;
    el.style.cssText = "background:#2f6fde;color:#fff;padding:9px 14px;border-radius:9px;box-shadow:0 4px 16px rgba(0,0,0,.2)";
    toastBox.appendChild(el);
    setTimeout(() => el.remove(), 6000);
  }

  function fire(kind, payload) {
    const list = handlers[kind] || [];
    list.forEach((fn) => { try { fn(payload); } catch (e) { console.error(e); } });
    return list.length > 0;
  }

  window.hub = {
    name,
    version,
    emit: (type, data) => post(type, data, false),
    on(kind, fn) { (handlers[kind] = handlers[kind] || []).push(fn); },
    toast,
  };

  const track = (document.documentElement.dataset.track || "").split(",").map((s) => s.trim()).filter(Boolean);
  const describe = (el) => {
    if (el.id) return `#${el.id}`;
    const attrs = [...el.attributes].filter((a) => a.name.startsWith("data-")).map((a) => `[${a.name}${a.value ? `=${a.value}` : ""}]`);
    return el.tagName.toLowerCase() + attrs.join("");
  };
  if (track.includes("view")) post("view", { path: location.pathname, referrer: document.referrer || null }, true);
  if (track.includes("click")) {
    document.addEventListener("click", (e) => {
      const el = e.target.closest("button,a,summary,[data-hub-click],input[type=checkbox],input[type=radio]");
      if (el) post("click", { target: describe(el), text: (el.innerText || el.value || "").trim().slice(0, 80) }, true);
    }, true);
  }
  if (track.includes("input")) {
    document.addEventListener("change", (e) => {
      const el = e.target;
      if (el.matches("input,textarea,select")) post("input", { target: describe(el), length: String(el.value || "").length }, true);
    }, true);
  }

  if (!window.EventSource) return;
  const es = new EventSource(`/api/events/stream?name=${encodeURIComponent(name)}&hub=1&token=${encodeURIComponent(token)}`);
  es.addEventListener("message", (e) => {
    const d = JSON.parse(e.data);
    if (!fire("message", d)) toast(d.message);
  });
  es.addEventListener("version", (e) => {
    const d = JSON.parse(e.data);
    if (d.version > version && !fire("version", d)) location.reload();
  });
  es.addEventListener("gone", () => {
    es.close();
    toast("This prototype was removed.");
  });
})();
