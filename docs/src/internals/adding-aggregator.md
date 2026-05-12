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

Add a row to `descriptor/capabilities_aggregations.go` describing the
operator's params, accepted field types, emitted type, and the
streamable hint. `TestManifestOperatorsComplete` enforces that every
registered aggregator has a capability row.

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
