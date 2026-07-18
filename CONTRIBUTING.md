# Contributing to flexitype

Thanks for your interest in flexitype. This guide covers how to build,
test and propose changes.

## Development

```bash
go build ./...                        # everything compiles without a database
go test ./...                         # Go suites (goconvey Given/When/Then)
go vet ./... && golangci-lint run ./... # static analysis
cd web && npm ci && npm test          # console tests + component guard
```

Some tests exercise PostgreSQL; CI runs them against a Postgres 16 service.
Locally, point the DB env vars (see [docs/configuration.md](docs/configuration.md))
at any Postgres 16 instance.

### Coverage

```bash
FLEXITYPE_TEST_DSN=postgres://... ./scripts/coverage.sh   # report
MIN_COVERAGE=90 ./scripts/coverage.sh                     # enforce a floor
COVERAGE_HTML=cov.html ./scripts/coverage.sh              # browsable report
```

CI runs the same script with a floor. The floor **ratchets upward** as coverage
lands — raise it when you add tests, never lower it to make a build pass. Run
with a DSN: the Postgres suites skip without one and the total drops sharply.

Process wiring and sample code (`cmd/`, `examples/`, `internal/demo`) are
excluded from the measured set; that list is deliberately short, and adding to
it needs a real justification rather than being a way to dodge a test.

## Conventions

- **Tests** are goconvey `Given / When / Then`.
- **Errors** carry stable machine codes (`domain/errors`); don't invent new
  HTTP status mappings ad hoc.
- **Writes** flow through the unit of work; repositories join the caller's
  transaction via `WithTx`.
- **SQL** uses `?` placeholders with `sqlx` dollar rebinding, never string
  interpolation of user input.
- **Console** components must be imported in the SFC that uses them —
  `npm run check:components` enforces this.
- Match the surrounding code's naming, comment density and idiom.

## Pull requests

1. Branch from `main`. Direct pushes to `main` are blocked.
2. Keep a PR focused on one change; write a description covering the
   problem, the fix and how you verified it.
3. All CI checks (build/test, lint, vuln scan, web) must pass. Branch
   protection requires the branch to be up to date with `main`.
4. Update `CHANGELOG.md` under `## [Unreleased]` for user-facing changes.
5. For API changes, update `api/openapi.yaml` (it is validated in CI) and,
   if the surface is versioned, note the compatibility impact per
   [docs/api-stability.md](docs/api-stability.md).

## Reporting

Security vulnerabilities: see [SECURITY.md](SECURITY.md). Bugs and feature
requests: open a GitHub issue.
