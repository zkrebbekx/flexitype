# Identity & access: what 1.0 does, and the roadmap

This note states the identity model flexitype ships at 1.0 and how it is
expected to grow, so adopters can design around it with confidence.

## What 1.0 provides

The only principal is a **machine service account** authenticated by a bearer
token (`ft_<account>_<secret>`):

- **Scopes** — `read`, `write`, `admin` gate the API coarsely (`admin` is
  required for the provisioning and erasure endpoints).
- **Field-level access control** — per-attribute `read` / `write` / `none`
  permissions on an account, enforced through the value read/write paths, the
  effective schema, grid / facets / export, and the FQL binder (an unreadable
  attribute is invisible, not leaked).
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
2. **Roles / groups** layered over the current scopes + field ACL, so
   permissions are assigned by role rather than per account.
3. **SSO** via OIDC / SAML, and an invite flow, for the hosted multi-tenant
   tier.

Until then: model human operators as individual service accounts (one per
person, not shared) if you need per-person attribution, and put SSO at your
edge (an authenticating proxy) if your deployment requires it. These additions
are intended to be **additive** — the service-account model and field ACL
remain the substrate.
