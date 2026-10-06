#!/usr/bin/env bash
# Runs every matrices example through predict + process.
set -euo pipefail
cd "$(dirname "$0")/../../.."
for f in internal/examples/matrices/*.json; do
  echo "== $f"
  bin/pulse api predict --request "$f" --json >/dev/null
  bin/pulse api process --request "$f" --json
done
