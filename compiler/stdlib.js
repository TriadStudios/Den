// den's standard library: the browser built-ins (stored, hash, path,
// route) and the imported Go packages. Like the runtime, an app ships only
// what it uses.

// Go's unicode.IsSpace, which differs from JS's \s.
const $WS = "[\\t-\\r \\x85\\xa0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]+";
function $trimSpace(s) { return s.replace(new RegExp("^" + $WS + "|" + $WS + "$", "g"), ""); }
function $fields(s) { return $sl(s.split(new RegExp($WS)).filter((f) => f)); }
function $trimPrefix(s, p) { return s.startsWith(p) ? s.slice(p.length) : s; }
function $trimSuffix(s, p) { return p && s.endsWith(p) ? s.slice(0, -p.length) : s; }
function $split(s, sep) { return $sl(sep === "" ? [...s] : s.split(sep)); }
function $replaceAll(s, o, n) { return o === "" ? n + [...s].join(n) + (s ? n : "") : s.split(o).join(n); }
// strconv.Atoi returns [n, err]; errors are JS Errors and err.Error() reads
// their message.
function $atoi(s) {
  const bad = (why) => [0, new Error("strconv.Atoi: parsing " + JSON.stringify(s) + ": " + why)];
  if (!/^[+-]?[0-9]+$/.test(s)) return bad("invalid syntax");
  const n = Number(s);
  return Number.isSafeInteger(n) ? [n, null] : bad("value out of range");
}
// stored keeps a signal in localStorage as JSON. dec turns parsed JSON back
// into den values and throws if it does not fit the type, in which case the
// initial value is used. Each write is saved as it is made (Update calls
// set too), not by an effect: an effect belongs to the block around it, and
// an update that wrote the signal and removed that block would be lost.
function $stored(key, init, dec) {
  let v = init;
  try { const j = localStorage.getItem(key); if (j !== null) v = dec(JSON.parse(j)); } catch {}
  const s = $signal(v), set = s.set, put = (v) => { try { localStorage.setItem(key, JSON.stringify(v, $json)); } catch {} };
  s.set = (v) => { put(v); set.call(s, v); };
  put(v);
  return s;
}
function $json(k, v) { return v instanceof Map ? Object.fromEntries(v) : v instanceof $Slice ? $arr(v) : v; }
function $want(v, t) { if (typeof v !== t) throw 0; return v; }
function $list(v) { if (!Array.isArray(v)) throw 0; return v; }
function $obj(v) { if (typeof v !== "object" || !v || Array.isArray(v)) throw 0; return v; }
// hash is a signal holding location.hash, kept in sync both ways. Like
// stored, a write moves location.hash as it is made, not by an effect.
function $hash() {
  const s = $signal(location.hash), set = s.set, f = () => s.set(location.hash);
  s.set = (v) => { if (location.hash !== v) location.hash = v; set.call(s, v); };
  addEventListener("hashchange", f);
  $cleanup(() => removeEventListener("hashchange", f));
  return s;
}
// path is one signal per app, holding location.pathname. Setting it pushes
// a history entry for the URL it is given (the signal keeps only the path).
// Back and forward set it, and so does a click on a link to this origin,
// which is followed here instead of loading a new page. It listens on
// window, so den's handlers on document, and their PreventDefault, go first.
let $pathS = null;
function $path() {
  if ($pathS) return $pathS;
  const s = ($pathS = $signal(location.pathname)), set = s.set;
  s.set = (v) => {
    const u = new URL(v, location.href);
    if (u.href !== location.href) history.pushState(null, "", u.href);
    set.call(s, location.pathname);
  };
  addEventListener("popstate", () => set.call(s, location.pathname));
  addEventListener("click", (e) => {
    let a = e.target, t, u;
    while (a && a.localName !== "a") a = a.parentNode;
    if (!a || e.defaultPrevented || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if ((u = a.getAttribute("href")) == null || a.getAttribute("download") != null || ((t = a.getAttribute("target")) && t !== "_self")) return;
    try { u = new URL(u, location.href); } catch { return; }
    // Other origins, and anchors on this page, are left to the browser.
    if (u.origin !== location.origin || (u.hash && u.pathname + u.search === location.pathname + location.search)) return;
    e.preventDefault();
    s.set(u.href);
    globalThis.scrollTo?.(0, 0);
  });
  return s;
}
// route[T](pattern) is $route(pattern, z): the pattern with its params
// renamed to T's JS field names, and T's zero value. A route holds s, the
// pattern's segments; p, a computed of its params (z itself when the path
// does not match); and ok, whether it matches.
function $route(pat, z) {
  const s = pat.split("/").slice(1), path = $path(), p = $computed(() => $match(s, z, path.get()) ?? z);
  return { s, p, ok: $computed(() => p.get() !== z) };
}
// $match returns the params that pathname v gives segments s, or null when
// it does not match. One trailing slash is ignored and segments are
// decoded. A :x param is a whole non-empty segment (an int parses as
// strconv.Atoi parses), and a catch-all takes the rest of the path, which
// may be empty. Browsers resolve "." and ".." segments away, so the path
// does not match when a decoded :x param is one, or when any "/"-separated
// part of the decoded catch-all value is one: a%2F..%2Fb is the value
// a/../b, which Link writes as the segments a, .. and b.
function $match(s, z, v) {
  const ps = v.split("/").slice(1), o = { ...z }, dot = (p) => p === "." || p === "..";
  if (ps.length > 1 && !ps.at(-1)) ps.pop();
  try {
    for (let i = 0; i < s.length; i++) {
      const c = s[i][0], k = s[i].slice(1);
      if (c === "*") { const r = ps.slice(i).map(decodeURIComponent).join("/"); if (r.split("/").some(dot)) return null; o[k] = r; return o; }
      if (i >= ps.length) return null;
      if (c !== ":") { if (decodeURIComponent(ps[i]) !== s[i]) return null; continue; }
      const d = decodeURIComponent(ps[i]);
      if (!d || dot(d)) return null;
      if (typeof z[k] !== "number") o[k] = d;
      else { const [n, err] = $atoi(d); if (err) return null; o[k] = n; }
    }
  } catch { return null; }
  return ps.length === s.length ? o : null;
}
// $linkTo is Route.Link: the path of route r with params v, each segment
// encoded. A path that would start with // (an empty first param) gets /.
// in front, so it can never be read as a link to another host. A :x param
// that is "", "." or ".." makes a path the route does not match.
function $linkTo(r, v) {
  const u = "/" + r.s.map((s) => (s[0] === ":" ? encodeURIComponent(v[s.slice(1)]) : s[0] === "*" ? v[s.slice(1)].split("/").map(encodeURIComponent).join("/") : encodeURIComponent(s))).join("/");
  return u[1] === "/" ? "/." + u : u;
}
// Motion: enter and exit transitions, layout animation and springs, all
// with the Web Animations API: keyframe objects (and one CSSOM property),
// never CSS text, so they run under style-src 'self'. A transition is
// {k, d}: k the keyframe of the hidden state, d the duration in ms.
// Elements carry theirs as $in and $out, and $lay when they glide to a
// new place in a list. Nothing animates under prefers-reduced-motion, or
// in a browser without element.animate.
function $fade(d) { return { k: { opacity: 0 }, d }; }
function $scale(d) { return { k: { opacity: 0, transform: "scale(.95)" }, d }; }
function $slide(dir, d) { return { k: { opacity: 0, transform: $slides[dir] }, d }; }
const $slides = { left: "translateX(-100%)", right: "translateX(100%)", up: "translateY(-100%)", down: "translateY(100%)" };
function $still() { return !!globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches; }
// $play runs transition s on element m: from the hidden state when m
// enters, and to it (held until m is removed) when m leaves (out). It
// returns the animation, or null when nothing plays.
function $play(m, s, out) {
  if (!s?.k || !m.animate || $still()) return null;
  return m.animate(out ? s.k : { ...s.k, offset: 0 }, { duration: s.d, easing: out ? "ease-in" : "ease-out", fill: out ? "forwards" : "none" });
}
function $enter(ns) { ns.forEach((m) => $play(m, m.$in)); }
// $exit takes nodes ns out of their block. An element with an exit
// transition is kept in out (node -> animation) until the transition ends,
// and then rm removes it; every other node goes at once. A leaving element
// takes no input: it is inert, and has pointer-events: none (SVG elements
// have no inert), focus inside it is dropped, and capture listeners on it
// stop every event type the app has a handler for ($evs, which the
// compiler writes), so none reaches a handler inside it, on the document
// or on the element itself, mouseenter and focus included. A browser that
// runs no animation frames (in a hidden tab) never finishes the animation,
// so a timer ends it too, 100ms after it should have finished; end runs
// once.
function $exit(ns, out, rm = (m) => m.remove()) {
  for (const m of ns) {
    const x = $play(m, m.$out, 1);
    if (!x) { m.remove(); continue; }
    const end = () => { if (out.get(m) !== x) return; out.delete(m); clearTimeout(x.t); x.cancel(); rm(m); };
    m.inert = true;
    m.style.pointerEvents = "none";
    if (m.contains(document.activeElement)) document.activeElement.blur();
    for (const t of $evs) m.addEventListener(t, $halt, true);
    m.addEventListener("focusin", $halt, true);
    out.set(m, x);
    x.onfinish = end;
    x.t = setTimeout(end, m.$out.d + 100);
  }
}
function $halt(e) { e.stopImmediatePropagation(); e.preventDefault(); e.target.blur?.(); }
// $gone removes the nodes still leaving when their block is disposed.
function $gone(out) { out.forEach((x, m) => { clearTimeout(x.t); x.cancel(); m.remove(); }); out.clear(); }
// $ifm is $if for blocks whose branches have enter or exit transitions.
// The branch it leaves is freed at once, so its bindings stop, but its
// elements with exit transitions stay (in out) until they have played,
// and are never part of a branch again. The new branch goes in at once,
// after them, and plays its enter transitions, except on the first run:
// a block that is built with its page or its parent does not animate.
function $ifm(a, b, cond, yes, no) {
  let cur, own;
  const out = new Map();
  $effect(() => {
    const v = !!cond();
    if (v === cur) return;
    const re = cur !== undefined, old = [];
    if (re) {
      $free(own);
      own = null;
      for (let m = a.nextSibling; m !== b; m = m.nextSibling) out.has(m) || old.push(m);
      $exit(old, out);
    }
    cur = v;
    const f = v ? yes : no;
    if (f) {
      let n;
      [n, own] = $scope(f);
      const ns = n.nodeType === 11 ? [...n.childNodes] : [n];
      b.before(n);
      if (re) $enter(ns);
    }
  });
  $cleanup(() => { $free(own); $gone(out); });
}
// $form is $for for lists whose rows have transitions or animate={layout()}.
// Rows whose keys are gone are freed and kept from $reconcile, so it never
// removes them: they play their exit transitions and leave on their own.
// Rows with new keys play their enter transitions (not on the first run),
// and elements with $lay glide from where they were to where the change
// put them, and again when leaving rows go.
function $form(a, b, list, key, row, ui, hs) {
  let rows = [], ran = false;
  const out = new Map(), rm = (m) => { const p = $where(rows); m.remove(); $glide(p, rows); };
  $effect(() => {
    const items = list();
    // $scope runs the rest untracked, and makes this effect the owner of
    // the rows it builds.
    $scope(() => {
      const ix = items.$k, now = new Map();
      items.forEach((v, n) => { const i = ix ? ix[n] : n, k = key(v, i); now.has(k) || now.set(k, [v, i]); });
      const keep = rows.filter((r) => now.has(r.k));
      const gone = rows.filter((r) => !now.has(r.k)), had = new Set(keep.map((r) => r.k)), p = $where(keep);
      // When $reconcile can reuse no row it clears the whole list at once,
      // leaving nodes too. Building the first row again here, in its
      // place, gives it one to reuse. It is built before anything is freed,
      // so if building it throws, the rows are as they were.
      if ((gone.length || out.size) && keep.length && !keep.some((r) => { const [v, i] = now.get(r.k); return Object.is(v, r.v) && (!ui || i === r.i); })) {
        const r = keep[0], [v, i] = now.get(r.k), [f, d] = $scope(() => row(v, i)), g = f.nodeType === 11;
        keep[0] = { k: r.k, v, i, d, p: -1, h: hs, a: g ? f.firstChild : f, b: g ? f.lastChild : f };
        if (hs) f.$r = keep[0];
        r.a.before(f);
        $free(r.d);
        $nodes(r).forEach((m) => m.remove());
      }
      gone.forEach((r) => $free(r.d));
      const ns = gone.flatMap($nodes);
      // If $reconcile throws, it has freed the rows it built and moved no
      // nodes, so the rows kept are the list, and the gone ones still leave.
      rows = keep;
      try { rows = $reconcile(a, b, keep, items, key, row, ui, hs); } finally { $exit(ns, out, rm); }
      if (ran) rows.forEach((r) => had.has(r.k) || $enter($nodes(r)));
      $glide(p, rows);
      ran = true;
    });
  });
  $cleanup(() => { rows.forEach((r) => $free(r.d)); $gone(out); });
}
// $where returns where the elements with $lay in rows rs are, by row key.
function $where(rs) {
  const p = new Map();
  if (!$still()) for (const r of rs) {
    const ms = $nodes(r).filter((m) => m.$lay);
    if (ms.length) p.set(r.k, ms.map((m) => m.getBoundingClientRect()));
  }
  return p;
}
// $glide moves the elements with $lay in rows rs from where p says they
// were to where they are now, starting them translated back by the
// difference. A glide still running is replaced by one from where the
// element is seen now.
function $glide(p, rs) {
  if (p.size) for (const r of rs) {
    const was = p.get(r.k);
    if (was) $nodes(r).filter((m) => m.$lay).forEach((m, i) => {
      if (!was[i] || !m.animate) return;
      m.$f?.cancel();
      const n = m.getBoundingClientRect(), x = was[i].left - n.left, y = was[i].top - n.top;
      if (x || y) m.$f = m.animate({ transform: `translate(${x}px, ${y}px)`, offset: 0 }, { duration: 200, easing: "ease-out" });
    });
  }
}
// $spring is spring(target): a signal that follows f() with a damped
// spring (stiffness 170, damping 26, mass 1), stepped once a frame only
// while it moves. It starts at its target, and jumps to each new target
// under reduced motion. It stops with the scope that made it.
function $spring(f) {
  const s = $signal(0);
  let x, v = 0, to, id = 0, t;
  const step = (now) => {
    for (let d, r = Math.min(now - (t ?? now - 16), 64) / 1e3; r > 0; r -= d) {
      d = Math.min(r, 0.004);
      v += (170 * (to - x) - 26 * v) * d;
      x += v * d;
    }
    t = now;
    if (Math.abs(to - x) > 1e-3 || Math.abs(v) > 1e-3) id = requestAnimationFrame(step);
    else { x = to; v = id = 0; t = undefined; }
    s.set(x);
  };
  $effect(() => {
    to = f();
    if (x !== undefined && globalThis.requestAnimationFrame && !$still()) id ||= requestAnimationFrame(step);
    else { if (id) cancelAnimationFrame(id); v = id = 0; t = undefined; s.set((x = to)); }
  });
  $cleanup(() => { if (id) cancelAnimationFrame(id); });
  return s;
}
