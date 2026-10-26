# Contributing to Den

Thanks for helping. Den is experimental and maintained part time, so a short
discussion before a large change saves everyone time: open an issue first for
anything bigger than a bug fix or a small, clearly scoped improvement.

## Scope

Den's scope is the [roadmap](README.md#roadmap), and changes are judged
against five principles: simple, type safe end to end, fast by construction,
secure by default, and beautiful by default. Requests outside that scope, such
as running npm packages or full Go compatibility, will be declined even when
they are good ideas, so that the language stays small and consistent.

Good places to start are issues labelled `good first issue`: docs, examples
and components.

## Development

```sh
go test ./...   # needs node on PATH; the runtime tests run compiled apps under node
go vet ./...
gofmt -l .      # must print nothing
```

- Every change to what the compiler accepts needs a test. Compile errors go in
  `TestErrors` in `compiler/compiler_test.go` with the exact `file:line:col`
  and message; runtime behavior goes in `compiler/runtime_test.go`.
- If you add a Go feature, add a program to `compiler/testdata/conform/`.
  The tests run it with both Go and den and require the same output. If den
  cannot match Go exactly, the feature should be a compile error instead.
- The runtime must stay under 3 KB gzipped and every example under 10 KB
  gzipped; the tests enforce both. `bench/run.js` measures speed against
  vanilla JS, and performance changes should include its numbers.
- Security rules (no raw HTML, no inline handlers, safe URLs, strict CSP) are
  not optional. A change that weakens one will not be merged.
- Den has no dependencies beyond the Go standard library and no npm. Please
  keep it that way.

## Pull requests

- Keep each pull request to one change, with a description of what it does
  and why.
- Match the surrounding code: gofmt, short comments that explain why, and
  error messages that say what is wrong in plain words and suggest a fix.
- CI runs gofmt, `go vet`, `go test -race`, builds every example and runs
  `govulncheck`. It must pass before review.

By contributing you agree that your contributions are licensed under the
project's license, and you agree to follow the
[code of conduct](CODE_OF_CONDUCT.md).
