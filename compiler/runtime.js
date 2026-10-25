// den runtime: fine-grained signals + safe DOM helpers.
// It never uses innerHTML, eval or inline handlers, so pages run under a
// strict CSP with Trusted Types enforced.
let $listener = null;
const $pending = new Set();
let $scheduled = false;
function $schedule(e) {
  $pending.add(e);
  if (!$scheduled) { $scheduled = true; queueMicrotask($flush); }
}
function $flush() {
  let rounds = 0;
  while ($pending.size) {
    if (++rounds > 1000) { $pending.clear(); console.error("den: update loop did not settle"); break; }
    const batch = [...$pending];
    $pending.clear();
    for (const e of batch) e.run();
  }
  $scheduled = false;
}
function $signal(value) {
  const subs = new Set();
  const s = {
    get() { if ($listener) { subs.add($listener); $listener.deps.add(subs); } return value; },
    set(v) { if (Object.is(v, value)) return; value = v; for (const e of [...subs]) $schedule(e); },
    update(f) { s.set(f(value)); },
  };
  return s;
}
function $effect(fn) {
  const e = {
    deps: new Set(),
    run() {
      for (const d of e.deps) d.delete(e);
      e.deps.clear();
      const prev = $listener;
      $listener = e;
      try { fn(); } finally { $listener = prev; }
    },
  };
  e.run();
  return e;
}
function $computed(fn) {
  const s = $signal(undefined);
  $effect(() => s.set(fn()));
  return { get: s.get };
}
const $URL_ATTRS = new Set(["href", "src", "action", "formaction", "poster", "cite", "xlink:href"]);
function $safeURL(v) {
  const s = String(v).replace(/[\t\n\r]/g, "").replace(/^[\u0000- ]+/, "");
  const m = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(s);
  if (!m) return s;
  const scheme = m[1].toLowerCase();
  return scheme === "http" || scheme === "https" || scheme === "mailto" || scheme === "tel" ? s : "about:blank#den-blocked-url";
}
function $h(tag) { return document.createElement(tag); }
function $hs(tag) { return document.createElementNS("http://www.w3.org/2000/svg", tag); }
function $set(el, name, v) {
  if (name === "value") { const s = String(v ?? ""); if (el.value !== s) el.value = s; return; }
  if (name === "checked" || name === "selected") { el[name] = !!v; return; }
  if (v === false || v == null) { el.removeAttribute(name); return; }
  v = v === true ? "" : String(v);
  if ($URL_ATTRS.has(name)) v = $safeURL(v);
  el.setAttribute(name, v);
}
function $attr(el, name, fn) { $effect(() => $set(el, name, fn())); }
function $text(parent, fn) {
  const t = document.createTextNode("");
  parent.append(t);
  $effect(() => { const v = String(fn()); if (t.data !== v) t.data = v; });
}
function $on(el, type, fn) { el.addEventListener(type, fn); }
function $mount(component, root) { root.replaceChildren(component()); }
