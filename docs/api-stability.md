# API stability & versioning

flexitype versions three surfaces: the **REST API** (`/api/v1`), the
**embedded Go API** (the `flexitype` package and its dependencies), and
the **storage schema** (migrations). This document states what stability
each offers and how changes are communicated.

## Release versioning

Releases follow [Semantic Versioning](https://semver.org/). The project is
currently **pre-1.0 (`0.x`)**: minor versions (`0.y`) may contain breaking
changes, patch versions (`0.y.z`) do not. Every release is a git tag
(`vX.Y.Z`), a GitHub release, and an entry in
[CHANGELOG.md](../CHANGELOG.md). Pin a tag; do not depend on `main`.

At **1.0** the usual SemVer guarantees take effect: breaking changes only
in majors, additive changes in minors, fixes in patches.

## REST API (`/api/v1`)

- The `v1` path prefix is the compatibility boundary. Within `v1`, changes
  are **additive** after 1.0: new endpoints, new optional request fields,
  new response fields. Clients must ignore unknown response fields.
- Breaking changes to request or response shapes ship under a new prefix
  (`/api/v2`); `v1` is then supported for at least one minor release cycle
  with a deprecation notice in the changelog and a `Deprecation` response
  header.
- Error responses carry stable machine codes (`VALIDATION`, `NOT_FOUND`,
  `CONFLICT`, `ARCHIVED`, `DEPENDENCY_VIOLATION`, `FEATURE_DISABLED`,
  `CURSOR_CONFLICT`, `CURSOR_EXPIRED`, `UNAUTHENTICATED`, `FORBIDDEN`).
  New codes may be added; existing codes keep their meaning.
- The event envelope has a `schema_version` field; a bump signals a
  breaking payload change and is called out in the changelog.

## Embedded Go API

- Exported symbols under `github.com/zkrebbekx/flexitype` and its
  subpackages follow SemVer at 1.0. Before 1.0, signatures may change in
  minor releases — the changelog lists them.
- `internal/` is never part of the public API.

## Storage schema

- Migrations are forward-only and idempotent; applying a newer binary to
  an older database is always safe. Downgrading the binary is not
  supported across a migration that changed data shape.
- A migration that changes an existing column or table in a
  backwards-incompatible way only ships in a minor (pre-1.0) or major
  (post-1.0) release and is flagged in the changelog.

## Reporting

Security issues: see [SECURITY.md](../SECURITY.md). Everything else:
GitHub issues.
