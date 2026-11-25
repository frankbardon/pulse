# CLI Concepts and Shared Flags

**Audience:** CLI users who need what `--help` cannot say: the
environment variables Pulse reads, the `--json` envelope, exit status,
and the semantics of flags several commands share.

Every command and every flag it accepts, with its value type, default
and help text, is listed in the generated
[command and flag reference](reference.md). This page explains the
concepts behind them.

> **LLM agents using MCP:** there is no LLM-facing skill for the CLI
> surface. Agents go via MCP tools (`pulse_process`, `pulse_inspect`,
> ...) — see [`pulse mcp`](mcp.md) and
> [Adding an MCP tool](../internals/adding-mcp-tool.md).

## Global flags

The bare `pulse` invocation takes `--json` (print the manifest, the
discovery entry point documented at [`pulse manifest`](manifest.md)),
`--slim` (drop prose descriptions) and `--intent ID` (scope the manifest
to one intent-taxonomy ID; an unknown ID is a
`PULSE_RECOMMEND_INTENT_UNKNOWN` error envelope). `--log-level`,
`--log-format`, `--shard-workers`, `--decode-workers`, `--limit` and
`--memory-limit` are persistent: every leaf accepts them, before or after
the leaf name. Their values and defaults are in the
[reference](reference.md#global-flags); limit names and grammar are in
[Tuning Limits](../library/tuning-limits.md).

Logs never reach stdout, so `--json` output and the `pulse mcp`
JSON-RPC transport stay byte-identical with or without them. Records
carry identifiers, counts, timings, enums and error codes only — never
row data, filter values or error messages. An unknown value is
`CLI_INPUT`. There are no environment-variable forms. Leaves that never build a Pulse instance (`import`, `convert`, `skills`, `errors`, `version`, `features`) accept the flags but emit nothing. The worker flags reach every instance a leaf builds, `pulse mcp` included, and trade speed against peak memory (figures: [Tuning Limits](../library/tuning-limits.md#worker-counts)); a negative count is a usage error. `--limit` likewise reaches every instance a leaf builds — `pulse api compose --limit max_compose_slots=2000` runs a 2,000-slot batch — and under `pulse mcp` wins over the feature profile's `limits` per key; an unknown name or bad value is a usage error carrying `CLI_INPUT` (details `{flag, value, limit?}`). Limits are instance-level only: no request carries one. `--memory-limit` is different: the Go runtime's soft limit is process-global, so it has no `pulse.Options` field and no `PULSE_*` form (`GOMEMLIMIT` is the environment variable), and it only makes the garbage collector work harder near the cap — it never refuses a request (`--limit max_estimated_memory` does). `pulse mcp --metrics-addr` is documented under [pulse mcp](mcp.md); see also [Observability](../library/observability.md).

## Environment variables

| Variable | Used by | Required | Purpose |
|---|---|---|---|
| `PULSE_DATA_DIR`        | `pulse mcp` (required unless `--data-dir`), `pulse import auto / list / drop` | conditionally | Base directory for cohort files and the managed-imports pool. A relative value is made absolute against the working directory at startup. Every other leaf resolves its `--input` / positional / `--output` paths against the working directory and ignores it |
| `PULSE_IMPORTS_DIR`     | `pulse import auto / list / drop`                                    | no | Managed-imports subdir under the data root. Defaults to `imports` |
| `PULSE_IMPORT_TTL`      | `pulse import auto`                                                  | no | Default TTL for managed imports. Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. Defaults to `7d` |
| `PULSE_LABEL_TABLES_DIR`| `pulse api sample --labels`, `pulse api facet --labels`, `pulse export * --labels`, `pulse convert [predict] --labels` | no | Directory of JSON files auto-loaded as label tables at `pulse.New` time; each `*.json` becomes one table keyed by its filename, its key order the table's entry order (the `.sav` value-label code order) |
| `PULSE_MCP_NO_COHORT_SCAN`| `pulse mcp`                                                        | no | Skip the startup walk that enumerates `.pulse` files as `pulse://` resources. Same as `--no-cohort-scan`; cohorts stay readable by URI, only `resources/list` loses them |
| `PULSE_FEATURE_PROFILE` | `pulse mcp` only                                                     | no | OS path (cwd-relative or absolute) to a feature profile JSON file. Same as `--feature-profile`, which wins when both are set. Every other leaf ignores it |

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
nullable-check overhead. `request` is opt-in (`--echo-request`, on the
leaves the [flags by name](reference.md#flags-by-name) index lists); `data.components` is the
additive `Response.Components` slot documented per leaf — see
[`api process` → `--json`](api-process.md#-json).

## Exit status

Scripts can trust the exit status. Every command except `pulse mcp` (a
long-running server, see [its page](mcp.md#exit-codes)) follows the same
three values, the bare `pulse --json` included. There are no per-domain
codes; branch on `errors[0].code` for the reason.

| Status | Meaning |
|---|---|
| 0 | Success. Warnings and advisories are allowed: they ride the envelope's `warnings` (stderr lines in text mode) and never move the status |
| 1 | A fatal error. Under `--json` the envelope's `errors` carries it (a warning `--strict` escalated included) and stdout is that envelope alone, with nothing added to stderr; in text mode it is printed to stderr as `error: ...` |
| 2 | A usage error refused before any library call: an unknown flag, a malformed flag value, a missing required flag or argument, mutually exclusive flags (`api sample --input` beside `--request`), an unknown `--log-level` / `--log-format`, a negative `--shard-workers` / `--decode-workers`, a bad `--limit`, or a bad `--memory-limit`. The usage text goes to the terminal; no envelope is written |

The rule follows the envelope: an envelope with a non-empty `errors`
always exits 1, one with only `warnings` exits 0. A leaf's own argument
checks (a missing positional `COHORT`, an unknown `--root`) run inside
the leaf, so they are a `CLI_INPUT` envelope and exit 1.

```bash
pulse api process -r req.json --json > out.json || exit $?   # 1: errors[0] in out.json
```

## Shared per-command flags

The semantics of flags several commands share. Which commands mount
each one is in the [flags by name](reference.md#flags-by-name) index.

### `--input` / `-i`

The cohort path, resolved against the working directory (an absolute
path is used as-is). `cohort inspect`, `widen`, `dedup` and the `index`
leaves also accept it as the positional argument; the flag wins when
both are given. Paired with `--request` (`api facet`, `api lookup`), the
flag **replaces** the request file's `cohort` outright — `filename` and
`data_dir` — so `-i x.pulse -r req.json` reads `x.pulse` whatever
`req.json` names. On `api sample` the two are mutually exclusive
(exit 2): the request file names the cohort, or the union, itself.

### `--strict`

Treat request-validation warnings (e.g. numeric aggregation on a
categorical field, low-quality field description) as errors. On
`api predict` and the three `api predict-*` leaves this fails
validation (every warning the leaf reports becomes an error); on
`api process` it refuses to execute. Useful in CI gates that want the strictest possible
validation.

`import <format>`, `import auto` and `convert` also take `--strict`,
with an import meaning: it escalates a partial import
(`PULSE_IMPORT_ROWS_DROPPED`) and, on `import <format>`, the parent-group
viability warnings — see [`--strict` (import)](#--strict-import). On
`convert` into `.sav` it also refuses a `u64` past 2^53
(`PULSE_SPSS_INTEGER_INEXACT`) rather than writing it as a rounded double.
`export spss --strict` carries only that meaning: the `.sav` export fails
on such a value (exit 1, `errors[0].code`) and writes no file — the CLI
spelling of `Options.Strict` / `ExportJob.Strict`.

### `--schema` / `--ignore-extra`

`--schema FILE` supplies an explicit schema (a JSON array of
`{name, type, description, nullable}`, plus `precision` / `scale` on a
`decimal128` field; `import schema-template INPUT` emits an editable one,
decimal precision and scale included). The file carries no byte
offsets — the layout is derived from field order and types exactly as
inference derives it, so a file mixing `u4` / `packed_bool` with
byte-aligned fields writes the same schema block an inferred import of
the same fields does. Its fields are matched to the source's columns
**by exact name** — case and whitespace included — never by position, so
the schema may list them in any order and that order is the cohort's
field order. For NDJSON and JSON-array sources the columns are the keys
of the first object. Arrow, Parquet and SPSS sources, whose own schema
an explicit one overrides, are matched the same way. Every mismatch is
refused before a row is read, under its own code:

| Code | When | Details |
|---|---|---|
| `PULSE_IMPORT_SCHEMA_FIELD_MISSING` | a schema field has no column of that name | `field`, `columns` |
| `PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED` | a source column no schema field names | `column`, `fields` |
| `PULSE_IMPORT_SCHEMA_DUPLICATE` | a field name declared twice, or a repeated header name | `field` or `column`, `side` |
| `PULSE_IMPORT_SCHEMA_KEY_LATE` | an NDJSON / JSON-array key first seen after the first object | `column`, `row`, `columns` |
| `PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN` | a field's `type` is not a registered field type (never read as `f64`) | `field`, `type`, `valid` |
| `PULSE_IMPORT_SCHEMA_INVALID` | the file is not a JSON array of field objects, or lists no field | `reason`, `error` |

`--ignore-extra` drops source columns, and late keys, that the schema
does not name instead of refusing (library: `ImportJob.IgnoreExtra` /
`ConvertJob.IgnoreExtra`). A schema field with no column is still
refused. Without `--schema` the flag is inert and inference is
unchanged.

The MCP `pulse_import` tool takes the same document inline as its
`schema` argument, with `ignore_extra` and `float` beside it, and
refuses with the same codes (plus `PULSE_IMPORT_SCHEMA_INVALID` for an
unknown entry key or a schema on a `.pulse` source).

### `--float`

The width schema inference gives an all-float column: `f64` (the
default) keeps every decimal the source wrote exact, so `0.1` and
`123456.789012` round-trip and a money column's sum matches the f64
reference; `f32` halves the column's bytes but rounds every value to
about 7 significant digits. Under `f32` a column whose sample holds a
value past f32's range still infers `f64`, and a later such value
promotes the field (`PULSE_IMPORT_WIDTH_PROMOTED`, below). Any other
value refuses the run with `PULSE_IMPORT_FLOAT_WIDTH_INVALID` (`value`,
`valid`), with or without `--schema`. Inert with `--schema` or a source
that carries its own schema (Arrow, Parquet, SPSS); a managed import's
`column_type_overrides` still forces a column's type either way.
Library: `ImportJob.FloatWidth` / `ConvertJob.FloatWidth` (`"f64"`,
`"f32"`; `""` is `f64`; `ImportSpec.FloatWidth` on the managed pool,
`float` on `pulse_import`).

### `--elide-constants`

`import predict` reports what would be elided and the bytes saved,
and writes nothing.

`--elide-constants` stores every field that holds exactly one value — or
is null — on every imported row once, in the schema block, and drops it
from each record. Constancy is measured over the full row pass, never
the inference sample. The output is a format `0x02` cohort that binaries
predating `0x02` cannot open, which is why it is opt-in. Nothing is
elided (and the cohort stays byte-identical `0x01`) for fewer than two
rows or when the saving would not repay the schema-block growth; when
every field is constant the lowest-index field stays in the row. The
elided fields are printed, and reported as `elided_constants` under
`--json`. See [parent groups](../format/parent-groups.md).

### `--group`

Repeatable — one parent
group per flag. On `import auto` the group is judged at the default
ratio floor and its findings are always warnings (`--json` puts them in
`warnings`, the per-group figures under `data.groups`); the floor and
the group findings' `--strict` stay on the per-format leaves, since
neither changes the bytes written (`import auto --strict` escalates a
partial import only).
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
`--json` adds `projection`, `groups`, `group_warnings`, `elided_constants`,
`group_candidates` and `width_warnings` to `data`; `format_version` stays
`"1.1"`. Every import, export, convert and shard `--json` `data` is
snake_case with field types as names (`"type": "f64"`).

### Width promotion (inferred imports and converts)

Not a flag: every inferred `import <format>`, `import auto`,
`pulse_import` and `pulse convert` does it. Inference sizes `categorical_*` rungs and integer
widths from the first `--sample-rows` rows. A later value that outgrows
them promotes the field to the narrowest type that holds it instead of
dropping the row: `categorical_u8` → `u16` → `u32`; `u4` → `u8` → `u16`
→ `u32` → `u64` for a larger non-negative integer; `u4`..`u32` → `f64`
for a negative, fractional or signed number; `f32` → `f64` for a value
outside f32's range — past `MaxFloat32`, or a non-zero magnitude f32
would flush to zero. Floats only infer `f32` under `--float f32` (see
below), and by that same range test, so a value f32 merely rounds
(`0.1`) never promotes. Every step is lossless
for every value already read. Each promoted field draws one `PULSE_IMPORT_WIDTH_PROMOTED`
warning (`field`, `from`, `to`, `source_row`) in the envelope `warnings`
and `data.width_warnings` (the same key on `import auto` /
`pulse_import`). A `--schema` or an authoritative source schema (SPSS,
Arrow, Parquet) never promotes: its overflow stays a
`PULSE_IMPORT_ROW_ERROR`. A `column_type_overrides` column never promotes
either, and any value it cannot hold refuses the whole import with
`PULSE_IMPORT_OVERRIDE_INVALID`. So does a non-number, a
non-integer in a `u64` column, a non-boolean in a `packed_bool` column
(no type holds both losslessly), and a dictionary past `categorical_u32`.

`pulse convert` copies cell text, so a promotion never changes what the
target receives. It reports the promoted types in `data.schema` and the
warnings in `data.width_warnings` and the envelope `warnings`, and a
`--keep-pulse` intermediate is byte-identical to a plain `import` of the
source. On a declared schema (`--schema`, or an SPSS / Arrow / Parquet
source's own) a full categorical rung still stops the convert with the
fatal `PULSE_IMPORT_CATEGORICAL_OVERFLOW`, and no intermediate is written.

### `--dedup-ratio-floor`

The rows-per-distinct-tuple floor below which a `--group` draws
`PULSE_DEDUP_LOW_RATIO`. A floor of `1` disables it, leaving only the
check that the group actually makes the file smaller. The group is
written either way unless `--strict` is set.

### `--strict` (import)

A PARTIAL import — some rows imported, some skipped as
`PULSE_IMPORT_ROW_ERROR` (a null in a non-nullable field, a value the
type cannot hold) — is a warning by default: the good rows are written
and the envelope `warnings` carries one `PULSE_IMPORT_ROWS_DROPPED` with
`dropped`, `imported` and `first_failure` `{row, column, reason}` in
`details` (`data.dropped_warnings`; plus `rows_dropped` on
`import auto`). `convert` adds `stage`: `target` for rows the target
writer refused, `keep_pulse` for rows the `--keep-pulse` intermediate
skipped. `--strict` turns it into the fatal error (exit 1,
`errors[0].code`) and nothing is written. `import predict --strict`
fails the same way when its measured pass (`--group`,
`--elide-constants`, `--suggest-groups`) would drop rows. Library:
`ImportJob` / `ConvertJob` / `ImportSpec` `StrictRows`, and any instance
built with `Options.Strict` (which `pulse_import` inherits).

On `import <format>` and `import predict` it also turns the parent-group
viability warnings (`PULSE_GROUP_TOO_NARROW`, `PULSE_DEDUP_LOW_RATIO`)
into errors: the import fails with that code and writes nothing. Other
import warnings are unaffected.
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

### `--vector` / `--vector-kind`

Repeatable — one vector per flag.

`--vector NAME:COL1,COL2,...` folds sibling source columns into ONE
native vector field (`vec_f64`; `NAME:f32:COL1,...` for `vec_f32`), the
elements in the listed order. `--vector NAME:/REGEX/` takes every column
the Go regular expression matches, in source column order. Labels are
the regex's first capture group, else the full column names, and are
always written. The members vanish from the cohort; the vector takes
the position of its first member.

```sh
pulse import csv -i survey.csv -o survey.pulse \
  --vector 'brand_rating:/^rate_(.*)$/' \
  --vector-kind brand_rating:scale:1:7
```

`--vector-kind NAME:KIND[:MIN:MAX]` declares a declared vector's kind
(`measure`, `scale`, `composition`, `binary`) and optional value range;
kind is never inferred.

Per row: every element cell empty is a null vector (an inferred field is
promoted to nullable), some empty are `NaN` elements, and a non-numeric
cell fails the row (`PULSE_IMPORT_ROW_ERROR`). Refused before any row is
read with `PULSE_IMPORT_VECTOR_INVALID` (`details.reason`): a fold of
fewer than 2 or more than 1024 columns, a regex matching no column, a
column claimed twice (two vectors, or a vector and a `--group`), an
unknown column, a `--vector-kind` naming no declared vector, a bad kind
or range, a vector named after a column it does not fold, or an SPSS
source. With `--schema`, type the field `vec_f64` / `vec_f32` with the
same `dim` (the schema file's `dim`, `labels`, `kind`, `scale_min`,
`scale_max` keys; a `vec_*` entry without `dim` is
`PULSE_IMPORT_SCHEMA_INVALID`). A column type override to a `vec_*` type
is `PULSE_IMPORT_OVERRIDE_INVALID`. Writes a format `0x02` cohort.
`pulse_import` (MCP) takes the same strings as `vectors` /
`vector_kinds`.

A bare `--vector NAME` (or `NAME:f32`) reads the NATIVE list column
`NAME` — Arrow `FixedSizeList` / `LIST`, Parquet `LIST`, an NDJSON or
JSON-array array — as one vector field at that column's position. The
elements are read typed off the source (never through the pipe-joined
text the set ladder sees); the dim is the length of the first non-null
list; no labels are written. A null list, or a list of nulls only, is a
null vector; a null element is `NaN`. The whole import is refused with
`PULSE_IMPORT_VECTOR_INVALID` on a list of a different length
(`list_length_varies`), a non-number element — a quoted `"3"` included
(`element_not_numeric`), a non-list cell (`not_a_list`), a length
outside 2..1024 (`dim_out_of_range`), or no non-null list at all
(`list_dim_unknown`); CSV / TSV / Excel have no typed lists
(`no_columns`). Without the flag a list column imports as a `set_*`
field exactly as before.

```sh
pulse import parquet -i embeddings.parquet -o emb.pulse \
  --vector embedding:f32 --vector-kind embedding:measure
```

### `--source-tz` / `--source-tz-field` / `--dst-policy`

`--source-tz Zone` reads every naive `datetime` value (no `Z`, no
`±HH:MM`) as a wall clock in `Zone` and stores the UTC instant it names;
storage is UTC epoch seconds either way, so the `.pulse` format is
unchanged. `Zone` is an IANA name (`America/New_York`), `UTC`, or — for
imports only — a fixed offset `+HH:MM` / `-HH:MM`. Repeatable
`--source-tz col=Zone` sets one column and wins over the bare form; it is
`CLI_INPUT` when `col` is not a column, or is not `datetime` (a `date` is
a calendar day and has no zone). The bare form skips `date` columns. A
value carrying its own `Z` or offset names that instant and ignores the
zone. Without `--source-tz` the cohort is byte-identical to before.

`--source-tz-field col` reads each ROW's zone from column `col` (same
spellings, `±HH:MM` included): every naive `datetime` column not named by a
`--source-tz col=Zone` reads in the zone its own row names, so one source
can mix New York, Berlin and Kolkata rows. The zone column imports as an
ordinary column (typically a categorical). A bare `--source-tz` becomes the
fallback for an empty zone cell; with no fallback that row is a row error
(`PULSE_IMPORT_ROW_ERROR`), and an unknown name is a row error coded
`PULSE_TIMEZONE_UNKNOWN` — both name `row`, `column` and `zone_column`,
skip the row, and count toward `PULSE_IMPORT_ROWS_DROPPED` (fatal under
`--strict`). A value with its own `Z` / offset never consults the zone. A
`col` the source does not carry, or a `datetime` column, is `CLI_INPUT`.
Each distinct zone name is loaded once per pass; `--dst-policy` applies
row by row, and a refusal names that row's zone.

`--dst-policy` decides a naive value a DST transition makes ambiguous (a
fall-back repeats the hour) or nonexistent (a spring-forward skips it):
`error` (default) fails the import at the first such row with
`PULSE_IMPORT_DST_AMBIGUOUS` / `PULSE_IMPORT_DST_NONEXISTENT`, naming
`row`, `column`, `value` and `zone`, and writes nothing; `earlier` uses
the pre-transition offset, `later` the post-transition offset. For an
overlap that is the first / second occurrence. For a nonexistent time it
inverts the wall clock: Berlin `2026-03-29 02:30` under `earlier` is
`01:30Z` = `03:30` local (the LATER wall clock), under `later` `00:30Z` =
`01:30` local. A resolved value is never silent: one
`PULSE_IMPORT_DST_RESOLVED` warning carries `ambiguous_n` /
`nonexistent_n` (`warnings` and `data.zone_warnings` under `--json`). The
policy is inert without a zone and under a fixed offset.

On `import auto` the zone, per-column map and policy are persisted onto
the managed sidecar (`source_tz`, `column_source_tz`, `source_tz_field`,
`dst_policy`; each omitted when unset); a
handle is never reused across zones — re-importing in another zone needs
`--overwrite`. `import predict` and `convert predict` raise the refusal
the real run would and report the resolved count. `convert` is a text
pass, so a zoned naive value is written to the target as the canonical
UTC literal of the instant it names (`2026-07-01T16:00:00Z`); a value
with its own `Z` / offset, an unparseable cell and every other column
pass through verbatim, and a `--keep-pulse` cohort is imported in the
same zone. Under `--source-tz-field` a row whose zone cannot be resolved
is dropped before the target (`PULSE_IMPORT_ROWS_DROPPED` with `stage`
`source`) rather than written as a naive literal. `pulse_import` (MCP)
takes `source_tz`, `column_source_tz` (`{column: zone}`),
`source_tz_field` and `dst_policy`.

```bash
pulse import csv -i orders.csv -o orders.pulse \
  --source-tz America/New_York --source-tz shipped_at=Europe/London \
  --dst-policy later

# each row names its own zone; empty cells read in UTC
pulse import csv -i shipments.csv -o shipments.pulse \
  --source-tz-field warehouse_tz --source-tz UTC
```

### `--zone-field` (cohort default zone)

`--zone-field col` declares the written cohort's DEFAULT per-row zone
field: `col` must import as a `categorical_*` column whose every value is
`UTC` or an IANA `Area/Location` name (never a fixed offset). The cohort
stores it in its schema (the `0x02` ZONE section, so it travels with every
copy) and `pulse inspect` shows it as `zone_field`; every zone-aware slot
then reads each row in its own zone unless the request names a zone
(predict echoes `source: "cohort"`). It is declared explicitly and is never
implied by `--source-tz-field`, which says how the SOURCE's literals were
recorded; the two may name the same column. A `col` the source does not
carry, or one that is not categorical, is `SERVICE_VALIDATION`
(`details.option` `zone_field`, `details.zone_field`) before any row is
read; an entry that is not a zone is `PULSE_TIMEZONE_UNKNOWN`
(`details.zone_field`, `dictionary_index`) before anything is written. Both
codes are the same from the CLI, the library (`ImportJob.ZoneField`) and
`pulse_import` (`zone_field`) — not re-coded `CLI_INPUT`. `import predict`
judges the declaration over the dictionaries the import would write. On
`convert` it declares the `--keep-pulse` cohort's default (the target file
carries no schema), so without `--keep-pulse` it is refused
(`reason` `no_cohort`). `import auto` records it on the sidecar
(`zone_field`) and refuses it for a `.pulse` source (`reason`
`passthrough`). Without the flag the cohort is byte-identical to before.

```bash
pulse import csv -i shipments.csv -o shipments.pulse \
  --source-tz-field warehouse_tz --zone-field warehouse_tz
```

### `--tz` (export)

`--tz Zone` renders every `datetime` cell on `Zone`'s wall clock with
its numeric offset — an RFC 3339 literal such as
`2026-03-29T08:00:00+05:30` under `Asia/Kolkata`, `+01:00` / `+02:00`
either side of a Europe/Berlin transition (a fall-back hour's two
readings differ only by offset) — instead of the canonical UTC
`2026-03-29T02:30:00Z`. The literal names the same instant, so
re-importing the export (offset literals keep their instant) round-trips
every value exactly. `date` columns are calendar days and never move.
`Zone` is an IANA name or `UTC` — no fixed offsets, abbreviations or
`Local` (`PULSE_TIMEZONE_UNKNOWN`, from `export predict` too). Absent,
`UTC` or a UTC-equivalent zone (`Etc/UTC`) is byte-identical to today's
output. An instant whose historical offset is not a whole minute (a
pre-1900 local mean time) keeps the `…Z` form: RFC 3339 cannot spell it.

Text formats carry the literal as text, and so do `arrow` / `parquet`,
whose `datetime` columns are UTF8 strings. `export spss` refuses a
non-UTC zone (`PULSE_SPSS_EXPORT_UNSUPPORTED`, `details.option` =
`--tz`), and `export predict --format spss --tz ...` predicts that: a
`.sav` DATETIME is a naive wall-clock count with no offset slot. The
library knob is `io.ExportJob.TimeZone`; the manifest's
`export.formats[].time_zone` says per format (`local_offset` /
`refused`).

```bash
pulse export csv -i orders.pulse -o orders.csv --tz Asia/Kolkata
```

### `--time-zone` (cohort filter)

`--time-zone Zone` is the zone a `to_day()` call in `--filter` reads,
on both predicate shapes (`--filter` alone, and `--include-from` AND
`--filter`): `to_day(ts) == d` keeps a row whose `ts` falls on `d` on
`Zone`'s wall clock. It is the instance default zone
(`Options.DefaultTimeZone`), so a predicate reads it exactly as
`FilterToFileRequest.TimeZone` would. `Zone` is an IANA name or `UTC`;
an unknown name is a `PULSE_TIMEZONE_UNKNOWN` envelope, exit 1, refused
before the cohort is read and with nothing written. Absent, `to_day`
reads UTC and the output is byte-identical to today's. The output path
is always `-o`; the leaf does no dedup naming (that is
[`FilterToFileWithRequest`](../library/filter-to-file.md)).

```bash
pulse cohort filter -i orders.pulse -o ny_same_day.pulse \
  --filter 'to_day(placed_at) == ship_day' --time-zone America/New_York
```

### `--flatten` (cohort filter)

`--flatten` writes a shard archive input as ONE single-file `.pulse`
instead of a per-shard archive: the surviving rows, shard by shard in
archive order, under the archive's canonical schema (widened `set_*`
rungs, union dictionaries and, for a grouped archive, the canonical
parent-group dictionaries). With it `--filter` / `--include-from` become
optional — none is a pure flatten, either one keeps exactly the rows
`process` keeps under the same filter. Memory is one shard; the output
is streamed into `<output>.pulse.tmp-staged` and renamed into place, so
`-o` is untouched until the run succeeds. On a single-file or
`archive#shard` input — already one file — the flag is a no-op: the
output is what the run without it writes (`--json` `data.flattened`
false). `--json` adds `data.flattened` only when the flag is given,
and `data.invalidated_sidecars` whenever `-o` replaced a cohort whose
point-lookup index or SPSS sidecar is now stale (with or without the
flag). Library: `Pulse.FilterToFileWithOptions` with
`FilterToFileOptions.Flatten` ([filter-to-file](../library/filter-to-file.md)).

```bash
pulse cohort filter -i sales.pulse -o sales_flat.pulse --flatten
pulse cohort filter -i sales.pulse -o eu_flat.pulse --flatten --filter 'region == "EU"'
```

### `--labels` (export)

`--labels FIELD=TABLE[:replace|augment]` binds a label table (from
`PULSE_LABEL_TABLES_DIR`) to a field. On every row format (`csv`, `tsv`,
`ndjson`, `jsonarray`, `excel`, `arrow`, `parquet`) `replace` rewrites
the cell to its label and `augment` adds a `FIELD_label` column after
it; `export spss` writes value labels instead (see
[Value labels](export-spss.md#value-labels)). The field may be:

- a `categorical_*` field — the table is keyed by its dictionary text;
- an integer-coded `u4` / `u8` / `u16` / `u32` field — the table is
  keyed by the stored integers' plain decimal spellings (`"1"`, `"7"`).
  A `replace` column becomes text (`arrow` / `parquet` / `excel` type it
  as a string); an `augment` column keeps its integer type. A key that is
  not such a spelling (text, `"01"`, `"1.0"`, past the type's maximum)
  refuses the export `PULSE_LABEL_KEY_INVALID`.

Any other type (`f32` / `f64`, `u64`, `decimal128`, `date`, `datetime`,
`packed_bool`, `set_*`, `vec_*`) refuses
`PULSE_LABEL_FIELD_NOT_CATEGORICAL` before a byte is written, and
`export predict --labels` raises the same code with or without
`--format`. So does a binding on a field the cohort lacks
(`PULSE_LABEL_FIELD_UNKNOWN`) and a second binding on one field
(`PULSE_LABEL_DUPLICATE_BINDING`) — the codes `export spss` raises — on
every row format, rather than labelling nothing or letting the last
binding win. Under `--json` each refusal is the envelope's error, exit
status 1. A value the table lacks keeps its raw text — an unmatched
code keeps its number — and `augment` leaves its `FIELD_label` cell
empty; one `PULSE_LABEL_LOOKUP_MISS` warning per field summarises them.

```bash
PULSE_LABEL_TABLES_DIR=./labels \
  pulse export csv -i survey.pulse -o survey.csv --labels gender=gender_names:augment
```

`convert` takes the same bindings against the schema it resolves
(inferred, declared by `--schema`, or the source's own) and labels the
converted ROW stream on every target, `.sav` included — a `.sav` written
by `convert` carries the label text in its cells, not value labels. Each
refusal above is raised before anything is written, plus
`PULSE_LABEL_TABLE_UNKNOWN` for an unregistered table; an inferred
integer column may widen in the row pass, so its keys are bounded by
`u32`. `convert predict --labels` raises every one of them with the
convert's own code (the library spelling is `Pulse.PredictConvert`).

```bash
PULSE_LABEL_TABLES_DIR=./labels pulse convert survey.csv survey.sav --labels region=regions
```

### Vector fields (export)

Not a flag — how every `export <format>` leaf writes a `vec_f32` /
`vec_f64` field. `csv`, `tsv` and `excel` expand it in place into one
column per element, `<name>_<label>` (`<name>_1..N` without labels), so
a battery folded with `--vector rate:/^rate_(.*)$/` exports back as its
`rate_*` columns and re-imports with the same flag to an identical
cohort; a NaN element and every element of a null vector are empty
cells. `ndjson` and `jsonarray` write one JSON array per cell (`null`
for a null vector and for a NaN / ±Inf element); `arrow` writes a
`FixedSizeList<float32|float64, dim>`, `parquet` the same for a
non-nullable vector and a `LIST` for a nullable one; each re-imports
under `--vector NAME`. `spss` writes one numeric variable per element
with the element label as its variable label. `--include` names the
vector field, never an element column. An expanded name that another
column already has refuses the export (`PULSE_EXPORT_COLUMN_COLLISION`),
and `export predict --format F` prints the `Columns:` it would write.

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
| Every command's flags, types and defaults | [Command and flag reference](reference.md) |
| Per-command synopsis & examples | [CLI Tour](../getting-started/cli-tour.md) and each `cli/` page |
| Library-side equivalents | [Library Embedding](../library/overview.md) |
| MCP-side equivalents | [How LLMs Use Pulse](../mcp/index.md) |
| Envelope and error code semantics | [Troubleshooting](../ops/troubleshooting.md) and the `pulse_errors_lookup` MCP tool / `pulse errors lookup CODE` CLI |
