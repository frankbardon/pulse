# Adding an Aggregator

**Audience:** Pulse internals contributors adding a new `AGG_*`
operator.

This page is a step-by-step recipe. The same content lives in
[`CLAUDE.md` → Common Claude Code Workflows → Adding a new
aggregator](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md#adding-a-new-aggregator);
this is the human-readable mirror.

> **From CLAUDE.md, Common Claude Code Workflows.**

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
        // ... existing entries, alphabetised ...
        AGG_GINI,
    }
}
```

The exhaustiveness tests (`TestStreamability_AggregationsKnown` and
friends) will fail until you add the streamability case in step 4.

## 2. Implement the aggregator and register it

The operator implementation lives in `processing/`. Write the factory
function (`newGini(...)` returning the aggregator interface) and
register it in `aggregatorRegistry` in `processing/registry.go`.

If the aggregator can update one row at a time, also implement the
`OnlineAggregator` interface so it joins the streaming Process path.
Sort-based or sum-of-deviation aggregators (like `AGG_MEDIAN`,
`AGG_ZSCORE`) skip this interface and run in the buffered path.

## 3. Tests

Tests come first: write them in `processing/aggregator_test.go`
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

Add a section for the new aggregator in
`skills/aggregation-guide.md`. Cover when to use it, what its inputs
and outputs look like, and any caveats (sort cost, memory, supported
field types).

The CI gate `TestSkillsCoverAllComponents` parses the skill body for
the operator name; the section can live anywhere in the file as long
as the name appears.

## 6. Declare the capability metadata

Add a row to `descriptor/capabilities_aggregators.go` describing the
operator's params, accepted field types, emitted type, and the
streamable hint. `TestManifestOperatorsComplete` enforces that every
registered aggregator has a capability row.

The capability row also carries the operator's `ComponentSchema` and
`Mergeability` — both required for every registered aggregator since
v0.20.0. The next section walks through both.

## 6a. Declare the `ComponentSchema` (Response.Components contract)

Every registered aggregator MUST declare a `ComponentSchema` on its
capability row in `descriptor/capabilities_aggregators.go`. The schema
enumerates the operator-specific keys the aggregator emits into
`Response.Components.Aggregations[i].Operator` and tags the operator
with one of three mergeability classes:

| Class | Wire value | When to use |
|---|---|---|
| `Mergeable` | `"mergeable"` | Components fold via the same associative / commutative path as the scalar value. Constant-space online merge works across streaming chunks and parallel shards. Used by `AGG_SUM`, `AGG_COUNT`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_WELFORD`. |
| `Partial` | `"partial"` | Components fold across chunks but at non-trivial allocation cost — map / set unions where the merge is associative but not constant-space. The orchestrator may stage the merge at terminal flush. Used by `AGG_FREQUENCY`, `AGG_MODE`, `AGG_DISTINCT_COUNT`. |
| `None` | `"none"` | Components cannot be computed from a per-chunk partial. The operator needs a sorted view of the full input. Streaming chunks omit components; emission lands only on the terminal buffered flush. Used by `AGG_MEDIAN`, `AGG_PERCENTILE`. |

The `aggSchema` helper in `capabilities_aggregators.go` prepends the
universal floor (`{"n", "n_null"}`) automatically — do not list those
keys in your `extra` slice, the helper adds them:

```go
{
    Name:          string(types.AGG_GINI),
    Category:      "aggregator",
    Description:   "Gini coefficient of the field across the input set.",
    AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
    EmitsTypeNote: "scalar float64 in [0, 1]",
    Streamable:    false,
    ComponentSchema: aggSchema(None,
        ComponentKey{Name: "sorted_n", Type: "int", Description: "Number of non-null values seen before the final sort."},
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
`processing.MetaAggregator` on your aggregator type and add a
compile-time assertion in `processing/aggregator.go`:

```go
type giniAggregator struct {
    // ... operator state ...
}

func (g *giniAggregator) Components() (map[string]any, error) {
    return map[string]any{
        "sorted_n": len(g.values),
    }, nil
}

// In processing/aggregator.go's compile-time assertion block:
var _ MetaAggregator = (*giniAggregator)(nil)
```

The assertion list near the bottom of `processing/aggregator.go` is the
grep-discoverable record of which operators emit components. Add the
new entry so interface drift is caught at build time.

**`ComponentsFunc` closure.** Used primarily by `pulse.Options.Extensions`
registrations where you cannot extend the aggregator type. The closure
receives the constructed aggregator instance and returns the
operator-specific keys map. See the [extension-points
skill](https://github.com/frankbardon/pulse/blob/main/skills/extension-points.md)
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
skill](https://github.com/frankbardon/pulse/blob/main/skills/response-components.md) —
that file remains the authoritative contract document.

## 6c. Mergeable-aggregator rule

If your aggregator is mergeable, implement `MergeableAggregator.Merge(other)`
and declare it as `Mergeable()` in `AggregationType.Mergeable()`
(`types/types.go`) so it composes correctly under parallel decode
(`service/parallel_reduce.go`) and shard reduce (`service/shard_reduce.go`).
Both surfaces fold per-worker / per-shard partials in deterministic
index order via `mergeShardPartials` + `finalizeMergedPartial`.

An aggregator registered but not `Mergeable()` silently forces the request
down the serial `scanIter` / `shardIter` path; both parallel paths gate on
`processing.CanMergeRequest` and fall through cleanly when an entry is not
flagged.

Associative + commutative aggregators (count, sum, min, max, frequency,
distinct_count, mode) produce byte-equal merge output; Welford-Pébaÿ
aggregators (mean, variance, stddev) use Chan-Welford and stay within ULP
of serial. If your aggregator's online state cannot be folded associatively,
leave `Mergeable()` returning false. See [Cohort schema design — Parallel
decode](https://github.com/frankbardon/pulse/blob/main/skills/cohort-schema-design.md)
for the gate composition and observed perf characteristics.

## 7. CLAUDE.md and registered-component lists

Update CLAUDE.md's "Current registered components" section with the
new aggregator name in the right alphabetised slot. If the operator
interacts with categorical fields in a special way, also update
`descriptor/predict.go`'s `numericAggregations` map.

## 8. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllComponents
go test ./descriptor/ -run 'TestManifest|TestPredict'
go test ./processing/ -run TestRegistryStreamability
go test ./...
```

The full Update Demand row for aggregators says: skill update +
capability declaration + CLAUDE.md update + the existing test
coverage. All four ride in the same PR. See
[The Update Demand](update-demand.md).
