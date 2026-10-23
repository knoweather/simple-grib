# Security policy

## Supported versions

Only the latest tagged release receives fixes. `main` is the development
branch; use a tag for production.

| Version | Supported |
|---|---|
| latest `v0.0.x` tag | yes |
| `main` / `dev` | development only |

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

- Preferred: open a private report via
  [GitHub Security Advisories](https://github.com/knoweather/simple-grib/security/advisories/new).
- Alternative: email the maintainer at the address on the GitHub profile.

Please include the version or commit, a minimal reproduction (a GRIB file plus
the call that misbehaves is ideal), and the impact you see. Files that decode
to wrong values are as interesting as crashes.

## What to expect

This is a single-maintainer project, so replies are best effort: an
acknowledgement is aimed at within 7 days, and a fix or mitigation for
confirmed issues within 30 days. You will be credited in the advisory unless
you ask otherwise.

## Scope

In scope:

- panics, hangs, unbounded memory use, or out-of-bounds reads triggered by a
  GRIB file
- wrong decoded values that differ from ecCodes
- path traversal or unsafe file handling when writing GRIB

Out of scope:

- resource exhaustion from simply choosing to decode a very large file
- issues in code that is not part of this module (ecCodes itself, WMO data)

## Safe defaults

The library validates structure before decoding and fails loudly instead of
guessing: unknown templates, truncated sections, and non-GRIB input return
errors rather than panics. If you find an input that breaks that promise, it
is a bug worth reporting.
