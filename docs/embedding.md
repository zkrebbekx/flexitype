# Embedding flexitype as a library

The rest of `docs/` describes the standalone service. This describes library
mode: flexitype running inside your process, against your database.

Library-mode knowledge used to exist only as package comments spread across
the facade, so an embedder wiring it from godoc alone could miss contracts
that have security and correctness consequences. Those contracts are here.

## The supported surface

Read [api-stability.md](api-stability.md) first. In short: the facade
(`New`/`NewInMemory`, its `Option`s, `Service`), the interactors reached
through `Service.Interactors(ctx)` and `Service.Factory()`, and the extension
ports. Everything else changes without notice.

## 1. The pool is yours

```go
pool, err := sqlx.Connect("postgres", dsn)
svc := flexitype.New(pool)
```

`New` takes a concrete `*sqlx.DB`, so **`github.com/jmoiron/sqlx` is part of
the supported signature** and a dependency you inherit. flexitype does not
open, close, or resize the pool: set `SetMaxOpenConns` and friends for your
whole application, counting flexitype's usage alongside your own.

Sharing one pool is the point. A value write, your own tables, and the audit
entry all commit together, because they are one transaction on one connection.

## 2. Migrations

```go
if err := svc.Migrate(ctx); err != nil { ... }
```

Call it at startup, before serving. It is idempotent, safe to run
concurrently across replicas (an advisory lock serialises them), and
forward-only.

Order it **before** your own migrations if any of yours reference flexitype
tables, and after if flexitype references none of yours — it never does, so
"first" is always safe.

`svc.SchemaDrift(ctx)` reports migrations the database has that this binary
does not — normal mid-deploy, worth surfacing. See [upgrades.md](upgrades.md).

## 3. Interactors are request-scoped

```go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    ctx := h.requestContext(r)          // tenant, actor, access — see below
    ia := h.svc.Interactors(ctx)        // per request, NOT per process
    ...
}
```

**Do not cache the value `Interactors` returns.** It carries dataloaders that
batch and cache reads within one request; holding it across requests serves
stale reads from that cache. Constructing it is cheap.

## 4. Stamp tenant, actor and access on every request

```go
ctx = uow.WithTenant(ctx, tenantID)
ctx = uow.WithActor(ctx, uow.Actor{ID: userID})
ctx = uow.WithAccess(ctx, accessPolicyFor(user))
```

- **Tenant** scopes every read and write. A context with no tenant is a
  programming error, not a default.
- **Actor** is what the audit log records. Without it the trail says nothing
  about who.
- **Access** is the field-level ACL. The zero value is *full* access, which is
  correct for a trusted internal caller and wrong for anything driven by an
  end user.

Call `uow.RequireAccessPolicy()` once at startup if every request in your
application is user-driven: it makes a missing access policy an error rather
than full access. It is one-way and process-wide, so set it before serving.

## 5. Background work

None of it runs unless you run it.

```go
// Everything, for a single process:
go svc.RunOutboxRelay(ctx)

// Or split the tiers, so an API pod serves and a worker pod delivers:
go svc.RunOutboxRelay(ctx, flexitype.DeliveryLoops{Relay: true, Worker: true, Pruner: false})

go svc.RunChangeSetScheduler(ctx, time.Minute)
```

Across replicas: the relay is safe on every one (it claims batches with a
lease) and so is the delivery worker (`FOR UPDATE SKIP LOCKED`). The pruner
and the scheduler are safe too, but there is no benefit to more than one, and
`DeliveryLoops` is how you keep them on a single tier.

Wire `WithBackgroundErrorObserver` — without it, a scheduler that can never
make progress fails silently.

## 6. Projections are synchronous

The search index and computed attributes are maintained in the writing
request's post-commit, in both delivery modes, so a read after a write
reflects it. `ReindexSearch` and `RecomputeComputed` are the recovery levers
after a crash, not part of the normal path. They are tenant-scoped and safe to
run while serving.

## 7. Events

`WithHandler` registers an in-process handler; `WithPublisher` bridges to your
own bus. Without `WithOutbox` a handler runs in the request's post-commit and
a failure is observed, not retried. With it, envelopes persist in the write's
transaction and the relay retries — at-least-once, so handlers must be
idempotent.

Register handlers during composition. `Service.Dispatcher()` is safe to call
later, but a dispatch already in flight does not pick up a new handler.

## 8. Erasure

`Interactors(ctx).Erasure()` is the right-to-erasure surface. Check
`MediaBlobsFailed` on the report before recording a request as satisfied — see
[erasure.md](erasure.md).

## A minimal embedding

```go
pool, err := sqlx.Connect("postgres", dsn)
if err != nil { return err }

svc := flexitype.New(pool,
    flexitype.WithOutbox(),
    flexitype.WithSearchIndex(),
    flexitype.WithBackgroundErrorObserver(func(err error) { log.Error(err) }),
)
if err := svc.Migrate(ctx); err != nil { return err }

uow.RequireAccessPolicy() // every request here is user-driven

go svc.RunOutboxRelay(ctx)

// per request:
ctx = uow.WithTenant(ctx, tenant)
ctx = uow.WithActor(ctx, uow.Actor{ID: user.ID})
ctx = uow.WithAccess(ctx, policyFor(user))
_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{ ... })
```
