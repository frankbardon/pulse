# Materialize Examples

Runnable JSON requests for the materialize-only `output` slot, which lays
out the cohort `pulse api materialize` writes (columns, renames, casts,
recode, bucket, shards, constants, parent groups). `process` and every other
operation refuse a request carrying `output` (`PULSE_MATERIALIZE_OUTPUT_ONLY`).
Predict first with `--predict`: it reads header and schema only and returns
the output columns and risks. Skills: `derived-cohorts`, `materialize-casts`,
`materialize-recode`, `materialize-container`.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./internal/examples/materialize/run-all.sh
```

## Catalog

| File | Demonstrates |
|---|---|
| `01_reshape_for_handoff.json` | Pick, rename, bucket and describe columns; drop one; `elide_constants` |
