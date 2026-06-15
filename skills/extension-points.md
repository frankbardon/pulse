---
name: extension-points
description: Register domain-specific operators (custom AGG_*, ATTR_*, FILTER_*, GROUP_*, WIN_*, FEAT_*, TEST_*, SYNTH_*) plus expression functions and lookup tables via pulse.Options.Extensions. Use when embedding Pulse in another Go binary and you need to inject domain logic without forking the engine.
type: guide
applies_to: process, predict, manifest
---

# Extension Points

Pulse exposes a public registration API so embedders can inject domain-specific operators and expression-runtime extensions at `pulse.New()` time. Registered extensions are first-class participants in Predict / Inspect / Process / Compose / Manifest.

The API is Go-native. There is no plugin loader, no `.so` files, no hot reload. Registration happens once when the embedding binary constructs its Pulse instance; restart to change the registration set.

## When to register vs use a built-in

Use the **built-in** registry when:
- The operator semantics fit one of the 18 aggregators / 9 attributes / 6 filterers / 6 groupers / 10 windows / 9 features / 20 tests Pulse already ships.
- The behaviour you need can be expressed via `ATTR_FORMULA` + an expression — register a custom `ExprFunction` instead of a full operator.

Use a **registered extension** when:
- The operator encodes proprietary business logic that should not live in Pulse core (e.g. a domain-specific composite score).
- You need a closed-form function call from within `ATTR_FORMULA` / `FILTER_EXPRESSION` (e.g. `rank_familiarity(value, total_pop)`).
- You have a static keyed lookup table that drives multipliers / calibration factors (e.g. per-(study, wave) adjustment).
- You need the manifest + MCP schema-bound tools to advertise your operator alongside Pulse built-ins.

## Naming convention

Embedder registrations MUST use a three-segment namespaced form:

```
<CATEGORY>_<NAMESPACE>_<NAME>
```

Pattern: `^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_[A-Z][A-Z0-9]+_[A-Z](?:[A-Z0-9_]*[A-Z0-9])?$`

| Example | Category | Namespace | Name |
|---|---|---|---|
| `AGG_ACME_BRAND_SCORE` | aggregator | ACME | BRAND_SCORE |
| `ATTR_ACME_ADJUSTMENT` | attribute | ACME | ADJUSTMENT |
| `FILTER_ACME_GEO_FENCE` | filterer | ACME | GEO_FENCE |
| `TEST_FINANCE_VAR` | test | FINANCE | VAR |

Validation at registration time:
- Names that fail the regex return `PULSE_EXTENSION_NAME_INVALID`.
- Names whose namespace is `BUILTIN`, `STANDARD`, `CORE`, or `PULSE` return `PULSE_EXTENSION_NAME_RESERVED`.
- Names that collide with a built-in (e.g. registering `AGG_COUNT`) return `PULSE_EXTENSION_NAME_COLLISION`.
- The same name registered twice in one `pulse.New` call returns `PULSE_EXTENSION_DUPLICATE`.

Pick a short namespace that matches your module / company. Two embedders in the same process must use disjoint namespaces; otherwise registration fails on the second instance.

## The Extensions struct

```go
import "github.com/frankbardon/pulse"

ext := pulse.Extensions{
    Aggregators:        []pulse.AggregatorRegistration{...},
    Attributes:         []pulse.AttributeRegistration{...},
    Filterers:          []pulse.FiltererRegistration{...},
    Groupers:           []pulse.GrouperRegistration{...},
    Windows:            []pulse.WindowRegistration{...},
    Features:           []pulse.FeatureRegistration{...},
    Tests:              []pulse.TestRegistration{...},
    SynthDistributions: []pulse.DistributionRegistration{...},

    ExprFunctions: []pulse.ExprFunction{...},
    LookupTables:  map[string]pulse.LookupTable{...},
}

p, err := pulse.New(pulse.Options{
    FS:         myFs,
    Extensions: ext,
})
```

Zero-value `Extensions` is the no-op case — pulse.New behaves exactly as the pure-Pulse binary.

## Per-category registration shapes

### Aggregator

```go
{
    Name:        "AGG_ACME_BRAND_SCORE",
    Description: "ACME brand composite (0-100).",
    Factory:     bera.NewBrandScoreAggregator,    // processing.AggregatorFactory
    Streamable:  true,                            // factory MUST return OnlineAggregator
    Accepts:     []encoding.FieldType{encoding.FieldTypeF64},
    Params:      []pulse.ParamMeta{{Name: "weights", JSONType: "array"}},

    // Components contract (E4-S5) — optional sibling-interface emitter:
    ComponentSchema: descriptor.ComponentSchema{
        Keys: []descriptor.ComponentKey{
            {Name: "n", Type: "int", Description: "Records aggregated (universal floor)."},
            {Name: "n_null", Type: "int", Description: "Null inputs (universal floor)."},
            {Name: "sum", Type: "float64", Description: "Running sum of scores."},
            {Name: "weights_applied", Type: "int", Description: "Weight multipliers fired."},
        },
        Mergeability: descriptor.Mergeable,
    },
    ComponentsFunc: func(instance processing.Aggregator) (map[string]any, error) {
        a := instance.(*brandScoreAggregator)
        return map[string]any{"sum": a.Sum(), "weights_applied": a.WeightsApplied()}, nil
    },
}
```

When `Streamable=true`, the probe at `pulse.New` time asserts the factory's returned value implements `processing.OnlineAggregator`. Mismatch returns `PULSE_EXTENSION_STREAMABLE_MISMATCH`.

**ComponentSchema + ComponentsFunc** is the embedder-facing path for the per-operator `Response.Components` contract. Three valid shapes:

- **Floor-only** — leave `ComponentSchema` zero and `ComponentsFunc` nil. The orchestrator emits the universal floor (`{"n", "n_null"}`) and no extra keys.
- **Schema + emitter** — declare a non-empty `ComponentSchema` with `Mergeability` set and supply `ComponentsFunc`. The runtime wraps the factory so `instance.(processing.MetaAggregator)` succeeds; the orchestrator routes the emitted map onto `Response.Components.Aggregations[i].Operator` after Aggregate / Finalize. Emitted keys MUST be a subset of `ComponentSchema.Keys` — probe-validation rejects extras at `pulse.New` time.
- **Emitter without schema** OR **schema without emitter** — rejected (the schema would never be satisfied, or the emitter would never be validated). E4-S6 introduces the formal `PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH` / `PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA` error codes; until then both surface as `PULSE_EXTENSION_PARAM_INVALID`.

Universal-floor keys are filled by the orchestrator unconditionally — do NOT re-emit `n` / `n_null` from `ComponentsFunc`. Return `(nil, nil)` to signal "no operator-specific keys; universal floor IS the entire payload" (the canonical AGG_COUNT shape).

### Attribute

```go
{
    Name:        "ATTR_ACME_ADJUSTMENT",
    Description: "Per-(study, wave) multiplier.",
    Factory:     newAdjustmentAttribute,            // processing.AttributeFactory
    Mode:        pulse.AttributeModeRowLocal,        // row_local | two_pass | buffered
    Accepts:     []encoding.FieldType{encoding.FieldTypeF64},
    Emits:       pulse.AttributeEmitFloat64,
}
```

Mode drives streaming-tier validation:
- `row_local` — factory's return value must implement `processing.RowLocalAttribute` (per-row no-PrePass).
- `two_pass` — must implement `processing.TwoPassAttribute` (Welford-Pébaÿ style).
- `buffered` — only `processing.AttributeComputer` is required.

### Filterer / Grouper / Window / Feature

All four follow the same envelope shape: name, description, factory, accepted types, params metadata. Filterers and windows currently have no streamable toggle — filterers are always row-local streamable, windows always run buffered.

`FiltererRegistration` and `GrouperRegistration` also carry `ComponentSchema` + `ComponentsFunc` on the same contract as `AggregatorRegistration`. The runtime wraps the factory so the orchestrator's `instance.(processing.MetaFilterer)` / `.MetaGrouper` assertion succeeds; the emitted map routes onto `Response.Components.Filterers[i].Operator` / `.Groupers[i].Operator`. Universal floors differ by category:

- **Grouper** floor: `{total_n, n_null}` — record counts post-filter; `ComponentsFunc` returns the operator-specific extras (bucket arrays, edges, dictionary mappings).
- **Filterer** floor: `{n_in, n_out, n_null_input}` — three-counter triple from the filter pass. v1 ships every built-in filterer as floor-only; embedders may opt in by declaring a schema + emitter for operator-specific extras (e.g. per-value `n` for FILTER_INCLUDE).

Floor-only registrations (no `ComponentSchema`, no `ComponentsFunc`) stay valid — the orchestrator emits just the universal floor.

### Test (tier-1 / tier-2)

```go
// Tier-1 (folds during streaming aggregation pass):
{
    Name:       "TEST_ACME_PROXY",
    Tier:       pulse.TestTierRow,
    RowFactory: newProxyRowTest,                // processing.RowTestFactory
    Streamable: true,
}

// Tier-2 (runs over materialised result rows after windows):
{
    Name:        "TEST_ACME_AGGREGATE_CHECK",
    Tier:        pulse.TestTierPost,
    PostFactory: newAggregateCheckPostTest,     // processing.PostTestFactory
}
```

Exactly one of `RowFactory` / `PostFactory` must be non-nil, matching `Tier`. Tier-2 tests are always buffered at runtime; `Streamable` on a tier-2 registration is ignored.

### Synth distribution

Reserved for embedders shipping bespoke samplers. The factory shape finalises alongside the synth distribution overlay phase; until then the registration validates name + duplicates and reserves the namespace.

## Expression functions

Custom Go functions become callable from `ATTR_FORMULA` and `FILTER_EXPRESSION`:

```go
{
    Name:        "rank_familiarity",
    Description: "BERA brand familiarity rank.",
    Signature:   "rank_familiarity(value float64, total_pop bool) float64",
    Fn:          bera.RankFamiliarity,
    Pure:        true,        // declares side-effect-free; reserved for future memoisation
}
```

Pulse passes the `Fn` value to expr-lang's `expr.Function(name, fn)`. expr-lang accepts typed functions via reflection — `func(v float64) float64` and `func(args ...any) (any, error)` both work. Use the canonical `func(args ...any) (any, error)` shape when zero-allocation calling matters.

## Lookup tables

Static keyed tables exposed via the built-in `lookup(table, keys...)` function:

```go
LookupTables: map[string]pulse.LookupTable{
    "adjustments": {
        Description: "Per-(study, wave-date) calibration multipliers.",
        // Rows is the simple path — caller-joined composite key.
        Rows: map[string]float64{
            "study_a|2025-01-01": 1.07,
            "study_a|2025-02-01": 1.12,
        },
        // OR — Lookup is the escape hatch:
        // Lookup: func(keys ...string) (float64, bool, error) { ... }
    },
},
```

Exactly one of `Rows` / `Lookup` must be non-nil. Validation at `pulse.New` returns `PULSE_EXTENSION_PARAM_INVALID` otherwise.

The `lookup()` built-in is available in any expression once at least one table is registered. Inside `ATTR_FORMULA` / `FILTER_EXPRESSION`:

```text
score * lookup("adjustments", study, wave_date)
```

`Rows`-backed tables join the keys with `|` before indexing. The `Lookup` function-backed path receives the keys as a `[]string` slice — embedders compose keys however they want, perform partial-match fallback, or pull from an external store.

At evaluation time:
- Unregistered table → `PULSE_LOOKUP_TABLE_UNKNOWN`.
- Missing key → `PULSE_LOOKUP_MISS`.

Both wrap into `PROCESSING_RUNTIME` when surfaced through `ATTR_FORMULA` / `FILTER_EXPRESSION` — use `errors.HasCode(err, errors.PULSE_LOOKUP_MISS)` to detect inside the chain.

## Streamability contract

Embedders declare streamability at registration time; the runtime trusts that declaration. Probe-validation at `pulse.New` catches obvious mismatches (e.g. `Streamable=true` with a buffered-only factory).

| Category | Streamable means | Required interface |
|---|---|---|
| Aggregator | one-pass online | `processing.OnlineAggregator` |
| Attribute (row_local) | per-row eval, no PrePass | `processing.RowLocalAttribute` |
| Attribute (two_pass) | PrePass + Finalize + Row | `processing.TwoPassAttribute` |
| Grouper | derive key from a single row | `processing.StreamingGrouper` |
| Feature | StreamingComputer pipeline | `feature.StreamingComputer` |
| Test (tier-1) | folds with online aggregators | `processing.RowTest` |

Filterers are always row-local streamable today. Windows always run buffered.

## FieldInputs hook (buffered-projection introspection)

Every operator registration accepts an optional `FieldInputs FieldInputsFunc` callback:

```go
type FieldInputsFunc func(raw json.RawMessage) []string
```

When `pulse.Options.ProjectBufferedFields` is enabled, the runtime walks each request before opening the streaming iterator and calls `processing.NeededFields(req, schema, ext)` to compute the set of source fields that the operators actually read. Built-in operators are fully introspectable from their spec (`Field`, `Field2`, `PartitionBy`, `OrderBy`, `Target`, `Predictors`, plus expr-AST identifiers for `ATTR_FORMULA` / `FILTER_EXPRESSION`). Custom operators registered through this surface are opaque by default — the projection extractor widens to "every field" whenever it encounters a custom operator without a `FieldInputs` hook, so the runtime stays correct.

To opt into projection on a custom operator, register `FieldInputs` and return every schema field the operator reads beyond its spec's explicit references:

```go
pulse.AggregatorRegistration{
    Name:    "AGG_ACME_BLENDED_SCORE",
    Factory: blendedScoreFactory,
    Params:  []pulse.ParamMeta{{Name: "weight_field", JSONType: "string"}},
    // Spec.Field carries the value field; weight_field names a second
    // numeric field. Both must be in the projected map for the
    // aggregator to compute correctly.
    FieldInputs: func(raw json.RawMessage) []string {
        var p struct {
            WeightField string `json:"weight_field"`
        }
        _ = json.Unmarshal(raw, &p)
        if p.WeightField == "" {
            return nil
        }
        return []string{p.WeightField}
    },
}
```

Return value semantics:
- `nil` / empty slice: no extra fields beyond the spec's `Field` (the operator reads only what the runtime can already infer).
- Names not present in the schema are silently dropped by `NeededFields` — return-what-you-read; the extractor filters against the live schema.
- Errors aren't part of the signature on purpose — `FieldInputs` runs on the hot path and should be allocation-free. Anything that needs decoding belongs in the factory.

For filterers, the callback receives `nil` as `raw` (filterers don't carry a Params block today). Return the static set of extra fields the filterer reads beyond `Filterer.Field`.

Tier-2 post-tests don't decode source records (they consume the materialized result row set after windows). Leave `FieldInputs` nil on tier-2 registrations.

The hook is plumbed via `buildRuntimeExtensions` into `processing.ExtensionRegistry.FieldInputs`, keyed by `StreamabilityKey(category, name)`. `ExtensionRegistry.FieldInputsFor(category, name, raw)` returns `(inputs, true)` when the callback ran successfully, `(nil, false)` when the operator is custom but has no registered callback — that second case is what triggers the extractor to widen.

Interaction with the decode plan. The retained set `NeededFields` returns feeds `Schema.BuildDecodePlan` (see `skills/cohort-schema-design.md` "Decode plan and projection"). A registration **with** `FieldInputs` participates normally — its contributed fields land in the retained set and the plan emits `SkipBytes` segments for every contiguous unprojected run, so unread byte ranges are advanced with a single `Seek`. A registration **without** `FieldInputs` widens the retained set to `*` (every schema field), which produces a full-coverage plan with **no** `SkipBytes` segments — equivalent to the unprojected per-field walk. Both shapes are correct; only the plan-driven one elides byte ranges. If your operator only ever reads `Spec.Field` and nothing else (the common case), the spec's explicit references already participate without a callback — register `FieldInputs` only when the operator reads additional fields named through `Params`.

## Manifest discoverability

`pulse manifest --json` and `pulse_manifest` include a top-level `Extensions` block whenever the host registered anything:

```json
{
  "format_version": "1.0",
  "components": { ... built-ins ... },
  "extensions": {
    "aggregators": [
      {"name": "AGG_ACME_BRAND_SCORE", "namespace": "ACME", "streamable": true, ...}
    ],
    "expr_functions": [
      {"name": "rank_familiarity", "signature": "rank_familiarity(value float64, total_pop bool) float64"}
    ],
    "lookup_tables": [
      {"name": "adjustments", "has_rows_data": true}
    ]
  }
}
```

LLM agents that call `pulse_manifest` see both the built-in set and the embedder additions in one fetch. The schema-bound MCP tools (after `pulse_inspect`) also include custom operator names in their enum lists.

## Migration recipe — embedder pre-processing → registration

Before extensions, the canonical pattern was for an embedder to rewrite the request before submitting it:

```go
// Old: rewrite "adjustment" attribute into a formula with the
// multiplier inlined.
req.Attributes = append(req.Attributes, &types.Attribute{
    Type:       types.ATTR_FORMULA,
    Field:      "score",
    Expression: fmt.Sprintf("score * %f", adjustmentFor(study, wave)),
})
```

With the extension API the request stays domain-named and the engine resolves the value at runtime:

```go
// New: register the lookup once at startup.
pulse.New(pulse.Options{
    Extensions: pulse.Extensions{
        LookupTables: map[string]pulse.LookupTable{
            "adjustments": {Lookup: orbit.LookupAdjustment},
        },
    },
})

// Request stays declarative:
req.Attributes = append(req.Attributes, &types.Attribute{
    Type:       types.ATTR_FORMULA,
    Field:      "score",
    Expression: "score * lookup(\"adjustments\", study, wave_date)",
})
```

Benefits: the manifest advertises `adjustments`, the schema-bound MCP tool surfaces it, predict can typecheck the expression, and the lookup runs without pre-processing every request.

## Error codes added by this surface

| Code | Trigger |
|---|---|
| `PULSE_EXTENSION_NAME_INVALID` | name fails the registration regex |
| `PULSE_EXTENSION_NAME_RESERVED` | namespace is BUILTIN/STANDARD/CORE/PULSE |
| `PULSE_EXTENSION_NAME_COLLISION` | name matches a built-in |
| `PULSE_EXTENSION_DUPLICATE` | same name registered twice |
| `PULSE_EXTENSION_STREAMABLE_MISMATCH` | declared streaming tier ≠ factory interface |
| `PULSE_EXTENSION_FACTORY_PANIC` | factory panicked / returned nil during probe |
| `PULSE_EXTENSION_PARAM_INVALID` | bad ParamMeta, missing Mode/Tier, lookup table with neither Rows nor Lookup, etc. |
| `PULSE_LOOKUP_TABLE_UNKNOWN` | expression referenced an unregistered table |
| `PULSE_LOOKUP_MISS` | lookup key not present |

Fetch the per-code Message + Fixup template via `pulse_errors_lookup` (MCP) or `pulse errors lookup CODE` (CLI).
