```yaml
name: tool-materialize
kind: tool
description: Write a request's derived columns (features, attributes, windows) as ordinary fields of a new cohort that later requests read.
type: reference
applies_to: mcp
```

## When to use

CALL WHEN A LATER REQUEST MUST READ A WINDOW, FEATURE OR ATTRIBUTE COLUMN AS AN ORDINARY FIELD.
A derived value lives only inside the request that computes it: a later request cannot name it, nor a filter read an attribute or window. `pulse_materialize` writes it to a new cohort; point the next request at `out`.

## Input

- `request` (object, required): a request body (the `pulse_process` shape) with `cohort`. Only `cohort`, `label`, `features`, `filterers`, `attributes`, `windows`, `sort`, `weight`, `time_zone`, `disable_components`, `output`.
- `out` (string; required unless `predict`): the NEW `.pulse` path, relative to the data dir.
- `overwrite` (bool): replace an existing cohort at `out`.
- `predict` (bool): validate only; writes nothing.

## Output

`target`, `records` (one per filter-passing source record; source order, or `sort` order), `derived_columns` (output order: feature outputs, attributes, windows), `warnings`, `invalidated_sidecars` (overwrite only, each with its rebuild command).

`predict: true` answers under `predict`: `valid`, `columns`, `derived_columns`, `risks`, `errors` / `warnings` (the run's codes).

The cohort holds the source fields verbatim, then one nullable `f64` per derived column (an `ATTR_PREVIOUS_STATE` column keeps its state field's categorical type and dictionary), described by operator, field, params and (when read) frame.

## Gotchas

- **Nest the request.** A slot beside `out` → `PULSE_REQUEST_UNKNOWN_FIELD`; a slot outside the list → `PULSE_MATERIALIZE_SLOT_UNSUPPORTED` (`details.slot`); nothing derived → `PULSE_MATERIALIZE_NO_DERIVED`.
- **Label outputs** you will reference: the column name is the label.
- Integer window outputs are `f64`; a non-numeric one → `PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC`.
- Existing `out` without `overwrite` → `SERVICE_VALIDATION`. Refusals precede any write (atomic).
- `sort` orders ROWS only (source or derived keys, nulls last); windows follow their own `order_by`.
- The SPSS metadata sidecar is carried, no other sidecar.
- **`output`** picks, orders, renames, describes, recodes, buckets, retypes columns; applied last ([`derived-cohorts`](derived-cohorts.md), [`materialize-recode`](materialize-recode.md), [`materialize-casts`](materialize-casts.md)); `shards` / `groups` shape the file ([`materialize-container`](materialize-container.md)).

## See

- [`derived-cohorts`](derived-cohorts.md) — when to materialise, and the two-step recipe.
- [`request-envelope`](request-envelope.md) — the request slots.
- [`tool-recommend`](tool-recommend.md) — a score or feature draft's steps are this two-step recipe, drafted.
