# pulse synth from-profile

**Audience:** CLI users topping up a real cohort with synthetic rows
whose per-field distributions match it — a tagged, source-preserving
augmentation, not a full anonymised replica.

`pulse synth from-profile` reads a profile JSON captured by
[`pulse profile create`](profile-create.md), reads the `--source`
cohort the profile was captured from, and writes a **new** `.pulse`
file combining every real row from `--source` with `--rows` newly
generated rows. The profile itself retains **no individual rows**
from the source; only summary statistics drive generation.

> **LLM agents using MCP:** see the `synthetic-data` skill.

## Synopsis

```
pulse synth from-profile --profile FILE --source FILE --output FILE --rows N
                         [--seed N] [--fidelity-report FILE]
                         [--rules FILE] [--emit-spec FILE] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--profile` | `-p` | string | (required) | Profile JSON path |
| `--source`  |      | string | (required) | Source `.pulse` cohort the profile was captured from — copied into the output, never opened for write |
| `--output`  | `-o` | string | (required) | Output `.pulse` file path — must be a distinct path from `--source` |
| `--rows`    |      | int    | (required) | Number of **new** rows to generate |
| `--seed`    |      | int    | 0          | Deterministic RNG seed |
| `--fidelity-report` | | string | (none) | Write a JSON fidelity report to this path after generation completes |
| `--rules`   |      | string | (none) | Load structural rules from a standalone JSON file and apply them to the derived spec |
| `--emit-spec` |    | string | (none) | Write the derived spec (after any `--rules` merge) to this path as indented JSON |
| `--json`    |      | bool   | false      | Emit the standard envelope |

`--rows` is required (unlike `from-schema`, which can pull it from
the spec) because the profile does not carry a generation count of
its own. **`--rows` is always an explicit count of new rows to
generate, never a "top up to N total" target** — `--rows 500` against
a 200-row `--source` cohort produces a 700-row output (200 real +
500 synthetic), not a 500-row output.

## Provenance tagging and the new-file-only contract

Every output cohort gains one appended field, `_synthetic`
(`packed_bool`): `false` on every row copied from `--source`, `true`
on every newly generated row. This is the only schema difference
between `--source` and the output — every other field is carried
through unchanged, in order.

`--output` must always be a path distinct from `--source`.
`synth from-profile` never opens `--source` for write and never
mutates it in place — an `--output` that resolves to the same file
as `--source` is refused outright, and the source cohort's bytes and
modification time are unchanged by a run.

## Inspecting and modifying the derived spec

`synth from-profile` derives a `synth.Spec` from the profile and
generates from it in one breath. Two additive flags open that step up.
Both absent, behaviour and output bytes are unchanged.

### `--emit-spec <path>`

Writes the spec that **actually generated** as indented JSON. It is the
real spec, not a summary of it: fed to
[`synth from-schema`](synth-from-schema.md) at the same `--seed` it
reproduces the same rows.

```
pulse synth from-profile -p cohort.json --source cohort.pulse \
    -o out.pulse --rows 20000 --seed 7 --emit-spec derived.json
```

It is the authoring aid — writing `{"when": "familiarity == 1", …}`
requires knowing the field is called `familiarity`, that it is a `u4`
and that its floor is 1, and nothing else prints any of that — and
equally the **diagnostic**. The emitted document is the only place a
reader can see:

- which captured `models` survived translation onto the spec,
- which distribution each field reconstructed to (`normal`, `bernoulli`,
  `mixture`, `weighted_categorical`, `constant` for a column the
  profiler could not summarise),
- which conditional pairs were retired, and which survived.

Each of those has been lost silently in this package before, every time
leaving a plausible-looking cohort behind. The spec is written **before**
generation runs, so a run that then fails still leaves the document.

### `--rules <path>`

Loads a standalone rules document and applies it to the derived spec.
Without it the whole [structural-rules](synth-from-schema.md#structural-rules)
layer is unreachable from the profile path.

The file is **the `rules` array itself** — a bare JSON array of rule
objects, identical to the `rules` key of a `from-schema` spec, so a rule
moves between the two by cut and paste:

```json
[
  {"when": "aware == 0", "set_null": ["perception_1", "perception_2"]},
  {"null_together": ["nps", "nps_reason"]}
]
```

```
pulse synth from-profile -p cohort.json --source cohort.pulse \
    -o out.pulse --rows 20000 --seed 7 --rules rules.json
```

**Merge semantics are REPLACE, not append.** `SpecFromProfile` derives no
rules, so there is nothing to append to; an append mode would only create
an ordering question no caller has asked. An empty array (`[]`) is a legal
document meaning "no rules"; a `{"rules": [...]}` wrapper is refused
rather than read as zero rules, because a run with every gate silently
missing is the exact failure eager validation exists to remove.

Validation is eager and runs before generation. Every refusal names the
**file** in `details.path` alongside the rule index, because an analyst
editing a rules file beside a spec beside a profile needs to know which
document is wrong:

| Fault | Code |
|---|---|
| A rule names a field the derived spec does not carry | `PULSE_SYNTH_RULE_FIELD_UNKNOWN` |
| A `when` or `set_expr` does not compile | `PULSE_SYNTH_RULE_EXPR_INVALID` |
| A `set` literal the target cannot hold | `PULSE_SYNTH_RULE_VALUE_INVALID` |
| The file is not readable | `DATA_FILE` |
| The file is not a JSON array of rule objects | `SERVICE_VALIDATION` |

The rule-specific codes are E1's own, not a leaf placeholder, so
`pulse errors lookup PULSE_SYNTH_RULE_FIELD_UNKNOWN` carries the recovery
prose. A refused file generates nothing.

### What a rule retires from the derived spec

A rule that **determines** a field takes it away from everything
upstream that was going to produce a value the rule overwrites. The
field's captured **linear model** stops being applied, any **conditional
pair** naming it is dropped, and it stops being a
**`residual_correlations`** participant. Each loss is warned:

```
conditional relationship conflict: field "promoter" is already claimed by
  structural rule 0 (set_expr); dropping linear model
residual correlation(s) naming [promoter] dropped: the field carries no
  surviving linear model, so it has no residual to correlate; the
  remaining participants still correlate
```

That is not primarily about saving work. The fidelity report's
[`models`](#models--did-generation-reproduce-the-captured-structure)
section asks generation's own compiler which models ran, so without the
retirement a rule-overwritten field would ship a captured-versus-recovered
coefficient delta **for a value nothing kept** — every number rendering,
and nothing on the report saying it describes something that did not
happen.

**Which rule shapes retire a field, and which deliberately do not:**

| Rule | Retires? | Why |
|---|---|---|
| `set` / `set_expr` with **no** `when` | yes | writes every row |
| any rule carrying a `when` | no | writes only some rows; the model still produces the value the rest keep |
| `set_null`, at any conditionality | **no** | it removes a value rather than supplying one — `if gate then null else inferred` needs the model to produce what the non-gated rows carry |
| `null_together` | no | copies one null decision; supplies no value |
| `set_expr` reading **its own target** | no | it transforms what generation produced rather than determining it |

The last row is why the documented pre-rounding remedy
`{"set_expr": {"nps": "int(nps)"}}` is safe — retiring `nps` there would
leave `int()` applied to a bare marginal draw. The reference is detected
on the parsed expression, so a field named `nps_reason` elsewhere in the
expression is not mistaken for one.

Measured on the 381,324-row survey cohort: an unconditional
`{"set_expr": {"promoter": "nps >= 9"}}` takes the applied models from 55
to 54, drops `promoter`'s 54 residual-correlation pairs, and removes its
`models` fidelity entry. A `set_null` over the same field changes none of
the three.

One consequence worth knowing: a rule that retires a model also removes
that model's per-row random draw, so a spec with such a rule generates a
different row sequence from the same spec without it. The determinism
contract is unaffected — same spec and seed still reproduce the same
bytes — and the change is always announced by the warning above.

### The two compose

```
pulse synth from-profile -p cohort.json --source cohort.pulse \
    -o out.pulse --rows 20000 --seed 7 \
    --rules rules.json --emit-spec derived.json
```

The emitted spec carries the merged rules, so the loop is emit → read →
write rules → apply → emit again to check what they became. `--emit-spec`
never changes the generated cohort; it is a pure diagnostic.

### A rule that never fires says so

A rule naming a mistyped field or carrying an uncompilable expression is
refused at spec parse. A rule whose `when` is simply **never true** is
refused by nothing: it validates, it compiles, it applies to no row, and
the cohort generates cleanly with the structural fact still absent.

Every rule's firings are counted during generation, and a rule that
applied to **zero** generated rows is reported — as a kind needing
**attention**, so it leads the stderr summary however many
expected-outcome lines sit below it:

```
  ! rule never fired (1)
      rule 0 never fired: when "familiarity == 99" was true on 0 of 20000
      generated row(s), so the rule applied to nothing; note a numeric
      compares PRE-ROUNDING, so normalise first (an earlier rule setting
      int(field)) or compare a range
```

The count is over rows that reached the **file**: a row a constraint
rejected was re-drawn and left no trace, so it is not a firing. A rule
that fires on every row, and one that fires on some, are silent.

The message names the pre-rounding gotcha because it is the usual cause.
The row holds the sampler's float and the wire holds `round(f)`, so
`familiarity <= 1` selects only the draws whose float is already at or
below 1. Measured on the 381,324-row survey profile at 20,000 generated
rows: the un-normalised gate fires on **1,830** rows, and the same gate
behind `{"set_expr": {"familiarity": "int(familiarity)"}}` fires on
**3,693** — the whole wire-value population it reads as. Both run
silently; only the fully-empty case is a warning.

If many rules never fire the listing is capped at 20 with a counted
`+N further rule(s) never fired` line, the same bound the thin-level and
thin-residual-pair listings use.

## Determinism

Same `(profile, source, seed, rows)` tuple → byte-identical output.
Seeds are `int64`; default `0`.

## Fidelity report

`--fidelity-report <path.json>` writes a JSON document after
generation completes, comparing the newly generated rows against the
source cohort's rows inside the one tagged output. It reuses the
existing statistical-test operators rather than new comparison math:
every **numeric** field is compared with `TEST_KS` (`split_by:
_synthetic`), and every **categorical** field with `TEST_CHISQ`
(contingency against `_synthetic`). Fields of any other on-wire type
(`date`, `datetime`, `packed_bool`, `u4`, `set_*`) are out of scope for
this section today.

Shape:

```json
{
  "source_rows": 200,
  "synthetic_rows": 500,
  "fields": [
    {"field": "score", "test": "TEST_KS", "result": {"type": "TEST_KS", "statistic": 0.04, "p_value": 0.87, "alpha": 0.05, "reject_null": false}},
    {"field": "country", "test": "TEST_CHISQ", "result": {"type": "TEST_CHISQ", "statistic": 1.2, "df": 2, "p_value": 0.55, "alpha": 0.05, "reject_null": false}}
  ],
  "pairwise": [
    {"a": "income", "b": "spend", "source_rho": 0.80, "synthetic_rho": 0.79, "delta": 0.01, "n": 20000}
  ],
  "warnings": [
    "thin numeric pair income x spend: only 12 supporting observation(s) (below 30) — reconstructed correlation may be unstable"
  ]
}
```

A field whose test could not run (e.g. a categorical column with only
one distinct value in one partition) gets `"error"` instead of
`"result"` — that single field's failure never blocks the report for
every other field, nor the generated cohort itself.

`pairwise` reports, for every numeric-numeric correlation the profile
captured (`--conditional`'s `Conditional.NumericPairs`, or the plain
`--include-correlations` stats as a fallback — whichever
`SpecFromProfile` used to populate `spec.Correlations`), the delta
between that pair's captured/target correlation (`source_rho` — the
same figure the copula reconstruction targeted) and its REALIZED
Pearson correlation over just the newly generated rows
(`synthetic_rho`); `delta` is `abs(source_rho - synthetic_rho)`. A pair
whose synthetic partition cannot produce a defined correlation (fewer
than two co-occurring non-null observations) gets `"error"` instead of
`synthetic_rho`/`delta`, following the same non-fatal-per-entry
contract as `fields`. `pairwise` is entirely absent — never an empty
array — when the spec carried no correlations at all (e.g. neither
`--conditional` nor `--include-correlations` was passed at profile
time).

### `models` — did generation reproduce the captured structure?

Every other section asks whether the generated rows *look like* the
source. `models` asks whether the captured **structure** survived
generation: for each linear model generation actually applied
(`profile create --fit-models`), it refits that same model on the
`_synthetic` partition and reports the captured coefficient beside the
recovered one, per predictor.

```json
{
  "models": [
    {
      "field": "spend",
      "latent_scale": 25.0,
      "captured_intercept": 0.0,
      "recovered_intercept": 0.004,
      "intercept_delta": 0.004,
      "n_obs": 10000,
      "r2": 0.61,
      "max_delta": 0.031,
      "predictors": [
        {"kind": "categorical_level", "field": "region", "level": "east",
         "captured_coefficient": 1.2, "recovered_coefficient": 1.187,
         "std_error": 0.012, "delta": 0.013, "n_fired": 3341},
        {"kind": "categorical_level", "field": "region", "level": "yukon",
         "captured_coefficient": 1.76, "n_fired": 0,
         "error": "predictor level is absent from the output cohort's dictionary, so no row can carry it"}
      ]
    }
  ]
}
```

Both coefficients are on the **latent scale** — units of one standard
deviation of the target's own reconstructed marginal, which is
`latent_scale`. That is not a presentation choice: a modelled numeric
is drawn as `value = Q(Φ(μ + σz))`, so its coefficients live on the
latent scale, and for a `--fit-shape` target the map from latent to
value is non-linear. Regressing the raw generated values would compare
a value-space effect against a latent coefficient and report a large
gap on a generation path that is exactly correct. Multiply by
`latent_scale` to return to the units `profile create --fit-models`
writes into the profile document.

`flagged` marks a gap large enough to indicate a real generation
fault: it fires only when `delta` exceeds **both** 0.10 latent standard
deviations **and** twice the refit's own `std_error`. A gap smaller
than the estimator's own noise cannot be evidence of anything.
`n_fired` — how many admitted synthetic rows the term's indicator was
1 on — is what separates the two ways a recovered coefficient reaches
zero: a term that fired thousands of times and recovered nothing is a
fault; a term that never fired had nothing to recover.

**A field a `--rules` rule determines has no entry here at all.** The
section reports only the models generation applied, and an unconditional
`set` / `set_expr` retires its target's model before generation runs —
see [What a rule retires from the derived
spec](#what-a-rule-retires-from-the-derived-spec). An absent field is
therefore an answer, not a gap; the conflict warning on stderr names it.

#### Reading a flag without concluding the feature is broken

**Expect flags.** On the 381,324-row survey cohort behind this feature,
55 models were applied and **30 of them flagged**. That is not a
generation failure, and the way to tell is that the flags cluster by the
target's **on-wire type** — 18 `packed_bool` targets and 9 `u4` targets
out of the 30.

The reason is a known limitation with a name. The refit inverts each
generated value back through `Q` to recover the latent it was drawn
from, and that inversion is exact — but it cannot undo what the *writer*
did afterwards. A `u4` field is stored rounded to a whole number; a
`packed_bool` field is stored as 0 or 1 and nothing else. A latent
effect of a fifth of a standard deviation survives that write only as a
small change in how many rows crossed a boundary, so most of it is gone
before anything measures it, and the refit correctly reports what is
left. Call it **quantization attenuation**. It is a real property of the
generated rows — they genuinely carry less conditioning than the model
asked for — and the fix is a discrete draw (logistic or ordinal) for
discrete targets rather than a looser tolerance. That is future work, so
for now these flags are information, not action.

What a flag on a **continuous, unrounded** target means is different,
and that is the case worth acting on. Separate the two:

| Look at | Quantization attenuation | A real generation fault |
|---|---|---|
| Target's field type | `packed_bool`, `u4`, or another narrow integer | `f32`/`f64`, or a wide integer |
| `n_fired` | Healthy — thousands of rows | Healthy — thousands of rows (a *zero* here is neither case: nothing fired, so there was nothing to recover) |
| `recovered_coefficient` | Same sign, systematically smaller | Near zero, or the wrong sign |
| Peers | Most models of the same target type flag alike | An outlier among similar targets |

`n_fired` is what stops the third row of that table being ambiguous. A
term that fired thousands of times and recovered nothing is a fault
worth reporting. A term that never fired had nothing to recover — the
level exists in the model but the field's reconstructed marginal does
not generate it, which is the absence case described above, and those
terms carry an `error` rather than a delta.

A quick triage over a report:

```bash
# how many models flagged, and of what target type
jq '[.models[] | select(.flagged)] | length'          out.fidelity.json
jq -r '[.models[] | select(.flagged) | .field] | .[]' out.fidelity.json

# the terms actually driving the flags: fired a lot, recovered little
jq '[.models[].predictors[]?
     | select(.flagged and .n_fired > 500)
     | {field, level, captured_coefficient, recovered_coefficient, n_fired}]
    | sort_by(.n_fired) | reverse | .[:10]' out.fidelity.json
```

For calibration: among strong, well-supported terms on that same cohort
(captured effect above 0.3 latent sd, `n_fired` over 500) the median
recovered/captured ratio is **0.85** — e.g. a captured 2.219 recovering
2.084. Generation is faithful there. A ratio near zero on a wide numeric
target with a healthy `n_fired` is the signature that something in the
generation path is not applying the model, and is worth reporting.

Three absence rules, all deliberate:

- A field carrying **no model** has no entry here, rather than an entry
  with empty values.
- A captured model generation did **not apply** — one whose target lost
  a conflict, or which generation refused — likewise has no entry. The
  section asks generation's own compiler which models ran; reporting a
  computed-looking delta for a relationship that was never applied is
  worse than reporting nothing, because the number looks like evidence.
- A predictor whose level the generated cohort never carries is listed
  with an `error` and no delta, for the same reason.

That last case has one cause worth naming, because it looks like a bug
and is not. A model's retained level set and the field's own captured
marginal are both cut at `--top-k`, but they are **ranked on different
bases**: the model ranks levels by frequency within the rows its fit
admitted for that one target, while the marginal ranks them across the
whole cohort. Both keep 32 and they need not agree, so a level the model
retained can be absent from the marginal that generates the field — on
the survey cohort behind this feature, 9 of `brand`'s 32 design levels,
4 of `ageExact`'s and 2 of `category`'s, 138 such terms out of 2,235.
The `"other"` catch-all is **not** one of them: it is a design column
like any other, it is generated through the categorical-pair stage, and
on that cohort all 54 catch-all terms fired, between 3,992 and 9,046
rows each.

A model whose predictor selection admitted nothing carries
`"marginal": true` and no `predictors` array. That is a *complete*
model — the field's own mean and spread — not a failure; its intercept
comparison is still a real check, since a drifted intercept there means
the marginal itself did not survive.

`models` is entirely absent when the profile carried no `models`
section, so a report for a profile captured without `--fit-models` is
unchanged.

`models` and the two **numeric-target** pair sections
(`categorical_numeric_pairwise`, `set_numeric_pairwise`) cover disjoint
sets of fields, and always will. A numeric reached by a linear model
draws from that model, so the pick-one conditional sampler those
sections score never runs for it and it gets no entry there — reporting
a delta for a mechanism that did not execute is the same mistake as
reporting one for a conflict-dropped pair. The retirement is per
**target**, not per profile: a numeric whose model was captured but not
applied keeps its conditional pair and is still scored in those
sections, because the alternative is a field reconstructed from nothing
at all. `categorical_pairwise`, `set_categorical_pairwise` and
`set_set_pairwise` are unaffected — a linear model targets a numeric and
subsumes nothing they describe.

### `model_residual_correlations` — did the structure BETWEEN models survive?

`models` asks whether each field's own captured structure survived.
This section asks whether the structure *between* modelled fields did:
for every residual correlation generation applied (`profile create
--residual-correlations`), it reports the captured rho beside the rho
recovered from the generated partition.

```json
{
  "model_residual_correlations": {
    "fields": ["spend", "tenure"],
    "compared": 1,
    "mean_delta": 0.012,
    "max_delta": 0.012,
    "pairs": [
      {"a": "spend", "b": "tenure", "captured_rho": 0.70, "recovered_rho": 0.688, "delta": 0.012, "n": 10000}
    ]
  }
}
```

This is **not** `pairwise` under another name. `pairwise` scores a
correlation between two fields' *values*, realized by the copula;
this scores a correlation between two models' *residuals*, realized by
the residual correlator. They are different numbers over the same two
fields — two fields both driven by `region` correlate strongly on raw
values while their residuals may be independent — and a modelled field
is excluded from the value-scale arm precisely so the two never both
fire on one field. The section names are how a reader tells which
mechanism a given number describes.

The residuals compared are the **refit's own**, on the latent scale.
Capture measured the residuals of a model fitted on its own rows, so
recovery measures the residuals of a model fitted on the synthetic rows
— the symmetric definition. Taking them against the captured
coefficients instead would fold the coefficient gaps `models` already
reports into a correlation and state one finding twice.

The listing is **bounded**, because the section is quadratic in
participants: 55 applied models is 1,485 pairs. `compared`, `flagged`,
`mean_delta` and `max_delta` describe every compared pair; `pairs`
carries the worst 20 by absolute `delta`, largest first, and `omitted`
counts the rest. A pair that did not make the listing recovered at
least as well as the last one that did. `flagged` fires at a `delta`
above 0.10 — a correlation is already unit-free, so unlike the
coefficient band it needs no standard-error term.

A pair generation applied but the synthetic partition cannot produce a
correlation for is listed under `unmeasured` with a `reason`
(`insufficient_overlap`, `no_variance`, or `no_model_fit` when one
endpoint's model could not be refitted). Those entries carry
`captured_rho` — so a reader can see how much structure went unverified
— and **no** recovered-rho key at all, so an unmeasured pair can never
be mistaken for one recovered at zero.

For calibration again: 55 applied models on the survey cohort produce
1,485 compared pairs, of which **147 flag**, at a mean `delta` of 0.055
and a worst of 0.490. The worst entries are the same quantized targets
the `models` section flags (`aware`×`familiarity`, `detractor`×`nps`) —
one cause, surfacing in both sections.

Because a modelled field is deliberately excluded from the value-scale
`pairwise` arm, this section is the only place the report scores
structure between two modelled fields. If you want the raw-value
correlation between them as well, compute it over the output cohort's
`_synthetic` partition yourself — on the survey cohort a source pair at
0.835 realizes at 0.791 that way, which is the residual mechanism
working as intended.

`model_residual_correlations` is entirely absent when the profile
carried no `residual_correlations` section, so a report for a profile
captured without that flag is unchanged.

`warnings` mirrors any thin-pair warnings the source profile carried
(`Profile.Warnings` — see [`pulse profile
create`](profile-create.md)'s `--conditional` section) verbatim,
letting a reader see "how well did it match" (`pairwise`) and "which
parts were built on thin data" (`warnings`) in the one document.
Absent, not an empty array, when the profile carried no warnings.

### What the report costs

The report is a footnote to generation, but on a wide cohort it is not a
small file and it is not instant. Every model is refitted, so the work
scales with the number of applied models and the width of their designs,
and `model_residual_correlations` is quadratic in applied models. On the
survey cohort — 105 captured models, 55 applied, designs up to 126
columns — the report is about **1.65 MB** and adds a few seconds to a
run that takes roughly **two minutes** in total. The refits are capped
at the first 10,000 synthetic rows regardless of `--rows`, because the
captured coefficients they are compared against were themselves
estimated from a 10,000-row sample and sharpening one side of a
comparison does not sharpen the comparison.

Budget for it in a loop, and skip the flag when you are only iterating
on `--seed`.

Omitting `--fidelity-report` writes no report at all and changes no
other behavior. The flag only has an effect on the tagged top-up path
(`--source` set); it is a no-op on plain `synth from-schema` (see
[`pulse synth from-schema`](synth-from-schema.md)), which has no
`_synthetic` partition to compare against.

## Profile shape

The profile is a `synth.Profile` JSON object produced by
`pulse profile create`. It carries per-field type, descriptive
statistics, top-K categorical entries (default K = 32), optional
pairwise correlations (when `--include-correlations` was passed at
profile-creation time), an optional row-aligned `conditional` section
(when `--conditional` was passed — preferred over the plain pairwise
stats when both are present, since its `n` is the true co-occurrence
count rather than an approximation), an optional per-numeric-field
`shape` (when `--fit-shape` was passed AND that field's fit was a
genuine improvement over normal — see
[`pulse profile create`](profile-create.md)'s `--fit-shape` section),
an optional per-numeric-field linear model (`models`, when
`--fit-models` was passed) with an optional correlation submatrix over
those models' residuals (`residual_correlations`, when
`--residual-correlations` was), and a row count. A field carrying
`shape` regenerates via the `mixture` distribution instead of `normal`,
and a field carrying a model is drawn through that model with its own
marginal as the shape — no separate flag here selects either; it is
decided entirely by the profile document's own contents.

See [`pulse profile create`](profile-create.md) for how to capture
one, and `synth/` for the underlying Go types.

## Output

### Text mode

```
Generated 1000 rows -> sales.synth.pulse (rejected 0)
Fidelity report -> sales.fidelity.json
```

Those lines are the whole of **stdout**, so a redirect stays exactly the
bytes it was.

### Warning summary and recovery headline (stderr)

Diagnostics go to **stderr**, in the same grouped, counted, capped shape
[`profile create`](profile-create.md#warning-summary) uses — kinds
needing attention first and marked `!`, expected outcomes counted
separately and marked `-`, three examples per kind. With
`--fidelity-report`, two headline lines precede it:

```
Model recovery: 55 model(s) checked, 33 flagged — see sales.fidelity.json (.models)
Residual recovery: 1485 pair(s) compared, 154 flagged — see sales.fidelity.json (.model_residual_correlations)
Warnings: 3846 in 6 kind(s) — 1 needing attention, 3845 expected
  ! residual correlation pairs unmeasured (1)
      residual correlations: 104 of 5460 pair(s) among 105 modelled field(s) could not be measured …
  - thin categorical pair (2931)
      …
      +2928 more of this kind
  - conditional relationship conflict (791)
      conditional relationship conflict: field "dma" is already claimed by categorical pair (wave -> dma); …
      …
      +788 more of this kind
  …
  Full list: sales.fidelity.json (.warnings)
```

The recovery lines exist because the sections they summarise live inside
a document that is 1.65 MB on the motivating cohort — a reader has to
learn that flags are there before deciding to open it. Both lines are
omitted when the report carries no such section, which is every spec
predating `profile create --fit-models`.

The summary spans **all three** warning channels this command touches,
because each carries findings the others do not:

| Channel | Produced by | Also written to |
|---|---|---|
| Capture-time | `profile create`, read back off the document | the profile document's `warnings` |
| Translation | `SpecFromProfile` — model drops and conditional conflicts | `--fidelity-report`'s `warnings` |
| Generation | `generate()` — the post-`--rules` arbitration, rule compilation, rules that never fired, correlation completion, model compilation | `--json`'s `data.warnings` **and** `--fidelity-report`'s `warnings` |

The middle channel is the one that carries `model for numeric field "x"
not applied: …`, and until the summary existed it reached no terminal at
all: a spec that silently applied a fraction of its captured models
generated a plausible cohort and printed `Generated 50000 rows`.

Translation and generation both derive their conflict lines from the
same arbitration, so byte-identical duplicates are dropped before
counting — 791 conflicts are reported as 791, not 1,582.

The generation channel reaches the **report** as well as the terminal,
and that is what makes the summary's `Full list:` footer true. Until it
did, the report held the two channels computed *before* the run, so a
`--rules` run's post-merge arbitration — precisely the record of which
captured relationships the rules retired — existed only on a terminal
nobody keeps. Measured on the 381,324-row profile with
`{"set_expr": {"promoter": "nps >= 9"}}` plus one never-firing rule: the
report went from 3,846 warnings that mentioned neither fact to 3,849
carrying the retired model, the dropped residual participant, and the
dead rule. The overlap with the translation channel is deduped at the
fold, so 791 shared lines are written once.

Without `--fidelity-report` no document holds the merged list, so the
footer names the flag that would produce one rather than a path that
does not exist.

### `--json`

Same envelope shape as
[`synth from-schema`](synth-from-schema.md#output). The stderr summary is
not printed on this path — `data.warnings` already carries the
generation channel, and the report carries every channel. The envelope
shape is unchanged: `warnings` is the same `[]string` slot it has always
been.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Profile parse error, infeasible constraints, missing/colliding `--source`/`--output`, or output write failure |

See `pulse errors lookup PULSE_SYNTH_SOURCE_REQUIRED` /
`PULSE_SYNTH_OUTPUT_REQUIRED` / `PULSE_SYNTH_OUTPUT_COLLISION` /
`PULSE_SYNTH_ALREADY_TAGGED` / `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`
for the specific top-up refusal codes.

## Examples

```bash
# Capture once
pulse profile create --input sales.pulse --output sales.profile.json

# Top up with different seeds — sales.pulse is read but never written
pulse synth from-profile --profile sales.profile.json --source sales.pulse --output sales.s42.pulse --rows 10000 --seed 42
pulse synth from-profile --profile sales.profile.json --source sales.pulse --output sales.s43.pulse --rows 10000 --seed 43

# Also write a per-field marginal fidelity report
pulse synth from-profile --profile sales.profile.json --source sales.pulse --output sales.s42.pulse --rows 10000 --seed 42 --fidelity-report sales.s42.fidelity.json
```

### Generating a numeric conditioned by several categoricals

The companion to [`pulse profile create`](profile-create.md#one-numeric-conditioned-by-several-categoricals)'s
worked example. Nothing here opts in — whether `spend` draws from a
model is decided entirely by whether the profile carries one for it.

```bash
pulse profile create --input survey.pulse --output survey.profile.json \
    --include-stats --conditional --fit-models --residual-correlations

pulse synth from-profile --profile survey.profile.json --source survey.pulse \
    --output survey.synth.pulse --rows 50000 --seed 7 \
    --fidelity-report survey.fidelity.json
```

Check that the model was applied at all — a captured model that
generation did not run has **no** entry in `models`, so presence is the
check:

```bash
jq '.models[] | select(.field == "spend")
    | {latent_scale, n_obs, r2, flagged,
       fields: [.predictors[].field] | unique}' survey.fidelity.json
```

```json
{
  "latent_scale": 412.6,
  "n_obs": 10000,
  "r2": 0.29,
  "flagged": null,
  "fields": ["region", "segment", "tier"]
}
```

`flagged` is omitted when nothing exceeded the band, which is the `null`
the projection shows. All three conditioning fields are present and the
model did not flag, so
the generated rows carry `region`, `segment` and `tier` effects
simultaneously — which the pick-one conditional sampler could not do.
Because `spend` is modelled it has no `categorical_numeric_pairwise`
entry, by design:

```bash
jq '[.categorical_numeric_pairwise[]? | select(.b == "spend")] | length' \
    survey.fidelity.json     # 0
```

And because the model expresses all three relationships at once, none of
them had to lose a conflict on the way in. Generation-time warnings —
the conflicts, and any model the spec could not apply — land in this
report's `warnings` array, after the capture-time ones the profile
carried:

```bash
jq -r '.warnings[] | select(startswith("conditional relationship conflict"))' \
    survey.fidelity.json     # empty for the numeric arms

jq -r '.warnings[] | select(contains("not applied"))' survey.fidelity.json
```

A `not applied` line is the one to read closely: that field fell back to
its `--conditional` pair, so it is still conditioned, but by one
categorical rather than by all of them. It is also one of the kinds the
stderr summary marks `!` and lists first, so it does not need finding.

## Limitations

- Categorical tails: anything past the captured top-K is **dropped**,
  not preserved as an `"other"` bucket — the retained levels' weights
  are renormalised, so the tail's share is redistributed across them. A
  1,900-level field therefore generates as `--top-k` levels. (The
  `"other"` catch-all that appears in a `--fit-models` design and in
  `conditional.*` tables is a different, per-section collapse and is
  generated normally.)
- Correlations: pairwise only, and only between numeric fields. The
  profile capture flag `--include-correlations` (or the more accurate
  `--conditional`) opts in; without either, fields are generated
  independently. Reconstruction uses a conditional-Gaussian
  construction (`synth/copula.go`) that exactly targets the captured
  Pearson `rho` for jointly-normal fields — see
  `skills/synthetic-data.md` for the technique and its trade-offs.
- Boolean (`packed_bool`) fields: reconstructed as `bernoulli` with
  `p` = the captured mean, which holds the prevalence exactly. Two
  consequences. A modelled boolean is drawn as a **probit**, so its
  coefficients order rows but are not probability changes; and its
  recovery is **unidentified** — the `models` section of a fidelity
  report carries an `error` for these targets rather than a recovered
  coefficient, because a 0/1 value does not determine the latent that
  produced it. A boolean observed at prevalence exactly 0 or 1 has no
  variance, so a model on it is dropped with a warning. A
  hand-authored schema-mode spec that puts a continuous distribution on
  a `packed_bool` is still biased (the writer rounds at 0.5); declare
  `bernoulli` there instead.
- Decimal and geo fields: regenerated within the same type family
  but with synthetic value distributions; downstream uses that
  depend on exact field values (e.g. joinable identifiers) need
  the schema-driven path instead.
- Shape fitting (`--fit-shape` at profile-creation time): fixed at
  exactly 2 mixture components, and no min/max clamp on the field's own
  independent draw. A field carrying a captured `shape` does not
  participate in `--conditional`'s categorical-numeric conditioning
  even when both were captured for it; it **does** compose with
  `--fit-models`, drawing through its captured linear model with the
  fitted mixture as the marginal — with the caveat that such a model's
  coefficients are on the latent scale and are not readable in data
  units. See `pulse profile create`'s `--fit-shape` section.

## Related

- [`pulse profile create`](profile-create.md)
- [`pulse synth from-schema`](synth-from-schema.md)
- `skills/synthetic-data.md` — the spec / profile grammar
