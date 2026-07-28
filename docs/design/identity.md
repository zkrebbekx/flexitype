# Identity & access: what 1.0 does, and the roadmap

This note states the identity model flexitype ships at 1.0 and how it is
expected to grow, so adopters can design around it with confidence.

## What 1.0 provides

The only principal is a **machine service account** authenticated by a bearer
token (`ft_<account>_<secret>`):

- **Scopes** — `read`, `write`, `admin` gate the API coarsely (`admin` is
  required for the provisioning and erasure endpoints).
- **Field-level access control** — per-attribute `read` / `write` / `none`
  permissions, enforced through the value read/write paths, the effective
  schema, grid / facets / export, and the FQL binder (an unreadable attribute
  is invisible, not leaked).
- **Roles** — a named permission set that many accounts hold. See
  [Roles](#roles) below.
- **Tenancy** — every account is pinned to a tenant; all data access is
  tenant-scoped.
- **Provisioning** — accounts and tenants are managed at runtime through the
  admin API (or a static file). Tokens are shown once and can be rotated or
  revoked; a database-backed auth cache bounds revocation latency.

This is sufficient for service-to-service integration and for a UI that signs
in with a service-account token. Change-set approval enforces separation of
duties between the *author* and *approver* principals (which today are service
accounts).

## What 1.0 does not provide (and the roadmap)

flexitype has **no human-user identity model** distinct from service accounts,
and no built-in single sign-on. Planned, in rough priority order:

1. **Human users** modelled distinctly from machine accounts, so audit and
   change-set approval attribute actions to a person, not a shared token.
2. **SSO** via OIDC / SAML, and an invite flow, for the hosted multi-tenant
   tier.

Until then: model human operators as individual service accounts (one per
person, not shared) if you need per-person attribution, and give each one a
**role** rather than its own copy of the permission set. Put SSO at your edge
(an authenticating proxy) if your deployment requires it. These additions are
intended to be **additive** — the service-account model, roles and the field
ACL remain the substrate.

## Roles

A role is a named permission set inside one tenant. It owns a scope set and a
per-attribute permission map. An account holds role names; nothing is copied
onto the account.

Before roles existed, a deployment with 500 operators across 5 permission
profiles needed 500 accounts, each carrying its own copy of the permission
map. Restricting one more attribute meant hundreds of writes that no
transaction spanned, the permission applied inconsistently while the rollout
ran, and "who can read this attribute" could only be answered by reading every
account. A role makes that one write, and one record to read.

### Resolution

The merge happens at **authentication**, not at write time, so a change to a
role reaches every account holding it as soon as the auth cache entry expires.
Nothing is written onto the accounts.

| Part | Rule |
|---|---|
| Scopes | The union of the account's own scopes and every role's scopes. A role grants; it never revokes. **A role may not grant `admin`.** |
| Field permissions | The **most permissive** level any role grants for that attribute. |
| Account override | The account's own entry for an attribute wins over every role. |

`write` outranks `read`, which outranks `none`. A level that is not one of the
three ranks below `none`, so a typo denies rather than grants — and the write
paths refuse such a level anyway, with a 422.

The account override exists so one person can be given an exception without
inventing a role for them. Use it sparingly: an override is invisible in the
role list, which is the record an auditor reads.

### Rules that the API enforces

- An account may hold no scope of its own when it holds a role. An account
  with neither a scope nor a role is refused: the credential would grant
  nothing.
- A role may carry field permissions and no scope. "This person sees every
  attribute except salary" is a real grant.
- An assignment that names a role the tenant does not have is refused with a
  404. A missing role resolves to nothing, so a typo would otherwise look
  exactly like a role that grants nothing.
- Roles are per tenant. An account resolves only its own tenant's roles, even
  when another tenant has a role of the same name.
- **A role may not carry the `admin` scope.** `admin` is a global,
  cross-tenant privilege *and* it short-circuits the whole field ACL, so a
  role granting it would void the account's own `field_permissions` —
  contradicting the precedence rule above — and confer the control plane. It
  would also be invisible: the account row would still read `scopes:
  ["read"]` with a field restriction, so a permissions review would call it
  safe. Grant `admin` directly on an account, where it shows. A role row
  carrying `admin` from a direct database edit is dropped at resolution.
- **Deleting a role that accounts still hold is refused** (409). A role that
  resolves to nothing contributes no field permissions, and an empty
  permission map reads as "unrestricted" — so deleting a held role would hand
  every account it restricted the attributes it was hiding, silently, with no
  change to the account rows. Reassign those accounts first
  (`PUT /service-accounts/{id}/roles`). To retire a role's grants without
  touching every account, upsert it with an empty scope set and the
  restrictions you want to keep.
- **A principal naming a role that does not exist is denied every
  attribute.** The API refuses the delete that would create that state; this
  is the second line, covering a row edited directly in the database. Read
  `unresolved_roles` on the effective-permissions view to find such an
  account.
- A role write **replaces** the whole role. A partial update would make "what
  does this role allow" a question about history rather than about the current
  record.
- Deleting a role removes its grants. An account that names it keeps the name
  and resolves nothing for it, which is the safe direction.
- **Every permission change evicts the auth cache.**
  `PUT /service-accounts/{id}/roles` drops that account's entries; a role
  write or delete, and a tenant deactivation, drop every entry for the
  tenant. A change an operator makes during an incident applies at once
  rather than at the end of the cache TTL on every replica.

### Endpoints

All need the `admin` scope.

| Method | Path | Purpose |
|---|---|---|
| `PUT` | `/api/v1/roles` | Create or replace a role |
| `GET` | `/api/v1/roles?tenant_name=…` | List a tenant's roles |
| `DELETE` | `/api/v1/roles/{name}?tenant_name=…` | Delete a role |
| `PUT` | `/api/v1/service-accounts/{id}/roles` | Replace an account's roles and overrides |
| `GET` | `/api/v1/service-accounts/{id}/effective` | Report an account's resolved permissions |

`POST /api/v1/service-accounts` also accepts `roles` and `field_permissions`.

### Example

Create a role that reads everything except salaries, then provision an account
that holds it and nothing else:

```bash
curl -X PUT $BASE/api/v1/roles -H "Authorization: Bearer $ADMIN" \
  -d '{"tenant_name":"acme","name":"analyst",
       "description":"reads everything except salaries",
       "scopes":["read"],
       "field_permissions":{"salary":"none"}}'

curl -X POST $BASE/api/v1/service-accounts -H "Authorization: Bearer $ADMIN" \
  -d '{"tenant_name":"acme","name":"jamie","roles":["analyst"]}'
```

To answer "what can this account actually do", read its effective
permissions — its own scopes unioned with its roles', and the merged
per-attribute levels:

```bash
curl "$BASE/api/v1/service-accounts/$ID/effective" -H "Authorization: Bearer $ADMIN"
```

```json
{ "name": "jamie", "roles": ["analyst"], "scopes": ["read"],
  "field_permissions": {"salary": "none"}, "unresolved_roles": [] }
```

The list endpoints report what was **assigned**, which is what you edit; this
reports what the enforcement path **computes**, through the same resolver. A
non-empty `unresolved_roles` is a fault, not a note: that principal is denied
every attribute until the role is restored or the account reassigned.
