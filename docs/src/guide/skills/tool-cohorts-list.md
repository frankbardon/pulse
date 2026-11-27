```yaml
name: tool-cohorts-list
kind: tool
description: List every .pulse cohort under the data directory with its record count and fields.
type: reference
applies_to: inspect, mcp
```

## When to use

CALL FIRST WHEN YOU DO NOT KNOW WHICH COHORT HOLDS THE DATA.
Pick the cohort a question needs from the fields each one carries, instead of guessing filenames. `pulse_imports_list` lists managed imports only; this lists every cohort, imported or not.

## Input

- `dir` (string, optional): one subdirectory of the data directory, relative to it. Omit for the whole data directory. Absolute or `..` → `SERVICE_VALIDATION`; missing → `SERVICE_RESOURCE`.

## Output

`{cohorts: [...]}` (no envelope), sorted by `path`, each `{path, record_count, fields: [{name, type, nullable}], shard_archive, shard_count, managed, handle, warnings, error}`. `path` is relative to the data directory: pass it as the cohort `filename` (no `data_dir`) or as `pulse_inspect` `path`. `shard_count` appears for an archive, `handle` for a managed import, `warnings` (`ENCODING_INVALID`, truncated tail) and `error` only when present. `cohorts: []` when there are none (never null).

## Gotchas

- Header + schema only, never a record; dictionaries and descriptions come from `pulse_inspect`.
- Every file ending `.pulse`, in every subdirectory. Not listed: sidecars (`.idx`, `.indexes.json`, `.spss.json`, `.meta.json`), profiles, hidden files and directories (leading `.`), the shards inside an archive.
- An unreadable `.pulse` file is listed with its coded `error` and `fields: []`.
- Listing does not slide a managed import's expiry (`pulse_imports_list` reports it).

## See

- [`tool-inspect`](tool-inspect.md) — one cohort's full schema.
- [`tool-imports-list`](tool-imports-list.md) — managed-import expiry.
- [`session-bootstrap`](session-bootstrap.md)
