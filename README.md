# Den — Go-syntax UI, compiled to tiny JavaScript

Den is an early prototype of a frontend framework where you write components in
Go syntax and the compiler emits small, fast, safe JavaScript. There is no
virtual DOM, no WebAssembly runtime and no npm.

```go
component Counter(label string, start int, step int) {
    count := signal(start)
    double := computed(count.Get() * 2)
    <section class="card">
        <h2>{label}</h2>
        <p class="value">{count.Get()}</p>
        <p class="hint">Double: {double.Get()}</p>
        <button onclick={count.Set(count.Get() - step)} disabled={count.Get() - step < 0}>−</button>
        <button onclick={count.Update(func(n int) int { return n + step })}>+</button>
    </section>
}
```

The demo app (two counters, a computed value, a bound text input) builds to
**4.9 KB, or 1.8 KB gzipped, runtime included**.

## Quick start

```sh
go build -o bin/den ./cmd/den
./bin/den dev examples/counter          # http://localhost:8080, rebuilds on save
./bin/den build examples/counter        # writes examples/counter/dist/
./bin/den check examples/counter        # type-check only
go test ./...
```

Requires Go 1.24+. The only dependency is the Go standard library. To install the
CLI: `go install github.com/TriadStudios/Den/cmd/den@latest`.

## How it works

1. **Parse.** `compiler/parse.go` reads `component Name(props) { ... }` blocks.
   The Go statements at the top of the body are kept as Go; the markup below is
   parsed into an element tree. Props are parsed with `go/parser`, so they are
   real Go parameter lists.
2. **Type-check with Go itself.** `compiler/check.go` writes the whole app out
   as one Go file: statements stay as-is, every `{expr}` becomes a typed call,
   every handler becomes a `func(e Event)`, and every `<Card title={x}/>`
   becomes `Card(x)`. A small prelude declares `Signal[T]`, `Computed[T]` and
   `Event`. `go/types` then checks everything, and `/*line app.den:L:C*/`
   directives map each error back to the exact spot in your `.den` file.
3. **Emit JavaScript from the typed AST.** `compiler/emit.go` walks the checked
   AST and writes direct DOM calls. Types drive the output:
   - Only expressions that read a signal get a reactive binding. The decision
     happens at compile time, so static parts cost nothing at runtime.
   - Constants fold (`{7 / 2}` becomes the text `3`).
   - Integer division truncates as in Go.
4. **Runtime.** `compiler/runtime.js` is about 80 lines: signals, effects,
   computed values, microtask batching and safe DOM helpers.

## Type safety

All of these fail at build time, reported as `file:line:col`:

```
app.den:5:36: cannot use "hi" (untyped string constant) as int value in argument to count.Set
app.den:6:13: cannot render struct{A int}: only strings, numbers and bools can be shown in markup
app.den:7:15: <Card> has no prop "titel"
```

Other checks: missing props, wrong prop types, unknown names, unused variables,
mismatched tags, and calling a component as a function.

## Security by default

- **No raw HTML.** `innerHTML`, `outerHTML`, `srcdoc` and `dangerously*` are
  compile errors. Text is always inserted as text nodes.
- **No inline JS.** `onclick="..."` strings are rejected; handlers must be Go.
  `<script>`, `<iframe>`, `<object>` and `<embed>` are rejected.
- **Safe URLs.** Static `href`/`src` values with dangerous schemes (including
  obfuscated ones like `java\tscript:`) fail the build. Dynamic URLs are
  sanitised at runtime, and only http, https, mailto, tel and relative URLs
  pass.
- **Strict CSP.** Generated pages ship with `script-src 'self'`, no
  `unsafe-inline`, no `unsafe-eval`, and Trusted Types enforced with zero
  policies allowed. The runtime never needs one.
- **No npm.** The toolchain is one Go binary, so there are no install scripts
  or transitive JS packages.

## Language subset (v0)

Supported:
- `signal`, `computed`, `.Get()`, `.Set()`, `.Update(func)`
- local variables, `if`/`else`, `return`, `+= -= *= /= %=`, `++`/`--`
- arithmetic, comparison and logic operators
- function literals, `len`, `itoa`, numeric and string conversions
- `e.Value()`, `e.Checked()`, `e.Key()`, `e.PreventDefault()` in handlers
- SVG elements

Not yet supported (all on the roadmap):
- lists (`for`) and conditionals in markup
- structs and slices in state
- children and slots for components
- scoped styles
- imports and server functions
- SSR and resumability
- template cloning (a performance pass)

## Roadmap

1. **Markup control flow:** keyed `for` lists and `if` blocks, so TodoMVC
   compiles (target under 10 KB gzipped).
2. **Data:** structs, slices and maps in signals, plus fine-grained stores.
3. **Server functions:** typed RPC, a required auth annotation, CSRF
   protection, and a `Secret[T]` type that can never reach the client.
4. **SSR plus resumability:** render on the server and attach without
   re-running components.
5. **Motion:** springs, layout and exit animations, and view transitions.
6. **Tooling:** a language server built on gopls, a formatter and source maps.
