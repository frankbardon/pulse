```yaml
name: synth-rules-from-profile
description: Reaching synth rules from the profile path — `--rules` (replace semantics, load-time validation) and `--emit-spec` (the spec that generated) — and `profile create --suggest-rules`: proposed candidates, inert evidence, fixed thresholds and the three detectors.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, profile create, rules, suggest-rules, emit-spec]
requires: [capability:synth]
```

# Synth rules from a profile

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

Detector-specific rules and the emitted order: [`synth-rule-detectors`](synth-rule-detectors.md).

## See

- [`synth-structural-rules`](synth-structural-rules.md) · [`synth-rule-detectors`](synth-rule-detectors.md) · [`synth-rule-nulls`](synth-rule-nulls.md).
- `docs/src/cli/profile-create.md` / `docs/src/cli/synth-from-profile.md` — flags and worked files.
