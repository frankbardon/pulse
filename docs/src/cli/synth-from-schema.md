# pulse synth from-schema

**Audience:** CLI users generating a synthetic `.pulse` cohort from a
declarative spec — for testing, demos, and bootstrapping fixtures.

`pulse synth from-schema` reads a JSON synth spec (field-by-field
distributions, row count, optional pairwise correlations) and writes
a deterministic `.pulse` file. Same `(spec, seed)` pair produces a
byte-identical output.

> **LLM agents using MCP:** see the `pulse_synth` MCP tool and the
> `synthetic-data` skill — it covers spec authoring, the 12 supported
> distributions, and constraint patterns.

## Synopsis

```
pulse synth from-schema --spec FILE --output FILE
                        [--rows N] [--seed N] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--spec`   | `-s` | string | (required) | Synth spec JSON path |
| `--output` | `-o` | string | (required) | Output `.pulse` file path |
| `--rows`   |      | int    | from spec | Override `row_count` in the spec |
| `--seed`   |      | int    | 0         | Deterministic RNG seed |
| `--json`   |      | bool   | false     | Emit the standard envelope |

## Spec shape (sketch)

```json
{
  "row_count": 10000,
  "fields": [
    {"name": "id",      "type": "u64",            "distribution": "monotonic_from", "from": 1},
    {"name": "region",  "type": "categorical_u8", "distribution": "weighted_categorical",
                         "weights": {"east": 0.4, "west": 0.4, "north": 0.1, "south": 0.1}},
    {"name": "revenue", "type": "f64",            "distribution": "lognormal", "mu": 4.0, "sigma": 0.8},
    {"name": "sold_on", "type": "date",           "distribution": "uniform_date",
                         "from": "2024-01-01", "to": "2024-12-31"}
  ]
}
```

Full spec grammar (correlations, regex, …) lives in
`skills/synthetic-data.md` and `synth/`; `constraints[]` and the
`rules[]` structural surface live in `skills/synth-structural-rules.md`.

## Categorical / numeric / set joint structure (advanced)

Beyond independent per-field distributions and `correlations`, a
hand-authored spec can also declare **conditional joint structure**
between fields directly — the same `Spec` fields `synth.SpecFromProfile`
populates from a captured profile's `conditional.*` sections (see
[`pulse profile create`](profile-create.md)), reachable in from-schema
JSON without ever running `profile create --conditional`:

| Spec field | JSON key | Reconstructs |
|---|---|---|
| `CategoricalPairs` | `categorical_pairs` | field B resampled from a captured contingency table conditioned on field A's drawn value |
| `CategoricalNumericPairs` | `categorical_numeric_pairs` | numeric field B resampled from `Normal(mean, std)` per observed category of categorical field A |
| `SetCategoricalPairs` | `set_categorical_pairs` | a `set_*` field's option (bit) resampled from `Bernoulli(P(selected \| categorical A's value))` |
| `SetNumericPairs` | `set_numeric_pairs` | numeric field B resampled from `Normal(mean, std)` conditioned on a `set_*` field's option state |
| `SetSetPairs` | `set_set_pairs` | one `set_*` field's option resampled conditioned on a DIFFERENT `set_*` field's option state |

Each entry runs as a generation-time **resample** step, applied after
every field's own independent draw and before the numeric-numeric
correlator — the field still needs its own `distribution` declared (a
`categorical_pairs` entry's `b` still needs `weighted_categorical`, a
`categorical_numeric_pairs` entry's `b` still needs `normal`); the
pair only overrides the drawn VALUE, never the field's declared shape.
Omitting all five keys reproduces plain independent-marginal generation
exactly, unchanged from a spec that never mentions them.

Two further keys are hand-authorable on the same terms. `models` (the
`Spec.Models` slot `profile create --fit-models` populates) declares one
additive linear predictor per numeric field — a field it names is drawn
from that model rather than from its own marginal, and any pair or
`correlations` entry naming the same field is dropped with a warning.
`residual_correlations` declares correlations between two **modelled**
fields' residuals, `{a, b, correlation}` exactly like `correlations` but
read on the residual scale; both endpoints must carry a `models` entry
or the spec is refused. Omitting either key is the zero state — every
residual is drawn independently, exactly as before they existed. The
construction and the latent-scale caveat for non-`normal` targets are in
`skills/synth-models.md`; the determinism rules in
`skills/synthetic-data.md`.

Minimal example — a categorical-categorical pair (`region` → `tier`)
alongside a categorical-numeric pair (`region` → `revenue`):

```json
{
  "row_count": 10000,
  "fields": [
    {"name": "region",  "type": "categorical_u8", "distribution": "weighted_categorical",
                         "weights": {"east": 0.5, "west": 0.5}},
    {"name": "tier",    "type": "categorical_u8", "distribution": "weighted_categorical",
                         "weights": {"gold": 0.5, "silver": 0.5}},
    {"name": "revenue", "type": "f64", "distribution": "normal", "mean": 500, "std": 100}
  ],
  "categorical_pairs": [
    {"a": "region", "b": "tier", "cells": [
      {"a_value": "east", "b_value": "gold",   "count": 80},
      {"a_value": "east", "b_value": "silver",  "count": 20},
      {"a_value": "west", "b_value": "gold",   "count": 30},
      {"a_value": "west", "b_value": "silver",  "count": 70}
    ]}
  ],
  "categorical_numeric_pairs": [
    {"a": "region", "b": "revenue", "categories": [
      {"category": "east", "mean": 650, "std": 90},
      {"category": "west", "mean": 350, "std": 80}
    ]}
  ]
}
```

Full cell/category shapes: `synth/spec.go`
(`CategoricalPairCellSpec`, `CategoricalNumericCategorySpec`, and the
`Set*PairSpec` family) and `skills/synthetic-data.md` ("Categorical
joint structure (generation)"). A field named by two or more of these
relationships — or by both a relationship and that field's own
`--fit-shape`-captured distribution when the spec came from
`SpecFromProfile` — is a **conflict**, not an error: one relationship
wins by a fixed priority order and every other claimant on that field
is dropped with a warning on `Result.Warnings`
(`data.warnings` under `--json`); see [`pulse profile
create`](profile-create.md) ("`--fit-shape`" section) for the full
priority order.

## Structural rules

Beyond distributions, correlations and joint structure — all of which
describe *variation* — a spec can declare **structural rules**: facts
about which fields a row may carry a value for at all, and what that
value is. A skip-logic gate ("the perception block is not asked of a
respondent who has never heard of the brand") is not a rate, and a
profile reconstructs it as soft noise with a plausible null rate and no
gate.

```json
{
  "row_count": 40000,
  "fields": [ "..." ],
  "rules": [
    {"when": "aware == 0",
     "set_null": ["perception_1", "perception_2"],
     "set": {"segment": "unaware"}},
    {"set_expr": {"promoter": "nps >= 9"}},
    {"null_together": ["nps", "nps_reason"]}
  ]
}
```

| Slot | Type | Meaning |
|---|---|---|
| `when`          | expr string | the rule applies to rows satisfying it; **absent means every row** |
| `set_null`      | array       | null these fields |
| `set`           | object      | assign these **literal** values |
| `set_expr`      | object      | assign the **result** of these expressions |
| `null_together` | array       | null this block as one decision (needs ≥ 2 distinct fields) |
| `owns_nulls`    | bool        | **modifier, not an action**: this rule is the only source of absence for the fields it names in `set_null`, so their own `null_rate` draw is discarded |

`set` and `set_expr` are separate keys on purpose: with one map,
`{"set": {"region": "west"}}` would be undecidable between a categorical
literal and a bare identifier.

Rules apply in **declaration order, sequentially, last write wins**, in
one pass at the very end of the row — after every distribution, pair,
correlation and model has settled. So a gated field is still *generated*
normally and the rule then masks or replaces it on the rows `when`
selects; rows the rule does not gate keep the value generation inferred.
An expression reads the row as it stood when **its rule** started, and a
later rule may still change it. Rules are deliberately **not** reordered
by dependency: declaration order is the one ordering you can read off the
file.

Running the pass last is what makes `if gate then null else inferred`
true; running it earlier would let a model overwrite its own rule-gated
target. The accepted consequence is that a field a rule **nulls** still
contributed its drawn value to any model that used it as a *predictor*
on that row — the propensity existed, the question was not asked.

`set_null` sets the null flag and leaves the drawn value in the row; the
file gets the type's zero, so a masked value never reaches the wire.
`set` and `set_expr` write the value and clear the null flag, because a
rule stating a field's value is stating the field has one. So `set_null`
then `set` yields the value, and `set` then `set_null` yields the null.

The pass consumes no randomness: a spec carrying rules draws exactly the
same per-row sequence as the same spec without them, and a spec
declaring no rules generates byte-identical output to one written before
the slot existed.

<!-- ANCHOR-PINNED: two pages link to this heading by its generated anchor
     (#owns_nulls-the-rule-owns-the-fields-absence) — profile-create.md and
     synth-from-profile.md. No link checker runs in CI, so a rename here breaks
     both links SILENTLY. Rename only together with those two references. -->
### `owns_nulls`: the rule owns the field's absence

`set_null` states **which rows** a field is absent on. It says nothing
about **how many**, and each target's own `null_rate` keeps firing
underneath the gate. That rate is a *marginal* the profiler captured, so
it already includes every row the gate removed, and the two compose as
`g + (1 - g) * r`: a gate that is exactly right about every gated row is
still wrong about the marginal.

Measured on the 381,324-row survey profile behind this feature, a
50-field `{"when": "aware == 0"}` gate took `regard` from a captured
0.2526 to a generated **0.4369**, and the five `*Aware` gates took
`people` from 0.4273 to **0.7506**.

```json
{"when": "aware == 0",
 "set_null": ["regard", "meaningfulness", "uniqueness"],
 "owns_nulls": true}
```

With the flag, all fifty land on **0.2455** — the gate's own firing
rate, which is the number the capture was measuring — while the gate
stays exact on 491,200 of 491,200 gated cells.

The flag is scoped to `set_null` and to nothing else; declaring it with
an empty `set_null` is `PULSE_SYNTH_RULE_OWNERSHIP_INVALID`, because a
claim that is merely ignored is invisible.

**It zeroes the field's own draw rather than applying the residual
`(r - g) / (1 - g)`.** The residual needs `g`, the rule's *firing
probability*, and a `when` is an arbitrary predicate over a row — no
spec knows how often it will be true, and estimating it would need a
generation pass whose own rows are drawn from the rates being corrected.
So the gap is **measured** instead of guessed: an owned field whose
realised null rate misses its discarded `null_rate` by more than 0.02
*and* by two standard errors is reported after generation, naming both
rates, the row count and every rule that claimed it. The second term is
not decoration — at 200 rows a correct claim on a 0.25 field misses by
more than 0.02 about half the time.

Two rules may own one field. Ownership is a **union**, not an exclusive
claim: `set_null` removes a value rather than supplying one, so it never
takes a field away from its own generation, and "two gates can each
account for this field's absence" needs no arbitration. The draw is
suppressed once and either gate nulls the field.

The field still *draws* its null and only the verdict is discarded, so
the seeded stream does not move: on the motivating cohort the other 72
fields are cell-identical to the un-owned run at the same seed, and the
only change is 381,351 null flags removed on non-gated rows — none
added, no value moved.

`null_together` already does this for its non-gate members, by copying
the gate's decision rather than by suppression, which is why naming a
never-null field first is an idiom. `owns_nulls` states it directly, on
the rule that makes it true. The block is still required for the shape
it alone expresses: a co-missing block with no gating field at all,
where one member's own draw is the block's only source of absence.

### `null_together`: one null decision for a block

A survey question block is asked or skipped as a unit, so its fields are
present or absent together. Generation draws each field's null
independently from its own `null_rate`, which turns a block into a
lottery: on a real 122-field survey profile the `nps` / `promoter` /
`passive` / `detractor` block shares `null_rate` 0.8260 exactly, and all
four came back present in **45 of 40,000** generated rows against the
~6,960 the block actually has — because `0.174⁴` is not `0.174`. With
the rule, 6,809–6,994 across five seeds, and no partial block at all.

```json
{"null_together": ["nps", "promoter", "passive", "detractor"]}
```

**The first named field's null state is copied to the rest.** Every
field has already taken its own null draw by the time the rule pass
runs, so copying an existing decision is the only resolution that adds
no randomness — and the pass consuming none is a hard contract, not a
preference. Order the block so the field you mean as the gate comes
first; it is normally the question the block hangs off.

**So every other member's own `null_rate` is ignored.** That is the cost
of the copy, it is deliberate, and the run says so out loud rather than
leaving you to find it: when a member's declared rate sits more than
**0.02** from the gate's, a warning names the rule, the gate and every
divergent field with its rate. It is a warning and not a refusal,
because a real block whose fields drifted a little — a coding
difference, a partial re-ask, a rate read back off a rounded table — is
still a block, and the copy is still the right answer for it. The
comparison is absolute and each member is measured against the *gate*,
not against the block's spread: what the copy discards is a number of
rows, so 0.80 against 0.84 matters and 0.001 against 0.002 does not.

The copy moves the null **decision**, never a value. Each member keeps
what its own sampler drew; a member the block nulls keeps it in the row
exactly as `set_null` does (the file gets the type's zero), and a member
the block **un-nulls** — the gate carried a value, the member had drawn
a null — publishes the value it drew. That second direction is what
makes the block share a *rate* rather than merely share its nulls.

Inside one rule the block is the **last** write, after `set_null`, `set`
and `set_expr`. The slots of a single rule have no order you can read
off the document, so the order is fixed: the block goes last because
that is the composition that does what it looks like —

```json
{"when": "aware == 0", "set_null": ["nps"],
 "null_together": ["nps", "promoter", "passive", "detractor"]}
```

nulls the gate and the whole block follows it. The same fixed order
means a `set_null` naming a **non-gate** member of the *same* rule is
overridden by the block. Across rules there is a real order to appeal
to and the ordinary last-write-wins applies instead: a later rule's
`set_null` over any member does break the block apart, and a later
`null_together` re-decides it from whatever the gate holds by then.

A block member that is not declared `"nullable": true` raises a **warning**
— where `set_null` over such a field is **refused**. The difference is
what each slot claims: `set_null` states the field *is* null on a
matching row, which a non-nullable field can never record, while
`null_together` states the members carry the *gate's* decision, and that
decision may be "present" on every row. The block's **gate** is never
reported at all: the copy reads it, so a non-nullable gate is not a field
the block failed to null — and a never-null gate is an idiom rather than
a mistake, since it clears every member's own null and leaves a later
`set_null` as the block's only source of absence. See below.

### `set_expr` and the coercion matrix

`set_expr` assigns what an expression computes from the row, which
collapses a derived-field relationship from several `when`-gated rules
into one unconditional rule:

```json
{"set_expr": {"promoter":  "nps >= 9",
              "passive":   "nps >= 7 && nps < 9",
              "detractor": "nps < 7"}}
```

It writes the value and clears the null flag exactly as `set` does.

An expression returns a Go value and the target field is one of the
seventeen declarable types, so the interesting surface is **which
results can land on which targets**. Getting a cell of that wrong is a
silent wrong value — a `true` reaching a `u8` as a formatted string
would encode as `0` — so the whole matrix is written down:

| expression result | scalar target | `categorical_*` | `set_*` |
|---|---|---|---|
| `true` / `false`                    | **1 / 0**                   | refused | refused |
| a number                            | the number, **range-checked** | refused | refused |
| a string                            | refused (see below)         | must be a **declared** value | refused |
| a selection (another `set_*` field, a list of option names, `split()`) | refused | refused | the selection |

*Scalar* means `u4` `u8` `u16` `u32` `u64` `f32` `f64` `date`
`packed_bool` `decimal128`. The range is the same one a `set` literal
obeys — `u4` is 0–15, `packed_bool` is 0–1 — because both go through one
implementation; `NaN` and infinities are refused. *Declared* means an
entry in a `weighted_categorical`'s `params.values`, or in a `set_*`'s
`params.options`; a `regex` or `constant` categorical has no declarable
domain, so any string is accepted there.

**A `decimal128` takes an exact string only from `set`, never from
`set_expr`.** A literal like `"1.25"` is parsed exactly, with no float
round trip. An expression cannot offer that — `expr` sees the field as
the `float64` the row holds, so anything computed already lost the
digits — and a string left in the row for a field the expression
environment types as a number would break the *next* rule that reads it,
on gated rows only. Write the literal with `set`.

**Two timings, one error.** A result type that could never land is
refused when the spec is parsed, before a single row is generated:
`{"set_expr": {"region": "nps * 2"}}` is a number aimed at a
categorical, and `expr` knows that from the types. A fault only a
*value* can settle — a number outside the target's range, a computed
category outside the declared domain — is refused at row time, because
that is the first moment it exists. Both are
`PULSE_SYNTH_RULE_VALUE_INVALID` and both name the rule index, the slot,
the field, the expression and the value.

**Inside one rule, order does not exist.** Every expression in a rule —
its `when` included — reads the row as it was *before* the rule ran, and
all of that rule's writes land afterwards. `{"set_expr": {"a": "b", "b":
"a"}}` swaps the two, the way a SQL `UPDATE` does. A `set` literal in
the same rule is **not** visible to a sibling `set_expr`. Reading the
target's own drawn value (`{"set_expr": {"nps": "nps + 1"}}`) works.
The alternative would have been alphabetical key order, which is not
something you can read off a JSON document and which would make renaming
a column change a number. *Across* rules nothing changed: an expression
sees what an earlier rule wrote and not what a later one will.

Expressions are the same `expr-lang` environment `constraints[]` uses —
every scalar including a boolean is a number (`flag == 1`, never bare
`flag`), a categorical is a string, a `set_*` is a map
(`flags["opt"]`), and `isnull(field)` tests absence. `when` must return
a bool. `isnull` takes a field NAME, bare or quoted, and a name the spec
does not declare is refused when the spec is parsed in either spelling —
the quoted form compiles, so it is reported as the unknown FIELD it is
(`PULSE_SYNTH_RULE_FIELD_UNKNOWN`), not as an expression that will not
compile.

The same array is also the standalone **rules file** format that
[`synth from-profile --rules`](synth-from-profile.md) loads, so an
inline declaration and a file are the same JSON.

Every malformed rule is refused **when the spec is parsed**, not at row
400,000: a rule naming a mistyped field, or carrying an expression that
does not compile, would otherwise generate a full cohort with the gate
silently missing. Each refusal names the rule's index (`rule_index`) —
rules have no names of their own — plus the offending slot and field.

### Two things that are silent if you get them wrong

**Nulling needs a nullable field.** The per-record null bitmap only
carries a bit for a field declared `"nullable": true`. Nulling any other
field writes the type's zero as an ordinary value with no null flag,
indistinguishable from a real `0` on a `u4` or a `packed_bool`: the rule
fires and the file cannot show it.

A `set_null` over such a field is therefore **refused when the spec is
parsed** (`PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE`, naming the rule index,
the slot and the field). The fix is on the **field**, not the rule —
declare it `"nullable": true`. On a profile-derived run a field is
nullable only if the source cohort had nulls in it, so the route is
[`--emit-spec`](synth-from-profile.md), add the flag, and generate with
`synth from-schema`. Dropping the field from `set_null` is the other
answer, if a real `0` is what the cohort should carry.

A `null_together` **member** only warns, for the reason above: that slot
copies a decision rather than making one.

**`when` and `set_expr` see the pre-rounding value — *when the field's
reconstruction is continuous*.** A numeric drawn from a continuous
distribution is a float in the row and `floor(v+0.5)` in the file, so
`familiarity == 1` tests the *drawn* value, not the `1` you read back: for
a clamped `normal` it fires on the clamp's point mass, not on the whole
wire-value-1 bucket. Use comparisons (`nps >= 9`) for those.

Two reconstructions are **exact** and need no normalisation at all. A
`packed_bool` reconstructs as `bernoulli`, whose row value is exactly `1.0`
or `0.0`, so `aware == 0` selects precisely the rows the file shows as `0`.
And an integer column (`u4`/`u8`/`u16`/`u32`/`u64`) with at most 64
observed levels reconstructs as `discrete` — its own captured histogram —
whose row value already *is* the stored integer, so `round(nps) == 9` and
`nps == 9` select the same rows and a `round()` normalisation rule over it
is a no-op.

The gap survives for three shapes, and the advice below is written for
them: an `f32`/`f64` field, an integer column too wide for the 64-level cap
(see [`profile create`](profile-create.md)), and a hand-authored spec that
puts a continuous distribution on an integer field.

It bites `set_expr` harder than `when`, because the result still looks
right. The three-band NPS rule above, written straight off `nps`, sets
exactly one flag on every scored row — it simply disagrees with the
score printed beside it whenever the float and its rounding fall on
opposite sides of a band edge. Measured on a real 122-field survey
profile at 20,000 rows: 476 of 3,486 scored rows, 13.7%. Normalise
first, in an **earlier** rule, because snapshot semantics means the same
rule will not do:

```json
"rules": [
  {"when": "!isnull(nps)", "set_expr": {"nps": "round(nps)"}},
  {"set_expr":      {"promoter":  "nps >= 9",
                     "passive":   "nps >= 7 && nps < 9",
                     "detractor": "nps < 7"},
   "null_together": ["nps", "promoter", "passive", "detractor"]}
]
```

Three things in that snippet are load-bearing, and each was measured
wrong before it was measured right.

**`round`, not `int`.** An integer field is stored as `floor(v+0.5)`, so
`round(v)` is the expression that reproduces the value the file will
hold: the flags move onto the right side of the band edge and the stored
score does not move at all. `int(v)` truncates, and `band(v)` equals
`band(floor(v))` for integer thresholds — so truncation moves no flag and
reaches the same agreement by pulling the SCORE down instead. On the
survey profile at 40,000 rows `round` repaired 1,029 band disagreements
and left the `nps` column byte-identical to the un-normalised run, while
`int` repaired none and rewrote 3,018 of 6,944 scored rows.

**The `when` on the normalisation rule.** A `set_expr` clears the
target's null flag, so an ungated `{"set_expr": {"nps": "round(nps)"}}`
un-nulls every row the score was missing from — and a `null_together`
keyed on `nps` then copies that decision to the whole block. Measured:
the block goes from present on 17.4% of rows to present on 100% of them,
with nothing refusing it.

**The classification and the block share ONE rule.** Within a rule
`null_together` is the last write, so the flags are computed and then take
the score's null decision. Declared as two rules with `null_together`
first, the `set_expr` runs afterwards and un-nulls all three flags on
every unscored row — 33,056 of 40,000 on the same measurement. The second
rule needs no `when` of its own precisely because the block resolves
after it; and having no `when` is also what makes the three flags
eligible for the pre-claim, so they are not modelled upstream only to be
overwritten.

Rules apply to **generated rows only**. `synth from-profile --source`
copies the real cohort through unchanged and tags it `_synthetic=false`;
no rule ever rewrites one of those rows.

## Supported distributions

`bernoulli`, `constant`, `discrete`, `exponential`, `lognormal`,
`mixture`, `monotonic_from`, `normal`, `pareto`, `poisson`, `regex`,
`set_bernoulli`, `uniform`, `uniform_date`, `weighted_categorical`.

`discrete` takes `values` (strictly ascending) plus optional `weights` and
emits one declared level per row at its own share — the exact marginal of a
coded integer scale, and what `profile create` reconstructs every narrow
integer column from. Its quantile function is a staircase, so a `discrete`
field used as a model target or a correlation participant carries
latent-scale effects: ordering holds, magnitude in scale points does not.

The full catalog (with parameters) is in `skills/synthetic-data.md`
and `pulse --json | jq '.data.distributions'`.

## Determinism

Same `(spec, seed)` → byte-identical output. The seed is a `int64`;
default `0`. Use a fixed seed for fixtures and a random seed for
load-testing variation.

## Output

### Text mode

```
Generated 10000 rows -> sales.pulse (rejected 0)
```

`rejected` counts rows that failed user-defined constraints
(`PULSE_SYNTH_CONSTRAINT_INFEASIBLE` when the rejection rate is too
high to make progress).

Generation warnings — conflict arbitration, correlation matrices
completed by assumption or ridge-regularized, models that could not be
compiled — are summarised on **stderr** in the same grouped, counted,
capped shape [`profile create`](profile-create.md#warning-summary) uses.
Nothing is printed when the run raised none, and nothing is printed on
the `--json` path, where `data.warnings` carries them in full.

### `--json`

```json
{
  "format_version": "1.1",
  "data": {
    "output_path": "sales.pulse",
    "rows_generated": 10000,
    "rows_rejected": 0,
    "seed": 0
  },
  "errors": [],
  "warnings": []
}
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Spec parse error, unknown distribution, infeasible constraints, or output write failure |

## Common error codes

| Code | Cause |
|---|---|
| `PULSE_SYNTH_DISTRIBUTION_UNKNOWN`  | Spec references a distribution name not in the catalog |
| `PULSE_SYNTH_CONSTRAINT_INFEASIBLE` | Constraints reject too high a fraction of generated rows |
| `PULSE_SYNTH_RULE_EMPTY`            | A rule declares no action slot (`set` / `set_expr` / `set_null` / `null_together`) |
| `PULSE_SYNTH_RULE_FIELD_UNKNOWN`    | A rule names a field the spec does not declare — in a slot, or inside `isnull("…")` |
| `PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE` | A rule's `set_null` names a field that is not declared `"nullable": true`, so the null could not be recorded |
| `PULSE_SYNTH_RULE_EXPR_INVALID`     | A rule's `when` or a `set_expr` value does not compile (`when` must return a bool) |
| `PULSE_SYNTH_RULE_VALUE_INVALID`    | A `set` literal is the wrong shape for its target field, outside the type's range, or outside the declared `values` / `options` domain |
| `PULSE_SYNTH_RULE_CONFLICT`         | One rule names the same field in two slots that disagree about it |
| `PULSE_SYNTH_RULE_BLOCK_INVALID`    | A `null_together` block names fewer than two distinct fields |
| `PULSE_SYNTH_RULE_OWNERSHIP_INVALID` | A rule declares `"owns_nulls": true` but names no field in `set_null`, so the claim has nothing to apply to |

## Examples

```bash
# Build sales.pulse from a spec
pulse synth from-schema --spec sales.spec.json --output sales.pulse --seed 42

# Override row count without editing the spec
pulse synth from-schema --spec sales.spec.json --output sales.pulse --rows 1000

# Programmatic envelope
pulse synth from-schema --spec sales.spec.json --output sales.pulse --json
```

## Related

- [`pulse synth from-profile`](synth-from-profile.md) — generate from
  a captured profile of an existing cohort
- [`pulse profile create`](profile-create.md) — capture the profile
- `skills/synthetic-data.md` — full spec grammar and distribution table
- [Synth calibration figures and design rationale](synth-calibration.md)
- `skills/synth-structural-rules.md` — `rules[]` and `constraints[]`
- [Library: pulse.Synth](../library/overview.md)
