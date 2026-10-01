# Flag Reference

**Audience:** CLI users who want one page that lists every flag and
every environment variable in scope across the binary.

The per-command pages list each command's full flag set; this page is
the cross-cutting reference for flags that appear on multiple
commands and for the environment variables Pulse reads.

> **LLM agents using MCP:** there is no LLM-facing skill for the CLI
> surface. Agents go via MCP tools (`pulse_process`, `pulse_inspect`,
> ...) — see [`pulse mcp`](mcp.md) and
> [Adding an MCP tool](../internals/adding-mcp-tool.md).

## Global flags

Available on the bare `pulse` invocation:

| Flag | Effect |
|---|---|
| `--json` | Print the root manifest as JSON (envelope-wrapped) |
| `--slim` | With `--json`, drop prose descriptions for size-sensitive clients |

Both default to off. `pulse --json` is the discovery entry point — it
emits the manifest documented at [`pulse manifest`](manifest.md).

## Environment variables

| Variable | Used by | Required | Purpose |
|---|---|---|---|
| `PULSE_DATA_DIR`        | All commands when no path override is given; required by `pulse mcp` | conditionally | Base directory for cohort files. Relative cohort paths resolve against it |
| `PULSE_IMPORTS_DIR`     | `pulse import auto / list / drop`                                    | no | Managed-imports subdir under the data root. Defaults to `imports` |
| `PULSE_IMPORT_TTL`      | `pulse import auto`                                                  | no | Default TTL for managed imports. Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. Defaults to `7d` |
| `PULSE_LABEL_TABLES_DIR`| `pulse api sample --labels`, `pulse api facet --labels`              | no | Directory of JSON files auto-loaded as label tables at `pulse.New` time; each `*.json` becomes one table keyed by its filename |
| `PULSE_MCP_NO_COHORT_SCAN`| `pulse mcp`                                                        | no | Skip the startup walk that enumerates `.pulse` files as `pulse://` resources. Same as `--no-cohort-scan`; cohorts stay readable by URI, only `resources/list` loses them |

`PULSE_DATA_DIR` is the only required `PULSE_*` environment variable.
The Makefile auto-loads a repo-root `.env` file so you can keep these
(and any future env vars) there for development.

When embedding the library, you can bypass the env vars entirely by
passing `pulse.Options{DataDir: "/path"}`, `pulse.Options{ImportsDir,
ImportTTL, LabelTablesDir, FS: myFs}` etc. — see
[`pulse.New` & Options](../library/options.md).

## `--json` envelope

Almost every leaf command accepts `--json`, which switches output
from human prose to a structured envelope. The envelope shape is
fixed and documented in CLAUDE.md → Output Format Contract:

```json
{
  "format_version": "1.1",
  "data":     { /* operation-specific result */ },
  "request":  { /* normalized request, omitted unless --echo-request was passed */ },
  "errors":   [ /* {"code": "...", "message": "...", "details": {...}} */ ],
  "warnings": [ /* same shape */ ]
}
```

`format_version` is currently `"1.1"`. `errors` and `warnings` are
always arrays (never null) so JSON consumers can index without
nullable-check overhead. `request` is opt-in (see
[`--echo-request`](#--echo-request) below); `data.components` is the
additive `Response.Components` slot documented per leaf — see
[`api process` → `--json`](api-process.md#-json).

## Shared per-command flags

Several flags appear on multiple commands with identical semantics.

### `--no-defaults`

Available on: `api process`, `api compose`.

Disable the runtime smart-defaults pass that infers operator `Type`
from the named field's schema type when the caller omits it. Forces
the request to be source-of-truth. See [pulse.New &
Options](../library/options.md) for the underlying library option.

### `--stream`

Available on: `api process`, `api compose`.

Stream result rows as NDJSON (one row per line) instead of buffering
the full result. For `compose`, each line carries an `{"index": N,
"row": {...}}` shape so consumers know which sub-request produced
each row. See [Streaming & ProcessStream](../library/streaming.md).

### `--strict`

Available on: `api process`, `api predict`.

Treat request-validation warnings (e.g. numeric aggregation on a
categorical field, low-quality field description) as errors. On
`api predict` this fails validation; on `api process` it refuses to
execute. Useful in CI gates that want the strictest possible
validation.

`import <format>` also takes `--strict`, with a narrower meaning: it
escalates the parent-group viability warnings only — see
[`--strict` (import)](#--strict-import).

### `--echo-request`

Available on: `api process`, `api process-chain`, `api compose`,
`api predict`, `api sample`, `api facet`.

Include the *normalized* request — smart defaults resolved, label
bindings expanded — on `envelope.request`. Absent (and omitted from
JSON) by default so the envelope shape is unchanged for callers that
do not need it. Streaming output (`--stream`) skips the echo because
NDJSON has no envelope.

### `--full-dict`

Available on: `cohort inspect`.

Print full categorical dictionaries instead of truncating after 100
entries. Pair with `--json` for programmatic consumption.

### `--strict` / `--seed` / `--rows`

`synth from-schema` and `synth from-profile` use `--seed` (for
deterministic RNG) and `--rows` (override the spec's row count). See
the per-command pages.

### `--emit-spec` / `--rules`

Available on: `synth from-profile`.

`--emit-spec <path>` writes the profile-derived spec (after any
`--rules` merge) as indented JSON. It is the spec that actually
generated, so feeding it to `synth from-schema` at the same `--seed`
reproduces the same rows — and it is the only way to see which captured
models survived translation, which distribution each field reconstructed
to, and which conditional pairs were retired.

`--rules <path>` loads structural rules from a standalone JSON file — a
bare array of rule objects, the same shape as a spec's `rules` key — and
**replaces** the derived spec's rules with them. Refusals carry E1's own
`PULSE_SYNTH_RULE_*` codes with the file path in `details.path`. See
[synth from-profile](synth-from-profile.md).

### `--suggest-rules`

Available on: `profile create`.

`--suggest-rules <path>` detects structural rules on the same scan the
profile already makes — no additional cohort read — and writes them to
`<path>` as the bare rules array `synth from-profile --rules` consumes
unmodified. Three detectors, one file: GATING relationships (a
low-cardinality field whose levels split a target's null rate into ~1 and
~0, proposed as `set_null`), CO-MISSING blocks (fields null on exactly
the same rows, proposed as `null_together`) and EXACT DEPENDENCIES (a
field that IS a function of one other, proposed as `set_expr`). An
identical null RATE is never enough for a block — two unrelated fields
can share one — so admission is identical null PATTERN; near misses,
almost-determined pairs, always-null columns and constant columns are
reported in `warnings` rather than proposed.

**Dependency detection is bounded, deliberately, and the bounds are
published** — in every candidate's `_evidence.note`, on stderr and in
[profile create](profile-create.md). TARGETS are `packed_bool` and `u4`
only; SOURCES are `categorical_*` / `packed_bool` / `u4` with at most 16
observed levels; the dependency is on exactly ONE source. A field missing
from the file was not cleared, it was not examined.

PROPOSED, never applied. Detection finds the STATISTICAL gate; a human
knows the SEMANTIC one, and the two are routinely different fields that
move together. Each candidate carries its own measurement on `_evidence`
— the one inert slot of a rule, which generation never reads — so a
candidate the analyst deletes takes its evidence with it. Gating
candidates are emitted first and dependency candidates LAST, because
declaration order is applied order: a block that FOLLOWS a hand-narrowed
`set_null` repairs it, and a `set_expr` derivation must re-resolve its
own block after every null-state rule has run. The profile document
itself gains no section: absent the flag it is byte-identical, and with
it only `warnings` moves. See [profile create](profile-create.md).

### `--run-continuation`

Available on: `profile create`.

`--run-continuation` measures, on the same scan the profile already
makes, each field's run-continuation: the fraction of adjacent row pairs
whose on-wire bytes (and, for a nullable field, null bit) repeat. That is
exactly the hit rate of the run-skip decode, which rewrites only the
fields that changed since the previous row — so a cohort that was sorted
by its parent key upstream scans faster, and one re-imported without
that `ORDER BY` silently loses the win. The additive `run_continuation`
section carries the per-field rates, an overall figure, the
high-continuation fields and a one-line advice; pairs never span a shard
boundary. Absent the flag the document is byte-identical. See
[profile create](profile-create.md).

### `--elide-constants`

Available on: `import csv`, `import tsv`, `import ndjson`,
`import jsonarray`, `import parquet`, `import arrow`, `import excel`,
`import spss`, `import predict` (reports what would be elided and the
bytes saved; writes nothing).

`--elide-constants` stores every field that holds exactly one value — or
is null — on every imported row once, in the schema block, and drops it
from each record. Constancy is measured over the full row pass, never
the inference sample. The output is a format `0x02` cohort that binaries
predating `0x02` cannot open, which is why it is opt-in. Nothing is
elided (and the cohort stays byte-identical `0x01`) for fewer than two
rows or when the saving would not repay the schema-block growth; when
every field is constant the lowest-index field stays in the row. The
elided fields are printed, and reported as `ElidedConstants` under
`--json`. See [parent groups](../format/parent-groups.md).

### `--group`

Available on: `import csv`, `import tsv`, `import ndjson`,
`import jsonarray`, `import parquet`, `import arrow`, `import excel`,
`import spss`, `import predict`, `import auto`. Repeatable — one parent
group per flag. On `import auto` the group is judged at the default
ratio floor and its findings are always warnings (`--json` puts them in
`warnings`, the per-group figures under `data.groups`); the floor and
`--strict` stay on the per-format leaves, since neither changes the
bytes written.
On `import predict` the declaration is evaluated, not applied: see
[`--suggest-groups`](#--suggest-groups).

`--group KEY[,KEY...]:MEMBER[,MEMBER...]` declares a **parent group**:
the key fields identify a parent (a customer ID, say) and the members
are the fields that key determines (its name, region, tier). Each
distinct tuple is stored once in the schema block and every record
carries a 4-byte index instead of the members, so a denormalised join
shrinks by its repeated parent block. `--group F1,F2,...` (no colon) is
a plain tuple group: each distinct combination is one entry, with no
key check.

```sh
pulse import csv -i lines.csv -o lines.pulse \
  --group cust_id:cust_name,cust_region \
  --group prod_id:prod_cat
```

A keyed declaration is **checked, not trusted**: if two rows share a key
tuple but disagree on a member (value or null state), the import fails
with `PULSE_GROUP_MEMBER_NOT_CONSTANT` naming the member, the record
index and the source row — a wrong declaration is refused, never
silently turned into a larger dictionary. A field in two groups is
`PULSE_GROUP_FIELD_CONFLICT` (naming both groups), an unknown field
`PULSE_GROUP_FIELD_UNKNOWN`, a malformed value
`PULSE_GROUP_DECLARATION_INVALID`; all three fail before the row pass.
More than 2^32 distinct tuples is `PULSE_GROUP_ENTRIES_EXHAUSTED`.

Every declared group passes a **per-group viability gate**. A group
whose members are no wider than the 4-byte index can never save space:
it is dropped (its members stay in the row) with a
`PULSE_GROUP_TOO_NARROW` warning naming both widths. A group whose
dedup ratio (rows per distinct tuple) is below `--dedup-ratio-floor`,
or that makes the file no smaller, is still written, with a
`PULSE_DEDUP_LOW_RATIO` warning carrying the ratio, the resident
dictionary bytes and the byte delta. Other groups on the same import
are unaffected. The text output prints each group's ratio, resident
dictionary bytes and byte delta; `--json` reports them per group under
`Groups` and the findings in `warnings`.

### `--suggest-groups`

Available on: `import predict`.

Finds candidate parent groups before you import, and measures each one.
A candidate is a single key field plus every field it determines (the
same value on every row sharing a key value). Each is reported with its
fields, verdict, distinct-tuple count, ratio, resident dictionary bytes,
byte delta and the exact file size with that group declared, next to
the flat cohort's size. You also get a ready-to-paste `--group` value
and the structured `{key, members}` form. A candidate below the width
or ratio floor is listed with its verdict and reason, not hidden.
`suggested` lists only the admitted candidates that do not share a field
with a better one. A source with no viable group suggests none.

```sh
pulse import predict -i lines.csv --suggest-groups
pulse import predict -i lines.csv --group cust_id:cust_name,cust_tier
```

**It suggests only.** Nothing is declared until you pass `--group` to
the import. Candidates are **nominated** over the first rows, up to
10,000 (fewer on a wide schema; `window_rows` / `window_bound`). The
500-row inference sample is too small at typical fanouts: it holds a few
dozen parents, and a column that happens to be constant across so few
looks like a parent attribute. Candidates are then **confirmed and
measured over every row**. A member that varies within its key later in
the file is dropped and listed under `rejected_members`. The figures
therefore come from the same conversion and the same `internal/encoding.DedupGate`
arithmetic the import uses, so they are not a sample estimate. They
equal what `import --group` reports and what `cohort inspect` shows
afterwards. A dependency can still break in a later re-export of the
same source; the import catches that with
`PULSE_GROUP_MEMBER_NOT_CONSTANT`.

Detection has these limits:

- Keys are single fields; composite keys are not detected. Evaluate one
  with `--group A,B:MEMBERS`.
- Up to 64 keys are evaluated, highest cardinality first
  (`keys_over_bound`), and 16 candidates are confirmed
  (`candidates_over_bound`).
- A field constant across the window, or declared in a `--group`, is
  never nominated.
- Candidate dictionaries share a 512 MiB budget. A candidate that
  outgrows its share is reported `unmeasured`.

Cost: `import predict` normally reads rows without converting them.
With `--suggest-groups`, `--group` or `--elide-constants` it converts
every row as the import would. That measured roughly 1.2x-1.4x the
import's own time on synthetic 12- and 102-column sources, against
about 0.15x for plain predict. It does not spool the rows, so memory is
bounded by the window and the candidate dictionaries, not the file.
When a field outgrows its sample-inferred width mid-file (see
`PULSE_IMPORT_WIDTH_PROMOTED` below) the measured pass stops measuring,
finishes the file to find every promotion, then rewinds and measures
again at the final widths, so such a source is read twice. Only the
measured pass sees the full-file widths: plain `import predict` reports
the sample-inferred schema.
`--json` adds `Projection`, `Groups`, `GroupWarnings`, `ElidedConstants`,
`GroupCandidates` and `WidthWarnings` to `data`; `format_version` stays
`"1.1"`.

### Width promotion (inferred imports and converts)

Not a flag: every inferred `import <format>`, `import auto`,
`pulse_import` and `pulse convert` does it. Inference sizes `categorical_*` rungs and integer
widths from the first `--sample-rows` rows. A later value that outgrows
them promotes the field to the narrowest type that holds it instead of
dropping the row: `categorical_u8` → `u16` → `u32`; `u4` → `u8` → `u16`
→ `u32` → `u64` for a larger non-negative integer; `u4`..`u32` → `f64`
for a negative, fractional or signed number; `f32` → `f64` for a value
outside f32's range — past `MaxFloat32`, or a non-zero magnitude f32
would flush to zero. That is the range test inference chose `f32` by, so
a value f32 merely rounds (`0.1`) never promotes. Every step is lossless
for every value already read. Each promoted field draws one `PULSE_IMPORT_WIDTH_PROMOTED`
warning (`field`, `from`, `to`, `source_row`) in the envelope `warnings`
and `data.WidthWarnings` (`width_warnings` on `import auto` /
`pulse_import`). A `--schema`, a `column_type_overrides` column or an
authoritative source schema (SPSS, Arrow, Parquet) never promotes: its
overflow stays a `PULSE_IMPORT_ROW_ERROR`. So does a non-number, a
non-integer in a `u64` column, a non-boolean in a `packed_bool` column
(no type holds both losslessly), and a dictionary past `categorical_u32`.

`pulse convert` copies cell text, so a promotion never changes what the
target receives. It reports the promoted types in `data.Schema` and the
warnings in `data.WidthWarnings` and the envelope `warnings`, and a
`--keep-pulse` intermediate is byte-identical to a plain `import` of the
source. On a declared schema (`--schema`, or an SPSS / Arrow / Parquet
source's own) a full categorical rung still stops the convert with the
fatal `PULSE_IMPORT_CATEGORICAL_OVERFLOW`, and no intermediate is written.

### `--dedup-ratio-floor`

Available on: every `import <format>` leaf, and `import predict`.
Default `2`.

The rows-per-distinct-tuple floor below which a `--group` draws
`PULSE_DEDUP_LOW_RATIO`. A floor of `1` disables it, leaving only the
check that the group actually makes the file smaller. The group is
written either way unless `--strict` is set.

### `--strict` (import)

Available on: every `import <format>` leaf, and `import predict` (which
then fails where the import would).

Turns the parent-group viability warnings (`PULSE_GROUP_TOO_NARROW`,
`PULSE_DEDUP_LOW_RATIO`) into errors: the import fails with that code
(`errors[0].code` under `--json`) and writes nothing. Other import
warnings are unaffected.
Groups are named in errors and output by position and key
(`group 1 [key: cust_id]`) — the format stores no group name. Each group
is printed with its distinct-tuple count, and reported under `Groups`
with `--json` (entry count, entry width, member bytes per row, index
width). Declared members are never constant-elided, so `--group`
composes with `--elide-constants`. Field names containing `,` or `:`
cannot be declared here; use `io.ImportJob.Groups`. The output is a
format `0x02` cohort that binaries predating `0x02` cannot open; with no
`--group` the import is unchanged. `pulse_import` (MCP) takes the same
declarations as structured `groups: [{key, members}]` entries, plus
`suggest_groups` to return measured candidates in the same shape. See
[parent groups](../format/parent-groups.md).

## Command index

Every runnable leaf the binary exposes, with the page that documents it
in depth. A leaf whose "Documented in" column names no dedicated page is
covered by the surrounding topic page or by `--help`.

The list itself is the contract, and it is enforced **in both
directions** by `TestSkillsCoverAllCliLeaves`: a leaf added to
`buildApp()` without a row here fails the gate, and a row here that names
a command the binary does not actually mount fails it too. The second
direction is the one that matters when a registration is deleted — the
documented command would otherwise stay listed while answering "command
not found". Group nodes that carry no action of their own (`pulse api`,
`pulse shard`, `pulse index`) are not leaves and are deliberately absent.

| Leaf | Purpose | Documented in |
|---|---|---|
| `pulse api compose` | Execute multiple processing requests in batch | [api compose](api-compose.md) |
| `pulse api facet` | Distinct values for a field, or a rich multi-field summary | [api facet](api-facet.md) |
| `pulse api lookup` | Key-exact row read against a prebuilt sidecar index | [api lookup](api-lookup.md) |
| `pulse api predict` | Validate a request against a cohort without executing | [api predict](api-predict.md) |
| `pulse api process` | Execute a processing request against a cohort | [api process](api-process.md) |
| `pulse api process-chain` | Source-rooted linear chain of mergeable stages | [api process](api-process.md) |
| `pulse api sample` | Return sample rows from a cohort | [api sample](api-sample.md) |
| `pulse cohort filter` | Filter a `.pulse` file to a new `.pulse` file | `--help` |
| `pulse cohort inspect` | Inspect a `.pulse` header and schema | [cohort inspect](cohort-inspect.md) |
| `pulse convert` | Convert between tabular formats, auto-detected from extensions | [import spss](import-spss.md), [export spss](export-spss.md) |
| `pulse convert predict` | Validate a conversion without writing output | [export spss](export-spss.md) |
| `pulse dedup` | Deduplicate an existing single-file cohort's repeated parent blocks into parent groups (format `0x02`) — the existing-cohort twin of `pulse import --group`, taking the same `--group`, `--elide-constants`, `--dedup-ratio-floor` and `--strict`. Rewrites in place (destructive, non-interactive, atomic: temp file, fsync, rename, so a refusal or failure leaves the cohort byte-identical) or, with `--out PATH`, writes a new file that must not exist. `--suggest-groups` detects candidates over the cohort's records; alone it writes nothing. An already-grouped cohort is regrouped from scratch. Refuses a shard archive (`SERVICE_VALIDATION`). An in-place rewrite names each invalidated sidecar and its rebuild command (`data.invalidated_sidecars`) and rebuilds nothing | [parent groups](../format/parent-groups.md) |
| `pulse errors list` | List error codes by domain and/or substring | `--help` |
| `pulse errors lookup` | Message + fixups for one error code | `--help` |
| `pulse examples search` | Search the embedded request-example library | `--help` |
| `pulse examples show` | Print a named example's runnable request JSON | `--help` |
| `pulse export arrow` | Export `.pulse` to Arrow IPC | `--help` |
| `pulse export csv` | Export `.pulse` to CSV | `--help` |
| `pulse export excel` | Export `.pulse` to Excel | `--help` |
| `pulse export jsonarray` | Export `.pulse` to a JSON array | `--help` |
| `pulse export ndjson` | Export `.pulse` to NDJSON | `--help` |
| `pulse export parquet` | Export `.pulse` to Parquet | `--help` |
| `pulse export predict` | Validate an export without writing output | [export spss](export-spss.md) |
| `pulse export spss` | Export `.pulse` to SPSS `.sav` | [export spss](export-spss.md) |
| `pulse export transfer` | Compress a cohort or shard archive (any format version) into a zstd transfer artifact, `<input>.zst` by default; `--level` 1..22 (default 3). Transport only — the artifact is never opened as a cohort | [transfer compression](../format/transfer.md) |
| `pulse export tsv` | Export `.pulse` to TSV | `--help` |
| `pulse import arrow` | Import Arrow IPC into `.pulse` | `--help` |
| `pulse import auto` | Auto-detect a source format into the managed pool; carries the per-format read knobs `--sheet` (Excel) and `--charset` (SPSS), `--group` parent-group declarations, and deliberately not `--spss-missing` | [import spss](import-spss.md) |
| `pulse import csv` | Import CSV into `.pulse` | `--help` |
| `pulse import drop` | Remove a managed-import handle | `--help` |
| `pulse import excel` | Import Excel into `.pulse` | `--help` |
| `pulse import jsonarray` | Import a JSON array into `.pulse` | `--help` |
| `pulse import list` | List managed-import handles and TTL status | `--help` |
| `pulse import ndjson` | Import NDJSON into `.pulse` | `--help` |
| `pulse import parquet` | Import Parquet into `.pulse` | `--help` |
| `pulse import predict` | Validate an import without writing output | [import spss](import-spss.md) |
| `pulse import schema-template` | Emit an editable schema template from input data | [import spss](import-spss.md) |
| `pulse import spss` | Import SPSS `.sav` / `.zsav` into `.pulse` | [import spss](import-spss.md) |
| `pulse import transfer` | Decompress a `pulse export transfer` artifact into a byte-identical `.pulse` at rest (temp file, fsync, rename); refuses an existing `--output` without `--overwrite` | [transfer compression](../format/transfer.md) |
| `pulse import tsv` | Import TSV into `.pulse` | `--help` |
| `pulse index build` | Build a point-lookup sidecar index | [index](index.md) |
| `pulse index drop` | Remove a cohort's sidecar index (destructive, no prompt) | [index](index.md) |
| `pulse index list` | List every sidecar index built for a cohort | [index](index.md) |
| `pulse index verify` | Report whether a cohort's sidecar index is fresh | [index](index.md) |
| `pulse mcp` | Run the MCP server over stdio | [mcp](mcp.md) |
| `pulse profile create` | Create a profile JSON for an existing cohort; carries the capture flags `--include-correlations`, `--conditional`, `--fit-shape`, `--fit-models` (one linear model per numeric field, so several categoricals can condition the same field) and `--residual-correlations` (the correlation submatrix among those models' residuals; requires `--fit-models`), plus `--suggest-rules <path>`, which detects structural gating relationships, co-missing question blocks and exact single-source dependencies on the same scan and writes them as a standalone rules file for review, and `--run-continuation`, which measures per-field run-continuation (the run-skip decode's hit rate) to show whether the cohort still has the sort order that makes scans fast. Profiles a single-file cohort or a whole shard archive | [profile create](profile-create.md) |
| `pulse schema` | Print the payload JSON Schema (raw, not envelope-wrapped) | [schema](schema.md) |
| `pulse shard add` | Append a shard to an existing archive | `--help` |
| `pulse shard compact` | Rewrite an archive to reclaim orphan bytes | `--help` |
| `pulse shard create` | Create a shard archive from single-file shards | `--help` |
| `pulse shard extract` | Write a shard's standalone `.pulse` bytes to stdout | `--help` |
| `pulse shard list` | List shards inside an archive | `--help` |
| `pulse shard remove` | Remove a shard from an archive by basename | `--help` |
| `pulse shard verify` | Re-validate every shard against the canonical schema | `--help` |
| `pulse skills list` | List every embedded skill | `--help` |
| `pulse skills show` | Print one skill's markdown | `--help` |
| `pulse synth from-profile` | Generate a synthetic cohort from a captured profile; `--emit-spec` writes the derived spec (the one that actually generated) and `--rules` applies a standalone structural-rules file to it; `--fidelity-report` additionally scores how much of the captured structure survived, including the `models` and `model_residual_correlations` sections a `--fit-models` profile earns | [synth from-profile](synth-from-profile.md) |
| `pulse synth from-schema` | Generate a cohort from a JSON schema/spec | [synth from-schema](synth-from-schema.md) |
| `pulse version` | Print the build version as `pulse <version>`; `--json` emits the standard envelope whose `data` carries the build version, Go version, VCS commit and commit time (both omitted when the binary carries no VCS metadata) and the envelope `format_version`. Same build version as `pulse --version` and the root manifest | `--help` |
| `pulse widen` | Widen a set column of a single-file cohort to a wider set rung (`--field`, `--to`), rewriting the cohort in place. Destructive and non-interactive, but atomic — temp file, fsync, rename — so a refusal or a failure leaves the cohort byte-identical. Refuses a narrower or equal target, a non-set column, a column already at `set_u256`, and a shard archive (whose widen spans the canonical schema plus every shard payload, so it is not this leaf's operation); each refusal carries its own error code, usable with `pulse errors lookup`. On success it names any sidecar the rewrite invalidated — the point-lookup index, the SPSS metadata sidecar — and the exact command that rebuilds it (`data.invalidated_sidecars` under `--json`); it rebuilds nothing, and prints nothing when there are none | `--help` |

## Help

Every command supports `--help`:

```bash
pulse --help
pulse api --help
pulse api process --help
pulse mcp --help
```

`--help` output is the urfave/cli v3 default — a usage block,
description, flag list, and an examples block where applicable.

## Cross-references

| If you need… | Go to |
|---|---|
| Per-command synopsis & examples | [CLI Tour](../getting-started/cli-tour.md) and each `cli/` page |
| Library-side equivalents | [Library Embedding](../library/overview.md) |
| MCP-side equivalents | [How LLMs Use Pulse](../mcp/index.md) |
| Envelope and error code semantics | [Troubleshooting](../ops/troubleshooting.md) and the `pulse_errors_lookup` MCP tool / `pulse errors lookup CODE` CLI |
