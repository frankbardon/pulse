---
name: vector-metrics
description: Declaring what a battery's values mean (vector kind measure, scale, composition or binary) so similarity and distance pick a suitable metric by default, the scale bounds a kind "scale" battery carries, and the warning (an error under strict) an explicit metric that fits the kind poorly raises. Topical design; per-operator params in the atomic skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [vector kind, metric defaults, similarity, distance, cosine, aitchison, scale bounds, unsuited metric]
requires: [capability:matrices]
---

# Vector kind and metric choice

A distance or similarity is only as good as its fit to what the numbers mean. Cosine ignores level and magnitude, so two respondents rating everything 1 and everything 5 look identical; Euclidean distance ignores that shares sum to a whole. Declare the meaning once on the vector and let the operators choose.

## Declare the kind

`{"name": "sat", "fields": ["q*"], "kind": "scale", "scale_min": 1, "scale_max": 5}`

| `kind` | Values | Distance default | Similarity default |
|---|---|---|---|
| `measure` | magnitudes (spend, load) | `std_euclidean` | — |
| `scale` | bounded ratings (Likert) | `euclidean` | `correlation` |
| `composition` | parts of a whole (shares, mix) | `aitchison` | `cosine` |
| `binary` | membership (0 / 1, multi-select) | `hamming` | `jaccard` |

No `kind` declares nothing: no default, no warning. An unknown kind is `PULSE_VECTOR_INVALID` (`reason` `unknown_kind`). The manifest lists the table under `matrix.vector_kinds` and each metric's `kinds_suited` / `poor_fit` under `matrix.metrics`.

## Scale bounds

`scale_min` / `scale_max` belong to a `kind: "scale"` vector only, set together, finite, min below max — otherwise `PULSE_VECTOR_INVALID` (`scale_bounds_without_scale_kind`, `scale_bounds_partial`, `scale_bounds_invalid`). They are the battery's declared response range: an operator that range-checks or reverse-scores items takes them when it sets no range of its own (its own params always win).<!-- feature: MAT_RELIABILITY --> `MAT_RELIABILITY` then checks every value against them and `params.reverse` needs no range of its own.<!-- /feature -->

## How the default is filled

An operator that takes a `metric` and reads the vector BY NAME (`vector`, not inline `fields`) fills the kind's default when the request names none. It is a smart default: predict reports it in `defaults_applied` (`category` `metric`, `path` `["Matrices", i, "Params", "metric"]`), the echoed request carries it, and disabling defaults (`--no-defaults`) turns it off. An explicit `metric` always wins.

When the operator does not accept the kind's default it fills the first metric it accepts that suits the kind (never a poor fit); the `reason` says so.<!-- feature: MAT_DISTANCE --> `MAT_DISTANCE` compares centroids, so `measure` fills `euclidean` (add `standardize` for the `std_euclidean` effect), `binary` fills `cosine`, and `composition` under `standardize` fills `manhattan` (z-scored centroids are signed). With no kind and no metric it keeps `euclidean`.<!-- /feature -->

An operator that needs a metric and gets neither a `metric` nor a kind is refused with `PULSE_VECTOR_METRIC_REQUIRED`; `valid` lists what it accepts.

## The unsuited-metric warning

An EXPLICIT metric that fits the kind poorly raises `PULSE_VECTOR_METRIC_UNSUITED`:

- `cosine` on `scale` or `measure`;
- the Euclidean family (`euclidean`, `sq_euclidean`, `std_euclidean`) on `composition`.

Cosine on `binary` is fine. Details carry `vector`, `kind`, `metric`, `slot` and `suggested` (the kind's default). A defaulted metric never raises it.

It is a warning in predict's `warnings` and in the response's `warnings`. Under strict mode (`Options.Strict`; `pulse api predict --strict`, `pulse api process --strict`) it is an error: predict reports the request invalid and the run is refused before a record is read.

Expressions are checked too, statically: `cosine(sat, other)` or `dist(shares, other)` where the argument IS the kind-declared vector warns with `function` in its details. A transformed argument is not the vector and is not judged — `cosine(vcenter(a), vcenter(b))` is the correlation a scale battery suits.

## Reference profiles

An embedder registers target profiles in `Options.Extensions.ReferenceVectors` (manifest `extensions.reference_vectors`: each table's `labels` and `keys`). `ref("table", "key")` reads one as a vector: `cosine(vcenter(sat), vcenter(ref("ideal", "premium")))`. Inside `dot` / `dist` / `cosine` (directly or under `vcenter` / `vzscore` / `vnormalize`) it is matched BY LABEL to the other side — a named vector or another `ref` — never by position; elsewhere it keeps the table's label order. Different label sets, or a literal-array partner, are `PULSE_VECTOR_REFERENCE_MISMATCH`; an unknown table or key, or a non-literal table, is `PULSE_VECTOR_REFERENCE_UNKNOWN`. Predict and the build check literals; a computed key is checked per row.
Row scores read the same tables: <!-- feature: ATTR_PROFILE_SIMILARITY -->`ATTR_PROFILE_SIMILARITY` <!-- /feature --><!-- feature: ATTR_VEC_DISTANCE -->`ATTR_VEC_DISTANCE` <!-- /feature -->with `reference: {table, key_field}`; a key with no profile is null, counted, warned.

## Choosing anyway

- Profiles of ratings: correlate (`correlation`) or center first; level differences are then ignored by design.
- Shares with zeros: `aitchison` is undefined at a zero part (the row or cell is null and counted); `hellinger` or `manhattan` tolerate zeros.
- Mixed units in a `measure` battery: standardize (`std_euclidean`), or one member dominates every distance.

## See

- `multivariate-design` — which operator answers the question.
- `matrix-results` — declaring the vector.
- `expression-language` — the metric functions.
