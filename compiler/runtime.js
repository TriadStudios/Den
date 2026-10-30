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
// A signal: v its value and o the nodes that read it (one node, or a Set
// once there are several).
class $Sig {
  constructor(v) { this.v = v; this.o = null; }
  get() {
    const l = $listener;
    if (l && !$has(l.d, this)) l.d = !l.d ? this : l.d.push ? (l.d.push(this), l.d) : [l.d, this];
    return this.v;
  }
  set(v) {
    if (Object.is(v, this.v) && !$mutable(v)) return;
    this.v = v;
    $each(this.o, (n) => $stale(n, 2));
  }
  update(f) { this.set(f(this.v)); }
}
// A computed value or effect is a signal with f, its function, called with c
// (the list row it belongs to, if any); s its state (0 clean, 1 check, 2
// dirty); d the nodes it read (one node, or an array); n the next entry in
// its scope; p its owner, the effect that made its scope (see $scope); and
// x, set once it is disposed. An effect also has e, a sink called with the
// node when its value changes, and t and k for the sink to write to. Most
// nodes have one reader and read one node, so storing those directly saves
// allocating a collection for each.
class $Node extends $Sig {
  constructor(f, e, t, k, c) { super($Node); this.f = f; this.e = e; this.t = t; this.k = k; this.c = c; this.s = 2; this.d = this.n = null; this.p = $owner?.p; }
  get() { $fresh(this); return super.get(); }
}
function $each(o, f) { o?.forEach ? o.forEach(f) : o && f(o); }
function $has(o, n) { return o === n || !!o?.includes?.(n); }
function $link(d, n) { const o = d.o; d.o = !o || o === n ? n : o.add ? o.add(n) : new Set([o, n]); }
function $unlink(d, n) { d.o === n ? (d.o = null) : d.o?.delete?.(n); }
// Maps and slices change in place, so setting one always notifies.
function $mutable(v) { return v instanceof Map || v instanceof $Slice; }
function $stale(n, s) {
  if (n.s < s) {
    if (!n.s && n.e && $queue.push(n) === 1) queueMicrotask($flush);
    n.s = s;
    $each(n.o, (m) => $stale(m, 1));
  }
}
function $fresh(n) {
  if (n.s === 1) $each(n.d, (d) => n.s === 1 && d.f && $fresh(d));
  if (n.s === 2) $update(n);
  n.s = 0;
}
function $update(n) {
  const old = n.d, l = $listener, v = n.v;
  n.d = null;
  $listener = n;
  try { n.v = n.f(n.c); } finally { $listener = l; }
  $each(old, (d) => $has(n.d, d) || $unlink(d, n));
  if (!n.x) $each(n.d, (d) => $has(old, d) || $link(d, n));
  n.s = 0;
  if (!Object.is(v, n.v) || $mutable(n.v)) n.e ? n.e(n) : $each(n.o, (m) => { m.s = 2; });
}
// $run runs n's owners that are due first, then n unless they disposed it:
// an effect never runs while a block around it still has to re-check, since
// the block may remove it. Each owner runs its own owners first, so they go
// outermost first.
function $run(n) {
  for (let o = n; o = o.p; ) o.s && $run(o);
  n.x || $fresh(n);
}
function $flush() {
  for (let i = 0; $queue.length; i++) {
    if (i > 1000) { $queue = []; console.error("den: update loop"); break; }
    $queue.splice(0).forEach($run);
  }
}
function $signal(v) { return new $Sig(v); }
function $computed(f) { const c = new $Node(f); $cleanup(c); return c; }
// Effects belong to the scope that created them and stop when it is
// disposed. The default sink, Boolean, does nothing.
function $effect(f, e = Boolean, t, k, c) { const n = new $Node(f, e, t, k, c); $cleanup(n); $update(n); return n; }
function $dispose(n) { n.x = true; $each(n.d, (d) => $unlink(d, n)); n.d = null; }
// A scope is a list of nodes and cleanup functions linked through n, so it
// costs no allocation of its own. It is passed around as its newest entry.
function $cleanup(x) { if ($owner) { x.n = $owner.h; $owner.h = x; } }
function $free(x) { for (; x; x = x.n) x.f ? $dispose(x) : x(); }
// $scope runs f untracked in a new scope: [result, scope]. The scope's p,
// the owner of the nodes made in it, is the effect running when $scope is
// called, or else the owner of the enclosing scope. So code that builds
// nodes untracked must do it in a $scope, never by setting $listener to
// null itself, or they get no owner.
function $scope(f) {
  const o = $owner, l = $listener, own = { h: null, p: l || o?.p };
  $owner = own;
  $listener = null;
  try { return [f(), own.h]; } finally { $owner = o; $listener = l; }
}
// $url lets through only http, https, mailto, tel and relative URLs. The
// compiler wraps every dynamic URL attribute (href, src, ...) in it.
function $url(v) {
  const s = String(v).replace(/[\t\n\r]|^[\0- ]+/g, "");
  return !/^[a-z][a-z0-9+.-]*:/i.test(s) || /^(https?|mailto|tel):/i.test(s) ? s : "about:blank#blocked";
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
// Bindings are effects whose sink writes the DOM when the value changes.
function $attr(el, name, f, c) { $effect(f, $attrSink, el, name, c); }
function $attrSink(n) { $set(n.t, n.k, n.v); }
function $text(t, f, c) { $effect(f, $textSink, t, 0, c); }
function $textSink(n) { n.t.data = String(n.v); }
// Bubbling events (the compiler decides which) use one listener per type on
// the document. The handler is stored on the element, or the element has an
// attribute like den-click="0" naming a handler in the table of its list
// row, whose element ($r) holds the row record.
const $delegated = {};
function $listen(type) { $delegated[type] ||= (document.addEventListener(type, $dispatch), 1); }
function $on(el, type, f) { $listen(type); el["$" + type] = f; }
function $dispatch(e) {
  for (let n = e.target, i; n && !e.cancelBubble; n = n.parentNode) {
    n["$" + e.type]?.(e);
    if ((i = n.getAttribute?.("den-" + e.type)) != null) { let r = n; while (!r.$r) r = r.parentNode; r.$r.h[i](e, r.$r.v); }
  }
}
// Control flow keeps its content between two comment anchors. Nested blocks
// only ever change nodes between their own anchors, so a block's first and
// last node stay put and the block can be moved or removed as one range.
function $clear(a, b) {
  const p = a.parentNode;
  if (p.firstChild === a && p.lastChild === b) { p.textContent = ""; p.append(a, b); }
  else while (a.nextSibling !== b) a.nextSibling.remove();
}
// own is the scope of the branch being shown.
function $if(a, b, cond, yes, no) {
  let cur, own;
  $effect(() => {
    const v = !!cond();
    if (v === cur) return;
    if (cur !== undefined) { $free(own); own = null; $clear(a, b); }
    cur = v;
    const f = v ? yes : no;
    if (f) { let n; [n, own] = $scope(f); b.before(n); }
  });
  $cleanup(() => $free(own));
}
// $for renders a keyed list. A row is kept while its key and item are the
// same (and its index, when the row uses it); otherwise it is rebuilt. The
// diff skips the common prefix and suffix, then moves the rows in between
// with a longest-increasing-subsequence diff, so only rows that actually
// moved touch the DOM.
function $for(a, b, list, key, row, ui, hs) {
  let rows = [];
  $effect(() => {
    const items = list();
    rows = $scope(() => $reconcile(a, b, rows, items, key, row, ui, hs))[0];
  });
  $cleanup(() => rows.forEach((r) => $free(r.d)));
}
// A row: k its key, v its item, i its index, a and b its first and last
// node, d its scope, p its position in the previous list, h the list's
// handler table. a and b are the list's anchors, xs the new items. ui says
// whether rows use their index, so a row whose index changed is rebuilt.
// The compiler never makes an empty row.
function $reconcile(a, b, old, xs, key, row, ui, h) {
  const ix = xs.$k, seen = new Set(), its = [], idx = [], keys = [];
  xs.forEach((v, n) => {
    const i = ix ? ix[n] : n, k = key(v, i);
    if (seen.has(k)) return console.error("den: duplicate key", k);
    seen.add(k); its.push(v); idx.push(i); keys.push(k);
  });
  const n = its.length, out = Array(n), byKey = new Map();
  const same = (r, j) => r.k === keys[j] && Object.is(r.v, its[j]) && (!ui || r.i === idx[j]);
  let s = 0, oe = old.length, ne = n, ru = 0;
  while (s < oe && s < ne && same(old[s], s)) out[s] = old[s++];
  while (oe > s && ne > s && same(old[oe - 1], ne - 1)) out[--ne] = old[--oe];
  if (s === oe && s === ne) return out;
  let at = ne < n ? out[ne].a : b;
  for (let j = s; j < oe; j++) { old[j].p = j; byKey.set(old[j].k, old[j]); }
  for (let j = s; j < ne; j++) {
    let r = byKey.get(keys[j]);
    if (r && same(r, j)) { byKey.delete(keys[j]); ru++; }
    else {
      const [f, d] = $scope(() => row(its[j], idx[j])), g = f.nodeType === 11;
      r = { k: keys[j], v: its[j], i: idx[j], d, p: -1, h, a: g ? f.firstChild : f, b: g ? f.lastChild : f };
      if (h) f.$r = r;
    }
    out[j] = r;
  }
  if (!ru && !s && oe && oe === old.length) { old.forEach((r) => $free(r.d)); $clear(a, b); }
  else byKey.forEach((r) => { $free(r.d); $nodes(r).forEach((m) => m.remove()); });
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
}
function $sl(a) { return new $Slice(a, 0, a.length, a.length); }
function $arr(s) { return !s ? [] : !s.o && s.n === s.a.length ? s.a : s.a.slice(s.o, s.o + s.n); }
function $slen(s) { return s ? s.n : 0; }
function $scap(s) { return s ? s.c : 0; }
function $panic(msg) { throw new Error("den: " + msg); }
// $ix bounds-checks index i of slice s and returns its index in s.a.
function $ix(s, i) { if (!(i >= 0 && i < $slen(s))) $panic("index out of range"); return s.o + i; }
function $at(s, i) { return s.a[$ix(s, i)]; }
function $put(s, i, v) { s.a[$ix(s, i)] = v; }
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
function $entries(m) { const a = [...m.values()]; a.$k = [...m.keys()]; return a; }
// $eq is Go == for structs and arrays.
function $eq(a, b) {
  if (a === b || typeof a !== "object" || !a || !b) return a === b;
  for (const k in a) if (!$eq(a[k], b[k])) return false;
  return true;
}
// $len is Go len for strings: the length in UTF-8 bytes.
function $len(s) { return new TextEncoder().encode(s).length; }
function $mount(app, root) { root.replaceChildren($scope(app)[0]); }
