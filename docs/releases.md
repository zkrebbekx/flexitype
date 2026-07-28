# Verifying a release

Release artifacts used to ship with SHA-256 checksums only. A checksum proves
a download was not corrupted; it proves nothing about where the file came
from, because anyone can publish a file and its correct checksum. Releases now
also carry a signature, a provenance attestation and an SBOM.

## Signature (cosign, keyless)

Every artifact is signed with a `.cosign.bundle` beside it. Keyless signing
binds the signature to the release workflow's OIDC identity, so there is no
private key to store, rotate or leak.

```bash
cosign verify-blob \
  --bundle flexitype_v1.3.0_linux_amd64.cosign.bundle \
  --certificate-identity-regexp '^https://github.com/zkrebbekx/flexitype/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  flexitype_v1.3.0_linux_amd64
```

A pass means this file was produced by that workflow in this repository. It
does **not** mean the workflow was doing something you want — read the
provenance for that.

## Provenance (SLSA)

Each binary carries a build attestation naming the workflow, the commit and
the runner:

```bash
gh attestation verify flexitype_v1.3.0_linux_amd64 --repo zkrebbekx/flexitype
```

## SBOM

`flexitype-sbom.spdx.json` lists every module in the build, in SPDX JSON. Feed
it to your scanner rather than re-deriving the dependency set from `go.mod` —
the SBOM is what was actually built.

## Checksums

`checksums.txt` is still published, for tooling that wants it. Prefer the
signature: a checksum only tells you the bytes match the checksum next to
them.

## Container image

The image is distroless, runs as non-root, and declares a `HEALTHCHECK` that
calls `/readyz` through the binary itself (the only executable in the image).
Pull by digest rather than tag for a reproducible deployment.
