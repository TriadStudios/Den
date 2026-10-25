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
**5.2 KB, or 1.9 KB gzipped, runtime included**.

Lists and conditionals use Go's own `for` and `if`. Lists need a key, so
updates move rows instead of rebuilding them:

```go
type Todo struct {
    ID   int
    Text string
    Done bool
}

component TodoList(todos *Signal[[]Todo]) {
    <ul>
        {for _, t := range todos.Get() key t.ID {
            <li>{t.Text}{if t.Done { ✓ }}</li>
        }}
    </ul>
}
```

`examples/todos` (add, toggle, remove, filter) builds to **3.3 KB gzipped**.

## Quick start

```sh
go build -o bin/den ./cmd/den
./bin/den dev examples/todos            # http://localhost:8080, rebuilds on save
./bin/den build examples/counter        # writes examples/counter/dist/
./bin/den check examples/counter        # type-check only
go test ./...                           # runtime tests need node on PATH
```

Requires Go 1.24+. The only dependency is the Go standard library. To install the
CLI: `go install github.com/TriadStudios/Den/cmd/den@latest`.

## How it works

1. **Parse.** `compiler/parse.go` reads `component Name(props) { ... }` blocks
   and the plain Go declarations (types, constants, functions) between them.
   The Go statements at the top of the body are kept as Go; the markup below is
   parsed into an element tree. Props are parsed with `go/parser`, so they are
   real Go parameter lists.
2. **Type-check with Go itself.** `compiler/check.go` writes the whole app out
   as one Go file: statements stay as-is, every `{expr}` becomes a typed call,
   every handler becomes a `func(e Event)`, every `<Card title={x}/>`
   becomes `Card(x)`, and `{if}` and `{for}` blocks become real Go `if` and
   `for` statements, so their scopes and types are Go's own. A small prelude declares `Signal[T]`, `Computed[T]` and
   `Event`. `go/types` then checks everything, and `/*line app.den:L:C*/`
   directives map each error back to the exact spot in your `.den` file.
3. **Emit JavaScript from the typed AST.** `compiler/emit.go` walks the checked
   AST and writes direct DOM calls. Types drive the output:
   - Only expressions that read a signal get a reactive binding. The decision
     happens at compile time, so static parts cost nothing at runtime. An
     `{if}` or `{for}` whose condition or list reads no signal compiles to a
     plain JS `if` or loop.
   - Constants fold (`{7 / 2}` becomes the text `3`).
   - Integer division truncates as in Go.
4. **Runtime.** `compiler/runtime.js` holds signals, effects, computed
   values, microtask batching, ownership scopes (effects stop when the `if`
   branch or list row that created them goes away), keyed list reconciliation
   with a longest-increasing-subsequence diff, and safe DOM helpers. Apps ship
   only the runtime functions they use.

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
- `{if cond { ... } else if ... { ... } else { ... }}` in markup
- `{for i, x := range xs key x.ID { ... }}` in markup, over slices, arrays
  and ints; the key (a string, number or bool) is required
- `type`, `const` and `func` declarations between components
- structs (including embedded fields), arrays and slices; composite
  literals, field access, indexing, slicing, `append`, `make`, `len`, `cap`,
  `min`, `max`
- local variables, `if`/`else`, `for` (all three forms and `range`),
  `break`, `continue`, `return` (one value, named results allowed),
  `+= -= *= /= %=`, `++`/`--`
- arithmetic, comparison and logic operators; `==` on structs and arrays
- function literals, variadic functions, generic functions, `itoa`, numeric
  and string conversions
- `e.Value()`, `e.Checked()`, `e.Key()`, `e.PreventDefault()` in handlers
- SVG elements

How Go values map to JavaScript, and where den is stricter than Go:
- Structs and arrays keep Go's value semantics. Den never changes one in
  place: `t.Done = true` builds a new struct and assigns it to `t`, so a
  copy (`u := t`) never sees the change.
- Slice elements are read-only (`xs[i] = v` is a compile error); build a new
  slice instead. `append` always copies, so it never writes into another
  slice's backing array. That is the one place den can differ from Go: code
  that relies on two slices sharing spare capacity after `append`.
- A nil slice behaves like an empty one, and comparing a slice with `nil` is
  a compile error (use `len(xs) == 0`).
- `len` of a string counts UTF-8 bytes, as in Go.

Not yet supported (all on the roadmap):
- maps, pointers (other than `*Signal[T]`), methods, interfaces,
  package-level variables, `switch`, multiple return values
- children and slots for components
- scoped styles
- imports and server functions
- SSR and resumability
- template cloning (a performance pass)

## Roadmap

The full plan, with phases, budgets and gates, is in [docs/PLAN.md](docs/PLAN.md).

1. **Markup control flow (done):** keyed `for` lists and `if` blocks.
   Next: a full TodoMVC (target under 10 KB gzipped) and js-framework-benchmark.
2. **Data:** structs and slices are done; maps, methods and fine-grained
   stores are next.
3. **Server functions:** typed RPC, a required auth annotation, CSRF
   protection, and a `Secret[T]` type that can never reach the client.
4. **SSR plus resumability:** render on the server and attach without
   re-running components.
5. **Motion:** springs, layout and exit animations, and view transitions.
6. **Tooling:** a language server built on gopls, a formatter and source maps.
