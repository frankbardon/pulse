# Adding a Feature Operator

> **Embedding Pulse, not contributing to it?** This recipe adds a *built-in* in `internal/processing/`, which embedders cannot import. To add your own operator, implement the matching contract in the public `extend` package instead — see [Extension Points](extension-points.md).

**Audience:** Pulse internals contributors adding a new `FEAT_*`
operator — a pre-filter feature engineer that runs before the
aggregation / window pass and emits one or more derived columns
(`FEAT_LOG`, `FEAT_SQRT`, `FEAT_BUCKETIZE`, …).

The recipe mirrors the aggregator recipe; the feature-specific moving
parts are the `StreamingComputer` (`internal/processing/feature`; embedders implement `extend.StreamingFeatureComputer`) interface, the output-label
emitter, and the predict-side label projection.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllFeatureTypes()`:

```go
const (
    // ... existing constants ...
    FEAT_BOX_COX FeatureType = "FEAT_BOX_COX"
)

func AllFeatureTypes() []FeatureType {
    return []FeatureType{
        // ... existing entries, alphabetised ...
        FEAT_BOX_COX,
    }
}
```

## 2. Implement in `internal/processing/feature/`

Each feature operator lives in `internal/processing/feature/<name>.go`.
Register via the package's `init()` calling
`register(types.FEAT_X, newX)`.

If the operator is streaming-eligible, implement the
`feature.StreamingComputer` interface — a three-method shape:

- `PrePass(rows)` — accumulate any whole-cohort statistics needed
  (mean, stddev) on a first pass.
- `Finalize()` — close out the pre-pass, compute coefficients.
- `EmitRow(row)` — emit the per-row feature value(s) on the second
  pass.

Operators without whole-cohort statistics skip `PrePass` and run as
single-pass row transforms.

## 3. Tests

Write tests in `internal/processing/feature/<name>_test.go` before the
implementation. Cover the empty-input, single-row, null-bearing, and
boundary cases.

## 4. Capability declaration

Add a row to `internal/descriptor/capabilities_features.go` with the operator's
params, accepted field types, and any emit shape.
`TestManifestOperatorsComplete` enforces a row per registered feature.

## 5. Predict-side label projection

Update `internal/descriptor/predict_feature.go`:

- Validate the operator's params (raise the appropriate
  `PROCESSING_CONFIG` / `SERVICE_VALIDATION` error code on invalid input).
- Emit the operator's output column labels in `featureOutputLabels`
  so predict can show the LLM client what columns the request will
  materialise. `TestPredict_Feature` enforces parity.

## 6. Update the feature-engineering skill

Add a section in `skills/feature-engineering.md` covering the
operator's params and output column naming convention. The
`TestSkillsCoverAllComponents` gate enforces presence by name.

## 7. Update CLAUDE.md

There is **no registered-feature count or list in CLAUDE.md to update** —
CLAUDE.md never hardcodes registered counts; the manifest is the source of
truth. The Update Demand operator row (`.claude/reference/update-demand.md`)
requires instead the atomic skill `skills/op-feat-<kebab>.md`, the capability declaration in
`internal/descriptor/capabilities_features.go` — and an `internal/examples/<dir>/*.json` example whose `_meta.operators`
names the operator (`TestEveryOperatorHasAnExampleTag`). Edit CLAUDE.md only if the
operator introduces a contract it states directly, and mind
`TestClaudeMdSizeBudget` — long-form prose belongs in `.claude/reference/`.

## 8. Run the gates

```bash
go test ./internal/skills/ -run TestSkillsCoverAllComponents
go test ./internal/descriptor/ -run 'TestManifestOperatorsComplete|TestPredict_Feature'
go test ./internal/processing/feature/...
```

The Update Demand row for feature operators covers all of these in
one PR; see [The Update Demand](update-demand.md).
