# Adding a Feature Operator

> **Run the site checklist first.** `make sites ARGS="new-op feature"` prints every file and declaration a new `FEAT_*` lands in — derived from where the existing ones appear — with the tests to rerun and the goldens to regenerate; `make sites ARGS="op <NAME>"` does the same for an existing one. See [Finding Change Sites](finding-change-sites.md). This recipe explains each step.

> **Embedding Pulse, not contributing to it?** This recipe adds a *built-in* in `internal/processing/`, which embedders cannot import. To add your own operator, implement the matching contract in the public `extend` package instead — see [Extension Points](extension-points.md).

**Audience:** Pulse internals contributors adding a new `FEAT_*`
operator — a pre-filter feature engineer that runs before the
aggregation / window pass and emits one or more derived columns
(`FEAT_LOG`, `FEAT_SQRT`, `FEAT_BUCKETIZE`, …).

The recipe mirrors the [aggregator recipe](adding-aggregator.md); the
feature-specific parts are the `feature.Computer` /
`feature.StreamingComputer` interfaces (`internal/processing/feature`;
embedders implement `extend.StreamingFeatureComputer`) and the one
output-label rule predict and the runtime share.

## 1. Declare the type constant

Add the new constant to `types/types.go` and append it to
`types.AllFeatureTypes()`; classify it in `FeatureType.Streamable()`
(`types/streamability.go`) and add the `types/streamability_test.go` row.

```go
const (
    // ... existing constants ...
    FEAT_BOX_COX FeatureType = "FEAT_BOX_COX"
)
```

## 2. Implement in `internal/processing/feature/`

Each feature operator lives in `internal/processing/feature/<name>.go`
and registers from the file's `init()` with
`register(types.FEAT_BOX_COX, newBoxCox)`. The factory returns a
`Computer`: `Compute(records, field)` returns one `Output` per derived
column (values plus a null mask), keyed by column name. That is the
buffered path.

To run on the streaming path, also implement `StreamingComputer`:

- `PrePass(record, field)` — called once per record on pass 1;
  accumulate any whole-cohort statistics (mean, standard deviation).
- `Finalize()` — close out the pre-pass and compute coefficients.
- `EmitRow(record, field)` — called once per record on pass 2; return
  that record's derived values.

Stateless per-row operators (`FEAT_LOG`, `FEAT_SQRT`) implement
`PrePass` and `Finalize` as no-ops. An operator without
`StreamingComputer` forces a buffered fallback.

## 3. Tests

Write tests in `internal/processing/feature/<name>_test.go` before the
implementation (empty input, single row, nulls, boundaries) and add the
operator to `streaming_parity_test.go` when it streams.

## 4. Output labels

Add the operator's output column names to
`vectors.FeatureOutputLabels` (`internal/vectors/computed.go`): predict
(`internal/descriptor/predict_feature.go`), the field-reference walk and
computed vector members all read it. A single-output feature with a
default label prefix also joins `singleOutputPrefixes`
(`internal/model/adstock.go`, pinned by
`TestFeatureMayWrite_CoversOutputLabels`). Validate the operator's
params in `predict_feature.go` with the same coded error the runtime
raises.

## 5. Capability and the other per-operator tables

Add a row to `internal/descriptor/capabilities_features.go`
(`TestManifestOperatorsComplete`), plus entries in `builtinFeatures`
(`internal/descriptor/features.go`), `internal/descriptor/purposes_features.go`,
`internal/descriptor/interpretation_reading.go` and the accepted-types
table (`internal/fieldaccept/fieldaccept.go`). `make sites ARGS="new-op
feature"` names each with an exemplar line.

## 6. Skill and example

Write the atomic skill `skills/op-feat-<kebab>.md`
(`.claude/reference/skill-pack.md`; copy `skills/op-feat-log.md`) and an
`internal/examples/features/NN_<name>.json` example whose
`_meta.operators` names it (`TestEveryOperatorHasAnExampleTag`). Touch
the topical `skills/feature-engineering.md` when the operator changes
which feature an agent should pick. CLAUDE.md is not a companion.

## 7. Run the gates

`make sites ARGS="op FEAT_BOX_COX"` prints the full gate set. At minimum:

```bash
go test -count=1 ./internal/skills/ -run 'TestOperatorHasAtomicSkill|TestSkillsCoverAllComponents'
go test -count=1 ./internal/descriptor/ -run 'TestManifestOperatorsComplete|TestPredict_Feature'
go test -count=1 ./internal/processing/feature/... ./internal/vectors/ ./internal/fieldaccept/ ./types/
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/
make docs
```

See [The Update Demand](update-demand.md).
