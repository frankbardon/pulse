# Adding an Attribute

**Audience:** Pulse internals contributors adding a new `ATTR_*`
operator — a per-record derived value computed from one or more cohort
fields (z-score, formula, lookup, etc.).

The recipe is the aggregator recipe with three swaps: a different type
constant, a different registry, and a different skill file. The shape
is otherwise identical.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllAttributeTypes()`:

```go
const (
    // ... existing constants ...
    ATTR_GINI_BUCKET AttributeType = "ATTR_GINI_BUCKET"
)

func AllAttributeTypes() []AttributeType {
    return []AttributeType{
        // ... existing entries, alphabetised ...
        ATTR_GINI_BUCKET,
    }
}
```

## 2. Implement and register

Implement the attribute in `internal/processing/`. Each attribute is a factory
function registered in `attributeRegistry` (`internal/processing/registry.go`).
Attribute factories return a closure with the signature
`func(record encoding.RecordView) (any, error)`.

If the attribute can be evaluated row-at-a-time (the common case),
that is all the implementation needs; the streaming Process path
invokes the closure on every record.

## 3. Tests

Write tests first in `internal/processing/attribute_test.go`. Run the suite,
confirm informative failure, port the implementation until green.

## 4. Declare the capability metadata

Add a row to `internal/descriptor/capabilities_attributes.go` with the
attribute's params, the field types it accepts as input, the type
it emits, and any documentation strings.
`TestManifestOperatorsComplete` enforces a capability row per registered
attribute.

## 5. Update the attribute-composition skill

Add a section in `skills/attribute-composition.md` covering the new
attribute's params, output column naming convention, and any caveats
(NaN propagation, integer underflow, expr-runtime cost). The
`TestSkillsCoverAllComponents` gate parses the skill body for the
operator name.

## 6. Update CLAUDE.md

There is **no registered-attribute count or list in CLAUDE.md to update** —
CLAUDE.md never hardcodes registered counts; the manifest is the source of
truth. The Update Demand operator row (`.claude/reference/update-demand.md`)
requires instead the atomic skill `skills/op-attr-<kebab>.md`, the capability declaration in
`internal/descriptor/capabilities_attributes.go` — and an `internal/examples/<dir>/*.json` example whose `_meta.operators`
names the operator (`TestEveryOperatorHasAnExampleTag`). Edit CLAUDE.md only if the
operator introduces a contract it states directly, and mind
`TestClaudeMdSizeBudget` — long-form prose belongs in `.claude/reference/`.

## 7. Run the gates

```bash
go test ./internal/skills/ -run TestSkillsCoverAllComponents
go test ./internal/descriptor/ -run TestManifestOperatorsComplete
go test ./processing/ -run TestAttribute
```

The Update Demand row for attributes covers all of these in one PR;
see [The Update Demand](update-demand.md).
