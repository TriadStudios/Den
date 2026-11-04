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
// Lifecycle: a component that calls onMount, onCleanup, after or every
// gets a record c = $life() first thing when it is built, kept in the
// scope it is built in: s, the functions to run when the component goes
// (its cleanups, and the stops of its timers), run newest first; and x,
// set once it has gone; and p, the owner of the nodes the component makes
// (see $scope). As the oldest entry the component puts in that scope, the
// record goes after everything the component made, as a deferred call
// would. Other stdlib code ties work to a component with $onCleanup(c, f).
function $life() {
  const c = { s: new Set(), x: 0, p: $owner?.p };
  $cleanup(() => { c.x = 1; [...c.s].reverse().forEach((f) => $lifeTry(c, f)); c.s.clear(); });
  return c;
}
// $lifeTry runs f, a callback of component c, as den runs an event
// handler: untracked, and reporting a panic rather than passing it on.
// It runs in a scope of its own that goes with c, so what f makes (a
// computed, a spring) stops with the component, and has the component's
// owner, so it never runs while the block that would take the component
// away is due. A callback that makes nothing adds nothing to the record.
function $lifeTry(c, f) {
  const o = $owner;
  $owner = c;
  try { const h = $scope(f)[1]; h && $onCleanup(c, () => $free(h)); } catch (e) { reportError(e); } finally { $owner = o; }
}
// $onCleanup runs f when the component goes, or at once if it has gone.
// It returns a function that takes f back, for work that ends first.
function $onCleanup(c, f) {
  const g = () => f();
  c.x ? $lifeTry(c, g) : c.s.add(g);
  return () => c.s.delete(g);
}
// $onMount runs f once the build that made the component, which puts it
// in the document, is over, unless the component has gone by then.
function $onMount(c, f) { queueMicrotask(() => c.x || $lifeTry(c, f)); }
// $after runs f once after d ns, or with every set, every d ns, until the
// stop it returns is called or the component goes. A timer that has fired
// once and for all takes itself off the record. On a component that has
// gone, the timer is stopped as it is made.
function $after(c, d, f, every) {
  if (every && !(d > 0)) $panic("non-positive interval for every");
  if (d > 2147483647e6) $panic("duration too long");
  const t = (every ? setInterval : setTimeout)(() => { every || stop(); $lifeTry(c, f); }, d / 1e6),
    off = $onCleanup(c, () => clearTimeout(t)), stop = () => { clearTimeout(t); off(); };
  return stop;
}
function $every(c, d, f) { return $after(c, d, f, 1); }
// ref={r}: r holds the element from when it is made until the scope that
// made it goes, unless another element has taken r over by then.
function $setRef(e, r) { r.e = e; $cleanup(() => { if (r.e === e) r.e = null; }); }
// time: a Time is {w, m}, the wall clock (ms since the epoch) and the
// monotonic clock (performance.now(), when there is one), as Go keeps
// both; the zero Time is null, Go's year 1 UTC. Sub uses the monotonic
// readings of two Times that have them, and Hour and the like the wall
// clock, which follows the system clock even after the computer sleeps.
// A Duration is a number of ns, which Sub saturates as Go does.
function $tnow() { return { w: Date.now(), m: globalThis.performance?.now() }; }
function $tms(t) { return t ? t.w : -62135596800000; }
function $tsub(t, u) {
  const d = t?.m != null && u?.m != null ? t.m - u.m : $tms(t) - $tms(u);
  return Math.min(Math.max(Math.round(d * 1e6), -9223372036854775808), 9223372036854775807);
}
function $tsince(t) { return $tsub($tnow(), t); }
function $tclock(t, f) { return t ? new Date(t.w)[f]() : 0; }
// $tsecs is Duration.Seconds as Go computes it: whole seconds plus the
// rest over 1e9, which can differ in the last bit from d / 1e9.
function $tsecs(d) { const n = d % 1e9; return (d - n) / 1e9 + n / 1e9; }
// Forms: field(initial, check) and bind={x}. A field is a signal with pe,
// a signal of the parse error of its element's text (null when it parses);
// tc, a signal of whether it was touched; er, a computed of its error (pe,
// else check(value), check being null or a func); and rs, which resets it.
// Its set clears pe, as the element then shows the value set.
function $field(v, check) {
  const s = $signal(v), set = s.set, pe = (s.pe = $signal(null)), tc = (s.tc = $signal(false));
  s.set = (x) => { pe.set(null); set.call(s, x); };
  s.er = $computed(() => pe.get() ?? (check ? check(s.get()) : null));
  s.rs = () => { s.set(v); tc.set(false); };
  return s;
}
// $bind binds the value of element el to x, a signal or a field: event t
// sets x to the text read by p ($str, $atoi, $int(lo, hi) or $float, each
// returning [value, error]). Text that does not read leaves x as it is,
// and is a field's parse error until it reads. An effect writes x to the
// element when x changes, or a field's parse error goes, unless the text
// there already reads as x, as "1." and "01" do while they are typed. Each
// bind helper returns x.
function $bind(el, x, t, p) {
  el.addEventListener(t, () => { const [v, err] = p(el.value); err ? x.pe?.set(err) : x.set(v); });
  $effect(() => { const v = x.get(), [w, err] = p(el.value); if (!x.pe?.get() && (err || w !== v)) el.value = String(v); });
  return x;
}
function $str(s) { return [s]; }
// $int(lo, hi) reads an int as $atoi does, between lo and hi. (Not
// "return (s) =>": the minifier would make that "returns=>".)
function $int(lo, hi) {
  return function (s) { const r = $atoi(s); return r[1] || (r[0] >= lo && r[0] <= hi) ? r : [0, new Error("strconv.Atoi: parsing " + JSON.stringify(s) + ": value out of range")]; };
}
// $float reads a decimal number, as strconv.ParseFloat does.
function $float(s) {
  const n = /^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i.test(s) ? +s : NaN;
  return isFinite(n) ? [n] : [0, new Error("strconv.ParseFloat: parsing " + JSON.stringify(s) + (n ? ": value out of range" : ": invalid syntax"))];
}
// $check binds a checkbox to a bool.
function $check(el, x) {
  el.addEventListener("change", () => x.set(el.checked));
  $effect(() => { el.checked = x.get(); });
  return x;
}
// $radio binds a radio to x == v, its value: a value, or a computed one
// when it reads signals. Checking it sets x to v.
function $radio(el, x, v) {
  const f = () => (v?.get ? v.get() : v);
  el.addEventListener("change", () => el.checked && x.set(f()));
  $effect(() => { el.checked = x.get() === f(); });
  return x;
}
// $touch marks field x touched when its element loses focus, but only
// while the block that made the element is there: when den takes the
// element away it frees that block first, and the focusout that follows
// (browsers fire one at a focused element that is removed, and $exit
// blurs focus inside a leaving one) is not the user leaving the field.
function $touch(el, x) {
  let on = 1;
  $cleanup(() => { on = 0; });
  el.addEventListener("focusout", () => on && x.tc.set(true));
}
// Async data: resource(fetchJSON[T](url)). $resource holds a resource's
// state and makes its requests; $fetchJSON is fetchJSON's requester; and
// $jparse, $jdec and the helpers after them decode JSON as Go's
// encoding/json does, reading the descriptors the compiler writes as $jt.
//
// $resource is resource(f(args)): a returns the values of f's arguments,
// read in an effect, so a change to a signal they read starts a new
// request, as r (Reload) does; q, f's requester, makes the request for
// them with an AbortSignal and returns a promise of the result; z is the
// zero value. A new request aborts the one in flight (stop). Only the
// latest request's outcome lands, written by its promise's callbacks: d
// the data, kept until a request succeeds; e the error of the latest
// request to finish, null after a success; l whether a request is in
// flight. When the scope that made the resource is disposed, the request
// in flight is aborted, and nothing lands after that.
function $resource(a, q, z) {
  const r = { d: $signal(z), e: $signal(null), l: $signal(false), n: $signal(0) };
  let c = null;
  const stop = () => { c?.abort(); c = null; };
  r.r = () => { stop(); r.n.set(r.n.v + 1); };
  $effect(() => {
    r.n.get();
    const v = a(), k = new AbortController();
    stop();
    c = k;
    r.l.set(true);
    q(v, k.signal).then(
      (x) => { if (c === k) { r.d.set(x); r.e.set(null); r.l.set(false); } },
      (x) => { if (c === k) { r.e.set(x); r.l.set(false); } },
    );
  });
  $cleanup(stop);
  return r;
}
// $fetchJSON(J, i) is the requester of fetchJSON[T], where J[i] describes
// T: a GET of url, which must be on the page's origin (mode same-origin
// has the browser refuse others, after redirects too), and whose response
// must have a 2xx status and JSON that fits T.
function $fetchJSON(J, i) {
  return async ([u], signal) => {
    let p = "fetchJSON: ", t, s;
    try { t = new URL(u, location.href); } catch {}
    if (t?.origin !== location.origin) throw Error(p + u + " is not on this origin");
    p += "GET " + u + ": ";
    try {
      t = await fetch(t.href, { mode: "same-origin", credentials: "same-origin", headers: { Accept: "application/json" }, signal });
      s = t.ok && (await t.text());
    } catch (x) { throw signal.aborted ? x : Error(p + x.message); }
    if (!t.ok) throw Error(p + t.status + (t.statusText && " " + t.statusText));
    try { s = $jparse(s); } catch (x) { throw Error(p + "invalid JSON: " + x.message); }
    return $jdec(J, i, s, $jzero(J, i));
  };
}
// $jparse reads JSON text s as encoding/json reads it. JSON.parse checks
// that s is JSON; then its tokens are read again, in order: an object
// becomes {o: [[key, value], ...]}, repeated keys included, which Go
// decodes one after another, and a number {n: text}, whose text says
// whether it fits an int (1.0 does not). Lone surrogates in strings become
// U+FFFD, as in Go.
function $jparse(s) {
  JSON.parse(s);
  const t = s.match(/"(?:[^"\\]|\\.)*"|[^\s,:[\]{}"]+|[[\]{}]/g), str = (x) => (x = JSON.parse(x)).toWellFormed?.() ?? x;
  let i = 0;
  const v = () => {
    const x = t[i++];
    if (x === "[") { const a = []; while (t[i] !== "]") a.push(v()); i++; return a; }
    if (x === "{") { const o = []; while (t[i] !== "}") o.push([str(t[i++]), v()]); i++; return { o }; }
    return x[0] === '"' ? str(x) : x === "null" ? null : x === "true" ? true : x === "false" ? false : { n: x };
  };
  return v();
}
// $jdec decodes j, JSON as $jparse reads it, into the type J[i]
// describes, starting from v: like encoding/json, it decodes into the
// value that is there, the zero value or what a repeated key decoded
// before, so a repeated object merges into it. It throws Go's error for
// the first value that does not fit, where c is [struct, path of keys],
// which Go names once inside a struct.
function $jdec(J, i, j, v, c) {
  const d = J[i], k = d.k;
  if (j === null) return k === "l" ? null : k === "m" ? new Map() : v;
  const w = typeof j === "string" ? "string" : typeof j === "boolean" ? "bool" : Array.isArray(j) ? "array" : j.o ? "object" : "number";
  const bad = (x = w, t = d.t) => { throw Error("json: cannot unmarshal " + x + " into Go " + (c ? "struct field " + c[0] + "." + c[1] : "value") + " of type " + t); };
  if (k === "s" || k === "b") return w === (k === "s" ? "string" : "bool") ? j : bad();
  if (k === "o") {
    if (w !== "object") bad();
    const o = { ...v };
    for (let [n, x] of j.o) {
      const f = d.f.find((f) => f[0] === n) ?? ((n = $jfold(n, d.F)), d.f.find((f) => f[1] === n));
      if (!f) continue;
      const [p, ...r] = f[2], g = (y) => $jdec(J, f[3], x, y, [d.s, (c ? c[1] + "." : "") + (f[4] ?? f[0])]);
      o[p] = r.length ? $jin(o[p], r, g) : g(o[p]);
    }
    return o;
  }
  if (k === "m") {
    if (w !== "object") bad();
    const m = new Map(v), K = J[d.K];
    for (const [n, x] of j.o) {
      const y = $jdec(J, d.e, x, $jzero(J, d.e), c), key = K.k === "s" ? n : $jnum(K, n);
      if (key === undefined) bad("number " + n, K.t);
      m.set(key, y);
    }
    return m;
  }
  if (k === "l" && w === "string" && J[d.e].k === "u" && J[d.e].b === 8) return $jbytes(j);
  if (k === "a" || k === "l") {
    if (w !== "array") bad();
    // Elements decode into what is there: an array's own, or a slice's
    // backing array's as far as its capacity (Go reuses it, so elements
    // past its length that a repeated key left count), then zero values.
    const a = k === "a" ? [...v] : v ? v.a.slice(v.o, v.o + v.c) : [];
    j.forEach((x, n) => { if (k === "l" || n < d.n) a[n] = $jdec(J, d.e, x, n < a.length ? a[n] : $jzero(J, d.e), c); });
    if (k === "a") return a.fill($jzero(J, d.e), j.length);
    return j.length ? new $Slice(a, 0, j.length, a.length) : $sl([]);
  }
  if (w !== "number") bad();
  const n = $jnum(d, j.n);
  return n === undefined ? bad("number " + j.n) : n;
}
// $jnum is number text s as a value of the number type d, or undefined
// when it does not fit: Go reads an integer kind with strconv.ParseInt or
// ParseUint (so 1.0 and 1e2 are no ints, nor -0 a uint) and checks the
// kind's range, and reads a float with strconv.ParseFloat for its size,
// which does not take what rounds to an infinity. Integers beyond 2^53
// lose precision, as all of den's do.
function $jnum(d, s) {
  if (d.k === "f") { const f = d.b < 64 ? Math.fround(s) : +s; return isFinite(f) ? f : undefined; }
  if (!(d.k === "i" ? /^[+-]?\d+$/ : /^\d+$/).test(s)) return;
  const n = BigInt(s), m = 2n ** BigInt(d.k === "i" ? d.b - 1 : d.b);
  return n < m && (d.k === "u" || n >= -m) ? Number(n) : undefined;
}
// $jbytes decodes the base64 text s that encoding/json takes for a
// []byte: the standard encoding, with padding; line breaks are ignored.
function $jbytes(s) {
  s = s.replace(/[\r\n]/g, "");
  if (!/^(?:[A-Za-z\d+/]{4})*(?:[A-Za-z\d+/]{2}==|[A-Za-z\d+/]{3}=)?$/.test(s)) throw Error("illegal base64 data at input byte " + s.match(/^(?:[A-Za-z\d+/]{4})*/)[0].length);
  return $sl([...atob(s)].map((c) => c.charCodeAt(0)));
}
// $jzero is the zero value of the type J[i] describes.
function $jzero(J, i) {
  const d = J[i];
  return d.z ? d.z() : d.k === "s" ? "" : d.k === "b" ? false : d.k === "l" ? null : d.k === "m" ? new Map() : d.k === "a" ? Array(d.n).fill($jzero(J, d.e)) : 0;
}
// $jin is struct o with the field at path p, through embedded structs,
// set to g(its value).
function $jin(o, p, g) { const [n, ...r] = p; return { ...o, [n]: r.length ? $jin(o[n], r, g) : g(o[n]) }; }
// $jfold folds key k as encoding/json folds names to match them ignoring
// case: ASCII letters, and the long s and the Kelvin sign, as Go's simple
// folding does (to S and K), and other letters by F, the compiler's table
// for the letters of the struct's field names. A letter outside every
// orbit F covers matches no field however it folds, so it stays.
function $jfold(k, F) {
  k = k.replace(/[a-zſK]/g, (c) => (c === "ſ" ? "S" : c === "K" ? "K" : c.toUpperCase()));
  return F ? [...k].map((c) => F[c] ?? c).join("") : k;
}
