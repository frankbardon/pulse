# Adding a Filterer

**Audience:** Pulse internals contributors adding a new `FILTER_*`
operator — a per-record predicate that decides whether the row
participates in the downstream aggregation / grouper / crosstab.

The recipe mirrors the aggregator recipe; the filterer-specific moving
parts are the predicate factory, the filter registry, and the universal
floor for `Response.Components.Filterers`.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllFiltererTypes()`:

```go
const (
    // ... existing constants ...
    FILTER_REGEX FiltererType = "FILTER_REGEX"
)

func AllFiltererTypes() []FiltererType {
    return []FiltererType{
        // ... existing entries, alphabetised ...
        FILTER_REGEX,
    }
}
```

## 2. Implement and register

Implement the filterer in `processing/`. Each filterer is a factory
that returns a `FiltererBuilder` — register it in `filtererRegistry`
(`processing/registry.go`).

The builder produces a per-record `FilterFunc` that returns
`(keep bool, err error)`. The streaming Process path invokes the
filter func once per row; the buffered path invokes it once per
materialised record.

## 3. Tests

Add tests in `processing/filter_test.go` before the implementation.
Cover both the include and exclude branches, the null-handling
contract, and any error path.

## 4. Declare the capability metadata

Add a row to `descriptor/capabilities_filterers.go` with the
filterer's params, accepted field types, and the
[`ComponentSchema`](#5-declare-the-componentschema-responsecomponents-contract)
that follows.
`TestManifestOperatorsComplete` enforces a capability row per
registered filterer.

## 5. Declare the `ComponentSchema` (Response.Components contract)

Every registered filterer MUST declare a `ComponentSchema` on its
capability row, with a `Mergeability` class
(`Mergeable` / `Partial` / `None`).

In v1 every built-in filterer is `Mergeable` with an empty
operator-specific keys slice — the orchestrator fills the universal
floor `{n_in, n_out, n_null_input}` from the filter pass's
record-walker counters, and no per-filter extras are emitted today:

```go
{
    Name:            string(types.FILTER_REGEX),
    Category:        "filterer",
    Description:     "Keep records whose field matches the supplied regular expression.",
    AcceptsTypes:    stringFieldTypes,
    ComponentSchema: filterSchema(Mergeable),
},
```

The `filterSchema` helper prepends the universal floor automatically —
do not list those keys in your `extra` slice. The interface
`processing.MetaFilterer` exists for extension-author parity with
`MetaAggregator` / `MetaGrouper` and leaves room for future per-filter
specifics (`n_below` / `n_above` for FILTER_RANGE, per-value `n` for
FILTER_INCLUDE / FILTER_EXCLUDE). When a built-in eventually
implements it, declare the extra keys in `filterSchema(...)` and add
the `Components()` method on the filterer struct plus a compile-time
sentinel:

```go
var _ MetaFilterer = (*regexFilterer)(nil)
```

The full Response.Components contract — universal floor semantics,
streaming behaviour, parity overlay reads — lives in the
[response-components
skill](https://github.com/frankbardon/pulse/blob/main/skills/response-components.md);
extension-side parity lives in
[Extension Points](extension-points.md).

## 6. Update the aggregation-guide skill

Add a section in the filtering portion of `skills/aggregation-design.md`
covering the new filterer's semantics, parameter shape, and the null-
input contract.

## 7. Update CLAUDE.md

Bump the registered-filterer count in CLAUDE.md's "Skill Pack" section.

## 8. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllComponents
go test ./descriptor/ -run TestManifestOperatorsComplete
go test ./processing/ -run TestFilter
```

The Update Demand row for filterers covers all of these in one PR;
see [The Update Demand](update-demand.md).
