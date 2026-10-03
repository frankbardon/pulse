---
name: session-synth-flags
description: The profile-capture and synth-generation CLI flags an agent must know — the five additive profile create sections, emitting the generating spec, supplying and suggesting structural rules — and why a model coefficient is a latent-scale quantity.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [CLI flags, profile create, synth from-profile, --fit-models, --emit-spec, --rules, --suggest-rules]
requires: [capability:synth]
---

# Synth CLI flags

Part of `session-bootstrap`. The synth surface: `synthetic-data`.

## Profile-capture CLI flags

Five independent, additive `pulse profile create` knobs; each adds an `omitempty` section, none implies another, all omitted reproduces the pre-flag document byte-for-byte. The leaf profiles a single file or a whole shard archive. Detail: `synthetic-data`, `docs/src/cli/profile-create.md`.

| Flag | Adds |
|---|---|
| `--conditional` | pick-one conditional pair sections (numeric-numeric, categorical-categorical, categorical-numeric, three `set_*` arms) |
| `--fit-shape` | 2-component Gaussian mixture per numeric on a BIC win; generates as `mixture`, not `normal` |
| `--fit-models` | one linear model per numeric, regressed on admitted categorical levels + set options. **How several drivers condition ONE numeric at once.** RETIRES the numeric-target conditional pairs for the targets it lands on — per target, never per document; the three non-numeric arms are untouched, so `--conditional` + `--fit-models` keeps both halves |
| `--residual-correlations` (needs `--fit-models`) | full correlation submatrix among fitted residuals, so a numeric can be both conditioned and correlated with a sibling |
| `--run-continuation` | `run_continuation`: per-field fraction of adjacent row pairs whose bytes + null bit repeat — exactly the run-skip decode's hit rate — plus `overall`, `high_fields` (≥0.75) and `advice`. Low overall (<0.5) ⇒ sort the source by its parent key upstream. Pairs never span a shard. Not read by synth |

## Synth-generation CLI flags

Two additive `pulse synth from-profile` knobs (both absent reproduces pre-flag output byte-for-byte) plus one on `pulse profile create`. Detail: `synth-structural-rules`, `docs/src/cli/synth-from-profile.md`, `docs/src/cli/profile-create.md`.

| Flag | Must know |
|---|---|
| `--emit-spec <path>` | the spec that actually generated (AFTER any `--rules` merge), indented JSON, not a rendering — fed to `synth from-schema` at the same seed it reproduces the same rows. **The only way to see which captured models survived translation, which distribution each field reconstructed to, and which conditional pairs were retired**, and how you learn the field names, types and floors a rule must be written against. Written before generation, so a failing run still leaves it |
| `--rules <path>` | the only way to reach the `rules[]` layer from the profile path. A **bare JSON array of rule objects — the `rules` key's own value**, so a rule moves between spec and file by cut and paste; `{"rules": […]}` is refused, not read as zero rules. REPLACES `Spec.Rules` (a derived spec carries none). Eager validation naming the FILE — e.g. `PULSE_SYNTH_RULE_FIELD_UNKNOWN` with `details.path`, never a bare parse error |
| `--suggest-rules <path>` (on `pulse profile create`) | DETECTS rules on the same scan (no extra cohort read) and writes the bare array `--rules` consumes unmodified: GATING (`set_null`), CO-MISSING blocks (`null_together`, admitted on identical null PATTERN — an identical null RATE is never enough) and EXACT DEPENDENCIES (`set_expr`; `packed_bool`/`u4` targets, `categorical_*`/`packed_bool`/`u4` source of ≤16 levels, band edges DISCOVERED from the data). Near misses, almost-determined pairs, always-null and constant columns land in `warnings`, never proposed. Gating first, dependency LAST — declaration order is applied order. **PROPOSED, never applied**: detection finds the STATISTICAL gate, a human knows the SEMANTIC one. Measurements ride `_evidence`, a rule's one INERT slot (an evidence-only rule is still `PULSE_SYNTH_RULE_EMPTY`). The profile document gains no section. Numeric gates emit through `round()` — a bare `==` reads the pre-rounding float and under-fires |

**A model coefficient is a LATENT-scale quantity**, not data units: it shifts the standard-normal `μ` in `value = Q(Φ(μ + σ·z))`, so it is non-linear in value space for every non-normal `Q`. Never report one as "this many points on the scale". Detail: `synth-models`.
