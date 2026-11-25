# CLI command and flag reference

<!-- Generated from the command tree (internal/cli) by `make docs`; do not edit. -->

Every runnable `pulse` command with the flags it accepts, read from the command tree the binary mounts, so this page and `--help` always agree. Concepts several commands share (environment variables, the `--json` envelope, exit status, parent groups, source zones, labels) are explained in [CLI concepts](flags.md); [Flags by name](#flags-by-name) lists the commands that mount each flag.

## Global flags

Every command accepts these as well as its own (see [Global flags](flags.md#global-flags)). With no command, `--json` prints the manifest. `--help` / `-h` is on every command.

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output self-describing manifest as JSON |
| `--slim` |  |  | Drop prose descriptions from the manifest payload (smaller for size-sensitive clients) |
| `--intent` | string |  | With --json, scope the manifest to one intent-taxonomy ID (e.g. compare_groups): only its operators and skills |
| `--log-level` | string | `off` | Structured log level written to stderr (off\|debug\|info\|warn\|error); off logs nothing |
| `--log-format` | string | `text` | Structured log format on stderr (text\|json); ignored while --log-level is off |
| `--shard-workers` | int |  | Per-shard parallel workers over a shard archive (Options.ShardWorkers): 0 = one per CPU, 1 = serial |
| `--decode-workers` | int |  | Parallel decode workers over a large single .pulse file (Options.DecodeWorkers): 0 = one per CPU, 1 = serial |
| `--limit` | string, repeatable |  | Instance resource limit as name=value (repeatable; names: request_timeout, max_groups, max_crosstab_cells, max_estimated_memory, max_matrix_dim, max_compose_slots, max_chain_stages, max_join_build_rows, max_derived_columns, max_expanded_items, max_mmm_geos, max_mmm_draw_values, max_disclosure_comparisons). Values: a positive integer, a Go duration for request_timeout (30s), unlimited (or -1), or 0 for the default. Under pulse mcp it overrides the feature profile's limits section per key; an unknown name or bad value is a usage error (CLI_INPUT). |
| `--memory-limit` | string |  | Go soft memory limit for the process (debug.SetMemoryLimit): a byte size as GOMEMLIMIT spells it (14GiB, 14000MiB; units B, KiB, MiB, GiB, TiB) or N% of the memory ceiling (85%): physical RAM, or the cgroup v2 memory.max when that is smaller (a container's limit). Wins over GOMEMLIMIT; unset leaves the runtime untouched; a bad value is a usage error (CLI_INPUT). |
| `--help`, `-h` |  |  | show help |
| `--version`, `-v` |  |  | print the version |

## Commands

### `pulse api compose`

Execute multiple processing requests in batch

Usage: `pulse api compose [options]`

In depth: [api compose](api-compose.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Composed request JSON file path |
| `--json` |  |  | Output the result as a JSON envelope |
| `--stream` |  |  | Stream rows as NDJSON; each line is {"index":N,"row":{...}}; a batch that warned adds one {"warnings":[...]} line after the rows; a ranked sweep ends with one {"ranking":[...]} line (plus "ranking_profile" under rank.profile, "sweep_trace" under an adaptive mode); optimize blocks end with one {"optimizations":[...]} line |
| `--stream-fits` |  |  | Under --stream, end the stream with one {"fits":{...}} line (the buffered fits object, after every other line) when the request declares fits; ignored without --stream |
| `--parallel` | int | `1` | Run requests concurrently with up to N workers (0 = GOMAXPROCS); 1 forces sequential |
| `--no-fail-fast` |  |  | Aggregate errors instead of cancelling on first failure (parallel only) |
| `--no-defaults` |  |  | Disable smart operator defaults; require an explicit Type on every aggregation and grouper |
| `--echo-request` |  |  | Include the normalized ComposedRequest on the envelope under "request". Each slot reflects its post-defaults form. Streaming output skips this. |
| `--no-components` |  |  | Suppress Response.Components on every per-slot Response in ComposedResponse.Responses. Wire form is byte-identical to the pre-Components baseline. |

### `pulse api facet`

Return distinct values for a field in a cohort, or a rich multi-field summary

Usage: `pulse api facet [options]`

In depth: [api facet](api-facet.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path |
| `--field`, `-f` | string, repeatable |  | Field name to facet (repeat for multiple) |
| `--request`, `-r` | string |  | Full FacetRequest JSON file (overrides flags) |
| `--top-k` | int |  | Cap discrete values per field |
| `--percentile` | float, repeatable |  | Numeric percentile (repeatable, in (0,1)) |
| `--histogram` |  |  | Include numeric histograms |
| `--histogram-bins` | int |  | Histogram bin count (default 20) |
| `--histogram-min` | float |  | Histogram lower bound (required with --histogram) |
| `--histogram-max` | float |  | Histogram upper bound (required with --histogram) |
| `--additive` | string, repeatable |  | Compute additive contribution counts for this field (repeatable) |
| `--labels` | string, repeatable |  | Label binding on a categorical field, or an integer-coded u4/u8/u16/u32 field in augment mode: field=table[:replace\|augment]. Repeatable. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--json` |  |  | Output the result as a JSON envelope |
| `--echo-request` |  |  | Include the resolved FacetRequest on the envelope under "request". |

### `pulse api lookup`

Look up row(s) by an indexed key against a cohort's prebuilt sidecar index (see `pulse index build`)

Usage: `pulse api lookup [options]`

In depth: [api lookup](api-lookup.md), [point lookup](../library/point-lookup.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path |
| `--key`, `-k` | string, repeatable |  | Key column=value pair to match; repeat for a composite key in the same column order the sidecar index was built with (e.g. --key region=EU --key date=2026-01-01) |
| `--return` | string |  | Comma-separated field names to project into the result row(s); omit for every schema field |
| `--mode` | string |  | Multiplicity mode when the key matches more than one row, one of assert_unique, first or all: assert_unique (default; also spelled assert-unique) errors PULSE_LOOKUP_AMBIGUOUS |
| `--request`, `-r` | string |  | Full LookupRequest JSON file (flags layer on top of the file) |
| `--json` |  |  | Output the result as a JSON envelope |
| `--echo-request` |  |  | Include the resolved LookupRequest on the envelope under "request". |

### `pulse api materialize`

Write a new cohort holding the request's filter-passing source records plus one f64 column per derived output (features, attributes, windows), so later requests read them as ordinary fields (atomic)

Usage: `pulse api materialize [options]`

In depth: [Materialize](../library/materialize.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Request JSON file path (cohort, label, features, filterers, attributes, windows, sort, weight, time_zone, output, disable_components only) |
| `--out`, `-o` | string |  | Path of the new .pulse cohort to write (required unless --predict) |
| `--overwrite` |  |  | Replace an existing cohort at --out (atomic); its now-stale sidecars are listed, never rebuilt |
| `--strict` |  |  | Fail with PULSE_FIELD_DESCRIPTION_LOW_QUALITY (nothing written) when an output field's description is weak, instead of warning; with --predict, promote every predict warning into an error |
| `--predict` |  |  | Validate without executing: print the output schema the run would write, every static refusal and the data-dependent risks, reading the source's header and schema only; nothing is written |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse api predict`

Validate a request against a cohort without executing

Usage: `pulse api predict [options]`

In depth: [api predict](api-predict.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Request JSON file path |
| `--json` |  |  | Output the result as a JSON envelope |
| `--strict` |  |  | Treat warnings as errors |
| `--echo-request` |  |  | Include the normalized request on the envelope under "request" (distinct from PredictResult.Request, which echoes the raw input). |

### `pulse api predict-chain`

Validate a process-chain request (each stage over the schema the one before produces) without executing

Usage: `pulse api predict-chain [options]`

In depth: [api predict](api-predict.md#compose-facet-and-chain-requests)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Chain request JSON file path |
| `--json` |  |  | Output the result as a JSON envelope |
| `--strict` |  |  | Treat warnings as errors |
| `--echo-request` |  |  | Include the root's normalized request on the envelope under "request" (distinct from data.request, which echoes the input as written). |

### `pulse api predict-compose`

Validate a Compose batch (each slot over its own cohort, then the batch overlays) without executing

Usage: `pulse api predict-compose [options]`

In depth: [api predict](api-predict.md#compose-facet-and-chain-requests)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Composed request JSON file path |
| `--json` |  |  | Output the result as a JSON envelope |
| `--strict` |  |  | Treat warnings as errors |
| `--echo-request` |  |  | Include the root's normalized request on the envelope under "request" (distinct from data.request, which echoes the input as written). |

### `pulse api predict-facet`

Validate a facet request against its cohort without executing

Usage: `pulse api predict-facet [options]`

In depth: [api predict](api-predict.md#compose-facet-and-chain-requests)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Facet request JSON file path |
| `--json` |  |  | Output the result as a JSON envelope |
| `--strict` |  |  | Treat warnings as errors |
| `--echo-request` |  |  | Include the root's normalized request on the envelope under "request" (distinct from data.request, which echoes the input as written). |

### `pulse api process`

Execute a processing request against a cohort

Usage: `pulse api process [options]`

In depth: [api process](api-process.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Request JSON file path |
| `--json` |  |  | Output the result as a JSON envelope |
| `--stream` |  |  | Stream rows as NDJSON (one row per line) instead of buffering; a run that warned ends with one {"warnings":[...]} line |
| `--no-defaults` |  |  | Disable smart operator defaults; require an explicit Type on every aggregation and grouper |
| `--strict` |  |  | Promote request-validation warnings into hard errors (e.g. numeric aggregation on a categorical field) |
| `--echo-request` |  |  | Include the normalized (post-defaults) request on the envelope under "request". Streaming output skips this — NDJSON has no envelope. |
| `--no-components` |  |  | Suppress Response.Components (per-aggregator / per-grouper / per-filterer / crosstab / run constituent-parts metadata). Wire form is byte-identical to the pre-Components baseline. |
| `--no-project` |  |  | Disable buffered-decode field projection; force full-record decode. Projection is on by default and output-transparent (same result, only faster), so this is an opt-out for debugging or full-map decode. |

### `pulse api process-chain`

Execute a source-rooted linear chain of mergeable processing stages

Usage: `pulse api process-chain [options]`

In depth: [api process](api-process.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | **Required.** Chain request JSON file path |
| `--json` |  |  | Output the result as a JSON envelope |
| `--no-defaults` |  |  | Disable smart operator defaults; require an explicit Type on every aggregation and grouper |
| `--echo-request` |  |  | Include the normalized ChainRequest on the envelope under "request". Each stage's Request reflects the per-stage post-defaults form captured during execution. |
| `--no-components` |  |  | Suppress Response.Components on every per-stage Response. Wire form is byte-identical to the pre-Components baseline. |

### `pulse api sample`

Return sample rows from a cohort

Usage: `pulse api sample [options]`

In depth: [api sample](api-sample.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (required unless --request) |
| `--request`, `-r` | string |  | Full SampleRequest JSON file, or - for stdin (cohort or union, union_source_field, n, labels); in place of --input. --count and --labels layer on top of the file |
| `--count`, `-n` | int | `10` | Number of rows to sample (with --request: overrides the file's n, the default when it has none) |
| `--json` |  |  | Output the result as a JSON envelope |
| `--labels` | string, repeatable |  | Label binding on a categorical field, or an integer-coded u4/u8/u16/u32 field in augment mode: field=table[:replace\|augment]. Repeatable. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--echo-request` |  |  | Include the resolved SampleRequest on the envelope under "request". |

### `pulse cohort filter`

Filter a .pulse file (single-file or shard archive) to a new .pulse file

Usage: `pulse cohort filter [options]`

In depth: [filter to a file](../library/filter-to-file.md), [--time-zone](flags.md#--time-zone-cohort-filter)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--filter` | string |  | Filter expression (FILTER_EXPRESSION semantics) |
| `--include-from` | string |  | Path to newline-delimited file of values to include |
| `--include-field` | string |  | Field name whose value is tested against --include-from |
| `--time-zone` | string |  | IANA zone (UTC or Area/Location) a to_day() call in --filter reads; omitted reads UTC. An unknown name is PULSE_TIMEZONE_UNKNOWN and nothing is written |
| `--flatten` |  |  | Write a shard archive input as ONE single-file .pulse (canonical schema, rows in archive order); --filter / --include-from become optional. A no-op on a single-file input |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse cohort inspect`

Inspect a .pulse file header and schema

Usage: `pulse cohort inspect [options] PATH`

In depth: [cohort inspect](cohort-inspect.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--json` |  |  | Output the result as a JSON envelope |
| `--full-dict` |  |  | Show full categorical dictionaries without truncation |

### `pulse cohort zone clear`

Remove the cohort's default per-row zone field, rewriting the cohort's schema in place (non-interactive, atomic; a cohort set then cleared is byte-identical to the original)

Usage: `pulse cohort zone clear [options] COHORT`

In depth: [default zone field](flags.md#--zone-field-cohort-default-zone)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse cohort zone set`

Declare a categorical field of IANA zone names as the cohort's default per-row zone field, rewriting the cohort's schema in place (non-interactive, atomic; a shard archive is rewritten whole, with a warning)

Usage: `pulse cohort zone set [options] COHORT FIELD`

In depth: [default zone field](flags.md#--zone-field-cohort-default-zone)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the first positional argument) |
| `--field`, `-f` | string |  | The categorical zone field to declare (or pass as the last positional argument) |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse completion bash`

Output bash completion script

Usage: `pulse completion bash`

In depth: [completion](completion.md)

No flags of its own.

### `pulse completion fish`

Output fish completion script

Usage: `pulse completion fish`

In depth: [completion](completion.md)

No flags of its own.

### `pulse completion zsh`

Output zsh completion script

Usage: `pulse completion zsh`

In depth: [completion](completion.md)

No flags of its own.

### `pulse convert`

Convert between tabular formats (auto-detects from extensions)

Usage: `pulse convert [options] INPUT OUTPUT`

In depth: [import spss](import-spss.md), [export spss](export-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--from` | string |  | Source format override (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss) |
| `--to` | string |  | Target format override (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss) |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--charset` | string |  | Character encoding override for SPSS .sav input (e.g. windows-1252, latin1, utf-8); ignored for any other input format |
| `--spss-missing` | string | `auto` | How SPSS numeric user-missing values are represented, one of auto or null: auto (default) is a null plus a &lt;var&gt;_missing sibling carrying the reason; null is a plain null, the reason not preserved |
| `--ignore-sidecar` |  |  | Do not read the SPSS metadata sidecar beside the source cohort; synthesise the .sav dictionary from the .pulse schema alone. Suppresses the READ, not merely the staleness verdict — an unreadable sidecar cannot block the export either. Cannot round-trip a cohort whose derived set_\* column is still present (see the docs). |
| `--uncompressed` |  |  | Write the .sav data section as flat 8-byte elements instead of SPSS's bytecode compression. Both encodings are losslessly equivalent; bytecode is the default because it is what SPSS's own SAVE writes. Does not select ZSAV — that is not emitted. |
| `--sanitize-names` |  |  | Rewrite cohort field names that cannot be SPSS variable names (a space, bracket, hyphen or leading digit) instead of refusing the export. Every rename is reported as a PULSE_SPSS_NAME_SANITIZED warning. Only affects a synthesised dictionary — names from a sidecar came from SPSS and are already legal. |
| `--keep-pulse` | string |  | Also write intermediate .pulse file at this path |
| `--strict` |  |  | Treat dropped rows (PULSE_IMPORT_ROWS_DROPPED: rows the target refused, or rows the --keep-pulse import skipped), and a value the target cannot store exactly (PULSE_SPSS_INTEGER_INEXACT), as an error: the convert fails and writes no target or intermediate |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--json` |  |  | Output the result as a JSON envelope |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone. A zoned naive value is written to the target as its UTC instant (…Z) |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the --keep-pulse cohort's default per-row zone field (as 'import &lt;format&gt; --zone-field'); requires --keep-pulse — the target file carries no schema |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field of the resolved schema: field=table[:replace\|augment]. Repeatable. Labels the converted rows (every target, .sav included); refused before anything is written with the export's codes. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |

### `pulse convert predict`

Validate a conversion without writing output

Usage: `pulse convert predict [options] INPUT OUTPUT`

In depth: [export spss](export-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--from` | string |  | Source format override (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss) |
| `--to` | string |  | Target format override (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss) |
| `--charset` | string |  | Character encoding override for SPSS .sav input (e.g. windows-1252, latin1, utf-8); ignored for any other input format |
| `--spss-missing` | string | `auto` | How SPSS numeric user-missing values are represented, one of auto or null: auto (default) is a null plus a &lt;var&gt;_missing sibling carrying the reason; null is a plain null, the reason not preserved |
| `--ignore-sidecar` |  |  | Do not read the SPSS metadata sidecar beside the source cohort; synthesise the .sav dictionary from the .pulse schema alone. Suppresses the READ, not merely the staleness verdict — an unreadable sidecar cannot block the export either. Cannot round-trip a cohort whose derived set_\* column is still present (see the docs). |
| `--uncompressed` |  |  | Write the .sav data section as flat 8-byte elements instead of SPSS's bytecode compression. Both encodings are losslessly equivalent; bytecode is the default because it is what SPSS's own SAVE writes. Does not select ZSAV — that is not emitted. |
| `--sanitize-names` |  |  | Rewrite cohort field names that cannot be SPSS variable names (a space, bracket, hyphen or leading digit) instead of refusing the export. Every rename is reported as a PULSE_SPSS_NAME_SANITIZED warning. Only affects a synthesised dictionary — names from a sidecar came from SPSS and are already legal. |
| `--json` |  |  | Output the result as a JSON envelope |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--labels` | string, repeatable |  | Label binding to check, as 'pulse convert --labels' takes it: field=table[:replace\|augment]. Repeatable. Predicts every label refusal the convert would raise (an unregistered table, an unknown field, a field bound twice, a field neither categorical nor integer-coded u4-u32, an integer field's non-decimal table key) with its own code. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |

### `pulse dedup`

Deduplicate an existing cohort's repeated parent blocks into parent groups (format 0x02), in place or to --out (DESTRUCTIVE in place, non-interactive; the rewrite is atomic, so a failure or refusal leaves the cohort byte-identical)

Usage: `pulse dedup [options] COHORT`

In depth: [parent groups](../format/parent-groups.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag. An already-grouped cohort is regrouped from scratch |
| `--out`, `-o` | string |  | Write the deduplicated cohort to this NEW path (must not exist) and leave the input untouched; default rewrites the input in place |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) as errors: nothing is written |
| `--suggest-groups` |  |  | Detect candidate parent groups over the cohort's records and print each measured candidate with a ready-to-paste --group value. Alone it writes nothing |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse docs export`

Write the instance's analysis reference as a deterministic Markdown tree with an mdBook SUMMARY.md fragment

Usage: `pulse docs export [options]`

In depth: [exporting the reference](../library/guided-analysis.md#exporting-the-reference)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--out` | string |  | **Required.** Directory to write the reference into (host OS path) |
| `--feature-profile` | string |  | Scope the reference to the feature profile at this host OS path |
| `--no-skills` |  |  | Leave out skills.md and the skills/ tree |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse errors list`

List error codes filtered by domain and/or substring query

Usage: `pulse errors list [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--domain` | string |  | Restrict to a single domain (CLI, DATA, ENCODING, PROCESSING, PULSE, SERVICE); case-insensitive |
| `--query` | string |  | Case-insensitive substring search across descriptions and fixup hints |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse errors lookup`

Print metadata (Message + Fixups) for one error code

Usage: `pulse errors lookup [options] CODE`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse examples search`

Search the embedded request-example library

Usage: `pulse examples search [options] [WORDS...]`

In depth: [example library](../examples/library.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--query` | string |  | Plain words (or pass them as arguments): one word is a case-insensitive substring of name, description or operators; several must each match a whole word of name, description, operators, intents or tags; operator aliases and intent phrasings, then near-miss spellings, match when nothing matches literally |
| `--intent` | string |  | Intent-taxonomy ID the examples must declare (pulse skills show intents lists them); an unknown one is PULSE_RECOMMEND_INTENT_UNKNOWN |
| `--tag` | string, repeatable |  | Filter by canonical taxonomy tag (repeatable; results must carry every tag) |
| `--category` | string |  | Exact directory match: aggregations, attributes, compose, crosstab, facet, features, filterers, groupers, materialize, matrices, overlays, regression, tests, windows |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse examples show`

Print the runnable request JSON for a named example

Usage: `pulse examples show [options] NAME`

In depth: [example library](../examples/library.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Wrap the example record in a JSON envelope (default prints just the runnable body) |

### `pulse explain`

Say in plain words what a request will do (never run; predict-checked when it names a cohort), what a response found, or how an operator is validated

Usage: `pulse explain [options] [OPERATOR]`

In depth: [explain](../library/guided-analysis.md#explain-from-a-request-or-a-result-to-plain-language)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--request`, `-r` | string |  | Request JSON file. Alone: described before it runs. With --response: the request that produced it |
| `--response` | string |  | Result JSON file to read into findings (a --json envelope is unwrapped to its data) |
| `--root` | string | `request` | Which root the files hold, one of request, composed, chain, facet or sample (request is a process request; sample takes --request only) |
| `--detail` | string |  | One of terse or full: terse (default) is the summary and structured parts; full adds narrative sentences, glossary refs, follow-ups and assumptions |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export arrow`

Export .pulse to arrow format

Usage: `pulse export arrow [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export csv`

Export .pulse to csv format

Usage: `pulse export csv [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export excel`

Export .pulse to excel format

Usage: `pulse export excel [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export jsonarray`

Export .pulse to jsonarray format

Usage: `pulse export jsonarray [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export ndjson`

Export .pulse to ndjson format

Usage: `pulse export ndjson [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export parquet`

Export .pulse to parquet format

Usage: `pulse export parquet [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export predict`

Validate an export without writing output

Usage: `pulse export predict [options]`

In depth: [export spss](export-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--format`, `-f` | string |  | Output format to validate against (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss). Omit to validate the source cohort only, with no target-format check. |
| `--ignore-sidecar` |  |  | Do not read the SPSS metadata sidecar beside the source cohort; synthesise the .sav dictionary from the .pulse schema alone. Suppresses the READ, not merely the staleness verdict — an unreadable sidecar cannot block the export either. Cannot round-trip a cohort whose derived set_\* column is still present (see the docs). |
| `--uncompressed` |  |  | Write the .sav data section as flat 8-byte elements instead of SPSS's bytecode compression. Both encodings are losslessly equivalent; bytecode is the default because it is what SPSS's own SAVE writes. Does not select ZSAV — that is not emitted. |
| `--charset` | string |  | Character encoding the emitted .sav is written in, and declared as, e.g. windows-1252 or utf-8. Default: the charset the SOURCE declared, or UTF-8 for a cohort with no SPSS provenance. Set it when the cohort now holds text the source's codepage cannot express. |
| `--sanitize-names` |  |  | Rewrite cohort field names that cannot be SPSS variable names (a space, bracket, hyphen or leading digit) instead of refusing the export. Every rename is reported as a PULSE_SPSS_NAME_SANITIZED warning. Only affects a synthesised dictionary — names from a sidecar came from SPSS and are already legal. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--labels` | string, repeatable |  | Categorical label binding to check, as the export leaves take it: field=table[:replace\|augment]. Repeatable. Predicts every export's label refusals (a field neither categorical nor integer-coded u4-u32, an integer field's non-decimal table key, an unregistered table) and, with --format spss, the .sav value-label ones too (unknown field, a field bound twice, a binding beside the SPSS metadata sidecar), with the export's own codes. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export spss`

Export .pulse to SPSS .sav format

Usage: `pulse export spss [options]`

In depth: [export spss](export-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |
| `--ignore-sidecar` |  |  | Do not read the SPSS metadata sidecar beside the source cohort; synthesise the .sav dictionary from the .pulse schema alone. Suppresses the READ, not merely the staleness verdict — an unreadable sidecar cannot block the export either. Cannot round-trip a cohort whose derived set_\* column is still present (see the docs). |
| `--uncompressed` |  |  | Write the .sav data section as flat 8-byte elements instead of SPSS's bytecode compression. Both encodings are losslessly equivalent; bytecode is the default because it is what SPSS's own SAVE writes. Does not select ZSAV — that is not emitted. |
| `--charset` | string |  | Character encoding the emitted .sav is written in, and declared as, e.g. windows-1252 or utf-8. Default: the charset the SOURCE declared, or UTF-8 for a cohort with no SPSS provenance. Set it when the cohort now holds text the source's codepage cannot express. |
| `--sanitize-names` |  |  | Rewrite cohort field names that cannot be SPSS variable names (a space, bracket, hyphen or leading digit) instead of refusing the export. Every rename is reported as a PULSE_SPSS_NAME_SANITIZED warning. Only affects a synthesised dictionary — names from a sidecar came from SPSS and are already legal. |
| `--strict` |  |  | Treat a value the .sav cannot store exactly (PULSE_SPSS_INTEGER_INEXACT: a u64 past 2^53, which no SPSS double holds) as an error: the export fails and writes no .sav. The CLI spelling of Options.Strict / ExportJob.Strict; without it the value is written as the nearest double and warned. |

### `pulse export transfer`

Compress a .pulse cohort or shard archive into a zstd transfer artifact (.pulse.zst); transport only — decompress with `pulse import transfer` before opening

Usage: `pulse export transfer [options]`

In depth: [transfer compression](../format/transfer.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort or shard archive |
| `--output`, `-o` | string |  | Output transfer artifact path (conventionally &lt;input&gt;.zst); overwritten |
| `--level` | int | `3` | zstd level 1..22 (1-2 fastest, 3-5 default, 6-9 better, 10-22 best) |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse export tsv`

Export .pulse to tsv format

Usage: `pulse export tsv [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Path of the exported file to write (host OS path) |
| `--include` | string, repeatable |  | Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order. |
| `--labels` | string, repeatable |  | Label binding on a categorical or integer-coded u4/u8/u16/u32 field: field=table[:replace\|augment]. Repeatable. An integer field's table is keyed by its values' decimal spellings (else PULSE_LABEL_KEY_INVALID); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration. |
| `--tz` | string |  | Render datetime columns on this IANA zone's wall clock with its offset (e.g. Asia/Kolkata -&gt; 2026-03-29T08:00:00+05:30) instead of UTC ...Z. Re-imports to the same instants; date columns never move. Omit, or UTC, for byte-identical UTC output. Refused by spss (a .sav DATETIME has no offset). |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse features check`

Validate a feature profile file against this build, exactly as pulse.New would

Usage: `pulse features check [options] FILE`

In depth: [feature profiles](../library/feature-profiles.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse features diff`

List the features this build offers that a feature profile does not list, and the names it does not resolve

Usage: `pulse features diff [options] FILE`

In depth: [feature profiles](../library/feature-profiles.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse features init`

Print a feature profile listing every feature this build offers, or seeded from an example feature profile

Usage: `pulse features init [options]`

In depth: [feature profiles](../library/feature-profiles.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--from` | string |  | Seed from the named example feature profile |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse features show`

Describe every feature a feature profile lists: kind, category, source, since and dependencies

Usage: `pulse features show [options] FILE`

In depth: [feature profiles](../library/feature-profiles.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse import arrow`

Import arrow file into .pulse format

Usage: `pulse import arrow [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |

### `pulse import auto`

Auto-detect a source format and import into the managed pool

Usage: `pulse import auto [options] SOURCE`

In depth: [import spss](import-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--format`, `-f` | string |  | Format override (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss, pulse) |
| `--handle` | string |  | Managed handle name (defaults to source basename) |
| `--ttl` | string | `7d` | Lifetime in the managed pool: Go duration (24h, 30m), day form (7d), or "pin" |
| `--sheet` | string |  | Excel sheet to read; omit for the first sheet (ignored for non-Excel input) |
| `--charset` | string |  | Character encoding override for SPSS .sav input (e.g. windows-1252, latin1, utf-8); ignored for any other input format |
| `--overwrite` |  |  | Replace an existing managed handle |
| `--schema` | string |  | Schema JSON file path (as on 'import &lt;format&gt; --schema'; refused on a .pulse source) |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--strict` |  |  | Treat a partial import (PULSE_IMPORT_ROWS_DROPPED) as an error: the import fails and writes neither the cohort nor its sidecar |
| `--group` | string, repeatable |  | Declare a parent group, as on 'import &lt;format&gt; --group': KEY[,KEY...]:MEMBER[,MEMBER...] or MEMBER[,MEMBER...]. Repeatable, one group per flag; judged at the default ratio floor, findings reported as warnings (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |
| `--json` |  |  | Output the result as a JSON envelope |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |

### `pulse import csv`

Import csv file into .pulse format

Usage: `pulse import csv [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |

### `pulse import drop`

Remove a managed-import handle from the pool

Usage: `pulse import drop [options] HANDLE`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse import excel`

Import Excel file into .pulse format

Usage: `pulse import excel [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |
| `--sheet` | string |  | Excel sheet to read; omit for the first sheet (ignored for non-Excel input) |

### `pulse import jsonarray`

Import jsonarray file into .pulse format

Usage: `pulse import jsonarray [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |

### `pulse import list`

List managed-import handles and their TTL status

Usage: `pulse import list [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse import ndjson`

Import ndjson file into .pulse format

Usage: `pulse import ndjson [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |

### `pulse import parquet`

Import parquet file into .pulse format

Usage: `pulse import parquet [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |

### `pulse import predict`

Validate an import without writing output

Usage: `pulse import predict [options]`

In depth: [import spss](import-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--format`, `-f` | string |  | Input format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss) |
| `--sheet` | string |  | Excel sheet to read; omit for the first sheet (ignored for non-Excel input) |
| `--charset` | string |  | Character encoding override for SPSS .sav input (e.g. windows-1252, latin1, utf-8); ignored for any other input format |
| `--spss-missing` | string | `auto` | How SPSS numeric user-missing values are represented, one of auto or null: auto (default) is a null plus a &lt;var&gt;_missing sibling carrying the reason; null is a plain null, the reason not preserved |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--suggest-groups` |  |  | Detect candidate parent groups (a key and the fields it determines) and measure each over every row: ratio, resident dictionary bytes, projected file size and a ready-to-paste --group value. Suggests only; nothing is declared |
| `--group` | string, repeatable |  | Evaluate a parent-group declaration exactly as 'import &lt;format&gt; --group' would apply it (same syntax, repeatable): its verdict and measured figures, or the error the import would fail with |
| `--elide-constants` |  |  | Report the fields 'import &lt;format&gt; --elide-constants' would elide and the bytes saved |
| `--dedup-ratio-floor` | float | `2` | Ratio floor the --group and --suggest-groups verdicts are judged against |
| `--strict` |  |  | Fail as 'import &lt;format&gt; --strict' would when a --group draws a viability warning, or (on the measured pass) when rows would be dropped |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Judge a --zone-field declaration exactly as 'import &lt;format&gt; --zone-field' would (the measured pass builds the dictionaries the import writes) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |

### `pulse import schema-template`

Generate an editable schema template from input data

Usage: `pulse import schema-template [options] INPUT`

In depth: [import spss](import-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--format`, `-f` | string |  | Input format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss) |
| `--sheet` | string |  | Excel sheet to read; omit for the first sheet (ignored for non-Excel input) |
| `--charset` | string |  | Character encoding override for SPSS .sav input (e.g. windows-1252, latin1, utf-8); ignored for any other input format |
| `--spss-missing` | string | `auto` | How SPSS numeric user-missing values are represented, one of auto or null: auto (default) is a null plus a &lt;var&gt;_missing sibling carrying the reason; null is a plain null, the reason not preserved |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |

### `pulse import spss`

Import SPSS .sav / .zsav file into .pulse format

Usage: `pulse import spss [options]`

In depth: [import spss](import-spss.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |
| `--charset` | string |  | Character encoding override for SPSS .sav input (e.g. windows-1252, latin1, utf-8); ignored for any other input format |
| `--spss-missing` | string | `auto` | How SPSS numeric user-missing values are represented, one of auto or null: auto (default) is a null plus a &lt;var&gt;_missing sibling carrying the reason; null is a plain null, the reason not preserved |

### `pulse import transfer`

Decompress a zstd transfer artifact (.pulse.zst) into a byte-identical .pulse at rest

Usage: `pulse import transfer [options]`

In depth: [transfer compression](../format/transfer.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse.zst transfer artifact |
| `--output`, `-o` | string |  | **Required.** Output .pulse path (must not exist unless --overwrite) |
| `--overwrite` |  |  | Replace an existing output cohort |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse import tsv`

Import tsv file into .pulse format

Usage: `pulse import tsv [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Source file to import, in the leaf's format (host OS path) |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--schema` | string |  | Schema JSON file path |
| `--ignore-extra` |  |  | With --schema: drop source columns (and NDJSON / JSON-array keys first seen after the first object) the schema does not name, instead of refusing with PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED / PULSE_IMPORT_SCHEMA_KEY_LATE. Schema fields always match columns by exact name |
| `--sample-rows` | int | `500` | Rows to sample for schema inference (min 50) |
| `--float` | string | `f64` | Width an all-float column infers as, one of f64 or f32: f64 (default) keeps exact decimals; f32 halves the bytes and rounds to ~7 significant digits. Any other value is PULSE_IMPORT_FLOAT_WIDTH_INVALID. Inert with --schema |
| `--json` |  |  | Output the result as a JSON envelope |
| `--elide-constants` |  |  | Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries) |
| `--group` | string, repeatable |  | Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries) |
| `--dedup-ratio-floor` | float | `2` | Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check |
| `--strict` |  |  | Treat import warnings as errors: a partial import (PULSE_IMPORT_ROWS_DROPPED) and parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) fail the import, which writes nothing |
| `--source-tz` | string, repeatable |  | Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone |
| `--source-tz-field` | string |  | Column whose cell holds each row's source zone (same spellings as --source-tz); every naive datetime column not named by a col=Zone --source-tz reads in its own row's zone. A bare --source-tz is the fallback for an empty zone cell (else the row is a row error); an unknown zone name is a PULSE_TIMEZONE_UNKNOWN row error. The zone column itself imports as an ordinary column |
| `--dst-policy` | string | `error` | How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz, one of error, earlier or later: error (default) fails the import naming the first such row, earlier takes the pre-transition offset, later the post-transition one; a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning |
| `--zone-field` | string |  | Declare the written cohort's DEFAULT per-row zone field: a column of IANA zone names (UTC or Area/Location) that imports as categorical. Zone-aware requests then read each row in its own zone unless they name a zone. Never implied by --source-tz-field (they may name the same column). A column that is missing or not categorical is SERVICE_VALIDATION; an entry that is not a zone is PULSE_TIMEZONE_UNKNOWN (writes format 0x02) |
| `--vector` | string, repeatable |  | Fold sibling columns into one native vector field: NAME[:f32\|f64]:COL1,COL2,... (element order as listed) or NAME:/REGEX/ (matching columns in source order; the first capture group labels each element, else the column name). 2..1024 columns, default f64; all elements empty = null vector, some empty = NaN. Repeatable, one vector per flag (writes format 0x02) |
| `--vector-kind` | string, repeatable |  | Declare a --vector's kind: NAME:measure\|scale\|composition\|binary[:MIN:MAX] (MIN:MAX the declared value range). Repeatable; never inferred |

### `pulse index build`

Build a point-lookup sidecar index for one or more key columns

Usage: `pulse index build [options] COHORT`

In depth: [index](index.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--key`, `-k` | string |  | **Required.** Key column(s) to build the index over; comma-separated for a composite key (e.g. region,date) |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse index drop`

Remove a cohort's sidecar point-lookup index (DESTRUCTIVE, non-interactive — no confirmation prompt; the sidecar is a cheap rebuild artifact, so `pulse index build` regenerates it)

Usage: `pulse index drop [options] COHORT`

In depth: [index](index.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--key`, `-k` | string |  | **Required.** Key column(s) the index was built over; comma-separated for a composite key (e.g. region,date). Removes the sidecar immediately — no confirmation prompt. |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse index list`

List every sidecar point-lookup index built for a cohort

Usage: `pulse index list [options] COHORT`

In depth: [index](index.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse index verify`

Report whether a cohort's sidecar point-lookup index is still fresh

Usage: `pulse index verify [options] COHORT`

In depth: [index](index.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--key`, `-k` | string |  | **Required.** Key column(s) the index was built over; comma-separated for a composite key (e.g. region,date) |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse mcp`

Run the Model Context Protocol server over stdio

Usage: `pulse mcp [options]`

In depth: [mcp](mcp.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--data-dir` | string |  | Directory of .pulse cohort files (overrides PULSE_DATA_DIR) Environment: `PULSE_DATA_DIR`. |
| `--no-cohort-scan` |  |  | Skip the startup walk of the data directory that enumerates .pulse files as pulse:// resources. Cohorts stay readable by URI; only the resources/list enumeration is withheld. Environment: `PULSE_MCP_NO_COHORT_SCAN`. |
| `--feature-profile` | string |  | Feature profile JSON file (OS path, relative to the working directory, not the data dir). Falls back to PULSE_FEATURE_PROFILE. An invalid profile fails startup. |
| `--return` | string |  | Response preset (full\|standard\|minimal) for a tool request without its own return block. Unset: the instance or feature-profile return default, else standard. full serves the unshaped library output. |
| `--metrics-addr` | string |  | Serve Prometheus text-format metrics on GET /metrics at this host:port (e.g. 127.0.0.1:9090; :0 picks a free port). Unset opens no port. |
| `--bind-on-open` |  |  | Register session-scoped schema-bound tool variants on successful pulse_inspect (default true). Disable for clients that bind tool schemas themselves. |

### `pulse profile create`

Create a profile JSON for an existing .pulse cohort

Usage: `pulse profile create [options]`

In depth: [profile create](profile-create.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | **Required.** Input .pulse cohort file path |
| `--output`, `-o` | string |  | **Required.** Output profile JSON path |
| `--top-k` | int | `32` | Top-K categorical values to retain per field |
| `--include-stats` |  |  | Include percentile / std stats |
| `--include-correlations` |  |  | Capture pairwise numeric correlations |
| `--correlation-top-k` | int | `16` | Cap on retained correlation pairs |
| `--conditional` |  |  | Capture row-aligned numeric-numeric pair structure (rho + true co-occurrence N) for exact correlation reconstruction; pairs below 30 supporting observations warn rather than refuse |
| `--fit-shape` |  |  | Fit a 2-component Gaussian mixture per numeric field and keep it only when it's a genuine BIC improvement over the plain normal (see skills/synthetic-data.md); a near-normal field is left as normal |
| `--fit-models` |  |  | Fit one linear model per numeric field on the same scan — the field regressed on the cohort's categorical levels, set options and the numeric fields declared before it (drawn first at generation) — keeping the coefficients, residual scale and fitted residuals; a field with no usable predictors or a rank-deficient design is skipped with a warning, never a refusal |
| `--residual-correlations` |  |  | Capture the full correlation submatrix among --fit-models' fitted residuals (every pair, not a top-K sample); a pair with too few co-present rows is recorded as unmeasured with a reason, never as rho=0. Requires --fit-models |
| `--suggest-rules` | string |  | Detect structural rules on the same scan — gating relationships ('set_null'), co-missing question blocks ('null_together') and exact single-source dependencies ('set_expr') — and write them to this path as a standalone rules file (a bare JSON array, the shape 'synth from-profile --rules' consumes). PROPOSED, never applied: read the '_evidence' on each candidate, correct it, delete what you do not believe. Near-miss blocks, almost-determined pairs, always-null and constant columns are reported in the warnings rather than proposed. Dependency detection is bounded to packed_bool/u4 targets and a single low-cardinality source of at most 16 levels. The profile document itself is unchanged |
| `--run-continuation` |  |  | Measure, on the same scan, each field's run-continuation — the fraction of adjacent row pairs whose on-wire bytes and null bit repeat, which is exactly the run-skip decode's hit rate — into the additive 'run_continuation' section, with the high-continuation fields and advice on whether to sort the source upstream (ORDER BY its parent key). Pairs never span a shard boundary |
| `--time-zone` | string |  | Capture every datetime field on this IANA zone's LOCAL clock (day range, weekday, hour and month counts) and rebuild it as a zoned uniform_datetime with dst_policy earlier; exclusive with --time-zone-field. Default: the cohort's declared default zone field, else UTC |
| `--time-zone-field` | string |  | Capture every datetime field on each row's LOCAL clock, read from this categorical field of IANA zone names, and rebuild it with time_zone_field and dst_policy earlier; overrides the cohort's declared default zone field; exclusive with --time-zone |
| `--sample-limit` | int |  | Cap rows ingested for the profile (0 = unlimited) |
| `--seed` | int |  | Deterministic RNG seed for --conditional's categorical-categorical reservoir sampling and --fit-models' residual reservoir (each draws from its own stream) |
| `--json` |  |  | Print envelope to stdout as well |

### `pulse rake`

Compute survey weights — rim weighting (raking) to one or more target margins, cell weighting when a margin joins several fields — and write them as a nullable f64 column of a NEW cohort beside every source column (atomic; the output weights are probability weights)

Usage: `pulse rake [options] COHORT`

In depth: [Rake](../library/rake.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Source .pulse cohort (single file, shard archive or archive#shard; or pass as the positional argument) |
| `--out`, `-o` | string |  | Path of the NEW single-file .pulse cohort to write |
| `--margins`, `-m` | string |  | Target margins: a JSON array literal or the path of a file holding one, e.g. [{"field":"gender","targets":{"F":0.51,"M":0.49}},{"fields":["age","region"],"cells":[{"values":["18-34","NE"],"target":0.2}, ...]}]; each margin may set "targets_as": "shares" (default; sum 1 or 100) or "counts" |
| `--request`, `-r` | string |  | A whole rake request (cohort, out, margins, weight_field, by, base_weight, missing, min_weight, max_weight, max_iter, tol, total, overwrite): a JSON object literal or the path of a file holding one. Every flag set on the command line overrides its key |
| `--weight-field` | string |  | Name of the output weight column (default rake_weight); a name the source already carries needs --overwrite, which replaces that column in place |
| `--by` | string |  | Rake every group of this field (e.g. a wave) separately to the same targets |
| `--base-weight` | string |  | Start from this design-weight field: the output is it times the raking adjustment |
| `--missing` | string |  | Null margin value policy, one of refuse or base: refuse (default) refuses the record with PULSE_RAKE_MISSING_VALUE; base keeps the record's factor for that margin |
| `--min-weight` | float |  | Lower cap on every weight, relative to its group's mean weight (0 = none), applied after every sweep |
| `--max-weight` | float |  | Upper cap on every weight, relative to its group's mean weight (0 = none), applied after every sweep |
| `--max-iter` | int |  | Sweep ceiling (default 100); a run that stops there still writes, with a PULSE_RAKE_NOT_CONVERGED warning |
| `--tol` | float |  | Convergence tolerance on the largest achieved-vs-target share gap (default 1e-6) |
| `--total` | float |  | Normalise every group's weights to sum to this value (default: the group's record count, mean weight 1) |
| `--overwrite` |  |  | Replace an existing cohort at --out (atomic; its now-stale sidecars are listed, never rebuilt) and an existing source column named --weight-field |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse recommend`

Turn a question kind (an intent ID) into ranked draft requests: placeholder skeletons, or predict-validated drafts bound to --cohort's fields

Usage: `pulse recommend [options]`

In depth: [recommend](../library/guided-analysis.md#recommend-from-a-question-to-a-request)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--intent` | string |  | Intent ID naming the kind of question (pulse skills show intents lists them); required |
| `--cohort` | string |  | A .pulse cohort to bind the drafts to; each bound draft has passed predict. Omit for unbound placeholder skeletons |
| `--field` | string, repeatable |  | A field hint (needs --cohort): pins the field to the first role of the intent it fits. Repeatable |
| `--level` | string |  | Rank operators at this level first: basic, intermediate or advanced |
| `--top` | int |  | Maximum recommendations returned (0 = the default, 10); sets RecommendRequest.Limit |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse schema`

Output the JSON Schema for Pulse request/response payloads

Usage: `pulse schema`

In depth: [schema](schema.md)

No flags of its own.

### `pulse shard add`

Append a shard to an existing archive

Usage: `pulse shard add [options] ARCHIVE SHARD`

In depth: [shard archives](../internals/managing-shard-archives.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |
| `--rewrite` |  |  | Rewrite the whole archive (staged file + rename) instead of appending in place; never opens the archive for writing and reclaims dead bytes |

### `pulse shard compact`

Rewrite a shard archive to reclaim orphan bytes and refresh canonical metadata

Usage: `pulse shard compact [options] ARCHIVE`

In depth: [shard archives](../internals/managing-shard-archives.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse shard create`

Create a new shard archive from one or more single-file .pulse shards

Usage: `pulse shard create [options] ARCHIVE`

In depth: [shard archives](../internals/managing-shard-archives.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--include`, `-i` | string, repeatable |  | Single-file .pulse shard to include (repeatable) |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse shard extract`

Extract a shard's standalone .pulse bytes to stdout

Usage: `pulse shard extract ARCHIVE BASENAME`

In depth: [shard archives](../internals/managing-shard-archives.md)

No flags of its own.

### `pulse shard list`

List shards inside an archive

Usage: `pulse shard list [options] ARCHIVE`

In depth: [shard archives](../internals/managing-shard-archives.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse shard remove`

Remove a shard from an archive by basename

Usage: `pulse shard remove [options] ARCHIVE BASENAME`

In depth: [shard archives](../internals/managing-shard-archives.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse shard verify`

Re-validate every shard's header + cohesion against the canonical schema

Usage: `pulse shard verify [options] ARCHIVE`

In depth: [shard archives](../internals/managing-shard-archives.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |
| `--repair` |  |  | First roll back an interrupted in-place shard add (truncate to the journalled length, remove the journal); refuses when the journal does not match the archive |

### `pulse skills list`

List all available skills

Usage: `pulse skills list [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse skills show`

Show a skill's documentation

Usage: `pulse skills show NAME`

No flags of its own.

### `pulse synth from-profile`

Top up a source cohort with rows generated from a previously-captured profile

Usage: `pulse synth from-profile [options]`

In depth: [synth from-profile](synth-from-profile.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--profile`, `-p` | string |  | **Required.** Profile JSON file to synthesize from (as written by pulse profile create) |
| `--source` | string |  | **Required.** Source .pulse cohort the profile was captured from — copied into the output tagged _synthetic=false unless --synthetic-only; never opened for write |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path — must differ from --source |
| `--rows` | int |  | **Required.** Number of NEW rows to generate (not a top-up-to-total target) |
| `--seed` | int |  | Deterministic RNG seed |
| `--fidelity-report` | string |  | Write a JSON fidelity report (per-field TEST_KS/TEST_CHISQ comparison against the source) to this path after generation |
| `--fidelity-model` | string |  | Refit a supplied request's regressions (a pulse process request JSON; its cohort is ignored) on the source and on the generated rows, and report both fits side by side in the fidelity report's utility.model_fits; needs --fidelity-report |
| `--rules` | string |  | Load structural rules from a standalone JSON file (a bare array of rule objects — the same shape as a spec's "rules" key) and apply them to the profile-derived spec; REPLACES any rules the spec carries |
| `--synthetic-only` |  |  | Write only the generated rows: no source row and no _synthetic column, so the output has the source's schema. The source is still read, and --fidelity-report still compares the generated rows against it |
| `--constraints` | string |  | Load row constraints from a standalone JSON file (a bare array of {"expr": ...} objects — the same shape as a spec's "constraints" key) and apply them to the profile-derived spec; a generated row failing any constraint is re-drawn. REPLACES any constraints the spec carries |
| `--max-rejection-rate` | float |  | Ceiling on the fraction of generated rows --constraints may reject before the run is refused PULSE_SYNTH_CONSTRAINT_INFEASIBLE; in [0, 1), 0 keeps the default 0.5 |
| `--disclosure-sample` | int |  | Cap on the generated rows the fidelity report's disclosure section measures the Gower distance to closest record over; more rows are sampled from a seeded stream that never moves the generated rows. 0 keeps the default 10000 |
| `--no-disclosure` |  |  | Leave the disclosure section (exact-match rate, Gower distance to closest record) out of the fidelity report |
| `--emit-spec` | string |  | Write the profile-derived spec (after any --rules merge) to this path as indented JSON — the spec that actually generates, so 'synth from-schema' reproduces this run at the same seed |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse synth from-schema`

Generate a .pulse file from a JSON schema/spec

Usage: `pulse synth from-schema [options]`

In depth: [synth from-schema](synth-from-schema.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--spec`, `-s` | string |  | **Required.** Synth spec JSON path |
| `--output`, `-o` | string |  | **Required.** Output .pulse file path |
| `--rows` | int |  | Override row_count from spec |
| `--seed` | int |  | Deterministic RNG seed |
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse version`

Print the Pulse build version (--json adds Go version, commit and envelope format_version)

Usage: `pulse version [options]`

| Flag | Value | Default | Description |
|---|---|---|---|
| `--json` |  |  | Output the result as a JSON envelope |

### `pulse widen`

Widen a set column to a wider set rung, rewriting the cohort in place (DESTRUCTIVE, non-interactive — no confirmation prompt; the rewrite is atomic, so a failure leaves the cohort byte-identical)

Usage: `pulse widen [options] COHORT`

In depth: [parent groups](../format/parent-groups.md)

| Flag | Value | Default | Description |
|---|---|---|---|
| `--input`, `-i` | string |  | Input .pulse cohort file path (or pass as the positional argument) |
| `--field`, `-f` | string |  | **Required.** Name of the set column to widen |
| `--to` | string |  | **Required.** Target set type: set_u16, set_u32, set_u64, set_u128 or set_u256. Must be WIDER than the column's current rung |
| `--json` |  |  | Output the result as a JSON envelope |

## Flags by name

Each flag a command mounts, with the commands that mount it. The global flags are in [Global flags](#global-flags).

| Flag | Commands |
|---|---|
| `--additive` | [`api facet`](#pulse-api-facet) |
| `--base-weight` | [`rake`](#pulse-rake) |
| `--bind-on-open` | [`mcp`](#pulse-mcp) |
| `--by` | [`rake`](#pulse-rake) |
| `--category` | [`examples search`](#pulse-examples-search) |
| `--charset` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss), [`import auto`](#pulse-import-auto), [`import predict`](#pulse-import-predict), [`import schema-template`](#pulse-import-schema-template), [`import spss`](#pulse-import-spss) |
| `--cohort` | [`recommend`](#pulse-recommend) |
| `--conditional` | [`profile create`](#pulse-profile-create) |
| `--constraints` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--correlation-top-k` | [`profile create`](#pulse-profile-create) |
| `--count`, `-n` | [`api sample`](#pulse-api-sample) |
| `--data-dir` | [`mcp`](#pulse-mcp) |
| `--dedup-ratio-floor` | [`dedup`](#pulse-dedup), [`import arrow`](#pulse-import-arrow), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--detail` | [`explain`](#pulse-explain) |
| `--disclosure-sample` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--domain` | [`errors list`](#pulse-errors-list) |
| `--dst-policy` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--echo-request` | [`api compose`](#pulse-api-compose), [`api facet`](#pulse-api-facet), [`api lookup`](#pulse-api-lookup), [`api predict`](#pulse-api-predict), [`api predict-chain`](#pulse-api-predict-chain), [`api predict-compose`](#pulse-api-predict-compose), [`api predict-facet`](#pulse-api-predict-facet), [`api process`](#pulse-api-process), [`api process-chain`](#pulse-api-process-chain), [`api sample`](#pulse-api-sample) |
| `--elide-constants` | [`dedup`](#pulse-dedup), [`import arrow`](#pulse-import-arrow), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--emit-spec` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--feature-profile` | [`docs export`](#pulse-docs-export), [`mcp`](#pulse-mcp) |
| `--fidelity-model` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--fidelity-report` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--field` | [`recommend`](#pulse-recommend) |
| `--field`, `-f` | [`api facet`](#pulse-api-facet), [`cohort zone set`](#pulse-cohort-zone-set), [`widen`](#pulse-widen) |
| `--filter` | [`cohort filter`](#pulse-cohort-filter) |
| `--fit-models` | [`profile create`](#pulse-profile-create) |
| `--fit-shape` | [`profile create`](#pulse-profile-create) |
| `--flatten` | [`cohort filter`](#pulse-cohort-filter) |
| `--float` | [`convert`](#pulse-convert), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import schema-template`](#pulse-import-schema-template), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--format`, `-f` | [`export predict`](#pulse-export-predict), [`import auto`](#pulse-import-auto), [`import predict`](#pulse-import-predict), [`import schema-template`](#pulse-import-schema-template) |
| `--from` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`features init`](#pulse-features-init) |
| `--full-dict` | [`cohort inspect`](#pulse-cohort-inspect) |
| `--group` | [`dedup`](#pulse-dedup), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--handle` | [`import auto`](#pulse-import-auto) |
| `--histogram-bins` | [`api facet`](#pulse-api-facet) |
| `--histogram-max` | [`api facet`](#pulse-api-facet) |
| `--histogram-min` | [`api facet`](#pulse-api-facet) |
| `--histogram` | [`api facet`](#pulse-api-facet) |
| `--ignore-extra` | [`convert`](#pulse-convert), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--ignore-sidecar` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss) |
| `--include-correlations` | [`profile create`](#pulse-profile-create) |
| `--include-field` | [`cohort filter`](#pulse-cohort-filter) |
| `--include-from` | [`cohort filter`](#pulse-cohort-filter) |
| `--include-stats` | [`profile create`](#pulse-profile-create) |
| `--include` | [`export arrow`](#pulse-export-arrow), [`export csv`](#pulse-export-csv), [`export excel`](#pulse-export-excel), [`export jsonarray`](#pulse-export-jsonarray), [`export ndjson`](#pulse-export-ndjson), [`export parquet`](#pulse-export-parquet), [`export spss`](#pulse-export-spss), [`export tsv`](#pulse-export-tsv) |
| `--include`, `-i` | [`shard create`](#pulse-shard-create) |
| `--input`, `-i` | [`api facet`](#pulse-api-facet), [`api lookup`](#pulse-api-lookup), [`api sample`](#pulse-api-sample), [`cohort filter`](#pulse-cohort-filter), [`cohort inspect`](#pulse-cohort-inspect), [`cohort zone clear`](#pulse-cohort-zone-clear), [`cohort zone set`](#pulse-cohort-zone-set), [`dedup`](#pulse-dedup), [`export arrow`](#pulse-export-arrow), [`export csv`](#pulse-export-csv), [`export excel`](#pulse-export-excel), [`export jsonarray`](#pulse-export-jsonarray), [`export ndjson`](#pulse-export-ndjson), [`export parquet`](#pulse-export-parquet), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss), [`export transfer`](#pulse-export-transfer), [`export tsv`](#pulse-export-tsv), [`import arrow`](#pulse-import-arrow), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import transfer`](#pulse-import-transfer), [`import tsv`](#pulse-import-tsv), [`index build`](#pulse-index-build), [`index drop`](#pulse-index-drop), [`index list`](#pulse-index-list), [`index verify`](#pulse-index-verify), [`profile create`](#pulse-profile-create), [`rake`](#pulse-rake), [`widen`](#pulse-widen) |
| `--intent` | [`examples search`](#pulse-examples-search), [`recommend`](#pulse-recommend) |
| `--json` | [`api compose`](#pulse-api-compose), [`api facet`](#pulse-api-facet), [`api lookup`](#pulse-api-lookup), [`api materialize`](#pulse-api-materialize), [`api predict`](#pulse-api-predict), [`api predict-chain`](#pulse-api-predict-chain), [`api predict-compose`](#pulse-api-predict-compose), [`api predict-facet`](#pulse-api-predict-facet), [`api process`](#pulse-api-process), [`api process-chain`](#pulse-api-process-chain), [`api sample`](#pulse-api-sample), [`cohort filter`](#pulse-cohort-filter), [`cohort inspect`](#pulse-cohort-inspect), [`cohort zone clear`](#pulse-cohort-zone-clear), [`cohort zone set`](#pulse-cohort-zone-set), [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`dedup`](#pulse-dedup), [`docs export`](#pulse-docs-export), [`errors list`](#pulse-errors-list), [`errors lookup`](#pulse-errors-lookup), [`examples search`](#pulse-examples-search), [`examples show`](#pulse-examples-show), [`explain`](#pulse-explain), [`export arrow`](#pulse-export-arrow), [`export csv`](#pulse-export-csv), [`export excel`](#pulse-export-excel), [`export jsonarray`](#pulse-export-jsonarray), [`export ndjson`](#pulse-export-ndjson), [`export parquet`](#pulse-export-parquet), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss), [`export transfer`](#pulse-export-transfer), [`export tsv`](#pulse-export-tsv), [`features check`](#pulse-features-check), [`features diff`](#pulse-features-diff), [`features init`](#pulse-features-init), [`features show`](#pulse-features-show), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import drop`](#pulse-import-drop), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import list`](#pulse-import-list), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import transfer`](#pulse-import-transfer), [`import tsv`](#pulse-import-tsv), [`index build`](#pulse-index-build), [`index drop`](#pulse-index-drop), [`index list`](#pulse-index-list), [`index verify`](#pulse-index-verify), [`profile create`](#pulse-profile-create), [`rake`](#pulse-rake), [`recommend`](#pulse-recommend), [`shard add`](#pulse-shard-add), [`shard compact`](#pulse-shard-compact), [`shard create`](#pulse-shard-create), [`shard list`](#pulse-shard-list), [`shard remove`](#pulse-shard-remove), [`shard verify`](#pulse-shard-verify), [`skills list`](#pulse-skills-list), [`synth from-profile`](#pulse-synth-from-profile), [`synth from-schema`](#pulse-synth-from-schema), [`version`](#pulse-version), [`widen`](#pulse-widen) |
| `--keep-pulse` | [`convert`](#pulse-convert) |
| `--key`, `-k` | [`api lookup`](#pulse-api-lookup), [`index build`](#pulse-index-build), [`index drop`](#pulse-index-drop), [`index verify`](#pulse-index-verify) |
| `--labels` | [`api facet`](#pulse-api-facet), [`api sample`](#pulse-api-sample), [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`export arrow`](#pulse-export-arrow), [`export csv`](#pulse-export-csv), [`export excel`](#pulse-export-excel), [`export jsonarray`](#pulse-export-jsonarray), [`export ndjson`](#pulse-export-ndjson), [`export parquet`](#pulse-export-parquet), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss), [`export tsv`](#pulse-export-tsv) |
| `--level` | [`export transfer`](#pulse-export-transfer), [`recommend`](#pulse-recommend) |
| `--margins`, `-m` | [`rake`](#pulse-rake) |
| `--max-iter` | [`rake`](#pulse-rake) |
| `--max-rejection-rate` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--max-weight` | [`rake`](#pulse-rake) |
| `--metrics-addr` | [`mcp`](#pulse-mcp) |
| `--min-weight` | [`rake`](#pulse-rake) |
| `--missing` | [`rake`](#pulse-rake) |
| `--mode` | [`api lookup`](#pulse-api-lookup) |
| `--no-cohort-scan` | [`mcp`](#pulse-mcp) |
| `--no-components` | [`api compose`](#pulse-api-compose), [`api process`](#pulse-api-process), [`api process-chain`](#pulse-api-process-chain) |
| `--no-defaults` | [`api compose`](#pulse-api-compose), [`api process`](#pulse-api-process), [`api process-chain`](#pulse-api-process-chain) |
| `--no-disclosure` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--no-fail-fast` | [`api compose`](#pulse-api-compose) |
| `--no-project` | [`api process`](#pulse-api-process) |
| `--no-skills` | [`docs export`](#pulse-docs-export) |
| `--out` | [`docs export`](#pulse-docs-export) |
| `--out`, `-o` | [`api materialize`](#pulse-api-materialize), [`dedup`](#pulse-dedup), [`rake`](#pulse-rake) |
| `--output`, `-o` | [`cohort filter`](#pulse-cohort-filter), [`export arrow`](#pulse-export-arrow), [`export csv`](#pulse-export-csv), [`export excel`](#pulse-export-excel), [`export jsonarray`](#pulse-export-jsonarray), [`export ndjson`](#pulse-export-ndjson), [`export parquet`](#pulse-export-parquet), [`export spss`](#pulse-export-spss), [`export transfer`](#pulse-export-transfer), [`export tsv`](#pulse-export-tsv), [`import arrow`](#pulse-import-arrow), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import spss`](#pulse-import-spss), [`import transfer`](#pulse-import-transfer), [`import tsv`](#pulse-import-tsv), [`profile create`](#pulse-profile-create), [`synth from-profile`](#pulse-synth-from-profile), [`synth from-schema`](#pulse-synth-from-schema) |
| `--overwrite` | [`api materialize`](#pulse-api-materialize), [`import auto`](#pulse-import-auto), [`import transfer`](#pulse-import-transfer), [`rake`](#pulse-rake) |
| `--parallel` | [`api compose`](#pulse-api-compose) |
| `--percentile` | [`api facet`](#pulse-api-facet) |
| `--predict` | [`api materialize`](#pulse-api-materialize) |
| `--profile`, `-p` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--query` | [`errors list`](#pulse-errors-list), [`examples search`](#pulse-examples-search) |
| `--repair` | [`shard verify`](#pulse-shard-verify) |
| `--request`, `-r` | [`api compose`](#pulse-api-compose), [`api facet`](#pulse-api-facet), [`api lookup`](#pulse-api-lookup), [`api materialize`](#pulse-api-materialize), [`api predict`](#pulse-api-predict), [`api predict-chain`](#pulse-api-predict-chain), [`api predict-compose`](#pulse-api-predict-compose), [`api predict-facet`](#pulse-api-predict-facet), [`api process`](#pulse-api-process), [`api process-chain`](#pulse-api-process-chain), [`api sample`](#pulse-api-sample), [`explain`](#pulse-explain), [`rake`](#pulse-rake) |
| `--residual-correlations` | [`profile create`](#pulse-profile-create) |
| `--response` | [`explain`](#pulse-explain) |
| `--return` | [`api lookup`](#pulse-api-lookup), [`mcp`](#pulse-mcp) |
| `--rewrite` | [`shard add`](#pulse-shard-add) |
| `--root` | [`explain`](#pulse-explain) |
| `--rows` | [`synth from-profile`](#pulse-synth-from-profile), [`synth from-schema`](#pulse-synth-from-schema) |
| `--rules` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--run-continuation` | [`profile create`](#pulse-profile-create) |
| `--sample-limit` | [`profile create`](#pulse-profile-create) |
| `--sample-rows` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`import arrow`](#pulse-import-arrow), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import schema-template`](#pulse-import-schema-template), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--sanitize-names` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss) |
| `--schema` | [`convert`](#pulse-convert), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--seed` | [`profile create`](#pulse-profile-create), [`synth from-profile`](#pulse-synth-from-profile), [`synth from-schema`](#pulse-synth-from-schema) |
| `--sheet` | [`import auto`](#pulse-import-auto), [`import excel`](#pulse-import-excel), [`import predict`](#pulse-import-predict), [`import schema-template`](#pulse-import-schema-template) |
| `--source-tz-field` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--source-tz` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--source` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--spec`, `-s` | [`synth from-schema`](#pulse-synth-from-schema) |
| `--spss-missing` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`import predict`](#pulse-import-predict), [`import schema-template`](#pulse-import-schema-template), [`import spss`](#pulse-import-spss) |
| `--stream-fits` | [`api compose`](#pulse-api-compose) |
| `--stream` | [`api compose`](#pulse-api-compose), [`api process`](#pulse-api-process) |
| `--strict` | [`api materialize`](#pulse-api-materialize), [`api predict`](#pulse-api-predict), [`api predict-chain`](#pulse-api-predict-chain), [`api predict-compose`](#pulse-api-predict-compose), [`api predict-facet`](#pulse-api-predict-facet), [`api process`](#pulse-api-process), [`convert`](#pulse-convert), [`dedup`](#pulse-dedup), [`export spss`](#pulse-export-spss), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--suggest-groups` | [`dedup`](#pulse-dedup), [`import predict`](#pulse-import-predict) |
| `--suggest-rules` | [`profile create`](#pulse-profile-create) |
| `--synthetic-only` | [`synth from-profile`](#pulse-synth-from-profile) |
| `--tag` | [`examples search`](#pulse-examples-search) |
| `--time-zone-field` | [`profile create`](#pulse-profile-create) |
| `--time-zone` | [`cohort filter`](#pulse-cohort-filter), [`profile create`](#pulse-profile-create) |
| `--to` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`widen`](#pulse-widen) |
| `--tol` | [`rake`](#pulse-rake) |
| `--top-k` | [`api facet`](#pulse-api-facet), [`profile create`](#pulse-profile-create) |
| `--top` | [`recommend`](#pulse-recommend) |
| `--total` | [`rake`](#pulse-rake) |
| `--ttl` | [`import auto`](#pulse-import-auto) |
| `--tz` | [`export arrow`](#pulse-export-arrow), [`export csv`](#pulse-export-csv), [`export excel`](#pulse-export-excel), [`export jsonarray`](#pulse-export-jsonarray), [`export ndjson`](#pulse-export-ndjson), [`export parquet`](#pulse-export-parquet), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss), [`export tsv`](#pulse-export-tsv) |
| `--uncompressed` | [`convert`](#pulse-convert), [`convert predict`](#pulse-convert-predict), [`export predict`](#pulse-export-predict), [`export spss`](#pulse-export-spss) |
| `--vector-kind` | [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--vector` | [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
| `--weight-field` | [`rake`](#pulse-rake) |
| `--zone-field` | [`convert`](#pulse-convert), [`import arrow`](#pulse-import-arrow), [`import auto`](#pulse-import-auto), [`import csv`](#pulse-import-csv), [`import excel`](#pulse-import-excel), [`import jsonarray`](#pulse-import-jsonarray), [`import ndjson`](#pulse-import-ndjson), [`import parquet`](#pulse-import-parquet), [`import predict`](#pulse-import-predict), [`import spss`](#pulse-import-spss), [`import tsv`](#pulse-import-tsv) |
