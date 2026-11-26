# Adding an Attribute

> **Run the site checklist first.** `make sites ARGS="new-op attribute"` prints every file and declaration a new `ATTR_*` lands in — derived from where the existing ones appear — with the tests to rerun and the goldens to regenerate; `make sites ARGS="op <NAME>"` does the same for an existing one. See [Finding Change Sites](finding-change-sites.md). This recipe explains each step.

> **Embedding Pulse, not contributing to it?** This recipe adds a *built-in* in `internal/processing/`, which embedders cannot import. To add your own operator, implement the matching contract in the public `extend` package instead — see [Extension Points](extension-points.md).

**Audience:** Pulse internals contributors adding a new `ATTR_*`
operator — a per-record derived value computed from one or more cohort
fields (z-score, formula, lookup, etc.).

The recipe is the [aggregator recipe](adding-aggregator.md) with a
different type constant, registry, interface and skill. `make sites
ARGS="new-op attribute"` lists every table a new attribute lands in.

## 1. Declare the type constant

Add the new constant to `types/types.go` and append it to the slice
returned by `types.AllAttributeTypes()`:

```go
const (
    // ... existing constants ...
    ATTR_GINI_BUCKET AttributeType = "ATTR_GINI_BUCKET"
)
```

Classify it in `AttributeType.Streamable()` (`types/streamability.go`)
and add the row to `types/streamability_test.go`
(`TestStreamability_AttributesKnown`).

## 2. Implement and register

Implement the attribute in `internal/processing/` (one
`attribute_<name>.go` file) and register its `AttributeFactory` in
`attributeRegistry` (`internal/processing/registry.go`). The factory
returns an `AttributeComputer`, whose `Compute(records, field)` yields
one value per record (`internal/processing/interfaces.go`). That is the
buffered path. To stream, also implement one sibling interface:

- `RowLocalAttribute` (`Row(record, field)`) when the value depends only
  on the current row (`ATTR_FORMULA`, `ATTR_DATE_PART`);
- `TwoPassAttribute` when it needs population statistics first
  (`ATTR_ZSCORE`'s mean and standard deviation: a `PrePass`, then `Row`).

An attribute that needs a sorted view of every value (`ATTR_PERCENTILE`)
implements neither and runs buffered. The streamability class in step 1
must agree.

## 3. Tests

Write tests first (`internal/processing/attribute_<name>_test.go`). Run
the suite, confirm an informative failure, then implement until green.

## 4. Declare the capability and the other per-operator tables

Add a row to `internal/descriptor/capabilities_attributes.go` (params,
accepted input types, emitted type); `TestManifestOperatorsComplete`
enforces it. Every attribute also has an entry in `builtinFeatures`
(`internal/descriptor/features.go`), `attributePurposes`
(`internal/descriptor/purposes_attributes.go`),
`internal/descriptor/interpretation_reading.go` and, when it takes a
weight or refuses one, `internal/weighting/weighting.go`. `make sites`
names each with an exemplar line.

## 5. Skill and example

Write the atomic skill `skills/op-attr-gini-bucket.md` (the operator name lowercased, `_` → `-`: `skills.OperatorStem`) (`.claude/reference/skill-pack.md`;
copy `skills/op-attr-formula.md`) and add an
`internal/examples/attributes/NN_<name>.json` example whose
`_meta.operators` names it (`TestEveryOperatorHasAnExampleTag`). Touch
the topical `skills/attribute-composition.md` only when the attribute
changes how attributes compose. CLAUDE.md is not a companion.

## 6. Run the gates

`make sites ARGS="op ATTR_GINI_BUCKET"` prints the full gate set and
goldens. At minimum:

```bash
go test -count=1 ./internal/skills/ -run 'TestOperatorHasAtomicSkill|TestSkillsCoverAllComponents'
go test -count=1 ./internal/descriptor/ -run 'TestManifestOperatorsComplete|TestBuiltinPurposes'
go test -count=1 ./types/ -run TestStreamability_AttributesKnown
go test -count=1 ./internal/processing/ -run TestAttribute
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/
make docs
```

See [The Update Demand](update-demand.md).
