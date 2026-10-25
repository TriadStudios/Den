# Benchmarks

The Phase 1 gate in [docs/PLAN.md](../docs/PLAN.md) is: den within 1.3x of
vanilla JS on the js-framework-benchmark operations.

- `den/` is the js-framework-benchmark keyed app written in den. Each row's
  label is its own signal, the idiomatic fine-grained style.
- `vanilla/` is a hand-optimized plain JS version of the same app: cloned
  rows, one delegated click listener and direct DOM updates.
- `run.js` serves both, drives them in Chromium with Playwright and compares
  them.

```sh
go run ./cmd/den build bench/den
node bench/run.js 15        # iterations per operation; needs Playwright
```

Playwright is a development tool for this script only; den itself has no npm
dependencies.

## Method

Each operation runs on a fresh page after warm-up runs. It is timed from the
click to the end of the next frame (`requestAnimationFrame` then
`setTimeout`), so script, style, layout and paint are all counted, which is
also what js-framework-benchmark reports. The CPU is throttled 4x, as
js-framework-benchmark does for its slower cases, to steady the numbers. Each
result is the median of the runs, and the summary is the unweighted geometric
mean of den's time over vanilla's. The script column stops once the click's
microtasks (den's update flush) have run. Memory is the JS heap
(`Runtime.getHeapUsage`) after creating 1,000 rows and forcing a garbage
collection, which is what js-framework-benchmark's memory results report.

This is a local reimplementation of the js-framework-benchmark keyed
operations, not the official harness, which needs npm and its own server.

## Results

Chromium (Playwright's build), Linux container, 15 runs per operation:

| Operation | Vanilla ms | den ms | den / vanilla | Vanilla script | den script |
| --- | --- | --- | --- | --- | --- |
| Create 1,000 rows | 284.6 | 312.6 | 1.10 | 29.0 | 32.6 |
| Replace 1,000 rows | 347.0 | 375.8 | 1.08 | 54.4 | 59.1 |
| Update every 10th row | 54.3 | 47.3 | 0.87 | 1.4 | 1.1 |
| Select row | 16.8 | 16.2 | 0.96 | 0.2 | 0.6 |
| Swap rows | 39.4 | 35.1 | 0.89 | 0.1 | 1.7 |
| Remove row | 63.9 | 61.6 | 0.96 | 0.1 | 1.3 |
| Create 10,000 rows | 2888.1 | 3107.6 | 1.08 | 312.5 | 470.3 |
| Append 1,000 rows | 314.4 | 355.1 | 1.13 | 27.4 | 49.5 |
| Clear 1,000 rows | 23.4 | 24.8 | 1.06 | 21.1 | 22.3 |

**Speed: 1.01x vanilla** (geometric mean; gate 1.30). Single operations vary
between runs by more than some of the differences shown. On script time
alone den is slower on the small updates (select, swap, remove), where
vanilla touches two rows directly and den runs its keyed diff or
re-evaluates each row's binding, and on create 10,000 rows.

**Memory: 1.26x vanilla** (JS heap after creating 1,000 rows and a full
GC: 2.45 MB against 1.94 MB; budget 1.20). This budget is **not met yet**.
Each den row holds about 360 bytes of reactive state that vanilla keeps
nowhere: the label signal and two binding effects (graph nodes), their
closures, the row record and the row's scope. Counting the DOM and layout
memory Chromium holds for the rows, den uses 1.07x vanilla. The next steps
are linking scopes through their nodes instead of arrays, and binding a
bare signal read (`{r.Label.Get()}`) without a closure.
