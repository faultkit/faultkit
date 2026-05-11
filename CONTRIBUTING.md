# Contributing to faultkit

Thanks for your interest in faultkit. The project is open to outside
contributions; this document is the short version of how to land one.

## Before you start

- For anything beyond a typo or small bug fix, open an issue first.
  This avoids the case where you write a patch that ends up out of scope.
- Read `CLAUDE.md` if you're going to touch code. It encodes the
  project's non-negotiables (the OSS/Pro boundary, no new dependencies
  without approval, exit-code stability, etc.).
- v0.1 is intentionally narrow. See `docs/internal/V0.1_SPEC.md` for
  what's in scope right now.

## Building

```
make build
make test
make lint
```

`make lint` requires `golangci-lint` on your `PATH`:

```
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

## Filing a PR

1. Fork, branch, push.
2. Each commit should be one logical change with a short imperative
   subject (under 72 chars) and a body that explains *why*.
3. The first PR you open will be greeted by a CLA bot. The CLA is
   a standard Apache-style individual CLA; signing takes about 30
   seconds and is one-time.
4. Keep diffs surgical. Don't refactor adjacent code, don't reformat
   things that aren't yours, don't add features that weren't asked for.
5. Make sure `make lint test` is green locally before pushing.

## Reporting bugs

[Open an issue](https://github.com/faultkit/faultkit/issues/new)
with: faultkit version (`faultkit version`), platform + kernel
(`uname -a`), exact command run, expected vs. observed behavior.
The output of `faultkit check` helps a lot.

## Asking questions

[GitHub Discussions](https://github.com/faultkit/faultkit/discussions)
is the right place. Don't open issues for usage questions.
