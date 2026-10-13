```yaml
name: vector-fields
description: Native vector fields (vec_f32 / vec_f64) end to end — importing a battery or a list column into one field, naming it directly in vector slots, f[k] element references, how its member identity differs from a scalar twin, null vs NaN, export shapes, and what refuses it. Use when a cohort stores embeddings or a rating battery as one field.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [vec_f32, vec_f64, native vector, vector field, list column, element member, f[k], vector import, vector export]
```

# Native vector fields

A `vec_f32` / `vec_f64` field stores a fixed-length numeric vector (dim 2..1024) per row, instead of dim scalar columns. Bytes, range and null bits: [`type-vec-f32`](type-vec-f32.md) / [`type-vec-f64`](type-vec-f64.md). The field carries its own kind (`measure`, `scale`, `composition`, `binary`), optional scale range and element labels in the schema; `pulse_inspect` reports them under the field's `vector` block.

## Creating one

- **Fold columns:** `--vector NAME:/REGEX/` or `NAME:COL1,COL2,...` (`NAME:f32:...` for `vec_f32`); MCP `pulse_import` takes the same strings as `vectors` / `vector_kinds`. The members vanish from the cohort. Declare meaning with `--vector-kind NAME:KIND[:MIN:MAX]`.
- **Native list column** (Arrow / Parquet LIST, NDJSON / JSON array): a bare `--vector NAME` (or `NAME:f32`). Dim = the first non-null list's length; a ragged list, non-numeric element or non-list cell refuses the whole import (`PULSE_IMPORT_VECTOR_INVALID`, `details.reason`). Without a declaration a list column still imports as a multi-select set.
- **Schema file:** type `vec_f64` / `vec_f32` with a required `dim`, optional `labels`, `kind`, `scale_min` / `scale_max`.
- **Go:** `Pulse.NewCohortBuilder` takes a slice of exactly dim elements per cell.

Vector import is never feature-gated; only the matrix operators that consume vector fields are.

## Naming it in a request

A native vector needs no `Request.Vectors` entry: name the field in any `vector` slot (`"vector": "emb"`). A spec named like a native field is `PULSE_VECTOR_NAME_COLLISION` (`details.native: true`). To re-kind, re-label or re-scope one for a single request, list it in a spec's `fields` — a literal or glob that matches it expands to its element members. A single element is `emb[k]` (**1-based**, 1..dim). Default element labels are the schema's, else `emb_<k>` (the same names an expanded export uses).

**Member identity differs from a scalar twin.** A battery stored as columns `q1..q5` and the same battery as a native vector give identical values, counts and labels, but wherever a result names a member (`row_keys`, `details` `members`, warning text) the native one says `emb[k]` where the twin says the column name. Map names before comparing runs.

## Null and NaN

A null vector is the field's null bit; a NaN element is a stored value. In a vector slot a NaN element is a null member and a null vector makes every element null, exactly as the scalar twin's null columns behave. Presence slots (counts, null counts, the null filter, the `{n, n_null}` floor) count a null vector as null and any other as present.

## What refuses a vector

Anything reading ONE value per record: a scalar aggregation, grouper, attribute, feature, test or regression input, window or sort key, weight, date slot, lookup key. All return `PULSE_VECTOR_FIELD_NOT_SCALAR` (predict and runtime alike; `details.suggest` names the vector operator to use). A join key is `PULSE_JOIN_TYPE_MISMATCH` (`reason: vector_key`). `pulse convert` refuses a vector schema (`PULSE_IMPORT_VECTOR_INVALID`, `not_declared`). `pulse_recommend` proposes `vector: "<field>"` in vector slots, never a vector in a scalar slot.
A vector cohort cannot take the SPSS metadata sidecar path (`PULSE_SPSS_SIDECAR_INVALID`; `--ignore-sidecar`).

## Export

CSV, TSV and Excel expand it to `<name>_<label>` columns (`<name>_1..N` unlabelled; empty for a NaN element or null vector). The source header comes back only when the vector is named after the column prefix: `rate:/^rate_(.*)$/` exports `rate_<label>`. A name clash is `PULSE_EXPORT_COLUMN_COLLISION`. NDJSON and JSON array write one array (`null` for a non-finite element or a null vector). Arrow writes `FixedSizeList`; Parquet the same, but `LIST` when the field is nullable. An all-NaN vector re-imports as null in every format.

## Synthetic data

A vector is ONE joint draw, never dim scalars. A spec field typed `vec_*` needs a `vector` block: `dim`, optional `kind`, `scale_min` / `scale_max`, `labels`, `nan_rate`, `elements` (per-element `{distribution, params, nan_rate}`) and `correlations` (`{a, b, correlation}`, 1-based). The field's `distribution` is the default element family (`normal`, `uniform`, `lognormal`, `exponential`, `bernoulli`, `discrete`, `mixture`). The kind bounds every element: scale clamps to the range (`discrete` with no `values` = the integers in it), binary rounds to 0/1, composition rows sum to 1. `profile create` captures per-element marginals, NaN rates and pairwise-complete correlations (none above 64 elements, with a warning); from-profile and the top-up regenerate them; the fidelity report adds `vector_fields`. A vector never joins a correlation, pair or model, and a rule may only `set_null` it.

## Shards

Dim and element type must match across shards (`PULSE_SHARD_SCHEMA_MISMATCH`, `details.dimension: vector_dim`); labels, kind and scale divergence only warn (`PULSE_SHARD_DESCRIPTION_DIVERGENCE`). See [`cohort-sharding`](cohort-sharding.md).

## See

- [`type-vec-f32`](type-vec-f32.md), [`type-vec-f64`](type-vec-f64.md), [`cohort-schema-design`](cohort-schema-design.md), [`tool-import`](tool-import.md).
