// End-to-end check of examples/todomvc against the TodoMVC app spec, in
// Chromium. Needs Playwright (a development tool, not a den dependency):
//
//   go run ./cmd/den dev examples/todomvc -addr localhost:8080 &
//   node examples/todomvc/e2e.js http://localhost:8080/
"use strict";
const { chromium } = require("playwright");
const assert = require("assert");

const url = process.argv[2] || "http://localhost:8080/";

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const problems = [];
  page.on("pageerror", (e) => problems.push(String(e)));
  page.on("console", (m) => m.type() === "error" && problems.push(m.text()));
  await page.goto(url);
  await page.evaluate(() => localStorage.clear());
  await page.reload();

  const titles = () => page.$$eval(".todo-list li label", (ls) => ls.map((l) => l.textContent));
  const count = () => page.textContent(".todo-count");
  const add = async (t) => { await page.fill(".new-todo", t); await page.press(".new-todo", "Enter"); };
  const step = (name) => console.log("ok  " + name);
  // hashchange fires after the click, so wait for the route to be applied.
  const routed = (h) => page.waitForSelector(`.filters a.selected[href='${h}']`);

  // No todos: main and footer are hidden; the new-todo input has focus.
  assert.strictEqual(await page.$(".main"), null);
  assert.strictEqual(await page.$(".footer"), null);
  assert.strictEqual(await page.evaluate(() => document.activeElement.className), "new-todo");
  step("no todos: main and footer hidden, input focused");

  // New todo: trimmed, input cleared, empty titles ignored.
  await add("  buy milk  ");
  await add("   ");
  await add("walk dog");
  await add("write code");
  assert.deepStrictEqual(await titles(), ["buy milk", "walk dog", "write code"]);
  assert.strictEqual(await page.inputValue(".new-todo"), "");
  step("new todo: trimmed, input cleared, blank ignored");

  // Counter and item state.
  assert.strictEqual(await count(), "3 items left");
  await page.check(".todo-list li:nth-child(1) .toggle");
  await page.check(".todo-list li:nth-child(2) .toggle");
  assert.strictEqual(await count(), "1 item left");
  assert.strictEqual(await page.getAttribute(".todo-list li:nth-child(1)", "class"), "completed");
  step("toggle item, completed class, pluralized counter");

  // Mark all as complete, and its checkbox follows the items.
  assert.strictEqual(await page.isChecked(".toggle-all"), false);
  await page.click(".toggle-all + label");
  assert.strictEqual(await count(), "0 items left");
  assert.strictEqual(await page.isChecked(".toggle-all"), true);
  await page.click(".toggle-all + label");
  assert.strictEqual(await count(), "3 items left");
  await page.check(".todo-list li:nth-child(3) .toggle");
  assert.strictEqual(await page.isChecked(".toggle-all"), false);
  await page.check(".todo-list li:nth-child(1) .toggle");
  await page.check(".todo-list li:nth-child(2) .toggle");
  assert.strictEqual(await page.isChecked(".toggle-all"), true);
  await page.uncheck(".todo-list li:nth-child(2) .toggle");
  await page.uncheck(".todo-list li:nth-child(3) .toggle");
  step("mark all as complete");

  // Editing: double-click focuses the edit field; Enter saves trimmed;
  // Escape cancels; blur saves; an empty title deletes the todo.
  await page.dblclick(".todo-list li:nth-child(2) label");
  assert.strictEqual(await page.getAttribute(".todo-list li:nth-child(2)", "class"), "editing");
  assert.strictEqual(await page.evaluate(() => document.activeElement.className), "edit");
  assert.strictEqual(await page.inputValue(".edit"), "walk dog");
  await page.fill(".edit", "  walk the dog  ");
  await page.press(".edit", "Enter");
  assert.deepStrictEqual(await titles(), ["buy milk", "walk the dog", "write code"]);
  assert.strictEqual(await page.$(".edit"), null);
  await page.dblclick(".todo-list li:nth-child(2) label");
  await page.fill(".edit", "discarded");
  await page.press(".edit", "Escape");
  assert.deepStrictEqual(await titles(), ["buy milk", "walk the dog", "write code"]);
  await page.dblclick(".todo-list li:nth-child(3) label");
  await page.fill(".edit", "write more code");
  await page.click("h1");
  assert.deepStrictEqual(await titles(), ["buy milk", "walk the dog", "write more code"]);
  await page.dblclick(".todo-list li:nth-child(2) label");
  await page.fill(".edit", "   ");
  await page.press(".edit", "Enter");
  assert.deepStrictEqual(await titles(), ["buy milk", "write more code"]);
  step("edit: enter saves, escape cancels, blur saves, empty deletes");

  // Routing, with the selected filter highlighted and history working.
  await add("third");
  await page.click(".filters >> text=Active");
  await routed("#/active");
  assert.strictEqual(new URL(page.url()).hash, "#/active");
  assert.deepStrictEqual(await titles(), ["write more code", "third"]);
  assert.strictEqual(await page.getAttribute(".filters a[href='#/active']", "class"), "selected");
  await page.click(".filters >> text=Completed");
  await routed("#/completed");
  assert.deepStrictEqual(await titles(), ["buy milk"]);
  await page.goBack();
  await routed("#/active");
  assert.deepStrictEqual(await titles(), ["write more code", "third"]);
  await page.click(".filters >> text=All");
  await routed("#/");
  assert.deepStrictEqual(await titles(), ["buy milk", "write more code", "third"]);
  step("routing: #/, #/active, #/completed, back button");

  // Clear completed is shown only when something is completed.
  assert.ok(await page.$(".clear-completed"));
  await page.click(".clear-completed");
  assert.deepStrictEqual(await titles(), ["write more code", "third"]);
  assert.strictEqual(await page.$(".clear-completed"), null);
  step("clear completed");

  // Destroy.
  await page.hover(".todo-list li:nth-child(1)");
  await page.click(".todo-list li:nth-child(1) .destroy");
  assert.deepStrictEqual(await titles(), ["third"]);
  step("destroy");

  // Persistence across a reload, including completed state.
  await page.check(".todo-list li:nth-child(1) .toggle");
  await add("after reload");
  await page.reload();
  assert.deepStrictEqual(await titles(), ["third", "after reload"]);
  assert.strictEqual(await page.getAttribute(".todo-list li:nth-child(1)", "class"), "completed");
  assert.strictEqual(await count(), "1 item left");
  step("persistence in localStorage");

  await page.screenshot({ path: process.env.SHOT || "todomvc.png" });
  assert.deepStrictEqual(problems, []);
  step("no errors or CSP violations");
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
