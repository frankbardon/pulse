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
| `--intent ID` | With `--json`, scope the manifest to one intent-taxonomy ID (e.g. `compare_groups`): only the operators serving it and its skills, the fixed sections dropped and listed in `elided`. An unknown ID is a `PULSE_RECOMMEND_INTENT_UNKNOWN` error envelope |

All default to off. `pulse --json` is the discovery entry point — it
emits the manifest documented at [`pulse manifest`](manifest.md).

Persistent on **every** leaf (accepted before or after the leaf name):

| Flag | Effect |
|---|---|
| `--log-level off\|debug\|info\|warn\|error` | Structured log records on **stderr** at or above the level (default `off`: nothing). `debug` adds the per-operation plan record (execution arm, workers, shards, projection) |
| `--log-format text\|json` | Log record encoding (default `text`); ignored while `--log-level` is `off` |

Logs never reach stdout, so `--json` output and the `pulse mcp`
JSON-RPC transport stay byte-identical with or without them. Records
carry identifiers, counts, timings, enums and error codes only — never
row data, filter values or error messages. An unknown value is
`CLI_INPUT`. There are no environment-variable forms. Leaves that never build a Pulse instance (`import`, `convert`, `skills`, `errors`, `version`, `features`) accept the flags but emit nothing. `pulse mcp --metrics-addr` is documented under [pulse mcp](mcp.md); see also [Observability](../library/observability.md).

## Environment variables

| Variable | Used by | Required | Purpose |
|---|---|---|---|
| `PULSE_DATA_DIR`        | `pulse mcp` (required unless `--data-dir`), `pulse import auto / list / drop` | conditionally | Base directory for cohort files and the managed-imports pool. A relative value is made absolute against the working directory at startup. Every other leaf resolves its `--input` / positional / `--output` paths against the working directory and ignores it |
| `PULSE_IMPORTS_DIR`     | `pulse import auto / list / drop`                                    | no | Managed-imports subdir under the data root. Defaults to `imports` |
| `PULSE_IMPORT_TTL`      | `pulse import auto`                                                  | no | Default TTL for managed imports. Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. Defaults to `7d` |
| `PULSE_LABEL_TABLES_DIR`| `pulse api sample --labels`, `pulse api facet --labels`, `pulse export * --labels` | no | Directory of JSON files auto-loaded as label tables at `pulse.New` time; each `*.json` becomes one table keyed by its filename, its key order the table's entry order (the `.sav` value-label code order) |
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
nullable-check overhead. `request` is opt-in (see
[`--echo-request`](#--echo-request) below); `data.components` is the
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
| 2 | A usage error refused before any library call: an unknown flag, a malformed flag value, a missing required flag or argument, or an unknown `--log-level` / `--log-format`. The usage text goes to the terminal; no envelope is written |

The rule follows the envelope: an envelope with a non-empty `errors`
always exits 1, one with only `warnings` exits 0. A leaf's own argument
checks (a missing positional `COHORT`, an unknown `--root`) run inside
the leaf, so they are a `CLI_INPUT` envelope and exit 1.

```bash
pulse api process -r req.json --json > out.json || exit $?   # 1: errors[0] in out.json
```

## Shared per-command flags

Several flags appear on multiple commands with identical semantics.

### `--input` / `-i`

Available on: every leaf that reads one cohort — `api sample`,
`api facet`, `api lookup`, `cohort inspect`, `cohort filter`, `widen`,
`dedup`, the `index` leaves, the `export` leaves, `profile create`.

The cohort path, resolved against the working directory (an absolute
path is used as-is). `cohort inspect`, `widen`, `dedup` and the `index`
leaves also accept it as the positional argument; the flag wins when
both are given. Paired with `--request` (`api facet`, `api lookup`), the
flag **replaces** the request file's `cohort` outright — `filename` and
`data_dir` — so `-i x.pulse -r req.json` reads `x.pulse` whatever
`req.json` names.

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
each row. A run that warned ends with one `{"warnings": [...]}` line
(compose: after the rows, before a ranked sweep's `ranking` line). See [Streaming & ProcessStream](../library/streaming.md).

### `--strict`

Available on: `api process`, `api predict`, `api predict-compose`,
`api predict-facet`, `api predict-chain`.

Treat request-validation warnings (e.g. numeric aggregation on a
categorical field, low-quality field description) as errors. On
`api predict` and the three `api predict-*` leaves this fails
validation (every warning the leaf reports becomes an error); on
`api process` it refuses to execute. Useful in CI gates that want the strictest possible
validation.

`import <format>`, `import auto` and `convert` also take `--strict`,
with an import meaning: it escalates a partial import
(`PULSE_IMPORT_ROWS_DROPPED`) and, on `import <format>`, the parent-group
viability warnings — see [`--strict` (import)](#--strict-import).

### `--echo-request`

Available on: `api process`, `api process-chain`, `api compose`,
`api predict`, `api predict-compose`, `api predict-facet`,
`api predict-chain`, `api sample`, `api facet`.

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

### `--schema` / `--ignore-extra`

Available on: `import csv`, `import tsv`, `import ndjson`,
`import jsonarray`, `import parquet`, `import arrow`, `import excel`,
`import spss`, `import predict`, `import auto`, `convert`.

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

Available on: `import csv`, `import tsv`, `import ndjson`,
`import jsonarray`, `import parquet`, `import arrow`, `import excel`,
`import spss`, `import predict`, `import schema-template`, `import auto`,
`convert`.

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
elided fields are printed, and reported as `elided_constants` under
`--json`. See [parent groups](../format/parent-groups.md).

### `--group`

Available on: `import csv`, `import tsv`, `import ndjson`,
`import jsonarray`, `import parquet`, `import arrow`, `import excel`,
`import spss`, `import predict`, `import auto`. Repeatable — one parent
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

Available on: every `import <format>` leaf, and `import predict`.
Default `2`.

The rows-per-distinct-tuple floor below which a `--group` draws
`PULSE_DEDUP_LOW_RATIO`. A floor of `1` disables it, leaving only the
check that the group actually makes the file smaller. The group is
written either way unless `--strict` is set.

### `--strict` (import)

Available on: every `import <format>` leaf, `import auto`, `convert`, and
`import predict` (which then fails where the import would).

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

Available on: every `import <format>` leaf, `import predict`, `import auto`.
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

Available on: every `import <format>` leaf, `import auto`, `import predict`,
`convert` and `convert predict`.

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

### `--tz` (export)

Available on: every `export <format>` leaf and `export predict`.

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

Available on: `cohort filter`.

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

### `--labels` (export)

Available on: every `export <format>` leaf and `export predict`.

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
| `pulse api materialize` | Write a request's derived columns (features, attributes, windows) as fields of a new cohort: `-r REQ.json -o OUT.pulse [--overwrite] [--strict] [--json]`; refusals keep their own code | [Materialize](../library/materialize.md) |
| `pulse api predict` | Validate a request against a cohort without executing | [api predict](api-predict.md) |
| `pulse api predict-chain` | Validate a process-chain request without executing | [api predict](api-predict.md#compose-facet-and-chain-requests) |
| `pulse api predict-compose` | Validate a Compose batch without executing | [api predict](api-predict.md#compose-facet-and-chain-requests) |
| `pulse api predict-facet` | Validate a facet request without executing | [api predict](api-predict.md#compose-facet-and-chain-requests) |
| `pulse api process` | Execute a processing request against a cohort | [api process](api-process.md) |
| `pulse api process-chain` | Source-rooted linear chain of mergeable stages | [api process](api-process.md) |
| `pulse api sample` | Return sample rows from a cohort | [api sample](api-sample.md) |
| `pulse cohort filter` | Filter a `.pulse` file to a new `.pulse` file: `-i IN -o OUT` plus `--filter EXPR` and / or `--include-from FILE --include-field F` (AND-combined, set first); `--time-zone Zone` is the zone a `to_day()` in `--filter` reads (absent = UTC) | [`--time-zone`](#--time-zone-cohort-filter) |
| `pulse cohort inspect` | Inspect a `.pulse` header and schema | [cohort inspect](cohort-inspect.md) |
| `pulse convert` | Convert between tabular formats, auto-detected from extensions | [import spss](import-spss.md), [export spss](export-spss.md) |
| `pulse convert predict` | Validate a conversion without writing output | [export spss](export-spss.md) |
| `pulse dedup` | Deduplicate an existing single-file cohort's repeated parent blocks into parent groups (format `0x02`) — the existing-cohort twin of `pulse import --group`, taking the same `--group`, `--elide-constants`, `--dedup-ratio-floor` and `--strict`. Rewrites in place (destructive, non-interactive, atomic: temp file, fsync, rename, so a refusal or failure leaves the cohort byte-identical) or, with `--out PATH`, writes a new file that must not exist. `--suggest-groups` detects candidates over the cohort's records; alone it writes nothing. An already-grouped cohort is regrouped from scratch. Refuses a shard archive (`SERVICE_VALIDATION`). An in-place rewrite names each invalidated sidecar and its rebuild command (`data.invalidated_sidecars`) and rebuilds nothing | [parent groups](../format/parent-groups.md) |
| `pulse rake` | Compute survey weights — rim weighting (raking) to inline target margins, or cell weighting when a margin joins several fields — and write them, beside every source column, as a nullable `f64` column of a NEW cohort: `COHORT -o OUT --margins JSON\|FILE [--request JSON\|FILE] [--weight-field W] [--by F] [--base-weight F] [--missing refuse\|base] [--min-weight X] [--max-weight X] [--max-iter N] [--tol T] [--total T] [--overwrite] [--json]`. `--margins` / `--request` take a JSON literal or a file; flags override the request's keys. Atomic; every refusal (`PULSE_RAKE_*`, `SERVICE_VALIDATION`) keeps its own code and writes nothing; not converging still writes, with `PULSE_RAKE_NOT_CONVERGED` | [Rake](../library/rake.md) |
| `pulse recommend` | Turn a question kind into ranked draft requests: `--intent ID` (required; an intent-taxonomy ID, `pulse skills show intents`), `--cohort PATH` to bind the drafts to the cohort's fields and predict-validate each one (omit for `<placeholder>` skeletons), `--field F` (repeatable, cohort only) to pin a field to the first role it fits, `--level basic\|intermediate\|advanced` to rank that level first, `--limit N` (default 10). Reads the cohort's header, schema and sidecar only. Refusals keep their own code: `PULSE_RECOMMEND_INTENT_UNKNOWN`, `SERVICE_VALIDATION` (bad hint, level or limit), `DATA_FILE` | `--help`; skill `tool-recommend` |
| `pulse explain` | Say in plain words what a request will do, or what a result found. `--request FILE` alone describes the request before it runs (never run; predict-checked over its cohort — header, schema and sidecar only — when it names one). `--response FILE` reads a result into findings (closed verdicts at the result's own alpha, strength bands with their convention, caveats); a `--json` envelope is unwrapped to its `data`, and a `null` figure stays undefined. `--request` beside `--response` is the request that produced it (without it an aggregation is described by count only). `--root request\|composed\|chain\|facet\|sample` (default `request`) names which root the files hold; `sample` has no response. `--detail terse\|full` (full adds narrative sentences, glossary refs, follow-ups and assumptions). Refusals keep their own code: `CLI_INPUT` (no file, bad `--root`, unreadable JSON), `SERVICE_VALIDATION` (bad `--detail`), `DATA_FILE` | `--help`; skill `tool-explain` |
| `pulse docs export` | Write the analysis reference — operator catalog, "Reading your results" pages, glossary, skills and an mdBook `SUMMARY.md` fragment — as deterministic Markdown under `--out DIR` (required). Needs no data directory; `--feature-profile PATH` (host OS path; `PULSE_FEATURE_PROFILE` is not read) scopes it to a feature profile, `--no-skills` leaves the skill pages out. An unmarked non-empty `--out` is refused with `PULSE_DOCS_EXPORT_DIR_NOT_EMPTY` | [guided analysis](../library/guided-analysis.md#exporting-the-reference) |
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
| `pulse features check` | Validate a feature profile file (host OS path) against this build exactly as `pulse.New` would; offline, so an unregistered extension-like name is a `PULSE_FEATURE_PROFILE_UNKNOWN` warning (`unverified_extension`), and any failure exits non-zero | [feature profiles](../library/feature-profiles.md) |
| `pulse features diff` | List the features this build offers that a feature profile does not list (flagged new past its `written_with`) and the names it does not resolve | [feature profiles](../library/feature-profiles.md) |
| `pulse features init` | Print strict feature-profile JSON listing every feature this build offers, or `--from NAME` seeded from an example feature profile | [feature profiles](../library/feature-profiles.md) |
| `pulse features show` | Describe every feature a feature profile lists: kind, category, source, since and dependencies | [feature profiles](../library/feature-profiles.md) |
| `pulse import arrow` | Import Arrow IPC into `.pulse` | `--help` |
| `pulse import auto` | Auto-detect a source format into the managed pool; carries the per-format read knobs `--sheet` (Excel) and `--charset` (SPSS), `--schema` / `--ignore-extra` / `--float` as on `import <format>`, `--strict` (a partial import is fatal), `--group` parent-group declarations, `--vector` / `--vector-kind` vector folds, `--source-tz` / `--source-tz-field` / `--dst-policy`, and deliberately not `--spss-missing` | [import spss](import-spss.md) |
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
| `pulse mcp` | Run the MCP server over stdio; `--metrics-addr HOST:PORT` serves Prometheus metrics on `GET /metrics` (unset opens no port); `--return PRESET` sets the response preset for a tool request without its own `return` (default `standard`); repeatable `--limit NAME=VALUE` sets an instance resource limit (`unlimited` / Go duration for `request_timeout`; a bad key or value is `CLI_INPUT`) | [mcp](mcp.md) |
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
