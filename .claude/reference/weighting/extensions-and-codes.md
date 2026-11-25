# Weighting — Extension contract, SPSS suggestion, Feature profile, Error codes

Slice of `.claude/reference/weighting.md` — load the index first, then only the slice(s) your change touches.

## Extension contract

Migration notes: `docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md`.

- `WeightAware bool` on the aggregator, attribute, test (tier 1 and 2), matrix-op and overlay registrations; projected as the manifest extension entry's `weight_aware` (omitempty). Filterers, groupers, windows and features have no declaration: a weight never reaches them (`skipped_not_weight_aware`). An aware matrix op's slot folds the weight into the engine's co-moment; the kind is on `extend.MatrixInput.WeightKind`.
- `extend.Record.Weight() (float64, bool)` — the engine-resolved weight for the slot; only VALID weights are reported (`weighting.Classify`); `ok == false` ⇒ no weight in force or the operator is not aware. The engine's own row always answers `(0, false)` — slot weights differ within one request — so the root adapter hands an aware slot a per-slot `weightedRecord` view (`extensions_adapt.go`). **A row whose weight is invalid never reaches an aware aggregator or tier-1 row test; an aware attribute owes every row a value, so it still receives the row, with `Weight()` false** (decision, E4-S1). `processing.StampWeightsWith` stamps aware extension test / attribute slots; `NewWeightRowTally` counts them, so an extension slot alone still raises `PULSE_WEIGHT_INVALID_ROWS`.
- Classing (`ResolveWeights`, off `ExtensionsSnapshot.WeightAwareness`, so predict ≡ runtime): aware ⇒ `applied`, including over `decimal128` (the decimal refusal belongs to the built-in decimal path; an extension owns its weighted form). Non-aware aggregator: explicit weight ⇒ `PULSE_EXTENSION_NOT_WEIGHT_AWARE` `{slot, operator, field}`; instance default ⇒ skipped. Non-aware attribute, test, matrix op or overlay kind: refused under ANY weight in force, the default included (it may read the whole population; an overlay's host is weighted whatever it declares, so there is no skip). A slot-only host weight does not refuse an overlay. `weight: null` opts out. A hidden extension is never-registered.
- **Inferential decision.** A WeightAware extension TEST is applied, not refused: the registration declared it computes a weighted result. There is no extension `REG_*` category.
- **No kind dimension (decision).** No `weight_kinds` on `OperatorMeta` and no kinds field on registrations: an aware extension is applied under BOTH kinds. The kind rides the stamped slot spec (`spec.Weight`), so an extension with no probability form must check it and refuse itself. A kinds declaration would be an additive change, left for demand.
- Floor keys: the orchestrator stamps them on an aware aggregator's slot in every mode; the extension never emits or declares them.
- Probe: an aware aggregator / attribute / test factory is constructed with and without a weight (`probeWeight`) and both pass the same checks; a failure of the weighted one carries `details.weighted: true` under the existing probe codes. Tests are probed only when they declare WeightAware — probing every test would refuse registrations that worked.

Gates: root `TestExtensionWeight_*` (`internal/integration/extensions_weight_test.go`), `TestResolveWeights_ExtensionClasses`, `TestStampWeightsWith_ExtensionAwareness`, `TestExtensions_OverlayWeightRefusalMatchesPredict`, the smoke module's `TestExtendWeightAwareAggregator` (`make smoke`). Recipe: `docs/src/internals/extension-points.md`; engine wiring: `architecture.md` (Extension surface).

## SPSS suggestion

Inspect reads the SPSS metadata sidecar (header-only — never records) and surfaces `suggested_weight {field, source: "spss_sidecar", kind: "probability"}` (omitempty). Predict echoes it as DATA when no weight resolves, plus the advisory `PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED` — never a warning, so `--strict` stays safe. A stale sidecar yields no suggestion, silently. A suggested weight is **never** auto-applied, including via `DefaultWeight`; there is no manifest-level suggestion. SPSS `WEIGHT BY` is frequency-like while the default `kind` is `probability` — say so wherever the suggestion is documented. Wiring, silent-failure list and gates: `predict-inspect.md` (Suggested weight).

## Feature profile

`capability:weighting` is a request-slot capability (`features.go`, `requestSlotCapabilities`). Hidden:

- the request-root `weight` and EVERY per-slot `weight` (inside Compose requests and chain stages too) are `PULSE_REQUEST_UNKNOWN_FIELD`, an explicit `null` included; a nested one carries `details.path` and `valid_keys` (`nestedWeightRefusal`, `internal/descriptor/request_slots.go`);
- payload schema, MCP tool schemas, manifest `weight_aware` / `weight_kinds` / weighted floor keys (and every operator's own weighted-slot-only key, `WeightedSlotOnlyKey`), predict `weights` and `suggested_weight` are absent;
- `Options.DefaultWeight` fails `pulse.New` with `PULSE_FEATURE_PROFILE_DEPENDENCY` — refused rather than ignored, because an ignored default would silently change every figure the instance promised to weight;
- `AGG_WEIGHTED_MEAN.params.weight_field` stays ungated (deliberate asymmetry, `TestHiddenWeighting_WeightedMeanParamUngated`): it is the operator's own parameter.

Contract: `feature-profiles.md` (Request-slot gate, Manifest hiding, Predict hiding, Error hiding).

## Error codes

Each needs a `codeMetadata` entry and an `error_owners.go` owner (`PULSE_WEIGHT_INVALID_ROWS` and `PULSE_WEIGHT_UNSUPPORTED` → `capability:weighting` + `AGG_WEIGHTED_MEAN`, whose ungated `weight_field` reaches both; the rest → `capability:weighting`):

- `PULSE_WEIGHT_INVALID_ROWS` — warning; excluded rows with the by-reason breakdown.
- `PULSE_WEIGHT_LOW_NEFF` — warning (strict: error); a probability-weighted test group's n_eff below its raw-n floor, or a regression's n_eff below p + 1.
- `PULSE_WEIGHT_UNSUPPORTED` — a refused surface under a weight (frequency-only under probability, permanent, pending, decimal128); fixups name `weight: null`, `kind: frequency` and the alternative. Its Message names no operator (`TestErrorMessagesNameOnlyOwners`).
- `PULSE_EXTENSION_NOT_WEIGHT_AWARE` — explicit weight on a non-aware extension aggregator, or any weight on a non-aware extension attribute, test, matrix op or overlay.
- `PROCESSING_CONFIG` — bad weight field / type, explicit weight on a not-weightable operator, `weight_field` conflict or nothing resolving.
