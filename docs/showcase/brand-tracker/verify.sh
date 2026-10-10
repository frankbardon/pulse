#!/usr/bin/env bash
# Compares the key figures in ./out against expected.tsv (3-decimal rounding). Run after run.sh.
set -uo pipefail
cd "$(dirname "$0")"
fail=0
while IFS=$'\t' read -r file expr want meaning; do
  [[ -z "$file" || "$file" == \#* ]] && continue
  got=$(jq -r "($expr) * 1000 | round / 1000" "$file")
  if [[ "$got" == "$want" ]]; then status=ok; else status=MISMATCH; fail=1; fi
  printf '%-9s %-10s %-10s %s\n' "$status" "$want" "$got" "$meaning"
done < expected.tsv
exit $fail
