# Security policy

Security is one of Den's five principles: unsafe patterns are compile errors,
generated pages run under a strict Content Security Policy with Trusted Types,
and the toolchain has no npm dependencies. If you find a way around any of
that, we want to hear about it.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub's
[private vulnerability reporting](https://github.com/TriadStudios/Den/security/advisories/new)
(the repository's **Security** tab, then **Report a vulnerability**).

Include what you found, how to reproduce it (a `.den` file is ideal), and
what an attacker could do with it. You will get an acknowledgement within
7 days.

## Disclosure

Den follows a 90-day disclosure policy: we aim to fix a confirmed
vulnerability and publish an advisory within 90 days of the report, sooner
for severe issues, and we credit reporters who want to be credited. If a fix
needs longer, we will agree a date with you.

## Supported versions

Den is experimental and has no releases yet. Security fixes go to the main
branch.

## What counts

Examples of what we treat as vulnerabilities:

- A way to inject HTML or script through den markup, attributes or URLs.
- A compiled app that needs `unsafe-inline`, `unsafe-eval` or a Trusted
  Types policy to run.
- A dangerous URL scheme that passes the compile-time or runtime checks.
- The den toolchain executing or fetching code it should not.
