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
  `overlays`) is not a property of its request root;
- a root whose capability is not offered is absent — `ComposedRequest` /
  `ComposedResponse` (compose), `ChainRequest` / `ChainResponse`
  (process-chain), `FacetRequest` / `FacetResult` (facet),
  `SampleRequest` (sample), `LookupRequest` / `LookupResult` (lookup);
- every def reachable only through an omitted part is dropped, so the
  document stays a valid draft 2020-12 schema with no dangling `$ref`.

`Request`, `Response` and `Envelope` are always present. `pulse schema`
serves the CLI's default instance; the `pulse://schema` MCP resource
serves the mounted instance's schema (`p.PayloadSchema()`).

## Structure

The document is a `$defs` bundle. The root `oneOf` lists the entry points:

- **Requests** — `#/$defs/Request` (process / predict), `ComposedRequest`
  (compose), `ChainRequest` (process-chain), `FacetRequest`, `SampleRequest`,
  `LookupRequest` (point lookup).
- **Results** — `#/$defs/Response`, `ComposedResponse`, `ChainResponse`,
  `FacetResult`, `LookupResult`.
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
  `DefaultTimeZone` → `UTC`, and a resolved non-UTC zone reaching a
  `datetime` field is currently refused with `PROCESSING_CONFIG`
  (zone-aware operator arithmetic has not landed).

The resolved zone per slot is echoed by predict as
`data.time_zones[]` — `{slot, operator, field_type, tz, source}` —
which is a predict result field, not part of this schema. See
`skills/request-envelope.md` (Time zones).

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
