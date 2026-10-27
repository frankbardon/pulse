# Predict / Inspect — the no-execute contracts

**How to load.** This file is an INDEX: the preamble below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/predict-inspect/` your change touches; find one with `grep -l '<identifier>' .claude/reference/predict-inspect/*.md`. `.claude/reference/predict-inspect/history.md` is superseded wording — do NOT load it by default. A section-qualified pointer at this file resolves through the section map.

Relocated from CLAUDE.md (section `### Predict / Inspect contracts`). CLAUDE.md keeps the always-load half inline — the structural import ban, the no-record inspect rule (header + schema + sidecar metadata), the streamability mirror, and the fact that `CountRecords` is header-fast. Everything below is the long form it points at.

**Load it before changing `internal/descriptor/predict.go`, `internal/descriptor/inspect.go`, `Pulse.Inspect` / `Pulse.InspectEnvelope`, `mcp.InspectOut` / `HandleInspect`, the `pulse cohort inspect` leaf, or `pulse.CountRecords`.** All four surfaces answer the same question over the same bytes and the failure mode they share is SILENT: a floored or truncated count is indistinguishable on the wire from an honest one.

`format_version` does NOT move for anything described here — `descriptor.InspectResult` is not payload-reachable and `descriptor/testdata/payload-schema.json` is untouched. Only the manifest golden moves, and only for the tool description.

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `predict-inspect/inspect-and-roots.md` | Inspect — the envelope is the only way to see a truncated tail; `Pulse.Inspect` returns the RESULT only and drops `env.Warnings` on the floor; Every `pulse cohort inspect` mode reads through the facade; `pulse_inspect` carries the warnings too; Byte-level inspect and predict live on the instance; Predicting the non-Request roots; Suggested weight — sidecar metadata, never records; Inspect — parent groups are header-only too; Inspect — vector fields are header-only too; CountRecords — one floor division, two observability contracts; The single-file floor division exists ONCE, at `encoding.Schema.RecordCountForPayload`,…; The arms differ in OBSERVABILITY and only there, deliberately |
| `predict-inspect/predict.md` | Predict; `PredictResult.Sizes`; `PredictResult.PostWindow`; Field references are one rule, not a predict-only check; Field-type acceptance is gated table-wide; Saved files are the one non-cohort read predict makes (`models`, `populations`) |
| `predict-inspect/limits.md` | Limit findings; `PredictResult.LimitFindings` (+ the Compose, chain and facet roots); The memory estimate (`MaxEstimatedMemory`, U19 E2-S3) |
| `predict-inspect/advisories.md` | Advisories; `PredictResult.Advisories`; Import predict is not descriptor predict |
| `predict-inspect/history.md` (not loaded by default) | The CLAUDE.md Inspect bullet, pre-compression (relocated verbatim at weighting-descriptive E1-S1) |
