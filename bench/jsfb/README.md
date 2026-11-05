# den in the official js-framework-benchmark

[`den/`](den/) holds the source files of a `frameworks/keyed/den` entry for
[krausest/js-framework-benchmark](https://github.com/krausest/js-framework-benchmark)
(written against its commit fa510a76), byte for byte as they are in the
entry. Unlike [`bench/den`](../den), which is den's own copy of the app for
`bench/run.js`, this one renders the markup the official harness expects:
the jumbotron with its six buttons, the `test-data` table and the preloaded
glyphicon, inside `<div id="main">` (with the small differences listed
[below](#how-its-page-differs-from-vanillajs)). The results are in
[COMPARISON.md](../COMPARISON.md).

- `den/src/app.den`: the app. One `rows` signal, one `selected` signal holding
  the selected row's id, a `Label` signal per row, and a keyed `{for}`.
- `den/index.html`: the page the harness opens. It links only
  `/css/currentStyle.css` and has no inline script or style, so it passes the
  harness's `default-src 'self'` CSP check.
- `den/build.js`: the entry's `build-prod` step. It runs
  `go run github.com/TriadStudios/Den/cmd/den@<pinned version> build src -o dist`,
  or the compiler of a local den checkout when `DEN_REPO` points at one.
- `den/README.md`: the entry's own README, for the harness's readers (what the
  app does, how den is fetched, and that Go 1.24 or later is needed to build
  it).

The entry also needs a `package.json` and a `package-lock.json`, which are not
kept here because den has no npm files. The harness finds an entry only when
both exist.

## Installing into a harness clone

Requires Node.js and Go 1.24 or later. `<harness>` is the harness clone and
`<den>` a checkout of this repository.

```sh
cd <harness>
mkdir -p frameworks/keyed/den
cp -r <den>/bench/jsfb/den/. frameworks/keyed/den/
# write frameworks/keyed/den/package.json as below, then:
cd frameworks/keyed/den
npm install            # writes package-lock.json; there are no dependencies
npm run build-prod     # or: DEN_REPO=<den> npm run build-prod
cd ../../..
npm start &            # the harness server, port 8080
npm run rebuild-ci keyed/den
```

`package.json`:

```json
{
  "name": "js-framework-benchmark-keyed-den",
  "version": "1.0.0",
  "type": "module",
  "private": true,
  "description": "Den demo: Go-syntax components compiled to JavaScript",
  "license": "Apache-2.0",
  "js-framework-benchmark": {
    "frameworkVersion": "0.0.0-b8a90eaa",
    "frameworkHomeURL": "https://github.com/TriadStudios/Den",
    "language": "Go",
    "repoURL": "https://github.com/TriadStudios/Den"
  },
  "scripts": {
    "dev": "node build.js",
    "build-prod": "node build.js"
  },
  "repository": {
    "type": "git",
    "url": "https://github.com/krausest/js-framework-benchmark.git"
  }
}
```

Den has no tagged release, so `frameworkVersion` is `0.0.0` plus the pinned
commit (`b8a90eaa`). `build.js` pins the same commit by its Go
pseudo-version, `v0.0.0-20261104135801-b8a90eaaf4bf`; keep the two in step
when moving the pin.

## How its page differs from vanillajs

After "Create 1,000 rows" and selecting a row, the `#main` subtree has the
same elements, classes, ids and `aria-hidden` attributes as vanillajs. What
differs:

- the page has `<div id="app">` around `#main`, since den mounts the app into
  the element with id `app`;
- rows that are not selected have `class=""` (vanillajs has no class
  attribute until a row has been selected once): den writes the string the
  `class` binding returns;
- each row's two `<a>` elements carry `den-click="0"` and `den-click="1"`,
  which den's one document click listener uses to find row handlers;
- `<tbody>` holds two empty comment nodes, the anchors of the `{for}` list.

Den code cannot call JavaScript, so there is no `Math.random`. Labels come
from the standard word lists through a Park-Miller generator seeded with 1,
and every page load produces the same labels.
