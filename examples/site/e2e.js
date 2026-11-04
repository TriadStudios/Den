// End-to-end check of examples/site in Chromium: every page through the
// nav, page transitions, the live demos, the theme toggle and the phone
// menu. Needs Playwright (a development tool, not a den dependency):
//
//   go run ./cmd/den dev examples/site -addr localhost:8080 &
//   node examples/site/e2e.js http://localhost:8080/
//
// CHANNEL=chrome (or msedge) uses an installed browser instead of
// Playwright's own Chromium.
"use strict";
const { chromium } = require("playwright");
const assert = require("assert");

const url = process.argv[2] || "http://localhost:8080/";

(async () => {
  const browser = await chromium.launch({ channel: process.env.CHANNEL || undefined });
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  const problems = [];
  page.on("pageerror", (e) => problems.push(String(e)));
  page.on("console", (m) => m.type() === "error" && !m.location().url.endsWith("/favicon.ico") && problems.push(m.text()));
  await page.goto(url);
  await page.evaluate(() => localStorage.clear());
  await page.reload();

  const step = (name) => console.log("ok  " + name);
  // One page is left in main once the old one has played its exit.
  const settled = () => page.waitForFunction(() => document.querySelectorAll("main > .page").length === 1);
  const h1 = () => page.textContent("main > .page h1");
  const current = () => page.$$eval("header nav a[aria-current=page]", (as) => as.map((a) => a.textContent));
  const nav = async (text, path) => {
    await page.click(`header nav >> text=${text}`);
    await page.waitForURL(new URL(path, url).href);
    await settled();
  };

  // Home: the live counter is the component shown beside it.
  assert.strictEqual(await h1(), "Go-syntax UI, compiled to tiny JavaScript");
  assert.deepStrictEqual(await current(), []);
  await page.click(".counter button >> text=+");
  await page.click(".counter button >> text=+");
  await page.click(".counter button >> text=−");
  assert.strictEqual(await page.textContent(".counter output"), "1");
  assert.strictEqual(await page.textContent(".counter p"), "Doubled: 2");
  step("home: live counter");

  // Every page through the nav, each marked current, with a transition.
  await nav("Features", "/features");
  assert.strictEqual(await h1(), "Features");
  assert.deepStrictEqual(await current(), ["Features"]);
  await nav("Performance", "/performance");
  assert.strictEqual(await h1(), "As fast as hand-written JavaScript");
  assert.strictEqual(await page.$$eval(".chart p", (ps) => ps.length), 13);
  await nav("Docs", "/docs/install");
  assert.strictEqual(await h1(), "Install");
  assert.deepStrictEqual(await current(), ["Docs"]);
  await nav("Why den", "/why");
  assert.strictEqual(await page.$$eval("tr", (rs) => rs.length), 7);
  step("nav: every page, aria-current, one page after each transition");

  // Old and new page overlap while one leaves and the other enters.
  await page.click("header nav >> text=Features");
  const both = await page.$$eval("main > .page", (ps) => ps.map((p) => p.inert));
  assert.deepStrictEqual(both, [true, false]);
  await settled();
  step("transition: the leaving page is inert while the new one enters");

  // Live demos: a stored signal and an {if} with transitions.
  await page.fill(".demo input", "Ada");
  assert.strictEqual(await page.textContent(".demo input + p"), "Hello, Ada! Reload: it stays.");
  await page.reload();
  assert.strictEqual(await page.inputValue(".demo input"), "Ada");
  assert.strictEqual(await page.textContent(".chip"), "Scaled in, slides out");
  await page.click(".demo >> text=Toggle");
  await page.waitForFunction(() => document.querySelectorAll(".chip").length === 1 && document.querySelector(".chip").textContent === "Slid in, scales out");
  step("features: stored input demo, toggle demo");

  // Docs: next and previous, an unknown slug, and /docs itself.
  await nav("Docs", "/docs/install");
  await page.click(".pager >> text=First component →");
  await page.waitForURL(new URL("/docs/first-component", url).href);
  await settled();
  assert.strictEqual(await h1(), "First component");
  assert.strictEqual(await page.textContent(".docs nav a[aria-current=page]"), "First component");
  await page.click(".pager >> text=← Install");
  await page.waitForURL(new URL("/docs/install", url).href);
  await settled();
  await page.goto(new URL("/docs/nope", url).href);
  assert.strictEqual(await h1(), "No such page");
  await page.goto(new URL("/docs", url).href);
  assert.strictEqual(await h1(), "Install");
  step("docs: pager, unknown slug, /docs");

  // Not found, and back.
  await page.goto(new URL("/nowhere", url).href);
  assert.strictEqual(await h1(), "Nothing lives here");
  await page.goBack();
  await settled();
  assert.strictEqual(await h1(), "Install");
  step("404 and back");

  // Theme: system, light, dark, and kept across a reload.
  const theme = () => page.getAttribute(".site", "class");
  assert.strictEqual(await theme(), "site system");
  await page.click("[aria-label^='Theme']");
  assert.strictEqual(await theme(), "site light");
  await page.click("[aria-label^='Theme']");
  assert.strictEqual(await theme(), "site dark");
  await page.reload();
  assert.strictEqual(await theme(), "site dark");
  assert.strictEqual(await page.evaluate(() => getComputedStyle(document.querySelector(".site")).colorScheme), "dark");
  await page.click("[aria-label^='Theme']");
  assert.strictEqual(await theme(), "site system");
  step("theme toggle, stored");

  // Phone: the nav is a menu that closes on navigation.
  await page.setViewportSize({ width: 375, height: 812 });
  assert.strictEqual(await page.isVisible("header nav"), false);
  await page.click("[aria-label=Menu]");
  assert.strictEqual(await page.getAttribute("[aria-label=Menu]", "aria-expanded"), "true");
  assert.strictEqual(await page.isVisible("header nav"), true);
  await nav("Performance", "/performance");
  assert.strictEqual(await page.isVisible("header nav"), false);
  assert.strictEqual(await page.evaluate(() => document.documentElement.scrollWidth), 375);
  step("phone: menu opens, closes on navigation, no sideways scroll");

  await page.screenshot({ path: process.env.SHOT || "site.png", fullPage: true });
  assert.deepStrictEqual(problems, []);
  step("no errors or CSP violations");
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
