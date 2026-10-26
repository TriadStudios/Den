# Den — Go-syntax UI, compiled to tiny JavaScript

> **Experimental.** Den is a young project. The language and APIs will change
> before 1.0, and it is not ready for production use. See the
> [plan](docs/PLAN.md) for where it is going.

Den lets Go developers build web apps in Go syntax. You write components in
`.den` files, Go's own type checker checks them, and the compiler emits small,
fast JavaScript: no virtual DOM, no WebAssembly runtime and no npm.

```go
type Todo struct {
    ID    int
    Title string
    Done  bool
}

component TodoList(todos *Signal[[]Todo]) {
    left := computed(countLeft(todos.Get()))
    <section>
        <ul>
            {for _, t := range todos.Get() key t.ID {
                <li class={doneClass(t.Done)}>{t.Title}</li>
            }}
        </ul>
        {if left.Get() > 0 {
            <p>{left.Get()} left</p>
        } else {
            <p>All done!</p>
        }}
    </section>
}
```

Every expression in the markup is ordinary Go. Change `t.Title` to `t.Titel`
and the build stops with `app.den:12:50: t.Titel undefined (type Todo has no
field or method Titel)` before any JavaScript is written.

| | JS shipped, gzipped |
| --- | --- |
| `examples/counter` (two counters, a computed value, a bound input) | 2.3 KB |
| `examples/todos` (add, toggle, remove, filter) | 3.9 KB |
| `examples/todomvc` ([TodoMVC](https://todomvc.com), the full spec) | 5.3 KB |
| The whole runtime, every feature included | 3.0 KB |

On the js-framework-benchmark operations den runs at **1.00x the time of
hand-written vanilla JS, using 1.15x its memory** ([details](bench/README.md)).

## Quick start

```sh
go install github.com/TriadStudios/Den/cmd/den@latest
den dev examples/todomvc       # http://localhost:8080, rebuilds and reloads on save
den build examples/todomvc     # writes examples/todomvc/dist/
den check examples/todomvc     # type-check only
```

Den needs Go 1.24 or later and has no dependencies beyond the Go standard
library. An app is a directory of `.den` files with a component named `App`,
plus an optional `style.css`.

## How it works

1. **Parse.** `compiler/parse.go` reads `component Name(props) { ... }`
   blocks and the plain Go declarations (types, constants, functions,
   imports) between them. Inside a component, the Go statements at the top
   stay Go and the markup below becomes an element tree.
2. **Type-check with Go itself.** `compiler/check.go` writes the whole app
   out as one Go file: every `{expr}` becomes a typed call, every handler a
   `func(e Event)`, every `<Card title={x}/>` a call `Card(x)`, and `{if}`
   and `{for}` blocks real Go `if` and `for` statements. A small prelude
   declares `Signal[T]`, `Computed[T]` and `Event`. `go/types` checks it all,
   and line directives map every error back to its place in the `.den` file.
3. **Emit JavaScript from the typed AST.** `compiler/emit.go` decides as much
   as it can at compile time:
   - Each component's static DOM is built once and copied with `cloneNode`;
     the code then fills in only the dynamic parts.
   - Only expressions that read a signal get a reactive binding. An `{if}`
     or `{for}` whose condition or list reads no signal compiles to a plain
     JS `if` or loop.
   - Constants fold, integer division truncates as in Go, and bubbling events
     share one delegated listener per type.
   - Handlers and bindings inside list rows are compiled once per list, as
     functions of the row's item, so a row costs almost nothing beyond its
     DOM. Their elements carry a small `den-click="0"`-style attribute that
     names the handler.
4. **Runtime.** `compiler/runtime.js` is a push-pull signal graph (computed
   values are lazy and effects are glitch-free), ownership scopes (effects
   stop when the branch or row that created them goes away), keyed list
   reconciliation with a longest-increasing-subsequence diff, and safe DOM
   helpers. The compiler ships only the runtime functions an app uses.

## Type safety

All of these fail at build time, reported as `file:line:col`:

```
app.den:5:36: cannot use "hi" (untyped string constant) as int value in argument to count.Set
app.den:6:13: cannot render struct{A int}: only strings, numbers and bools can be shown in markup
app.den:7:15: <Card> has no prop "titel"
app.den:4:10: for in markup needs a key so rows can move instead of being rebuilt: {for _, x := range xs key x.ID { ... }}
app.den:2:8: package "fmt" is not available in den; you can import strconv, strings
```

Other checks: missing props, wrong prop types, unknown names, unused
variables and imports, mismatched tags, list keys that are not strings,
numbers or bools, calling a component as a function, and children or slots
a component does not take.

## Security by default

- **No raw HTML.** `innerHTML`, `outerHTML`, `srcdoc` and `dangerously*` are
  compile errors. Text is always inserted as text nodes, and templates are
  built with DOM calls, never by parsing HTML.
- **No inline JS.** `onclick="..."` strings are rejected; handlers must be Go.
  `<script>`, `<iframe>`, `<object>` and `<embed>` are rejected.
- **Safe URLs.** Static `href`/`src` values with dangerous schemes (including
  obfuscated ones like `java\tscript:`) fail the build. Dynamic URL
  attributes are sanitized at runtime: only http, https, mailto, tel and
  relative URLs pass.
- **Strict CSP.** Generated pages ship with `script-src 'self'`, no
  `unsafe-inline`, no `unsafe-eval`, and Trusted Types enforced with zero
  policies allowed. The runtime never needs one.
- **No npm.** The toolchain is one Go binary, so there are no install scripts
  or transitive JS packages.

## The language

A `.den` file holds components and plain Go declarations. Den supports a
subset of Go; anything outside it is a compile error, never silently
different behavior.

**Components and markup**
- `component Name(props) { statements; <markup> }` with exactly one root
  element; props are a Go parameter list, checked at every use.
- `{expr}` in text and attributes; `onevent={statements}` handlers, with
  `e.Value()`, `e.Checked()`, `e.Key()` and `e.PreventDefault()`.
- `{if cond { ... } else if ... { ... } else { ... }}`.
- `{for i, x := range xs key x.ID { ... }}` over slices, arrays, maps and
  ints. The key is required and must be a string, number or bool.
- SVG elements, and `autofocus` on elements added after the page loads.

**Children and slots**

```go
component Card(title string, header den.Children, children den.Children) {
    <section class="card">
        <header>{header}</header>
        <h2>{title}</h2>
        {children}
    </section>
}

component App() {
    n := signal(0)
    <Card title="Hi">
        <:header><b>Top {n.Get()}</b></:header>
        <p>Body</p>
        <button onclick={n.Set(n.Get() + 1)}>+</button>
    </Card>
}
```

- `den.Children` is built in; no import is needed. The prop named
  `children` gets the markup between the component's tags, and every other
  `den.Children` prop is a named slot, filled by `<:name>...</:name>`
  directly inside them.
- `{children}` renders it, as often as you like, and `<Inner>{children}</Inner>`
  passes it on. Children and slots that are not given render nothing.
- The markup belongs to the caller: it reads the caller's signals, variables
  and list items. The nodes it makes belong to where the callee renders it,
  so a callee `{if}` that hides it also stops its bindings.

**State**
- `signal(v)`, `computed(expr)`, `.Get()`, `.Set(v)`, `.Update(func)`.
- `stored(key, initial)`: a signal kept in localStorage as JSON and decoded
  back into Go types.
- `hash()`: a signal holding `location.hash`, for simple routing.

**Go**
- `type`, `const` and `func` declarations, including generic and variadic
  functions, named results and multiple return values.
- Structs (with embedded fields), arrays, slices and maps, with Go's
  semantics: structs and arrays are values, slices share backing arrays and
  `append` grows them as Go does, maps change in place, and nil slices and
  maps work. Indexing and slicing are bounds-checked and panic like Go.
- `if` (with init statements), `for` in all its forms, `range`, `break`,
  `continue`, parallel assignment, closures.
- `len`, `cap`, `append`, `copy`, `make`, `delete`, `clear`, `min`, `max`,
  conversions, and `itoa(n)`.
- `import "strings"`: `Contains`, `HasPrefix`, `HasSuffix`, `TrimSpace`,
  `TrimPrefix`, `TrimSuffix`, `ToLower`, `ToUpper`, `Split`, `Join`,
  `Repeat`, `ReplaceAll`, `Fields`. `import "strconv"`: `Itoa`, `Atoi`,
  `FormatBool`. Errors support `err != nil` and `err.Error()`.

`compiler/testdata/conform/` holds Go programs that the tests run with both Go
and den and require to print the same thing.

**Where den still differs from Go.** These are known and planned:
- `int` is a JS number, exact up to ±2^53, and does not wrap around on
  overflow. (The plan is BigInt for explicit `int64` and `uint64`.)
- Floats are printed the JavaScript way (`1000000`, where Go prints `1e+06`).
- Strings are JS strings: `len` counts UTF-8 bytes as in Go, but indexing
  and slicing strings are not supported yet. `strings.ToLower` and `ToUpper`
  follow JavaScript's rules for a few special characters.
- Ranging over a map visits keys in insertion order (one of the orders Go
  allows).

**Not supported yet:** pointers (other than `*Signal[T]`), methods,
interfaces (other than `error`), package-level variables, `switch`,
`defer`, goroutines and channels, scoped styles, server functions, and
server-side rendering. All are on the [roadmap](docs/PLAN.md).

## Developing den

```sh
go test ./...          # needs node on PATH for the runtime tests
go vet ./...
```

The tests compile apps and run them under node against a small fake DOM
(`compiler/testdata/dom.js`), fuzz the keyed list diff, run the conformance
programs against Go, and hold the runtime to 3 KB and every example to
10 KB gzipped. `examples/todomvc/e2e.js` and `bench/run.js` drive Chromium
with Playwright; they are optional development tools.

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request, and
[SECURITY.md](SECURITY.md) to report a vulnerability.

## Roadmap

The full plan, with phases, budgets and gates, is in [docs/PLAN.md](docs/PLAN.md).

1. **Core language (Phase 1, done):** markup control flow, structs, slices
   and maps, template cloning, lazy computed values, ownership scopes,
   TodoMVC under 10 KB, and the benchmark gate.
2. **Components, styling and motion:** children and slots (done), a typed
   router, scoped styles and design tokens, animations, `den fmt` and a VS
   Code extension.
3. **Server and security:** typed server functions with required `auth=`,
   `Secret[T]`, CSRF protection, SSR, source maps and `den test`.
4. **Scale:** `<VirtualList>`, data windows, live streams, and the
   million-row log viewer.
5. **Tooling and launch:** a language server, tree-sitter grammar and
   playground.
6. **Desktop:** `den desktop` on native webviews.

## License

Den is licensed under the [Apache License 2.0](LICENSE).
