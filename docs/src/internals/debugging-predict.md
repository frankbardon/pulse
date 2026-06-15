# Debugging a Predict Mismatch

**Audience:** Pulse internals contributors and embedders triaging a
case where `pulse predict` says a request is valid but execution
fails, or vice versa.

`pulse predict` reads only the `.pulse` header and schema; it never
touches record data. That makes the predict path fast and safe to
call from an LLM agent, but it also means predict can only catch
structural and shape errors — type mismatches, unknown field names,
operators that cannot accept a given field type. Anything that
depends on the actual record values (e.g. a divide-by-zero in
`ATTR_FORMULA`) is a runtime concern.

## The triage sequence

Run predict against the request first:

```bash
pulse predict --json < request.json
```

Read the envelope's `errors` and `warnings` arrays — predict returns
both. Errors are structural failures; warnings are advisories that
identify low-quality input but do not block execution.

The most common issues:

| Symptom | Likely code |
|---|---|
| Field-name typo | `SERVICE_VALIDATION` |
| Numeric aggregation on a categorical field | `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` |
| Description below the quality threshold | `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` |
| Operator's `AcceptsTypes` excludes the field | `SERVICE_VALIDATION` |
| `Filterer` referenced from `Aggregation` not declared | `SERVICE_VALIDATION` |

Resolve the typo / mismatch and re-run predict.

## Cross-check against the actual schema

If predict's error mentions a field that you believe should exist,
read the schema with `pulse inspect`:

```bash
pulse inspect --json <file.pulse>
```

The inspect output lists every field's name, type, nullability flag,
and (if present) description. A mismatch between what you think the
schema says and what predict / inspect actually reads almost always
traces to a stale cohort file or a request crafted against a
different schema generation.

## When predict passes but runtime fails

If predict reports the request as valid but execution fails, the bug
is in the processing layer — not predict.

`descriptor/predict.go` has a structural ban on importing `service/`
and `processing/` (`TestPredictNoExecutionImports`). The predict path
runs against `encoding.ReadHeader` + `encoding.ReadSchema` only,
never against records. A divergence between predict's verdict and
runtime's verdict is therefore always either:

- A processing-layer bug (the runtime rejects something predict had
  no way to know was bad), or
- A capability declaration that drifted from the registry (the
  capability row says the operator accepts a type the runtime
  rejects, or vice versa).

For the second case, the registry-vs-capability parity gates are:

- `TestManifestOperatorsComplete`
- `TestStreamability_AggregationsKnown`
- `TestPredict_Streamable_MatchesRuntime`
- `TestCanStreamRequest_RegressionMatrix`

Re-run them locally and inspect any failure — they catch capability
drift before it ships.

## Streamability mismatch

Predict surfaces a per-slot `Streamable` flag derived from the
per-type `Streamable()` methods plus schema gates (decimal). The
runtime parity check is `processing.CanStreamRequest(req, schema)`.
A divergence means either:

- `Streamable()` returned a value that doesn't match the
  `OnlineAggregator` capability of the registered operator
  (caught by `TestRegistryStreamabilityMatchesTypes`), or
- The schema gate (decimal) was missed in one of the two computations.

Both surfaces share helpers in `types/streamability.go`; the
drift is almost always in the table at the top of that file.
