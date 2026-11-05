# Benchmarks

The Phase 1 gate on the [roadmap](../README.md#roadmap) is: den within 1.3x
of vanilla JS on the js-framework-benchmark operations.

- `den/` is the js-framework-benchmark keyed app written in den. Each row's
  label is its own signal, the idiomatic fine-grained style.
- `vanilla/` is a hand-optimized plain JS version of the same app: cloned
  rows, one delegated click listener and direct DOM updates.
- `run.js` serves both, drives them in Chromium with Playwright and compares
  them.
- `jsfb/` is den's entry for the official js-framework-benchmark harness.

The results on this page are from `run.js`, not from the official harness.
In the official harness (two runs, headless Chrome for Testing 157 on
Windows) den's weighted geometric mean was 1.11 and 1.12 against 1.00 and
1.01 for vanilla JS, and its memory after creating 1,000 rows 1.13x vanilla
JS's. [COMPARISON.md](COMPARISON.md) has those results, the other
frameworks, and how the two measurements differ.

```sh
go run ./cmd/den build bench/den
node bench/run.js 15        # iterations per operation; needs Playwright
```

Playwright is a development tool for this script only; den itself has no npm
dependencies.

## Method

Each operation runs on a fresh page after warm-up runs. It is timed from the
click to the end of the next frame (`requestAnimationFrame` then
`setTimeout`), so script, style, layout and paint are all counted.
js-framework-benchmark counts the same work but reads it from a Chrome
trace, up to the end of the compositor commit. The CPU is throttled 4x for
every operation to steady the numbers; js-framework-benchmark throttles only
five of them (4x for update, select, swap and clear, 2x for remove). Each
result is the median of the runs, and the summary is the unweighted geometric
mean of den's time over vanilla's. The script column stops once the click's
microtasks (den's update flush) have run. Memory is the JS heap
(`Runtime.getHeapUsage`) after creating 1,000 rows and forcing a garbage
collection; js-framework-benchmark reports
`performance.measureUserAgentSpecificMemory()` after a forced collection
instead.

This is a local reimplementation of the js-framework-benchmark keyed
operations, not the official harness, which needs npm and its own server.
Its vanilla JS app is `vanilla/`, not the harness's vanillajs entry.

## Results

`run.js` in Chromium (Playwright's build), Linux container, 15 runs per
operation:

| Operation | Vanilla ms | den ms | den / vanilla | Vanilla script | den script |
| --- | --- | --- | --- | --- | --- |
| Create 1,000 rows | 314.6 | 324.1 | 1.03 | 40.7 | 32.3 |
| Replace 1,000 rows | 351.8 | 337.6 | 0.96 | 51.9 | 50.0 |
| Update every 10th row | 43.4 | 46.9 | 1.08 | 1.1 | 1.1 |
| Select row | 16.2 | 16.6 | 1.02 | 0.1 | 1.2 |
| Swap rows | 37.1 | 35.9 | 0.97 | 0.1 | 1.5 |
| Remove row | 65.5 | 58.5 | 0.89 | 0.1 | 0.9 |
| Create 10,000 rows | 2856.1 | 2811.3 | 0.98 | 304.7 | 372.5 |
| Append 1,000 rows | 292.9 | 321.5 | 1.10 | 29.8 | 37.6 |
| Clear 1,000 rows | 21.9 | 22.1 | 1.01 | 18.8 | 19.7 |

**Speed: 1.00x vanilla** (geometric mean in this runner; gate 1.30).
Single operations vary between runs by more than some of the differences
shown. On script time alone den is slower on the small updates (select,
swap, remove), where vanilla touches two rows directly and den runs its
keyed diff or re-evaluates each row's binding; those are well under 2 ms
even with the CPU throttled.

**Memory: 1.15x vanilla** (JS heap after creating 1,000 rows and a full
GC: 2.24 MB against 1.94 MB; budget 1.20). What keeps it down:

- Handlers and bindings in list rows are compiled once per list, as
  functions of the row's item, instead of as closures in every row.
  Handlers are found through a `den-click="0"`-style attribute in the
  row's template, so rows store no handler state at all.
- A graph node stores a single dependency or reader directly and only
  allocates a collection when there are several; plain signals are a
  lighter object than computed values and effects.
- Scopes are linked lists through their nodes rather than arrays.
