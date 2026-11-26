# Adding a Filterer

> **Run the site checklist first.** `make sites ARGS="new-op filterer"` prints every file and declaration a new `FILTER_*` lands in — derived from where the existing ones appear — with the tests to rerun and the goldens to regenerate; `make sites ARGS="op <NAME>"` does the same for an existing one. See [Finding Change Sites](finding-change-sites.md). This recipe explains each step.

> **Embedding Pulse, not contributing to it?** This recipe adds a *built-in* in `internal/processing/`, which embedders cannot import. To add your own operator, implement the matching contract in the public `extend` package instead — see [Extension Points](extension-points.md).

**Audience:** Pulse internals contributors adding a new `FILTER_*`
operator — a per-record predicate that decides whether the row
participates in the downstream aggregation / grouper / crosstab.

The recipe mirrors the [aggregator recipe](adding-aggregator.md); the
filterer-specific moving parts are the predicate factory, the filter
registry, the filter precompute and the universal floor for
`Response.Components.Filterers`.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllFiltererTypes()`:

```go
const (
    // ... existing constants ...
    FILTER_REGEX FiltererType = "FILTER_REGEX"
)

```

Append it to `types.AllFiltererTypes()` and classify it in
`FiltererType.Streamable()` (`types/streamability.go`, plus the
`types/streamability_test.go` row).

## 2. Implement and register

Implement the filterer in `internal/processing/`. Each filterer is a factory
that returns a `FiltererBuilder` — register it in `filtererRegistry`
(`internal/processing/registry.go`).

The builder produces a per-record `FilterFunc` that returns
`(keep bool, err error)`. The streaming Process path invokes the
filter func once per row; the buffered path invokes it once per
materialised record. A filterer whose verdict depends only on its
field's value joins the per-entry precompute switch in
`precomputeInputs` (`internal/processing/filter_precompute.go`).

## 3. Tests

Add tests in `internal/processing/filterer_test.go` before the implementation.
Cover both the include and exclude branches, the null-handling
contract, and any error path.

## 4. Declare the capability metadata

Add a row to `internal/descriptor/capabilities_filterers.go` with the
filterer's params, accepted field types, and the
[`ComponentSchema`](#5-declare-the-componentschema-responsecomponents-contract)
that follows.
`TestManifestOperatorsComplete` enforces a capability row per
registered filterer.

## 5. Declare the `ComponentSchema` (Response.Components contract)

Every registered filterer MUST declare a `ComponentSchema` on its
capability row, with a `Mergeability` class
(`Mergeable` / `Partial` / `None`).

Every built-in filterer is `Mergeable` with an empty
operator-specific keys slice — the orchestrator fills the universal
floor `{n_in, n_out, n_null_input}` from the filter pass's
record-walker counters, and no built-in emits per-filter extras:

```go
{
    Name:            string(types.FILTER_REGEX),
    Category:        "filterer",
    Description:     "Keep records whose field matches the supplied regular expression.",
    AcceptsTypes:    tableAcceptsTypes(types.FILTER_REGEX),
    ComponentSchema: filterSchema(descriptor.Mergeable),
},
```

The `filterSchema` helper prepends the universal floor automatically —
do not list those keys in your `extra` slice. The interface
`MetaFilterer` (`internal/processing`) exists for parity with
`MetaAggregator` / `MetaGrouper`; no built-in implements it. A filterer
that emits per-filter keys (say `n_below` / `n_above`) implements it:
declare the extra keys in `filterSchema(...)` and add
the `Components()` method on the filterer struct plus a compile-time
sentinel:

```go
var _ MetaFilterer = (*regexFilterer)(nil)
```

The full Response.Components contract — universal floor semantics,
streaming behaviour, parity overlay reads — lives in the
[response-components
skill](https://github.com/frankbardon/pulse/blob/main/internal/skills/response-components.md);
extension-side parity lives in
[Extension Points](extension-points.md).

## 6. The other per-operator tables

Every filterer also has an entry in `builtinFeatures`
(`internal/descriptor/features.go`), `internal/descriptor/purposes_filterers.go`,
`internal/descriptor/interpretation_reading.go`, the accepted-types
table (`internal/fieldaccept/fieldaccept.go`) and the guide's
`filtererShape` (`internal/guide/skeleton.go`: the value slot the
filterer reads beside its field). `make sites ARGS="new-op filterer"`
names each with an exemplar line.

## 7. Skill and example

Write the atomic skill `skills/op-filter-<kebab>.md`
(`.claude/reference/skill-pack.md`; copy `skills/op-filter-range.md`)
and an `internal/examples/filterers/NN_<name>.json` example whose
`_meta.operators` names it (`TestEveryOperatorHasAnExampleTag`).
CLAUDE.md is not a companion.

## 8. Run the gates

`make sites ARGS="op FILTER_REGEX"` prints the full gate set. At minimum:

```bash
go test -count=1 ./internal/skills/ -run 'TestOperatorHasAtomicSkill|TestSkillsCoverAllComponents'
go test -count=1 ./internal/descriptor/ -run TestManifestOperatorsComplete
go test -count=1 ./internal/processing/ -run TestFilter
go test -count=1 ./internal/service/ -run TestService_Process_FiltererComponents
go test -count=1 ./types/ ./internal/fieldaccept/ ./internal/guide/
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/
make docs
```

See [The Update Demand](update-demand.md).
