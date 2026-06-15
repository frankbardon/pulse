# Adding a Window Operator

**Audience:** Pulse internals contributors adding a new `WIN_*`
operator — a window-frame function that emits a per-row value derived
from a sliding or anchored frame around the current record
(`WIN_LAG`, `WIN_LEAD`, `WIN_RANK`, `WIN_RUNNING_*`, `WIN_EWMA`, …).

The recipe mirrors the aggregator recipe; the window-specific moving
parts are the frame contract, the windows registry, and the streamability
case.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllWindowTypes()`:

```go
const (
    // ... existing constants ...
    WIN_PERCENT_RANK WindowType = "WIN_PERCENT_RANK"
)

func AllWindowTypes() []WindowType {
    return []WindowType{
        // ... existing entries, alphabetised ...
        WIN_PERCENT_RANK,
    }
}
```

## 2. Implement and register

Window operators live under `processing/window/`. Each file is one
operator; register the factory in the package's `init()` via the
`register(types.WIN_X, newX)` call shape used by sibling files.

The frame semantics — anchored vs sliding, lookahead vs lookback —
are part of the operator's contract and ride in the operator's struct
parameters; the orchestrator's window-pass invokes the implementation
per row in declared order.

## 3. Declare streamability

Window operators that need a full forward / backward pass (lead-based,
percent-rank style) are not streamable. Add the case in
`types/streamability.go`:

```go
func (t WindowType) Streamable() bool {
    switch t {
    // ...
    case WIN_PERCENT_RANK:
        return false
    }
}
```

Add the matching row to `types/streamability_test.go` so
`TestStreamability_WindowsKnown` passes.

## 4. Capability declaration

Add a row to `descriptor/capabilities_windows.go` with the operator's
params, accepted field types, and streamable hint.
`TestManifestOperatorsComplete` enforces a capability row per registered
window operator.

## 5. Tests

Add tests in `processing/window/<name>_test.go`. Cover the empty-frame,
single-row, null-bearing, and order-sensitive cases.

## 6. Update the window-operations skill

Add a section in `skills/window-operations.md` covering the operator's
frame contract, parameter shape, and output column naming. The
`TestSkillsCoverAllWindowTypes` gate enforces presence.

## 7. Update CLAUDE.md

Bump the registered-window count in CLAUDE.md's "Skill Pack" section.

## 8. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllWindowTypes
go test ./descriptor/ -run TestManifestOperatorsComplete
go test ./processing/window/...
go test ./types/ -run TestStreamability_WindowsKnown
```

The Update Demand row for windows covers all of these in one PR; see
[The Update Demand](update-demand.md).
