# Performance comparison

Measured on 2026-11-04 on one Windows desktop: den against vanilla JS, Solid,
Svelte, Vue and React in the official
[js-framework-benchmark](https://github.com/krausest/js-framework-benchmark)
harness, and den's own site against a Next.js twin of it. Every result below
comes from the result files of these runs (the harness's raw JSON and its
results page, and the Lighthouse reports), with two exceptions: the figures
of den's local benchmark are quoted from [bench/README.md](README.md), and a
few facts about the Next.js twin (such as its 6 to 13 ms navigation delay)
come from the twin's parity checks. Hypotheses and estimates are marked as
such. The raw result files are not in this repository.

## Summary

- In the official harness den took about 11% more time than hand-written
  vanilla JS. Its weighted geometric mean over the nine CPU benchmarks was
  1.11 in run A and 1.12 in run B, where vanilla JS scored 1.00 and 1.01.
- Solid (1.09 and 1.13) and Svelte (1.14 and 1.14) are within run-to-run
  noise of den; den and Solid swapped places between the runs. Vue (1.18 and
  1.22) and React (1.45 and 1.50) were slower than den in both runs.
- den loses most on select row (1.51 and 1.30 times vanilla JS's time),
  clear rows (1.20 and 1.31) and swap rows (1.22 and 1.20); in run B
  partial update (1.26) was also above swap rows. On script time
  alone it is behind Solid (2.07 and 2.02 against 1.76 and 1.79) and ahead of
  Svelte, Vue and React.
- den comes closest to vanilla JS on remove row (1.03 and 0.95; in run B it
  was the fastest of all six), append rows (1.03 and 1.00) and create 10,000
  rows (1.06 and 1.07).
- Memory after creating 1,000 rows: 2.30 MB, second only to vanilla JS
  (2.03 MB) and below Solid (2.85 MB). After five create/clear cycles den
  holds 0.83 MB, against 0.65 for vanilla JS and 0.78 for Solid.
- Size: 4.2 kB brotli, second smallest after vanilla JS (2.5 kB) and just
  below Solid (4.5 kB).
- den's site ships one script of 10,210 B gzip on every page. Its Next.js 16
  twin ships 7 or 8 scripts of 140,210 to 140,703 B gzip before the load
  event. Both have a median Lighthouse score of 100 on every page, and their
  LCP medians are equal or one of Lighthouse's steps of about 60 ms apart.
  Mobile Total Blocking Time is 0 to 23 ms for den and 27 to 56 ms for Next
  (medians).
  Next prerenders HTML and den renders in the browser, so the bytes do not
  measure the same thing (see [the caveats](#caveats-of-the-site-comparison)).
- den's own `bench/run.js` reports 1.00x vanilla JS. That is a different
  measurement against a different vanilla JS app; the figure above is the one
  that compares with other frameworks. Why the two differ is not established
  ([below](#differences-from-the-local-benchmark)).
- This is two runs on one machine, headless, with other programs running.
  Single CPU benchmarks moved by up to 15% between the runs, so only the
  geometric means and the large gaps should be quoted.

## js-framework-benchmark

### Method

- Harness: krausest/js-framework-benchmark at commit fa510a76, with the den
  entry from [`jsfb/den/`](jsfb/den/) added. The puppeteer runner (the
  default of `npm run bench`) ran the default benchmark set for six keyed
  entries, twice: run A from 16:16 to 16:44 and run B from 17:02 to 17:30
  (UTC-5). Both ended with "successful run", no failed benchmarks and no
  plausibility warnings.
- CPU benchmarks 01 to 09: 15 iterations each (25 for select row), each on a
  fresh page after warm-up clicks. The results page lists 5 warm-up runs (3
  for partial update), but the code (`webdriver-ts/src/benchmarksPuppeteer.ts`)
  differs for three benchmarks: select row makes a single warm-up selection
  (its warm-up loop is commented out), swap rows swaps 6 times, and remove
  row removes 5 rows and then one more. The harness reads a
  Chrome performance trace and times each operation from the click event to
  the end of the compositor commit that follows the work it triggered, so
  script, style, layout and paint all count. Five benchmarks run with the CPU
  throttled: 4x for partial update, select, swap and clear, 2x for remove.
- Memory 21, 22 and 25: `performance.measureUserAgentSpecificMemory()` after a
  forced garbage collection, one sample each.
- Size 41 and 42 and first paint 43: one sample each. The sizes are of all
  implementation files without the CSS and HTTP headers; 42 is brotli.
- Each cell is the median with its 95% confidence interval and, in brackets,
  the slowdown: the median divided by the fastest median of the row. The
  weighted geometric mean combines the nine CPU slowdowns with the harness's
  weights (`webdriver-ts-results/src/Common.ts`), which are smallest for
  select row (0.19) and swap rows (0.13); the other seven are 0.42 to 0.64.
  Memory and size use a plain geometric mean. The results page computes the
  memory factors from the values rounded to two decimals, so they can differ
  slightly from ratios of the raw values (as in the
  [den against vanilla JS](#den-against-vanilla-js) table).
- The tables are transcriptions of the harness's results page
  (`npm run results`) in its default view, "median results" and "total
  duration", with the columns in a fixed order.

Command, from the harness root with its server (`npm start`) on port 8080:

```sh
npm run bench -- --headless true --chromeBinary "<Chrome for Testing>" \
  --framework keyed/den keyed/vanillajs keyed/react-hooks keyed/solid keyed/svelte keyed/vue
```

### Machine and browser

| | |
| --- | --- |
| CPU | AMD Ryzen 9 9950X3D2, 16 cores / 32 threads (MaxClockSpeed reported as 4300 MHz) |
| RAM | 61.7 GB |
| OS | Windows 11 Home 10.0.26200 (25H2, build 26200.9711) |
| Power plan | Balanced |
| Node.js / npm | 24.16.0 / 11.13.0 |
| Go | 1.27.3 windows/amd64 |
| Browser | Chrome for Testing 157.0.8103.38, headless (`--headless true`; the harness starts it with `--headless=new`) |

### Versions

| framework | version | harness entry |
| --- | --- | --- |
| den | commit b8a90eaa on main, reported as `0.0.0-b8a90eaa` (Go module version `v0.0.0-20261104135801-b8a90eaaf4bf`) | `keyed/den`, from [`jsfb/den/`](jsfb/den/) |
| vanilla JS | the harness's own | `keyed/vanillajs` |
| React | 19.2.0 | `keyed/react-hooks` |
| Solid | solid-js 1.9.3 | `keyed/solid` |
| Svelte | 5.42.1 | `keyed/svelte` |
| Vue | 3.5.39 | `keyed/vue` |

The other five entries are the harness's own at fa510a76, rebuilt locally with
`npm run rebuild-ci`. The den entry sits on a local branch of the harness
clone (commit eba676d) and has not been submitted upstream. Its `build.js`
builds den at the pinned version; a build with a local checkout of the same
commit gives a byte-identical `app.js`.

### Run A

CPU, median ms ± 95% CI (slowdown against the fastest of the row):

| benchmark | CPU throttle | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: | --: |
| 01 create 1,000 rows | - | 37.1 ± 1.3 (1.00) | 40.5 ± 0.9 (1.09) | 38.1 ± 1.9 (1.03) | 41.7 ± 1.7 (1.12) | 44.6 ± 1.5 (1.20) | 46.3 ± 1.7 (1.25) |
| 02 replace all 1,000 rows | - | 39.1 ± 0.9 (1.00) | 44.3 ± 1.7 (1.13) | 44.4 ± 1.9 (1.14) | 46.2 ± 2.0 (1.18) | 49.0 ± 1.9 (1.25) | 55.1 ± 3.0 (1.41) |
| 03 update every 10th row | 4x | 27.6 ± 2.6 (1.00) | 30.0 ± 2.4 (1.09) | 29.3 ± 4.1 (1.06) | 32.4 ± 2.6 (1.17) | 30.2 ± 2.8 (1.09) | 36.9 ± 1.4 (1.34) |
| 04 select row | 4x | 6.1 ± 0.3 (1.00) | 9.2 ± 0.4 (1.51) | 7.8 ± 0.5 (1.28) | 12.5 ± 0.8 (2.05) | 7.8 ± 0.5 (1.28) | 14.5 ± 0.7 (2.38) |
| 05 swap rows | 4x | 28.7 ± 1.5 (1.00) | 35.1 ± 3.4 (1.22) | 33.8 ± 1.9 (1.18) | 33.5 ± 1.4 (1.17) | 33.3 ± 1.9 (1.16) | 229.6 ± 12.6 (8.00) |
| 06 remove row | 2x | 26.8 ± 1.8 (1.00) | 27.5 ± 1.9 (1.03) | 26.7 ± 1.7 (1.00) | 26.7 ± 1.9 (1.00) | 27.9 ± 2.2 (1.04) | 28.5 ± 1.6 (1.07) |
| 07 create 10,000 rows | - | 417.4 ± 17.7 (1.00) | 443.9 ± 13.8 (1.06) | 446.4 ± 15.7 (1.07) | 435.2 ± 11.1 (1.04) | 491.8 ± 24.3 (1.18) | 587.9 ± 19.5 (1.41) |
| 08 append 1,000 rows to 1,000 | - | 41.9 ± 1.3 (1.00) | 43.3 ± 1.3 (1.03) | 43.1 ± 1.3 (1.03) | 45.3 ± 1.6 (1.08) | 46.9 ± 2.4 (1.12) | 49.6 ± 1.5 (1.18) |
| 09 clear 1,000 rows | 4x | 20.8 ± 0.5 (1.00) | 24.9 ± 1.5 (1.20) | 26.1 ± 1.4 (1.25) | 24.2 ± 1.3 (1.16) | 29.9 ± 2.1 (1.44) | 40.4 ± 2.2 (1.94) |
| **weighted geometric mean** | | **1.00** | **1.11** | **1.09** | **1.14** | **1.18** | **1.45** |

Memory, MB (factor against the smallest), one sample:

| benchmark | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: |
| 21 after page load | 0.57 (1.10) | 0.52 (1.00) | 0.60 (1.15) | 0.67 (1.29) | 0.87 (1.67) | 1.19 (2.29) |
| 22 after creating 1,000 rows | 2.03 (1.00) | 2.30 (1.13) | 2.85 (1.40) | 3.04 (1.50) | 4.07 (2.00) | 4.59 (2.26) |
| 25 after 5 x create and clear 1,000 rows | 0.65 (1.00) | 0.83 (1.28) | 0.78 (1.20) | 0.99 (1.52) | 1.19 (1.83) | 1.98 (3.05) |
| **geometric mean** | **1.03** | **1.13** | **1.25** | **1.43** | **1.83** | **2.51** |

Size and first paint (factor against the smallest), one sample:

| benchmark | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: |
| 41 uncompressed, kB | 11.7 (1.02) | 11.9 (1.03) | 11.5 (1.00) | 26.6 (2.31) | 64.3 (5.59) | 190.3 (16.55) |
| 42 brotli, kB | 2.5 (1.00) | 4.2 (1.68) | 4.5 (1.80) | 9.7 (3.88) | 23.3 (9.32) | 51.4 (20.56) |
| 43 first paint, ms | 210.7 (1.18) | 256.9 (1.44) | 218.3 (1.23) | 177.9 (1.00) | 222.1 (1.25) | 242.2 (1.36) |
| **geometric mean** | **1.06** | **1.36** | **1.30** | **2.08** | **4.02** | **7.74** |

Other views of the results page, weighted geometric mean only:

| view | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: |
| mean, total duration | 1.00 | 1.12 | 1.11 | 1.15 | 1.21 | 1.47 |
| median, only JS duration | 1.02 | 2.07 | 1.76 | 2.39 | 3.47 | 5.54 |
| mean, only JS duration | 1.02 | 2.06 | 1.75 | 2.34 | 3.46 | 5.46 |

### Run B

CPU, median ms ± 95% CI (slowdown against the fastest of the row):

| benchmark | CPU throttle | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: | --: |
| 01 create 1,000 rows | - | 34.0 ± 1.2 (1.00) | 36.0 ± 1.1 (1.06) | 39.5 ± 1.4 (1.16) | 35.9 ± 0.8 (1.06) | 40.5 ± 1.0 (1.19) | 45.1 ± 2.2 (1.33) |
| 02 replace all 1,000 rows | - | 35.1 ± 0.8 (1.00) | 40.0 ± 1.2 (1.14) | 44.6 ± 1.7 (1.27) | 43.5 ± 1.4 (1.24) | 45.3 ± 1.2 (1.29) | 49.7 ± 1.2 (1.42) |
| 03 update every 10th row | 4x | 23.4 ± 1.8 (1.00) | 29.6 ± 2.7 (1.26) | 28.7 ± 2.1 (1.23) | 30.2 ± 3.1 (1.29) | 29.9 ± 2.9 (1.28) | 39.3 ± 2.7 (1.68) |
| 04 select row | 4x | 6.0 ± 0.5 (1.00) | 7.8 ± 0.4 (1.30) | 7.1 ± 0.3 (1.18) | 10.9 ± 0.7 (1.82) | 7.7 ± 0.3 (1.28) | 13.1 ± 0.6 (2.18) |
| 05 swap rows | 4x | 26.5 ± 1.7 (1.00) | 31.7 ± 1.5 (1.20) | 29.9 ± 2.4 (1.13) | 33.7 ± 2.4 (1.27) | 34.0 ± 2.7 (1.28) | 207.6 ± 6.2 (7.83) |
| 06 remove row | 2x | 24.4 ± 1.6 (1.05) | 23.3 ± 1.3 (1.00) | 24.6 ± 1.3 (1.06) | 24.8 ± 1.4 (1.06) | 28.1 ± 1.4 (1.21) | 27.3 ± 1.6 (1.17) |
| 07 create 10,000 rows | - | 413.2 ± 7.4 (1.00) | 442.4 ± 10.2 (1.07) | 450.2 ± 13.5 (1.09) | 441.8 ± 14.1 (1.07) | 455.1 ± 14.6 (1.10) | 593.2 ± 23.8 (1.44) |
| 08 append 1,000 rows to 1,000 | - | 41.3 ± 2.0 (1.00) | 41.5 ± 1.1 (1.01) | 41.1 ± 1.6 (1.00) | 41.2 ± 1.5 (1.00) | 42.8 ± 1.3 (1.04) | 47.8 ± 1.5 (1.16) |
| 09 clear 1,000 rows | 4x | 20.6 ± 0.9 (1.00) | 26.9 ± 1.2 (1.31) | 23.3 ± 0.8 (1.13) | 22.8 ± 0.7 (1.11) | 31.3 ± 1.3 (1.52) | 35.2 ± 1.1 (1.71) |
| **weighted geometric mean** | | **1.01** | **1.12** | **1.13** | **1.14** | **1.22** | **1.50** |

den was the fastest on 06 remove row in this run, so vanilla JS's slowdown
there is 1.05 and its geometric mean 1.01.

Memory, MB (factor against the smallest), one sample:

| benchmark | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: |
| 21 after page load | 0.57 (1.00) | 0.60 (1.05) | 0.60 (1.05) | 0.68 (1.19) | 0.88 (1.54) | 1.18 (2.07) |
| 22 after creating 1,000 rows | 2.03 (1.00) | 2.30 (1.13) | 2.85 (1.40) | 3.03 (1.49) | 4.07 (2.00) | 4.58 (2.26) |
| 25 after 5 x create and clear 1,000 rows | 0.66 (1.00) | 0.82 (1.24) | 0.78 (1.18) | 0.99 (1.50) | 1.18 (1.79) | 1.99 (3.02) |
| **geometric mean** | **1.00** | **1.14** | **1.20** | **1.39** | **1.77** | **2.41** |

Size and first paint (factor against the smallest), one sample:

| benchmark | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: |
| 41 uncompressed, kB | 11.7 (1.02) | 11.9 (1.03) | 11.5 (1.00) | 26.6 (2.31) | 64.3 (5.59) | 190.3 (16.55) |
| 42 brotli, kB | 2.5 (1.00) | 4.2 (1.68) | 4.5 (1.80) | 9.7 (3.88) | 23.3 (9.32) | 51.4 (20.56) |
| 43 first paint, ms | 219.6 (1.13) | 205.6 (1.05) | 195.2 (1.00) | 224.5 (1.15) | 225.0 (1.15) | 318.0 (1.63) |
| **geometric mean** | **1.05** | **1.22** | **1.22** | **2.18** | **3.92** | **8.21** |

Other views of the results page, weighted geometric mean only:

| view | vanillajs | den | Solid | Svelte | Vue | React |
| --- | --: | --: | --: | --: | --: | --: |
| mean, total duration | 1.00 | 1.12 | 1.12 | 1.14 | 1.23 | 1.48 |
| median, only JS duration | 1.00 | 2.02 | 1.79 | 2.30 | 3.49 | 5.57 |
| mean, only JS duration | 1.00 | 2.00 | 1.77 | 2.25 | 3.43 | 5.44 |

### Run A against run B

18 of the 90 framework and benchmark cells moved by more than 10% between the
runs in their median or, for the CPU benchmarks, their mean; 15 cells (10 of
them CPU) moved by more than 10% in their median alone. Every CPU cell among
them was faster in B, vanilla JS included (02 replace 39.1 to 35.1 ms, 03
update 27.6 to 23.4 ms), and the largest CPU moves were about 15%. The weighted geometric means, recomputed from the raw
JSON, moved by at most 4.5% (Solid; den 1.2%). Sizes were the same in both
runs; first paint moved by up to 31% (React 242.2 to 318.0 ms) and is not a
result to quote from one sample. Why B was faster is not known; the Balanced
power plan and the different background load (see
[machine load](#machine-load)) are possible causes that were not tested.

### den against vanilla JS

den's median divided by vanilla JS's (from the raw JSON):

| benchmark | A den | A vanilla JS | A den / vanilla JS | B den | B vanilla JS | B den / vanilla JS |
| --- | --: | --: | --: | --: | --: | --: |
| 01 create 1,000 rows, ms | 40.5 | 37.1 | 1.092 | 36.0 | 34.0 | 1.059 |
| 02 replace all 1,000 rows, ms | 44.3 | 39.1 | 1.133 | 40.0 | 35.1 | 1.140 |
| 03 update every 10th row, ms | 30.0 | 27.6 | 1.087 | 29.6 | 23.4 | 1.265 |
| 04 select row, ms | 9.2 | 6.1 | 1.508 | 7.8 | 6.0 | 1.300 |
| 05 swap rows, ms | 35.1 | 28.7 | 1.223 | 31.7 | 26.5 | 1.196 |
| 06 remove row, ms | 27.5 | 26.8 | 1.026 | 23.3 | 24.4 | 0.955 |
| 07 create 10,000 rows, ms | 443.9 | 417.4 | 1.063 | 442.4 | 413.2 | 1.071 |
| 08 append 1,000 rows to 1,000, ms | 43.3 | 41.9 | 1.033 | 41.5 | 41.3 | 1.005 |
| 09 clear 1,000 rows, ms | 24.9 | 20.8 | 1.197 | 26.9 | 20.6 | 1.306 |
| 21 memory after page load, MB | 0.52 | 0.57 | 0.919 | 0.60 | 0.57 | 1.048 |
| 22 memory after 1,000 rows, MB | 2.30 | 2.03 | 1.133 | 2.30 | 2.03 | 1.135 |
| 25 memory after 5 create/clear cycles, MB | 0.83 | 0.65 | 1.276 | 0.82 | 0.66 | 1.248 |
| 41 size uncompressed, kB | 11.9 | 11.7 | 1.017 | 11.9 | 11.7 | 1.017 |
| 42 size brotli, kB | 4.2 | 2.5 | 1.680 | 4.2 | 2.5 | 1.680 |
| 43 first paint, ms | 256.9 | 210.7 | 1.219 | 205.6 | 219.6 | 0.936 |

The ratios are of the unrounded values. The weighted geometric mean of the
nine CPU ratios, with the harness's weights, is 1.107 in run A and 1.113 in
run B.

The [roadmap](../README.md#roadmap)'s budgets are 1.3x vanilla JS's time on
the geometric mean and 1.2x its memory after creating 1,000 rows. Here they hold at 1.11 and 1.13;
single operations go above 1.3x (select row in run A, clear rows in run B).

### Where den loses, and why it might

These are hypotheses from reading the den entry and den's runtime
(`compiler/runtime.js`). None of them has been profiled, and den's compiler
and runtime were not changed for these runs. Script times are the harness's
"only JS duration" medians.

1. **Select row** (1.51 and 1.30 against vanilla JS; Solid 1.28 and 1.18,
   Vue 1.28 and 1.28). Script time was 2.6 ms for den against 2.0 for Solid
   and Vue and 0.4 for vanilla JS in run A (2.2 against 1.4, 2.0 and 0.6 in
   run B). Hypothesis: every row binds `class={danger(selected.Get() == r.ID)}`,
   so one `selected.Set` re-runs 1,000 class bindings. Vanilla JS touches the
   two rows that change, and Solid's entry uses a selector
   (`createSelector`) that notifies only those two rows.
2. **Swap rows** (1.22 and 1.20; Solid 1.18 and 1.13). Script time 4.3 ms for
   den against 1.6 for Solid, 2.1 for Vue, 2.9 for Svelte and 0.4 for vanilla
   JS in run A (3.9 against 1.6, 2.0, 2.4 and 0.4 in run B). Hypothesis: the
   entry copies the slice, swaps two rows and sets it. den's keyed reconcile
   skips the common prefix and suffix (only rows 0 and 999 match), then puts
   the 998 rows in between into a `Map`, looks each new key up, and runs a
   longest-increasing-subsequence pass over those 998 entries to find the
   two rows to move. There is no special case for a swap, so moving two rows
   costs work in proportion to the list.
3. **Clear rows** (1.20 and 1.31). Vanilla JS leads every framework here
   (Solid 1.25 and 1.13, Svelte 1.16 and 1.11). Script time was 20.2 ms for
   den, 21.4 for Solid, 20.3 for Svelte and 16.9 for vanilla JS in run A, so
   on par there; in run B den's 22.0 ms was above Solid's 19.3 and Svelte's
   18.1 (vanilla JS 16.5). den already empties `<tbody>` in one step
   (`textContent = ""`) when the list fills it. Hypothesis: the rest is the
   per-row disposal, freeing each row's bindings and its subscription to
   `selected`, which vanilla JS does not have.
4. **Memory after five create/clear cycles** (0.83 and 0.82 MB against
   0.65 and 0.66 for vanilla JS, 0.78 for Solid). About 0.16 to 0.18 MB more
   than vanilla JS stays reachable after the list is empty again. The cause
   is not known; no heap snapshot was taken.
5. **Script time overall.** On the "only JS duration" view den's weighted
   geometric mean is 2.07 and 2.02, against 1.76 and 1.79 for Solid: den runs
   more script than Solid. It shows less in the totals because style, layout
   and paint take most of the time in most benchmarks.
6. **Size.** 4.2 kB brotli against 2.5 for vanilla JS (and 4.5 for Solid);
   11.9 kB uncompressed against 11.7 for vanilla JS and 11.5 for Solid.

First paint put den last in run A (256.9 ms) and second in run B (205.6 ms).
It is one sample per run and says nothing either way.

## den's site against a Next.js twin

### What was compared

- **den:** den's product site, `examples/site` on the `feat/showcase-site`
  branch (commit c823c79, three commits after the benchmarked b8a90eaa; they
  add the site, its tests and README entries and do not change the compiler
  or the runtime), built with `go run ./cmd/den build examples/site`. Five pages,
  client-side routing, page transitions, a stored theme and live demos; one
  `app.js` of 35,286 B (10,210 B gzip).
- **Next.js:** the same site rewritten for this comparison in Next.js 16.4.5
  (App Router, static export, Turbopack build), React (the
  19.3.0-canary-278794d7-20261002 build that Next vendors for the App Router)
  and TypeScript 7.0.2. Same pages, URLs, text, `style.css` and behaviour,
  checked with parity scripts (DOM, screenshots, computed styles, den's own
  end-to-end test, a frame-by-frame check of the page transitions); the
  differences are listed in [the caveats](#caveats-of-the-site-comparison).
- **Measuring:** Lighthouse 13.6.0 (performance only) through
  chrome-launcher 1.2.2 on Node.js 24.16.0, in the same headless Chrome for
  Testing 157.0.8103.38, on the same machine, from 16:47 to 16:59 between the
  two harness runs. Pages `/`, `/features`, `/performance`, `/docs/install`
  and `/why`; presets "desktop" (Lighthouse's desktop config) and "mobile"
  (its default), both with simulated throttling; 5 runs per page, build and
  preset, each in a fresh browser: 100 runs, plus 4 untimed warm-up runs that
  were dropped. The two builds of each page and preset ran back to back and
  took turns going first (25 times each). Lighthouse's benchmark index was
  5023 (median).
- **Serving:** both builds from the same small static server on 127.0.0.1,
  HTTP/1.1, every file gzip-compressed at level 9 before the first request,
  `Cache-Control: no-cache`. Unknown paths get den's `index.html` (status
  200) or Next's `404.html` (status 404), as each framework expects.
- **Bytes:** from the network records of the first Lighthouse run of each
  page, build and preset; each same-origin response was fetched again and
  compressed with Node's zlib (gzip level 9, brotli quality 11). Requests
  are split at the load event. The brotli column is computed only: the
  server sent gzip.

### JavaScript per page

Before the load event, desktop preset (the mobile preset loads the same
scripts before load). B = bytes.

| page | den requests | den raw | den gzip | den brotli | Next requests | Next raw | Next gzip | Next brotli |
| --- | --: | --: | --: | --: | --: | --: | --: | --: |
| `/` | 1 | 35,286 | 10,210 | 8,899 | 8 | 471,353 | 140,526 | 120,492 |
| `/features` | 1 | 35,286 | 10,210 | 8,899 | 8 | 471,765 | 140,703 | 120,654 |
| `/performance` | 1 | 35,286 | 10,210 | 8,899 | 7 | 470,882 | 140,210 | 120,236 |
| `/docs/install` | 1 | 35,286 | 10,210 | 8,899 | 7 | 470,882 | 140,210 | 120,236 |
| `/why` | 1 | 35,286 | 10,210 | 8,899 | 7 | 470,882 | 140,210 | 120,236 |

Next's JavaScript is about 13.7 times den's, gzip. After the load event Next
prefetches the routes of the links in view: 1 or 2 more scripts totalling
316 to 809 B gzip (desktop; 316 to 493 B on mobile), HEAD requests for the
linked routes, and React Server Components payloads of 2,281 to 16,037 B
gzip. den makes no script or data requests after load (both builds request
`/favicon.ico`, which neither site has).

### Everything else on first load

gzip bytes, desktop preset:

| page | den HTML | den CSS (2 files) | Next HTML | of which inline scripts (raw / gzip) | Next CSS (2 files) |
| --- | --: | --: | --: | --: | --: |
| `/` | 334 | 5,213 | 3,843 | 8,819 / 2,685 | 4,463 |
| `/features` | 334 | 5,213 | 4,070 | 11,708 / 2,843 | 4,463 |
| `/performance` | 334 | 5,213 | 3,737 | 11,873 / 2,660 | 4,463 |
| `/docs/install` | 334 | 5,213 | 2,965 | 7,458 / 2,089 | 4,463 |
| `/why` | 334 | 5,213 | 3,261 | 8,113 / 2,289 | 4,463 |

den's HTML is an empty shell (580 B raw); the page's text is in `app.js`.
Next's HTML holds the prerendered page plus its React Server Components
payload as inline scripts (the inline-script column is measured on its own
and is part of the HTML figure). den's CSS is unminified (17,996 B raw);
Next minifies the same rules (15,164 B raw).

### Lighthouse

Median [min–max] over 5 runs. CLS was 0 in every run of both builds.

Desktop:

| page | build | score | FCP ms | LCP ms | TBT ms | Speed Index ms |
| --- | --- | --: | --: | --: | --: | --: |
| `/` | den | 100 [100–100] | 300 [240–300] | 300 [240–300] | 0 [0–0] | 335 [301–382] |
| `/` | Next | 100 [100–100] | 300 [300–300] | 300 [300–300] | 0 [0–0] | 302 [300–310] |
| `/features` | den | 100 [100–100] | 241 [241–300] | 241 [241–300] | 0 [0–0] | 241 [241–300] |
| `/features` | Next | 100 [100–100] | 300 [241–300] | 300 [241–300] | 0 [0–0] | 300 [241–300] |
| `/performance` | den | 100 [100–100] | 241 [240–300] | 241 [240–300] | 0 [0–0] | 249 [244–319] |
| `/performance` | Next | 100 [100–100] | 300 [300–300] | 300 [300–300] | 0 [0–0] | 300 [300–300] |
| `/docs/install` | den | 100 [100–100] | 300 [240–300] | 300 [240–300] | 0 [0–0] | 300 [240–300] |
| `/docs/install` | Next | 100 [100–100] | 300 [241–300] | 300 [241–300] | 0 [0–0] | 300 [241–300] |
| `/why` | den | 100 [100–100] | 300 [241–300] | 300 [241–300] | 0 [0–0] | 300 [251–300] |
| `/why` | Next | 100 [100–100] | 300 [300–300] | 300 [300–300] | 0 [0–0] | 300 [300–300] |

Mobile:

| page | build | score | FCP ms | LCP ms | TBT ms | Speed Index ms |
| --- | --- | --: | --: | --: | --: | --: |
| `/` | den | 100 [100–100] | 901 [900–960] | 901 [900–960] | 23 [21–48] | 960 [900–975] |
| `/` | Next | 100 [99–100] | 960 [960–960] | 960 [960–960] | 56 [49–94] | 960 [960–1049] |
| `/features` | den | 100 [100–100] | 900 [900–960] | 900 [900–960] | 4 [1–33] | 900 [900–960] |
| `/features` | Next | 100 [100–100] | 901 [900–960] | 901 [900–960] | 27 [24–54] | 901 [900–960] |
| `/performance` | den | 100 [100–100] | 960 [900–960] | 960 [900–960] | 0 [0–11] | 960 [900–960] |
| `/performance` | Next | 100 [100–100] | 960 [900–960] | 960 [900–960] | 42 [25–52] | 960 [900–974] |
| `/docs/install` | den | 100 [100–100] | 960 [900–960] | 960 [900–960] | 0 [0–0] | 960 [900–960] |
| `/docs/install` | Next | 100 [100–100] | 960 [960–960] | 960 [960–960] | 52 [47–83] | 960 [960–960] |
| `/why` | den | 100 [100–100] | 960 [900–960] | 960 [900–960] | 0 [0–0] | 960 [900–960] |
| `/why` | Next | 100 [100–100] | 901 [901–960] | 901 [901–960] | 47 [26–71] | 901 [901–1042] |

Every median score is 100 for both builds (Next's lowest single score is 99,
on `/` mobile). Desktop TBT is 0 for both everywhere. Lighthouse's simulated
throttling puts FCP and LCP on steps of about 60 ms, so those hardly separate
the builds: desktop LCP medians are 241 to 300 ms for den and 300 ms for
Next, mobile 900 to 960 ms for both. Mobile TBT does separate them: medians
of 0 to 23 ms for den against 27 to 56 ms for Next.

### Caveats of the site comparison

- **Different architectures.** Next prerenders every page to HTML at build
  time and hydrates it; den's `index.html` is an empty `<div id="app">` and
  `app.js` builds the page in the browser. den's 10,210 B therefore carry the
  whole site (every page's text, the router, the demos), while Next's text
  is in each page's HTML and its scripts are React, the Next router and the
  page's client components. Next's page shows content before any script has
  run; den's shows nothing until `app.js` has run. den has no server
  rendering yet (it is on the [roadmap](../README.md#roadmap)'s phase 3), so
  this compares den as it is today with Next's static export, not two
  equivalent builds.
- **Trusted Types.** den's site sets its CSP in a `<meta>` tag, including
  `require-trusted-types-for 'script'; trusted-types 'none'`. The Next build
  gets the same policy in a `<meta>` tag (added after the build, with the
  hashes of its inline scripts) but without those two directives:
  Turbopack's runtime loads route chunks with `script.src = url`, which
  Trusted Types blocks, and Next creates no policy.
- **CSS.** Next minifies `style.css` and merges it with the tokens and scoped
  styles; den ships it as written. Next's CSS pipeline also needed a
  one-rule fix to keep the header's `backdrop-filter`.
- **Serving.** HTTP/1.1 on localhost, for both. A real host would serve
  Next's 7 or 8 scripts over HTTP/2; that was not measured. Lighthouse's
  timings are modelled from the trace (simulated throttling), not measured
  on a real phone or network.
- **Page transitions** in the Next app keep the leaving page mounted with a
  Next internal (`LayoutRouterContext`, the usual "frozen router" approach),
  which is not public API and can break on a Next upgrade.
- **Windows build fix.** On Windows, Next 16.4.5's static export writes the
  per-segment prefetch files with a `\` in their names, so every prefetch
  would fail with 404. The twin's post-build script (`postbuild.mjs`) renames
  them to the names the client requests (what the same build writes on
  Linux).
- **Parity is close, not exact.** The canonical DOM is identical on 9 of 11
  URLs (the other two differ by a server-rendered `value` attribute and an
  empty text node); 22 of 32 screenshots are byte-identical and the other 10
  differ by at most 2 levels of one colour channel, from Next's CSS minifier
  rounding colours. den's own end-to-end test of its site passes on the Next
  build only with two changes: waiting for the new page (Next puts it in the
  DOM about 6 to 13 ms after the click, in one informal run, against about
  1 ms for den), and allowing the browser's console message for the not
  found page's 404 status (a static export answers unknown paths with
  `404.html` and status 404).
- **Who wrote it.** The Next app was written for this comparison by den's
  authors, to match den's site as closely as possible. It is not an
  independent or expert-tuned Next.js app.
- **Load.** A game ran during the last 3.5 of the measurement's 12 minutes,
  roughly the last 30% of the Lighthouse runs (an estimate: the runs carry no
  timestamps; see [machine load](#machine-load)).
- **Not reproducible from here yet.** The Next.js app, its parity checks and
  the measuring script live outside this repository and have not been
  published.

## Differences from the local benchmark

den's own [`bench/run.js`](run.js) reports 1.00x vanilla JS's time and 1.15x
its memory ([bench/README.md](README.md)). The official harness gives 1.11
and 1.12 for den against 1.00 and 1.01 for vanilla JS, and 1.13x its memory
after 1,000 rows. They are different measurements:

| | `bench/run.js` | js-framework-benchmark |
| --- | --- | --- |
| vanilla JS | `bench/vanilla/app.js`, written for this repository | the harness's `keyed/vanillajs` entry |
| den app | `bench/den/app.den` | `bench/jsfb/den/src/app.den` (the same logic in the harness's markup) |
| page CSS | `bench/den/style.css`, a short stylesheet of its own | the harness's `/css/currentStyle.css` (Bootstrap) |
| click | `element.click()` called inside the page | puppeteer's element click (input events through the DevTools protocol) |
| clock | `performance.now()` in the page, from the click to a timer queued from the next `requestAnimationFrame` | Chrome trace, from the click event to the end of the compositor commit after the work |
| CPU throttle | 4x on all nine operations | 4x on partial update, select, swap and clear, 2x on remove, none on the other four |
| iterations | 15 per operation (as run for the published table), fresh page each | 15 (25 for select row), fresh page each |
| summary | plain geometric mean of den / vanilla JS | weighted geometric mean of each framework's slowdown against the fastest |
| memory | JS heap (`Runtime.getHeapUsage`) after a forced GC, median of 3 page loads | `performance.measureUserAgentSpecificMemory()` after a forced GC, one sample |
| browser and OS | Playwright's Chromium build, Linux container | Chrome for Testing 157, headless, Windows 11 |

Per operation, den / vanilla JS:

| operation | `bench/run.js` | harness run A | harness run B |
| --- | --: | --: | --: |
| create 1,000 rows | 1.03 | 1.092 | 1.059 |
| replace 1,000 rows | 0.96 | 1.133 | 1.140 |
| update every 10th row | 1.08 | 1.087 | 1.265 |
| select row | 1.02 | 1.508 | 1.300 |
| swap rows | 0.97 | 1.223 | 1.196 |
| remove row | 0.89 | 1.026 | 0.955 |
| create 10,000 rows | 0.98 | 1.063 | 1.071 |
| append 1,000 rows | 1.10 | 1.033 | 1.005 |
| clear 1,000 rows | 1.01 | 1.197 | 1.306 |

Which of the differences above accounts for the gap is not established:
`run.js` has not been run on the machine and browser used here, and the
harness has not been run with `run.js`'s settings. Two observations:

- The weighting is not the cause. select row and swap rows, two of den's
  worst ratios in the harness, carry the smallest weights, so a plain
  geometric mean of the same harness ratios puts den further from vanilla
  JS, not closer.
- The largest disagreements are on select row, swap rows and clear rows.
  `run.js` stops its clock in a timer queued from the next animation frame,
  so its totals include the wait for that frame, and an update much shorter
  than a frame measures about one frame. For select row it measured 16.2 ms
  for vanilla JS and 16.6 ms for den, although den's script took 1.2 ms
  against 0.1 ms. The harness stops at the end of the commit and shows the
  difference (6.1 against 9.2 ms in run A). This may hide small differences
  in `run.js`; it has not been tested.

The memory ratios agree: 1.15x in `run.js` (2.24 against 1.94 MB of JS heap)
and 1.13x in the harness (2.30 against 2.03 MB).

## Caveats of the harness runs

- **Headless.** The official krausest results are run with a visible browser
  window; these runs were headless.
- **Not a benchmark machine.** A Windows desktop on the Balanced power plan,
  with other programs open and some of them busy (see
  [machine load](#machine-load)).
- **Single samples.** Memory (21, 22, 25), size (41, 42) and first paint (43)
  have one sample per run, as the harness takes them, and no confidence
  interval.
- **Local harness changes.** None touch how anything is measured.
  - A one-line patch in `browserPath()` of
    `webdriver-ts/src/playwrightAccess.ts` and
    `webdriver-ts/src/puppeteerAccess.ts` (two files) returns
    `$CHROME_PATH` when it is set, so that `npm run rebuild-ci`, which passes
    no browser path, could use Chrome for Testing. The benchmark runs passed
    `--chromeBinary`, which takes precedence.
  - `npm run results` fails on Windows when
    `webdriver-ts-results/overview.html` is checked out with CRLF line
    endings ("Marker region 'latest' not found"); it ran on an LF copy of the
    same file.
  - The root `npm ci` needs `--legacy-peer-deps` with npm 11.13 (an ESLint
    peer dependency conflict); the lockfile was not changed.
  - The results page shows a browser badge "Chrome 156.0.8078.48". That
    string is hard-coded in `webdriver-ts-results/src/App.tsx`; the browser
    used was Chrome for Testing 157.0.8103.38.
- **den's page is not identical to vanillajs's.** After "Create 1,000 rows"
  and selecting a row, `#main` has the same elements, classes, ids and
  `aria-hidden` attributes as vanillajs, with four differences: a
  `<div id="app">` around `#main` (den mounts into it), `class=""` on rows
  that are not selected, `den-click="0"` and `den-click="1"` on each row's
  two links (how den's single document listener finds row handlers), and two
  empty comment nodes in `<tbody>` (the list's anchors). The entry passes
  the harness's checks: the smoke test, the keyed check and the CSP check.
- **Labels.** den code cannot call JavaScript, so the den entry draws labels
  from the standard word lists with a Park-Miller generator seeded with 1
  instead of `Math.random`. Every page load gets the same labels; vanillajs
  gets new ones each time. The labels' lengths follow the same distribution
  as with vanillajs's generator (checked by generating den's labels and
  comparing their lengths with what vanillajs's generator gives).

## Machine load

A logger sampled the machine about every 15 seconds. Before each of run A,
the Next.js measurement and run B, a gate checked that the mean total CPU over
60 seconds was below 25%; it was 4.4%, 4.3% and 4.9%, so none had to wait.

| phase | from | to | samples | total CPU median | total CPU max | samples over 25% |
| --- | --- | --- | --: | --: | --: | --: |
| run A | 16:16 | 16:44 | 112 | 10.1% | 27.1% | 1 |
| Next.js measurement | 16:47 | 16:59 | 48 | 8.6% | 23.6% | 0 |
| run B | 17:02 | 17:30 | 111 | 7.4% | 19.8% | 0 |

- **Run A:** a Go test run from other work on the machine (`compiler.test`
  and `go`) came and went. A test process was present in 66 of the 112
  samples, using at most 6.7% of the machine (about two threads).
- **Next.js measurement and run B:** a game ran from 16:55:53 to
  17:07:48, using up to 11.5% of the machine's CPU in the samples, plus GPU
  time the logger does not see. That covers the last 3.5 of the Next.js
  measurement's 12 minutes (roughly the last 30% of the Lighthouse runs) and
  run B's 01 create rows, 02 replace rows and the start of 03 partial
  update. Run B had no test processes.
- A web browser, chat apps and game launchers were open throughout, each at
  about 1% of the machine or less in the samples during the runs (one sample
  of another Chrome process reached 1.5%).

The logger misses processes that start and end between two samples; the
total CPU column is the reliable figure.

## Not measured

- **Startup** (the harness's 30_startup, Lighthouse metrics per entry): it
  does not run in this version of the harness.
- **Server rendering:** den has none yet ([roadmap](../README.md#roadmap)
  phase 3), so the site comparison is client rendering against prerendered
  HTML.
- **HTTP/2, a real host, real devices and networks:** everything was served
  from localhost over HTTP/1.1, and Lighthouse's timings are simulated.
- **Other systems and browsers:** no Linux or macOS, no Firefox or Safari, no
  headful run.
- **Other frameworks:** only five of the harness's entries, keyed only.
- **Repeated memory and size samples,** and a heap profile of den after the
  create/clear cycles.

## Reproducing

js-framework-benchmark, with `<harness>` a clone of it and `<den>` a
checkout of this repository:

```sh
git clone https://github.com/krausest/js-framework-benchmark <harness>
cd <harness>
git checkout fa510a76
npm ci --legacy-peer-deps              # npm 11 needs the flag
npm run install-server
npm run install-webdriver-ts
npm run install-webdriver-ts-results
npx @puppeteer/browsers install chrome@157.0.8103.38 --path .chrome

# the den entry
mkdir -p frameworks/keyed/den
cp -r <den>/bench/jsfb/den/. frameworks/keyed/den/
# write frameworks/keyed/den/package.json as given in bench/jsfb/README.md, then:
(cd frameworks/keyed/den && npm install)   # writes package-lock.json; no dependencies

npm start                              # the harness server on port 8080; leave it running
# in a second shell, from <harness>:
for f in den vanillajs react-hooks solid svelte vue; do npm run rebuild-ci keyed/$f; done
npm run bench -- --headless true --chromeBinary "<Chrome for Testing>" \
  --framework keyed/den keyed/vanillajs keyed/react-hooks keyed/solid keyed/svelte keyed/vue
npm run results
# then open http://localhost:8080/webdriver-ts-results/dist/index.html
```

`<Chrome for Testing>` is the `chrome` executable that `@puppeteer/browsers`
installed under `.chrome/` (157.0.8103.38 was the stable version on
2026-11-04). `npm run rebuild-ci` passes no browser path and looks for Chrome
in its usual place; to point it at Chrome for Testing instead, add the
`CHROME_PATH` line described in the caveats and run
`npm run compile-webdriver-ts`. On Windows, if `npm run results` reports
"Marker region 'latest' not found", delete
`webdriver-ts-results/overview.html` and check it out again with LF line
endings (`git -c core.autocrlf=false checkout-index -f webdriver-ts-results/overview.html`).
`npm run results` also rewrites two tracked files,
`webdriver-ts/results.json` and `webdriver-ts-results/src/results.ts`.

den's local benchmark:

```sh
go run ./cmd/den build bench/den
node bench/run.js 15        # needs Playwright
```

The site comparison cannot be reproduced from this repository yet: the
Next.js twin and its measuring script are not published.
