# Payload JSON Schema

Pulse publishes a single machine-readable **JSON Schema** (draft 2020-12)
describing every public payload — the request envelopes and the universal
`--json` output envelope. Use it to validate requests before sending them,
to generate client types, or to drive editor autocompletion.

## Where to get it

The schema is reachable three ways, all backed by the same generator
(`descriptor.BuildPayloadSchema`):

| Surface | How |
|---|---|
| **Docs URL** | <https://frankbardon.github.io/pulse/payload-schema.json> — the file's own `$id`. |
| **CLI** | `pulse schema` prints it to stdout (offline; no cohort needed). |
| **MCP resource** | Read `pulse://schema` (MIME `application/json`) alongside `pulse_manifest`. |

The CLI and MCP surfaces emit byte-identical output to the published file.

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
