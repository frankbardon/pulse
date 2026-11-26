# Adding an Aggregator

> **Run the site checklist first.** `make sites ARGS="new-op aggregator"` prints every file and declaration a new `AGG_*` lands in — derived from where the existing ones appear — with the tests to rerun and the goldens to regenerate; `make sites ARGS="op <NAME>"` does the same for an existing one. See [Finding Change Sites](finding-change-sites.md). This recipe explains each step.

> **Embedding Pulse, not contributing to it?** This recipe adds a *built-in* in `internal/processing/`, which embedders cannot import. To add your own operator, implement the matching contract in the public `extend` package instead — see [Extension Points](extension-points.md).

**Audience:** Pulse internals contributors adding a new `AGG_*`
operator.

This page is a step-by-step recipe. The binding list of companions is
the operator row of the Update Demand
(`.claude/reference/update-demand/operators-and-guidance.md`) plus
`make sites ARGS="new-op aggregator"`; this is the human-readable
walkthrough.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllAggregationTypes()`. Example, for a hypothetical `AGG_GINI`:

```go
const (
    // ... existing constants ...
    AGG_GINI AggregationType = "AGG_GINI"
)

func AllAggregationTypes() []AggregationType {
    return []AggregationType{
        // ... existing entries; append ...
        AGG_GINI,
    }
}
```

The exhaustiveness tests (`TestStreamability_AggregationsKnown` and
friends) will fail until you add the streamability case in step 4.

## 2. Implement the aggregator and register it

The operator implementation lives in `internal/processing/`. Write the factory
function (`newGini(...)` returning the aggregator interface) and
register it in `aggregatorRegistry` in `internal/processing/registry.go`.

If the aggregator can update one row at a time, also implement the
`OnlineAggregator` interface so it joins the streaming Process path.
Sort-based or sum-of-deviation aggregators (like `AGG_MEDIAN`,
`AGG_ZSCORE`) skip this interface and run in the buffered path.

## 3. Tests

Tests come first: write them in `internal/processing/aggregator_test.go`
before the implementation, run the suite, confirm they fail
informatively, then port the implementation until green. See
[Testing Conventions](../contributing/testing.md).

## 4. Declare streamability

Add a case for the new type in `types/streamability.go`:

```go
func (t AggregationType) Streamable() bool {
    switch t {
    // ...
    case AGG_GINI:
        return false // sort-based
    }
}
```

Add the same row to the table in `types/streamability_test.go`.

If the aggregator is online, also expect
`TestRegistryStreamabilityMatchesTypes` to compare your
`OnlineAggregator` implementation against the
`AggregationType.Streamable()` return value — they must agree.

## 5. Update the skill pack

Write the atomic skill `skills/op-agg-gini.md` (the operator name lowercased, `_` → `-`: `skills.OperatorStem`) (frontmatter, required
sections and token budget: `.claude/reference/skill-pack.md`; copy
`skills/op-agg-sum.md` for the shape). `TestOperatorHasAtomicSkill`,
`TestAtomicSkillHasRequiredSections` and `TestSkillTokenBudget` enforce
it; `TestSkillsCoverAllComponents` requires the name in the pack. Add a
line to the topical `skills/aggregation-design.md` when the operator
changes which aggregator an agent should pick.

## 6. Declare the capability metadata

Add a row to `internal/descriptor/capabilities_aggregators.go` describing the
operator's params, accepted field types, emitted type, and the
streamable hint. `TestManifestOperatorsComplete` enforces that every
registered aggregator has a capability row.

The capability row also carries the operator's `ComponentSchema` and
its mergeability class, both required for every registered aggregator.
The next section walks through both.

## 6a. Declare the `ComponentSchema` (Response.Components contract)

Every registered aggregator MUST declare a `ComponentSchema` on its
capability row in `internal/descriptor/capabilities_aggregators.go`. The schema
enumerates the operator-specific keys the aggregator emits into
`Response.Components.Aggregations[i].Operator` and tags the operator
with one of three mergeability classes:

| Class | Wire value | When to use |
|---|---|---|
| `Mergeable` | `"mergeable"` | Components fold via the same associative / commutative path as the scalar value. Constant-space online merge works across streaming chunks and parallel shards. Used by `AGG_SUM`, `AGG_COUNT`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_WELFORD`. |
| `Partial` | `"partial"` | Components fold across chunks but at non-trivial allocation cost — map / set unions where the merge is associative but not constant-space. The orchestrator may stage the merge at terminal flush. Used by `AGG_MODE_COUNT`, `AGG_MODE`, `AGG_DISTINCT_COUNT`, `AGG_DISTINCT_SUM`. |
| `None` | `"none"` | Components cannot be computed from a per-chunk partial. The operator needs a sorted view of the full input. Streaming chunks omit components; emission lands only on the terminal buffered flush. Used by `AGG_MEDIAN`, `AGG_PERCENTILE`. |

The `aggSchema` helper in `capabilities_aggregators.go` prepends the
universal floor (`{"n", "n_null"}`) automatically — do not list those
keys in your `extra` slice, the helper adds them:

```go
{
    Name:          string(types.AGG_GINI),
    Category:      "aggregator",
    Description:   "Gini coefficient of the field across the input set.",
    AcceptsTypes:  tableAcceptsTypes(types.AGG_GINI),
    EmitsTypeNote: "scalar float64 in [0, 1]",
    Streamable:    false,
    ComponentSchema: aggSchema(descriptor.None,
        descriptor.ComponentKey{Name: "sorted_n", Type: "int", Description: "Number of non-null values seen before the final sort."},
    ),
},
```

A floor-only aggregator (operator-specific keys empty) is valid — pass
no `extra` arguments and the schema declares only `{n, n_null}` under
the chosen mergeability class. `AGG_COUNT` is the canonical floor-only
operator.

## 6b. Emit per-operator component values at runtime

Two equivalent paths exist for emitting the operator-specific keys at
runtime; pick whichever fits your aggregator type.

**Sibling interface (preferred for built-in operators).** Implement
`MetaAggregator` (`internal/processing`) on your aggregator type and add a
compile-time assertion in `internal/processing/aggregator.go`:

```go
type giniAggregator struct {
    // ... operator state ...
}

func (g *giniAggregator) Components() (map[string]any, error) {
    return map[string]any{
        "sorted_n": len(g.values),
    }, nil
}

// In internal/processing/aggregator.go's compile-time assertion block:
var _ MetaAggregator = (*giniAggregator)(nil)
```

The assertion list near the bottom of `internal/processing/aggregator.go` is the
grep-discoverable record of which operators emit components. Add the
new entry so interface drift is caught at build time.

**`ComponentsFunc` closure.** Used primarily by `pulse.Options.Extensions`
registrations where you cannot extend the aggregator type. The closure
receives the constructed aggregator instance and returns the
operator-specific keys map. See
[Extension Points](extension-points.md)
for the full extension recipe and the probe-validation contract
(`PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA`,
`PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH`).

In both paths the orchestrator owns the universal floor `{n, n_null}`
unconditionally — your `Components()` MUST NOT re-emit those keys.
Return `(nil, nil)` to signal "no operator-specific keys; let the
orchestrator fill the floor only" — the canonical AGG_COUNT shape.

The full Response.Components contract (per-family typed shape, streaming
behaviour by mergeability class, overlay parity reads) lives in the
[response-components
skill](https://github.com/frankbardon/pulse/blob/main/internal/skills/response-components.md) —
that file remains the authoritative contract document.

## 6c. Mergeable-aggregator rule

If your aggregator is mergeable, implement the engine's
`MergeableAggregator.MergeOnline(other)` and declare it as `Mergeable()`
in `AggregationType.Mergeable()` (`types/streamability.go`) so it
composes correctly under parallel decode
(`internal/service/parallel_reduce.go`) and shard reduce (`internal/service/shard_reduce.go`).
Both surfaces fold per-worker / per-shard partials in deterministic
index order through `partialMerger` (the shard arm incrementally, as
each prefix of shards completes; the decode arm via
`mergeShardPartials`), then `finalizeMergedPartial`.

An aggregator registered but not `Mergeable()` silently forces the request
down the serial scan / `shardIter` path; both parallel paths gate on
`internal/processing`'s `CanMergeRequestWithExtensions` (the built-in-only
`CanMergeRequest` is its nil-registry case) and fall through cleanly when
an entry is not flagged.

An **extension** aggregator takes the same paths through the public
contract instead: it implements `extend.MergeableAggregator.Merge(other)`
and its `pulse.AggregatorRegistration` declares `Streamable: true` and
`Mergeable: true`; probe-validation at `pulse.New` refuses a declaration
the value cannot honour (`PULSE_EXTENSION_MERGEABLE_MISMATCH`). See
[Extension points — Aggregator](extension-points.md).

**Crosstab margin class.** A built-in cell aggregator is admitted to the
fused crosstab arm by `AggregationType.MarginReducibility()`
(`types/streamability.go`) — classify every new aggregator there (the
default is `MarginRecompute`, which keeps its crosstabs buffered). An
extension aggregator declares the same class on its registration's
`MarginReducibility` field (needs `Mergeable`; refused otherwise with
`PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH`); undeclared, its
crosstab cells run buffered. A built-in the classifier does not name
reaches the margin pass as `PULSE_CROSSTAB_AGG_UNCLASSIFIED`, an
internal guard no request can trigger.

Integer and sentinel aggregators (count, min, max, frequency,
distinct_count, mode) produce byte-equal merge output. A float SUM must
not use `+=`: accumulate it in an `internal/reprosum.Sum` (`Add`,
`AddWeighted(w, x)` for a weighted product, `Merge` in `MergeOnline`,
`Float64` at finalize) — the binned reproducible sum `AGG_SUM`,
`AGG_AVERAGE` (that sum over an exact integer n), `AGG_RATIO`, the
weighted sums and the `sum_weights` floor use, so serial, every
`DecodeWorkers` / `ShardWorkers` count and every shard split answer the
same bits (gate: root `TestBitExact_*`). Welford-Pébaÿ aggregators
(variance, stddev, `AGG_WELFORD`, the shape moments) use Chan-Welford and
stay within ULP of serial, not bit for bit. If your aggregator's online
state cannot be folded associatively, leave `Mergeable()` returning false. See [Cohort schema design — Parallel
decode](https://github.com/frankbardon/pulse/blob/main/internal/skills/cohort-schema-design.md)
for the gate composition and observed perf characteristics.

## 6d. Vector-input and array-valued aggregators

A built-in that reads a member set instead of one field (the
`AGG_VEC_*` shape) joins `vectors.VectorAggregationTypes`
(`internal/vectors/aggregations.go`); one whose value is one figure per
member also returns true from `AggregationType.ArrayValued()`
(`types/streamability.go`) and implements `Rich` (the `[]float64`) and
`ExpandedElements` (`params.expand`). Every consumer — predict's field
references, the runtime slot rewrite, the crosstab / sort / overlay /
chain refusals, the expanded output columns and the recommend planner —
asks the one classifier, `vectors.AggregationKinds` (`IsVector`,
`IsArrayValued`), so nothing else needs a list. An extension declares
the same two facts on its registration (`VectorInput`, `ArrayValued`)
and is classified identically — see [Extension Points — Vector
inputs](extension-points.md#vector-inputs-vector--fields).

A bit-exact merge keys its partials by the row's GLOBAL record ordinal
(`Record.MergeBlock`: block, shard, head) and combines them in the fixed
`MergeTree` shape, so a shard archive answers its single-file twin's
bits (`aggregator_vector.go`, `block_merge.go`).

## 7. The other per-operator tables

Every registered aggregator also has an entry in each of these tables;
`make sites ARGS="new-op aggregator"` lists them with an exemplar line
to copy, and each table's category gate fails until the entry exists:

| Table | File | Read first |
|---|---|---|
| `aggregatorClasses` (weight class) | `internal/weighting/weighting.go` | `.claude/reference/weighting.md` |
| `builtinFeatures` (`op(...)` row, `Since`, dependency edges) | `internal/descriptor/features.go` | `.claude/reference/feature-profiles.md` |
| `aggregatorPurposes`, and an Interpretation when the value needs reading | `internal/descriptor/purposes_aggregators.go`, `interpretation_reading.go` | `.claude/reference/guided-analysis.md` |
| `aggregatorBytesTable` (memory estimate) | `internal/limits/aggregator_bytes.go` | — |
| accepted-types row | `internal/fieldaccept/fieldaccept.go` | — |

The accepted-types row matters when the operator reads its field's value
as a number: predict and the runtime then refuse a categorical (its
dictionary index) or a set field with `PULSE_FIELD_TYPE_UNSUPPORTED`,
and the manifest's `accepts_types` comes from
`tableAcceptsTypes(...)`.

Add an `internal/examples/<dir>/*.json` example whose `_meta.operators`
names the operator (`TestEveryOperatorHasAnExampleTag`). CLAUDE.md is
not a companion and never carries a count; the manifest does.

## 8. Run the gates

`make sites ARGS="op AGG_GINI"` prints the exact gate set (table-driven
tests to extend, category gates, structural gates) and the goldens to
regenerate. At minimum:

```bash
go test -count=1 ./internal/skills/ -run 'TestOperatorHasAtomicSkill|TestSkillsCoverAllComponents|TestSkillTokenBudget'
go test -count=1 ./descriptor/ ./internal/descriptor/ -run 'TestManifest|TestPredict'
go test -count=1 ./internal/processing/ -run 'TestRegistryStreamability|TestAggregatorRegistryComplete'
go test -count=1 ./types/ ./internal/weighting/ ./internal/limits/ ./internal/examples/
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/   # manifest + payload goldens
make docs                                                     # the generated guide
```

All companions ride in the same PR. See
[The Update Demand](update-demand.md).
