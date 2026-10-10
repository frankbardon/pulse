# Payload JSON Schema

Pulse publishes a single machine-readable **JSON Schema** (draft 2020-12)
describing every public payload — the request envelopes and the universal
`--json` output envelope. Use it to validate requests before sending them,
to generate client types, or to drive editor autocompletion.

## Where to get it

The schema is reachable three ways, all backed by the same generator
(`BuildPayloadSchema` in `internal/descriptor`):

| Surface | How |
|---|---|
| **Docs URL** | <https://frankbardon.github.io/pulse/payload-schema.json> — the file's own `$id`. |
| **CLI** | `pulse schema` prints it to stdout (offline; no cohort needed). |
| **MCP resource** | Read `pulse://schema` (MIME `application/json`) alongside `pulse_manifest`. |
| **Go library** | `p.PayloadSchema()` returns the schema of instance `p` as raw JSON. |

The CLI and MCP surfaces emit byte-identical output to the published file.

## Per-instance schema

The root `$comment` carries the describing instance's feature-set digest,
`"feature_set_digest: fs1:…"` — the same value as the manifest's
`feature_set_digest` and `p.FeatureSetDigest()`, so both self-descriptions
can be cached under one key. `$id` never changes.

`p.PayloadSchema()` describes only what instance `p` offers. Without a
feature profile it is the published file byte for byte. An instance built
with a feature profile gets a narrower document:

- the operator, overlay-kind and regression enums list only the enabled
  names;
- a request slot the instance does not offer (`crosstab`, `joins`,
  `overlays`, `vectors`, `matrices`, `weight`) is not a property of its
  request root (a hidden `vectors` takes `VectorSpec` and `VectorCoerce`
  with it; hidden `matrices` takes `MatrixSpec`, the `Response.matrices`
  and `ResponseComponents.matrices` slots and `MatrixResult` /
  `MatrixValues` / `MatrixComponents` with it); without
  `capability:weighting` no per-slot `weight` is a property either (so
  `SlotWeight` and `WeightSpec` are absent), and without
  `capability:multiplicity` no `multiplicity` block is a property of any
  root or slot (so `Multiplicity`, `MultiplicityMethod` and
  `MultiplicityFamily` are absent);
- a root whose capability is not offered is absent — `ComposedRequest` /
  `ComposedResponse` (compose), `ChainRequest` / `ChainResponse`
  (process-chain), `FacetRequest` / `FacetResult` (facet),
  `SampleRequest` (sample), `LookupRequest` / `LookupResult` (lookup),
  `RecommendRequest` / `RecommendResult` (recommend),
  `ExplainRequest` / `ExplainResult` (explain); on an instance that
  offers explain, `ExplainRequest` drops the slots of every hidden root
  (`composed` / `composed_response`, `chain` / `chain_response`,
  `facet` / `facet_result`, `sample`);
- every def reachable only through an omitted part is dropped, so the
  document stays a valid draft 2020-12 schema with no dangling `$ref`.

`Request`, `Response` and `Envelope` are always present. `pulse schema`
serves the CLI's default instance; the `pulse://schema` MCP resource
serves the mounted instance's schema (`p.PayloadSchema()`).

## Structure

The document is a `$defs` bundle. The root `oneOf` lists the entry points:

- **Requests** — `#/$defs/Request` (process / predict), `ComposedRequest`
  (compose), `ChainRequest` (process-chain), `FacetRequest`, `SampleRequest`,
  `LookupRequest` (point lookup), `RecommendRequest` (recommend),
  `ExplainRequest` (explain: one request root, or one result root with
  its request companion, plus `detail`).
- **Results** — `#/$defs/Response`, `ComposedResponse`, `ChainResponse`,
  `FacetResult`, `LookupResult`, `RecommendResult` (its draft `request`
  is an open object: a `"<placeholder>"` may sit where `Request` takes a
  number), `ExplainResult` (its `findings[].numbers` values are
  `["number", "null"]`, `verdict` the closed `Verdict` enum).
- **Advisory** — `#/$defs/Advisory`, one entry of a predict result's
  `advisories` (`{code, message, details}`): a coded, non-blocking note
  that the analysis may not fit the data. Never a warning.
- **Envelope** — `#/$defs/Envelope`, the universal `--json` wrapper. Its
  `data` slot is intentionally open: it carries whatever the operation
  returned (a `Response`, the manifest, a predict result, an inspect
  result, …). To validate a wrapped result strictly, validate the
  unwrapped `data` value against its own def (e.g. `#/$defs/Response`).

Example — validate a request body with any draft-2020-12 validator:

```bash
pulse schema > payload-schema.json
# then point your validator at  payload-schema.json#/$defs/Request
```

## How it stays in sync

The schema is generated, never hand-maintained, so it cannot silently
drift from the engine:

1. **Reflection** over the Go payload structs — a new or renamed field
   changes the output.
2. **Registry-injected enums** — the operator, overlay-kind, and
   regression discriminants draw their value lists from the same
   `types.All*Types()` / `AllOverlayKinds()` / `AllRegressionTypes()`
   registries the engine executes against, so registering a new operator
   changes the schema.
3. **Hand-tuned strict unions** for the two shapes reflection cannot
   express: `OverlayRef` (at most one arm populated) and `OverlayPayload`
   (shape-discriminated `scalar` / `series` / `matrix`).

A golden test (`TestPayloadSchemaGolden`) pins the output and an
enum-parity test (`TestPayloadSchema_EnumsMatchRegistry`) fails CI on
drift; the schema's `format_version` is held equal to the envelope's
(`TestPayloadSchema_VersionMatchesEnvelope`). Regenerate after an
intentional payload change with:

```bash
go test ./descriptor/ -run TestPayloadSchemaGolden -update
```

## v1 boundaries

The schema is faithful but not maximally strict in two places, by design:

- **Operator `params`** (the `json.RawMessage` slot on aggregations,
  groupers, overlays, etc.) is an open object. There is no central
  declarative source for per-operator input parameters — each operator's
  param schema lives alongside its processor — so encoding it here would
  duplicate that surface and rot. Consult the operator's skill / manifest
  entry for its accepted params.
- **Small closed mode enums** (`OverlayScope`, `OverlayShape`,
  `CrosstabNormalize`, `CrosstabShape`, `LabelMode`, `MarginAxis`) are
  typed as plain strings rather than `enum`s — they have no registry
  helper, and a hardcoded list would drift silently.

One shape reads oddly against the rest and is deliberate rather than an
oversight: `MarginAggregationFigure.present` (on the crosstab's auxiliary
margin figures — `row_margin_aggregations` / `column_margin_aggregations`
/ `grand_total_aggregations`) is `required`, so a `false` is on the wire,
while its sibling `value` is optional and open. A margin slot that
admitted no record has no defined aggregate; emitting `0` there would be
indistinguishable from a genuine zero, so the figure carries no `value`
at all and `present` is what says so. Dropping `present` when false would
put that statement back into an absence — exactly what it exists to
avoid. Every other field of every response sub-shape stays `omitempty`.

Cross-slot validation rules that depend on more than one field (e.g. a
crosstab requires at least one row **and** one column, mutually exclusive
with top-level `groups`; every `crosstab.margin_aggregations` entry needs
a type and a label distinct from every other entry's and from the cell's)
are enforced by `pulse predict` at request time, not by the schema.

## Time-zone slots

Two additive, optional string slots name a time zone (`format_version`
stays `"1.1"`; omitting them is byte-identical to the earlier wire form):

- **`time_zone`** on `Request` and `FacetRequest` — the request-level
  zone. `ComposedRequest` and `ChainRequest` carry none of their own
  (each inner `Request` does); `SampleRequest` has none.
- **`tz`** on every `Group` (so also `crosstab.rows[]` /
  `crosstab.columns[]`), `Filterer`, `Attribute` and `Feature` entry —
  the per-slot override. It is a slot key beside `type` / `field`,
  never a key inside `params`.

Both are typed as plain strings. The schema does not encode the zone
name set or which operators accept `tz`; three rules are enforced by
`pulse predict` and at run time instead:

- a name must be exactly `UTC` or an IANA `Area/Location` zone
  (`Europe/Berlin`, `Etc/GMT-5`) — anything else is
  `PULSE_TIMEZONE_UNKNOWN`;
- `tz` is accepted only on operators whose manifest entry carries
  `"zone": "capable"`, and never on a `date` field — otherwise
  `PROCESSING_CONFIG`;
- precedence is slot `tz` → `time_zone` → the engine's
  `DefaultTimeZone` → `UTC`; a resolved zone on a `datetime` field
  reads each instant on its local calendar day, and a non-UTC zone
  reaching a derived field (absent from the schema) is refused with
  `PROCESSING_CONFIG`.

The resolved zone per slot is echoed by predict as
`data.time_zones[]` — `{slot, operator, field_type, tz, source}` —
which is a predict result field, not part of this schema. See
`skills/request-envelope.md` (Time zones).

## Multiplicity slots

Multiple-comparison correction adds one additive block shape,
`#/$defs/Multiplicity` `{method, family, alpha}` (every key optional;
`format_version` stays `"1.1"`; a request that names no block is
byte-identical to the earlier wire form and hashes identically). It rides
as `multiplicity` on `Request`, `Test` (`tests[]` and `post_tests[]`),
`OverlaySpec` (request and facet overlays), `ComposeOverlaySpec` and
`ComposedRequest`. `method` is the closed enum `MultiplicityMethod`
(`none`, `bonferroni`, `holm`, `bh`, `by`) and `family` the closed enum
`MultiplicityFamily` (`layer`, `row`, `column`, `request`, `compose`);
both are full vocabularies on every instance (not features). `alpha` is a
number in (0, 1).

Each key falls through on its own: slot → request (a Compose slot: then
the `ComposedRequest`) → the engine's `DefaultMultiplicity` → none, and an
omitted family takes the surface default (`request` for tests, `layer`
for overlays). `{"method": "none"}` is the explicit opt-out. What the
schema cannot say is enforced identically by `pulse predict` and the
runtime (`PULSE_MULTIPLICITY_INVALID`): the families each surface offers
(`row` / `column` only on a MATRIX-payload overlay kind, `compose` only
inside Compose, never `request` on a Compose overlay or anything but
`layer` / `row` / `column` on a facet overlay), no `alpha` on a test's
own block, and no explicit correction on the Tukey HSD post-test. Members
of one `request` or `compose` family that resolve to different methods
are refused `PULSE_MULTIPLICITY_CONFLICT`.

When a correction runs, each corrected `TestResult` (in `tests[]` and
`post_tests[]`) gains three additive `omitempty` keys BESIDE its raw
`p_value` / `reject_null`, which never change: `p_adjusted` (number, or
`null` when the raw p is undefined — such a p is left out of the family
size), `significant_adjusted` (`p_adjusted` < the test's own `alpha`;
absent when `p_adjusted` is null) and `multiplicity`
(`#/$defs/AppliedMultiplicity` `{method, family, alpha, m}`, `m` the
number of defined p-values corrected together). Each test contributes
one headline p; the Tukey HSD post-test never joins a family. No block,
or a method resolving to `none`, emits none of them — the response is
byte-identical. On an instance that hides `capability:multiplicity` the
three keys and `AppliedMultiplicity` are absent from the schema too.

An inferential overlay layer is corrected the same way, its adjusted
figures riding the layer's own additive slots — the base payload (the
`matrix` cells, `scalar`, each summary's `p_value` / `statistic`) never
changes. Where they land follows where the kind carries its p-values:
an `OverlaySummary` (the layer's, for the χ² / KS kinds whose p is
`summary.p_value`; each SERIES entry's, for `OVERLAY_CHISQ_ROW` /
`_COL`, and for `OVERLAY_T_VS_REF` / `OVERLAY_Z_VS_REF` whose p is the
entry's `statistic`) gains `p_adjusted` and `significant_adjusted`; a
MATRIX payload whose cells are p-values (the pairwise, Fisher and
`*_CELL` kinds) gains two parallel `MatrixPayload`s, `payload.p_adjusted`
and `payload.significant_adjusted`, on identical headers, keys and
`[row][column]` coordinates (a panel kind's cell vector maps element for
element: `[]number|null` and `[]boolean|null`); a base cell that is
absent stays absent, an undefined adjusted p is `null` and its
significance cell absent. The layer gains a `multiplicity` echo
(`AppliedMultiplicity`), present only when a correction ran.
`significant_adjusted` compares against the resolved
`multiplicity.alpha` (default `0.05`). A `layer` family never mixes
layers; a `request`-family layer pools with the request's tests and
post-tests, so its echo's `m` is the pooled count. A `row` (`column`)
family is one family per row (column) index of the layer's own MATRIX
payload — purely coordinate-based, never crossing rows (columns) or
layers — and every element of a panel cell joins its cell's row
(column); its echo adds `m_per` (array of integers, `omitempty`), each
family's size index-aligned with the matrix rows (columns), `0` for an
index with no defined p-value, and `m` is their sum. A facet overlay
(`FacetResult.overlays[]`) is corrected per layer with the same slots
and the engine's `DefaultMultiplicity` applying. These keys ride
`capability:multiplicity` like the test outputs.

## Weight slots

Row weighting adds two additive slot shapes (`format_version` stays
`"1.1"`; a request that names no weight is byte-identical to the earlier
wire form and hashes identically):

- **`weight`** on `Request` — a `WeightSpec` object `{field, kind}`,
  `kind` ∈ `probability` (default when omitted) | `frequency`.
  `ComposedRequest` and `ChainRequest` carry none of their own (each
  inner `Request` does); `FacetRequest` and `SampleRequest` have none.
- **`weight`** on every `Aggregation` (so also `crosstab.cell` and each
  `crosstab.margin_aggregations[]` entry), `Test` (`tests[]` and
  `post_tests[]`), `RegressionSpec`, `Attribute`, `OverlaySpec` and
  `Group` (`groups[]` and both crosstab axes) — the per-slot
  `SlotWeight` union: a field-name string, a `WeightSpec` object, or
  `null`. Inferential slots (tests, regressions, reference-distribution
  attributes, the quantile grouper, inferential overlays) are refused
  `PULSE_WEIGHT_UNSUPPORTED` while a weight is in force, so on them
  `null` is the way to run unweighted.

**`null` is not absence.** An absent per-slot `weight` inherits the
request's `weight`, then the engine's `DefaultWeight`; an explicit
`null` opts that one slot out, so it runs unweighted while the rest of
the request stays weighted. The schema says so with
`oneOf [string, WeightSpec, null]` on `#/$defs/SlotWeight`.

The field set a weight may name is not in the schema; `pulse predict`
and the runtime enforce it identically: the field must be a cohort
column (a joined, prefixed name included — never a derived column) of
an unsigned-integer or float type, otherwise `SERVICE_VALIDATION`
(unknown field) or `PROCESSING_CONFIG` (`{slot, field, type}`). The
resolved weight per slot is echoed by predict as `data.weights[]` —
`{slot, operator, field, kind, status, source}`, omitted when nothing
names a weight — a predict result field, not part of this schema.

A weighted aggregation slot reports three optional floor fields on its
`Response.components.aggregations[i]` entry — `sum_weights`, `n_eff`
(probability weights only) and `n_weight_invalid` — each `omitempty`
and present only when a weight was applied to that slot, so an
unweighted response is unchanged. `n` and `n_null` keep their
value-presence meaning.

Which operators honour, skip or refuse a weight, the invalid-weight
rules and the unweighted-base recipe: [Row Weighting](../library/weighting.md).

## Vector slot

`vectors` on `Request` is an additive array of `VectorSpec`
`{name, fields | pattern, labels?, coerce?}` (`format_version` stays
`"1.1"`; a request with no vectors is byte-identical and hashes
identically). `coerce` is the closed `VectorCoerce` enum (`binary`).
What the schema cannot say, predict and the runtime enforce
identically before any record is read: exactly one of `fields` /
`pattern`; literal `fields` entries keep the caller's order while glob
entries and `pattern` (a Go regular expression) expand in schema order;
members must be integer or float columns (`packed_bool` only with
`coerce: "binary"`); `labels` count equals the member count; names and
members are unique — refusals carry the `PULSE_VECTOR_*` codes.
Predict echoes the resolved members as `data.resolved_vectors`
(`{name: [members]}`, a predict result field, not part of this schema).
`Request.Hash()` hashes a vector as written (see
[Request hashing](../library/request-hashing.md)).

## Matrix slots

`matrices` on `Request` is an additive array of `MatrixSpec`
`{name, type, vector | fields, params, weight, encoding}` and
`matrices` on `Response` the matching array of `MatrixResult`
`{name, type, group_key?, group_header?, primary, auxiliary, vectors,
scalars, warnings}`, one per spec in request order (`format_version`
stays `"1.1"`; a matrix-free request and response are byte-identical).
`type` is the registry-backed `MatrixType` enum (`MAT_COVARIANCE`,
`MAT_CORRELATION`, `MAT_PARTIAL_CORRELATION`);
`encoding` is `full` (default) or `upper`. Every matrix is a dedicated
`MatrixValues` `{kind, encoding, row_keys, column_keys, labels?,
values}` — not the crosstab `MatrixPayload` — whose `values` rows hold
`p` cells (`full`) or `p − r` cells from the diagonal (`upper`).
Undefined cells and scalars (no mass, too few rows, a determinant of a
matrix that is not positive definite) are `null`, keys kept. What the
schema cannot say, predict and the runtime enforce identically before
any record is read: exactly one of `vector` / `fields`; a `vector` a
`vectors` entry defines (else `PULSE_VECTOR_UNKNOWN`); inline `fields`
follow the vector rules; result names are unique; `params` are the
operator's own (`MAT_COVARIANCE`: `ddof` 0 or 1; both: `missing`
`listwise` (default) or `pairwise`, `max_drop_share` in [0, 1],
listwise only; `MAT_CORRELATION` only: `method` `pearson` (default),
`spearman` or `kendall` — a rank method takes frequency weights only —
and `summary` `{top_pairs: k}`, k a positive integer;
`MAT_PARTIAL_CORRELATION` only: `control` `"all"` (default) or a list of
numeric fields — a listed member leaves the output axis, so `row_keys`
are the non-control members — and `repair` `"nearest"`). Under `pairwise`, `auxiliary.n` is a `MatrixValues` of
the same shape and encoding holding each pair's row count. `vectors` is an
open object: with `summary.top_pairs`, `vectors.top_pairs` is
`[{row, col, r, n}]` — the k off-diagonal pairs with the largest `|r|`,
ties in axis order, undefined pairs skipped, `n` the pair's row count.
`warnings` are `{code, message, details}` entries
(`PULSE_MATRIX_INSUFFICIENT_N`, `_ZERO_VARIANCE`,
`_LISTWISE_HEAVY_DROP`, `_NOT_PSD` — fatal, not a warning, on
`MAT_PARTIAL_CORRELATION` without `repair: "nearest"`). `components.matrices` carries one
`MatrixComponents` per result, in the same order: `{name, type,
group_key?, n, n_null, n_listwise_dropped, min_pair_n?, max_pair_n?,
sum_weights?, n_eff?, n_weight_invalid?, operator?}` (pairwise and
weighted keys only when they apply). Contract:
`.claude/reference/matrix-and-vectors.md`.

## Per-group aggregation components

`groups` on `AggregationComponents` is an additive `omitempty` array of
`AggregationGroupComponents` `{group_key, n, n_null, sum_weights?,
n_eff?, n_weight_invalid?, operator?}` (`format_version` stays `"1.1"`;
an ungrouped or crosstab response has no `groups` key and is
byte-identical). On a grouped, non-crosstab run each
`components.aggregations[i]` carries the cohort-wide floor (no
`operator`) and one `groups` entry per `data` row, in `data` order
(`sort` included), each equal to an ungrouped run over that bucket's
records. What the schema cannot say: entry `i` describes `data[i]`; every
execution arm (buffered, streaming, parallel) emits the same figures, and
a stream carries them on its terminal chunk only. Contract:
`.claude/reference/response-components.md` (Per-group aggregation
components).

## Return slot

`return` on `Request` is an additive `Return` object
`{preset?, include?, exclude?, precision?}` (`format_version` stays
`"1.1"`; a request without one is byte-identical and hashes
identically). `preset` is the closed `ReturnPreset` enum (`full`,
`standard`, `minimal`); `precision` is significant digits, 1–17 (0 or
absent: unlimited). `include` / `exclude` are paths over this schema's
`Response` JSON names at any depth — `.` between keys, `[*]` into every
array element, a trailing `*` on a map-key segment as a prefix glob
(`tests[*].details.effect_*`). What the schema cannot say, predict and
the runtime enforce identically before any record is read: resolution
runs preset, then include adds, then exclude removes (exclude wins);
include without a preset starts from an empty base; top-level
`warnings`, and every nested `warnings` whose parent is emitted, stay
unless excluded. A path this instance's `Response` does not carry — a
hidden feature's included — is `PULSE_RETURN_PATH_UNKNOWN`, as is a
`data[*].<column>` the request cannot produce (data under a join or a
crosstab, test `details` and operator component maps accept any key); an
unknown preset, a precision out of range or a malformed path is
`PULSE_RETURN_INVALID`. Predict echoes the resolved plan as
`data.return` (`{preset, include, exclude, keep, precision?, identity,
digest, unresolved_includes?}`, a predict result field, not part of
this schema); equivalent spellings share a `digest`, and
`unresolved_includes` lists the open-path includes predict cannot
resolve without data. On every request, `return` block or not, predict
also reports `data.sizes`: each top-level section as `{section,
full_bytes, shaped_bytes, basis}` (`shaped_bytes` equals `full_bytes`
with no `return`, 0 when excluded; a section whose size depends on the
data is omitted) — guidance only.

At runtime the plan is applied to the finished `Response`: an excluded
slot is **absent** on the wire — never `null`, required keys such as
`metadata.total_rows` or `tests[*].p_value` included — and zero (nil
when nillable) on the Go value. A shaped response carries `returned`
(`ReturnedMarker {preset, digest, precision?}` — `preset` is `custom`
for an explicit include / exclude without one; `digest` equals
predict's); it is emitted only when the plan changes something and is
not itself a selectable path (naming it is `PULSE_RETURN_INVALID`). An
include through a map key or an open value that matched nothing in the
executed response adds a `PULSE_RETURN_PATH_UNMATCHED` entry to
`warnings` (details `path`) unless `warnings` is excluded.

`precision` is **wire-only**: the Go response keeps full float64, and on
the wire every float (data cells, tests, regressions, overlays, matrices
incl. `top_pairs[*].r` and `scalars.determinant`, component maps) is
`strconv.FormatFloat(v, 'g', precision, 64)` — so `3e-9` at 4 digits is
`3e-09` and `123456.789` is `1.235e+05`. NaN / ±Inf stay `null`; ints
are never touched. Exempt (written exact): integer-semantics floats —
`matrices[*].auxiliary.n` on every request, and, derived from the
request, the `data` column of a count aggregation (`AGG_COUNT`,
`AGG_DISTINCT_COUNT`, `AGG_NULL_COUNT`, `AGG_MODE_COUNT`,
`AGG_FREQUENCY`, `AGG_SET_FREQUENCY`, `AGG_SET_CARDINALITY_SUM`,
`AGG_SET_DISTINCT_VALUES`), a count crosstab cell under `normalize`
none (cells, margins, grand total, long-form column) and a count
auxiliary margin aggregation's figures — plus `decimal128` values, which
are decimal strings. The `upper` matrix encoding keeps its shape.

A shaped response is a **projection**: it is not schema-valid against
the full payload schema (required keys may be absent), so validate the
request and the plan, not the shaped output. A request without `return`
stays byte-identical. Agent-facing summary: `skills/response-shaping.md`;
library walkthrough: `library/response-shaping.md`.

### Presets

The presets are defined once (`internal/descriptor/return_resolve.go`,
`returnPresetPaths`) and listed in the manifest as `return_presets`
(`[{name, paths}]`, in `full` / `standard` / `minimal` order) with each
preset's paths **expanded against the instance**: a path a hidden
feature owns (e.g. `matrices[*]` without `capability:matrices`,
`tests[*].p_adjusted` without `capability:multiplicity`) is absent from
the listing and from the selection, never an error. A preset is an
include allowlist, so a caller's `include` can always add a part back.
Nested `*.warnings` are not listed: each is kept wherever its parent is
emitted. Every listed path must resolve against this schema
(`TestReturnPathsMatchSchema`).

| Slot | `minimal` (primary result) | `standard` |
|---|---|---|
| `data`, `warnings` | yes | yes |
| `metadata` | no | yes |
| `crosstab` | `shape`, `matrix` | whole |
| `matrices[*]` | `name`, `type`, `group_key`, `primary` | all but `auxiliary` |
| `tests[*]` / `post_tests[*]` | `label`, `type`, `statistic`, `p_value`, `reject_null`, `p_adjusted`, `significant_adjusted` | minimal's keys + `variant`, `df`, `alpha`, `multiplicity`, `details.effect_size` (no other `details` key) |
| `regressions[*]` | `name`, `type`, `coefficients`, `p_values` | all but `credible_intervals` and `selection` |
| `overlays[*]` | `name`, `kind`, `ref`, `summary` (no `payload`) | whole |
| `components` | no | no |

`full` is the identity: every visible top-level key, byte-identical to
a request without `return`. `standard` and `minimal` stamp `returned`
with their preset name.

### Compose and chain

Every Compose slot and every chain stage is a `Request`, so its own
`return` (else the instance default) shapes `responses[i]` /
`stages[i]` exactly as a single Process would; a refusal carries
`details.request` / `details.stage`. `ComposedRequest` adds its own
`return` (same `Return` def) whose paths root at `ComposedResponse`:
only `overlays…` paths are valid (any other is `PULSE_RETURN_INVALID`,
details `root: "compose"`), `responses` is always kept whole, presets
expand to their overlay paths, and no instance default applies at that
level. A non-identity Compose-level plan stamps
`ComposedResponse.returned`. Shaping runs after the Compose overlay and
multiplicity folds — layers are computed from unshaped slots — and after
the whole chain, so a stage that excludes `data` still feeds the next
stage its rows. `final` is the last stage, shaped by its `return`;
there is no chain-level block.

## Undefined figures

A result figure can be undefined even when every input is present — a
ratio over an all-zero denominator, a confidence bound under two rows,
the first entry of an index-vs-prior series, a rolling window that has
not filled. The engine's Go results carry NaN there; JSON has no NaN,
so every JSON surface writes the figure as **`null` in place, key
kept** (`types.MarshalFinite`), and the rest of the response serialises
normally. `null` means "reported, undefined here"; an absent optional
key keeps meaning "not reported for this kind".

The schema says so: every float slot on a result-only def —
`TestResult`, `RegressionResult`, `OverlaySummary`, the
`OverlayPayload.scalar` arm, `FacetNumeric`, `FacetHistogram` and the
weighted floor's `sum_weights` / `n_eff` on `AggregationComponents` and
`AggregationGroupComponents` —
is `"type": ["number", "null"]`. The open slots (`data` rows,
components operator maps, matrix cell `value`) already admit `null`.
Request floats stay `"number"`: a decoded request never carries a
non-finite float. `ExplainRequest` is not a request root for this rule:
the results it carries keep their nullable floats.

Reading a result back is the inverse: plain `encoding/json` decodes a
`null` into a Go float as `0`, which a reader would take for a real
figure. `pulse explain --response` and MCP `pulse_explain` decode the
result they are handed so that `null` in a float slot is NaN again —
undefined stays undefined (a `not_computable` verdict, a `null` number),
never a confident 0.

Widening those slots did not move `format_version` (still `"1.1"`):
every document the schema accepted before it still accepts, and output
that serialised before is byte-identical — the only outputs that
change are ones that previously failed to serialise at all.

## Whether a crosstab fuses is a predict answer

The schema cannot tell you which arm the engine will build a crosstab
on — the fused one-pass arm (memory `O(cells + margins)`) or the buffered
arm (holds every filter-passing record). Output is identical; only
memory differs. `pulse predict` reports it as
`data.crosstab_fusable` (absent when the request has no crosstab) plus
`data.crosstab_fusion_reasons` (every reason it will not fuse, in rule
order). Both are predict result fields, not part of this schema, so
adding them did not move `format_version` (still `"1.1"`). They honour
the instance: an engine built with `Options.DisableCrosstabFusion`
answers `false` with a reason. See `skills/crosstab-guide.md`.

## What the schema cannot say about `margin_aggregations`

The auxiliary margin-only slot is fully described structurally — it is an
array of the same `Aggregation` shape the `cell` slot takes — but two of
its contracts are semantic, so validating a request against the schema
tells you nothing about either. Both are reported by `pulse predict`.

**Whether the figures will be emitted at all.** The auxiliary figures
land only on `components.crosstab.row_margin_aggregations` /
`column_margin_aggregations` / `grand_total_aggregations`, and each rides
the matching DISPLAY flag (`crosstab.margins.rows` / `.columns` /
`.grand`). A schema-valid request with every display flag false produces
no auxiliary figure at all. Setting a `normalize` direction does not
help: it makes its margin required as a normalization *denominator*, and
an auxiliary is never a denominator. That shape warns
`PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED`.

**Which records each figure is built from.** An auxiliary observes the
same record admission as the CELL aggregator — a record contributes only
if it contributed to a cell — so a record whose cell field is null, and a
record whose axis key a grouper `include` excluded, are absent from every
auxiliary figure while the cell's own `row_margin_components` beside them
still count both. Nothing in the schema distinguishes the two, and the
consequence is silent: a consumer reading an auxiliary's `components.n`
as a cohort count gets a well-formed, plausible, wrong number. See
`skills/crosstab-guide.md` and `skills/response-components.md`.
