# Den keyed benchmark

[Den](https://github.com/TriadStudios/Den) compiles components written in Go
syntax (`.den` files) to JavaScript. This implementation uses den at GitHub
main commit [b8a90eaa](https://github.com/TriadStudios/Den/commit/b8a90eaaf4bfaece81972e590666397c2b7804e9).
Den has no tagged release yet, so the version is reported as `0.0.0-b8a90eaa`;
Go's module system names the same commit `v0.0.0-20261104135801-b8a90eaaf4bf`.

The whole app is `src/app.den`: one `rows` signal holding the row list, one
`selected` signal holding the selected row's id, a per-row `Label` signal
that "Update every 10th row" sets, and a keyed `{for ... key r.ID}` list.
Application code does not touch the DOM, cache nodes, delegate events or
schedule animation frames. Den itself attaches one click listener to the
document and finds row handlers through a `den-click` attribute on the
elements that have them. Rows that are not selected have `class=""`, since
den writes the attribute's string value.

Den code cannot call JavaScript, so there is no `Math.random`: labels are
drawn from the standard word lists with a Park-Miller generator seeded with 1,
so every page load produces the same labels.

## Build

Prerequisites: Node.js, Go 1.24 or newer on `PATH`, and network access to the
Go module proxy (proxy.golang.org) or GitHub for the pinned source. Den has no
npm dependencies.

```sh
npm ci
npm run build-prod
```

`build.js` runs `go run github.com/TriadStudios/Den/cmd/den@v0.0.0-20261104135801-b8a90eaaf4bf build src -o dist`.
Go downloads den and checks it against the Go checksum database. To build with
a local checkout of den instead, point `DEN_REPO` at it:

```sh
DEN_REPO=/path/to/Den npm run build-prod
```

The output is `dist/app.js` (no CSS: the page uses only `/css/currentStyle.css`).

## Run

Start the benchmark server from the repository root with `npm start`, then
open <http://localhost:8080/frameworks/keyed/den/index.html>.
