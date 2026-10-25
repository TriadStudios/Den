// den runtime: fine-grained signals, ownership scopes and safe DOM helpers.
// It never uses innerHTML, eval or inline handlers, so pages run under a
// strict CSP with Trusted Types enforced. The compiler ships only the
// top-level declarations an app references, so keep one per line start.
// The runtime is held to 3 KB gzipped, which is why hot code uses short
// property names; the comments say what each one is.

// Signals, computed values and effects form a push-pull graph. Writing a
// signal marks the nodes that read it DIRTY and everything further down
// CHECK, and queues the effects it reaches. A computed value recomputes only
// when it is read and an input really changed, and queued effects run once
// per flush after every write has landed, so they never see a half-updated
// graph.
let $listener = null;
let $owner = null;
let $queue = [];
// A node: f its function (none for a signal), v its value, e whether it is
// an effect, s its state (0 clean, 1 check, 2 dirty), d the nodes it read,
// o the nodes that read it, x set once it is disposed.
class $Node {
  constructor(f, v, e) { this.f = f; this.v = v; this.e = e; this.s = f ? 2 : 0; this.d = this.o = null; }
  get() {
    if ($listener) ($listener.d ??= new Set()).add(this);
    if (this.f) $fresh(this);
    return this.v;
  }
  set(v) {
    if (Object.is(v, this.v) && !$mutable(v)) return;
    this.v = v;
    this.o?.forEach((n) => $stale(n, 2));
  }
  update(f) { this.set(f(this.v)); }
}
// Maps and slices change in place, so setting one always notifies.
function $mutable(v) { return v instanceof Map || v?.m$ === 1; }
function $stale(n, s) {
  if (n.s >= s) return;
  if (!n.s && n.e && $queue.push(n) === 1) queueMicrotask($flush);
  n.s = s;
  n.o?.forEach((m) => $stale(m, 1));
}
function $fresh(n) {
  if (n.s === 1 && n.d) for (const d of n.d) { if (d.f) $fresh(d); if (n.s === 2) break; }
  if (n.s === 2) $update(n);
  n.s = 0;
}
function $update(n) {
  const old = n.d, l = $listener, v = n.v;
  n.d = null;
  $listener = n;
  try { n.v = n.f(); } finally { $listener = l; }
  old?.forEach((d) => n.d?.has(d) || d.o?.delete(n));
  if (!n.x) n.d?.forEach((d) => (d.o ??= new Set()).add(n));
  n.s = 0;
  if (!n.e && (!Object.is(v, n.v) || $mutable(n.v))) n.o?.forEach((m) => { m.s = 2; });
}
function $flush() {
  for (let i = 0; $queue.length; i++) {
    if (i > 1000) { $queue = []; console.error("den: update loop did not settle"); break; }
    const q = $queue;
    $queue = [];
    for (const e of q) if (!e.x) $fresh(e);
  }
}
function $signal(v) { return new $Node(null, v, false); }
function $computed(f) { const c = new $Node(f, undefined, false); $cleanup(c); return c; }
// Effects belong to the scope that created them and stop when it is disposed.
function $effect(f) { const e = new $Node(f, undefined, true); $cleanup(e); $update(e); return e; }
function $dispose(n) { n.x = true; n.d?.forEach((d) => d.o?.delete(n)); n.d = null; }
// A scope holds cleanup functions and the nodes to dispose.
function $cleanup(f) { $owner?.push(f); }
// $scope runs f untracked in a new ownership scope: [result, dispose].
function $scope(f) {
  const o = $owner, l = $listener, own = [];
  $owner = own;
  $listener = null;
  try { return [f(), () => { for (const d of own.splice(0)) d.f ? $dispose(d) : d(); }]; } finally { $owner = o; $listener = l; }
}
// $url lets through only http, https, mailto, tel and relative URLs. The
// compiler wraps every dynamic URL attribute (href, src, ...) in it.
function $url(v) {
  const s = String(v).replace(/[\t\n\r]/g, "").replace(/^[\u0000- ]+/, "");
  const m = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(s);
  return !m || /^(https?|mailto|tel)$/i.test(m[1]) ? s : "about:blank#den-blocked-url";
}
function $h(tag) { return document.createElement(tag); }
function $hs(tag) { return document.createElementNS("http://www.w3.org/2000/svg", tag); }
function $frag() { return document.createDocumentFragment(); }
function $cm() { return document.createComment(""); }
// $tpl builds a template once and returns a function that clones it.
function $tpl(build) { let t; return () => (t ??= build()).cloneNode(true); }
function $set(el, name, v) {
  if (name === "value") { v = String(v ?? ""); if (el.value !== v) el.value = v; return; }
  if (name === "checked" || name === "selected") { el[name] = !!v; return; }
  if (typeof v === "boolean" && name.startsWith("aria-")) v = String(v);
  if (v === false || v == null) el.removeAttribute(name);
  else el.setAttribute(name, v === true ? "" : v);
}
function $attr(el, name, f) { let last = $attr; $effect(() => { const v = f(); if (v !== last) $set(el, name, (last = v)); }); }
function $text(t, f) { $effect(() => { const v = String(f()); if (t.data !== v) t.data = v; }); }
// Bubbling events (the compiler decides which) use one listener per type on
// the document, and the handler is stored on the element.
const $delegated = new Set();
function $on(el, type, f) {
  if (!$delegated.has(type)) { $delegated.add(type); document.addEventListener(type, $dispatch); }
  el["$" + type] = f;
}
function $dispatch(e) {
  for (let n = e.target; n && !e.cancelBubble; n = n.parentNode) n["$" + e.type]?.(e);
}
// Control flow keeps its content between two comment anchors. Nested blocks
// only ever change nodes between their own anchors, so a block's first and
// last node stay put and the block can be moved or removed as one range.
function $clear(a, b) {
  const p = a.parentNode;
  if (p.firstChild === a && p.lastChild === b) { p.textContent = ""; p.append(a, b); }
  else while (a.nextSibling !== b) a.nextSibling.remove();
}
function $if(a, b, cond, yes, no) {
  let cur, dispose;
  $effect(() => {
    const v = !!cond();
    if (v === cur) return;
    cur = v;
    if (dispose) { dispose(); $clear(a, b); }
    const f = v ? yes : no;
    dispose = null;
    if (f) { let n; [n, dispose] = $scope(f); b.before(n); }
  });
  $cleanup(() => dispose?.());
}
// $for renders a keyed list. A row is kept while its key and item are the
// same (and its index, when the row uses it); otherwise it is rebuilt. The
// diff skips the common prefix and suffix, then moves the rows in between
// with a longest-increasing-subsequence diff, so only rows that actually
// moved touch the DOM.
function $for(a, b, list, key, row, useIndex) {
  let rows = [];
  $effect(() => {
    const items = list(), l = $listener;
    $listener = null;
    try { rows = $reconcile(a, b, rows, items, key, row, useIndex); } finally { $listener = l; }
  });
  $cleanup(() => rows.forEach((r) => r.d()));
}
// A row: k its key, v its item, i its index, a and b its first and last
// node, d its dispose function, p its position in the previous list.
function $reconcile(start, end, old, items, key, row, useIndex) {
  const ix = items.$k, seen = new Set(), its = [], idx = [], keys = [];
  items.forEach((v, n) => {
    const i = ix ? ix[n] : n, k = key(v, i);
    if (seen.has(k)) return console.error("den: duplicate key in list, row skipped:", k);
    seen.add(k); its.push(v); idx.push(i); keys.push(k);
  });
  const n = its.length, out = Array(n), byKey = new Map();
  const same = (r, j) => r.k === keys[j] && Object.is(r.v, its[j]) && (!useIndex || r.i === idx[j]);
  let s = 0, oe = old.length, ne = n, reused = 0;
  while (s < oe && s < ne && same(old[s], s)) out[s] = old[s++];
  while (oe > s && ne > s && same(old[oe - 1], ne - 1)) out[--ne] = old[--oe];
  if (s === oe && s === ne) return out;
  let at = ne < n ? out[ne].a : end;
  for (let j = s; j < oe; j++) { old[j].p = j; byKey.set(old[j].k, old[j]); }
  for (let j = s; j < ne; j++) {
    let r = byKey.get(keys[j]);
    if (r && same(r, j)) { byKey.delete(keys[j]); reused++; }
    else {
      const [f, d] = $scope(() => row(its[j], idx[j])), frag = f.nodeType === 11;
      if (frag && !f.firstChild) f.append($cm());
      r = { k: keys[j], v: its[j], i: idx[j], d, p: -1, a: frag ? f.firstChild : f, b: frag ? f.lastChild : f };
    }
    out[j] = r;
  }
  if (!reused && !s && oe && oe === old.length) { old.forEach((r) => r.d()); $clear(start, end); }
  else byKey.forEach((r) => { r.d(); $nodes(r).forEach((m) => m.remove()); });
  const keep = $lis(out.slice(s, ne).map((r) => r.p));
  if (!keep.size) {
    const f = $frag();
    for (let j = s; j < ne; j++) f.append(...$nodes(out[j]));
    at.before(f);
  } else for (let j = ne - 1; j >= s; j--) {
    if (!keep.has(j - s)) at.before(...$nodes(out[j]));
    at = out[j].a;
  }
  return out;
}
function $nodes(r) {
  const out = [r.a];
  for (let m = r.a; m !== r.b;) out.push((m = m.nextSibling));
  return out;
}
// $lis returns the indexes of a longest increasing run of old positions;
// those rows stay where they are. Negative positions are new rows.
function $lis(a) {
  const prev = [], tails = [], keep = new Set();
  a.forEach((v, i) => {
    if (v < 0) return;
    let lo = 0, hi = tails.length;
    while (lo < hi) { const m = (lo + hi) >> 1; if (a[tails[m]] < v) lo = m + 1; else hi = m; }
    prev[i] = lo ? tails[lo - 1] : -1;
    tails[lo] = i;
  });
  for (let i = tails.length ? tails.at(-1) : -1; i >= 0; i = prev[i]) keep.add(i);
  return keep;
}
// A Go slice is a window onto a shared backing array, as in Go: a the
// array, o the offset, n the length, c the capacity. append grows in place
// while there is capacity, and slices that share an array see each other's
// writes. nil is null.
class $Slice {
  constructor(a, o, n, c) { this.a = a; this.o = o; this.n = n; this.c = c; }
  get m$() { return 1; }
  toJSON() { return $arr(this); }
}
function $sl(a) { return new $Slice(a, 0, a.length, a.length); }
function $arr(s) { return !s ? [] : !s.o && s.n === s.a.length ? s.a : s.a.slice(s.o, s.o + s.n); }
function $slen(s) { return s ? s.n : 0; }
function $scap(s) { return s ? s.c : 0; }
function $panic(msg) { throw new Error("den: " + msg); }
function $at(s, i) { if (!(i >= 0 && i < $slen(s))) $panic("index out of range"); return s.a[s.o + i]; }
function $put(s, i, v) { if (!(i >= 0 && i < $slen(s))) $panic("index out of range"); s.a[s.o + i] = v; }
function $slice(s, lo, hi = $slen(s), max = $scap(s)) {
  if (lo < 0 || hi < lo || max < hi || max > $scap(s)) $panic("slice bounds out of range");
  return s && new $Slice(s.a, s.o + lo, hi - lo, max - lo);
}
function $append(s, vs) {
  const n = $slen(s), m = n + vs.length;
  if (s && m <= s.c) { vs.forEach((v, i) => { s.a[s.o + n + i] = v; }); return new $Slice(s.a, s.o, m, s.c); }
  return vs.length ? new $Slice($arr(s).concat(vs), 0, m, Math.max(m, 2 * $scap(s))) : s;
}
function $make(n, c, zero) { return new $Slice(Array(n).fill(zero), 0, n, c); }
function $copy(d, s) { const t = $arr(s).slice(0, $slen(d)); t.forEach((v, i) => { d.a[d.o + i] = v; }); return t.length; }
function $fill(s, zero) { for (let i = 0; i < $slen(s); i++) s.a[s.o + i] = zero; }
function $entries(m) { const a = [...m.values()]; a.$k = [...m.keys()]; return a; }
function $seq(n) { return Array.from({ length: n }, (_, i) => i); }
// $eq is Go == for structs and arrays.
function $eq(a, b) {
  if (a === b || typeof a !== "object" || !a || !b) return a === b;
  for (const k in a) if (!$eq(a[k], b[k])) return false;
  return true;
}
// $len is Go len for strings: the length in UTF-8 bytes.
function $len(s) { let n = 0; for (const c of s) { const p = c.codePointAt(0); n += p < 0x80 ? 1 : p < 0x800 ? 2 : p < 0x10000 ? 3 : 4; } return n; }
function $mount(component, root) { root.replaceChildren($scope(component)[0]); }
