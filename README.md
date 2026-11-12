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
| `examples/counter` (two counters, a computed value, a bound input) | 2.4 KB |
| `examples/todos` (add, toggle, remove, filter) | 4.0 KB |
| `examples/todomvc` ([TodoMVC](https://todomvc.com), the full spec) | 5.4 KB |
| `examples/router` (pages by URL path, a typed param, not found, scoped styles) | 4.2 KB |
| `examples/motion` (list rows that enter, leave and glide, a panel, a spring) | 5.9 KB |
| `examples/clock` (a clock, a stopwatch, a search box focused on mount) | 3.8 KB |
| `examples/theme` (a theme store and compound tabs, passed down with context) | 3.7 KB |
| `examples/form` (a signup form: bound inputs, checks shown after blur, a summary) | 4.3 KB |
| `examples/fetch` (JSON pager, Reload, and a Save action that posts then reloads) | 8.5 KB |
| The whole runtime, every feature included | 3.0 KB |

On the js-framework-benchmark operations den runs at **1.00x the time of
hand-written vanilla JS, using 1.15x its memory** ([details](bench/README.md)).

## Quick start

```sh
go install github.com/TriadStudios/Den/cmd/den@latest
den dev examples/todomvc       # http://localhost:8080, rebuilds and reloads on save
den build examples/todomvc     # writes examples/todomvc/dist/
den check examples/todomvc     # type-check only
den fmt -w examples/todomvc    # format the .den files in place
```

Den needs Go 1.24 or later and has no dependencies beyond the Go standard
library. An app is a directory of `.den` files with a component named `App`,
plus an optional `style.css` for page-wide styles and an optional `public/`
directory of static files, such as JSON the app fetches, which `den build`
and `den dev` copy into `dist` as they are (see [Async data](#async-data)).

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
   - Each component's `style` block becomes scoped CSS in `den.css`, and
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
app.den:2:8: package "fmt" is not available in den; you can import den/http, errors, strconv, strings, time
```

Other checks: missing props, wrong prop types, unknown names, unused
variables and imports, mismatched tags, list and `{key}` keys that are
not strings, numbers or bools, calling a component as a function,
children or slots a component does not take, route patterns that do
not fit their params, `bind` on an input that cannot hold its type,
transitions where they could never play, timers and cleanups outside a
component, types that fetched JSON cannot be decoded into, and design
tokens that do not exist, hold a bad value or are used in the wrong kind
of property (`app.den:9:30: $surface is a den.Color, but padding takes
a length`).

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
- **Checked CSS.** Den checks the CSS in style blocks and the values of
  design tokens. The app's own `style.css` is not checked. Every URL
  written in a style block is checked: the argument of `url(...)` and
  every string anywhere inside `image-set()`, `-webkit-image-set()`,
  `image()` and `src()`. It must be a URL a static `href` may have (http,
  https, mailto, tel or relative) or a `data:image/` URL, with no
  `expression(` or `javascript:` in it. `var()`, `env()`, `attr()` and
  design tokens are compile errors inside those functions, and calls of
  custom functions (`--name(...)`) are errors anywhere in a style block,
  since an `@function` in `style.css` could turn their arguments into
  URLs. So are calls of `inherit()` (the `inherit` keyword is fine),
  which substitutes a value like `var()` does, and which Chromium can
  read as running into every later rule. So nothing in a style block can
  become a URL den has not checked. Elsewhere, `expression(` and `javascript:` are errors outside
  strings (in a string such as `content: "a javascript: tutorial"` they
  are only text), and so are `behavior` and `-moz-binding`. CSS names
  and keywords are compared ignoring ASCII case only, as browsers do.
  Backslash escapes are allowed only inside strings, and the CSS of every
  block is read back before it is written, so one component's rules
  cannot run into the rules after them.
  A design token's value is checked by the same code as a value in a
  style block, and more strictly, since `var()` can take it into any
  property: it cannot hold a function that takes a URL or an image at
  all (`url()`, `image-set()`, `image()`, `src()`, `cross-fade()`,
  `element()` and their prefixed forms), a custom function, `inherit()`,
  `$`, control characters (newlines and form feeds included), `;`, `<`,
  backslashes, comments, `javascript:` or `expression(`, so a token can
  never carry a URL or end its string, declaration or rule. Nor can it
  hold `!important`, which would stop `style.css` from theming it.
- **Same-origin data.** `fetchJSON` requests only URLs on the page's
  origin, refuses redirects elsewhere, and reads JSON with `JSON.parse`
  into checked Go types; nothing it fetches is ever run.
- **No npm.** The toolchain is one Go binary, so there are no install scripts
  or transitive JS packages.

## Formatting

`den fmt` gives `.den` files one layout, as `gofmt` does for Go, and is
used the same way:

```sh
den fmt app.den      # print the file formatted
den fmt -l .         # list the files whose formatting differs
den fmt -w .         # rewrite them in place
den fmt -d .         # show the changes as a diff
den fmt < app.den    # format standard input
```

A directory stands for every `.den` file under it, except those in
`dist`, `node_modules` and dot directories. `-w` replaces a file
atomically and keeps its permissions. A file that does not parse is
reported as `file:line:col` and left alone, and the exit status is 2.

What it changes:
- Go is formatted as `gofmt` formats it: declarations, statements,
  props, and the Go in markup (`{expr}`, attribute values, handlers,
  `{if}` and `{for}` headers and `{key}` values). A handler written on
  one line stays on one line if it is short.
- Markup is indented with tabs. An element holding only text and
  `{expr}`s goes on one line if it fits in 100 columns (a tab counts as
  4), and so does any element, `{if}`, `{for}` or `{key}` written on one
  line that fits. Otherwise children go on lines of their own, wherever
  that changes nothing (below). A start tag that does not fit has one
  attribute per line. Quoted attribute values keep their text, in `"`
  unless they hold one, except that a carriage return in one is written
  `&#13;`, the same character to den, so the file can end its lines with
  LF alone. Tags close as `<x />`, and at most one blank line is kept.
- Style blocks get a line per rule and per declaration, `prop: value;`,
  with each run of whitespace collapsed to one space, apart from strings,
  comments and `url()`. A selector list longer than 80 columns gets a
  line per selector.
- Line endings become LF.

What it never changes is what the app compiles to. Den's text rules say
which whitespace counts: whitespace with a newline in it is dropped next
to a tag or `{...}` and is one space between words, while a space
without a newline is text. So `<b>a</b> <i>b</i>` keeps its space, and
`{a}` and `{b}` on two lines become `{a}{b}`, which shows that the page
has no space between them. Whitespace in `<pre>` and `<textarea>` is
kept exactly, and so is the text around a line holding nothing but a
non-breaking space, which den drops. Comments of every kind are kept.
`den fmt` checks its own work: it parses what it wrote and compares it
with what it read, and reports an error rather than write a file that
would compile differently.

## The language

A `.den` file holds components and plain Go declarations. Den supports a
subset of Go; anything outside it is a compile error, never silently
different behavior.

**Components and markup**
- `component Name(props) { statements; <markup> }` with exactly one root
  element; props are a Go parameter list, checked at every use.
- `{expr}` in text and attributes; `onevent={statements}` handlers, with
  `e.Value()`, `e.Checked()`, `e.Key()` and `e.PreventDefault()`. A
  `{...}` with nothing but Go comments in it, like `{/* note */}`, is a
  comment; `<!-- ... -->` works too.
- `{if cond { ... } else if ... { ... } else { ... }}`.
- `{for i, x := range xs key x.ID { ... }}` over slices, arrays, maps and
  ints. The key is required and must be a string, number or bool.
- `{key x { ... }}` builds its content once, and again from scratch
  whenever the value of `x` changes: the old content goes, with the state
  of the components in it, and nothing is kept. The same value changes
  nothing. `x` is read like an `{if}` condition, so it follows the signals
  it reads, and like a `{for}` key it must be a string, number or bool. A
  key that reads no signal never changes, and its content is built once.
  `key` is not a Go keyword, so `{key}`, `{key.Name}`, `{key + 1}`,
  `{key(x)}` and `{key[0]}` are still expressions using a name `key`. A
  block starts only where `key` is followed by whitespace and then a
  name, a number starting with a digit, a string or rune literal, or a
  `!` that is not `!=`, and its value ends with the `{` of the body. Any
  other start, such as `(`, `[`, `.5`, `-x`, `*p`, `&x` or a comment,
  leaves the `{...}` an expression, so such a value is written another
  way: `{key 0 - x { ... }}` or `{key int(-x) { ... }}`.
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
  back into Go types. Each `Set` or `Update` is saved as it is made.
- `hash()`: a signal holding `location.hash`, for simple routing; setting
  it sets `location.hash` at once. To route by URL path, use `route` and
  `path` (below).
- Changes land together: the signals a handler sets update the page once,
  in a microtask after it returns, and nothing sees some of them but not
  the others. An `{if}`, `{for}` or `{key}` checks its condition, list or
  value again before anything inside it runs, so a guard holds:
  `{if len(xs.Get()) > 1 { <p>{xs.Get()[1]}</p> }}` takes the `<p>` away
  when `xs` shrinks, and never reads past the end.
- A binding that panics (an index out of range, say) is reported with
  `reportError`, as an uncaught error would be, and the rest of the
  update still lands; the binding runs again when what it read changes.
  A `computed` that panics panics again for each binding that reads it,
  until its own inputs change. A binding that panics as its branch or row
  is built is reported the same way, and the branch or row is shown
  without it. A panic outside bindings while a branch or row is built (in
  a component's statements or a prop) leaves that `{if}` branch out, or
  keeps the `{for}`'s old rows, until its condition or list changes again.

**Context**

```go
type Theme struct {
    Dark *Signal[bool]
}

component App() {
    provide(Theme{Dark: signal(false)})
    <main><Toolbar /><Page /></main>
}

component Toolbar() {
    theme := use[Theme]()
    <button onclick={theme.Dark.Set(!theme.Dark.Get())}>Dark mode</button>
}
```

- `provide(v)` makes `v` reach everything the component builds, without
  props in between. `use[T]()` returns the value of the nearest `provide`
  of a `T` among the components that build it, or the zero `T` when there
  is none, as a map lookup does.
- Values are found by their type, so each kind of context gets a type of
  its own: `T` must be a named type that the program declares. An alias
  of one is that type, and each instance of a generic type (`Box[int]`,
  `Box[string]`) is a type of its own.
- The components that build a component are those of the render tree:
  markup passed as children or a slot sees the context of the component
  that renders it, not of the one that wrote it. So compound components
  work: a `Tabs` that provides its state reaches the `Tab` elements its
  caller writes between `<Tabs>` and `</Tabs>`.
- A `provide` applies from where it is written: the component's later
  statements and all of its markup see it. Statements and closures see
  the context in effect where they are written, so a closure written
  before a `provide` does not see it, whenever the closure runs. (Markup
  passed as children or a slot is the exception above: it sees the
  context where it is rendered.) A second `provide` of the same type
  replaces the first for what follows it.
- `use` can be called anywhere in a component (its statements, closures,
  handlers and markup) and reads the context once: values are values. To
  share state that changes, provide a struct of signals, a store, as
  above; components that read its signals follow them.
- It costs little: in an app that provides something, each component
  takes one more argument, and each `provide` makes one object whose
  prototype is the context above it, so a lookup finds the nearest
  provider. An app that provides nothing compiles as if context did not
  exist, and there each `use` is the zero value.
- These are compile errors: a `provide` or `use` of a type that is not a
  named type, as in `provide(3)`, `use[string]()`, `use[*Signal[int]]()`
  and `use[[]Item]()` (`app.den:3:14: context values are found by their
  type, so use needs a named type: declare one, as in type Theme
  struct{...}`), or of a named type that is not the program's own, such
  as `error`, `Signal[int]` or `den.Children`; a `provide` that is not a
  statement of its own at the top level of a component's statements (in
  an `if`, a closure, a handler or a function); a `use` outside a
  component, as in a function; `use()` without its type; and `provide`
  or `use` used as values (`f := use[Theme]`).
- `provide` and `use` are ordinary names: a program that has its own at
  the top level keeps it.

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
  does above, rather than `user.Params().ID`. The page itself stays when
  only its params change (the `{if}` around it still matches), and so does
  the state it keeps in its own signals, such as an open dialog. To give
  it a fresh start for each user, put it in a key block:
  `{key user.Params().ID { <User user={user} /> }}`.
- These are compile errors, reported at the pattern or at `T`: a pattern
  that is not a constant or does not start with `/`; a param with no name,
  a name that is not a Go identifier or is used twice, a catch-all that is
  not last; a `T` that is not a struct, a param with no field, a field with
  no param, and a field that is not a string or an int. For example,
  `app.den:3:16: field Name of UserParams has no param in route pattern "/users/:id"`.
- Hosting: the server must answer paths it has no file for with
  `index.html` (`den dev` does, for paths whose last segment has no `.`),
  and the app must be served from the site root, since its `index.html`
  then loads `/app.js`, `/den.css` and `/style.css`. A base path is not
  supported yet.
- `route`, `Route` and `path` are ordinary names. A program that has its
  own at the top level, as a declaration, a component or an import, keeps
  it. `route` and `Route` go together: a program with either has neither
  of den's, and using the other says so.

<a id="async-data"></a>**Async data**

```go
type User struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}

component Users() {
    page := signal(1)
    users := resource(fetchJSON[[]User]("/users/" + itoa(page.Get()) + ".json"))
    <div>
        {if users.Loading() { <p>Loading...</p> }}
        {if users.Err() != nil { <p>{users.Err().Error()}</p> }}
        <ul>{for _, u := range users.Data() key u.ID { <li>{u.Name}</li> }}</ul>
        <button onclick={page.Set(page.Get() + 1)}>Next</button>
        <button onclick={users.Reload()}>Reload</button>
    </div>
}
```

- `resource(f(args))` takes a call that returns `(T, error)`, as Go
  passes such a call to a function of two parameters, and is a
  `*Resource[T]`. Today the call is `fetchJSON[T](url)`.
- The arguments are read like `computed`'s: when a signal they read
  changes, the request in flight is aborted and a new one starts. The
  first starts when the component is made. `r.Reload()` starts again
  with the current arguments, as a change to them does: the arguments are
  read again, in the update after the handler, and once they are, the
  request in flight is aborted and a new one starts.
- `r.Loading()` is true while a request is in flight. `r.Data()` is the
  value of the latest request that succeeded (the zero `T` before one),
  kept while the next one loads and when it fails. `r.Err()` is the error
  of the latest request to finish, kept while the next one loads, and nil
  after a success. All three are reactive, like `.Get()`. Only the latest
  request's outcome lands: an aborted request's never does.
- When the component that made the resource goes away (with its `{if}`
  branch or `{for}` row, say), the request in flight is aborted, and
  nothing lands afterwards. The abort is one of the component's
  cleanups, run with its `onCleanup` functions, newest first: after one
  registered later than the `resource` call, before one registered
  earlier. A panic while the arguments are evaluated is
  reported as a binding's is, and the resource keeps its state, the
  request in flight included.
- `fetchJSON[T](url)` is a GET of `url`, resolved against the page. It
  must be on the page's origin, or `Err()` says so and no request is made
  (`fetchJSON: https://x.example/a.json is not on this origin`); the
  request also asks the browser to refuse other origins, after redirects
  too (`mode: "same-origin"`), sends same-origin credentials and `Accept:
  application/json`, and the page's CSP allows only its own origin. A
  status other than 2xx is an error (`fetchJSON: GET /users/4.json: 404
  Not Found`), and so is a failed request.
- The body is parsed with `JSON.parse`, never evaluated, and decoded into
  `T` as Go's `encoding/json` decodes it. Only exported fields are
  filled, along with the fields of embedded structs, promoted as Go
  promotes them; a field's key is its `json` tag's name, or else its own
  name, and `json:"-"` leaves it out. A key matches a field exactly or,
  failing that, ignoring case as Go folds it (`"id"` and `"Id"` fill
  `ID`); keys of no field are ignored and missing ones leave zero values.
  `null` leaves a value as it is, except that a slice becomes nil and a
  map empty. A number goes into an integer only if it is written as one
  and is in the type's range (`1.0`, `1e2` and, for unsigned types, `-0`
  do not fit), and into a `float32` rounded once from what is written, as
  Go rounds it. JSON may be nested 10,000 levels deep, as in Go, and
  deeper is an error (`exceeded max depth`). Strings with escaped lone
  surrogates (`"\ud800"`) get U+FFFD in their place, as in Go, in
  browsers that have `String.prototype.toWellFormed` (all current ones).
  An array takes up to its length, the rest zero; a map
  takes string keys, or integer keys parsed from the object's keys; a
  `[]byte` takes base64 text. A key given twice is decoded twice, in
  order: a second object merges into the first, as in Go. A value that
  does not fit is an error worded like Go's (`json: cannot unmarshal
  string into Go struct field User.id of type int`), and JSON that does
  not parse is `fetchJSON: GET /a.json: invalid JSON: ...`.
- Where versions of Go's `encoding/json` differ, den follows the one
  built on json/v2 (the default since Go 1.27): a key that matches two
  fields only ignoring case goes to the first found breadth-first, so
  `"NAME"` fills an outer `Name` rather than one promoted from an
  embedded struct. Where den differs from Go: `int64` and `uint64` values
  beyond 2^53 lose precision, as den's integers do; and browsers drop a
  UTF-8 byte order mark at the start of a response, which Go rejects.
- These are compile errors: `fetchJSON` anywhere but as `resource`'s
  whole argument, or without its type (`fetchJSON needs the type to
  decode the JSON into: fetchJSON[T](url)`); `resource` outside a
  component's statements (in a
  function, a func literal, a handler, markup, a prop, or the argument of
  `computed`, `spring` or another `resource`); `resource` of anything but
  a call of `fetchJSON` (typed server functions are planned); a `T` den
  cannot decode into (funcs, channels, complex numbers, pointers,
  interfaces, den's own types, maps with keys that are not strings or
  integers), reported at `T`; `json` tag options other than `omitempty`
  and `omitzero`, which change nothing in decoding (`,string` is not
  supported yet); `json` tag names with characters other than letters,
  digits, spaces and ASCII punctuation other than quotes, backquotes and
  backslashes, which versions of Go read differently; and `resource` or
  `fetchJSON` used as a value. For example, `app.den:3:29: fetchJSON
  cannot decode JSON into func() int (field F of T): den decodes JSON
  into strings, numbers, bools, and structs, arrays, slices and maps of
  them`.
- Static files: `den build` and `den dev` copy the app's `public/`
  directory, if it has one, into `dist` as it is, so `public/pages/1.json`
  is served as `/pages/1.json` next to the app, and `den dev` rebuilds
  when a file in it changes. A file there that would replace `app.js`,
  `index.html`, `den.css` or `style.css`, or anything under `__den/`,
  where `den dev` serves live reload, is an error (names compared
  ignoring case), and so is anything in `public/` but files and
  directories (a symbolic link or a Windows junction could lead out of
  it), and an output directory inside `public/`. These are found before
  anything is written. Files removed from `public/` stay in `dist` until
  it is removed. `examples/fetch` pages through three such files.
- `resource`, `Resource` and `fetchJSON` are ordinary names: a program
  that has its own at the top level keeps it. `resource` and `Resource`
  go together, and `fetchJSON` goes with them; using one then says why.

<a id="actions"></a>**Actions (sending changes)**

```go
type Note struct {
    Text string `json:"text"`
}
type Saved struct {
    OK bool `json:"ok"`
}

component Editor(notes *Resource[[]Note]) {
    a := action(sendJSON[Note, Saved]("POST", "/notes"))
    a.Then(func(_ Saved) { notes.Reload() })
    <div>
        <button disabled={a.Pending()} onclick={a.Do(Note{Text: "hi"})}>Save</button>
        {if a.Err() != nil { <p>{a.Err().Error()}</p> }}
    </div>
}
```

- `action(sendJSON[In, Out](method, url))` is a `*Action[In, Out]`. The
  method must be the constant `"POST"`, `"PUT"`, `"PATCH"` or `"DELETE"`.
  The URL is read when `Do` runs, not when the action is made, so a signal
  in it is not subscribed: change the URL, then call `Do`.
- `a.Do(v)` starts one request. While `a.Pending()` is true, another `Do`
  does not send: it sets `a.Err()` to `action: Do called while a request
  is pending`, and the request in flight still settles and replaces that
  error. `a.Data()` is the latest successful `Out` (zero before one);
  `a.Err()` is the latest finished error, or nil after a success. All
  three are reactive.
- When `In` is the unnamed `struct{}`, write `a.Do()` with no value: it
  sends `{}`, except `"DELETE"`, which sends no body. A named empty
  struct is a normal value and needs `a.Do(Named{})`, which sends `{}`.
- `a.Then(f)` replaces the success callback (one function). It runs only
  after a 2xx reply, under the component's lifecycle, so what it makes is
  owned and cleaned up with the component. A common pattern is
  `a.Then(func(_ Out) { list.Reload() })`.
- The request uses the page's shared headers from `"den/http"`,
  `mode`/`credentials` `same-origin`, and `Content-Type: application/json`
  when there is a body. Cross-origin, non-http(s), or userinfo URLs error
  with no request (`sendJSON: … is not on this origin`). Non-2xx errors
  keep the same shape as `fetchJSON` (`sendJSON: POST /notes: 422 …`) and
  carry status/code/message for `http.Status` / `Code` / `Message`. A 204
  or an empty body is the zero `Out`.
- The body is encoded as Go's `encoding/json` encodes it (tags,
  `omitempty`, `omitzero`, `-`, embedded fields, maps with string or
  integer keys sorted as strings, `[]byte` as base64). Unsupported types
  are compile errors at `In` or `Out`, worded like `fetchJSON`'s.
- When the component that made the action goes away, the request in
  flight is aborted and nothing lands afterwards, including `Then`.
- These are compile errors: `sendJSON` anywhere but as `action`'s whole
  argument; `action` outside a component's statements; a non-constant or
  other method; `Do` with the wrong arity for `In`; types den cannot
  encode or decode; and `action` or `sendJSON` used as values.
  `action`, `Action` and `sendJSON` are ordinary names and give way
  together when the program declares its own.

### HTTP errors and headers

Import `"den/http"` (package `http`) for browser request controls. These are
Den operations, distinct from Go's `net/http`. `http.Status(err) int` and
`http.Code(err) string` return zero and empty for ordinary errors and nil.
`http.Message(err) string` returns the server's message, falling back to
`err.Error()` (empty for nil). Non-2xx errors keep their existing error text
and status even when reading or parsing the response fails. A JSON response
`{"error":{"code":"invalid","message":"Check your input"}}` adds its string
code and message.

`http.SetHeader(name, value string) error` replaces a shared request header;
`http.DeleteHeader(name string) error` removes it, ignoring case. Check the
returned error. Each request takes a snapshot, so changes affect subsequent
requests. For example, set `X-CSRF-Token` after login, and delete it at logout;
never put tokens in URLs. Headers apply only to same-origin requests; the
browser also refuses cross-origin redirects. Invalid names, control characters,
non-byte values and Fetch-forbidden names are rejected. `Proxy-` and `Sec-`
prefixes and all three method-override headers are rejected, even for safe
method values. Deleting a forbidden or invalid name also returns an error.

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
  number of `ms` or `s` (numbers may have exponents: `1e3ms`, `1.5E-1rem`);
  a `den.Font` is a list of font families; a `den.Token` is any other
  value. Every token value is also checked as a value in a style block
  is, and cannot hold a URL: write `url()` and `image-set()` in the style
  block, where den checks the URL. A color cannot go where only a length
  can (`padding`, `width`, `gap` and so on), a length where only a color
  can, a duration outside `transition` and `animation`, or a font outside
  `font` and `font-family`.
- Tokens become CSS custom properties: `$surface` is `var(--surface)`, and
  the CSS starts with `:root{--surface:#ffffff;...}`, giving every token
  the style blocks use. `den build` writes the CSS of all style blocks to
  `den.css`, which the page links before the app's own `style.css`. That
  file is copied as it is (`@import` and the like still work at its top),
  and its rules come later, so they can give the tokens other values, for
  a dark theme for instance:

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
  nested rules and `&`, and `{}` blocks in custom property values; calls
  of custom functions (`--name(...)`) and of `inherit()`; tokens in
  selectors, in `@media` conditions or inside `url()`, where custom
  properties do not work; and tokens, `var()`, `env()` or `attr()` inside
  `image-set()`, `image()` or `src()`, whose URLs are checked when the app
  builds.
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

**Motion**

```go
const Fast den.Duration = "150ms"

component List(items *Signal[[]Item]) {
    <ul>
        {for _, it := range items.Get() key it.ID {
            <li enter={fade()} exit={slide("left").For(Fast)} animate={layout()}>{it.Name}</li>
        }}
    </ul>
}

component Dot(target *Signal[float64]) {
    x := spring(target.Get())
    <svg viewBox="0 0 300 40"><circle cx={x.Get()} cy="20" r="12" /></svg>
}
```

- `enter={t}` and `exit={t}` give an element the transition it plays when
  it is added and when it is removed. A transition is `fade()`, `scale()`
  (a fade from 95% size) or `slide(dir)`, a fade while moving in from, or
  out to, `"left"`, `"right"`, `"up"` or `"down"` by the element's own
  size. `.For(d)` sets how long it takes (200ms otherwise), where `d` is a
  constant `den.Duration`: `"150ms"`, `"0.2s"` or a design token const.
  Entering eases out and leaving eases in.
- They go on an HTML or SVG element directly inside an `{if}` branch, a
  `{for}` row or a `{key}` block, the elements that block adds and
  removes. A block directly inside the branch, row or key block counts as
  well, so an `else if` chain animates as one block, and rows of a list
  directly in a branch leave with it. When a key block's value changes,
  its old content leaves while the new content enters, as when a row's
  key goes and another comes.
- When the block changes, new elements enter while the old ones leave. A
  leaving element's bindings and handlers stop at once, so it keeps
  showing what it showed last. It takes no more input: it is made `inert`
  and gets `pointer-events: none` (SVG elements have no `inert`), focus
  inside it is dropped, and events inside it never reach its handlers. It
  goes when its exit transition ends. Other nodes go at once. A browser
  that runs no animation frames, as in a hidden tab, never ends a
  transition; den then removes the element 100ms after its transition
  should have ended. Transitions play only when the
  block itself changes: not when the page is built, nor when a block is
  built along with the branch or row around it. A row whose key stays but
  whose item changed is rebuilt in place, without transitions; a key that
  comes back while its old row is leaving gets a new row.
- `animate={layout()}` goes on an element directly inside a `{for}` row.
  When the list changes, and when a leaving row is finally removed, the
  element glides (200ms) from where it was to its new place: den measures
  it before and after the change and animates a `transform` between them.
- `spring(x)` is a `*Computed[float64]` that follows `x` with spring
  physics (stiffness 170, damping 26, mass 1). Like `computed`, `x` is
  read again whenever the signals it reads change. The spring starts at
  `x`, steps once a frame only while it moves, settles exactly on its
  target, and stops with the component that made it.
- Transitions are values: a variable, a prop or a function can hold one,
  and the zero `Transition{}` plays nothing. They are read once, when the
  element is made, so they cannot depend on a signal, and they cannot be
  compared.
- Everything runs on the Web Animations API (`element.animate` with
  keyframe objects) and one CSSOM property (`style.pointerEvents`), which
  the strict CSP allows: no CSS text is written, as a `style` attribute or
  otherwise. Under `prefers-reduced-motion: reduce`, or in a browser
  without `element.animate`, nothing animates: leaving elements go at
  once, rows do not glide, and springs jump to their targets.
- An app ships the code for motion only when it uses it, and only the
  parts it uses: an `{if}` with transitions ships no list or spring code.
- These are compile errors: `enter`, `exit` or `animate` given a string,
  no value or a value of the wrong type, put on a component, not directly
  inside an `{if}` branch, a `{for}` row or a `{key}` block (`animate`: a
  `{for}` row), or inside blocks that read no signal and so never change
  (`app.den:3:25: enter never plays: the {if}, {for} or {key} around <p>
  reads no signal, so it never adds or removes it`); a transition that
  reads a signal; a `slide` direction that is not one of the four constants; a
  `.For` duration that is not a constant, not a number of ms or s,
  negative, or too long; and den's functions used as values
  (`f := slide`).
- `Transition`, `Layout`, `fade`, `scale`, `slide`, `layout` and `spring`
  are ordinary names: a program that has its own at the top level keeps
  it. `fade`, `scale` and `slide` give way along with `Transition`, and
  `layout` with `Layout`; using one then says why.

**Lifecycle, refs and timers**

```go
import "time"

component Search() {
    box := ref()
    n := signal(0)
    onMount(func() { box.Focus() })
    stop := every(time.Second, func() { n.Set(n.Get() + 1) })
    onCleanup(func() { /* runs when the component goes away */ })
    <div>
        <input ref={box} />
        <p>{n.Get()}</p>
        <button onclick={stop()}>Stop</button>
        <button onclick={after(2*time.Second, func() { n.Set(0) })}>Reset soon</button>
    </div>
}
```

- These belong to the component whose Go calls them: its statements,
  the func literals there (also one passed whole as a prop, which runs
  when the component given it calls it), its handlers (in its list rows
  too) and the markup it passes to another component as children or a
  slot. Each use of a component is one instance, with its own.
- `onMount(f)` runs `f` once, after the build that made the component,
  when its nodes are in the document and its refs are set: in a
  microtask after the page, or the `{if}` branch, `{for}` row or `{key}`
  block holding it, is built. Components mount in the order they were
  built, so a parent before its children. A component that goes before
  then, because the same update took its block away or a panic stopped
  its block being built, never mounts.
- `onCleanup(f)` runs `f` when the component goes, with the `{if}`
  branch, `{for}` row or `{key}` block that made it (the page's own
  components never go). Cleanups run newest first and after everything
  the component made has stopped, as deferred calls do, so a child's run
  before its parent's. Called after the component has gone, `onCleanup`
  runs `f` at once. By then the component's own `computed` values have
  stopped following their signals and may hold old values, so a cleanup
  should read the signals themselves.
- `after(d, f)` runs `f` once, `d` from now; `every(d, f)` runs it every
  `d`. Each returns a `stop` func, and each stops when the component
  goes, so a timer never outlives the component that set it. Calling
  `stop` again does nothing. An `after` that has fired is forgotten, so
  a button that calls `after` costs nothing once its timer has run. `d`
  is a `time.Duration`: `after` with `d <= 0` runs on the next task,
  `every` panics with `d <= 0` (as Go's `time.NewTicker` does), and both
  panic when `d` is longer than a browser timer can wait (2^31-1 ms,
  about 24.8 days). Called after the component has gone, they set no
  timer. Each call starts a timer: a func among the statements that
  calls `after` or `every`, called from markup that den evaluates again
  (a `{...}` that reads a signal, say), starts one each time.
- What `onMount`, `onCleanup`, `after` and `every` run, runs as an event
  handler does: reading signals subscribes to nothing, and the signals
  it sets update the page once, in a microtask. What it makes, such as
  a `computed` or a `spring`, belongs to the component and stops with
  it, so what an `every` callback makes piles up, one per tick, until
  the component goes. A panic in one is reported with `reportError` and
  the rest still run; `every` keeps ticking.
- `ref()` makes a `*Ref`. `ref={r}` on an HTML or SVG element puts the
  element in `r` when den makes it, and takes it out when the block that
  made it goes, unless another element has taken `r` by then.
  `r.Focus()`, `r.Blur()`, `r.Select()` (on elements that have `select`,
  such as inputs) and `r.ScrollIntoView()` call the element. While `r`
  holds no element they do nothing, and on a nil `*Ref` they panic, as a
  nil pointer would in Go. A `*Ref` is a value like any other: pass it
  to a component as a prop and it can focus an input inside it. Den
  rejects `ref=` in a `{for}` row of the markup that holds it, but not
  a `*Ref` given as a prop to components used in rows, nor `ref=` in
  markup passed as children or a slot that the component given it shows
  in its own rows, or more than once: there, the element made last holds
  the ref.
- These are compile errors: `onMount`, `onCleanup`, `after` and `every`
  in a top-level function, which belongs to no component
  (`app.den:3:5: onMount can only be called in a component: in its
  statements, handlers or func literals`); any of them, or `ref`, in Go
  that den evaluates again or once per row or element: the argument of
  `computed` or `spring`, a `{...}` or attribute value in markup, an
  `{if}`, `{for}` or `{key}` header, a list key, a prop (other than a
  func literal that is the whole prop), or the value of `enter=`,
  `exit=`, `animate=` or `ref=` (`app.den:4:32: after cannot
  be called in computed(...): it would run again each time the value is
  recomputed`); `ref=` on a component, given no value, a string or
  anything but a `*Ref`, reading a signal, or inside a `{for}` row, where
  every row would set the same ref; a `Ref` that is not a `*Ref`;
  comparing Refs or times; and den's functions used as values (`f :=
  after`).
- `onMount`, `onCleanup`, `after`, `every`, `ref` and `Ref` are ordinary
  names: a program that has its own at the top level keeps it. `ref` and
  `Ref` give way together.

**Forms**

```go
import (
    "errors"
    "strings"
)

func checkEmail(s string) error {
    if !strings.Contains(s, "@") {
        return errors.New("enter an email address")
    }
    return nil
}

component Signup() {
    name := signal("")
    age := signal(18)
    plan := signal("free")
    email := field("", checkEmail)
    <form onsubmit={e.PreventDefault(); email.Touch()}>
        <input bind={name} />
        <input type="number" bind={age} />
        <label><input type="radio" value="free" bind={plan} /> Free</label>
        <label><input type="radio" value="pro" bind={plan} /> Pro</label>
        <input type="email" bind={email} />
        {if email.Touched() && email.Error() != nil {
            <p>{email.Error().Error()}</p>
        }}
    </form>
}
```

- `bind={x}` keeps an `<input>`, `<textarea>` or `<select>` and `x`, a
  `*Signal[T]` or a `*Field[T]`, in step both ways: what the user enters
  sets `x`, and when `x` changes the element shows it. The element and
  its `type` decide what is bound, and which `T` it takes:

  | Element | `T` | Binds |
  | --- | --- | --- |
  | `<input>` with no type, or of type text, search, email, url, tel, password, hidden, color, date, time, datetime-local, month or week; `<textarea>` | a string | `value`, on `input` |
  | `<input type="number">` or `"range"` | an int or a float | `value`, parsed, on `input` |
  | `<input type="checkbox">` | a bool | `checked`, on `change` |
  | `<input type="radio">` with `value={v}` of type `T`, or `value="..."` when `T` is a string | a string, a number or a bool | checked while `x == v`; checking it sets `x` to `v` |
  | `<select>`, not `multiple` | a string, an int or a float | the chosen option's value, parsed, on `change` |

  (`T` may be any type with one of these underlying types.)
- Text becomes an int as `strconv.Atoi` reads it, and must fit the int's
  kind (an `int8` takes -128 to 127, a `uint` no negative number); it
  becomes a float as `strconv.ParseFloat` reads a decimal number (`1.5`,
  `.5`, `1e3`). Text that does not parse, an empty number input included,
  leaves a signal as it is. A field keeps it as its error
  (`strconv.Atoi: parsing "abc": invalid syntax`) until the text parses.
- When `x` changes, den writes it to the element, unless the element's
  text already means `x`: typing `1.` or `01` into a number input is
  never rewritten under the cursor.
- The element keeps its own handlers: `oninput`, `onchange` and the rest
  run as usual, after bind has set `x`, so they see the new value.
- bind works in `{if}` branches, `{for}` rows and `{key}` blocks, and `x`
  may belong to a row's item (`bind={row.Name}`) or come from context
  (`bind={use[Form]().Email}`). It is read once, when the element is
  made, so it cannot depend on a signal. A leaving element
  (motion) takes no more input, so its binds stop with its handlers.
- A `<select>` is bound after its options are made, so options from a
  `{for}` are chosen from the start. Options added or removed later do
  not change `x`, and choosing one whose value does not parse as `T`
  leaves `x` as it is.
- `field(initial, check)` is a `*Field[T]`: a value with a check, a
  `func(T) error` that may be nil. `Get` and `Set` work as on a signal.
  `f.Error()` is the parse error of the element's text if there is one,
  and otherwise `check(f.Get())`. `f.SetError(err)` puts a server (or other)
  message in that same slot and marks the field touched so it shows at
  once; the next edit, or `Set` / `Reset`, clears it. `f.Touched()` is
  false until an element bound to `f` loses focus, or `f.Touch()` /
  `SetError` is called. An element that den takes away while it has focus
  does not touch `f`: that is not the user leaving the field, though
  browsers send the same event. `f.Reset()` sets `f` back to its initial
  value, untouched and with no parse error; `Set` clears a parse or
  server error too, as the element then shows the value set. `Get`,
  `Error` and `Touched` are reactive, like `.Get()` on a signal.
- A form is plain composition: `ok := computed(name.Error() == nil &&
  email.Error() == nil)`, shown with `{if}`, and `<form onsubmit={...}>`
  with `e.PreventDefault()` to submit it.
- These are compile errors: bind on a component, or on an element other
  than `<input>`, `<textarea>` and `<select>`; on a `<select multiple>`;
  on an `<input>` whose type is `type={...}`, file, button, submit,
  reset, image or a type den does not know; `bind="..."` or `bind`
  without a value, and `Bind` or `BIND` (it is written in lower case);
  an `x` that is not a `*Signal` or a `*Field` (a
  `*Computed` cannot be set), or that reads a signal
  (`bind={rows.Get()[0].Name}`); a `T` that the element does not take
  (`app.den:4:18: bind to an int needs type="number" or "range"`), or
  that is not a string, a number or a bool; a radio without a value, or
  with a value that is not a `T` by Go's rules (`value={1}` fits an
  `int8`, `value={300}` does not); `value=` together with bind
  (`checked=` on a checkbox or radio), since the two would fight; and a
  `<textarea>` with bind and content of its own.
- `field` and `Field` are ordinary names: a program that has either at
  the top level has neither of den's, and using the other says so.

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
  `FormatBool`. `import "errors"`: `New`. Errors support `err != nil`
  and `err.Error()`, and as in Go an error is equal only to itself: two
  `errors.New("x")` are never `==`.
- `import "time"`: `Duration`, its constants `Nanosecond` to `Hour`,
  `d.Milliseconds()` and `d.Seconds()`; `Time`, `Now`, `Since`,
  `t.Sub(u)`, `t.UnixMilli()`, and `t.Hour()`, `t.Minute()` and
  `t.Second()` in local time. A `Time` holds the wall clock and the
  monotonic clock, as Go's does: `Sub` and `Since` use the monotonic
  one, which never jumps, and `Hour` and the rest the wall clock. The
  zero `Time` is Go's (January 1, year 1, UTC). `time.Now` is not a
  signal: to show the time, keep it in one and set it from `every`.
  Den does not compare times with `==` (Go does): compare
  `t.UnixMilli()`, or test `t.Sub(u) == 0`.

`compiler/testdata/conform/` holds Go programs that the tests run with both Go
and den and require to print the same thing.

**Where den still differs from Go.** These are known and planned:
- `int` is a JS number, exact up to ±2^53, and does not wrap around on
  overflow. (The plan is BigInt for explicit `int64` and `uint64`.) So
  is a `time.Duration`, which is exact up to about 104 days; `Sub`
  saturates at Go's limits, which a JS number holds only roughly.
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
go run ./cmd/den fmt -w .
```

The tests compile apps and run them under node against a small fake DOM
(`compiler/testdata/dom.js`), fuzz the keyed list diff, run the conformance
programs against Go, and hold the runtime to 3 KB and every example to
10 KB gzipped. They also fail when a `.den` file in the repository is not
formatted, and hold `den fmt` to compiling every program in the
repository and its tests to the same JavaScript and CSS once formatted.
`examples/todomvc/e2e.js` and `bench/run.js` drive Chromium with
Playwright; they are optional development tools.

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
   router (done), scoped styles and design tokens (done), animations
   (done), `den fmt` (done) and a VS Code extension.
3. **Server and security:** typed server functions with required `auth=`,
   `Secret[T]`, CSRF protection, SSR, source maps and `den test`.
4. **Scale:** `<VirtualList>`, data windows, live streams, and the
   million-row log viewer.
5. **Tooling and launch:** a language server, tree-sitter grammar and
   playground.
6. **Desktop:** `den desktop` on native webviews.

## License

Den is licensed under the [Apache License 2.0](LICENSE).
