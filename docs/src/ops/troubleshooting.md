# Troubleshooting

**Audience:** operators chasing a specific failure mode in production
(file not found, permission errors, MCP transport issues, common error
codes).

This page is organised by symptom. For the exhaustive recovery
playbook keyed by error code, agents should fetch the
`error-code-reference` skill via `pulse_skills_get`; humans can also
read it at `pulse skills show error-code-reference`.

> **LLM agents using MCP:** the `error-code-reference` skill is the
> single source of truth for code-by-code recovery steps. This page
> focuses on operational symptoms that don't reduce to a single error
> code.

## "data directory required: set PULSE_DATA_DIR or pass --data-dir"

`pulse mcp` refuses to start. The MCP leaf is the one place the
binary insists on a base directory because it enumerates cohorts at
session start.

**Fix:** export `PULSE_DATA_DIR` in the client's MCP config, or pass
`--data-dir /path/to/data` on the command line. The
[`pulse mcp`](../cli/mcp.md) page has the full example.

## "file not found" / "no such file or directory"

The cohort path was resolved against the wrong base. Pulse prefers
absolute paths; with `PULSE_DATA_DIR` set, relative paths resolve
against it.

**Fix:** call `pulse cohort inspect /absolute/path/data.pulse` to
verify the file is where you think it is. If you're running inside
`pulse mcp`, check the data-dir line on stderr at startup.

## "permission denied"

Pulse runs as your user; it does not escalate. When deployed as an
MCP process under a different user (e.g. via `launchd` / `systemd`),
the cohort directory and files must be readable by that user.

**Fix:** check `id` inside the MCP startup banner on stderr; check
the file mode with `ls -l`; widen the group as needed.

## "invalid pulse magic bytes" / "unsupported pulse format version"

The file isn't a `.pulse` file — or it's from a future binary that
introduced a new format version. The reader rejects unknown versions
at parse time (see [Header Layout](../format/header.md)) so a future
binary doesn't silently mis-decode an older file.

**Fix:** verify the file with `file path/to/data.pulse` and the first
nine bytes (`hexdump -C`). The expected magic is `50 55 4c 53 45 00 00 00`
followed by a version byte (`0x01` today).

## "truncated pulse header"

The file is shorter than nine bytes or was cut off mid-write.

**Fix:** re-import. If you suspect a partial write, also check whether
the writer was killed mid-flush — Pulse writes the header first, then
the schema, then the records, so a truncated file usually fails here
before any data is observed.

## `SERVICE_VALIDATION` errors

A field name in the request doesn't exist in the cohort, or an
operator targets a field of the wrong type.

**Fix:** run [`pulse api predict`](../cli/api-predict.md) on the same
request — predict diagnoses validation failures without executing.
Common cases: typo in field name; numeric aggregation on a
categorical field (warning code
`PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`); two-pass attribute
combined with a feature (currently buffered, not invalid — predict
will flag this in `streamable_reasons`).

## `PULSE_IMPORT_*` errors

Import-time failures. The two most common:

- `PULSE_IMPORT_CATEGORICAL_OVERFLOW` — too many distinct values for
  the chosen categorical width. Either bump the width (`categorical_u16`
  / `categorical_u32`), drop the categorical encoding, or filter the
  source before re-importing. See [Dictionary
  Blocks](../format/dictionaries.md).
- `PULSE_IMPORT_DESCRIPTION_TOO_LONG` — schema field description
  exceeds 1000 bytes. Trim it.

## `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`

A warning by default, an error under `--strict`. The description is
empty, under ten characters, or a generic placeholder (`"n/a"`,
`"tbd"`, `"unknown"`, `"field"`, `"data"`, `"value"`, `"column"`).

**Fix:** edit the description in the schema JSON, re-import with
`--schema`.

## MCP "tool not found" / "no tools registered"

An MCP client connects but sees no Pulse tools.

**Fix:** check the client's MCP log (Claude Desktop surfaces this in
`~/Library/Logs/Claude/`). Common causes: `pulse` binary is not on
PATH, the wrong working directory, or `PULSE_DATA_DIR` is not set in
the MCP env block. Re-read [`pulse mcp`](../cli/mcp.md).

## mmap / file-mapping failures

On very large `.pulse` files the streaming reader uses memory mapping
where available. If your environment forbids mmap (some sandboxed
containers, very locked-down macOS configurations), the reader falls
back to a buffered read.

**Fix:** typically transparent. If you suspect a regression, run with
verbose Go runtime tracing or compare against a non-mmap file by
copying it to `/tmp` and re-running.

## When in doubt: predict, then process

Almost every "why doesn't this work" question is answerable by

```bash
pulse api predict --request request.json --json
```

Predict reads only the header and schema — it never touches record
data — and returns the full envelope of errors, warnings, and the
`streamable` flag. If predict says `valid:true` and process still
fails, the bug is in the processing layer, not the request.
