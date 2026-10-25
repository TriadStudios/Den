// Runs the benchmark apps in Chromium and compares den with vanilla JS.
//
//   go run ./cmd/den build bench/den && node bench/run.js [iterations]
//
// Needs Playwright with Chromium (npm i -g playwright). This is a dev tool
// only; den itself has no npm dependencies.
//
// Each operation is timed from the click to the end of the next frame, so
// script, style, layout and paint are all included. The CPU is throttled
// 4x, as js-framework-benchmark does for its slower cases, to make the
// numbers stable. The result is the median of the iterations; the summary
// is the geometric mean of den's time over vanilla's.
"use strict";
const http = require("http");
const fs = require("fs");
const path = require("path");
const { chromium } = require("playwright");

const ITER = Number(process.argv[2] || 10);
const root = __dirname;
const types = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css" };

function serve() {
  const server = http.createServer((req, res) => {
    let p = decodeURIComponent(new URL(req.url, "http://x").pathname);
    if (p.endsWith("/")) p += "index.html";
    const file = p.startsWith("/den/") ? path.join(root, "den/dist", p.slice(5)) : path.join(root, p);
    if (!file.startsWith(root)) { res.writeHead(403).end(); return; }
    fs.readFile(file, (err, body) => {
      if (err) { res.writeHead(404).end(); return; }
      res.writeHead(200, { "Content-Type": types[path.extname(file)] || "application/octet-stream" }).end(body);
    });
  });
  return new Promise((r) => server.listen(0, "127.0.0.1", () => r(server)));
}

const click = (sel) => async (page) => page.click(sel);
const times = (n, f) => async (page) => { for (let i = 0; i < n; i++) await f(page); };
const row = (n, cls) => `tbody tr:nth-child(${n}) a.${cls}`;

// Each benchmark: setup steps (including warmup), then the measured click.
const benches = [
  { name: "create 1,000 rows", setup: times(5, async (p) => { await p.click("#run"); await p.click("#clear"); }), run: "#run" },
  { name: "replace 1,000 rows", setup: times(5, click("#run")), run: "#run" },
  { name: "update every 10th row", setup: async (p) => { await p.click("#run"); await times(3, click("#update"))(p); }, run: "#update" },
  { name: "select row", setup: async (p) => { await p.click("#run"); for (let i = 5; i > 0; i--) await p.click(row(i + 2, "lbl")); }, run: row(2, "lbl") },
  { name: "swap rows", setup: async (p) => { await p.click("#run"); await times(5, click("#swaprows"))(p); }, run: "#swaprows" },
  { name: "remove row", setup: async (p) => { await p.click("#run"); for (let i = 5; i > 0; i--) await p.click(row(i + 4, "remove")); }, run: row(4, "remove") },
  { name: "create 10,000 rows", setup: async (p) => { await p.click("#runlots"); await p.click("#clear"); }, run: "#runlots" },
  { name: "append 1,000 rows", setup: click("#run"), run: "#add" },
  { name: "clear 1,000 rows", setup: async (p) => { await times(3, async (q) => { await q.click("#run"); await q.click("#clear"); })(p); await p.click("#run"); }, run: "#clear" },
];

// measure returns [total, script]: total runs from the click to the end of
// the next frame; script stops once the click's microtasks (den's update
// flush) have run, before style, layout and paint.
async function measure(page, sel) {
  return page.evaluate(async (sel) => {
    const el = document.querySelector(sel);
    if (!el) throw new Error("missing " + sel);
    await new Promise((r) => requestAnimationFrame(() => setTimeout(r, 0)));
    const t0 = performance.now();
    el.click();
    await Promise.resolve();
    const t1 = performance.now();
    await new Promise((r) => requestAnimationFrame(() => setTimeout(r, 0)));
    return [performance.now() - t0, t1 - t0];
  }, sel);
}

const median = (xs) => { const s = [...xs].sort((a, b) => a - b); return s.length % 2 ? s[s.length >> 1] : (s[s.length / 2 - 1] + s[s.length / 2]) / 2; };

(async () => {
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch();
  const results = {};
  for (const fw of ["vanilla", "den"]) {
    results[fw] = {};
    for (const b of benches) {
      const samples = [], scripts = [];
      for (let i = 0; i < ITER; i++) {
        const page = await browser.newPage();
        const errors = [];
        page.on("pageerror", (e) => errors.push(String(e)));
        await page.goto(`${base}/${fw}/`);
        await page.waitForSelector("#run");
        await b.setup(page);
        const cdp = await page.context().newCDPSession(page);
        await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
        const [total, script] = await measure(page, b.run);
        samples.push(total);
        scripts.push(script);
        await cdp.send("Emulation.setCPUThrottlingRate", { rate: 1 });
        if (errors.length) throw new Error(`${fw} ${b.name}: ${errors.join("; ")}`);
        await page.close();
      }
      results[fw][b.name] = median(samples);
      results[fw][b.name + " script"] = median(scripts);
    }
  }
  await browser.close();
  server.close();

  const pad = (s, n) => String(s).padEnd(n);
  console.log(`median of ${ITER} runs, CPU throttled 4x; script = JS only, total = to the next painted frame\n`);
  console.log(pad("benchmark", 24) + pad("vanilla total", 15) + pad("den total", 11) + pad("ratio", 8) + pad("vanilla script", 16) + pad("den script", 12) + "ratio");
  let logSum = 0, logScript = 0;
  for (const b of benches) {
    const v = results.vanilla[b.name], d = results.den[b.name], r = d / v;
    const vs = results.vanilla[b.name + " script"], ds = results.den[b.name + " script"], rs = ds / vs;
    logSum += Math.log(r);
    logScript += Math.log(rs);
    console.log(pad(b.name, 24) + pad(v.toFixed(1), 15) + pad(d.toFixed(1), 11) + pad(r.toFixed(2), 8) + pad(vs.toFixed(1), 16) + pad(ds.toFixed(1), 12) + rs.toFixed(2));
  }
  const gm = Math.exp(logSum / benches.length);
  console.log(`\ngeometric mean den/vanilla: ${gm.toFixed(2)} total (gate: within 1.30), ${Math.exp(logScript / benches.length).toFixed(2)} script only`);
  process.exitCode = gm <= 1.3 ? 0 : 1;
})().catch((e) => { console.error(e); process.exit(1); });
