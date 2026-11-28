# Predict / Inspect — the no-execute contracts

**How to load.** This file is an INDEX: the summary below plus a section map. Load it, then ONLY the slice(s) under `.claude/reference/predict-inspect/` your change touches; find one with `grep -l '<identifier>' .claude/reference/predict-inspect/*.md`. `predict-inspect/history.md` is superseded wording; do not load it. Debugging procedure: `docs/src/internals/debugging-predict.md`.

Load it before changing `internal/descriptor/predict.go`, `internal/descriptor/inspect.go`, any predict root (`PredictCompose` / `Facet` / `Chain` / `Materialize`), `Pulse.Inspect` / `InspectEnvelope`, `pulse_inspect` / `pulse_predict`, the `pulse cohort inspect` leaf, or `pulse.CountRecords`.

## Summary — the rules that bind every change

- **Import ban.** `internal/descriptor/predict.go` must not import `internal/service/` or `internal/processing/` (`TestPredictNoExecutionImports`). Shared knowledge lives in no-execute leaf packages both sides import.
- **Parity.** Wherever predict and the runtime judge the same thing, both call the same function, in the runtime's entry order, raising the runtime's code, message and details. Every such rule has a parity gate; a rule added to one side alone fails it. Predict stricter than a correct runtime is a predict bug.
- **No record is read.** Predict and inspect read header + schema (+ sidecar metadata and saved model files); counts derive from file length or archive headers. An archive is read by random access, never whole.
- **Truncated tails are visible only through an envelope.** `Pulse.Inspect` drops warnings; surfaces that must show a truncated tail use `InspectEnvelope`. `CountRecords` floors SILENTLY, deliberately — never make it error. The floor division exists once (`encoding.Schema.RecordCountForPayload`).
- **Advisories are never warnings,** never escalated by Strict, never change execution.
- `format_version` does not move for anything here: `descriptor.PredictResult` / `InspectResult` are not payload-reachable (`descriptor.Advisory` is an ungated payload root). The API golden moves for new result fields.

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `predict-inspect/inspect-and-roots.md` | Inspect — the envelope is the only way to see a truncated tail; Byte-level inspect and predict live on the instance; Predicting the non-Request roots (Options, Strict and EchoRequest, Compose slots, CLI and MCP, Materialize, Sweeps, Banners, Optimize blocks, Nested-model comparisons, Item fan-out); Suggested weight — sidecar metadata, never records; Inspect — parent groups are header-only too; Inspect — vector fields are header-only too; CountRecords — one floor division, two observability contracts |
| `predict-inspect/predict.md` | Predict (Streamable, CrosstabFusable, TimeZones, Weights, PValues, Matrices); `PredictResult.Sizes`; `ComposeSlotPredict.Sizes`; Field references are one rule, not a predict-only check; Field-type acceptance is gated table-wide; Operator config faults are one rule too; Literal `lookup()` / `range_label()` calls are judged statically; `PredictResult.PostWindow`; `Pulse.ClassifyPostWindow(ctx, req, schema)`; Saved files are the one non-cohort read predict makes (`models`, `populations`) |
| `predict-inspect/limits.md` | Limit findings; `PredictResult.LimitFindings` (+ the Compose, chain and facet roots); The memory estimate (`MaxEstimatedMemory`) |
| `predict-inspect/advisories.md` | Advisories; `PredictResult.Advisories`; the codes; Import predict is not descriptor predict |
| `predict-inspect/compose-compare.md` | Nested-model comparisons — the `regcompare` rule at predict, the summary, sweep-bound, vector and SEM pairs |
| `predict-inspect/history.md` (not loaded by default) | Superseded wording, kept for archaeology |
