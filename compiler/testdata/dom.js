// A minimal DOM for running compiled apps under node in tests. It implements
// only what the den runtime uses, and counts structural changes so tests can
// check that keyed lists move rows instead of rebuilding them.
"use strict";
let domOps = 0;
// With focusEvents set, blur() and removing the focused element (or one
// holding it) fire focusout on it, as Chromium does.
let focusEvents = false;
class Node {
  constructor() { this.parentNode = null; this.childNodes = []; }
  get firstChild() { return this.childNodes[0] ?? null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] ?? null; }
  get nextSibling() { return this.#sibling(1); }
  get previousSibling() { return this.#sibling(-1); }
  #sibling(d) {
    if (!this.parentNode) return null;
    const s = this.parentNode.childNodes;
    return s[s.indexOf(this) + d] ?? null;
  }
  #insert(nodes, at) {
    const list = [];
    for (const n of nodes) {
      const node = typeof n === "string" ? new Text(n) : n;
      if (node instanceof DocumentFragment) list.push(...node.childNodes.splice(0));
      else list.push(node);
    }
    for (const n of list) {
      if (n.parentNode) n.parentNode.childNodes.splice(n.parentNode.childNodes.indexOf(n), 1);
      n.parentNode = this;
      domOps++;
    }
    if (at === null) this.childNodes.push(...list);
    else this.childNodes.splice(this.childNodes.indexOf(at), 0, ...list);
  }
  append(...nodes) { this.#insert(nodes, null); }
  before(...nodes) { this.parentNode.#insert(nodes.filter((n) => n !== this), this); }
  remove() {
    if (!this.parentNode) return;
    const a = document.activeElement, lost = focusEvents && a && this.contains(a);
    const s = this.parentNode.childNodes;
    s.splice(s.indexOf(this), 1);
    this.parentNode = null;
    domOps++;
    if (lost) { document.activeElement = null; a.dispatch("focusout"); }
  }
  replaceChildren(...nodes) { for (const c of [...this.childNodes]) c.remove(); this.append(...nodes); }
  replaceWith(...nodes) { const p = this.parentNode; p.#insert(nodes.filter((n) => n !== this), this); this.remove(); }
  cloneNode(deep) {
    const c = this.shallow();
    if (deep) for (const k of this.childNodes) c.append(k.cloneNode(true));
    return c;
  }
  get textContent() { return this.childNodes.map((c) => c.textContent).join(""); }
  set textContent(v) { for (const c of [...this.childNodes]) c.remove(); if (v) this.append(String(v)); }
  get ownerDocument() { return document; }
  contains(n) { for (; n; n = n.parentNode) if (n === this) return true; return false; }
}
class Text extends Node {
  constructor(s) { super(); this.data = String(s); }
  get nodeType() { return 3; }
  get textContent() { return this.data; }
  shallow() { return new Text(this.data); }
}
class Comment extends Node {
  constructor(s) { super(); this.data = String(s); }
  get nodeType() { return 8; }
  get textContent() { return ""; }
  shallow() { return new Comment(this.data); }
}
class DocumentFragment extends Node {
  get nodeType() { return 11; }
  shallow() { return new DocumentFragment(); }
}
class Element extends Node {
  constructor(tag, ns) {
    super();
    this.tagName = tag;
    this.namespaceURI = ns;
    this.attributes = new Map();
    this.listeners = {};
    this.captures = {};
    this.value = "";
    this.checked = false;
    this.inert = false;
    this.style = {};
  }
  get nodeType() { return 1; }
  get localName() { return this.tagName; }
  shallow() { const e = make(this.tagName, this.namespaceURI); e.attributes = new Map(this.attributes); return e; }
  setAttribute(k, v) { this.attributes.set(k, String(v)); }
  getAttribute(k) { return this.attributes.get(k) ?? null; }
  removeAttribute(k) { this.attributes.delete(k); }
  addEventListener(type, fn, capture) { ((capture ? this.captures : this.listeners)[type] ??= []).push(fn); }
  // focus records whether the element was in the document (under the
  // app's root) when it was focused.
  focus() { document.activeElement = this; this.focusedInDoc = root.contains(this); }
  blur() { if (document.activeElement === this) { document.activeElement = null; if (focusEvents) this.dispatch("focusout"); } }
  // Only inputs and textareas have select, as in browsers; it and
  // scrollIntoView count their calls.
  get select() { return this.tagName === "input" || this.tagName === "textarea" ? () => { this.selects = (this.selects ?? 0) + 1; } : undefined; }
  scrollIntoView() { this.scrolls = (this.scrolls ?? 0) + 1; }
  // animate records each animation in anims; finish() and cancel() end it.
  animate(k, o) { const a = new Animation(this, k, o); anims.push(a); return a; }
  // An element is where rect says, or else in a column: 10px down for each
  // element before it among its parent's children.
  getBoundingClientRect() {
    if (this.rect) return this.rect;
    const s = this.parentNode?.childNodes.filter((c) => c instanceof Element) ?? [];
    return { left: 0, top: 10 * Math.max(s.indexOf(this), 0), width: 100, height: 10 };
  }
  // dispatch runs an event's capture listeners from the top element down
  // to this one, then bubbles it from this element to the document and
  // then the window. extra overrides fields such as button or ctrlKey.
  dispatch(type, extra = {}) {
    const ev = {
      type, target: this, button: 0, ctrlKey: false, metaKey: false, shiftKey: false, altKey: false,
      defaultPrevented: false, cancelBubble: false, stopped: false, preventDefault() { this.defaultPrevented = true; },
      stopPropagation() { this.cancelBubble = true; }, stopImmediatePropagation() { this.cancelBubble = this.stopped = true; }, ...extra,
    };
    const path = [], call = (fns) => { for (const fn of fns ?? []) if (!ev.stopped) fn(ev); };
    for (let n = this; n; n = n.parentNode) path.push(n);
    for (let i = path.length - 1; i >= 0 && !ev.cancelBubble; i--) call(path[i].captures?.[type]);
    for (let i = 0; i < path.length && !ev.cancelBubble; i++) call(path[i].listeners?.[type]);
    if (!ev.cancelBubble) call(docListeners[type]);
    if (!ev.cancelBubble) call(winListeners[type]);
    return ev;
  }
}
// <option> and <select>, for bind: an option's value is the one set, else
// its value attribute, else its text. A select's value is its selected
// option's: the first option until one is chosen. Setting it selects the
// first option with that value, or none (value "") if no option has it.
class Option extends Element {
  constructor(t, ns) { super(t, ns); this.v = undefined; this.selected = false; }
  get value() { return this.v ?? this.getAttribute("value") ?? this.textContent; }
  set value(v) { this.v = String(v); }
}
class Select extends Element {
  constructor(t, ns) { super(t, ns); this.none = false; }
  get value() { const o = findAll("option", this); return (o.find((x) => x.selected) ?? (this.none ? null : o[0]))?.value ?? ""; }
  set value(v) {
    let hit = false;
    for (const o of findAll("option", this)) o.selected = !hit && (hit = o.value === String(v));
    this.none = !hit;
  }
}
function make(t, ns) { return t === "option" ? new Option(t, ns) : t === "select" ? new Select(t, ns) : new Element(t, ns); }
// An Animation as element.animate returns it: el, its keyframes k and
// options o. finish() runs onfinish; a cancelled one never finishes.
let anims = [];
class Animation {
  constructor(el, k, o) { this.el = el; this.k = k; this.o = o; this.playState = "running"; this.onfinish = null; }
  cancel() { if (this.playState === "running") this.playState = "idle"; }
  finish() { if (this.playState !== "running") return; this.playState = "finished"; this.onfinish?.(); }
}
const root = new Element("div", "html");
const docListeners = {};
globalThis.document = {
  addEventListener: (type, fn) => (docListeners[type] ??= []).push(fn),
  createElement: (t) => make(t, "html"),
  createElementNS: (ns, t) => make(t, ns),
  createTextNode: (s) => new Text(s),
  createComment: (s) => new Comment(s),
  createDocumentFragment: () => new DocumentFragment(),
  getElementById: (id) => (id === "app" ? root : null),
};

// localStorage, location, history and window events.
const storage = new Map();
globalThis.localStorage = { getItem: (k) => storage.get(k) ?? null, setItem: (k, v) => storage.set(k, String(v)) };
const winListeners = {};
globalThis.addEventListener = (t, f) => (winListeners[t] ??= []).push(f);
globalThis.removeEventListener = (t, f) => { winListeners[t] = (winListeners[t] ?? []).filter((g) => g !== f); };
// The page is at url; entries is the session history, entry the current one.
let url = new URL("http://localhost/");
const entries = [url.href];
let entry = 0;
globalThis.location = {
  get href() { return url.href; },
  get origin() { return url.origin; },
  get pathname() { return url.pathname; },
  get search() { return url.search; },
  get hash() { return url.hash; },
  set hash(v) {
    v = String(v);
    if (v && v[0] !== "#") v = "#" + v;
    if (v === "#") v = "";
    if (v === url.hash) return;
    url.hash = v;
    setTimeout(() => (winListeners.hashchange ?? []).forEach((f) => f()), 0);
  },
};
// pushState and replaceState change the URL without any event, and refuse
// another origin, as browsers do.
function moveTo(u) {
  if (u === undefined || u === null) return;
  const next = new URL(u, url.href);
  if (next.origin !== url.origin) throw new Error("SecurityError: " + next.href + " is another origin");
  url = next;
}
globalThis.history = {
  pushState(state, unused, u) { moveTo(u); entries.splice(++entry, Infinity, url.href); },
  replaceState(state, unused, u) { moveTo(u); entries[entry] = url.href; },
};
let scrolls = 0;
globalThis.scrollTo = () => { scrolls++; };
// matchMedia answers prefers-reduced-motion from reduced. Animation frames
// run only on frame(), which moves the clock (now) on by ms.
let reduced = false;
globalThis.matchMedia = (q) => ({ matches: q === "(prefers-reduced-motion: reduce)" && reduced });
const rafs = new Map();
let rafID = 0, now = 0;
globalThis.requestAnimationFrame = (f) => { rafs.set(++rafID, f); return rafID; };
globalThis.cancelAnimationFrame = (id) => { rafs.delete(id); };
// fetch answers as the test says. Each call is kept in fetches as {url,
// opts, signal, done}, and its promise settles when the test calls
// reply(f, status, body) or fail(f, message); an abort before that rejects
// it with an AbortError, as fetch does. The response's text() resolves
// whatever happens after, so a test can have a response land after its
// request was aborted. With serve set to a function of a URL returning
// [status, body], every request is answered so, after a tick (a real one).
const fetches = [];
let serve = null;
const statusTexts = { 200: "OK", 404: "Not Found", 500: "Internal Server Error" };
globalThis.fetch = (u, opts = {}) => new Promise((resolve, reject) => {
  const f = { url: String(u), opts, signal: opts.signal, done: false, resolve, reject };
  fetches.push(f);
  opts.signal?.addEventListener("abort", () => { if (!f.done) { f.done = true; reject(new DOMException("This operation was aborted", "AbortError")); } });
  if (serve) realTimeout(() => reply(f, ...serve(f.url)), 0); // not a timer fakeTimers holds back
});
function reply(f, status, body = "") {
  if (f.done) return;
  f.done = true;
  f.resolve({ ok: status >= 200 && status < 300, status, statusText: statusTexts[status] ?? "", text: async () => body });
}
function fail(f, message) { if (!f.done) { f.done = true; f.reject(new TypeError(message)); } }
// inFlight lists the URLs of the requests not settled yet.
function inFlight() { return fetches.filter((f) => !f.done).map((f) => f.url); }

// reportError is how the runtime reports an error it caught (an effect
// that threw); here it logs the message, so tests see it.
globalThis.reportError = (e) => console.log("reportError:", e?.message ?? e);
// Timers: once fakeTimers(wall) is called, the app's setTimeout,
// setInterval, clearTimeout and clearInterval run on a fake clock (clock,
// in ms from 0), performance.now() reads it, and Date.now() is wall plus
// it. advance(ms) moves the clock on, running each timer that falls due in
// order, with the microtasks it queues; pending() counts the timers set.
// tick() and settle() still wait on node's own event loop.
const realTimeout = setTimeout, realImmediate = setImmediate;
let clock = 0, timerID = 0;
const timers = new Map();
function fakeTimers(wall = 0) {
  const add = (f, ms, every) => {
    ms = Math.max(Number(ms) || 0, 0);
    timers.set(++timerID, { at: clock + ms, f, every: every ? Math.max(ms, 1) : 0 });
    return timerID;
  };
  globalThis.setTimeout = (f, ms) => add(f, ms, false);
  globalThis.setInterval = (f, ms) => add(f, ms, true);
  globalThis.clearTimeout = globalThis.clearInterval = (id) => { timers.delete(id); };
  Object.defineProperty(globalThis, "performance", { value: { timeOrigin: wall, now: () => clock }, configurable: true, writable: true });
  Date.now = () => wall + clock;
}
function pending() { return timers.size; }
async function advance(ms) {
  // Microtasks queued before now run before any timer, as in browsers.
  await settle();
  const end = clock + ms;
  for (;;) {
    let id = 0, t = null;
    for (const [i, u] of timers) if (u.at <= end && (!t || u.at < t.at)) { id = i; t = u; }
    if (!t) break;
    clock = t.at;
    if (t.every) t.at += t.every;
    else timers.delete(id);
    t.f();
    await settle();
  }
  clock = end;
  await settle();
}

// ---- helpers for test scripts ----------------------------------------------
function html(n = root) {
  if (n instanceof Text) return n.data.replace(/&/g, "&amp;").replace(/</g, "&lt;");
  if (n instanceof Comment) return "";
  const inner = n.childNodes.map((c) => html(c)).join("");
  if (n === root) return inner;
  const attrs = [...n.attributes].map(([k, v]) => ` ${k}="${v}"`).join("");
  return `<${n.tagName}${attrs}>${inner}</${n.tagName}>`;
}
function findAll(tag, n = root, out = []) {
  for (const c of n.childNodes) {
    if (c instanceof Element) {
      if (c.tagName === tag) out.push(c);
      findAll(tag, c, out);
    }
  }
  return out;
}
function find(tag, text) {
  const el = findAll(tag).find((e) => text === undefined || e.textContent === text);
  if (!el) throw new Error(`no <${tag}>${text ?? ""}`);
  return el;
}
function click(el) { el.dispatch("click"); }
// back and forward move through the session history as the browser's
// buttons do, firing popstate; visit adds an entry and fires popstate as if
// the browser had moved to it.
function traverse(n) {
  if (!entries[entry + n]) throw new Error("no history entry " + (entry + n));
  entry += n;
  url = new URL(entries[entry]);
  (winListeners.popstate ?? []).forEach((f) => f({ type: "popstate" }));
}
function back() { traverse(-1); }
function forward() { traverse(1); }
function visit(u) { history.pushState(null, "", u); traverse(0); }
function key(el, k) { el.dispatch("keydown", { key: k }); }
function check(el, on) { el.checked = on; el.dispatch("change"); }
function input(el, value) { el.value = value; el.dispatch("input"); }
// choose picks the option with value in a select; leave moves focus out of
// el, which fires focusout.
function choose(el, value) { el.value = value; el.dispatch("change"); }
function leave(el) { el.blur(); el.dispatch("focusout"); }
function tick() { return new Promise((r) => realTimeout(r, 0)); }
// settle waits until the microtasks queued so far, and those they queue,
// have run: den's flushes and onMount callbacks.
function settle() { return new Promise((r) => realImmediate(r)); }
function ops() { const n = domOps; domOps = 0; return n; }
function log(...a) { console.log(...a); }
// running lists the animations still running, as tag:text; finishAll
// finishes them.
function running() { return anims.filter((a) => a.playState === "running").map((a) => a.el.tagName + ":" + a.el.textContent); }
function finishAll() { anims.filter((a) => a.playState === "running").forEach((a) => a.finish()); }
function frame(ms = 16) { now += ms; const fs = [...rafs.values()]; rafs.clear(); fs.forEach((f) => f(now)); }
// frames runs frames until none is asked for, at most n, and returns how
// many ran.
function frames(n = 1000) { let i = 0; for (; rafs.size && i < n; i++) frame(); return i; }
