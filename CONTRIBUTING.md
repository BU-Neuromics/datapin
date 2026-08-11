# Contributing to datapin

Thank you for your interest in contributing. This document explains how to get
started, the standards the project uses, and what to expect from the review
process.

## Prerequisites

- Go 1.24 or later (`go.mod` is the source of truth)
- `golangci-lint` v2 for local linting (`brew install golangci-lint` or see
  [golangci-lint install docs](https://golangci-lint.run/welcome/install/))
- A personal OSF token for manual smoke tests (optional but useful)

## Getting started

```console
git clone https://github.com/BU-Neuromics/datapin
cd datapin
go build -o datapin .
go test ./...
```

## Development workflow

### Branching

Cut a feature branch from `main`:

```console
git checkout -b your-handle/short-description
```

Keep branches focused on one logical change. Open a pull request when ready.

### Test-driven development (required)

This project uses TDD. **Write the failing test first, then the code that
makes it pass.** This is non-negotiable:

1. **Red** — write a test that encodes the desired behaviour; confirm it fails.
2. **Green** — write the minimum production code to make it pass.
3. **Refactor** — clean up with the test suite as a safety net.

Bug fixes **must** start with a regression test that reproduces the bug. If a
bug shipped without a test, the fix adds the test that would have caught it.

No production logic lands without a test that exercises it. The only exceptions
are thin Cobra `RunE` glue and `main()`.

### Running tests

```console
go test ./...                               # unit tests
go test -race ./...                         # with race detector
go test -tags integration -count=1 ./integration/...  # end-to-end tests
```

### Linting and formatting

```console
gofmt -l .            # must print nothing
go vet ./...          # must pass clean
golangci-lint run     # must report no issues
```

### Definition of done

A change is ready to merge when:

1. A failing test was written first (or a regression test for a bug fix).
2. `go build ./...` succeeds.
3. `go test -race ./...` passes.
4. `gofmt -l .` prints nothing.
5. `go vet ./...` is clean.
6. `golangci-lint run` reports no issues.
7. `--output=json` is supported and produces valid JSON (for command changes).
8. `--help` text is accurate.
9. Non-zero exit codes on all error paths.

## Pull request process

1. Fill in the PR template (summary, test plan).
2. Keep PRs small and focused — one logical change per PR is easiest to review.
3. Link to any related issue.
4. The CI suite must be green before merging.
5. At least one maintainer approval is required.

## Reporting bugs

Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.yml). Include
the `datapin --version` output, your OS and architecture, and the exact command
you ran.

## Requesting features

Use the [feature request template](.github/ISSUE_TEMPLATE/feature_request.yml).
Describe the use case rather than the implementation — explaining *why* helps
maintainers evaluate fit.

## Code style

- Follow standard Go idioms (`gofmt`, `go vet`).
- No comments that explain *what* the code does — good names do that.
  Only add a comment when the *why* is non-obvious (a hidden constraint,
  a subtle invariant, a workaround for a specific upstream bug).
- Keep `RunE` bodies thin; push decision logic into `internal/` functions
  that can be unit-tested.
- New dependencies on the network, filesystem, clock, or keychain must be
  injectable (interface or field) so tests can fake them.

## Commit messages

Use the imperative mood for the subject line:

```
Add --conflict=rename support to push
Fix path resolution for Windows separators
```

Reference issues with `Fixes #N` or `Closes #N` in the body when applicable.

## License

By contributing you agree that your contributions will be licensed under the
[MIT License](./LICENSE) that covers this project.
