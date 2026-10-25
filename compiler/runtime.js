// den runtime: fine-grained signals, ownership scopes and safe DOM helpers.
// It never uses innerHTML, eval or inline handlers, so pages run under a
// strict CSP with Trusted Types enforced. The compiler ships only the
// top-level declarations an app references, so keep one per line start.
// Signals, computed values and effects form a push-pull graph. Writing a
// signal marks its observers DIRTY and everything further down CHECK, and
// queues the effects it reaches. A computed value recomputes only when it is
// read and an input really changed, and queued effects run once per flush
// after every write has landed, so they never see a half-updated graph.
let $listener = null;
let $owner = null;
let $queue = [];
let $scheduled = false;
class $Node {
  constructor(fn, value, effect) {
    this.fn = fn;
    this.value = value;
    this.effect = effect;
    this.state = fn ? 2 : 0; // 0 clean, 1 check, 2 dirty
    this.sources = null;
    this.observers = null;
  }
  get() {
    if ($listener) ($listener.sources ??= new Set()).add(this);
    if (this.fn) $fresh(this);
    return this.value;
  }
  set(v) {
    if (Object.is(v, this.value) && !$mutable(v)) return;
    this.value = v;
    if (this.observers) for (const o of this.observers) $stale(o, 2);
  }
  update(f) { this.set(f(this.value)); }
}
// Maps and slices change in place, so setting one always notifies.
function $mutable(v) { return v instanceof Map || v instanceof $Slice; }
function $stale(n, state) {
  if (n.state >= state) return;
  if (n.state === 0 && n.effect) {
    $queue.push(n);
    if (!$scheduled) { $scheduled = true; queueMicrotask($flush); }
  }
  n.state = state;
  if (n.observers) for (const o of n.observers) $stale(o, 1);
}
function $fresh(n) {
  if (n.state === 1 && n.sources) {
    for (const s of n.sources) {
      if (s.fn) $fresh(s);
      if (n.state === 2) break;
    }
  }
  if (n.state === 2) $update(n);
  else n.state = 0;
}
function $update(n) {
  const old = n.sources, prev = $listener, before = n.value;
  n.sources = null;
  $listener = n;
  try { n.value = n.fn(); } finally { $listener = prev; }
  if (old) for (const s of old) if (!n.sources?.has(s)) s.observers?.delete(n);
  if (n.sources && !n.dead) for (const s of n.sources) (s.observers ??= new Set()).add(n);
  n.state = 0;
  if (!n.effect && n.observers && (!Object.is(before, n.value) || $mutable(n.value))) for (const o of n.observers) o.state = 2;
}
function $flush() {
  for (let rounds = 0; $queue.length; rounds++) {
    if (rounds > 1000) { $queue = []; console.error("den: update loop did not settle"); break; }
    const q = $queue;
    $queue = [];
    for (const e of q) if (!e.dead) $fresh(e);
  }
  $scheduled = false;
}
function $signal(value) { return new $Node(null, value, false); }
function $computed(fn) {
  const c = new $Node(fn, undefined, false);
  $cleanup(() => $dispose(c));
  return c;
}
// Effects belong to the scope that created them and stop when it is disposed.
function $effect(fn) {
  const e = new $Node(fn, undefined, true);
  $cleanup(() => $dispose(e));
  $update(e);
  return e;
}
function $dispose(n) {
  n.dead = true;
  if (n.sources) for (const s of n.sources) s.observers?.delete(n);
  n.sources = null;
}
function $cleanup(fn) { if ($owner) $owner.push(fn); }
// $scope runs fn untracked in a new ownership scope: [result, dispose].
function $scope(fn) {
  const o = $owner, l = $listener, own = [];
  $owner = own;
  $listener = null;
  try { return [fn(), () => { for (const d of own.splice(0)) d(); }]; } finally { $owner = o; $listener = l; }
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
function $frag() { return document.createDocumentFragment(); }
function $set(el, name, v) {
  if (name === "value") { const s = String(v ?? ""); if (el.value !== s) el.value = s; return; }
  if (name === "checked" || name === "selected") { el[name] = !!v; return; }
  if (typeof v === "boolean" && name.startsWith("aria-")) v = String(v);
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
// Control flow keeps its content between two comment anchors. Nested blocks
// only ever change nodes between their own anchors, so a block's first and
// last node stay put and the block can be moved or removed as one range.
function $anchor(parent) { const c = document.createComment(""); parent.append(c); return c; }
function $clear(start, end) { while (start.nextSibling !== end) start.nextSibling.remove(); }
function $if(parent, cond, a, b) {
  const start = $anchor(parent), end = $anchor(parent);
  let cur, dispose;
  $effect(() => {
    const v = !!cond();
    if (v === cur) return;
    cur = v;
    if (dispose) { dispose(); dispose = null; $clear(start, end); }
    const f = v ? a : b;
    if (f) { let frag; [frag, dispose] = $scope(f); end.before(frag); }
  });
  $cleanup(() => dispose && dispose());
}
// $for renders a keyed list. A row is kept while its key and item are the
// same (and its index, when the row uses it); otherwise it is rebuilt. Rows
// that stay are moved with a longest-increasing-subsequence diff, so only
// rows that actually moved touch the DOM.
function $for(parent, list, key, row, useIndex) {
  const start = $anchor(parent), end = $anchor(parent);
  let rows = new Map();
  $effect(() => {
    const items = list();
    const l = $listener;
    $listener = null;
    try { rows = $reconcile(start, end, rows, items, key, row, useIndex); } finally { $listener = l; }
  });
  $cleanup(() => { for (const r of rows.values()) r.dispose(); });
}
function $reconcile(start, end, rows, items, key, row, useIndex) {
  const next = new Map(), order = [], ix = items.$k;
  for (let n = 0; n < items.length; n++) {
    const it = items[n], i = ix ? ix[n] : n, k = key(it, i);
    if (next.has(k)) { console.error("den: duplicate key in list, row skipped:", k); continue; }
    let r = rows.get(k);
    if (r) {
      rows.delete(k);
      if (!Object.is(r.item, it) || (useIndex && r.i !== i)) { $drop(r); r = null; }
    }
    if (!r) {
      const [f, dispose] = $scope(() => row(it, i));
      if (!f.firstChild) f.append(document.createComment(""));
      r = { item: it, first: f.firstChild, last: f.lastChild, dispose, pos: -1 };
    }
    r.i = i;
    next.set(k, r);
    order.push(r);
  }
  if (!order.length) {
    for (const r of rows.values()) r.dispose();
    $clear(start, end);
    return next;
  }
  for (const r of rows.values()) $drop(r);
  const keep = $lis(order.map((r) => r.pos));
  if (!keep.size) {
    const f = $frag();
    for (const r of order) f.append(...$nodes(r));
    end.before(f);
  } else {
    let ref = end;
    for (let j = order.length - 1; j >= 0; j--) {
      const r = order[j];
      if (!keep.has(j)) { if (r.first === r.last) ref.before(r.first); else ref.before(...$nodes(r)); }
      ref = r.first;
    }
  }
  order.forEach((r, j) => { r.pos = j; });
  return next;
}
function $nodes(r) {
  const out = [];
  for (let n = r.first; ; n = n.nextSibling) { out.push(n); if (n === r.last) return out; }
}
function $drop(r) { r.dispose(); for (const n of $nodes(r)) n.remove(); }
// $lis returns the indexes of a longest increasing run of old positions;
// those rows stay where they are. Negative positions are new rows.
function $lis(a) {
  const prev = [], tails = [], keep = new Set();
  for (let i = 0; i < a.length; i++) {
    if (a[i] < 0) continue;
    let lo = 0, hi = tails.length;
    while (lo < hi) { const m = (lo + hi) >> 1; if (a[tails[m]] < a[i]) lo = m + 1; else hi = m; }
    prev[i] = lo ? tails[lo - 1] : -1;
    tails[lo] = i;
  }
  for (let i = tails.length ? tails[tails.length - 1] : -1; i >= 0; i = prev[i]) keep.add(i);
  return keep;
}
// A Go slice is a window onto a shared backing array, as in Go: append
// grows in place while there is capacity, and slices that share an array
// see each other's writes. nil is null.
class $Slice {
  constructor(a, o, n, c) { this.a = a; this.o = o; this.n = n; this.c = c; }
  toJSON() { return $arr(this); }
}
function $sl(a) { return new $Slice(a, 0, a.length, a.length); }
function $arr(s) { return s === null ? [] : s.o === 0 && s.n === s.a.length ? s.a : s.a.slice(s.o, s.o + s.n); }
function $slen(s) { return s === null ? 0 : s.n; }
function $scap(s) { return s === null ? 0 : s.c; }
function $panic(msg) { throw new Error("den: " + msg); }
function $at(s, i) { if (s === null || i < 0 || i >= s.n) $panic("index out of range"); return s.a[s.o + i]; }
function $put(s, i, v) { if (s === null || i < 0 || i >= s.n) $panic("index out of range"); s.a[s.o + i] = v; }
function $slice(s, lo, hi = $slen(s), max = $scap(s)) {
  if (lo < 0 || hi < lo || max < hi || max > $scap(s)) $panic("slice bounds out of range");
  return s === null ? null : new $Slice(s.a, s.o + lo, hi - lo, max - lo);
}
function $append(s, vs) {
  const n = $slen(s), m = n + vs.length;
  if (s !== null && m <= s.c) {
    for (let i = 0; i < vs.length; i++) s.a[s.o + n + i] = vs[i];
    return new $Slice(s.a, s.o, m, s.c);
  }
  if (!vs.length) return s;
  const a = s === null ? [] : s.a.slice(s.o, s.o + n);
  for (const v of vs) a.push(v);
  return new $Slice(a, 0, m, Math.max(m, 2 * $scap(s)));
}
function $make(n, c, zero) { return new $Slice(Array(n).fill(zero), 0, n, c); }
function $copy(d, s) {
  const n = Math.min($slen(d), $slen(s));
  if (n) { const t = s.a.slice(s.o, s.o + n); for (let i = 0; i < n; i++) d.a[d.o + i] = t[i]; }
  return n;
}
function $fill(s, zero) { for (let i = 0; i < $slen(s); i++) s.a[s.o + i] = zero; }
function $entries(m) { const a = [...m.values()]; a.$k = [...m.keys()]; return a; }
function $seq(n) { return Array.from({ length: n }, (_, i) => i); }
// $eq is Go == for structs and arrays.
function $eq(a, b) {
  if (a === b || typeof a !== "object" || a === null || b === null) return a === b;
  for (const k in a) if (!$eq(a[k], b[k])) return false;
  return true;
}
// $len is Go len for strings: the length in UTF-8 bytes.
function $len(s) {
  let n = 0;
  for (const c of s) { const p = c.codePointAt(0); n += p < 0x80 ? 1 : p < 0x800 ? 2 : p < 0x10000 ? 3 : 4; }
  return n;
}
function $mount(component, root) { root.replaceChildren($scope(component)[0]); }
