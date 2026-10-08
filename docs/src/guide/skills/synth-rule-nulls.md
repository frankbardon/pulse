```yaml
name: synth-rule-nulls
description: Synth rule null handling — `null_together` as one null decision per question block (first field wins), and `owns_nulls` making a gate the field's only source of absence, with the divergence warnings each emits.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, rules, null_together, owns_nulls, set_null]
requires: [capability:synth]
```

# Synth rule null handling

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

## See

- [`synth-structural-rules`](synth-structural-rules.md) · [`synth-rule-validation`](synth-rule-validation.md) (non-nullable members) · [`synth-rules-from-profile`](synth-rules-from-profile.md) (detected blocks and gates).
