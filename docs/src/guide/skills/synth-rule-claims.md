```yaml
name: synth-rule-claims
description: Which synth rules pre-claim a field ahead of its linear model, conditional pairs and residual correlations, the four exclusions that are silent if got backwards, and why the fidelity report is the reason.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, rules, conflicts, models, pre-claim]
requires: [capability:synth]
```

# Synth rule claims

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

## See

- [`synth-conflicts`](synth-conflicts.md) — the full claim order; [`synth-models`](synth-models.md) · [`synth-model-recovery`](synth-model-recovery.md).
- [`synth-rule-expressions`](synth-rule-expressions.md) — the pre-rounding remedy that must not claim.
