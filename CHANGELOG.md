# Changelog

All notable changes to flexitype are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once it reaches 1.0 (see [API stability](docs/api-stability.md)).

## [Unreleased]

## [0.1.0] — 2026-07-11

First tagged release. The full feature set is production-verified against
PostgreSQL 16 and covered by the test suite.

### Added

- **Soft types & attributes** — runtime-defined `TypeDefinition` →
  `AttributeDefinition` → `AttributeValue` over an opaque `entity_id`, with
  12 data types and constraints (min/max length, min/max value, RE2
  pattern, one-of; required / multi-valued / unique flags).
- **Attribute dependencies** — cascading picklists and conditional
  validation (equals / in / range / pattern / dynamic-time), resolved as a
  per-entity effective schema.
- **Type inheritance** — single-inheritance hierarchies with
  hierarchy-wide no-shadowing, subtype-anchored values, and cross-level
  dependencies and relationships.
- **Relationships** — user-defined directed (parent/child, optional role
  labels, per-side version pinning) and symmetric (unordered peer)
  relationship types, each with their own attributes, constraints and
  definition inheritance.
- **FQL** — a schema-aware query language (comparisons, `in`, `range`,
  `has`, `length`, `min`/`max`/`count`, string matching, boolean nesting,
  `type isa`, `child()`/`parent()`/`linked()` traversals, `matches()`
  full-text) executed identically over PostgreSQL and the in-memory store.
- **Domain events** — a strongly-typed dispatcher fanning a stable JSON
  envelope to consumer hooks; HMAC-signed webhooks and pub/sub publishers
  built in.
- **Event delivery for external services** — a transactional outbox with
  gap-free feed sequencing, managed webhook subscriptions (signed
  deliveries, exponential backoff, dead-lettering, redrive) with an SSRF
  guard, a cursor-paged events feed with SSE tail and compare-and-swap
  cursors, and a Google Cloud Pub/Sub publisher.
- **Activity log** — every change audited with JSON before/after
  descriptors, written in the same transaction as the change.
- **Search index** (optional) — an event-driven per-entity projection
  powering FQL `matches()`, with trigram-accelerated `contains`.
- **Admin console** — a Vue 3 SPA for modelling types, attributes,
  dependencies and relationships, browsing entities with dependency-aware
  editing, and auditing changes.
- **WebAssembly playground** — the whole service compiled to WASM over an
  in-memory store, hosted on GitHub Pages.
- **Deployment shapes** — embedded Go library and standalone service
  (versioned REST API, service-account auth, OpenTelemetry, health
  endpoints), multi-tenant from day one.

### Security

- Tenant isolation enforced on every by-ID interactor path (no
  cross-tenant reads or writes).
- Webhook delivery blocks private, loopback, link-local and cloud-metadata
  targets at dial time (SSRF guard), overridable for on-prem.

[Unreleased]: https://github.com/zkrebbekx/flexitype/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/zkrebbekx/flexitype/releases/tag/v0.1.0
