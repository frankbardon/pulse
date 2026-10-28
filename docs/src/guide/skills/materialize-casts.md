```yaml
name: materialize-casts
description: Retype a materialized column with output.columns[].type — the cast matrix (numeric, packed_bool, date-family, set widen, categorical), decimal precision / scale, exactness, on_cast_error, and the predict risks. Topical design; child of derived-cohorts.
type: guide
kind: design
applies_to: materialize, predict
covers: [type, on_cast_error, precision, scale, cast, PULSE_MATERIALIZE_CAST_FAILED, PULSE_MATERIALIZE_CAST_UNSUPPORTED, PULSE_MATERIALIZE_CAST_NULLED]
requires: [capability:materialize, capability:materialize_output]
```

# Retyping materialized columns

An `output.columns` entry's `type` (a field type name) casts the column it reads — a source field or a derived column ([`derived-cohorts`](derived-cohorts.md)). Empty, or the column's own type, keeps it. Every cast is EXACT or fails: source integers (a `u64` past 2^53 too) never pass through a float on their way to an integer or `decimal128`.

## The matrix

| From → to | A stored value fails when |
|---|---|
| numeric (`u4`..`u64`, `f32`, `f64`, `decimal128`) → integer | fractional, negative or past the max (`f64 → u8: integral, 0 ≤ v ≤ 255`) |
| numeric → `f32` / `f64` | never (rounds to nearest), except `f64 → f32` past ±3.4e38 |
| numeric → `decimal128` | more fractional digits than `scale`, or too many integer digits; NaN / ±Inf |
| numeric → `packed_bool` | not exactly 0 or 1; `packed_bool` → numeric is 0 / 1 |
| numeric → `date` / `datetime` | not an integral epoch day / second in range; the reverse is the epoch count |
| `date` → `datetime` | never: local midnight in the zone below |
| `datetime` → `date` | only past the `int32` day range: the local day in that zone, truncated toward the past |
| `set_*` → a wider `set_*` | never: labels and dictionary unchanged |
| `categorical_*` → `categorical_*` | never: the whole dictionary (order, codes, unused labels) kept |
| `categorical_*` → numeric | the label is not a number (`unparseable`), or does not fit as one |
| numeric → `categorical_*` | NaN / ±Inf, or one distinct value more than the rung holds (`overflow`: 256 / 65536 / 4294967295) |

Refused before anything is read (`PULSE_MATERIALIZE_CAST_UNSUPPORTED`, `details.from_type` / `to_type` / `reason`): `vec_*` retyped or cast to, a categorical to or from anything but a categorical or numeric type, a categorical NARROWED below its dictionary's size (257 labels to `categorical_u8`), a set narrowed or cast to a scalar, `packed_bool` ↔ `date` / `datetime`.

**Labels.** A label parses as a number in decimal syntax only (`"3"`, `"-2.5"`, `"1e3"`; never `" 3"`, `"NaN"`, hex), exactly for an integer / `decimal128` target. A number becomes its label as JSON prints it — integers in digits, `3.0` → `"3"`, `1e6` → `"1000000"`, `-0` → `"0"`, an exponent only past 1e21 or under 1e-6 — and a `decimal128` at its scale (`"3.50"`). The written dictionary is built in FIRST-SEEN output order (after filters and sort).

**Zone.** `date` ↔ `datetime` reads local days in the request `time_zone`, else the instance default zone, else UTC — never a per-record `time_zone_field`. A day whose midnight a DST jump skips starts at its first instant ([`time-zones`](time-zones.md)).

**Decimal shape.** `precision` (1..38) and `scale` (0..precision) shape a `decimal128` target only; omitted, a decimal column keeps its own shape and any other column takes 38 / 0. A float casts its shortest printed form (`0.1`, not its binary tail).

## A value that does not fit

- Default (`on_cast_error` absent or `"fail"`): the run fails with `PULSE_MATERIALIZE_CAST_FAILED` — `details.column`, `row` (0-based output row), `value` (exact), `reason` (`fractional`, `overflow`, `not_finite`, `not_boolean`, `unparseable`). Nothing is written.
- `on_cast_error: "null"`: the cell is written null and the run warns ONCE per column, `PULSE_MATERIALIZE_CAST_NULLED` (`details.count`, `first_row`). The output column must be nullable — `nullable: true` on a non-nullable source.

`PULSE_MATERIALIZE_OUTPUT_INVALID` reasons: `unknown_type`, `decimal_shape` (out of range, or on a non-decimal type), `on_cast_error_invalid`, `on_cast_error_without_type`, `on_cast_error_not_nullable`.

## Predict first

Predict refuses every static case above with the run's code and details, and lists each cast a stored value CAN fail under `risks`: `{column, path: "output.columns[i].type", code, check}` — `check` the condition above, `code` `PULSE_MATERIALIZE_CAST_FAILED` (or `_CAST_NULLED` under `on_cast_error: "null"`). A widening cast lists nothing.

## Gotchas

- A cast column's SPSS sidecar variable is rebuilt from its new type (a categorical: an unlabelled string variable of its labels): old value labels never land on cast values — a cast previous-state column drops its state's labels too.
- An SPSS import's categoricals hold the numeric CODES as labels: cast one to `u8` to get the codes as numbers.
- A `rank` or row-number window casts to `u32` exactly (integral); a ratio cast to an integer fails at its first fraction — keep it `f64`, or null the fractions with `on_cast_error`.
- Casts run after filters and sort, on the written rows only.

## See

- [`derived-cohorts`](derived-cohorts.md) — the output slot and the two-step recipe.
- [`tool-materialize`](tool-materialize.md) — the MCP tool.
- [`time-zones`](time-zones.md) — zone resolution and DST.
