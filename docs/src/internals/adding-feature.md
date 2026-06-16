# Adding a Feature Operator

**Audience:** Pulse internals contributors adding a new `FEAT_*`
operator — a pre-filter feature engineer that runs before the
aggregation / window pass and emits one or more derived columns
(`FEAT_LOG`, `FEAT_SQRT`, `FEAT_BUCKETIZE`, …).

The recipe mirrors the aggregator recipe; the feature-specific moving
parts are the `feature.StreamingComputer` interface, the output-label
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

## 2. Implement in `processing/feature/`

Each feature operator lives in `processing/feature/<name>.go`.
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

Write tests in `processing/feature/<name>_test.go` before the
implementation. Cover the empty-input, single-row, null-bearing, and
boundary cases.

## 4. Capability declaration

Add a row to `descriptor/capabilities_features.go` with the operator's
params, accepted field types, and any emit shape.
`TestManifestOperatorsComplete` enforces a row per registered feature.

## 5. Predict-side label projection

Update `descriptor/predict_feature.go`:

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

Bump the registered-feature count in CLAUDE.md's "Skill Pack" section.

## 8. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllComponents
go test ./descriptor/ -run 'TestManifestOperatorsComplete|TestPredict_Feature'
go test ./processing/feature/...
```

The Update Demand row for feature operators covers all of these in
one PR; see [The Update Demand](update-demand.md).
