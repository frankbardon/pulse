# Adding a Window Operator

**Audience:** Pulse internals contributors adding a new `WIN_*`
operator — a window-frame function that emits a per-row value derived
from a sliding or anchored frame around the current record
(`WIN_LAG`, `WIN_LEAD`, `WIN_RANK`, `WIN_RUNNING_*`, `WIN_EWMA`, …).

The recipe mirrors the aggregator recipe; the window-specific moving
parts are the frame contract and the windows registry. Streamability is
not one of them — see step 3.

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

## 3. Streamability — nothing to do

No window operator streams today. `WindowType.Streamable()` in
`types/streamability.go` is an unconditional `return false`, because every
`WIN_*` operator needs a sort over the row set:

```go
func (t WindowType) Streamable() bool {
    return false
}
```

There is **no per-type case to add and no table row to write**.
`TestStreamability_WindowsKnown` iterates `types.AllWindowTypes()` and asserts
every constant answers `false`, so a new operator is covered the moment step 1
lands. If a future window operator genuinely streams, that is when the method
grows a `switch` and the test grows a table — not before.

## 4. Capability declaration

Add a row to `descriptor/capabilities_window_ops.go` with the operator's
params, accepted field types, and streamable hint.
`TestManifestOperatorsComplete` enforces a capability row per registered
window operator.

## 5. Tests

Add tests in `processing/window/<name>_test.go`. Cover the empty-frame,
single-row, null-bearing, and order-sensitive cases.

## 6. Write the atomic skill

The gated target is the **atomic** skill `skills/op-win-<kebab>.md`, not a
section in `skills/window-design.md` — `TestSkillsCoverAllWindowTypes` and
`TestOperatorHasAtomicSkill` both key off that stem. Frontmatter `name:` must
equal the file stem; `category: WIN`; `operator:` the full constant. The
required `##` sections are `## Params`, `## Inputs`, `## Output`,
`## Gotchas`, `## See`, and the body budget is 1,200 characters
(`TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`).

Add the operator to `skills/window-design.md` only where the topical prose
would otherwise be wrong (the frame-forbidden list, a family gotcha) — per-op
detail belongs in the atomic file.

## 7. Update CLAUDE.md

There is **no registered-window count to bump** — CLAUDE.md forbids hardcoded
component counts because the manifest is the source of truth. CLAUDE.md's
Update Demand row for windows is generic over `descriptor/capabilities_*.go`,
so a new window operator normally needs no CLAUDE.md edit at all. Edit it only
if the operator introduces a contract CLAUDE.md states directly, and mind
`TestClaudeMdSizeBudget` — long-form prose belongs in `.claude/reference/`.

## 8. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllWindowTypes
go test ./descriptor/ -run TestManifestOperatorsComplete
go test ./processing/window/...
go test ./types/ -run TestStreamability_WindowsKnown
```

The Update Demand row for windows covers all of these in one PR; see
[The Update Demand](update-demand.md).
