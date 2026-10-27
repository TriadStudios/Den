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
// initial value is used.
function $stored(key, init, dec) {
  let v = init;
  try { const j = localStorage.getItem(key); if (j !== null) v = dec(JSON.parse(j)); } catch {}
  const s = $signal(v);
  $effect(() => { const v = s.get(); try { localStorage.setItem(key, JSON.stringify(v, $json)); } catch {} });
  return s;
}
function $json(k, v) { return v instanceof Map ? Object.fromEntries(v) : v instanceof $Slice ? $arr(v) : v; }
function $want(v, t) { if (typeof v !== t) throw 0; return v; }
function $list(v) { if (!Array.isArray(v)) throw 0; return v; }
function $obj(v) { if (typeof v !== "object" || !v || Array.isArray(v)) throw 0; return v; }
// hash is a signal holding location.hash, kept in sync both ways.
function $hash() {
  const s = $signal(location.hash), f = () => s.set(location.hash);
  addEventListener("hashchange", f);
  $cleanup(() => removeEventListener("hashchange", f));
  $effect(() => { const h = s.get(); if (location.hash !== h) location.hash = h; });
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
// decoded. A param is never "." or "..", which browsers resolve away, so a
// path with one does not match. A :x param is a whole non-empty segment
// (an int parses as strconv.Atoi parses), and a catch-all takes the rest of
// the path, which may be empty.
function $match(s, z, v) {
  const ps = v.split("/").slice(1), o = { ...z };
  const dec = (p) => { p = decodeURIComponent(p); if (p === "." || p === "..") throw 0; return p; };
  if (ps.length > 1 && !ps.at(-1)) ps.pop();
  try {
    for (let i = 0; i < s.length; i++) {
      const c = s[i][0], k = s[i].slice(1);
      if (c === "*") { o[k] = ps.slice(i).map(dec).join("/"); return o; }
      if (i >= ps.length) return null;
      if (c !== ":") { if (decodeURIComponent(ps[i]) !== s[i]) return null; continue; }
      const d = dec(ps[i]);
      if (!d) return null;
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
