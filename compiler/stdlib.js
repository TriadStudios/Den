// den's standard library: the browser built-ins (stored, hash) and the
// imported Go packages. Like the runtime, an app ships only what it uses.

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
function $json(k, v) { return v instanceof Map ? Object.fromEntries(v) : v; }
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
