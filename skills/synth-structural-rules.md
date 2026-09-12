---
name: synth-structural-rules
description: Synth spec `rules[]` and `constraints[]` — slot semantics, declaration order and last-write-wins, the `set_expr` coercion matrix, `null_together` blocks, eager validation codes, and the shared expr row environment. Split out of the synthetic-data topical.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, rules, constraints, set_null, set_expr, null_together, PULSE_SYNTH_RULE]
---

# Synth structural rules

Two synth-spec slots act ON a drawn row rather than describing its variation: `constraints[]` rejects a row and makes the generator redraw; `rules[]` masks or overwrites named fields on the rows a predicate selects. Both compile against the SAME `expr-lang` row environment (`rowExprEnv`) — a second hand-rolled env is what produced issue #258.

Modes, distributions, correlations, profile capture and the determinism contract: `synthetic-data`. Model machinery: `synth-models`. Every measurement behind a figure here, and the closed design questions: `docs/src/cli/synth-calibration.md`.

## Constraints

`constraints[]` reuse `expr-lang/expr`. Any constraint returning false rejects the row; the generator redraws until `row_count`. Reject cap 50% (`max_rejection_rate`); beyond it `PULSE_SYNTH_CONSTRAINT_INFEASIBLE`. Same-row reach only; no cross-row in v1.

**Env types are the ROW's types** (`sentinelFor`, derived from `fieldTypeFromName` so the two cannot drift): every scalar — `u4`/`u8`…`u64`, `f32`/`f64`, `date`, `decimal128` AND `packed_bool` — is `float64`; `categorical_*` is `string`; `set_*` is `map[string]bool` (`flags["opt"]`). So a boolean is tested `flag == 1`, never bare `flag` (a compile-time refusal, not a boolean). Typing `packed_bool` as Go `bool` was issue #258: compiled, then failed every row with `PROCESSING_RUNTIME: invalid operation: bool(float64)`.

**`isnull(field)`** reads the per-record null mask — the only way to test absence, since a nulled field still carries its drawn value. Takes the field NAME: `isnull(x)` (rewritten to a literal) and `isnull("x")` are equivalent, and an undeclared name in EITHER form is refused before a row exists — bare by expr's unknown-name check, string form by a parse pass (`isnullUnknownField`) applied to rules and `constraints[]` alike, never answered `false`. Only an argument that is a string at RUN time (`isnull(region + "!")`) reaches the loud run-time refusal.

## Structural rules

`rules[]` (additive, `omitempty`) state what no statistical summary can — and what therefore comes back from marginals as soft noise with a plausible rate and no gate: a question block not ASKED of a respondent who failed a screener, a flag that is a band of another column.

```json
"rules": [
  {"when": "aware == 0", "set_null": ["perception_1", "perception_2"], "set": {"segment": "unaware"}},
  {"set_expr": {"promoter": "nps >= 9"}},
  {"null_together": ["nps", "nps_reason"]}
]
```

Five slots plus one modifier: `when` optional predicate (**absent means EVERY row**, not "never"); `set` assigns LITERALS; `set_expr` expression RESULTS; `null_together` nulls a block as one decision; `owns_nulls` changes what a `set_null` MEANS rather than adding an action. `when` must return bool.

**`set` and `set_expr` are sibling keys, never one map with a `{"$expr": …}` marker** — one map leaves `{"set": {"region": "west"}}` undecidable between a literal and an identifier.

The pass consumes no RNG and allocates nothing per row, so a rules-free spec is byte-identical to pre-slot output. **The standalone rules-file format is this array itself** — an inline declaration and a `--rules` file are the same JSON.

### Order and placement

**Declaration order, sequentially, LAST WRITE WINS**, in ONE pass that is `drawRow`'s last act, after the model stage — and **the placement IS the semantics**: `if gate then null else inferred`. A gated field is still drawn through its own sampler, model and pairs; the rule masks or replaces it only where `when` selects, so non-gated rows keep the INFERRED value. NOT topologically sorted — declaration order is the one ordering an author can read off the document.

**Accepted consequence: a field a rule NULLS still contributed its DRAWN value to any model using it as a PREDICTOR on that row.** The two-phase alternative (evaluate gates, re-run dependent stages with gated fields withheld) is rejected; the single-phase reading is that the respondent's propensity existed, the question was not asked.

| Slot | Value | Null mask |
|---|---|---|
| `set_null` | LEFT as drawn (the shape `nullableSampler` produces; the wire gets the type's zero, never the masked value) | set |
| `set` / `set_expr` | written | CLEARED — a rule stating a field's value states the field HAS one |

A `set` literal is coerced to the row's Go shape once at compile time via `constantRowValue`, which turns a `set_*` target's `["tv","radio"]` into the row's `map[string]bool`.

**ACROSS rules an expression sees EARLIER writes, not LATER ones. WITHIN one rule order is UNOBSERVABLE:** every expression, `when` included, reads the row as it stood BEFORE the rule ran, and all its writes land after — so `{"set_expr": {"a": "b", "b": "a"}}` swaps, like SQL `UPDATE`. A sibling `set` literal is invisible to a sibling `set_expr`; self-reference (`{"nps": "nps + 1"}`) works and does NOT pre-claim the field. The alternative exposes an order nobody can SEE: a rule's slots are Go maps, JSON key order is gone after decode, and the surviving order is ALPHABETICAL, so renaming a column would silently change a number.

Rules reach GENERATED rows only — `synth from-profile --source` re-encodes the real partition straight through.

### `null_together`

**One null decision per block; the FIRST named field wins.** A question block is asked or skipped as a unit; independent per-field null draws make it a lottery (four fields sharing `null_rate` 0.826 come back all-present at `0.174⁴`, not `0.174`). The rule COPIES `nullMask[first]` onto the rest — every field has already taken its null draw, so a copy is the only resolution adding no randomness.

- **Cost: every other member's `null_rate` is IGNORED.** A member more than **0.02** from the gate's (`nullRateDivergenceThreshold`; ABSOLUTE, measured against the GATE not the block's spread, because what the copy discards is a number of ROWS) warns as an ATTENTION kind, never a refusal — a real block whose fields drifted slightly would otherwise be unusable.
- **The gate is never reported by either warning**: the copy READS it, so a non-nullable gate is not a field the block failed to null — and a never-null gate is the IDIOM, clearing the members' own MCAR nulls so a later `set_null` becomes the block's only absence.
- The copy moves the DECISION, never a value: a member the gate UN-nulls publishes what it drew, which is what makes the block share a RATE rather than only share its nulls.
- **Within one rule it is the LAST write**, after `set_null`/`set`/`set_expr`: `{"set_null": ["nps"], "null_together": ["nps", …]}` nulls the gate and the block follows, so a `set_null` over a NON-gate member of the same rule is overridden. Across rules, ordinary last-write-wins — and the case that bites is a later `set_expr` (it clears the mask on every row it writes, un-nulling every member). Put both slots in ONE rule rather than trusting declaration order.

### `owns_nulls`

**The rule is the field's ONLY source of absence.** `set_null` states WHICH rows are absent and nothing about HOW MANY, so each target's own `null_rate` keeps firing beneath the gate — and that rate is a MARGINAL the profiler captured, already inclusive of what the gate removed. The two compose as `g + (1−g)·r`: a gate exactly right about every gated row is still wrong about the marginal. `"owns_nulls": true` DISCARDS those fields' own null draw.

```json
{"when": "aware == 0", "set_null": ["regard", "charming", …], "owns_nulls": true}
```

- Scoped to `set_null` and nothing else — declaring it with an empty `set_null` is `PULSE_SYNTH_RULE_OWNERSHIP_INVALID`, because an ignored claim is invisible.
- **It zeroes the draw rather than applying the residual `(r − g)/(1 − g)`, because `g` — the rule's FIRING PROBABILITY — is a property of the data no spec knows.** The gap is therefore MEASURED: any owned field whose realised rate misses its discarded `null_rate` by more than **0.02** *and* two standard errors warns as an ATTENTION kind naming both rates, the row count and every claiming rule. The noise term is load-bearing on small runs.
- **Ownership is a UNION, not a `claim()`**: two gates may each own one field (the draw is suppressed once, either gate nulls it), since `set_null` removes a value rather than supplying one and never claims a field away from its own generation.
- Suppression is a WRAPPER discarding the verdict (`ruleOwnedNullSampler`), never a rebuild without the nullable wrapper — `nullableSampler` consumes one inner draw PLUS one `rng.Float64()` whatever the rate, so a rebuild shifts the whole seeded stream.
- **`null_together` already does this for its non-gate members, by COPY**, so the gated-block idiom is ownership as a side effect. `owns_nulls` says it directly; the block stays REQUIRED for a co-missing block with no gate at all, where one member's own draw is the block's only absence.

### `set_expr` and the coercion matrix

`set_expr` assigns an expression's RESULT, writing the value and clearing the mask as `set` does — collapsing three `when`-gated rules into one. A wrong cell is a SILENT wrong value:

| result | scalar target | `categorical_*` | `set_*` |
|---|---|---|---|
| bool | **1 / 0** | refused | refused |
| number (`float64`/`int`) | the number, RANGE-CHECKED | refused | refused |
| string | refused (see decimal) | must be a DECLARED value | refused |
| selection (`map[string]bool`, option list, `split()`) | refused | refused | **the selection** |

Scalar = `u4` `u8` `u16` `u32` `u64` `f32` `f64` `date` `packed_bool` `decimal128`. Range is the bound a `set` literal obeys (`u4` 0–15) — ONE shared matrix; NaN/Inf refused. Declared = `weighted_categorical`'s `values` / a `set_*`'s `options`; a `regex` or `constant` categorical is unbounded, so any string lands.

**One asymmetry: a `decimal128` takes an exact string only from a `set` LITERAL.** `set_expr` refuses one — expr already reduced the value to `float64`, so exactness is gone, and a string left in a `float64`-typed row slot breaks the NEXT rule reading it, on gated rows only.

**Two timings, one code.** A fault the RETURN TYPE settles is refused at SPEC PARSE; one only a VALUE settles (range, category) at ROW time. Both `PULSE_SYNTH_RULE_VALUE_INVALID`, naming rule, slot, field, value. The static side owns no table — it probes the same matrix with the type's zero and refuses only on a TYPE fault, so the two timings cannot disagree.

### Two silent gotchas

**Nulling a non-nullable field.** A field not declared `"nullable": true` gets the type's zero written as an ordinary value with NO null bit — the rule fires and the file cannot show it. **`set_null` over such a field is REFUSED** (`PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE`); the fix is on the FIELD, and on a profile-derived run a field is nullable only if the source had nulls (`--emit-spec`, add the flag, `from-schema`). A `null_together` MEMBER only warns, because that slot copies the gate's decision — which may be "present" on every row — rather than claiming a null. Do NOT "finish the job" by promoting it: the gated-block idiom names a never-null field first on purpose.

**Pre-rounding.** `when` / `set_expr` read the row's PRE-ROUNDING float, not the wire value, so `== k` fires only on draws landing exactly on `k`. Use comparisons (`>= 9`). It bites `set_expr` hardest because the result still LOOKS right — a three-band NPS classification off raw `nps` sets exactly one flag per row and merely disagrees with the score beside it. Normalise in an EARLIER rule (snapshot semantics means the same rule will not do), with `round`, NOT `int`: an integer field is stored as `floor(v+0.5)`, so `round(v)` reproduces the value the FILE will hold and moves the FLAGS onto the right side of the edge, while `int(v)` truncates and reaches the same band agreement by moving the SCORE down instead. **Guard with `when` whenever the target is nullable** — `set_expr` clears the null flag:

```json
{"when": "!isnull(nps)", "set_expr": {"nps": "round(nps)"}}
```

**LIVE only where row value and stored value differ** — `f32`/`f64`, an integer column over 64 observed levels (a clamped normal), a hand-authored continuous distribution on an integer field. A small integer reconstructs as `discrete` and a `packed_bool` as `bernoulli`, so for those the row value already IS the stored integer and the normalisation is a no-op.

### Eager validation

**At spec parse, never at row time.** A rule naming a mistyped field, or carrying an uncompilable expression, is invisible at run time: the run succeeds and the gate is simply absent. Eight codes, each naming the rule INDEX (`rule_index` — a rule has no name) plus slot and field where one exists:

| Code | Fires on |
|---|---|
| `PULSE_SYNTH_RULE_EMPTY` | no action slot (`_evidence` alone is still empty) |
| `_FIELD_UNKNOWN` | a slot's name, AND an undeclared name inside `isnull("…")` — that expression COMPILES, so it is an unknown FIELD, not an `_EXPR_INVALID` |
| `_FIELD_NOT_NULLABLE` | `set_null` only |
| `_EXPR_INVALID` | uncompilable, or a `when` not returning bool |
| `_VALUE_INVALID` | the coercion matrix, either timing |
| `_CONFLICT` | one rule naming a field in two slots that DISAGREE (`set_null` + `null_together` do not disagree and are ordered instead) |
| `_BLOCK_INVALID` | `null_together` naming fewer than two DISTINCT fields |
| `_OWNERSHIP_INVALID` | `owns_nulls` with an empty `set_null` |

Faults report in a fixed slot order with SORTED map keys, so a rule with two faults refuses the same one every run. A `when` failing at RUN time stays `PROCESSING_RUNTIME` — the remaining causes are generic expr faults (a dynamic `isnull` argument, `int()` of a non-numeric string), not "a malformed rule", and the coded error already carries rule index, slot and expression. `pulse errors lookup CODE` is authoritative.

## What a rule retires

A rule that DETERMINES a field **pre-claims it at priority 0** in `resolveConflicts`, ahead of the linear-model pre-claim — the rule pass runs last and an unconditional write wins outright, so nothing upstream should produce a value it discards. Three things stop for that field: its `models` entry, any conditional pair naming it, its place in `residual_correlations`. Each loss is warned in the ordinary wording (`conditional relationship conflict: field "promoter" is already claimed by structural rule 0 (set_expr); dropping linear model`).

A whole-field claim SUBSUMES that field's `set_*` OPTIONS, so an unconditional rule writing a whole multi-select retires the per-option set-set / set-categorical pairs driving it. One-directional: an option-level claim does NOT reach the whole field, since a pair driving one option has said nothing about the others.

**The report is the reason, not the wasted work.** `FidelityReport.Models` asks generation's own compiler which models ran, so without the claim a rule-overwritten modelled field ships a captured-versus-recovered coefficient delta for a value nothing kept — every number rendering, nothing saying it describes something that did not happen.

**Four exclusions, each SILENT if got backwards:**

| Rule shape | Claims? | Why |
|---|---|---|
| `set` / `set_expr`, no `when` | **yes** | writes every row from inputs the field's own generation does not supply |
| carries a `when` | no | writes only some rows; claiming strips the model from the rest, leaving a plausible bare marginal |
| `set_null` (any conditionality), `owns_nulls` or not | **no** | removes a value rather than supplying one — `if gate then null else inferred` needs the model to produce what non-gated rows keep. Ownership suppresses the NULL DRAW and claims nothing; the value is still the model's |
| `null_together` | no | copies one null decision; supplies no value |
| `set_expr` reading its OWN target | no | transforms what generation produced rather than determining it |

The last is why the pre-rounding remedy `{"set_expr": {"nps": "round(nps)"}}` is safe: claiming there would leave the rounding applied to a bare marginal draw — the documented fix for one silent fault causing a larger one. Self-reference is detected on the PARSED expression, never by substring (`nps_reason` is not `nps`), and an unparseable expression answers "reads", the direction that cannot lose structure.

**One contract narrows.** The pass still consumes no RNG, but a CLAIMING rule retires a model and a retired model stops drawing its own per-row normal, so the stream shifts — for a reason a warning names. No rules, or rules claiming nothing ⇒ byte-identical.

## A rule that never fires

Eager validation cannot catch a `when` that is simply NEVER TRUE: the rule validates, compiles, applies to nothing, and the cohort generates cleanly with the structural fact still missing — the same silent-inertness class as a scored pair generation never applied. So firings are COUNTED during generation and a rule applying to ZERO rows warns, naming its index, its `when` verbatim and the row count the zero is out of. Kind ATTENTION (`rule never fired`), so it leads the stderr summary.

**The count is over rows that reached the FILE.** A constraint-rejected row was re-drawn and left no trace, so it is not a firing — which keeps the figure divisible by the row count and makes a rule firing only on rejected rows report the truth (zero). Firing on every row, and on some, are both silent. Many dead rules list at most 20 plus a counted `+N further rule(s) never fired`. Counting consumes no RNG and allocates nothing per row.

**The message names the cause that applies to THAT rule**, in three arms:

1. **CONSTRAINT** — the rule DID select rows and a constraint rejected every one. Recorded during the run (attempt remembered, acceptance counted) rather than inferred, so the line blames the constraint and says nothing about rounding.
2. **PRE-ROUNDING** — the predicate reads a field that rounds on write AND draws continuous values; remedy `round(field)` in an earlier rule, never `int()`.
3. **NEUTRAL** — neither. Names each field the predicate reads with its type and distribution, and RULES PRE-ROUNDING OUT for a field whose row value already IS the stored value.

Arm 3 is why the split exists: a field that rounds on write still draws exact values under `discrete`, `bernoulli`, `uniform_date`, `poisson` or `monotonic_from`, and a small integer reconstructs as `discrete` automatically — so on a survey cohort the pre-rounding advice is wrong for most columns, and sending an author to normalise an already-exact gate is worse than saying nothing.

## Reaching rules from a profile

`SpecFromProfile` derives a spec, generates from it in one breath, and emits no rules, so `pulse synth from-profile` carries two flags that make the layer reachable.

- `--rules <path>` loads a standalone rules document (the bare `rules` array — cut and paste between spec and file) and **REPLACES** `Spec.Rules`. No append mode: a derived spec has nothing to append to, and one would only create an ordering question. `{"rules": […]}` refused rather than read as zero rules; `[]` accepted, meaning "no rules". Validation runs at the LOAD against a COPY (a profile-derived spec bypasses `validateSpec` by design), so a refused file leaves `Spec.Rules` untouched.
- `--emit-spec <path>` writes the derived spec AFTER the merge as indented JSON. The spec that GENERATED, not a rendering: fed to `synth from-schema` at the same seed it reproduces the rows byte for byte. Authoring aid (field names, types, floors a `when` must be written against) and diagnostic (which models survived translation, which distribution each field reconstructed to, which pairs were retired). Written BEFORE generation, so a failing run still leaves it.

Generation-time warnings — POST-merge arbitration, rule-compilation, never-fired reports — also land in `--fidelity-report`'s `warnings` array, deduped against the translation channel.

Refusals name the FILE as well as the rule index (`details.path` on every `PULSE_SYNTH_RULE_*` raised by the load), so an analyst holding a rules file, a spec and a profile knows which document is wrong. Missing file `DATA_FILE`, malformed one `SERVICE_VALIDATION`; both name the path.

## Detecting rules from the data (`--suggest-rules`)

Coverage bounds the layer's value and rules get written from RECALL. `pulse profile create --suggest-rules <path>` runs three detectors on the SAME scan (no extra byte read) and writes the bare rules array `--rules` consumes UNMODIFIED. Per-detector treatment: `docs/src/cli/profile-create.md`.

**Proposed, never applied.** A detected pattern can be a coincidence of the sample, and detection finds the STATISTICAL gate while a human knows the SEMANTIC one — on a real cohort two different predicates gated the identical rows with identical evidence to sixteen digits, so nothing in the data could separate them. Read each candidate's evidence, correct the `when`, delete the rest.

**Evidence rides the rule on `_evidence`** — `RuleSpec`'s ONE inert slot. Generation never reads it, an evidence-only rule is still `PULSE_SYNTH_RULE_EMPTY`, hand-authoring it is harmless. There rather than in a sibling block because the file must stay a bare ARRAY, and because evidence in a second document drifts the first time a candidate is deleted. Handles inside it are CONTENT-derived, never an index — the file is edited by deletion and every index below a deleted line shifts.

**Thresholds are package CONSTANTS, never `ProfileOptions` fields** — a capture flag that moved one would make two documents over the same cohort disagree about what the field IS. TIGHT because skip logic is EXACT in the source; a looser pair finds ordinary ASSOCIATION and proposes it as structure.

**Over-cap input is ABANDONED, never truncated** — a partial level map, field set or histogram makes every share below the cut a share of an arbitrary subset, the defect the detectors exist to remove. Caps: `maxGateLevels` (16 observed levels on a gate candidate), `maxBlockFields` (256 nullable fields), `maxDepFields` (256 dependency participants). Block accumulation is a per-field bitset over `blockChunkRows` folded by popcount, so memory is flat in the row count. Each abandonment is COUNTED in `Profile.Warnings`.

| Detector | Proposes | Admission rule (the non-guessable part) |
|---|---|---|
| gating (`detector: "gating"`) | `set_null` + `"owns_nulls": true` | a low-cardinality field (`categorical_*`, `packed_bool`, `u4`) whose levels split a target's null rate into ≥`gateHighNullRate` (0.98) and ≤`gateLowNullRate` (0.02). `P(null \| open)` IS the residual `owns_nulls` zeroes, so the flag is within 0.02 of exact BY CONSTRUCTION — without it the gate repairs CO-MISSINGNESS ONLY |
| co-missing (`detector: "co_missing"`, evidence on `_evidence.block`) | `null_together` | **IDENTICAL NULL PATTERN**, never an identical `null_rate` — two unrelated fields can share a rate to sixteen digits and overlap by chance, and the rule would then make the claim TRUE in output. Every emitted member carries `agreement: 1` and `max_null_rate_deviation: 0`, so a block can never trip the divergence warning and member order is arbitrary rather than a ranking |
| dependency (`detector: "dependency"`, evidence on `_evidence.dependency`) | `set_expr` (+ its own `null_together`, same rule) | a field that IS a function of ONE other on every co-present row, AND identical null patterns — read from the co-missing accumulator rather than a second one, because a `set_expr` clears the null mask and would un-null the target wherever the source is absent |

Detector rules that are silent if got backwards:

- **Numeric gates emit through `round()`, uniformly, including `packed_bool`** — a bare `==` reads the pre-rounding float and under-fires silently, and a file mixing bare and rounded gates teaches a reader that bare is sometimes fine with nothing saying which. Every emitted `when` is COMPILED against the rule layer's own environment before it is written.
- `gated_share` (`1 − P(gate)`) is a CHECKING aid, not the ranking signal — the split test arithmetically implies it. Ranking is target count, then rows affected.
- A gate whose ONLY gated level is the null pseudo-level is CO-MISSINGNESS, not value gating: counted, not proposed, or it would restate one finding once per block member.
- A **NEAR block is REPORTED with its agreement and disagreeing row count, never emitted** — `null_together` has no dial for "almost" and, unlike a gating candidate's `when`, nothing in it can be corrected. Measure is the null-SET overlap (`nearBlockAgreement` 0.95), never row-level agreement: two independent fields each null at 1% agree on 98% of ROWS.
- An **ALWAYS-NULL column is its own finding and gets NO rule** — it reconstructs as a typed `constant` with `null_rate` 1.0, so generation already reproduces it. Check the source or the slice profiled.
- **The dependency search is NARROW and the bounds ride the file**: targets `packed_bool`/`u4` only; sources `categorical_*`/`packed_bool`/`u4` with at most `maxDepLevels` (16) levels; exactly ONE source. **A field missing from the file was not cleared, it was not examined.**
- **Band edges are DISCOVERED, never encoded.** A THRESHOLD form is preferred wherever the target's value regions are contiguous in the source's own order, because it is a TOTAL function — an unobserved source value lands in the nearest band rather than off the end of an enumeration. `form` names the reading: `threshold` / `threshold_chain` total, `membership` / `membership_complement` / `enumeration_chain` not.
- **ONE dependency candidate per SOURCE**, carrying its `null_together` in the SAME rule (split out, the derivation runs afterwards and un-nulls every member) and every numeric term inside `round()` — so nothing writes the source and no `!isnull` guard is needed. No `when`, so its targets are **pre-claim-eligible**: accepting one RETIRES their captured model, pairs and residual correlations.
- Reported rather than proposed: an ALMOST-determined pair (1..`maxDepExceptions` = 8 contradicting rows, counted order-independently; over 8 dropped AND unreported, being not "almost" anything), a CONSTANT column, a mutually-determining pair, and a target contested by two sources (written once, by the strongest).
- Caps are PER DETECTOR, so twenty gating candidates cannot hide that the cohort has question blocks at all.

### Emitted order is applied order

**A candidate WRITING a field another candidate READS is emitted BEFORE it** (`ruleCandidateOrder`). The detector preference — gating, blocks, dependencies — is a READABILITY choice kept wherever nothing forces a move, and a candidate is HOISTED only just before the one it must precede.

Gate-before-block is arithmetic rather than luck for the fields a gate WRITES (identical patterns classify identically at every level of every gate, so a gate takes a WHOLE block or none) and is chosen for the file's actual purpose — being EDITED, where a hand-narrowed `set_null` over PART of a block is repaired by a block that follows it. **The equivalence is FALSE for what a gate READS**: a block member that is a gate's `when` FIELD moves the gate's own INPUT after the gate read it, so the gate fires on the drawn value, the block then nulls the field it was reading, and the target it left present becomes an answer on a row whose screener the same file says was never asked. The trade is deliberately asymmetric — the repair property protects an edit that may never be made; the ordering fault corrupts rows unconditionally.

Reordering by hand: keep writers before readers. A mutual pair (each writing a field the other reads) has no satisfying order — the closing edge is dropped, the survivor decides the pair (possibly inverting the detector preference, because an edge is a constraint and the order a preference), and the drop is WARNED about rather than resolved silently.

## See

- `synthetic-data` — modes, distributions, marginals, correlations, determinism contract.
- `synth-models` — `--fit-models`, and what a claiming rule retires from it.
- `docs/src/cli/synth-calibration.md` — the measurements behind every rule here, and the closed design questions.
- `tool-errors-lookup` — `PULSE_SYNTH_RULE_*` / `PULSE_SYNTH_CONSTRAINT_*` recovery; `pulse errors lookup CODE` is authoritative.
- `docs/src/cli/synth-from-schema.md` — CLI surface; the `rules` array is also the standalone rules-file format.
