# Den — Go-syntax UI, compiled to tiny JavaScript

> **Experimental.** Den is a young project. The language and APIs will change
> before 1.0, and it is not ready for production use. See the
> [roadmap](#roadmap) for where it is going.

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
| `examples/router` (pages by URL path, a typed param, not found, scoped styles) | 4.1 KB |
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
plus an optional `style.css` for page-wide styles.

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
   - Each component's `style` block becomes scoped CSS in `style.css`, and
     its elements get their scope attribute in the template, so styles cost
     no JavaScript at all.
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
numbers or bools, calling a component as a function, children or slots
a component does not take, route patterns that do not fit their params,
and design tokens that do not exist, hold a bad value or are used in the
wrong kind of property (`app.den:9:30: $surface is a den.Color, but
padding takes a length`).

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
  policies allowed. The runtime never needs one. Styles are `style-src
  'self'` too: the browser ignores `<style>` elements and `style="..."`
  attributes, so CSS goes in style blocks (below) or `style.css`.
- **Checked CSS.** In style blocks, `url(...)` and the strings in
  `image-set()` and `src()` take the same URLs as a static `href`, and
  `expression(`, `javascript:`, `behavior` and `-moz-binding` are compile
  errors. Backslash escapes are allowed only inside strings, and the CSS
  of every block is read back before it is written, so one component's
  rules cannot run into the rules after them. Design token values cannot
  contain control characters (newlines and form feeds included), `;`,
  braces, `<`, backslashes, `url(`, `javascript:` or comments, so a token
  can never end its string, declaration or rule.
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
  `den.Children` prop (or alias of it) is a named slot, filled by
  `<:name>...</:name>` directly inside them.
- `{children}` renders it, as often as you like, and `<Inner>{children}</Inner>`
  passes it on. Each copy is built as HTML or SVG to match where it is
  rendered. Children and slots that are not given render nothing.
- `den` is still an ordinary name: a program that declares its own `den`
  keeps it, and then cannot use `den.Children`.
- The markup belongs to the caller: it reads the caller's signals, variables
  and list items. The nodes it makes belong to where the callee renders it,
  so a callee `{if}` that hides it also stops its bindings.

**State**
- `signal(v)`, `computed(expr)`, `.Get()`, `.Set(v)`, `.Update(func)`.
- `stored(key, initial)`: a signal kept in localStorage as JSON and decoded
  back into Go types.
- `hash()`: a signal holding `location.hash`, for simple routing. To route
  by URL path, use `route` and `path` (below).

**Routing**

```go
type UserParams struct {
    ID int
}

component App() {
    home := route[struct{}]("/")
    user := route[UserParams]("/users/:id")
    <div>
        <nav><a href="/">Home</a> <a href={user.Link(UserParams{ID: 7})}>User 7</a></nav>
        {if home.Matches() {
            <p>Home</p>
        } else if user.Matches() {
            <User user={user} />
        } else {
            <p>Not found</p>
        }}
    </div>
}

component User(user *Route[UserParams]) {
    <h1>User {user.Params().ID}</h1>
}
```

- `route[T](pattern)` is a typed URL pattern. A pattern starts with `/`, and
  each segment is static text, a param `:name` that takes one non-empty
  segment, or (last) a catch-all `*name` that takes the rest of the path,
  which may be empty. Each param fills the field of the struct `T` with
  its name, ignoring case (`:id` fills `ID`). Fields are strings or ints;
  a catch-all fills a string.
- `r.Matches()` reports whether the current path matches, and `r.Params()`
  returns its params, or the zero `T` when it does not match. Both are
  reactive, like `.Get()`. A path matches with or without one trailing
  slash, params are percent-decoded, and an int param must parse as
  `strconv.Atoi` parses it, or the route does not match.
- `r.Link(p)` is the path for params `p`, each one percent-encoded. It
  leads back to the same params, except for a `:name` param that is `""`,
  `"."` or `".."`, and a catch-all that ends in `/` or has a `.` or `..`
  part: an empty param never matches, one trailing slash is ignored, and
  browsers resolve `.` and `..` segments (encoded or not) away, so no
  param ever takes one.
- `path()` is a signal holding `location.pathname`. `Set` navigates with
  `history.pushState` (the URL may have a query and a fragment; the signal
  keeps the path), and back and forward update it.
- Once the app has called `route` or `path`, a click on a link to the same
  origin is followed without loading a page, unless it is a modified or
  middle click, the link has a `target` other than `_self` or a `download`
  attribute, it only jumps to an anchor on the current page, or a handler
  called `e.PreventDefault()`.
- Props are values, read when the component is made. A page that should
  follow its params from one URL to the next takes the route, as `User`
  does above, rather than `user.Params().ID`.
- These are compile errors, reported at the pattern or at `T`: a pattern
  that is not a constant or does not start with `/`; a param with no name,
  a name that is not a Go identifier or is used twice, a catch-all that is
  not last; a `T` that is not a struct, a param with no field, a field with
  no param, and a field that is not a string or an int. For example,
  `app.den:3:16: field Name of UserParams has no param in route pattern "/users/:id"`.
- Hosting: the server must answer paths it has no file for with
  `index.html` (`den dev` does, for paths whose last segment has no `.`),
  and the app must be served from the site root, since its `index.html`
  then loads `/app.js` and `/style.css`. A base path is not supported yet.
- `route`, `Route` and `path` are ordinary names. A program that has its
  own at the top level, as a declaration, a component or an import, keeps
  it. `route` and `Route` go together: a program with either has neither
  of den's, and using the other says so.

**Styles**

```go
const (
    Surface  den.Color  = "#ffffff"
    Ink      den.Color  = "oklch(25% 0.02 260)"
    Space4   den.Length = "16px"
    RadiusLg den.Length = "12px"
    Body     den.Font   = "system-ui, sans-serif"
)

component Card(title string, children den.Children) {
    style {
        .card { padding: $space-4; border-radius: $radius-lg; background: $surface; }
        .card h2 { color: $ink; font-family: $body; }
        @media (min-width: 40rem) { .card { padding: calc($space-4 * 2); } }
    }
    <section class="card"><h2>{title}</h2>{children}</section>
}
```

- A component can have one `style { ... }` block, among its Go statements
  before the markup. It holds CSS that applies only to the elements the
  component's own markup makes. Each of them carries an attribute named
  from a hash of the component's name, such as `d-k3x9q`, and every
  compound selector in the block is narrowed to it: `.card h2` becomes
  `.card[d-k3x9q] h2[d-k3x9q]`. So a component's rules never reach into
  the components it uses, and theirs never reach into it. Markup passed as
  children or a slot is styled by the component that wrote it.
- The attribute is part of the template, so scoping costs nothing at run
  time, and `class={...}` can replace the whole class without losing it.
- The attribute goes before a pseudo-element (`a[d-k3x9q]::before`), and
  the arguments of `:not()`, `:is()`, `:has()` and the like are left as
  written. `@media`, `@supports` and `@container` rules hold rules as
  usual. `@keyframes fade` becomes local to the component, and
  `animation` and `animation-name` in the block follow it. A keyframes
  name cannot be one of the keywords those properties take, such as
  `auto`, `ease`, `linear`, `infinite` or `none`.
- `$name` in a value is a design token: a package-level constant of type
  `den.Color`, `den.Length`, `den.Duration`, `den.Font` or `den.Token`,
  named in kebab case (`$radius-lg` is the constant `RadiusLg` or
  `radiusLg`). Token values are checked when the app builds: a `den.Color`
  is a hex color, a color function such as `oklch(...)`, `transparent`,
  `currentColor` or a color name; a `den.Length` is `0`, a number with a
  unit, or `calc()`, `min()`, `max()` or `clamp()`; a `den.Duration` is a
  number of `ms` or `s`; a `den.Font` is a list of font families; a
  `den.Token` is any other value. A color cannot go where only a length
  can (`padding`, `width`, `gap` and so on), a length where only a color
  can, a duration outside `transition` and `animation`, or a font outside
  `font` and `font-family`.
- Tokens become CSS custom properties: `$surface` is `var(--surface)`, and
  the CSS starts with `:root{--surface:#ffffff;...}`, giving every token
  the style blocks use. `den build` writes the CSS of all style blocks at
  the top of `style.css`, followed by the app's own `style.css`, whose
  rules come later and can give the tokens other values, for a dark theme
  for instance:

  ```css
  @media (prefers-color-scheme: dark) {
    :root { --surface: #1f1f1d; --ink: #f1efe8; }
  }
  ```

- These are compile errors: a second style block, or one after the
  markup; a token that does not exist, matches two constants (`ink` and
  `Ink`) or is not a constant of a token type; `:root`, `html` or `body`
  in a selector, which no component makes (page-wide styles go in
  `style.css`); at-rules other than `@media`, `@supports`, `@container` and
  `@keyframes` (`@import`, `@font-face` and the rest go in `style.css`);
  nested rules and `&`; and tokens in selectors, in `@media` conditions or
  inside `url()`, where custom properties do not work, or inside
  `image-set()` and `src()`, whose URLs are checked when the app builds.
- Backslash escapes are allowed only inside strings: `content: "\201C"`
  is fine, `.sm\:p-4` is an error. To select a class with such
  characters, match it as a string: `[class~="sm:p-4"]`. Brackets must
  be closed by their own kind, an unquoted `url()` cannot contain spaces,
  quotes or `(`, and control characters and `<!--` are errors. Each of
  these is a place where a browser could read the CSS differently from
  den and run one rule into the next. As a last check, den reads the CSS
  it writes for each block again, with a separate CSS tokenizer that
  follows the CSS syntax spec, and fails the build if a rule or
  declaration would not come out as written.
- A line among a component's Go statements that starts a statement with
  `style {` (or `style{`) begins a style block. In Go, such a line could
  only be a composite literal of a type named `style`, as in
  `style{1, 2}[0] = 5`; write it as `(style{1, 2})[0] = 5`, or give the
  type another name. Everywhere else `style` is an ordinary name.

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
`defer`, goroutines and channels, server functions, and server-side
rendering. The larger ones are on the [roadmap](#roadmap).

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

Each phase ends at a gate with measurable exit criteria. Every release must
hold these budgets: the runtime under 3 KB gzipped, TodoMVC under 10 KB
gzipped, the js-framework-benchmark operations within 1.3x of vanilla JS's
time, memory after creating 1,000 rows within 1.2x of vanilla JS, and a
rebuild of 100 components under 50 ms.

1. **Core language (Phase 1, done):** markup control flow, structs, slices
   and maps, template cloning, lazy computed values, ownership scopes,
   TodoMVC under 10 KB, and the benchmark gate.
2. **Components, styling and motion:** children and slots (done), a typed
   router (done), scoped styles and design tokens (done), animations,
   `den fmt` and a VS Code extension.
3. **Server and security:** typed server functions with required `auth=`,
   `Secret[T]`, CSRF protection, SSR, source maps and `den test`.
4. **Scale:** `<VirtualList>`, data windows, live streams, and the
   million-row log viewer.
5. **Tooling and launch:** a language server, tree-sitter grammar and
   playground.
6. **Desktop:** `den desktop` on native webviews.

## License

Den is licensed under the [Apache License 2.0](LICENSE).
