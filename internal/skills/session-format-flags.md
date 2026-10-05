---
name: session-format-flags
description: Per-format CLI flags an agent must know — source-side read knobs (Excel sheet, SPSS missing-value mode and charset), parent-group declaration on the managed import path, the import source zone, and the four .sav write knobs with their refusal defaults.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [CLI flags, --sheet, --spss-missing, --charset, --group, --source-tz, --dst-policy, --ignore-sidecar, --uncompressed, --sanitize-names]
---

# Format CLI flags

Part of `session-bootstrap`. Every leaf and flag: `docs/src/cli/flags.md`.

## Source-format CLI flags

Per-format READ knobs the file cannot always answer itself; all ride `io.ReaderOptions` (one sub-struct per format) and every other format ignores them.<!-- feature: io_format:spss --> Full SPSS model: `spss-cohorts`, `docs/src/cli/import-spss.md`.<!-- /feature -->

| Flag | Format | Leaves | Must know |
|---|---|---|---|
| `--sheet` | Excel | `pulse import excel`, `import predict`, `import schema-template`, `pulse import auto`<!-- feature: capability:import -->; `pulse_import` as `sheet`<!-- /feature --> | — |
<!-- feature: io_format:spss -->
| `--spss-missing` (`auto` \| `null`, default `auto`) | SPSS `.sav` / `.zsav` | `pulse import spss`, `import predict`, `import schema-template`, `convert`, `convert predict` | `auto` nulls each numeric user-missing value (so sums and means never meet a refusal code) AND adds a `<var>_missing` sibling carrying WHY (`sysmis`, the value label, or the code); `null` = same nulls, reason gone. **A `.sav` import can therefore yield MORE columns than the file has variables** — count from `ReadHeader` / the returned schema, never the SPSS variable count. Bad value ⇒ `PULSE_SPSS_MISSING_MODE_INVALID`, never a silent default. Deliberately NOT on `pulse import auto` or the MCP import tool |
| `--charset` | SPSS `.sav` / `.zsav` | the `--spss-missing` leaves **plus** `pulse import auto` and the MCP import tool (as `charset`) | overrides the encoding the file declares about itself; decoding only. Reach for it on `PULSE_SPSS_CHARSET_INVALID` / `PULSE_SPSS_CHARSET_UNSUPPORTED` |
<!-- /feature -->

**Parent groups on the managed path.** `pulse import auto --group KEY[,KEY]:MEMBER[,MEMBER]` (repeatable)<!-- feature: capability:import --> and `pulse_import` `groups: [{key, members}]`<!-- /feature --> declare them; `suggest_groups` returns measured `group_candidates` whose `key`/`members` paste straight back. Default ratio floor 2; findings are warnings (`group_warnings` / envelope `warnings`), never failures. No floor, strict or elision knob there — `pulse import <fmt>` carries those. Detail:<!-- feature: capability:import --> `tool-import`,<!-- /feature --> `cohort-parent-groups`.

**Source zone (every `pulse import <fmt>`).** Naive datetimes import as UTC unless `--source-tz Zone` (IANA, `UTC` or `±HH:MM`; repeatable `col=Zone` wins per column; `date` columns skipped, `col=` on one is `CLI_INPUT`) says where they were recorded. Offset/`Z` literals keep their instant. DST gap/overlap is FATAL (`PULSE_IMPORT_DST_AMBIGUOUS` / `_NONEXISTENT`, naming the row) unless `--dst-policy earlier|later`, which warns `PULSE_IMPORT_DST_RESOLVED` with counts.

<!-- feature: io_format:spss -->
## Target-format CLI flags

Four `.sav` WRITE knobs, one per `io.SPSSWriterOptions` field (`io.WriterOptions.SPSS`). All on `pulse export spss`; `--ignore-sidecar`, `--uncompressed`, `--sanitize-names` also on `convert` / `convert predict` (`convert`'s `--charset` is the SOURCE charset, so the write charset is export-only). Full model: `spss-export`, `docs/src/cli/export-spss.md`.

| Flag | Must know |
|---|---|
| `--ignore-sidecar` | synthesise the dictionary from the `.pulse` schema alone. Suppresses the sidecar **read**, not just the staleness verdict — a healthy sidecar is ignored too (`PULSE_SPSS_SIDECAR_IGNORED`, which cannot say which refusal it silenced). **Cannot round-trip a cohort still carrying a derived MD `set_*` column** ⇒ `PULSE_SPSS_NAME_COLLISION`; export without it |
| `--uncompressed` | flat 8-byte elements instead of SPSS bytecode; losslessly equivalent. Does **not** select ZSAV — emitting that is `PULSE_SPSS_COMPRESSION_UNSUPPORTED` |
| `--charset` (export leaf only) | charset written AND declared. Default: the source's own declared spelling; UTF-8 with no SPSS provenance. Set it when the cohort holds text that codepage cannot express (else `PULSE_SPSS_CHARSET_UNENCODABLE`) |
| `--sanitize-names` | rewrite names a `.sav` cannot carry (space, bracket, hyphen, leading digit) instead of refusing. **Refusal is the default on purpose**; this is the opt-in for the synthesised path. Deterministic, collision-safe, every rename reported as `PULSE_SPSS_NAME_SANITIZED` (full `field → name` list). Inert on the sidecar path |

`pulse export spss` **refuses** `--include` and `--labels` rather than ignoring them (`PULSE_SPSS_EXPORT_UNSUPPORTED`): the writer encodes from raw cohort storage, not the rendered row stream those transform. Narrow or relabel into a cohort first.
<!-- /feature -->
