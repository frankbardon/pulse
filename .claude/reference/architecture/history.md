# Architecture — history (NOT loaded by default)

Superseded wording relocated out of the live file at E9-S4; do not load it for contract work — the index `.claude/reference/architecture.md` and its slices are live.

## The CLAUDE.md Extension Points bullets, pre-compression (relocated verbatim at weighting-descriptive E1-S1)

CLAUDE.md "Extension Points" now carries one pointer bullet in their place; the sections above are the authoritative long form.

- **Probe-validation:** each factory is constructed once against a minimal synthetic schema. A streamable-flagged registration not returning the streaming interface → `PULSE_EXTENSION_STREAMABLE_MISMATCH`; a factory panic → `PULSE_EXTENSION_FACTORY_PANIC`. Grouper `Streamable` / `FansOut` mismatches (`PULSE_EXTENSION_FANOUT_MISMATCH`): `.claude/reference/architecture.md` (Extension surface).
- **Expression env + tables:** `ExprFunctions` merge into the expr-lang env; `LookupTables` (`key→float64`) via `lookup(table, keys...)` (`PULSE_LOOKUP_TABLE_UNKNOWN` / `PULSE_LOOKUP_MISS`), `LabelTables` (output-time labels), `RangeTables` (date-range sets); all three project into the manifest `extensions` block.
- **Snapshot, lazy `table:` resolution, `FieldInputs`, DECLARED `Streamable` / `Mergeable`, `Components()` emission, `DependsOn`:** `.claude/reference/architecture.md` (Extension surface).
- **Skills / examples:** `Extensions.Skills` (`.md`) / `Extensions.Examples` (`_meta` `.json`), validated HARD at `pulse.New` (`PULSE_EXTENSION_SKILL_*` / `_EXAMPLE_*`; never shadowing a built-in), served and pruned like built-ins (stems, rules, wiring: Extension surface).
- **Guidance:** optional `Purpose` (+ test `Interpretation`) checked by the built-in validators at `pulse.New` → `PULSE_EXTENSION_PURPOSE_INVALID`; only intent IDs project.
