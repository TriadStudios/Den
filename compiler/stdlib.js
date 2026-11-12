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
// a signal of the parse error of its element's text (null when it parses),
// also written by SetError for a server message; tc, a signal of whether
// it was touched; er, a computed of its error (pe, else check(value),
// check being null or a func); and rs, which resets it. Its set clears pe,
// as the element then shows the value set, and so does the next edit.
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
// request, and so does r (Reload), which changes n; q, f's requester,
// makes the request for them with an AbortSignal and returns a promise of
// the result; z is the zero value. A new request aborts the one in flight
// (stop), once the arguments are read: if reading them panics, the one in
// flight goes on. Only the latest request's outcome lands, written by its
// promise's callbacks: d the data, kept until a request succeeds; e the
// error of the latest request to finish, null after a success; l whether
// a request is in flight. k is the AbortController of the request in
// flight. When the component that made the resource goes (c is its
// lifecycle record), the request in flight is aborted, and nothing lands
// after.
function $resource(c, a, q, z) {
  const r = { d: $signal(z), e: $signal(null), l: $signal(false), n: $signal(0) };
  let k = null;
  const stop = () => { k?.abort(); k = null; };
  r.r = () => r.n.set(r.n.v + 1);
  $effect(() => {
    r.n.get();
    const v = a(), m = new AbortController();
    stop();
    k = m;
    r.l.set(true);
    q(v, m.signal).then(
      (x) => { if (k === m) { r.d.set(x); r.e.set(null); r.l.set(false); } },
      (x) => { if (k === m) { r.e.set(x); r.l.set(false); } },
    );
  });
  $onCleanup(c, stop);
  return r;
}
// $fetchJSON(J, i) is the requester of fetchJSON[T], where J[i] describes
// T: a GET of url, which must be on the page's origin (mode same-origin
// has the browser refuse others, after redirects too), and whose response
// must have a 2xx status and JSON that fits T.
const $httpHeaders = new Map();
function $httpHeaderName(n) {
  if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(n)) return Error("http: invalid header name");
  n = n.toLowerCase();
  if (/^(proxy-|sec-)/.test(n) || ["accept-charset", "accept-encoding", "access-control-request-headers", "access-control-request-method", "connection", "content-length", "cookie", "cookie2", "date", "dnt", "expect", "host", "keep-alive", "origin", "referer", "set-cookie", "te", "trailer", "transfer-encoding", "upgrade", "via", "x-http-method", "x-http-method-override", "x-method-override"].includes(n)) return Error("http: forbidden header name");
  return null;
}
function $httpSetHeader(n, v) {
  const e = $httpHeaderName(n);
  if (e) return e;
  if (/[^\x20-\x7e\x80-\xff]/.test(v)) return Error("http: invalid header value");
  $httpHeaders.set(n.toLowerCase(), v.replace(/^ +| +$/g, ""));
  return null;
}
function $httpDeleteHeader(n) {
  const e = $httpHeaderName(n);
  if (!e) $httpHeaders.delete(n.toLowerCase());
  return e;
}
function $httpSnapshot() { return { accept: "application/json", ...Object.fromEntries($httpHeaders) }; }
function $httpMessage(e) { return e?.$message ?? e?.message ?? ""; }
function $httpError(t, p) {
  return (async () => {
  const e = Error(p + t.status + (t.statusText && " " + t.statusText));
  e.$status = t.status;
  try {
    const b = JSON.parse(await t.text());
    if (b && typeof b === "object" && b.error && typeof b.error === "object") {
      if (typeof b.error.code === "string") e.$code = b.error.code;
      if (typeof b.error.message === "string") e.$message = b.error.message;
    }
  } catch {}
  return e;
  })();
}
function $fetchJSON(J, i) {
  return async ([u], signal) => {
    let p = "fetchJSON: ", t, s;
    try { t = new URL(u, location.href); } catch {}
    if (t?.origin !== location.origin || !/^https?:$/.test(t.protocol) || t.username || t.password) throw Error(p + u + " is not on this origin");
    p += "GET " + u + ": ";
    try {
      t = await fetch(t.href, { mode: "same-origin", credentials: "same-origin", headers: $httpSnapshot(), signal });
      s = t.ok && (await t.text());
    } catch (x) { throw signal.aborted ? x : Error(p + x.message); }
    if (!t.ok) throw await $httpError(t, p);
    try { s = $jparse(s); } catch (x) { throw Error(p + "invalid JSON: " + x.message); }
    return $jdec(J, i, s, $jzero(J, i));
  };
}
// $jparse reads JSON text s as encoding/json reads it. JSON.parse checks
// that s is JSON; then its tokens are read again, in order: an object
// becomes {o: [[key, value], ...]}, repeated keys included, which Go
// decodes one after another, and a number {n: text}, whose text says
// whether it fits an int (1.0 does not). Lone surrogates in strings become
// U+FFFD, as in Go, where the browser has toWellFormed. The containers
// being read are kept on a stack, not in recursive calls, so JSON nested
// as deep as Go reads it (10,000 levels) never runs out of stack, and
// deeper is Go's error.
function $jparse(s) {
  JSON.parse(s);
  const t = s.match(/"(?:[^"\\]|\\.)*"|[^\s,:[\]{}"]+|[[\]{}]/g), str = (x) => (x = JSON.parse(x)).toWellFormed?.() ?? x, st = [];
  let out;
  for (const x of t) {
    if (x === "]" || x === "}") { st.pop(); continue; }
    const v = x === "[" ? [] : x === "{" ? { o: [] } : x[0] === '"' ? str(x) : x === "null" ? null : x === "true" ? true : x === "false" ? false : { n: x }, p = st.at(-1);
    // In an object, a key waits in p.k for its value.
    if (!p) out = v;
    else if (!p.o) p.push(v);
    else if (p.k === undefined) p.k = v;
    else { p.o.push([p.k, v]); p.k = undefined; }
    if ((x === "[" || x === "{") && st.push(v) > 1e4) throw Error("exceeded max depth");
  }
  return out;
}
// $jdec decodes j, JSON as $jparse reads it, into the type J[i]
// describes, starting from v: like encoding/json, it decodes into the
// value that is there, the zero value or what a repeated key decoded
// before, so a repeated object merges into it. It throws Go's error for
// the first value that does not fit. $jone decodes one value, and for an
// object or array pushes a frame onto a stack, st, which hands out its
// elements (next) and takes their values (put) until it is done (end):
// a stack, not recursion, so the deepest JSON Go reads decodes.
function $jdec(J, i, j, v, c) {
  const st = [];
  let r = $jone(st, J, i, j, v, c);
  while (st.length) {
    const f = st.at(-1);
    if (r !== st) f.put(r); // r is the value of f's last element; st, a new frame
    const n = f.next();
    if (n) r = $jone(st, J, ...n);
    else { st.pop(); r = f.end(); }
  }
  return r;
}
// $jone decodes j into J[i] as $jdec does, or for an object or array,
// pushes its frame onto st and returns st. c, for errors, is [struct,
// path of keys], which Go names once inside a struct.
function $jone(st, J, i, j, v, c) {
  const d = J[i], k = d.k;
  if (j === null) return k === "l" ? null : k === "m" ? new Map() : v;
  const w = typeof j === "string" ? "string" : typeof j === "boolean" ? "bool" : Array.isArray(j) ? "array" : j.o ? "object" : "number";
  const bad = (x = w, t = d.t) => { throw Error("json: cannot unmarshal " + x + " into Go " + (c ? "struct field " + c[0] + "." + c[1] : "value") + " of type " + t); };
  if (k === "s" || k === "b") return w === (k === "s" ? "string" : "bool") ? j : bad();
  if (k === "l" && w === "string" && J[d.e].k === "u" && J[d.e].b === 8) return $jbytes(j);
  if (k !== "o" && k !== "m" && k !== "a" && k !== "l") {
    if (w !== "number") bad();
    const n = $jnum(d, j.n);
    return n === undefined ? bad("number " + j.n) : n;
  }
  if (w !== (k === "o" || k === "m" ? "object" : "array")) bad();
  let x = 0, f, n;
  if (k === "o") {
    const o = { ...v };
    st.push({
      next() {
        while (x < j.o.length) {
          let [n, y] = j.o[x++];
          f = d.f.find((f) => f[0] === n) ?? ((n = $jfold(n, d.F)), d.f.find((f) => f[1] === n));
          if (f) return [f[3], y, f[2].reduce((o, p) => o[p], o), [d.s, (c ? c[1] + "." : "") + (f[4] ?? f[0])]];
        }
      },
      put(r) { const [p, ...q] = f[2]; o[p] = q.length ? $jin(o[p], q, () => r) : r; },
      end: () => o,
    });
  } else if (k === "m") {
    // Go decodes each value from zero, and then the key.
    const m = new Map(v), K = J[d.K];
    st.push({
      next() { if (x < j.o.length) { let y; [n, y] = j.o[x++]; return [d.e, y, $jzero(J, d.e), c]; } },
      put(r) { const key = K.k === "s" ? n : $jnum(K, n); if (key === undefined) bad("number " + n, K.t); m.set(key, r); },
      end: () => m,
    });
  } else {
    // Elements decode into what is there: an array's own, or a slice's
    // backing array's as far as its capacity (Go reuses it, so elements
    // past its length that a repeated key left count), then zero values.
    // An array takes as many as it holds.
    const a = k === "a" ? [...v] : v ? v.a.slice(v.o, v.o + v.c) : [];
    st.push({
      next() { if (x < j.length && (k === "l" || x < d.n)) { n = x++; return [d.e, j[n], n < a.length ? a[n] : $jzero(J, d.e), c]; } },
      put(r) { a[n] = r; },
      end: () => (k === "a" ? a.fill($jzero(J, d.e), j.length) : j.length ? new $Slice(a, 0, j.length, a.length) : $sl([])),
    });
  }
  return st;
}
// $jnum is number text s as a value of the number type d, or undefined
// when it does not fit: Go reads an integer kind with strconv.ParseInt or
// ParseUint (so 1.0 and 1e2 are no ints, nor -0 a uint) and checks the
// kind's range, and reads a float with strconv.ParseFloat for its size,
// which does not take what rounds to an infinity. Integers beyond 2^53
// lose precision, as all of den's do.
function $jnum(d, s) {
  if (d.k === "f") { const f = d.b < 64 ? $jf32(s) : +s; return isFinite(f) ? f : undefined; }
  if (!(d.k === "i" ? /^[+-]?\d+$/ : /^\d+$/).test(s)) return;
  const n = BigInt(s), m = 2n ** BigInt(d.k === "i" ? d.b - 1 : d.b);
  return n < m && (d.k === "u" || n >= -m) ? Number(n) : undefined;
}
// $jf32 is number text s rounded once to the nearest float32, as Go's
// strconv.ParseFloat(s, 32) does. Number(s) rounds to a float64 first;
// only where that lands exactly halfway between two float32s (o and g,
// infinities standing for 2^128) can rounding again go the wrong way, and
// there an exact comparison of s with it says which way s lies.
function $jf32(s) {
  const f = +s, g = Math.fround(f), b = new DataView(new ArrayBuffer(4)), big = (x) => (isFinite(x) ? x : Math.sign(x) * 2 ** 128);
  if (g === f || !isFinite(f)) return g;
  b.setFloat32(0, g);
  b.setUint32(0, b.getUint32(0) + (Math.abs(f) > Math.abs(g) ? 1 : -1));
  const o = b.getFloat32(0);
  if (f - big(o) !== big(g) - f) return g;
  const c = $jcmp(s, f), far = Math.abs(big(o)) > Math.abs(big(g));
  return c > 0 ? (far ? o : g) : c < 0 ? (far ? g : o) : g;
}
// $jcmp compares the magnitude of number text s with that of float64 f,
// exactly, in BigInts: -1, 0 or 1.
function $jcmp(s, f) {
  const [, n, m = "", e = 0] = /^-?(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(s), b = new DataView(new ArrayBuffer(8));
  b.setFloat64(0, f);
  const w = b.getBigUint64(0), x = Number((w >> 52n) & 2047n), y = (x ? x : 1) - 1075, z = e - m.length;
  let p = BigInt(n + m), q = (w & (2n ** 52n - 1n)) | (x ? 2n ** 52n : 0n);
  if (z > 0) p *= 10n ** BigInt(z); else q *= 10n ** BigInt(-z);
  if (y > 0) q <<= BigInt(y); else p <<= BigInt(-y);
  return p > q ? 1 : p < q ? -1 : 0;
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
// case: ASCII letters, and U+017F (long s) and U+212A (Kelvin sign), as
// Go's simple folding does (to S and K), and other letters by F, the
// compiler's table for the letters of the struct's field names. A letter
// outside every orbit F covers matches no field however it folds, so it
// stays as it is.
function $jfold(k, F) {
  return [...k].map((c) => { const r = c.codePointAt(0); return r < 128 ? c.toUpperCase() : r === 383 ? "S" : r === 8490 ? "K" : F?.[c] ?? c; }).join("");
}
// $action is action(sendJSON[In, Out](method, url)). a returns [method, url],
// read when Do runs (go), inside $scope, so a signal it reads is not
// subscribed and a panic is reported rather than sent. q is $sendJSON.
// d, e and p are Data, Err and Pending. A Do while one is pending sets Err
// and sends nothing; the request in flight still settles, and that outcome
// replaces Err. n is Then, one function, replaced, called only after a
// success. k is the AbortController in flight. When the component goes
// (c is its lifecycle record), the request is aborted and nothing lands.
function $action(c, a, q, z) {
  const r = { d: $signal(z), e: $signal(null), p: $signal(false) };
  let k = null, f = null;
  const stop = () => { if (k) k.abort(); k = null; };
  r.go = (v) => {
    if (r.p.v) { r.e.set(Error("action: Do called while a request is pending")); return; }
    let m, u;
    try { [m, u] = $scope(a)[0]; } catch (e) { reportError(e); return; }
    const ac = new AbortController();
    k = ac;
    r.p.set(true);
    q(m, u, v, ac.signal).then(
      (x) => { if (k === ac) { r.d.set(x); r.e.set(null); r.p.set(false); if (f) $lifeTry(c, () => f(x)); } },
      (x) => { if (k === ac) { r.e.set(x); r.p.set(false); } },
    );
  };
  r.n = (g) => { f = g; };
  $onCleanup(c, stop);
  return r;
}
// $sendJSON(J, inn, out) is the requester of sendJSON[In, Out]: method and
// url were read by Do, v is the In (undefined when In is struct{} and the
// call was Do()), signal aborts it. The URL must be on the page's origin,
// checked before anything is sent. struct{} sends {}, except DELETE, which
// sends no body. A 204 or an empty body is the zero Out.
function $sendJSON(J, inn, out) {
  return async (method, u, v, signal) => {
    let p = "sendJSON: ", t;
    try { t = new URL(u, location.href); } catch {}
    if (t?.origin !== location.origin || !/^https?:$/.test(t.protocol) || t.username || t.password) throw Error(p + u + " is not on this origin");
    p += method + " " + u + ": ";
    let body = null;
    if (v === undefined) body = method === "DELETE" ? null : "{}";
    else body = $jenc(J, inn, v);
    const headers = $httpSnapshot();
    if (body !== null && headers["content-type"] === undefined) headers["content-type"] = "application/json";
    let s;
    try {
      const opts = { method, mode: "same-origin", credentials: "same-origin", headers, signal };
      if (body !== null) opts.body = body;
      t = await fetch(t.href, opts);
      s = t.ok && t.status !== 204 ? await t.text() : "";
    } catch (x) { throw signal.aborted ? x : Error(p + x.message); }
    if (!t.ok) throw await $httpError(t, p);
    if (t.status === 204 || s === "") return $jzero(J, out);
    try { s = $jparse(s); } catch (x) { throw Error(p + "invalid JSON: " + x.message); }
    return $jdec(J, out, s, $jzero(J, out));
  };
}
// $jenc encodes v as the type J[i] describes, the way encoding/json does:
// tags and omitempty/omitzero from g, embedded fields flattened, map keys
// sorted as strings in code-point order, []byte as base64, HTML-escaped
// strings. It walks a stack, like $jdec, so a deep value does not recurse.
// $jput encodes one value, or for an object or array pushes a frame and
// returns st, the sentinel that the value is not ready yet.
function $jenc(J, i, v) {
  const st = [];
  let r = $jput(st, J, i, v);
  while (st.length) {
    const f = st.at(-1);
    if (r !== st) f.add(r);
    const n = f.next();
    if (n) r = $jput(st, J, n[0], n[1]);
    else { st.pop(); r = f.end(); }
  }
  return r;
}
function $jput(st, J, i, v) {
  const d = J[i], k = d.k;
  if (v == null && (k === "l" || k === "m")) return "null";
  if (k === "b") return v ? "true" : "false";
  if (k === "s") return $jstr(v);
  if (k === "f") return $jflt(v, d.b);
  if (k === "i" || k === "u") return $jint(v);
  if (k === "l" && J[d.e].k === "u" && J[d.e].b === 8) return $jstr($jb64(v));
  if (k === "o") {
    const g = d.g;
    let x = 0, buf = "{", sep = "";
    st.push({
      next() {
        while (x < g.length) {
          const [, path, ti, omit] = g[x++];
          let cur = v;
          for (const p of path) cur = cur[p];
          if (omit && $jomit(J, ti, cur, omit)) continue;
          this.k = g[x - 1][0];
          return [ti, cur];
        }
      },
      add(s) { buf += sep + $jstr(this.k) + ":" + s; sep = ","; },
      end: () => buf + "}",
    });
    return st;
  }
  if (k === "m") {
    const keys = [...v.keys()].sort($jkey);
    let x = 0, buf = "{", sep = "";
    const K = J[d.K];
    st.push({
      next() {
        if (x < keys.length) {
          const key = keys[x++];
          this.k = K.k === "s" ? key : $jint(key);
          return [d.e, v.get(key)];
        }
      },
      add(s) { buf += sep + $jstr(this.k) + ":" + s; sep = ","; },
      end: () => buf + "}",
    });
    return st;
  }
  const n = k === "a" ? d.n : v.n, arr = k === "a" ? v : v.a, off = k === "a" ? 0 : v.o;
  let x = 0, buf = "[", sep = "";
  st.push({
    next() { if (x < n) { const i = x++; return [d.e, arr[off + i]]; } },
    add(s) { buf += sep + s; sep = ","; },
    end: () => buf + "]",
  });
  return st;
}
// $jomit reports whether encoding/json omits a field with this omit mask:
// 1 omitempty, 2 omitzero, 3 both, and either one is enough. A struct is
// never empty. omitzero of a struct is every field zero, and a map or a
// slice is zero only when it is nil. Den's maps are never nil (a nil map
// is an empty one), so an empty map is kept by omitzero and encoded as {}.
function $jomit(J, i, v, flag) {
  if (flag & 2 && $jisz(v)) return true;
  if (flag & 1 && $jempty(J, i, v)) return true;
  return false;
}
function $jempty(J, i, v) {
  const k = J[i].k;
  if (k === "b") return !v;
  if (k === "s") return v === "";
  if (k === "i" || k === "u" || k === "f") return v === 0;
  if (k === "l") return v == null || v.n === 0;
  if (k === "m") return v == null || v.size === 0;
  if (k === "a") return J[i].n === 0;
  return false;
}
function $jisz(v) {
  if (v == null) return true;
  if (typeof v !== "object") return v === false || v === 0 || v === "";
  if (v instanceof $Slice || v instanceof Map) return false;
  if (Array.isArray(v)) { for (let i = 0; i < v.length; i++) if (!$jisz(v[i])) return false; return true; }
  for (const p in v) if (!$jisz(v[p])) return false;
  return true;
}
// $jstr is a JSON string as encoding/json writes it, with <, > and & and
// U+2028 and U+2029 escaped so the text is safe in HTML.
function $jstr(s) {
  let o = '"';
  for (const c of s) {
    const r = c.codePointAt(0);
    if (c === '"' || c === "\\") o += "\\" + c;
    else if (c === "\b") o += "\\b";
    else if (c === "\f") o += "\\f";
    else if (c === "\n") o += "\\n";
    else if (c === "\r") o += "\\r";
    else if (c === "\t") o += "\\t";
    else if (r < 32 || c === "<" || c === ">" || c === "&") o += "\\u00" + r.toString(16).padStart(2, "0");
    else if (r === 0x2028 || r === 0x2029) o += "\\u" + r.toString(16);
    else o += c;
  }
  return o + '"';
}
// $jflt is a float as encoding/json's ES6 formatting writes it. float64
// follows JSON.stringify except for -0, which stays -0. float32 is the
// shortest round trip through a float32, the same digits Go prints.
function $jflt(n, bits) {
  if (n !== n) throw Error("json: unsupported value: NaN");
  if (n === Infinity) throw Error("json: unsupported value: +Inf");
  if (n === -Infinity) throw Error("json: unsupported value: -Inf");
  if (bits < 64) n = Math.fround(n);
  if (Object.is(n, -0)) return "-0";
  if (bits >= 64) return JSON.stringify(n);
  const neg = n < 0, abs = Math.abs(n), sci = abs < 1e-6 || abs >= 1e21;
  for (let p = 1; p <= 9; p++) {
    const raw = abs.toPrecision(p);
    if (Math.fround((neg ? -1 : 1) * Number(raw)) !== n) continue;
    return (neg ? "-" : "") + $jrender(raw, sci);
  }
  return JSON.stringify(Math.abs(n) === n ? n : abs);
}
function $jparts(raw) {
  let i = raw.indexOf("e");
  if (i < 0) i = raw.indexOf("E");
  const m = i < 0 ? raw : raw.slice(0, i);
  let e = i < 0 ? 0 : +raw.slice(i + 1);
  const d = m.indexOf(".");
  if (i < 0) e = (d < 0 ? m.length : d) - 1;
  return [d < 0 ? m : m.slice(0, d) + m.slice(d + 1), e];
}
function $jrender(raw, sci) {
  let [digits, e] = $jparts(raw);
  while (digits.endsWith("0")) digits = digits.slice(0, -1);
  if (!digits) digits = "0";
  if (digits === "0") return "0";
  if (sci) {
    const frac = digits.slice(1);
    return digits[0] + (frac ? "." + frac : "") + "e" + (e < 0 ? "" + e : "+" + e);
  }
  if (e >= 0) {
    if (digits.length <= e + 1) return digits + "0".repeat(e + 1 - digits.length);
    return digits.slice(0, e + 1) + "." + digits.slice(e + 1);
  }
  return "0." + "0".repeat(-e - 1) + digits;
}
function $jint(n) {
  if (n !== n) throw Error("json: unsupported value: NaN");
  if (n === Infinity) throw Error("json: unsupported value: +Inf");
  if (n === -Infinity) throw Error("json: unsupported value: -Inf");
  n = Math.trunc(n);
  if (n === 0) return "0";
  return String(n);
}
// $jb64 is the standard base64 of a []byte, as encoding/json writes it.
function $jb64(v) {
  let s = "";
  for (let i = 0; i < v.n; i += 0x8000) {
    const c = Math.min(0x8000, v.n - i), u = [];
    for (let j = 0; j < c; j++) u.push(v.a[v.o + i + j]);
    s += String.fromCharCode.apply(null, u);
  }
  return btoa(s);
}
// $jkey orders map keys as encoding/json does: the UTF-8 byte order of
// their string form, which is code-point order. JS "<" is UTF-16 code-unit
// order and disagrees for characters outside the BMP.
function $jkey(a, b) {
  if (typeof a !== "string") a = $jint(a);
  if (typeof b !== "string") b = $jint(b);
  const x = [...a], y = [...b], n = x.length < y.length ? x.length : y.length;
  for (let i = 0; i < n; i++) {
    const d = x[i].codePointAt(0) - y[i].codePointAt(0);
    if (d) return d;
  }
  return x.length - y.length;
}
// $events is events(url). urlFn is read in an effect, so a signal in the
// URL closes the current source and opens another; registrations in hs
// stay and are attached to the new one. c is Connected and e is Err.
// A URL that is not http(s) on this origin, or that carries a username or
// password, sets Err and does not construct an EventSource. The browser
// sends cookies (withCredentials) and remembers Last-Event-ID; den adds
// no headers and no token. Deliveries sit in one queue and flush once,
// on the next animation frame or after 50ms, whichever comes first, so a
// hidden tab cannot grow the queue for hours. A URL change or the
// component going away bumps the generation, drops the queue and closes
// the source. A native error leaves the source open so the browser can
// reconnect; the next open clears Err.
function $events(c, urlFn) {
  const r = { c: $signal(false), e: $signal(null), hs: [] };
  let es = null, g = 0, q = [], arm = 0, raf = 0, tid = 0;
  const cancel = () => {
    if (!arm) return;
    arm = 0;
    cancelAnimationFrame(raf);
    clearTimeout(tid);
  };
  const drop = () => {
    g++;
    const old = es;
    es = null;
    if (old) old.close();
    q = [];
    cancel();
  };
  const flush = () => {
    const batch = q;
    q = [];
    for (const item of batch) {
      if (item.g !== g) continue;
      let v;
      try { v = $jdec(item.J, item.i, $jparse(item.data), $jzero(item.J, item.i)); }
      catch (err) { r.e.set(Error("events: invalid JSON: " + err.message)); continue; }
      $lifeTry(c, () => item.fn(v));
    }
  };
  const go = () => {
    if (!arm) return;
    const frame = raf, timer = tid;
    arm = 0;
    cancelAnimationFrame(frame);
    clearTimeout(timer);
    flush();
  };
  const schedule = () => {
    if (arm) return;
    arm = 1;
    raf = requestAnimationFrame(go);
    tid = setTimeout(go, 50);
  };
  const attach = (src, h) => {
    src.addEventListener(h.name, (ev) => {
      if (es !== src) return;
      q.push({ g, J: h.J, i: h.i, fn: h.fn, data: ev.data });
      schedule();
    });
  };
  r.listen = (h) => { if (es) attach(es, h); };
  $effect(() => {
    const u = urlFn();
    drop();
    let parsed = null;
    try { parsed = new URL(u, location.href); } catch { parsed = null; }
    if (!parsed || parsed.origin !== location.origin || !/^https?:$/.test(parsed.protocol) || parsed.username || parsed.password) {
      r.c.set(false);
      r.e.set(Error("events: " + u + " is not on this origin"));
      return;
    }
    const src = new EventSource(parsed.href, { withCredentials: true });
    es = src;
    src.onopen = () => { if (es !== src) return; r.c.set(true); r.e.set(null); };
    src.onerror = () => { if (es !== src) return; r.c.set(false); r.e.set(Error("events: connection lost")); };
    for (const h of r.hs) attach(src, h);
  });
  $onCleanup(c, () => { drop(); r.c.set(false); });
  return r;
}
// $onEv is on[T](stream, name, fn). name was read once, untracked. An
// empty name is reported and not listened for. The handler is recorded
// and, when a source is already open, attached immediately: events()
// connects in an effect that runs before a later on() in the same body.
function $onEv(s, name, J, i, fn) {
  if (name === "") { reportError("events: empty event name"); return; }
  const h = { name, J, i, fn };
  s.hs.push(h);
  s.listen(h);
}
