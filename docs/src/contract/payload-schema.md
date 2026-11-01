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
feature profile or extensions it is the published file byte for byte.
Every extension operator the instance registers (and its profile keeps)
joins its category's operator-type enum beside the built-ins —
`AggregationType`, `AttributeType`, `FiltererType`, `GroupType`,
`WindowType`, `FeatureType`, `TestType`, `MatrixType` and
`RegressionType` — so a request
naming one validates against the instance's own schema. An instance
built with a feature profile gets a narrower document:

- the operator, overlay-kind and regression enums list only the enabled
  names;
- a request slot the instance does not offer (`crosstab`, `joins`,
  `overlays`, `vectors`, `matrices`, `weight`) is not a property of its
  request root (a hidden `vectors` takes `VectorSpec`, `VectorCoerce`,
  `VectorKind` and `VectorStandardize` with it; hidden `matrices` takes `MatrixSpec`, the `Response.matrices`
  and `ResponseComponents.matrices` slots and `MatrixResult` /
  `MatrixComponents` with it — `MatrixValues` stays, shared with the
  regression `vcov` / `correlation`); without
  `capability:weighting` no per-slot `weight` is a property either (so
  `SlotWeight` and `WeightSpec` are absent), and without
  `capability:multiplicity` no `multiplicity` block is a property of any
  root or slot (so `Multiplicity`, `RegressionMultiplicity`,
  `MultiplicityMethod` and `MultiplicityFamily` are absent); without
  `capability:regression_holdout` neither `RegressionSpec.holdout` nor
  `RegressionResult.holdout` is a property (so `RegressionHoldout` is
  absent);
- a root whose capability is not offered is absent — `ComposedRequest` /
  `ComposedResponse` (compose), `ChainRequest` / `ChainResponse`
  (process-chain), `FacetRequest` / `FacetResult` (facet),
  `SampleRequest` (sample), `LookupRequest` / `LookupResult` (lookup),
  `RecommendRequest` / `RecommendResult` (recommend),
  `ExplainRequest` / `ExplainResult` (explain),
  `MaterializePredictResult` (materialize); on an instance that
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
  `FacetResult` (its `warnings` are coded `{code, message, details}`
  entries, the `Response.warnings` shape — `[]string` before U44),
  `LookupResult`, `RecommendResult` (its draft `request`
  is an open object: a `"<placeholder>"` may sit where `Request` takes a
  number; `steps` — the `RecommendStep` plan `{tool, request,
  placeholders, why}` — is deprecated, marked `deprecated: true`, and
  always absent: recommend drafts no multi-request plan), `ExplainResult` (its `findings[].numbers` values are
  `["number", "null"]`, `verdict` the closed `Verdict` enum; `warnings` relays predict's
  coded warnings), `MaterializePredictResult` (`Pulse.PredictMaterialize`
  / `pulse api materialize --predict` / `pulse_materialize {predict:
  true}`: `valid`, the source `schema_info`, the output `columns` —
  `{name, type, nullable, description, from, derived}` plus a truncated
  `dictionary` and a decimal `precision` / `scale` — the
  `derived_columns` names and the data-dependent `risks`
  `{column, path, code, check}`; the three lists are `[]`, never
  `null`). A bootstrap `RegressionResult` echoes
  `bootstrap_requested` / `bootstrap_succeeded`; a delete-a-group
  jackknife (`RegressionSpec.jackknife_groups` / `jackknife_by`, both
  additive `omitempty`) echoes its replicate count as
  `jackknife_groups`, absent on leave-one-out. A `RegressionResult`
  carries the fit's own coded diagnostics in `warnings` (additive
  `omitempty`, `#/$defs/ResponseWarning` entries — today an extension
  regression's, e.g. `PROCESSING_REGRESSION_DIAGNOSTIC`), each also
  lifted onto the response's `warnings`. Each
  `ComposedResponse.responses[i]` echoes its slot's final `label`
  (absent on a standalone `Response`).
- **Advisory** — `#/$defs/Advisory`, one entry of a predict result's
  `advisories` (`{code, message, details}`): a coded, non-blocking note
  that the analysis may not fit the data. Never a warning. The same shape
  rides `RegressionResult.advisories` (additive `omitempty`, in the
  `standard` and `minimal` presets): an `l1` / `elasticnet` fit without
  `resample` carries `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` beside
  its `std_errors`, `p_values`, `ci_lower` and `ci_upper`, which are
  `null` on every key (the data pick the predictors, so no analytical
  inference is valid); predict raises the identical advisory and
  `Options.SuppressAdvisories` drops it from both.
  Predict's `limit_findings` (`{limit, configured, estimated, grade,
  details}` — on Process, Compose, chain and facet predict; `details`
  locates a Compose slot as `request` or a chain stage as `stage`) is a
  predict result field, not part of this schema.
  So is predict's `post_window` (`{windows_first, window_labels,
  attributes: [{index, label, depends_on, window_reads}]}` — on Process
  predict and per Compose slot, additive `omitempty`, absent without a
  window): the post-window plan, which attributes run after the windows,
  by index.
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
   express: `OverlayRef` (at most one arm populated — ten arms, the
   newest `matrix {name}` naming a `Request.Matrices` spec for the
   matrix-result overlay host) and `OverlayPayload`
   (shape-discriminated `scalar` / `series` / `matrix`, plus named
   `parts` that never nest).

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
crosstab requires at least one row grouper, mutually exclusive with
top-level `groups`; every `crosstab.margin_aggregations` entry needs
a type and a label distinct from every other entry's and from the cell's)
are enforced by `pulse predict` at request time, not by the schema.

`crosstab.columns` is optional (not in the schema's `required` list):
absent, `null` or `[]` runs the crosstab with ONE implicit column, key
`["Total"]`, `column_header` `{fields: ["_total"], types: ["TOTAL"]}`,
holding every record whose row key resolved — a Total column with no
constant field behind it. Additive: a request that declares columns
marshals and runs byte-identically.

`crosstab.cell` is optional too: a crosstab names its cell aggregation
either as `cell` or as several measures in `cells` (an array of
`Aggregation`, mutually exclusive with `cell` — predict refuses both, or
neither, since the schema cannot say "exactly one"). Each measure
becomes a stub row under every row key: `row_header` gains the
`_measure` / `MEASURE` position, `matrix.cell_labels[i]` names row i's
measure, and the per-measure column margins and grand total move to
`matrix.measures[]` (`CrosstabMeasureMargins`) and
`components.crosstab.measures[]` (`CrosstabMeasureComponents`). All three
result slots are optional and absent on a single-`cell` crosstab, so a
`cell` request is byte-identical; `format_version` stays `"1.1"`.

## Time-zone slots

Four additive, optional string slots name a time zone (`format_version`
stays `"1.1"`; omitting them is byte-identical to the earlier wire form):

- **`time_zone`** on `Request` and `FacetRequest` — the request-level
  zone. `ComposedRequest` and `ChainRequest` carry none of their own
  (each inner `Request` does); `SampleRequest` has none.
- **`tz`** on every `Group` (so also `crosstab.rows[]` /
  `crosstab.columns[]`), `Filterer`, `Attribute` and `Feature` entry —
  the per-slot override. It is a slot key beside `type` / `field`,
  never a key inside `params`.
- **`time_zone_field`** (request) and **`tz_field`** (slot), on the
  same objects — a per-row zone: the name of a categorical cohort field
  whose values are IANA zone names, so each row is read in its own zone.

All are typed as plain strings. The schema does not encode the zone
name set or which operators accept `tz`; these rules are enforced by
`pulse predict` and at run time instead:

- a name must be exactly `UTC` or an IANA `Area/Location` zone
  (`Europe/Berlin`, `Etc/GMT-5`) — anything else is
  `PULSE_TIMEZONE_UNKNOWN`;
- `tz` and `tz_field` are accepted only on operators whose manifest
  entry carries `"zone": "capable"`, never on a `date` field, and never
  both on one slot — otherwise `PROCESSING_CONFIG`;
- a zone field must be a `categorical_*` cohort field (absent:
  `SERVICE_VALIDATION`; another type: `PROCESSING_CONFIG`) whose every
  dictionary entry is a valid zone name (else `PULSE_TIMEZONE_UNKNOWN`
  with `details.dictionary_index`);
- precedence is slot `tz` → slot `tz_field` → `time_zone_field` →
  `time_zone` → the engine's `DefaultTimeZone` → `UTC`; a resolved zone
  on a `datetime` field reads each instant on its local calendar day,
  and a non-UTC zone (or any zone field) reaching a derived field
  (absent from the schema) is refused with `PROCESSING_CONFIG`.

The resolved zone per slot is echoed by predict as
`data.time_zones[]` — `{slot, operator, field_type, tz, tz_field,
source}` (`source: "field"` with `tz: null` for a per-row zone) —
which is a predict result field, not part of this schema. See
`skills/request-envelope.md` (Time zones).

## Request params

`params` on `Request` is an additive, optional object of named
constants every expression of that request reads, read-only, as
`params.<name>` (or `params["<name>"]`): `ATTR_FORMULA`,
`FILTER_EXPRESSION`, a `total()` argument, a regression `holdout` and
`OVERLAY_FORMULA`. `format_version` stays `"1.1"`; omitting it is
byte-identical to the earlier wire form. Gated by `capability:params`:
an instance whose feature profile omits it has no `params` property and
refuses the key as an unknown field.

The schema types it as an object whose property names match
`^[A-Za-z_][A-Za-z0-9_]*$` and whose values are a number, a string, a
boolean, or an array of numbers, of strings or of booleans. Rules the
schema cannot say, enforced by `pulse predict` and at run time alike,
before any record is read:

- a value is never `null`, and a list holds ONE kind — otherwise
  `PULSE_PARAMS_INVALID` (`details.reason`: `null_value`,
  `invalid_name`, `mixed_list`, `unsupported_value`);
- numbers are kept as their JSON text: an integer binds as an integer
  (`int64`, or `uint64` above it), so a large id compares exactly; any
  other number binds as a float. A list of numbers binds as the one
  integer or float array type every element fits;
- reading a name the object does not declare is
  `PULSE_PARAMS_UNDECLARED`; reading `params.<name>` where `params` is
  also a column (a schema field, feature output, attribute label or
  vector called `params`) is `PULSE_PARAMS_FIELD_COLLISION`. A bare
  `params` always reads that column.

`ComposedRequest` and `ChainRequest` carry a `params` of their own,
same schema and gate: every sub-request inherits it — each Compose
slot (sweep- and banner-expanded ones included), each Compose `fits`
request, each chain stage — merged during normalization with a key the
sub-request declares itself winning. The top-level block is validated
once, before any sub-request (`PULSE_PARAMS_INVALID`, even when every
sub-request overrides the bad name). The normalized request
(`--echo-request`, `pulse api predict-compose` / `predict-chain`)
shows the merged params on every sub-request. A Compose-level
`OVERLAY_FORMULA` reads the batch's top-level params; an undeclared
read there is `PULSE_PARAMS_UNDECLARED`.

`FacetRequest` carries a `params` of its own too, same schema, rules and
gate: its filterers' `FILTER_EXPRESSION`s (base and additive scope
alike) read it as a `Request`'s filterers read theirs, so a facet over
the filter an analysis applies (`wave == params.wave`) takes the same
block. `pulse api facet`, `pulse api predict-facet` and the
`pulse_facet_schema` tool accept it; a facet template can pass a
variable as `"params": {"wave": {"$var": "wave"}}`. Omitting it is
byte-identical to the earlier wire form.

## Materialize output slot

`output` on `Request` (`#/$defs/MaterializeOutput`) lays out the cohort
`Pulse.Materialize` writes: `columns` — a list of
`#/$defs/MaterializeOutputColumn` `{from, as, description, nullable,
type, precision, scale, on_cast_error, recode, bucket}`, `from` required — XOR `exclude` (field / derived-column names) — plus the
container keys `shards` (`#/$defs/MaterializeShards` `{max_records}`: a
shard archive of `max_records`-row shards; `max_records ≤ 0` is
`PULSE_MATERIALIZE_OUTPUT_INVALID`), `elide_constants` (store a
column constant over the written rows once) and `groups`
(`#/$defs/MaterializeGroup` `{key, members}` over written names; absent
inherits the source's parent groups whose members are written
unchanged, `[]` writes none). It is
additive: `format_version` stays `"1.1"` and omitting it writes the
cohort Materialize always wrote, byte for byte. It is MATERIALIZE-ONLY:
every other operation that takes a `Request` (process, compose, chain,
predict, their streaming, CLI and MCP surfaces) refuses it with
`PULSE_MATERIALIZE_OUTPUT_ONLY` (`details.slot: "output"`). Gated by
`capability:materialize_output`: an instance whose feature profile
omits it has no `output` property and refuses the key as an unknown
field everywhere. Rules the schema cannot say, enforced before any
record is read: `columns` and `exclude` together, an empty `columns`
list, an `exclude` dropping every column or an entry without `from` is
`PULSE_MATERIALIZE_OUTPUT_INVALID`; a name that is neither a source
field nor a derived column is `PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN`;
two columns under one name after `as` is
`PULSE_MATERIALIZE_OUTPUT_DUPLICATE`; a `type` its column cannot cast
to is `PULSE_MATERIALIZE_CAST_UNSUPPORTED`, an unknown `type`, a bad
`precision` / `scale` or a misused `on_cast_error` (`"fail"` /
`"null"`) `PULSE_MATERIALIZE_OUTPUT_INVALID`. `recode`
(`#/$defs/MaterializeRecode` `{map, order}`; a `map` value is a label or
`null`) rewrites a categorical's dictionary before `type` reads it: a
`map` key its source dictionary lacks is
`PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL` (`details.label`); a recode of a
non-categorical, a `null` on a non-nullable column, or an `order` that
is not exactly the recoded labels once each is
`PULSE_MATERIALIZE_OUTPUT_INVALID`. `bucket`
(`#/$defs/MaterializeBucket` `{ranges, range_table}`, each range a
`#/$defs/MaterializeBucketRange` `{label, start, end}` whose bounds are a
number, an ISO date string or `null`) writes a numeric or date-family
column as the `categorical_*` of its range labels; its refusals are the
shared range codes (`PULSE_RANGE_SOURCE_AMBIGUOUS`,
`PULSE_RANGE_TABLE_UNKNOWN`, `PULSE_RANGE_TABLE_KIND_MISMATCH`,
`PULSE_RANGE_EMPTY` / `_INVALID` / `_DUPLICATE_LABEL` / `_OVERLAP`) plus
`PULSE_MATERIALIZE_OUTPUT_INVALID` for an unbucketable column, and a
value outside every range fails like a cast. A null in a `nullable:
false` column fails the run with `PULSE_MATERIALIZE_OUTPUT_NULL`, a
stored value its cast cannot represent with
`PULSE_MATERIALIZE_CAST_FAILED` (or, `on_cast_error: "null"`, a null and
a `PULSE_MATERIALIZE_CAST_NULLED` warning). See
[Materializing Derived Columns](../library/materialize.md).

## Multiplicity slots

Multiple-comparison correction adds one additive block shape,
`#/$defs/Multiplicity` `{method, family, alpha}` (every key optional;
`format_version` stays `"1.1"`; a request that names no block is
byte-identical to the earlier wire form and hashes identically). It rides
as `multiplicity` on `Request`, `Test` (`tests[]` and `post_tests[]`),
`OverlaySpec` (request and facet overlays), `MatrixSpec` (`matrices[]`),
`ComposeOverlaySpec` and `ComposedRequest`; a `RegressionSpec`
(`regressions[]`) carries `#/$defs/RegressionMultiplicity`, the same
three keys plus `coefficients` (an array of coefficient names). A
member regression's `RegressionResult` gains the additive `p_adjusted`
(number map, `null` for an undefined p), `significant_adjusted` (boolean
map) and `multiplicity` (`AppliedMultiplicity`) beside `p_values`. `method` is the closed enum
`MultiplicityMethod` (`none`, `bonferroni`, `holm`, `bh`, `by`) and
`family` the closed enum `MultiplicityFamily` (`layer`, `row`, `column`,
`request`, `compose`, `matrix`, and the echo-only `margins` — the family
a regression's marginal effects form on their own, which a block naming
it is refused);
both are full vocabularies on every instance (not features). `alpha` is a
number in (0, 1).

Each key falls through on its own: slot → request (a Compose slot: then
the `ComposedRequest`) → the engine's `DefaultMultiplicity` → none, and an
omitted family takes the surface default (`request` for tests and
regressions, `layer` for overlays, `matrix` for a matrix spec). A
regression joins only through its own block — an inherited one never
reaches it — and `REG_BAYES_LINEAR` refuses any method but `none`. `{"method": "none"}` is the explicit opt-out. What the
schema cannot say is enforced identically by `pulse predict` and the
runtime (`PULSE_MULTIPLICITY_INVALID`): the families each surface offers
(`row` / `column` only on a MATRIX-payload overlay kind, `compose` only
inside Compose, never `request` on a Compose overlay or anything but
`layer` / `row` / `column` on a facet overlay, `matrix` only — with
`request` — on a matrix spec), no `alpha` on a test's own block, no
explicit correction on the Tukey HSD post-test, and no block on a matrix
operator without p-values (only `MAT_CORRELATION` /
`MAT_PARTIAL_CORRELATION` take one; it implies `params.inference`, and an
explicit `params.inference: false` beside it is refused). Members
of one `request` or `compose` family that resolve to different methods
are refused `PULSE_MULTIPLICITY_CONFLICT`.

On a grouped request (`groups` set) every `tests[]` entry runs once per
composite group key, and `tests[]` in the response is flat: one
`TestResult` per (test x group), test-major, the groups in the final
`data` row order. Each carries the additive `omitempty` `group_key` (an
array — the bucket's per-grouper key tuple, one element for one grouper;
the `MatrixResult.group_key` shape), so entry k belongs to request
`tests[k / groups]`. A group whose data the test refuses has `null`
`statistic` and `p_value` (`reject_null` false) and a response warning
under the refusal's code naming its `group_key`. An ungrouped response
carries no `group_key` and is byte-identical; `format_version` stays
`"1.1"`.

With two or more groupers every `post_tests[]` entry likewise runs once
per partition of the result rows — the rows sharing the leading group
key columns, every key but the last by default, or the leading prefix
its `params.partition_by` names (`[]` = one run over every row) — and
`post_tests[]` is flat: post-tests x partitions, test-major, partitions
in first-appearance `data` order, each result's `group_key` the
partition key (one element per partition column). Every post-test of a
request partitions alike (`PULSE_TEST_PARTITION_INVALID` otherwise), so
entry k belongs to request `post_tests[k / partitions]`. One grouper,
`partition_by: []` and a crosstab host never partition: one result per
post-test, no `group_key`, byte-identical.

When a correction runs, each corrected `TestResult` (in `tests[]` and
`post_tests[]`) gains three additive `omitempty` keys BESIDE its raw
`p_value` / `reject_null`, which never change: `p_adjusted` (number, or
`null` when the raw p is undefined — such a p is left out of the family
size), `significant_adjusted` (`p_adjusted` < the test's own `alpha`;
absent when `p_adjusted` is null) and `multiplicity`
(`#/$defs/AppliedMultiplicity` `{method, family, alpha, m, excluded}`,
`m` the number of defined p-values corrected together, `excluded`
(integer, `omitempty`) the family's members left out of `m` because their
p is undefined — a sparse group or partition; absent when 0, and summed
over the families of a `row` / `column` echo like `m`). Every
(test x group) and (post-test x partition) result joins its slot's
family, so a `request` family pools them all. Each test contributes
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
entry's `statistic`; each tested period's, for the `*_VS_PRIOR` tests
whose p is the entry's `p_value` — the first period has none and is no
family member) gains `p_adjusted` and `significant_adjusted`; a
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

A matrix spec under `params.inference` (set, or implied by its own
block) is corrected over its DISTINCT pairs: the off-diagonal upper
triangle of each `square_symmetric` result (m = k(k − 1)/2 for k
members — the diagonal and the mirrored lower triangle are not separate
hypotheses), every cell of a rectangular one. The result's open
`auxiliary` map gains `p_adjusted` and `significant_adjusted`, two
`MatrixValues` of `auxiliary.p`'s kind, encoding and keys (mirrored into
both triangles under `full`, diagonal `null`; `significant_adjusted` is
`1` when `p_adjusted` < the block's `alpha`, else `0`, `null` beside a
null `p_adjusted`), and the `MatrixResult` gains a `multiplicity` echo
(`AppliedMultiplicity`). `auxiliary.p` never changes. Family `matrix`
(the default) is one family per result — per spec and per group
bucket; `request` pools every bucket of the spec with the request's
other `request`-family members. An inherited block reaches only a spec
already under `params.inference`. The echo key rides
`capability:multiplicity`.

## Overlay payload parts

`OverlayPayload.parts` (additive, `omitempty`) carries a multi-output
overlay kind's named secondary payloads BESIDE its primary: one spec
still produces one layer, and the primary (`shape` + its arm) stays where
every renderer already reads it.

```json
{"shape": "matrix", "matrix": {"…": "residuals"},
 "parts": {"expected": {"shape": "matrix", "matrix": {"…": "…"}},
           "p":        {"shape": "matrix", "matrix": {"…": "…"}}}}
```

- Each part is a plain `scalar` | `series` | `matrix` payload under the
  same shape rule as the primary. Parts never nest: the schema's per-part
  definition has no `parts` key, and the engine refuses a handler that
  builds one (`PROCESSING_INTERNAL`).
- Part names are fixed per kind and listed in the kind's skill.
- A single-output kind may still write an OPT-IN part: every
  `OVERLAY_PAIRWISE_*` kind writes `letters` only under
  `params.letters` — a matrix on the host coordinates whose present cell
  `value` is an object `{"letter", "group", "higher", "above"}`
  (`types.PairwiseLetterCell`; `higher` / `above` are letter arrays,
  empty, never `null`), not a number.
- A coordinate-style matrix part whose columns are dimensions rather than
  host keys (points × dimensions) keys them `["dim_1"]`, `["dim_2"]`, …
  in dimension order.
- A layer without parts — every single-output kind — writes no `parts`
  key, so its bytes are unchanged; `format_version` stays `"1.1"`.
- `return` addresses one part as `overlays[*].payload.parts.<part>`. A
  part name is a map key, so the path is Open: an excluded part is
  ABSENT (never `null`), an include naming no existing part raises
  `PULSE_RETURN_PATH_UNMATCHED`, and excluding every part drops the
  `parts` key.
- Export: NDJSON, Arrow and Parquet write each layer as JSON, parts
  included; Excel writes the primary on the layer's sheet and drops the
  parts under one `PULSE_OVERLAY_EXPORT_PARTS_DROPPED` warning; CSV, TSV
  and SPSS drop whole layers under `PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED`.

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
`{name, fields | pattern, labels?, coerce?, kind?, scale_min?, scale_max?, standardize?}`
(`format_version` stays `"1.1"`; a request with no vectors is
byte-identical and hashes identically). `coerce` is the closed
`VectorCoerce` enum (`binary`); `kind` the closed `VectorKind` enum
(`measure`, `scale`, `composition`, `binary`) — what the values mean,
which fills a metric-taking operator's default metric (reported in
predict's `defaults_applied`, category `metric`) and flags an explicit
poorly fitting one (`PULSE_VECTOR_METRIC_UNSUITED`, a warning, an
error under strict). `scale_min` / `scale_max` are numbers, accepted
only beside `kind: "scale"`, both or neither, min below max.
`standardize` is the closed `VectorStandardize` enum (`zscore`): the
members are z-scored with their population SD over the rows a
listwise co-moment `MAT_*` slot keeps (frequency weights respected), so
its covariance is their correlation; every other consumer (a test,
attribute, grouper or aggregation `vector`, an expression, `MAT_DISTANCE`,
`missing: "pairwise"`, a rank method) is refused with
`PULSE_VECTOR_INVALID` (`details.reason` `standardize_unsupported`,
`details.slot`), and predict's `matrices[].standardize` echoes it.
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

## Test vector inputs

`Test` carries four additive slots, the `MatrixSpec` mirror:
`vector` / `fields` (the member set) and `vector2` / `fields2` (a
paired second set). A vector-input test (`TEST_HOTELLING_T2`, `TEST_MANOVA`,
`TEST_BOX_M`, `TEST_BARTLETT_SPHERICITY`, `TEST_MARDIA`) sets
exactly one of `vector` (a `vectors` entry) and `fields` (inline
names and globs, the `VectorSpec.fields` rules) and no `field`; a
second set must resolve to the same member count and is accepted only
by a test that reads one (`TEST_HOTELLING_T2`'s paired form, which
refuses `split_by` beside it); every other test refuses all four.
`TEST_MANOVA` and `TEST_BOX_M` require `split_by` (`details.reason`
`split_by_required`); `TEST_BARTLETT_SPHERICITY` and `TEST_MARDIA`
refuse it (`split_by_not_accepted`). Missing
data is listwise only (`params.missing` other than `"listwise"` is
refused). Shape refusals are `SERVICE_VALIDATION` with
`details.reason`; an undefined vector is `PULSE_VECTOR_UNKNOWN`.
`format_version` stays `"1.1"`; a test without them is byte-identical.

## Attribute vector inputs

`Attribute` carries two additive slots, the same mirror: `vector` /
`fields`. A vector-input attribute (the fitted `ATTR_MAHALANOBIS`,
`ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`, the row-local
`ATTR_SCALE_SCORE`, and the reference comparisons
`ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE`) sets exactly one of
them and no `field`; every other attribute refuses both.
Members are source columns only. `ATTR_SCALE_SCORE`'s `params` are
`stat` (`mean` | `sum`, prorated), `min_valid`, `reverse` (member
labels) and `scale_min` / `scale_max` (else the kind `scale` vector's
bounds). The comparisons' `params` are `metric` (similarity / distance
direction respectively; default from the vector's `kind`), `min_valid`
(complete pairs, default 1), `labels` (inline fields only) and
`reference` — a literal array (member order), a label map
`{label: value}`, `{table, key}` or `{table, key_field}` over
`Extensions.ReferenceVectors`, matched by label, a centroid
`{centroid: {filter, by}}` (the weighted per-member mean of the filtered
rows `filter` admits, one per `by` value with each row compared with its
own — a pre-pass, so the slot is two-pass), `{population: name}` (a
bound vector fit's stored mean, matched by label) or
`{record: {cohort?, keys: [{field, value}]}}` (one stored record,
point-looked-up through its cohort's sidecar index before the run — the
request's own cohort when `cohort` is omitted — read by member column
name); `ATTR_VEC_DISTANCE`
adds `fit_on` / `missing` beside its fitted metrics. A fitted attribute's `params` share `fit_on` —
`"filtered"` (the default) or an inline array of `Filterer` objects
choosing the fit rows among the filtered ones (every filtered row is
still scored) — and `missing` (`"listwise"` only). The default label is
`<TYPE>_<vector>`, or `<TYPE>_<first fields entry>` (`ATTR_PC_SCORE`
appends `_PC<component>`, `ATTR_FACTOR_SCORE` `_F<factor>`). Shape refusals
are `SERVICE_VALIDATION` with `details.attribute` and `details.reason`;
an undefined vector is `PULSE_VECTOR_UNKNOWN`. A fit that is singular,
or has N* ≤ p, nulls every score and adds a `PULSE_MATRIX_SINGULAR` /
`PULSE_MATRIX_INSUFFICIENT_N` entry to `Response.warnings` (details
`scope: "fit"`); `ATTR_FACTOR_SCORE` also nulls every score with
`PULSE_MATRIX_NOT_IDENTIFIED` (an unidentified or Kaiser-short factor
count) or `PULSE_MATRIX_HEYWOOD` (Bartlett over a uniqueness ≤ 0), and
scores with a `PULSE_MATRIX_HEYWOOD` / `PULSE_MATRIX_NOT_CONVERGED`
entry otherwise. `format_version` stays `"1.1"`; an attribute without
them is byte-identical.

## Attribute subject and order slots

`Attribute` carries two additive string slots read only by
`ATTR_PREVIOUS_STATE`: `subject_field` (the cohort field identifying
each record's subject — any scalar type but a `set_*` or a vector) and
`order_field` (a `date` or `datetime` cohort field ordering each
subject's records by instant). Both are required on that operator, name
source columns only (an earlier attribute or feature output is refused
`SERVICE_VALIDATION`), and are refused `PROCESSING_CONFIG` on every
other attribute type; a field of the wrong type is
`PULSE_FIELD_TYPE_UNSUPPORTED` (`details.slot` names the key). They are
slot keys, never `params` keys. `format_version` stays `"1.1"`; an
attribute without them is byte-identical.

## Grouper vector inputs

`Group` carries the same two additive slots, `vector` / `fields`, on
`groups` and both crosstab axes. A vector-input grouper (`GROUP_KMEANS`)
sets exactly one of them and no `field`; every other grouper refuses
both. Members are source columns only. Its key column (the Data row key,
the crosstab axis field) is `<TYPE>_<vector>` or `<TYPE>_<first fields
entry>`. Shape refusals are `SERVICE_VALIDATION` with `details.group`
and `details.reason`; an undefined vector is `PULSE_VECTOR_UNKNOWN`. A
best start stopped at `params.max_iter` adds a
`PULSE_MATRIX_NOT_CONVERGED` entry to `Response.warnings` (`solver`
`kmeans_lloyd`). Predict's `groups[i].row_buffer_bytes` reports its point
store. `format_version` stays `"1.1"`; a grouper without them is
byte-identical.

## Aggregation vector inputs

`Aggregation` carries the same two additive slots, `vector` / `fields`,
on `aggregations`, `crosstab.cell` and `crosstab.margin_aggregations`.
A vector aggregator (`AGG_VEC_MEAN`, `AGG_VEC_SUM`, `AGG_VEC_SD`) sets exactly one of
them and no `field`; every other aggregator (extensions included)
refuses both. Members are source columns only; `params.labels` (one per
member) is accepted with `fields` only. The default label is
`<TYPE>_<vector>` or `<TYPE>_<first fields entry>`. The value in
`data` is a JSON ARRAY, one figure per member in member order (a
non-finite element is `null`); its components entry adds `labels`,
`element_n`, `element_sum` and, weighted, `element_sum_weights`
(`AGG_VEC_SD` adds `element_mean`, `element_m2` and, weighted,
`element_sum_weights_sq`; an element whose sample size is 1 or less is
`null`). Shape
refusals are `SERVICE_VALIDATION` with `details.aggregation` and
`details.reason`; an undefined vector is `PULSE_VECTOR_UNKNOWN`. An
array-valued aggregator is refused `PROCESSING_CONFIG`
(`details.reason` `array_valued`) as a crosstab cell or margin
aggregation (the manifest lists them in
`crosstab.array_valued_aggregators`), as a `sort` key, as
`aggregations[0]` under `overlays` and in a chain stage that feeds
another (unless it expands, below). A crosstab cell or margin
aggregation runs when its `params.element` names ONE member label
(built-in `AGG_VEC_*` only): the slot is a scalar over that member —
the cell (or auxiliary) is that element's figure, its floor counts the
rows that answered the member (so a record reaches an auxiliary margin
only if its selected member reached a cell), its components are
one-entry arrays, and it margins and fuses like its scalar twin;
`element` outside a crosstab, beside `expand`, unknown or shared is
`SERVICE_VALIDATION` (`element_*`). `format_version` stays `"1.1"`; an aggregation without them is
byte-identical.

`params.expand: true` on a vector aggregator replaces the array with one
scalar column per member, `<label>[<element label>]` in member order
(grouped: per row); the label column is absent. Every execution mode
writes the same keys. `PredictResult.aggregations[i].columns` lists them
(omitted on every other slot, whose one column is its label), the
`return` column check and `PredictChain`'s `stage_schemas` use them, and
duplicate element labels are refused (`SERVICE_VALIDATION`, reason
`expand_duplicate_labels`). Each column is an ordinary nullable f64
column: a `sort` key, a window's `field` / `order_by` / `partition_by`,
a post-test field and a series overlay's `params.field` (request,
Compose or whole-chain host) read it, and so does a later chain stage —
the synthesised schema carries each as an f64 field (an element no row
answered is NaN — null on the wire — and orders last / is skipped like a
null, and a later chain stage reads it as a null; an overlay reads it as
a missing host value), while the bare array label
stays refused there `PROCESSING_CONFIG` (`array_valued`) — so is an
overlay scoring it by default as `aggregations[0]`. A `return` path
names one with a quoted segment (`data[*]."<label>[<element label>]"`)
and all of them with a prefix glob (`data[*].<label>*`).

`field` is optional on `Aggregation`, `Group` and `Attribute` (a vector
aggregator or grouper reads `vector` / `fields`, `ATTR_FORMULA` an
`expression`); the Go wire form still writes it.

## Matrix slots

`matrices` on `Request` is an additive array of `MatrixSpec`
`{name, type, vector | fields, params, weight, encoding}` and
`matrices` on `Response` the matching array of `MatrixResult`
`{name, type, group_key?, group_header?, primary, auxiliary, vectors,
scalars, warnings}`, one per spec in request order (`format_version`
stays `"1.1"`; a matrix-free request and response are byte-identical).
`type` is the registry-backed `MatrixType` enum (`MAT_COVARIANCE`,
`MAT_CORRELATION`, `MAT_PARTIAL_CORRELATION`, `MAT_PCA`, `MAT_FACTOR`,
`MAT_COLLINEARITY`, `MAT_RELIABILITY`, `MAT_DISTANCE`, `MAT_SEM`); `encoding` is `full` (default), `upper` or `records`. Every
matrix is a dedicated `MatrixValues` `{kind, encoding, row_keys,
column_keys, labels?, values}` — not the crosstab `MatrixPayload` —
whose `values` rows hold `p` cells (`full`) or `p − r` cells from the
diagonal (`upper`). `kind` is `square_symmetric` or `rectangular`
(`MAT_PCA`'s p × k loadings and eigenvectors: `row_keys` the members,
`column_keys` `PC1` … `PCk`, k possibly 0, always written `full`;
`MAT_FACTOR`'s p × k loadings: `column_keys` `F1` … `Fk`;
`MAT_COLLINEARITY`'s variance decomposition: a row per variable —
`(intercept)` first unless `center` — and `column_keys` `D1` … `Dq`;
`MAT_DISTANCE`'s primary is `square_symmetric` but keyed by its
between grouper's bucket labels, and its `auxiliary.centroids` is k × p:
`row_keys` the buckets, `column_keys` the members; `MAT_SEM`'s
`auxiliary.estimates` is r × 5: `row_keys` semopy's inspect rows
`"lval op rval"`, `column_keys` `estimate`, `std_estimate`, `std_err`,
`z_value`, `p_value` — r × 7 with `ci_lower`, `ci_upper` on a
lavaan-convention fit, whose `~1` rows are keyed `"lval ~1"`; that fit's
`auxiliary.implied_means` / `observed_means` are p × 1: `row_keys` the
members, `column_keys` `mean`).
Under `encoding: "records"` every matrix (primary and each auxiliary,
rectangular ones too) is instead `{kind, encoding, columns, rows,
labels?}`: `columns` the column keys, `rows` one object per row in row
order, `{"key": <row key>, <column>: number | null, ...}`, plus on
`MAT_SEM`'s estimates `lval`, `op`, `rval`, `parameter` and `free` (and
`group`, `label` under `params.group`); the
schema's `MatrixValues` is the `oneOf` of the two forms.
`MAT_CORRELATION` (every `method`) and `MAT_PARTIAL_CORRELATION` take
`params.inference` (bool) and `params.alpha` (0 < alpha < 1, default
0.05): on, `auxiliary` gains `p`, `ci_lower` and `ci_upper`, square
twins of `primary` (same axis and `encoding`, diagonal `null`); off or
absent, the response is byte-identical. `params` stays an open object
in the schema — its keys are the manifest's per-operator `params`.
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
are the non-control members — and `repair` `"nearest"`;
`MAT_RELIABILITY` only: `reverse` (item names), `scale_min` /
`scale_max` (required with `reverse`; a missing range or an out-of-range
value is `PROCESSING_CONFIG`) and `repair` `"nearest"`; at least 2
items; `MAT_PCA` only: `on` `correlation` (default) or `covariance`,
`components` an integer k ≤ p, `"kaiser"` (default on a correlation,
refused on a covariance) or `{"variance": share}` — required on a
covariance — `rotation` `none` (default) or an orthogonal criterion
(`varimax`, `quartimax`, `geomin_orthogonal`, `entropy`; an oblique one
is refused, reason `rotation_oblique_unsupported`), `epsilon`
(`geomin_orthogonal` only, default 0.01) and `repair` `"nearest"`;
`MAT_COLLINEARITY` only:
`center` (boolean, default `false`: Belsley uncentered with the
intercept) and `repair` `"nearest"`; `MAT_FACTOR` only: `factors` an
integer k ≤ p or `"kaiser"` (default), `extraction` `minres` (default),
`principal_axis` or `ml`, `rotation` `none` (default), orthogonal
`varimax`, `quartimax`, `geomin_orthogonal`, `entropy` or oblique
`promax`, `oblimin`, `geomin`, `kappa` (promax only), `gamma` (oblimin
only) and `epsilon` (the geomin pair only, default 0.01), and
`repair` `"nearest"`; `MAT_DISTANCE` only: `between` (required) a grouper
spec as in `groups` — any built-in but `GROUP_DATE` / `GROUP_DATE_RANGES`,
no own `weight` or `tz` — `metric` a `linalg/metric` registry name
(`capabilities.matrix.metrics`, consumer `MAT_DISTANCE`): `euclidean`
(default), `sq_euclidean`, `manhattan`, `chebyshev`, `cosine`,
`hellinger`, `aitchison` or `mahalanobis`, `standardize` (boolean, default
`false`), `min_valid` (integer 1 … p, default 1), listwise only;
`MAT_SEM` only: `model` (a lavaan / semopy description whose observed
variables are exactly the members by label; default one factor over every
member), `solver` `slsqp` (default) or `fisher`, `information` `dense`
(default) or `rank2`, `ftol` (slsqp only, default 1e-6), `max_iter`
(default 10000 slsqp, 500 fisher), `grad_tol` (fisher only, default
1e-10), `singular` `nearest` (default, semopy's) or `refuse`,
`meanstructure` (boolean, default `false`; true — or a `~ 1` line in the
model — models the means under lavaan conventions), `group` (a categorical,
`packed_bool` or `u4` / `u8` field: one joint multi-group fit over its
levels, lavaan conventions) and `group_reference` (a level label; default
the first level), listwise only — a probability weight is
`PULSE_WEIGHT_UNSUPPORTED`). Under `pairwise`, `auxiliary.n` is a `MatrixValues` of
the same shape and encoding holding each pair's row count. `vectors` is an
open object: with `summary.top_pairs`, `vectors.top_pairs` is
`[{row, col, r, n}]` — the k off-diagonal pairs with the largest `|r|`,
ties in axis order, undefined pairs skipped, `n` the pair's row count;
`MAT_RELIABILITY` adds `vectors.item_total_r` / `alpha_if_deleted` /
`item_mean` / `item_sd` (one number per item, axis order) and `scalars`
`alpha`, `alpha_standardized`, `mean_inter_item_r`, `omega`;
`MAT_PCA` adds `auxiliary.eigenvectors`, `vectors.eigenvalues` /
`explained_variance` / `cumulative` / `communalities` / `kmo_msa` (one
number per member or component) and `scalars` `kmo`, `bartlett_chisq`,
`bartlett_df`, `bartlett_p`, `components_retained` (the two integer
scalars are never rounded by `return.precision`); `MAT_COLLINEARITY`
adds `auxiliary.variance_decomposition` (rows sum to 1),
`vectors.vif` / `tolerance` (one per member) and `condition_indices`
(one per dimension, ascending) and `scalars` `condition_number`,
`max_vif`; `MAT_FACTOR` adds `auxiliary.unrotated` (rotated runs) and
`auxiliary.factor_correlation` (an oblique rotation: `square_symmetric`
keyed `F1` … `Fk`, always `full`), `vectors.communalities` /
`uniquenesses` (one per member) and `variance_accounted` (one per
factor) and `scalars` `rmsr`, `empirical_chisq`, `df`, `empirical_p`,
`kmo`, `bartlett_chisq`, `bartlett_df`, `bartlett_p`, `iterations`,
`converged`, `factors_retained` (`df`, `iterations`, `converged` and
`factors_retained` are never rounded by `return.precision`), plus, under
`extraction` `ml` only, `likelihood_chisq`, `likelihood_p`, `tli`,
`rmsea`, `rmsea_lower`, `rmsea_upper` (the interval at
`params.conf_level`, default 0.9) and `bic` (all but `likelihood_chisq`
null at `df` ≤ 0); `MAT_DISTANCE` adds
`auxiliary.centroids` (always), `centroids_standardized` (standardize)
and `pooled_covariance` (mahalanobis), and `vectors.sizes` (each bucket's
weight mass) / `counts` (its rows, integers), one per bucket;
`MAT_SEM`'s primary is the model-implied covariance over the members and
it adds `auxiliary.estimates` and `residual_covariance` (S − Σ(θ̂)),
`vectors.parameter` (each estimate row's parameter name) / `free`
(booleans) and `scalars` `dof`, `dof_baseline`, `chi2`, `chi2_p_value`,
`chi2_baseline`, `cfi`, `gfi`, `agfi`, `nfi`, `tli`, `rmsea`, `aic`,
`bic`, `loglik`, `objective`, `n`, `n_params`, `iterations`, `converged`
(`dof`, `dof_baseline`, `n_params`, `iterations` and `converged` are
never rounded by `return.precision`); under `params.group` the estimate rows are
keyed `"[level] lval op rval"`, `vectors` adds `group` and `label` (one
per row), the primary is the `group_reference` level's implied
covariance, `auxiliary` carries `estimates` plus per level
`implied_covariance.<level>`, `residual_covariance.<level>`,
`implied_means.<level>` and `observed_means.<level>`, and `scalars` add
`chi2_group_<level>` and `n_group_<level>`).
`warnings` are `{code, message, details}` entries
(`PULSE_MATRIX_INSUFFICIENT_N`, `_ZERO_VARIANCE`,
`_LISTWISE_HEAVY_DROP`, `_NOT_PSD` — fatal, not a warning, on
`MAT_PARTIAL_CORRELATION` without `repair: "nearest"`; on
`MAT_RELIABILITY` a warning that nulls `omega`, beside `_HEYWOOD` and
`_NOT_IDENTIFIED`; `MAT_PCA` fatal like the partial correlation, plus
`_PAIRWISE_N_STAR` and a `_SINGULAR` warning that nulls KMO and
Bartlett; `MAT_COLLINEARITY` fatal like the partial correlation, and a
singular correlation is a fatal `_SINGULAR` naming `dependent_fields`;
`MAT_FACTOR` fatal like the partial correlation, plus `_PAIRWISE_N_STAR`,
`_NOT_IDENTIFIED` (null fit), `_HEYWOOD` (fit kept), `_NOT_CONVERGED`,
a `_SINGULAR` warning that nulls KMO and Bartlett (and, under `ml`, the
whole fit) and, under `ml`, `_INSUFFICIENT_N` scope `likelihood` that
nulls the likelihood figures; `MAT_DISTANCE` `_INSUFFICIENT_N` scope
`buckets` (a massless bucket) or `pooled` (mahalanobis with no more rows
than buckets) and a `_SINGULAR` warning that nulls the mahalanobis
distances; `MAT_SEM` `_NEAREST_PD` when the covariance does not factor
and is fitted to semopy's `cov_nearest` substitute (`singular`
`nearest`, the default; one that factors is fitted raw), a fatal
`_SINGULAR` on a zero-spread member or, under `singular` `refuse`, on
any rank-deficient covariance,
`_NOT_CONVERGED` (solver stop short of success, last iterate kept) and
`_NOT_IDENTIFIED` (negative degrees of freedom, fitted anyway)). `components.matrices` carries one
`MatrixComponents` per result, in the same order: `{name, type,
group_key?, n, n_null, n_listwise_dropped, min_pair_n?, max_pair_n?,
sum_weights?, n_eff?, n_weight_invalid?, operator?}` (pairwise and
weighted keys only when they apply; `MAT_RELIABILITY`'s `operator`
carries the omega fit's `iterations` / `converged`; `MAT_DISTANCE`'s
`metric`, `standardize`, `buckets` and `n_null_key` — the rows its
between grouper placed in no bucket). Contract:
`.claude/reference/matrix-and-vectors.md`.

## Regression coefficient covariance

`RegressionSpec` takes an additive `vcov` boolean (default `false`) and
`RegressionResult` two additive `omitempty` `MatrixValues` slots,
`vcov` and `correlation`, present only when the spec set `vcov: true`
(`format_version` stays `"1.1"`; a fit without it is byte-identical).
Both are `square_symmetric`, `full`, with `row_keys` = `column_keys` =
`(intercept)` then the predictors in spec order. `vcov` is the
coefficient covariance — `REG_OLS` σ̂²·(XᵀX)⁻¹ (ridge: the sandwich),
`REG_GLM` φ·(XᵀWX)⁻¹ at the converged working weights (φ = 1 for
binomial / poisson; gamma's estimate, the Pearson χ² over its `df`),
whose square-rooted diagonal is exactly `std_errors`; `REG_BAYES_LINEAR`
the POSTERIOR covariance b_n/(a_n−1)·Λ_n⁻¹ (`null` cells when
a_n ≤ 1), whose diagonal is `std_errors`²·a_n/(a_n−1). A weighted fit
reads the inference size N* (Σw frequency, n_eff probability), as its
standard errors do. `correlation` is `vcov` scaled to a unit diagonal
(R's `cov2cor`); an entry with a zero or undefined variance is `null`.
Beside a classical `vcov` whose dispersion is estimated — unpenalized
`REG_OLS` (σ̂² = RSS/df) and `REG_GLM` gamma (the Pearson χ²/df) —
`RegressionResult` carries the additive `omitempty` number `dispersion`,
so `vcov / dispersion` is (XᵀWX)⁻¹ (`null` when the residual df is ≤ 0;
absent on binomial / poisson, whose φ is 1, and on every robust,
resampled, penalized or Bayesian fit). The influence figures over a
bound fit read it.
Every plain `REG_OLS` / `REG_GLM` fit carries the additive `omitempty`
numbers `two_log_lik` (twice the maximised log-likelihood, R's
`logLik()`) and `aic` (−`two_log_lik` + 2·k, k `logLik`'s df: σ counted
on OLS, the dispersion on gamma, an estimated θ on negative binomial),
absent under a probability weight, a penalty, a binding OLS bound and on
`REG_BAYES_LINEAR`; `null` when infinite (a non-count poisson /
binomial response). A `REG_GLM` `negative_binomial` fit (NB2, Var = μ +
μ²/θ) adds two more additive `omitempty` numbers: `theta` — θ̂, the
maximum-likelihood estimate (MASS `glm.nb`), or the spec's fixed `theta`
(an additive `omitempty` `RegressionSpec` number, > 0) — and
`theta_std_err` (θ̂'s standard error 1/√I(θ̂), present only when θ was
estimated; `null` when the information is not positive). Its `vcov` is (XᵀWX)⁻¹ at θ̂ with φ = 1 (MASS
`summary.negbin`), so no `dispersion` rides it — unless a fixed θ sets
the additive `omitempty` `RegressionSpec` string `dispersion`
(`fixed` | `estimated`) to `estimated`: then its `vcov` is
φ̂·(XᵀWX)⁻¹, φ̂ the Pearson χ²/df, carried as `dispersion` with `df` (R's
`summary.glm`). Every other family leaves `theta` and `theta_std_err`
absent.
An additive `omitempty` `RegressionSpec` object `margins` (`{type,
terms, scale}`, `REG_OLS` / `REG_GLM`) adds the additive `omitempty`
object `RegressionResult.margins` (`RegressionMarginsResult`): `type`
(`ame` | `mem`), `scale` (`response` | `link`), `terms` (string array),
the term-keyed number maps `estimates`, `std_errors`, `statistics`,
`p_values`, `ci_lower`, `ci_upper`, and `at` (`mem` only), the numbers
`conf_level` and `df` (present only for a Student t; `null` when ≤ 0),
and — gated on `capability:multiplicity` like the coefficient twins —
`p_adjusted` / `significant_adjusted` and a `multiplicity`
(`AppliedMultiplicity`, `family: "margins"`) echo: under the
regression's own block the terms form one family per regression, apart
from the coefficients'. An undefined figure is `null`,
key kept. A request without `margins` is byte-identical.
An additive `omitempty` `RegressionSpec` object `collinearity`
(`{basis, groups}`, `REG_OLS` / `REG_GLM`; `basis` `model` |
`predictors`, `groups` an array of `{name, predictors}`) adds the
additive `omitempty` object `RegressionResult.collinearity`
(`RegressionCollinearityResult`): `basis`, `terms` (string array), the
term-keyed maps `df` (integers), `vif` and `tolerance` (numbers), and
`gvif_scaled` (numbers, present only when `groups` is set). An
undefined figure is `null`, key kept. A request without `collinearity`
is byte-identical.
Under `resample` it is the replicate covariance (jackknife
(g−1)/g·Σ(β₋ₖ−β̄)(β₋ₖ−β̄)ᵀ over its g delete groups, g = n on
leave-one-out; bootstrap the B−1 sample covariance of the
successful replicates), under `selection` the final refit's covariance
keyed `(intercept)` + the selected predictors — so its diagonal is
`std_errors`² there too. Lasso / elastic net refuse `vcov`, with or
without a modifier, with `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`
(`details.reason` `penalty`) in predict and at runtime alike. Both slots are in the `standard` preset (not
`minimal`) and every float in them is rounded by `return.precision`.

## Extension regression results

An embedder-registered `REG_<NS>_<NAME>` model (`pulse.Options.Extensions.Regressions`)
returns its result in the built-in `RegressionResult` shape, plus two
additive `omitempty` slots the built-ins never fill (`format_version`
stays `"1.1"`; a built-in fit is byte-identical): `statistics`, the
per-coefficient test statistic (keyed like `coefficients`), and
`outputs`, an object of the model's declared extras (its
registration's `OutputSchema` keys). On an extension fit `std_errors`,
`statistics` and `p_values` each carry EVERY coefficient key; a figure
the model did not compute is `null` (key kept), so the coefficient
table stays rectangular. A model whose registration does not declare
`PValues` emits none: its `p_values` is `null` on every coefficient,
under `resample` too. Only a coefficient whose p-value is defined
joins a multiplicity family. `ci_lower` / `ci_upper` / `conf_level`
are the engine's (below, Regression coefficient intervals): β ∓ q·SE
on every coefficient key, `null` where the standard error is, q the
Student-t on the model's reported `df`, else (and under `resample`)
the normal; `df` is the reported one only on a t interval. A
non-finite float in `outputs` is `null`.
`n_obs`, `sum_weights` / `n_eff`, `p_adjusted`, `multiplicity` and
`error` stay engine-owned. `outputs` is not on the `components`
machinery: neither `disable_components` / `--no-components` nor any
`return` preset strips it (it is in `minimal` and `standard`). The Go
`RegressionResult.ReportedFigures` (the fit statistics the model
reported, so a reported exact 0 ranks in a compose `sweep`) is
`json:"-"` and never reaches the wire. A sweep `rank` reads an
extension fit like a built-in: `regressions.<name>.outputs.<key>` ranks
on a declared output (an exact 0 included), a fit statistic the model
reported ranks, and one it did not report is missing — refused
`PULSE_SWEEP_RANK_PATH` (reason `missing`) when no slot reports it.

Under `resample` (always accepted on an extension model) the result
echoes `resample` (and `bootstrap_requested` / `bootstrap_succeeded`,
`jackknife_groups`) as a built-in's does, and every coefficient's
`std_errors`, `statistics` (the Wald z) and `p_values` carry the
replicate figures; under `selection` (a `SupportsSelection` model) it
echoes `selection`, `criterion` and `selected_features`.

On the request side `RegressionSpec` takes an additive `params` object
(`omitempty`; absent is byte-identical), the model's own configuration.
Its keys are the registration's declared `Params` — the manifest's
`extensions.regressions[].params` entries not marked `slot` — and any
other key is refused `PULSE_REQUEST_UNKNOWN_FIELD` with `details.path`
`regressions[i].params.<key>`, identically at predict and at runtime; a
value not of its declared `json_type`, or a missing `required` param, is
`PULSE_REQUEST_PARAM_INVALID` at the same path (every extension
category's `params` is checked this way). A
registration declaring no params leaves the object unchecked (predict
notes it `PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED`). The built-ins
(`REG_OLS`, `REG_GLM`, `REG_BAYES_LINEAR`) take every knob as a typed
field and declare no params, so any `params` key on one is refused. The
manifest row lists the model's `output_schema` (`key`, `json_type`,
`description`), the keys `outputs` may carry. Recipe:
`docs/src/internals/extension-points.md` (Regression model).

## Regression robust covariance

`RegressionSpec` takes an additive `covariance` object `{type, cluster,
order_by, lags}` and `RegressionResult` an additive `omitempty`
`covariance` echo `{type, cluster, clusters, order_by, lags}`
(`format_version` stays `"1.1"`). `type` is the `CovarianceType` enum
(`classical`, `hc0`, `hc1`, `hc2`, `hc3`, `cluster`, `hac`); `lags` is a
non-negative integer or `"auto"`. Absent or `classical` is
byte-identical to a request without the block and echoes nothing. Any
other type on `REG_OLS` or `REG_GLM` replaces the estimator behind
`std_errors`, `p_values`, `ci_lower` / `ci_upper` and `vcov` (so
√diag(`vcov`) is still exactly `std_errors`); under `cluster`, an OLS
`df` is the cluster count minus 1 (GLM stays Wald z). On
`REG_BAYES_LINEAR` it keeps the posterior-mean `coefficients` and
REPLACES the posterior covariance with Müller's sandwich posterior:
`std_errors`, `credible_intervals`, `ci_lower` / `ci_upper` (normal, no
`df`) and `vcov` follow it. Shape faults are `PROCESSING_CONFIG`; a
robust type beside a penalty, `resample` or `selection`, or on another
regression type, is
`PROCESSING_REGRESSION_VCOV_UNSUPPORTED` (`details.reason`), in predict
and at runtime alike. The echo is in the `standard` preset.

## Regression shrinkage controls

`RegressionSpec.prior_precision` (`REG_BAYES_LINEAR`) is
`#/$defs/PriorPrecision`, a `oneOf` of a non-negative number (the
scalar ε, Λ₀ = ε·I; byte-identical to the former float field, absent or
0 = the default 1e-3) and an array of non-negative numbers (one per
coefficient, intercept first like `prior_mu`: Λ₀ = diag; 0 is a flat
prior). A wrong length or a negative entry is `PROCESSING_CONFIG` in
predict and at runtime. `RegressionSpec.unpenalized` is an additive
`omitempty` array of predictor names the `REG_OLS` ridge (`penalty`
`l2`) penalty skips (glmnet `penalty.factor` 0); every other penalty or
type refuses it `PROCESSING_CONFIG`, predict and runtime alike.
`format_version` stays `"1.1"`; neither is echoed on the result.

`RegressionSpec.bounds` (`REG_OLS`) is an additive `omitempty` object
`#/$defs/RegressionBounds` `{nonnegative: [predictor names]}`: those
slopes are constrained ≥ 0. A fit no bound binds is plain OLS byte for
byte; a binding fit keeps its coefficients (a held one exactly `0`) and
R² and reports `std_errors`, `p_values`, `ci_lower` / `ci_upper` and
every `vcov` / `correlation` entry `null` (keys kept), with
`PULSE_ADVISORY_CONSTRAINT_ACTIVE` on `advisories` (result-only).
Refused `PROCESSING_CONFIG` with a penalty or selection, on another
type or for an unknown / repeated name; a robust `covariance` is
`PROCESSING_REGRESSION_VCOV_UNSUPPORTED` (`details.reason` `bounds`).
`format_version` stays `"1.1"`; the bounds are not echoed.

## Regression coefficient intervals

`RegressionSpec` takes an additive `conf_level` float (`0 < c < 1`,
zero or absent = 0.95; out of range is `PROCESSING_CONFIG` in predict
and at runtime) and `RegressionResult` four additive `omitempty`
slots: `ci_lower` / `ci_upper` (maps keyed like `coefficients`, a key
present exactly when `std_errors` carries it), `df` and `conf_level`
(`format_version` stays `"1.1"`). Each bound is β ∓ q·SE: `REG_OLS` a
Student-t q on `df` = the residual N* − p − 1 (fractional under
probability weights), `REG_GLM` a Wald normal q (no `df`),
`REG_BAYES_LINEAR` the posterior t on `df` = 2·a_n — equal to
`credible_intervals` when `conf_level` is unset, since it then falls
back to `credible_level` —, a registered model a t q on the `df` it
reports (else a normal q, no `df`), and any fit under `resample` a normal q on
the resampled standard errors (no `df`). An undefined bound or `df`
(residual df ≤ 0) is `null`. All four are in the `standard` preset (not
`minimal`) and rounded by `return.precision`.

## Regression holdout

`RegressionSpec` takes an additive `holdout` string — an expr-lang
boolean predicate over the fully built record (schema fields, feature
outputs, attribute labels, window labels and post-window attribute
labels; a window label only where the windows run over the record rows,
else `PULSE_REGRESSION_WINDOW_UNAVAILABLE` with `details.slot`
`holdout`). A row it calls `true` is held out of the fit and scored by
the fitted model afterwards, `false` is fitted, `null` joins neither.
`RegressionResult` gains the additive `omitempty` object `holdout`
(`#/$defs/RegressionHoldout`) `{n, n_null, rmse, mae, r2}`: `n` the
scored held-out rows (target, predictors and — weighted — a valid
positive weight present; a raw count), `n_null` the null-predicate
rows, the figures on the response scale (a `REG_GLM`'s inverse link)
weighted by the slot `weight`, `r2` = 1 − SSE/SST about the held-out
rows' own mean — `null` (key kept) when the held-out targets do not
vary. Absent `holdout` is byte-identical and `format_version` stays
`"1.1"`. A predicate that does not compile as a boolean or names an
unknown column is `PULSE_FORMULA_COMPILE_ERROR`; a type the instance
cannot score is `PULSE_MODEL_SCORE_INVALID` `details.reason`
`no_scorer` — both refused identically at predict and at runtime. No
scorable held-out row fails the slot alone
(`PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `details.reason`
`holdout_empty`; `PULSE_SLOT_FAILED`). A holdout forces the buffered
path (predict `streamable_reasons`: `"regression <TYPE> holdout requires
the buffered path (the held-out rows are scored after the fit)"`). The
block rides `capability:regression_holdout`, is kept by the `standard`
and `minimal` presets, is rounded by `return.precision` and never
appears under `Response.components`.

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

## Attribute components

`attributes` on `ResponseComponents` is an additive `omitempty` array of
`AttributeComponents` `{label, type, n, n_null, operator?}`
(`format_version` stays `"1.1"`; a response with no attribute slot whose
operator declares a `ComponentSchema` has no `attributes` key and is
byte-identical). What the schema cannot say: an entry exists only for a
slot whose operator declares a schema (`ATTR_SCALE_SCORE`, an extension
attribute registered with one) — `manifest.components_schemas.attributes`
lists them — so entries are keyed back by `label` (the effective output
label) and `type`, never by index into `attributes`; `n` / `n_null`
count the filter-passing rows whose output is present / null; entries
are cohort-wide (a grouped or crosstab run carries one per slot, never
per bucket) and carry no weighted floor keys. `ATTR_SCALE_SCORE`'s
`operator` holds `n_out_of_range` / `n_rows_out_of_range` only when a
range is declared. Contract: `.claude/reference/response-components.md`
(Attribute components).

## Return slot

`return` on `Request` is an additive `Return` object
`{preset?, include?, exclude?, precision?}` (`format_version` stays
`"1.1"`; a request without one is byte-identical and hashes
identically). `preset` is the closed `ReturnPreset` enum (`full`,
`standard`, `minimal`); `precision` is significant digits, 1–17 (0 or
absent: unlimited). `include` / `exclude` are paths over this schema's
`Response` JSON names at any depth — `.` between keys, `[*]` into every
array element, a trailing `*` on a map-key segment as a prefix glob
(`tests[*].details.effect_*`). A key holding `.`, `[`, `]` or `*` (an
expanded vector element column) is a quoted segment,
`data[*]."emb_mean[q3]"`: inside the quotes every byte is literal except
`\"` and `\\`, and only a segment that starts with `"` is quoted, so
every unquoted path and its plan digest are unchanged. What the schema cannot say, predict and
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
(`[{name, paths, companions?}]`, in `full` / `standard` / `minimal` order) with each
preset's paths **expanded against the instance**: a path a hidden
feature owns (e.g. `matrices[*]` without `capability:matrices`,
`tests[*].p_adjusted` without `capability:multiplicity`) is absent from
the listing and from the selection, never an error. A preset is an
include allowlist, so a caller's `include` can always add a part back.
Nested `*.warnings` are not listed: each is kept wherever its parent is
emitted. `companions` lists a preset's CONDITIONAL selections, each
`<path> (with <key>)`: the map key at `path` is kept wherever its parent
map also holds a kept `<key>` (`returnPresetCompanions`). `standard` has
one, `matrices[*].auxiliary.n (with p)`; predict's `return` plan echoes
it as `companions` and its digest covers it. An include or exclude
covering the path removes it; excluding `<key>` drops it too. Every listed path must resolve against this schema
(`TestReturnPathsMatchSchema`).

| Slot | `minimal` (primary result) | `standard` |
|---|---|---|
| `label` (the Compose slot echo) | yes | yes |
| `data`, `warnings` | yes | yes |
| `metadata` | no | yes |
| `crosstab` | `shape`, `matrix` | whole |
| `matrices[*]` | `name`, `type`, `group_key`, `primary` | all but `auxiliary`, except the inference figures a spec asked for: `auxiliary.p` / `ci_lower` / `ci_upper` (`params.inference`), `p_adjusted` / `significant_adjusted` (a multiplicity family), and the pairwise `auxiliary.n` beside a kept `p` (a companion). They exist only on that spec's result, so a spec without inference keeps no auxiliary |
| `tests[*]` / `post_tests[*]` | `label`, `type`, `group_key`, `statistic`, `p_value`, `reject_null`, `p_adjusted`, `significant_adjusted` | minimal's keys + `variant`, `df`, `alpha`, `multiplicity`, `details.effect_size` (no other `details` key) |
| `regressions[*]` | `name`, `type`, `coefficients`, `p_values`, an extension model's `outputs`, `holdout` when asked for | all but `credible_intervals` and `selection` (so `ci_lower` / `ci_upper` / `df` / `conf_level`, and `vcov` / `correlation` / `covariance` / `holdout` when asked for; an extension model's `statistics` and `outputs`) |
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

### Compose sweep

`ComposedRequest.sweep` (`SweepSpec`) is the one slot that expands into
ordinary Compose slots, so a sweep adds no response shape of its own:
`responses` carries explicit slots first, then one entry per expansion.
`SweepSpec` is `{axes, mode, samples, rng_seed, label, request,
overlays, rank}`: `axes` are `SweepAxis {name, values, range}` (`values`
a non-empty list of scalars under `grid` / `zip`; `range` a
`SweepRange {min, max, scale}` — `SweepScale` `linear` default, `log` —
under `random` / `lhs`, never both), `mode` the closed enum `SweepMode`
(`grid` default, `zip`, `random`, `lhs`; the sampling modes draw
`samples` slots seeded by `rng_seed`, 0 deriving one from the spec), `request` an
object in the `Request` shape that may carry `{{axis}}` / `{"$var":
"axis"}` placeholders (the schema types it as `object`; the substituted
body is decoded strictly at expansion; a `{{axis}}` inside an
expression string is `PULSE_SWEEP_EXPR_SPLICE`), `overlays` an array of
`ComposeOverlaySpec` objects with placeholders, and `rank` a
`SweepRank {by, order, top}` (`SweepRankOrder`: `asc`, `desc`). The slot
is `omitempty`, so a sweep-free request is byte-identical on the wire.
It is gated by `capability:compose_sweep`: on an instance that hides it
the `sweep` property and the `Sweep*` defs are absent. The sampling modes
ride `capability:compose_sweep_sampling`: on an instance that hides it
the `SweepMode` enum is `grid` / `zip`, and `samples`, `rng_seed`, the
axis `range`, the `SweepRange` def and `ComposedResponse.sweep_seed` are
absent. An item `for_each`'s `mode` is `grid` / `zip` and its axes carry
`values` only, on every instance.
A malformed sweep
is `PULSE_SWEEP_INVALID` with `details.field` (a `sweep`-rooted path)
and `details.reason`; a mode/axis mix-up is `PULSE_SWEEP_MODE_MISMATCH`,
a bad range `PULSE_SWEEP_RANGE_INVALID`, a missing or sub-1 `samples`
`PULSE_SWEEP_SAMPLES_INVALID`. `pulse_predict` with `composed` reports the
expansion as `sweep {axes, mode, expanded_count, rng_seed, labels}` on the
predict result; that summary is not payload-reachable and is not in this
schema.

A sampling sweep (`random` / `lhs`) adds `ComposedResponse.sweep_seed`,
an `omitempty` integer: the effective seed it drew with, so pinning it as
`rng_seed` replays the run. Absent for `grid` / `zip`. Like `ranking` it
is pinned outside the Compose-level `return`.

A sweep with `rank` adds `ComposedResponse.ranking`, an `omitempty` array
of `RankEntry {label, value, rank, slot, axes}` (1-based, in rank order,
`value` always finite). `slot` (always present) is the entry's 0-based
index among the sweep slots in expansion order; `axes` (always present)
maps each sweep axis name to the scalar the slot ran with — a number, a
string or a boolean, as the axis declared it (a drawn `random` / `lhs`
value is a number in shortest round-trip form). Only sweep slots are
ranked. A slot whose value at
`rank.by` is null, or whose path is missing there but resolves elsewhere,
is left out with a `PULSE_SWEEP_RANK_PATH` warning on its own response;
a non-number, ambiguous or nowhere-resolving path refuses the batch with
the same code. `ranking` is pinned outside the Compose-level `return`:
always emitted, never rounded by `precision`.

### Compose banner

`ComposedRequest.banner` (`BannerSpec`) is
`{request, sections, groups, total, overlays}`: `request` a typed
`Request` (the base every slot starts from — no `label`, no crosstab
`columns`, no crosstab `shape: long`), `sections` a non-empty list of
`BannerSection {label, filterers, rows, cell, cells, overlays}`, `groups` a list of
`BannerGroup {label, columns}`, `total` a boolean (the Total block: a
crosstab with no column grouper) and `overlays` request `OverlaySpec`s
appended to every group slot — except a section's that sets its own
`overlays`, which REPLACE them on its group slots. The base request's
`labels` ride only the slots whose crosstab rows or columns group by the
bound field (a binding no slot groups by stays on every slot). It expands into one crosstab slot per
section × block (Total first, then the groups), labelled
`<section>:<block>`, appended after the explicit and sweep slots. The
slot is `omitempty`, so a banner-free request is byte-identical on the
wire, and it is gated by `capability:compose_banner` (hidden: the
`banner` property and the `Banner*` defs are absent). A malformed banner
is `PULSE_BANNER_INVALID` with `details.field` and `details.reason`;
predict reports `banner {sections, groups, total, expanded_count,
labels}`.

The banner adds `ComposedResponse.banner`, an `omitempty`
`BannerResult {sections}`: one `BannerSectionResult {label, row_header,
row_keys, column_keys, cells, column_margins, cell_label, cell_labels,
normalize_applied, blocks, letters}` per section, the section's slot
matrices side by side — rows the union of the blocks' rows, column keys
concatenated. Each `BannerBlock {label, slot, total, start, count,
column_header, row_margins, grand_total, measures}` locates one block's
columns and carries its own margins: a record reaches a block's margin
only if it reached a CELL of that block. `letters` holds one
`BannerLetters {overlay, name, kind, scope, cells}` per banner overlay
carrying a `letters` part (from a section's own overlays: `section: true`
added, `overlay` indexing the section's list; absent otherwise); under `column` scope the letters are
re-assigned banner-wide (unique across blocks, comparison groups never
spanning two blocks) and the Total block carries no letter cell — it is
compared to nothing. Every figure is copied from its slot; undefined
figures are `null`. `banner` is pinned outside the Compose-level
`return`: always emitted, never rounded.

### Compose fits

`ComposedRequest.fits` is an `omitempty` array of `FitSpec {name,
request}`: `request` is an ordinary `Request` with no `groups` /
`crosstab` holding exactly one regression (a regression fit) or, with no
regression, exactly one vector battery — one `matrices` entry or one
`ATTR_MAHALANOBIS` / `ATTR_PC_SCORE` / `ATTR_FACTOR_SCORE` attribute (a
vector fit: the battery's listwise mean and co-moment) — one `MAT_SEM`
entry is instead a SEM fit (kind `sem`: the fitted model's parameter
matrices per group). Every fit runs
once, before any slot (sweep slots included), over its own cohort,
filters, features and weight; slots read it by `name` — a regression fit
through `ATTR_REG_PREDICT` / `coef()`, a vector fit through `params.fit`
on a fitted vector attribute or any co-moment `MAT_*` slot, a SEM fit
through `ATTR_SEM_SCORE`'s `params.fit` (row-local, bit-equal to the
inline fit over the same rows). One namespace holds all three kinds: `PULSE_MODEL_NAME_COLLISION` on a duplicate,
`PULSE_MODEL_UNKNOWN` for an unbound reference, `PULSE_MODEL_KIND_MISMATCH`
(`details.fit`, `kind`, `expected`) for a name of another kind. A
malformed entry is `PULSE_FIT_INVALID` with `details.fit` (the entry
index) and `details.reason` (`name_missing`, `request_missing`,
`regression_count`, `kind_ambiguous`, `vector_count`, `grouped`,
`vector_source`); a fit request's own refusal keeps its
code and gains `details.fit`. A fit request's `return` is ignored. The
fits surface as `ComposedResponse.fits`, an `omitempty` object of
`FitResult` keyed by fit name: a regression fit's `RegressionResult`
object as is (byte-identical to the regression-only shape), or a vector
fit's `{"vector": VectorFitResult}` — `operator`, `labels`, `members`,
`basis`, `n`, `n_weight_invalid`, `sum_weights`, `n_star`, `mean` and
`covariance` (a full square-symmetric `MatrixValues`, M2*/(N* − 1) on the
fit's weight basis; undefined cells `null`), or a SEM fit's
`{"sem": SEMFitResult}` — `operator`, `model`, `labels`, `members`, the
model's `observed` / `inner` / `latents` orders, `estimator`,
`conventions`, `meanstructure` and the multi-group knobs, `solver`,
`information`, `basis`, `n`, `sum_weights`, `dof`, `n_params`,
`converged`, `status`, `iterations`, `nearest_pd_min_eigenvalue` (only
when the nearest positive-definite substitute was fitted) and `groups`
(per level `level`, `n`, `sum_weights` and the full-precision matrices
`lambda`, `beta`, `psi`, `theta`, `nu`, `alpha`, `sample_mean`). An
ordinal fit (`estimator` `dwls` / `wlsmv`) adds `ordered` (its ordered
variables, `observed` order), `categories` (each one's category values,
ascending) and, per group, `thresholds` (one list per ordered variable);
all three are absent on an ML fit. A fit grouped by a column its request
computed adds `group_derived: true` and, when the levels were declared,
`group_levels` (`[{value, label}]`, in order); both are absent on a
cohort-field group. The
`vector` / `sem` key discriminates. A fit that failed on its data keeps its entry with
an `error` and no figures (see Failed slots). The object is pinned outside the Compose-level `return`
like `ranking` (always emitted, never rounded). Both slots are gated by
`capability:compose_fits`. `pulse_predict` with `composed` lists them as
`fits [{name, type, target, predictors, cohort}]` — a vector fit as
`{name, cohort, vector: {operator, labels, members}}`, a SEM fit as
`{name, cohort, sem: {operator, labels, members}}` — on the predict
result (not payload-reachable).

A `fits` entry may carry `from_sweep {regression}` (`FitFromSweep`)
instead of `request` — `request` is therefore optional on the wire,
exactly one of the two — binding the rank-1 slot of the batch's ranked
`sweep`: the sweep body's regression whose `name` is `regression`,
refitted on the winning slot's rows as an ordinary regression fit. Its
static signature (type, target, predictors) is read from the sweep body
and must be the same in every sweep slot. A malformed binding is
`PULSE_FIT_INVALID` with `details.fit` and `details.reason`
`from_sweep_with_request`, `from_sweep_no_sweep`, `from_sweep_no_rank`,
`from_sweep_regression_unknown` (`details.regression`, `available`),
`from_sweep_signature_varies` or `from_sweep_cycle` (a sweep slot reads
the fit; both with `details.slot`, the 0-based sweep index), judged
before anything runs, identically by predict and the runtime. The key is
gated by `capability:compose_sweep_bind` (dependencies
`capability:compose_sweep` AND `capability:compose_fits`); predict's fit
summary echoes it as `from_sweep`.

### Compose optimize

`ComposedRequest.optimize` is an `omitempty` array of `OptimizeSpec
{name, variables, objective, direction, constraints, starts, rng_seed,
tolerance, max_iterations}`: a constrained optimisation solved once,
after the fits, with no record data. `variables` are `{name, lower,
upper, start}`, each bound an `OptimizeBound` — a number or
`"params.<name>"` naming a numeric batch param (`start` optional; the
midpoint when absent). `objective` and every `constraints[i].expr`
(type `eq`: expr = 0; `ineq`: expr >= 0) are expressions over the
variables, `params.<name>`, `coef()` over the batch's `fits` and the
built-ins; `direction` is `maximize` or `minimize` (required); `starts`
defaults to 8 (1 to 1000); `models` (gated by `capability:models`)
loads saved model files `{name, path}` for the block's `coef()`, above
the batch's fits. Predict and the runtime refuse a malformed
block identically before any fit or slot runs:
`PULSE_OPTIMIZE_SPEC_INVALID`, `PULSE_OPTIMIZE_VARIABLE_INVALID`,
`PULSE_OPTIMIZE_EXPRESSION_INVALID`, or the shared
`PULSE_PARAMS_UNDECLARED` / `PULSE_MODEL_*` coef() codes — each with
`details.optimize` (the block index) and `details.field`. Results land
in `ComposedResponse.optimizations`, an `omitempty` array of
`OptimizeResult` in declaration order — `name`, `direction`,
`objective`, `variables [{name, value, marginal, active}]`,
`constraints [{expr, type, value, multiplier}]`, `converged`, `status`,
`iterations`, `max_violation`, `starts`, `starts_agreeing`, `rng_seed`
— pinned outside the Compose-level `return` (always emitted, never
rounded); a block that failed on its numbers keeps `{name, direction,
error}` (see Failed slots) and warns `PULSE_SLOT_FAILED` (`slot_kind`
`optimizations`). Each block is solved from its start point plus
`starts − 1` seeded Latin-hypercube points with SLSQP and central
finite-difference gradients; `marginal` is ∂f/∂xᵢ, `multiplier` the
constraint's Lagrange multiplier in the objective's own direction
(∇f = Σ multiplier·∇expr on the free variables — for a budget `eq` it
equals every free variable's `marginal`), `starts_agreeing` the
converged starts within `10·tolerance·max(1, |f*|)` of the best. Both slots are gated by
`capability:compose_optimize`. `pulse_predict` with `composed` lists the
blocks as `optimize [{name, direction, variables, constraints, starts,
rng_seed}]` on the predict result (not payload-reachable).

### Model files (`save`)

`RegressionSpec.save` and `FitSpec.save` are `omitempty` strings: a path,
relative to the instance filesystem root, where the finished fit is
written as a model file (`model_format_version` 1, or 2 — adding the
`covariance` echo — when a robust covariance produced the fit's standard
errors, or 3 — adding the fit's `outputs`, its coefficients named by the
model — for an extension regression whose registration supplies a
scorer, or 4 — adding the fit's `dispersion` — for a classical `vcov`
fit that carries one, or 5 — adding the fit's `theta` — for a `REG_GLM`
`negative_binomial` fit, or 6 — `theta` and `dispersion` — for a fixed-θ
`negative_binomial` fit whose dispersion is estimated; `.pulse-model.json`;
the format is in `.claude/reference/byte-layout.md`, "Model file"). The
path must end `.pulse-model.json` and stay under the root
(`PULSE_MODEL_SAVE_INVALID` with `details.reason` and `details.regression`
or `details.fit`, refused by predict and the runtime alike before any
record is read); a failed write is `PULSE_MODEL_WRITE_FAILED`. Replacing a
file succeeds with the warning `PULSE_MODEL_OVERWRITTEN`: on the
`Response.warnings` of the request whose regression saved, or on
`ComposedResponse.warnings` — an `omitempty` array of `ResponseWarning`
for batch-level diagnostics, always emitted (a `warnings` keep path the
Compose-level `return` cannot exclude) — for a `fits` save
(`details.fit`). Both `save` keys are
gated by `capability:model_save`; the file itself is not payload-reachable,
so `format_version` stays `"1.1"`. Two saves naming one file in a request
or a Compose batch are refused (`reason: duplicate`, `details.sites`
naming both writers), and a batch judges every save path — fits and
slots — before any fit or slot runs (a slot's fault gains
`details.request`). Materialize and ProcessChain admit no regression, so
neither ever writes a model file. A VECTOR fit's `save` writes a
population file instead (`population_format_version` 1; the path must end
`.pulse-population.json`, else `PULSE_MODEL_SAVE_INVALID` reason
`suffix`): the battery's stored listwise state — labels, members, weight
basis, counts, Σw / Σw², raw mean and raw co-moment — with nothing
derived, written atomically under the fit's name with the same overwrite
warning (format: `.claude/reference/byte-layout.md`, "Population file"). A SEM fit's `save` writes a SEM fit file (`sem_format_version` 1, 2 for an ordinal fit, or 3 for a fit grouped by a computed column; the
path must end `.pulse-sem.json`, else `PULSE_MODEL_SAVE_INVALID` reason
`suffix`): the model description and options, member labels, the
model's variable orders, the solver stop and per group its level,
counts and parameter matrices at full precision, written atomically
under the fit's name with the same overwrite warning (format:
`.claude/reference/byte-layout.md`, "SEM fit file").

### Loading models (`models`)

`Request.models` and `FacetRequest.models` are `omitempty` arrays of
`ModelRef {name, path}`: each loads a model file a `save` wrote, under
`name`, into the namespace a Compose `fits` list binds, so
`ATTR_REG_PREDICT` `fit` and `coef()` resolve a loaded name exactly like
a fit (bit-for-bit the same scores). The path's suffix picks the kind: a
`.pulse-sem.json` path loads a SEM fit file as a SEM fit, which
`ATTR_SEM_SCORE` `params.fit` scores from bit-for-bit like the
in-compose fit that saved it (a file whose model does not rebuild its
stored variable orders is `PULSE_MODEL_FILE_INVALID`). Every file is read, version-checked
and decoded before any record is read — in a Compose batch or a chain,
before any fit, slot or stage runs, so a load always sees the file as it
was before the run. Refusals, predict and runtime alike:
`PULSE_MODEL_REF_INVALID` (no name, or a path the save-path rule
refuses; `details.model`, `reason`), `PULSE_MODEL_FILE_UNREADABLE`
(`missing` / `unreadable`), `PULSE_MODEL_FILE_INVALID` /
`PULSE_MODEL_VERSION_UNSUPPORTED` (with `details.model` and `name`),
`PULSE_MODEL_NAME_COLLISION` (a name a fit or an earlier entry binds;
`details.sources` names both), then the scoring rules
(`PULSE_MODEL_PREDICTOR_MISSING` / `_NOT_NUMERIC` against the scoring
frame). Provenance is informational: a model fit on one cohort scores any
cohort holding its predictor columns. A fit request's own `models` are
all it sees (fits run before the batch binds); each chain stage loads its
own. Gated by `capability:models`; `format_version` stays `"1.1"`.

### Loading populations (`populations`)

`Request.populations` is an `omitempty` array of `PopulationRef {name,
path}`: each loads a population file a vector fit's `save` wrote, under
`name`, as a vector fit in the same namespace, so a fitted vector
attribute's or co-moment `MAT_*` slot's `params.fit` resolves a loaded
name exactly like a Compose vector fit — bit-for-bit the same figures on
one architecture (a decomposition recomputed on another is
tolerance-equal), aligned to the slot's members by label. It loads at the
same point as `models` and is refused with the same codes, located by
`details.population`: `PULSE_MODEL_REF_INVALID` (no name, or a path not
ending `.pulse-population.json`), `PULSE_MODEL_FILE_UNREADABLE`,
`PULSE_MODEL_FILE_INVALID` / `PULSE_MODEL_VERSION_UNSUPPORTED`,
`PULSE_MODEL_NAME_COLLISION` (a name a `models` entry, an earlier entry
or a fit binds). Provenance is informational, never compared. Gated by
`capability:models`; `format_version` stays `"1.1"`.

**Registered populations.** An instance may also register populations
at `pulse.New` — `Options.Extensions.Populations` (the encoded file
bytes) and `Options.PopulationsDir` / `PULSE_POPULATIONS_DIR` (each
`*.pulse-population.json` under its file stem). They are bound beneath
EVERY request, so `params.fit` names one with no `populations` entry,
and the manifest lists them under `extensions.populations`
(`PopulationMeta {name, operator, basis, labels, n}`). An entry or a
Compose fit reusing a registered name is `PULSE_MODEL_NAME_COLLISION`,
its first source `{slot: "registered"}`; predict judges the same names
and labels.

### Item fan-out (`for_each`)

`Window`, `Feature` and `Attribute` each carry an optional `for_each`
(`ForEach {axes, mode}` — the sweep's `SweepAxis` list and `SweepMode`).
Before any other check the item is replaced IN PLACE by one ordinary
item per axis combination (grid: first axis slowest; zip: lockstep),
each rendered from the item's own body with the sweep substitution —
`{{axis}}` in any non-expression string (`label`, `field`; inside an
expression slot it is `PULSE_FOR_EACH_EXPR_SPLICE`), `{"$var":
"axis"}` as a whole value inside `params` (opaque JSON; a typed
non-string key cannot hold the marker) — and decoded strictly. Plain
items are untouched, so a list may mix both, and no response shape
changes: each expanded item produces its own column. The key is
`omitempty`, so a for_each-free request is byte-identical on the wire
and under `CanonicalHash`. It is gated by `capability:for_each`: on an
instance that hides it the `for_each` properties and the `ForEach` def
are absent (`SweepAxis` stays while a compose sweep is visible). A
malformed for_each is `PULSE_FOR_EACH_INVALID` with `details.field` (a
request-rooted path such as `windows[0].for_each.axes[1]`) and
`details.reason`; a `for_each` inside a sweep body is refused with
reason `nested_in_sweep`. `Limits.MaxDerivedColumns` (default 1,000)
bounds plain plus expanded window, feature and attribute items, checked
before any item renders. Predict reports each fan-out as `for_each
[{path, axes, mode, expanded_count}]` on the predict result (not
payload-reachable, so not in this schema).

## Undefined figures

A result figure can be undefined even when every input is present — a
ratio over an all-zero denominator, a confidence bound under two rows,
the first entry of an index-vs-prior series, a rolling window that has
not filled. The engine's Go results carry NaN there; JSON has no NaN,
so every JSON surface writes the figure as **`null` in place, key
kept** (`types.MarshalFinite`), and the rest of the response serialises
normally. `null` means "reported, undefined here"; an absent optional
key keeps meaning "not reported for this kind".

A figure over **no value** is undefined the same way: the mean, the
minimum / maximum / range, the variance / standard deviation, a
quantile, the mode, a shape moment or the average set cardinality of a
group, cell, margin or cohort with no non-null value (or, weighted,
whose weights sum to 0) is `null` on every execution arm — never `0`.
A count or a sum of nothing stays `0`. So is a figure whose formula is
0/0 on the data it has: the skewness or kurtosis of one value or of
values that never vary, the z-score where the spread is zero, and a
sample variance (`AGG_WELFORD`'s, a weighted `weighted_variance`)
below two values or a weight total of 1 — `null`, never `0`; a
population spread of one value stays a defined `0`. A series overlay
reads such a host value as a missing point: its entry keeps its key with
no `statistic` key at all, on the request, Compose and chain hosts
alike; a crosstab overlay reads such a cell as an absent cell — no
overlay cell, no warning, never in a denominator or a spread.

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

## Failed slots

A regression, test (tier-1 or post) or matrix slot whose data its
operator refuses — a separated or unconverged fit, a rank-deficient
design, too few rows, zero variance, a singular matrix — fails **that
slot alone**; every other slot of the response is returned as usual.
The failed result keeps its object (never `null` in the array: a
`null` entry still means the slot was skipped by `return`) with its
identity and an `error` — a `SlotError` `{code, message, details}`,
the refusal's own code, message and details, the shape of an envelope
error entry:

```json
{"regressions": [
  {"name": "default", "type": "REG_GLM",
   "error": {"code": "PROCESSING_REGRESSION_SEPARATION",
             "message": "…", "details": {"predictors": ["collections_flag"]}}},
  {"name": "ols", "type": "REG_OLS", "coefficients": {"…": 0.5}}
]}
```

Every numeric field of a failed result is **absent** (not `null`): a
`RegressionResult` writes `name`, `type` and `error`; a `TestResult`
`label`, `type`, `group_key` and `error`; a `MatrixResult` `name`,
`type`, `group_key`, `group_header` and `error`. The schema says so:
on `TestResult`, `RegressionResult` and `MatrixResult` the identity
keys (`name` / `type`) stay required and the figures that are always
written otherwise (`statistic`, `p_value`, `alpha`, `reject_null`;
`primary`) are required only when `error` is absent (`if` / `then`).

Each failed slot also adds a `PULSE_SLOT_FAILED` response warning,
details `{slot_kind, index, label, code}` (`slot_kind` the response
key, `index` the request slot index, `code` the slot's own code;
`group_key` for one bucket of a grouped matrix), which the `--json`
envelope lifts to its `warnings`; the run exits 0. Under
`Options.Strict` (`--strict`) the run instead fails with the slot's
own code. Only data-dependent refusals fail alone: a configuration
fault is still fatal for the whole request. A failed slot's `error` is
kept by every `return` preset and include list (like a nested
`warnings`); only an explicit exclude removes it.

A Compose `fits` entry whose fit fails on its data fails alone too: its
`ComposedResponse.fits[name]` entry carries `error` beside its identity
— `{name, type, error}` for a regression fit, `{"vector": {operator,
error}}` for a vector fit, `{"sem": {operator, error}}` for a SEM fit
(on `VectorFitResult` / `SEMFitResult` `operator` stays
required and the figures move under the same `if` / `then`) — its
`PULSE_SLOT_FAILED` warning lifts to the batch `warnings` with
`details.fit`, and every other fit and slot runs. An attribute reading
it — `ATTR_REG_PREDICT` scoring it, an `ATTR_FORMULA` naming it in a
literal `coef()` call, a fitted vector attribute's `params.fit`, a
`{population}` reference — is `null` on every row and adds a
`PULSE_FIT_UNAVAILABLE` warning `{fit, attribute, code}` to that slot's
response; a `MAT_*` slot's `params.fit` fails that slot alone (a
`matrices` entry `{name, type, error}` with the fit's own code and
`details.fit`, plus its `PULSE_SLOT_FAILED` warning). A
`FILTER_EXPRESSION` naming it in `coef()` (a filter's verdict cannot be
null) and a `coef()` whose fit name is not a literal are refused with
the fit's own code and `details.fit`. Under `Options.Strict` the batch
fails with the fit's own code. Predict
cannot foresee a data fault, so `pulse predict` never reports a fit as
failing.

Additive: a response with no failed slot is byte-identical, and
`format_version` stays `"1.1"`.

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
auxiliary figure. The cell's own `row_margin_components` beside them
follow the same axis rule (a record reaches any margin only if it reached
a cell, so a null or empty set or an excluded key is in no margin and no
normalize denominator) but still count a null cell field as `n_null`.
Nothing in the schema distinguishes the two, and the
consequence is silent: a consumer reading an auxiliary's `components.n`
as a cohort count gets a well-formed, plausible, wrong number. See
`skills/crosstab-guide.md` and `skills/response-components.md`.
