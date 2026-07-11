# Security policy

## Supported versions

flexitype is pre-1.0. Security fixes land on `main` and in the next
tagged release. Once 1.0 ships, the latest minor line will receive
security patches.

| Version | Supported |
| --- | --- |
| latest `0.x` / `main` | ✅ |
| older tags | ❌ (upgrade to the latest release) |

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately through GitHub's
[private vulnerability reporting](https://github.com/zkrebbekx/flexitype/security/advisories/new)
("Report a vulnerability" on the Security tab). Include:

- a description of the issue and its impact,
- steps to reproduce (a minimal proof of concept if possible),
- affected version or commit.

You can expect an acknowledgement within a few days. We'll work with you
on a fix and a coordinated disclosure, and credit you in the advisory
unless you prefer to remain anonymous.

## Scope notes

flexitype is tenant-isolated and ships an SSRF guard on webhook delivery,
constant-time service-account authentication, and HMAC-signed webhooks.
Deployments are responsible for configuring TLS termination, network
policy, database credentials and service-account secrets appropriately;
see [docs/configuration.md](docs/configuration.md).
