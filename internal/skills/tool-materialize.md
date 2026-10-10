---
name: tool-materialize
kind: tool
description: Write a request's derived columns (features, attributes, windows) as ordinary fields of a new cohort that later requests read.
type: reference
applies_to: mcp
---

## When to use

CALL WHEN A LATER REQUEST MUST READ A WINDOW, FEATURE OR ATTRIBUTE COLUMN AS AN ORDINARY FIELD.
A derived value lives only inside the request that computes it: a regression, grouper, filter or join cannot name it. `pulse_materialize` writes it to a new cohort; point the next request at `out`.

## Input

- `request` (object, required): a request body<!-- feature: capability:process --> (the `pulse_process` shape)<!-- /feature --> with `cohort`. Only `cohort`, `label`, `features`, `filterers`, `attributes`, `windows`, `weight`, `time_zone`, `disable_components`.
- `out` (string, required): the NEW `.pulse` path, relative to the data dir.
- `overwrite` (bool): replace an existing cohort at `out`.

## Output

`target`, `records` (one per filter-passing source record, source order), `derived_columns` (output order: feature outputs, attributes, windows), `warnings`, `invalidated_sidecars` (overwrite only, each with its rebuild command).

The cohort holds the source fields verbatim, then one nullable `f64` per derived column, described by operator, field and params.

## Gotchas

- **Nest the request.** A slot beside `out` → `PULSE_REQUEST_UNKNOWN_FIELD`; a slot outside the list → `PULSE_MATERIALIZE_SLOT_UNSUPPORTED` (`details.slot`); nothing derived → `PULSE_MATERIALIZE_NO_DERIVED`.
- **Label the outputs** you will reference; the column name is the label (or the operator's default output name).
- Integer window outputs (row number, rank) are stored as `f64`; a non-numeric output → `PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC`.
- Existing `out` without `overwrite` → `SERVICE_VALIDATION`. Every refusal happens before anything is written; the write is atomic.
- Output is always a single-file cohort; source sidecars are not carried.

## See

- `derived-cohorts` — when to materialise, and the two-step recipe.
- `request-envelope` — the request slots.
