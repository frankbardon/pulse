---
name: synth-structural-rules
description: Synth spec `rules[]` and `constraints[]` — slot semantics, declaration order and last-write-wins, the `set_expr` coercion matrix, `null_together` blocks, eager validation codes, and the shared expr row environment. Split out of the synthetic-data topical.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, rules, constraints, set_null, set_expr, null_together, PULSE_SYNTH_RULE]
---

# Synth structural rules

Two synth-spec slots act ON a drawn row rather than describing its variation: `constraints[]` rejects a row outright and makes the generator redraw, `rules[]` masks or overwrites named fields on the rows a predicate selects. They compile against the SAME `expr-lang` row environment (`rowExprEnv`), so they live together here; a second hand-rolled env is what produced issue #258.

Everything else about synth — modes, the distribution registry, pairwise correlations, profile capture, multi-predictor models, the fidelity report and the determinism contract — stays in `synthetic-data`.

## Constraints

`constraints[]` reuse `expr-lang/expr`. Row rejected if any constraint returns false; generator keeps drawing until `row_count` is reached. Default reject cap 50%; beyond that `PULSE_SYNTH_CONSTRAINT_INFEASIBLE` fires. Override via `max_rejection_rate`. Constraints read any field on the same row; no cross-row reach in v1.

**Env types are the row's types** (`sentinelFor`, derived from `fieldTypeFromName` so the two cannot drift): every scalar — `u4`/`u8`…`u64`, `f32`/`f64`, `date`, `decimal128` AND `packed_bool` — is a `float64`; `categorical_*` is a `string`; `set_*` is a `map[string]bool` (`flags["opt"]`). So a boolean field is tested as `flag == 1`, never bare `flag` (that is a compile-time refusal, not a boolean). Typing `packed_bool` as a Go `bool` was issue #258: it compiled and then failed on every row with `PROCESSING_RUNTIME: invalid operation: bool(float64)`, making constraints unusable on the commonest survey field type.

**`isnull(field)`** answers the row's null state from the per-record null mask — the only way to test absence, since a nulled field still carries its drawn value in the row. Takes the field NAME: `isnull(x)` (bare identifier, rewritten to a literal) and `isnull("x")` are equivalent; an undeclared name is refused (compile-time for a bare identifier, run-time for a string literal), never answered `false`.

## Structural rules

`rules[]` (additive, `omitempty`) state what no statistical summary can, and so comes back from marginals as soft noise with a plausible rate and no gate: a question block not ASKED of a respondent who failed a screener, a flag that is a band of another column.

```json
"rules": [
  {"when": "aware == 0", "set_null": ["perception_1", "perception_2"], "set": {"segment": "unaware"}},
  {"set_expr": {"promoter": "nps >= 9"}},
  {"null_together": ["nps", "nps_reason"]}
]
```

Five slots. `when` is an optional predicate — **absent means EVERY row**, not "never". `set` assigns LITERALS, `set_expr` expression RESULTS, `null_together` nulls a block as one decision.

**`set` and `set_expr` are sibling keys, never one map with a `{"$expr": …}` marker**: one map leaves `{"set": {"region": "west"}}` undecidable between a literal and an identifier.

Expressions are the constraint environment above (`rowExprEnv`, shared with `constraints[]`). `when` must return a bool. The pass consumes no RNG and allocates nothing per row, so a rules-free spec is byte-identical to output from before the slot existed. **The standalone rules-file format is this array itself**, so an inline declaration and a file are the same JSON.

**Declaration order, sequentially, last write wins**, in ONE pass that is `drawRow`'s last act, after the model stage — and **the placement is the design**. It buys `if gate then null else inferred`: a gated field is still drawn through its own sampler, model and pairs, and the rule masks or replaces it only where `when` selects, so non-gated rows keep the inferred value. **Accepted consequence: a field a rule NULLS still contributed its DRAWN value to any model using it as a PREDICTOR on that row.** Write semantics: `set_null` sets the mask and LEAVES the drawn value in the row (the shape `nullableSampler` produces; the wire gets the type's zero, never the masked value); `set` and `set_expr` write the value and CLEAR the mask, so declaration order composes symmetrically. A `set` literal is coerced to the row's Go shape once at compile time via `constantRowValue` — that is what turns a `set_*` target's `["tv","radio"]` into the row's `map[string]bool`.

**`null_together`: one null decision per block, and the FIRST named field wins.** A question block is asked or skipped as a unit; independent per-field null draws make it a lottery. The motivating profile's `nps`/`promoter`/`passive`/`detractor` all declare 0.8260 and came back all-present in **45 of 40,000** rows against ~6,960: `0.174⁴ ≠ 0.174`. The rule COPIES `nullMask[first]` onto the rest — every field has already taken its null draw, so a copy is the only resolution adding no randomness. **Cost: every other member's `null_rate` is IGNORED** — a member more than **0.02** from the gate's (`nullRateDivergenceThreshold`; absolute, against the GATE) warns as an ATTENTION kind, never a refusal. The copy moves the DECISION, never a value: a member the gate UN-nulls publishes what it drew, which is what makes the block share a RATE and not merely share its nulls. **Within one rule it is the LAST write**, after `set_null`/`set`/`set_expr` — `{"set_null": ["nps"], "null_together": ["nps", …]}` nulls the gate and the block follows — so a `set_null` over a NON-gate member of the same rule is overridden. Across rules, ordinary last-write-wins.

**`set_expr`: the coercion matrix.** It assigns an expression's RESULT, writing the value and clearing the mask as `set` does — collapsing three `when`-gated rules into one. A wrong cell is a SILENT wrong value, so:

| result | scalar target | `categorical_*` | `set_*` |
|---|---|---|---|
| bool | **1 / 0** | refused | refused |
| number (`float64`/`int`) | the number, RANGE-CHECKED | refused | refused |
| string | refused (see decimal) | must be a DECLARED value | refused |
| selection (`map[string]bool`, option list, `split()`) | refused | refused | **the selection** |

Scalar = `u4` `u8` `u16` `u32` `u64` `f32` `f64` `date` `packed_bool` `decimal128`. Range is the bound a `set` literal obeys (`u4` 0–15) — ONE matrix, shared; NaN/Inf refused. Declared = `weighted_categorical`'s `values` / a `set_*`'s `options`; a `regex` or `constant` categorical is unbounded, so any string lands.

**One asymmetry: a `decimal128` takes an exact string only from a `set` LITERAL.** `set_expr` refuses one — expr already reduced the value to `float64`, so the exactness is gone, and a string left in a `float64`-typed row slot breaks the NEXT rule reading it, only on gated rows.

**Two timings, one code.** A fault the RETURN TYPE settles is refused at SPEC PARSE; one only a VALUE settles (range, category) at ROW time. Both are `PULSE_SYNTH_RULE_VALUE_INVALID`, naming rule, slot, field and value.

**Across rules an expression sees EARLIER writes and not LATER ones — correct and surprising. WITHIN one rule, order is UNOBSERVABLE:** every expression, `when` included, reads the row as it was BEFORE the rule ran and all its writes land after, so `{"set_expr": {"a": "b", "b": "a"}}` swaps, like SQL `UPDATE`. A sibling `set` literal is invisible to a sibling `set_expr`; self-reference (`{"nps": "nps + 1"}`) works — and does NOT pre-claim the field (see below).

Two silent gotchas. Nulling a field not declared `"nullable": true` — via `set_null` OR a `null_together` block — writes the type's zero as an ordinary value with NO null bit: the rule fires and the file cannot show it, so it warns naming the rule index, the slot and the field. And `when` / `set_expr` read the row's PRE-ROUNDING float, not the wire value: `== k` fires only on draws landing exactly on `k`. Use comparisons (`>= 9`); `packed_bool` is exact (`1.0`/`0.0`). It bites `set_expr` harder because the result still looks right — a three-band NPS classification off raw `nps` sets exactly one flag per row and merely disagrees with the score beside it (476 of 3,486 scored rows on the motivating profile). Normalise in an EARLIER rule — snapshot semantics means the same rule will not do — and normalise with `round`, not `int`: an integer field is stored as `floor(v+0.5)`, so `round(v)` reproduces the value the FILE will hold and moves the FLAGS onto the right side of the edge, while `int(v)` truncates and reaches the same agreement by moving the SCORE down instead. **Guard it with `when` whenever the target is nullable** — a `set_expr` clears the null flag, so an ungated `{"set_expr": {"nps": "round(nps)"}}` un-nulls every row and takes any `null_together` block keyed on it along:

```json
{"when": "!isnull(nps)", "set_expr": {"nps": "round(nps)"}}
```

Measured on the motivating profile at 40,000 rows: `round` leaves the stored `nps` byte-identical to the un-normalised run and repairs 1,029 band disagreements; `int` repairs none of them and rewrites 3,018 of 6,944 scored rows. Rules reach GENERATED rows only — `synth from-profile --source` re-encodes the real partition straight through.

**Validation is EAGER — at spec parse, never at row time.** A rule naming a mistyped field, or carrying an uncompilable expression, is invisible at run time: the run succeeds and the gate is simply absent. Six codes, each naming the rule INDEX (`rule_index` — a rule has no name of its own) plus the slot and field where one exists: `PULSE_SYNTH_RULE_EMPTY`, `_FIELD_UNKNOWN`, `_EXPR_INVALID`, `_VALUE_INVALID` (a `set` literal OR a `set_expr` result — one matrix; `details.slot` says which), `_CONFLICT` (one rule naming a field in two slots that DISAGREE: within a rule there is no order to appeal to, so it is refused not arbitrated — `set_null` + `null_together` do not disagree and are ordered instead), `_BLOCK_INVALID`. `pulse errors lookup CODE` is authoritative.

## What a rule retires

A rule that DETERMINES a field **pre-claims it at priority 0** in `resolveConflicts`, ahead of the linear-model pre-claim — the rule pass runs last and an unconditional write wins outright, so nothing upstream should produce a value it discards. Three things stop for that field: its `models` entry, any conditional pair naming it, and its place in `residual_correlations`. Each loss is warned, with the ordinary wording (`conditional relationship conflict: field "promoter" is already claimed by structural rule 0 (set_expr); dropping linear model`).

**The report is the reason, not the wasted work.** `FidelityReport.Models` asks generation's own compiler which models ran, so without the claim a rule-overwritten modelled field ships a captured-versus-recovered coefficient delta for a value nothing kept — every number rendering, nothing saying it describes something that did not happen.

**Four exclusions, each SILENT if got backwards:**

| Rule shape | Claims? | Why |
|---|---|---|
| `set` / `set_expr`, no `when` | **yes** | writes every row from inputs the field's own generation does not supply |
| carries a `when` | no | writes only some rows; claiming strips the model from the rest and leaves a plausible bare marginal |
| `set_null` (any conditionality) | **no** | removes a value rather than supplying one — `if gate then null else inferred` needs the model to produce what non-gated rows keep |
| `null_together` | no | copies one null decision; supplies no value |
| `set_expr` reading its OWN target | no | transforms what generation produced rather than determining it |

The last is why the documented pre-rounding remedy `{"set_expr": {"nps": "round(nps)"}}` is safe: claiming there would leave the rounding applied to a bare marginal draw. Self-reference is detected on the PARSED expression, never by substring (`nps_reason` is not `nps`). The no-`when` restriction is sufficient rather than limiting because `set_expr` collapses the motivating three-band case into one unconditional rule.

**One contract narrows.** The pass still consumes no RNG, but a CLAIMING rule retires a model and a retired model stops drawing its own per-row normal, so the stream shifts — for a reason a warning names. A spec with no rules, or with rules that claim nothing, stays byte-identical.

## A rule that never fires

Eager validation catches a mistyped field and an uncompilable expression. It cannot catch a `when` that is simply NEVER TRUE: that rule validates, compiles, applies to nothing, and the cohort generates cleanly with the structural fact still missing — the same silent-inertness class as a scored pair generation never applied. So every rule's firings are COUNTED during generation, and a rule that applied to ZERO rows warns, naming its index, its `when` verbatim and the row count the zero is out of. The kind is ATTENTION (`rule never fired`), so it leads the stderr summary however many expected-outcome lines sit under it; an unrecognised or zero-firing outcome is never filed as expected.

The count is over rows that reached the FILE. A row a constraint rejected was re-drawn and left no trace, so it is not a firing — which keeps the figure divisible by the row count and makes a rule that fires only on rejected rows report the truth (zero). A rule firing on every row, and one firing on some, are both silent. Many dead rules list at most 20 plus a counted `+N further rule(s) never fired`.

The usual cause is the pre-rounding gotcha above, so the message says so. Measured on the motivating profile at 20,000 rows, `familiarity <= 1` fires on 1,843 rows raw, on 2,739 behind `{"set_expr": {"familiarity": "round(familiarity)"}}` — exactly the rows the file shows as `1`, with the stored value unmoved — and on 3,824 behind `int(familiarity)`, which gets there by dropping 1,085 respondents a point. All three run silently; only the empty case warns. Counting consumes no RNG and allocates nothing per row: generated bytes are identical with it and without it.

## Reaching rules from a profile

`SpecFromProfile` derives a spec and generates from it in one breath and emits no rules of its own, so `pulse synth from-profile` carries two flags that make the layer reachable — the profile path is the one the motivating use case takes.

- `--rules <path>` loads a standalone rules document and **REPLACES** `Spec.Rules` (no append mode: a derived spec carries nothing to append to, and one would only create an ordering question). The file is the `rules` array itself — a bare JSON array of rule objects — so a rule moves between a spec and a file by cut and paste. `{"rules": […]}` is refused rather than read as zero rules; `[]` is accepted and means "no rules".
- `--emit-spec <path>` writes the derived spec, AFTER the merge, as indented JSON. It is the spec that generated, not a rendering: fed to `synth from-schema` at the same seed it reproduces the same rows byte for byte. It is the authoring aid (field names, types, floors a `when` must be written against) and the diagnostic (which models survived translation, which distribution each field reconstructed to, which conditional pairs were retired). Written BEFORE generation, so a failing run still leaves the document.

Generation-time warnings — the POST-merge arbitration (which relationships the rules retired), rule-compilation warnings and the never-fired reports — also land in `--fidelity-report`'s `warnings` array, deduped against the translation channel. Before that they reached a terminal and no document, so the one file an analyst keeps could not say what the rules retired.

The two compose: emit, read what your rules became, re-run. Refusals name the FILE as well as the rule index — `details.path` on every `PULSE_SYNTH_RULE_*` raised by the load, so an analyst holding a rules file, a spec and a profile knows which document is wrong. A missing file is `DATA_FILE`, a malformed one `SERVICE_VALIDATION`; both name the path.

## See

- `synthetic-data` — modes, distributions, correlations, models, determinism contract.
- `tool-errors-lookup` — `PULSE_SYNTH_RULE_*` / `PULSE_SYNTH_CONSTRAINT_*` recovery; `pulse errors lookup CODE` is authoritative.
- `docs/src/cli/synth-from-schema.md` — CLI surface; the `rules` array is also the standalone rules-file format.
