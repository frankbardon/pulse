# Predict / Inspect — the no-execute contracts

Relocated from CLAUDE.md (section `### Predict / Inspect contracts`). CLAUDE.md keeps the always-load half inline — the structural import ban, the header-only rule, the streamability mirror, and the fact that `CountRecords` is header-fast. Everything below is the long form it points at.

**Load it before changing `descriptor/predict.go`, `descriptor/inspect.go`, `Pulse.Inspect` / `Pulse.InspectEnvelope`, `mcp.InspectOut` / `HandleInspect`, the `pulse cohort inspect` leaf, or `pulse.CountRecords`.** All four surfaces answer the same question over the same bytes and the failure mode they share is SILENT: a floored or truncated count is indistinguishable on the wire from an honest one.

`format_version` does NOT move for anything described here — `descriptor.InspectResult` is not payload-reachable and `descriptor/testdata/payload-schema.json` is untouched. Only the manifest golden moves, and only for the tool description.

## Inspect — the envelope is the only way to see a truncated tail

`descriptor.Inspect` reads only `encoding.ReadHeader` + `encoding.ReadSchema`, never a record. Dictionaries are truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`.

`InspectResult.RecordCount` is derived from the file LENGTH, never by reading a record — the cumulative per-shard sum for an archive, that shard's own count for an `archive.pulse#shard.pulse` anchor, and `payload_bytes / record_stride` for a single file.

**`Pulse.Inspect` returns the RESULT only and drops `env.Warnings` on the floor.** `Pulse.InspectEnvelope(ctx, path, *descriptor.InspectOptions)` returns the ENVELOPE and is the only way to reach the `FullDict` knob or the truncated-tail warning.

**Every `pulse cohort inspect` mode reads through the facade.** The `--json` / `--full-dict` branch used to `os.ReadFile` the raw argument itself, which bypassed the injected `afero.Fs` and silently lost anchor resolution — an anchor that inspected fine as text returned `data:null`. A leaf needing options or warnings takes the envelope sibling, never its own read.

**`pulse_inspect` carries the warnings too.** `mcp.InspectOut` EMBEDS `descriptor.InspectResult` and adds an `omitempty` `warnings []*descriptor.EnvelopeEntry` slot; `HandleInspect` reads `InspectEnvelope`, never `Inspect`, for the same reason the CLI does — a floored `record_count` is indistinguishable on the wire from an honest one, so a tool routed through the result-only wrapper left an agent unable to see a truncated tail at all. Embedding keeps every pre-existing key at the top level and a clean read emits no `warnings` key, so the tool's wire form is byte-identical for the ordinary case. The entries are coded `{code, message, details}` (not strings) so the code is usable with `pulse_errors_lookup`, and `details` carries `record_stride` + `trailing_bytes`.

## Inspect — parent groups are header-only too

For a cohort newer than `0x01`, `InspectResult` adds `layout` (`pulse_format_version`, physical vs logical record stride), `groups` (one `InspectGroup` per group in file order) and a per-field `group: {group, key, kind}` marker. All three are `omitempty` and absent for `0x01`, so a `0x01` envelope is byte-identical to before (`inspect.json` unchanged; `inspect_grouped.json` pins the grouped shape). **Every figure is header + schema + file length:** the entry count is in the schema block and the record count is the SAME `RecordCountForPayload` floor, so `ratio = record_count ÷ entry_count` needs no scan — `TestInspect_GroupFiguresReadNoRecord` garbles every record byte and asserts identical output. The numbers come from `encoding.AssessGroup` and the verdict from `GroupViability.Finding`, the functions the import gate calls, so inspect and `ImportReport.Groups` cannot disagree over the same file (`TestInspect_GroupFiguresMatchImportReport`); inspect judges at `DefaultDedupRatioFloor` because the file does not record the floor it was imported under. A low verdict is a FIGURE, never an envelope warning — `pulse_inspect`'s `warnings` stay header-read diagnostics. `dictionary_bytes` is `len(Group.Entries)`, the resident cost. Labels come from `Schema.GroupSpecOf` — the format has no group name or declaration ordinal, so they number groups by file position. The archive arm reports no groups: grouped shards are refused at archive build, so the case is unreachable today. The facade parity suites compare inspect through `logicalInspect`, which strips exactly these three additions.

## CountRecords — one floor division, two observability contracts

`pulse.CountRecords(ctx, path) (uint64, error)` returns the record total without decoding the payload. Single-file: `(size − header − schema) / record_stride`. Shard archive: the zip central directory + the `_schema.pulse` SHRD trailer's `AggregateRecordCount`. Anchor: the named shard's own count.

**The single-file floor division exists ONCE, at `encoding.Schema.RecordCountForPayload`, which `descriptor.Inspect` calls too.** Identical arithmetic written twice is one edit away from two different counts over the same bytes, and nothing on either wire says which arm produced the number.

**The arms differ in OBSERVABILITY and only there, deliberately.** `Inspect` has an envelope and raises the `ENCODING_INVALID` truncated-tail warning naming the leftover bytes. `CountRecords` has no warning channel and floors SILENTLY, because it feeds the parallel-decode eligibility gate as well as the facade, and a half-written trailing record must not stop a cohort that still processes from reporting its whole-record count. **Do not close that gap by making `CountRecords` error.**

## Predict

`descriptor/predict.go` MUST NOT import `service/` or `processing/` — gated by `TestPredictNoExecutionImports`. Predict duplicates operator knowledge on purpose; reaching for the real implementation to validate params fails the gate.

`PredictResult.Streamable` mirrors the per-type `Streamable()` methods plus schema gates (decimal). Runtime parity is asserted against `processing.CanStreamRequest(req, schema)` by `TestPredict_Streamable_MatchesRuntime`. `DefaultsApplied` is always computed, whatever the request.

Debugging procedure: `docs/src/internals/debugging-predict.md`.

## Import predict is not descriptor predict

`io.ImportJob.Predict` (`pulse import predict`) is the ONLY predict that may read source rows, and none of the rules above bind it — it lives in `io/`, not `descriptor/`. Its measured pass (declared `Groups`, `ElideConstants`, or `SuggestGroups`) converts every row through `rowConverter`, the converter `Run` uses, and derives group figures through `encoding.DedupGate` / `AssessGroup` / `PlanConstantElisionFor` — so import predict, `ImportReport.Groups` and inspect report the same numbers for the same data (`TestImportPredict_DeclaredGroupsMatchImport`, `TestImportPredict_SuggestedMatchesImport`). Candidate detection nominates over a bounded window and confirms on the full pass; it suggests, never declares. Without those three knobs predict keeps its plain row-count pass and its report is unchanged.
