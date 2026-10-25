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
microtasks (den's update flush) have run.

This is a local reimplementation of the js-framework-benchmark keyed
operations, not the official harness, which needs npm and its own server.

## Results

Chromium (Playwright's build), Linux container, 15 runs per operation:

| Operation | Vanilla ms | den ms | den / vanilla | Vanilla script | den script |
| --- | --- | --- | --- | --- | --- |
| Create 1,000 rows | 279.8 | 305.9 | 1.09 | 37.9 | 33.6 |
| Replace 1,000 rows | 357.3 | 376.3 | 1.05 | 58.5 | 63.1 |
| Update every 10th row | 47.7 | 52.8 | 1.11 | 1.2 | 2.3 |
| Select row | 15.8 | 16.6 | 1.05 | 0.1 | 1.4 |
| Swap rows | 36.7 | 56.9 | 1.55 | 0.1 | 2.5 |
| Remove row | 83.2 | 76.6 | 0.92 | 0.1 | 1.4 |
| Create 10,000 rows | 2702.0 | 3023.8 | 1.12 | 305.4 | 467.8 |
| Append 1,000 rows | 314.4 | 354.2 | 1.13 | 29.4 | 56.9 |
| Clear 1,000 rows | 27.0 | 29.2 | 1.08 | 23.6 | 26.0 |

**Geometric mean: 1.11x vanilla** (gate: 1.30). Single operations vary
between runs by more than the differences shown; swap rows has measured
anywhere from 1.00x to 1.55x. On script time alone den is slower on the small
updates (select, swap, remove), where vanilla touches two rows directly and
den runs its keyed diff or re-evaluates each row's binding. Those are the
first places to optimize.
