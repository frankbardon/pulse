---
name: synth-rule-detectors
description: The `--suggest-rules` detector rules that are silent if got backwards — rounded gates, co-missingness, near and always-null blocks, the narrow dependency search and discovered band edges — and why emitted candidate order is applied order.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [profile create, suggest-rules, rules, detectors]
requires: [capability:synth]
---

# Synth rule detectors

## Detector rules that are silent if got backwards

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

- `synth-rules-from-profile` — the detector table, evidence and thresholds.
- `synth-rule-nulls` · `synth-rule-claims`.
