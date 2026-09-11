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

**`isnull(field)`** answers the row's null state from the per-record null mask — the only way to test absence, since a nulled field still carries its drawn value in the row. Takes the field NAME: `isnull(x)` (rewritten to a literal) and `isnull("x")` are equivalent, and an undeclared name in EITHER form is refused before a row exists — the bare form by expr's unknown-name check, the string form by a parse pass (`isnullUnknownField`) on rules and `constraints[]` alike, never answered `false`. Only an argument that is a string at RUN time (`isnull(region + "!")`) reaches the loud run-time refusal.

## Structural rules

`rules[]` (additive, `omitempty`) state what no statistical summary can, and so comes back from marginals as soft noise with a plausible rate and no gate: a question block not ASKED of a respondent who failed a screener, a flag that is a band of another column.

```json
"rules": [
  {"when": "aware == 0", "set_null": ["perception_1", "perception_2"], "set": {"segment": "unaware"}},
  {"set_expr": {"promoter": "nps >= 9"}},
  {"null_together": ["nps", "nps_reason"]}
]
```

Five slots plus one modifier. `when` is an optional predicate — **absent means EVERY row**, not "never". `set` assigns LITERALS, `set_expr` expression RESULTS, `null_together` nulls a block as one decision, and `owns_nulls` changes what a `set_null` MEANS rather than adding an action.

**`set` and `set_expr` are sibling keys, never one map with a `{"$expr": …}` marker**: one map leaves `{"set": {"region": "west"}}` undecidable between a literal and an identifier.

Expressions are the constraint environment above (`rowExprEnv`, shared with `constraints[]`). `when` must return a bool. The pass consumes no RNG and allocates nothing per row, so a rules-free spec is byte-identical to output from before the slot existed. **The standalone rules-file format is this array itself**, so an inline declaration and a file are the same JSON.

**Declaration order, sequentially, last write wins**, in ONE pass that is `drawRow`'s last act, after the model stage — and **the placement is the design**. It buys `if gate then null else inferred`: a gated field is still drawn through its own sampler, model and pairs, and the rule masks or replaces it only where `when` selects, so non-gated rows keep the inferred value. **Accepted consequence: a field a rule NULLS still contributed its DRAWN value to any model using it as a PREDICTOR on that row.** Write semantics: `set_null` sets the mask and LEAVES the drawn value in the row (the shape `nullableSampler` produces; the wire gets the type's zero, never the masked value); `set` and `set_expr` write the value and CLEAR the mask, so declaration order composes symmetrically. A `set` literal is coerced to the row's Go shape once at compile time via `constantRowValue` — that is what turns a `set_*` target's `["tv","radio"]` into the row's `map[string]bool`.

**`null_together`: one null decision per block, and the FIRST named field wins.** A question block is asked or skipped as a unit; independent per-field null draws make it a lottery. The motivating profile's `nps`/`promoter`/`passive`/`detractor` all declare 0.8260 and came back all-present in **45 of 40,000** rows against ~6,960: `0.174⁴ ≠ 0.174`. The rule COPIES `nullMask[first]` onto the rest — every field has already taken its null draw, so a copy is the only resolution adding no randomness. **Cost: every other member's `null_rate` is IGNORED** — a member more than **0.02** from the gate's (`nullRateDivergenceThreshold`; absolute, against the GATE) warns as an ATTENTION kind, never a refusal. **The gate itself is never reported by either warning**: the copy READS it, so a non-nullable gate is not a field the block failed to null — and a never-null gate is the idiom (it clears the members' own nulls so a later `set_null` is the block's only absence). The copy moves the DECISION, never a value: a member the gate UN-nulls publishes what it drew, which is what makes the block share a RATE and not merely share its nulls. **Within one rule it is the LAST write**, after `set_null`/`set`/`set_expr` — `{"set_null": ["nps"], "null_together": ["nps", …]}` nulls the gate and the block follows — so a `set_null` over a NON-gate member of the same rule is overridden. Across rules, ordinary last-write-wins.

**`owns_nulls`: the rule is the field's ONLY source of absence.** `set_null` states WHICH rows are absent and says nothing about HOW MANY, so each target's own `null_rate` keeps firing beneath the gate — and that rate is a MARGINAL the profiler captured, already inclusive of everything the gate removed. The two compose as `g + (1-g)·r`. Measured on the motivating profile, a 50-field `{"when": "aware == 0"}` gate that is exactly right about every gated row took `regard` from a captured 0.2526 to **0.4369**, and the five `*Aware` gates took `people` from 0.4273 to **0.7506**. Adding `"owns_nulls": true` DISCARDS those fields' own null draw and lands all fifty on 0.2455 — the gate's own firing rate, which is the number the capture was measuring — with the gate still exact on 491,200 of 491,200 gated cells.

```json
{"when": "aware == 0", "set_null": ["regard", "charming", …], "owns_nulls": true}
```

Scoped to `set_null` and nothing else: declaring it with an empty `set_null` is `PULSE_SYNTH_RULE_OWNERSHIP_INVALID` (an ignored claim is invisible). **It zeroes the draw rather than applying the residual `(r − g)/(1 − g)`, because `g` — the rule's FIRING PROBABILITY — is a property of the data and no spec knows it.** So the gap is MEASURED instead of guessed: any owned field whose realised rate misses its discarded `null_rate` by more than **0.02** *and* two standard errors warns as an ATTENTION kind naming both rates, the row count and every claiming rule. The noise term matters — at 200 rows a correct claim on a 0.25 field misses by 0.02 about half the time. **Ownership is a UNION, not a `claim()`**: two gates may each own one field (the draw is suppressed once, either gate nulls it) because `set_null` removes a value rather than supplying one and never claims a field away from its own generation. The field still DRAWS its null and only the verdict is discarded, so the seeded stream is unmoved — on the motivating cohort the other 72 fields are cell-identical to the un-owned run, and the only change is 381,351 null flags REMOVED on non-gated rows, 0 added, 0 values moved. **`null_together` already does this for its non-gate members, by COPY** — the gated-block idiom (name a never-null field first so the copy clears the block's own nulls, then supply the real gate with a `set_null`) is ownership as a side effect, reaching 0.2455 by the same route. `owns_nulls` says it directly, on the rule that makes it true; the block stays REQUIRED for a co-missing block with no gate at all, where one member's own draw is the block's only absence.

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

Two silent gotchas. Nulling a field not declared `"nullable": true` writes the type's zero as an ordinary value with NO null bit — the rule fires and the file cannot show it. **`set_null` over such a field is REFUSED** (`PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE`); the fix is on the FIELD, and on a profile-derived run a field is nullable only if the source had nulls (`--emit-spec`, add the flag, `from-schema`). A `null_together` MEMBER only warns, because that slot copies the gate's decision — which may be "present" on every row — rather than claiming a null. And `when` / `set_expr` read the row's PRE-ROUNDING float, not the wire value: `== k` fires only on draws landing exactly on `k`. Use comparisons (`>= 9`); `packed_bool` is exact (`1.0`/`0.0`). It bites `set_expr` harder because the result still looks right — a three-band NPS classification off raw `nps` sets exactly one flag per row and merely disagrees with the score beside it (476 of 3,486 scored rows on the motivating profile). Normalise in an EARLIER rule — snapshot semantics means the same rule will not do — and normalise with `round`, not `int`: an integer field is stored as `floor(v+0.5)`, so `round(v)` reproduces the value the FILE will hold and moves the FLAGS onto the right side of the edge, while `int(v)` truncates and reaches the same agreement by moving the SCORE down instead. **Guard it with `when` whenever the target is nullable** — a `set_expr` clears the null flag, so an ungated `{"set_expr": {"nps": "round(nps)"}}` un-nulls every row and takes any `null_together` block keyed on it along:

```json
{"when": "!isnull(nps)", "set_expr": {"nps": "round(nps)"}}
```

Measured on the motivating profile at 40,000 rows: `round` leaves the stored `nps` byte-identical to the un-normalised run and repairs 1,029 band disagreements; `int` repairs none of them and rewrites 3,018 of 6,944 scored rows. Rules reach GENERATED rows only — `synth from-profile --source` re-encodes the real partition straight through.

**Validation is EAGER — at spec parse, never at row time.** A rule naming a mistyped field, or carrying an uncompilable expression, is invisible at run time: the run succeeds and the gate is simply absent. Eight codes, each naming the rule INDEX (`rule_index` — a rule has no name of its own) plus the slot and field where one exists: `PULSE_SYNTH_RULE_EMPTY`, `_FIELD_UNKNOWN` (a slot's name, AND an undeclared name inside `isnull("…")` — that expression COMPILES, so it is an unknown field and not an `_EXPR_INVALID`), `_FIELD_NOT_NULLABLE` (`set_null` only), `_EXPR_INVALID`, `_VALUE_INVALID`, `_CONFLICT` (one rule naming a field in two slots that DISAGREE — `set_null` + `null_together` do not disagree and are ordered instead), `_BLOCK_INVALID`, `_OWNERSHIP_INVALID` (`owns_nulls` with an empty `set_null`). A `when` that fails at RUN time stays `PROCESSING_RUNTIME` — the causes left are generic expr faults (a dynamic `isnull` argument, `int()` of a non-numeric string), not "a malformed rule", and the coded error already carries the rule index, slot and expression. `pulse errors lookup CODE` is authoritative.

## What a rule retires

A rule that DETERMINES a field **pre-claims it at priority 0** in `resolveConflicts`, ahead of the linear-model pre-claim — the rule pass runs last and an unconditional write wins outright, so nothing upstream should produce a value it discards. Three things stop for that field: its `models` entry, any conditional pair naming it, and its place in `residual_correlations`. Each loss is warned, with the ordinary wording (`conditional relationship conflict: field "promoter" is already claimed by structural rule 0 (set_expr); dropping linear model`).

**The report is the reason, not the wasted work.** `FidelityReport.Models` asks generation's own compiler which models ran, so without the claim a rule-overwritten modelled field ships a captured-versus-recovered coefficient delta for a value nothing kept — every number rendering, nothing saying it describes something that did not happen.

**Four exclusions, each SILENT if got backwards:**

| Rule shape | Claims? | Why |
|---|---|---|
| `set` / `set_expr`, no `when` | **yes** | writes every row from inputs the field's own generation does not supply |
| carries a `when` | no | writes only some rows; claiming strips the model from the rest and leaves a plausible bare marginal |
| `set_null` (any conditionality), `owns_nulls` or not | **no** | removes a value rather than supplying one — `if gate then null else inferred` needs the model to produce what non-gated rows keep. Ownership suppresses the field's own NULL DRAW and claims nothing: the value is still the model's |
| `null_together` | no | copies one null decision; supplies no value |
| `set_expr` reading its OWN target | no | transforms what generation produced rather than determining it |

The last is why the documented pre-rounding remedy `{"set_expr": {"nps": "round(nps)"}}` is safe: claiming there would leave the rounding applied to a bare marginal draw. Self-reference is detected on the PARSED expression, never by substring (`nps_reason` is not `nps`). The no-`when` restriction is sufficient rather than limiting because `set_expr` collapses the motivating three-band case into one unconditional rule.

**One contract narrows.** The pass still consumes no RNG, but a CLAIMING rule retires a model and a retired model stops drawing its own per-row normal, so the stream shifts — for a reason a warning names. A spec with no rules, or with rules that claim nothing, stays byte-identical.

## A rule that never fires

Eager validation catches a mistyped field and an uncompilable expression. It cannot catch a `when` that is simply NEVER TRUE: that rule validates, compiles, applies to nothing, and the cohort generates cleanly with the structural fact still missing — the same silent-inertness class as a scored pair generation never applied. So every rule's firings are COUNTED during generation, and a rule that applied to ZERO rows warns, naming its index, its `when` verbatim and the row count the zero is out of. The kind is ATTENTION (`rule never fired`), so it leads the stderr summary however many expected-outcome lines sit under it; an unrecognised or zero-firing outcome is never filed as expected.

The count is over rows that reached the FILE. A row a constraint rejected was re-drawn and left no trace, so it is not a firing — which keeps the figure divisible by the row count and makes a rule that fires only on rejected rows report the truth (zero). A rule firing on every row, and one firing on some, are both silent. Many dead rules list at most 20 plus a counted `+N further rule(s) never fired`.

**The message names the cause that applies to THAT rule, in three arms.** (1) CONSTRAINT — the rule DID select rows and a constraint rejected every one; recorded during the run (the attempt is remembered, the acceptance is counted) rather than inferred, so the line blames the constraint and says nothing about rounding. (2) PRE-ROUNDING — the predicate reads a field that rounds on write (`u4`/`u8`/`u16`/`u32`/`u64`, `date`, `packed_bool`) AND draws continuous values; the remedy is `round(field)` in an earlier rule, never `int()`. Measured on the motivating profile at 20,000 rows, `familiarity <= 1` fires on 1,843 rows raw, on 2,739 behind `{"set_expr": {"familiarity": "round(familiarity)"}}` — exactly the rows the file shows as `1`, with the stored value unmoved — and on 3,824 behind `int(familiarity)`, which gets there by dropping 1,085 respondents a point. (3) NEUTRAL — neither; the line names each field the predicate reads with its type and distribution, and RULES PRE-ROUNDING OUT for a field whose row value already IS the stored value.

That third arm is why the split exists. A field that rounds on write still draws exact values when its distribution is `discrete`, `bernoulli`, `uniform_date`, `poisson` or `monotonic_from` — and a small integer reconstructs as `discrete` and a `packed_bool` as `bernoulli` automatically, so the pre-rounding advice is WRONG for most survey columns: on the motivating cohort **103 of 123 fields** round on write and draw exact values, against 3 that carry the hazard. Sending an author to normalise a gate that is already exact is worse than saying nothing. All three arms run silently on a rule that fires; only the empty case warns. Counting consumes no RNG and allocates nothing per row: generated bytes are identical with it and without it.

## Reaching rules from a profile

`SpecFromProfile` derives a spec and generates from it in one breath and emits no rules of its own, so `pulse synth from-profile` carries two flags that make the layer reachable — the profile path is the one the motivating use case takes.

- `--rules <path>` loads a standalone rules document and **REPLACES** `Spec.Rules` (no append mode: a derived spec carries nothing to append to, and one would only create an ordering question). The file is the `rules` array itself — a bare JSON array of rule objects — so a rule moves between a spec and a file by cut and paste. `{"rules": […]}` is refused rather than read as zero rules; `[]` is accepted and means "no rules".
- `--emit-spec <path>` writes the derived spec, AFTER the merge, as indented JSON. It is the spec that generated, not a rendering: fed to `synth from-schema` at the same seed it reproduces the same rows byte for byte. It is the authoring aid (field names, types, floors a `when` must be written against) and the diagnostic (which models survived translation, which distribution each field reconstructed to, which conditional pairs were retired). Written BEFORE generation, so a failing run still leaves the document.

Generation-time warnings — the POST-merge arbitration (which relationships the rules retired), rule-compilation warnings and the never-fired reports — also land in `--fidelity-report`'s `warnings` array, deduped against the translation channel. Before that they reached a terminal and no document, so the one file an analyst keeps could not say what the rules retired.

The two compose: emit, read what your rules became, re-run. Refusals name the FILE as well as the rule index — `details.path` on every `PULSE_SYNTH_RULE_*` raised by the load, so an analyst holding a rules file, a spec and a profile knows which document is wrong. A missing file is `DATA_FILE`, a malformed one `SERVICE_VALIDATION`; both name the path.

## Detecting rules from the data (`--suggest-rules`)

Coverage bounds the layer's value and rules get written from RECALL. `pulse profile create --suggest-rules <path>` measures `P(target null | gate = level)` on the SAME scan (no extra byte read) for every low-cardinality field — `categorical_*`, `packed_bool`, `u4` — and proposes a `set_null` rule for each field whose levels split a target's null rate into ~1 and ~0 (`gateHighNullRate` 0.98 / `gateLowNullRate` 0.02, package constants, not flags). The file is the bare rules array `--rules` consumes UNMODIFIED.

**Proposed, never applied.** Detection finds the STATISTICAL gate; a human knows the SEMANTIC one. Measured on the motivating cohort it proposes `aware` AND `familiarity == 1` with identical evidence to sixteen digits — they gate the same 96,326 rows with zero exceptions, so nothing in the data can separate them. Read each candidate's evidence, correct the `when`, delete the rest.

**Evidence rides the rule, on `_evidence`** — the ONE inert slot of `RuleSpec`. Generation never reads it, an evidence-only rule is still `PULSE_SYNTH_RULE_EMPTY`, and hand-authoring it is harmless. It lives there rather than in a sibling block because the file must stay a bare array (a wrapper object is refused) and because evidence in a second document drifts the first time a candidate is deleted. It carries per-level conditional null rates and support, per-target gated/open rates, `rows_affected`, `gated_share` and `max_null_rate_deviation`.

`gated_share` is the `1 - P(gate)` figure that identified this cohort's gate by hand (0.2526093295989762, matching 50 fields' `null_rate` to ten digits). It is a CHECKING aid, not the ranking signal: the split test arithmetically implies it, so every admitted candidate scores well. Ranking is target count, then rows affected.

Numeric gates are emitted through `round()`, uniformly, including `packed_bool`. Bare `==` reads the pre-rounding float and silently under-fires — 531 of 901 rows in the committed regression.

Two things it will NOT propose, both counted in `Profile.Warnings` rather than dropped silently: a field with more than `maxGateLevels` (16) observed levels (an attribute, not a branch — abandoned, never truncated), and a gate whose ONLY gated level is the null pseudo-level, which is co-missingness rather than value gating (the section below proposes those). Thin candidates SHIP with their support attached, warned thinnest-first and capped.

**Every gating candidate carries `"owns_nulls": true`, and the detector's own thresholds are what make that correct**: a pair is admitted only when the target is null on ≥0.98 of gated rows and ≤`gateLowNullRate` (0.02) of OPEN rows — and `P(null | open)` IS the residual the flag zeroes — so the suppression is inside the 0.02 the divergence warning calls immaterial, by construction. Emitting without it was strictly worse: the gate was right about WHICH rows were absent and wrong about HOW MANY. Measured on the motivating cohort's 11-candidate accepted file at 40,000 rows, the 50 gated fields moved from **0.4320 to 0.2455** (captured 0.2526) and `people` from 0.7506 to 0.4208 (captured 0.4273), with the coherence table, the candidate ordering and the zero orphan count all unchanged. No candidate's claim diverged enough to warn. A block candidate never carries the flag — it discards its non-gate members' rates by copying the gate's decision.

### Co-missing blocks and always-null columns

The same scan also proposes `null_together` candidates (`detector: "co_missing"`, evidence on `_evidence.block`): fields nulled as one question block.

**An identical `null_rate` is NEVER enough**, and that is the whole of it. Two unrelated fields can share a rate to sixteen digits and overlap by chance, so the rate is only the grouping key and admission is IDENTICAL NULL PATTERN — null on exactly the same rows. Every member of an emitted block therefore carries `agreement: 1`, an identical `null_count`, and `max_null_rate_deviation: 0`, which is precisely E1-S5's divergence measure: no member's declared rate is discarded, and the member order is arbitrary by construction rather than a ranking.

A NEAR block is reported in the warnings with its agreement and its disagreeing row count, never emitted: `null_together` has no dial for "almost" and, unlike a gating candidate's `when`, there is nothing in it for an analyst to correct. The agreement is the null-SET overlap, not row-level agreement — two independent fields each null at 1% agree on 98% of ROWS.

An ALWAYS-NULL column is its own finding, named with its type, and belongs to no block and no gate. **No rule is proposed for it because one would change nothing**: a column the profiler summarised nothing for reconstructs as a typed `constant` with `null_rate` 1.0, so every row is nulled and generation reproduces it exactly (measured: `lgbt` null on 20,000 of 20,000 generated rows). The thing to check is the source or the slice profiled, not the rules file.

Bounded by construction: a per-field bitset over `blockChunkRows` rows folded into a co-null matrix by popcount, so memory is flat in the row count; over `maxBlockFields` (256) nullable fields the detector abandons rather than truncating. Blocks rank largest-first, capped with a counted remainder.

**EMITTED ORDER IS APPLIED ORDER, and a candidate WRITING a field another candidate READS is emitted BEFORE it.** The preference — gating, then blocks, then dependencies — is kept everywhere nothing forces a move: a block AFTER a gate repairs a `set_null` you narrow by hand, and a dependency carries its own `null_together`, so it re-resolves its block from the source last. The preference alone was wrong about a gate's SOURCE. The equivalence that makes block-after-gate safe (identical patterns classify identically, so a gate takes a whole block or none of one) governs the fields a gate WRITES; a block member that is a gate's `when` FIELD moves the gate's own input after the gate read it. Measured on the motivating cohort, where five `*Aware` gates sit inside the 50-field block and one `set_expr` writes a sixth gate's field: **8,693 of 20,000 generated rows carried an answer to a question the same file said was never asked; 0 after the reorder.** Only forced candidates move — the headline gate stays first, one block is hoisted ahead of the five gates reading it. If you reorder the file by hand, keep writers before readers. A mutual pair (each writing a field the other reads) has no satisfying order: one edge is dropped and the drop is warned about.

Measured on the 381,324-row motivating cohort: four blocks — 50 fields at 0.2526, **26 at 0.3564 (the cluster no single gate could explain)**, 13 at 0.2649 and the four-field NPS block at 0.8260 — plus one always-null column and four near misses, alongside the eight gating candidates, in one file `--rules` consumes unmodified.

### Exact dependencies (`set_expr` candidates)

The third detector (`detector: "dependency"`, evidence on `_evidence.dependency`) reads VALUES, not null state: a field that IS a function of ONE other on every row where both are present. `promoter` is `nps >= 9`, and generation otherwise samples it independently and models it, so the cohort contains promoters scoring 3.

**The search is NARROW and the bounds ride the file.** Targets `packed_bool`/`u4` ONLY; sources `categorical_*`/`packed_bool`/`u4` with at most `maxDepLevels` (16) observed levels; exactly ONE source. Wider numerics, `date`, `set_*`, categorical-VALUED targets and joint two-field dependencies were not looked for — **a field missing from the file was not cleared, it was not examined**, and the note says so because an analyst who thinks otherwise stops looking.

**Band edges are DISCOVERED.** The measurement is a lookup; the rendering is a separate judgement. A threshold form is preferred wherever the target's value regions are contiguous in the source's own order, because it is a TOTAL function — an unobserved source value lands in the nearest band instead of off the end of an enumeration. `form` names the reading (`threshold`/`threshold_chain` total; `membership`/`membership_complement`/`enumeration_chain` fall to a default arm). The standard 9-10 / 7-8 / 0-6 NPS definition is this detector's OUTPUT on the motivating cohort, never its input.

**ONE candidate per SOURCE, carrying its `null_together` in the SAME rule.** A partition is three measurements against one field, and three rules would have to be kept consistent by hand. All three E2-S4 remedies ride the emitted rule: every numeric term goes through `round()`; there is NO separate normalisation rule (the rounding is inside each predicate, so nothing writes the source and the `!isnull` guard is unnecessary); and `null_together` names the SOURCE first inside the same rule, where it is the last write. Splitting it out lets the derivation run afterwards and un-null every member. No `when`, so the targets are **pre-claim-eligible** — accepting a candidate RETIRES their captured model, conditional pairs and residual correlations rather than computing and overwriting them.

**Admission is IDENTICAL NULL PATTERNS**, read from the co-missing detector's own accumulator rather than a second one. Differing patterns, or an unavailable accumulator, are REPORTED not guessed: a `set_expr` clears the null mask and would un-null the target wherever the source is absent. Also reported: an ALMOST-determined pair (1..`maxDepExceptions` = 8 contradicting rows, counted order-independently; over 8 it is dropped and not reported, being not "almost" anything), a CONSTANT column (determined by everything and by nothing), a mutually-determining pair, and a target contested by two sources (written once, by the strongest).

**Emitted LAST**, so the derivation re-resolves its block after every null-state rule. Measured with a hand-narrowed `set_null` over the source alone: last leaves 0 orphan rows, first leaves 940.

Measured on the motivating cohort: `nps -> {detractor <= 6, passive 7..8, promoter >= 9}` on 66,343 co-present rows, and `aware = round(familiarity) >= 2` on all 381,324 — **which answers the proxy question the gating detector could only report**. Nothing in a null-state measurement separates `aware` from `familiarity == 1`; values do, and one is derived from the other. 14 candidates in one file, +7.5% CPU over the two-detector figure, no additional byte read.

## See

- `synthetic-data` — modes, distributions, correlations, models, determinism contract.
- `tool-errors-lookup` — `PULSE_SYNTH_RULE_*` / `PULSE_SYNTH_CONSTRAINT_*` recovery; `pulse errors lookup CODE` is authoritative.
- `docs/src/cli/synth-from-schema.md` — CLI surface; the `rules` array is also the standalone rules-file format.
