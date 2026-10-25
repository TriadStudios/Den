// den runtime: fine-grained signals, ownership scopes and safe DOM helpers.
// It never uses innerHTML, eval or inline handlers, so pages run under a
// strict CSP with Trusted Types enforced. The compiler ships only the
// top-level declarations an app references, so keep one per line start.
let $listener = null;
let $owner = null;
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
// Effects belong to the scope that created them and stop when it is disposed.
function $effect(fn) {
  const e = {
    deps: new Set(),
    run() {
      if (e.dead) return;
      $unsub(e);
      const prev = $listener;
      $listener = e;
      try { fn(); } finally { $listener = prev; }
    },
  };
  $cleanup(() => { e.dead = true; $unsub(e); });
  e.run();
  return e;
}
function $unsub(e) { for (const d of e.deps) d.delete(e); e.deps.clear(); }
function $cleanup(fn) { if ($owner) $owner.push(fn); }
// $scope runs fn untracked in a new ownership scope: [result, dispose].
function $scope(fn) {
  const o = $owner, l = $listener, own = [];
  $owner = own;
  $listener = null;
  try { return [fn(), () => { for (const d of own.splice(0)) d(); }]; } finally { $owner = o; $listener = l; }
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
  const next = new Map(), order = [];
  for (let i = 0; i < items.length; i++) {
    const it = items[i], k = key(it, i);
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
