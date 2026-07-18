#!/usr/bin/env bash
# Measure statement coverage across the module and enforce a floor.
#
# Two things make a naive `go test -cover ./...` badly under-report this
# codebase:
#
#  1. Most coverage comes from CROSS-package tests — the root integration
#     suites and the in-memory feature suites drive application/, domain/ and
#     infrastructure/ code. Without -coverpkg, each package is credited only
#     for what its OWN tests touch. So we pass an explicit -coverpkg list.
#  2. The root package and ./infrastructure/postgres/ collide on shared
#     projection state when run concurrently against the same database, so
#     packages are run serially (-p 1).
#
# Postgres-backed tests skip unless FLEXITYPE_TEST_DSN is set; without it the
# infrastructure/postgres numbers (and the total) are far lower, so CI always
# sets a DSN.
set -euo pipefail

MIN="${MIN_COVERAGE:-0}"
OUT="${COVERAGE_OUT:-coverage.out}"
HTML="${COVERAGE_HTML:-}"

# Excluded from the measured set: process wiring and sample code that carries
# no independently testable behaviour. Everything else counts — this list is
# deliberately short, and adding to it needs a real justification.
EXCLUDE_RE='/cmd/|/examples/|/internal/demo'

pkgs="$(go list ./... | grep -Ev "$EXCLUDE_RE" | paste -sd, -)"

go test -p 1 -coverpkg="$pkgs" -coverprofile="$OUT" $(go list ./... | grep -Ev "$EXCLUDE_RE")

total="$(go tool cover -func="$OUT" | awk '/^total:/ {gsub("%","",$3); print $3}')"

echo
echo "── coverage by package ──────────────────────────────────────────"
# Merge duplicate blocks (each test binary emits the same source blocks under
# -coverpkg): a block counts as covered if ANY binary executed it.
tail -n +2 "$OUT" | awk '{
  k=$1; if (!(k in st)) st[k]=$2; if ($3+0 > mx[k]) mx[k]=$3+0
} END {
  for (k in st) {
    split(k,f,":"); p=f[1]; n=split(p,s,"/"); sub("/"s[n],"",p)
    tot[p]+=st[k]; if (mx[k]==0) unc[p]+=st[k]
  }
  for (p in tot) printf "%6.1f%%  %5d/%5d  %s\n", (tot[p]-unc[p])*100/tot[p], tot[p]-unc[p], tot[p], p
}' | sort -n
echo "─────────────────────────────────────────────────────────────────"
echo "TOTAL: ${total}%  (floor: ${MIN}%)"

if [ -n "$HTML" ]; then
  go tool cover -html="$OUT" -o "$HTML"
  echo "HTML report: $HTML"
fi

if awk "BEGIN {exit !($total < $MIN)}"; then
  echo "FAIL: coverage ${total}% is below the ${MIN}% floor" >&2
  exit 1
fi
