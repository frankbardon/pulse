---
id: U34
slug: extension-validation
title: "Extension registrations are validated as strictly as built-ins, and chain predict knows them"
track: API & release
size: S
status: not-started
depends_on: [U02b]
soft_depends_on: []
blocks: [U32]
todo_items: [194, 195, 196, 197, 216]
branch: extension-validation
---

# U34 — extension-validation

**Outcome:** Extension registrations are validated as strictly as built-ins, and chain predict knows them.

**Track:** API & release · **Size:** S · **Depends on:** [U02b](U02b-extension-contract.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

[U02b](U02b-extension-contract.md) landed the `extend` contract with four deliberate gaps, each recorded in its Landed deviations. None changes a result today, but each lets an embedder mistake surface late or not at all. This unit closes them: chain predict gets a production entry point that sees extensions, an operator's own `Components()` keys are checked against its `ComponentSchema` at `pulse.New`, `FeatureRegistration.Streamable` is probe-validated like the other categories, and the synth-distribution registration gets a real `extend` factory shape or is explicitly retired as an extension category.

Numbering note: the unit is U34 because U33 (v1-release) was already allocated; it is appended rather than renumbered, and [U32](U32-docs-audit.md) lists it as a dependency so the docs audit covers the final extension surface.

## References

**Theme documents (read before starting):**
- [U02b extension-contract](U02b-extension-contract.md) — Landed deviations (Limits, `Components()` self-emission, streamability)
- [api-and-release 00 — Public Go surface](../v1.0.0-api-and-release/00-public-surface.md) — Deliverables, landed deviations
- `docs/src/internals/extension-points.md` — Limits of extension operators
- `.claude/reference/synthetic-data.md` — load before touching any `synth/` surface (item #197)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#194** (1. API surface & release pipeline › Public Go surface › Extension hardening) Chain predict has a production caller that passes the `ExtensionsSnapshot`: `internal/descriptor.ValidateChain` / `ValidateChainWithExtensions` (which admit Mergeable extension aggregators and groupers and `row_local` extension attributes) are reachable from a facade, CLI or MCP chain-predict entry point, not only from tests
- [ ] **#195** (1. API surface & release pipeline › Public Go surface › Extension hardening) An operator's own `Components()` keys are probe-validated against its `ComponentSchema` at `pulse.New` when no `ComponentsFunc` is registered (the `ComponentsFunc` path already fails `PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH`); the self-emission behaviour itself is kept
- [ ] **#196** (1. API surface & release pipeline › Public Go surface › Extension hardening) `FeatureRegistration.Streamable` is probe-validated at `pulse.New` against whether the factory's value implements `extend.StreamingFeatureComputer`, matching the other categories' `PULSE_EXTENSION_STREAMABLE_MISMATCH`
- [ ] **#216** (1. API surface & release pipeline › Public Go surface › Extension hardening) Extension aggregators declare whether their output is a count, so `return.precision` can write it exact
- [ ] **#197** (1. API surface & release pipeline › Public Go surface › Extension hardening) Synth-distribution extension contract: an `extend` factory shape for authoring a distribution (today the registration only reserves the namespace), or a documented decision that distributions are not an extension category

## Scope

**In scope**
- A chain-predict entry point (facade method; CLI and MCP reach it through the facade) that threads the `ExtensionsSnapshot` into `ValidateChainWithExtensions`; a parity test against runtime `ProcessChain` admission for extension operators
- Probe validation of self-emitted `Components()` keys and of feature `Streamable`, with `codeMetadata` for any new code
- Synth-distribution extension shape (#197): the design decision first, then either the factory shape with probe validation and a profile/spec round trip, or retirement of the slot

**Out of scope**
- New extension categories other than the synth-distribution decision
- Removing `Components()` self-emission (kept deliberately)
- Per-group aggregator Components (owned by [U17](U17-response-shaping-core.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test|docs(extension-validation/E<n>-S<m>): …`; close each epic with `milestone(extension-validation/E<n>): vertical slice complete — <epic title>`.

### E1 — Registrations are validated as strictly as built-ins
- S1: probe-validate self-emitted `Components()` keys; probe-validate feature `Streamable`
- S2: chain-predict entry point passing the extension snapshot, with a runtime parity test

### E2 — Synth distributions are authorable
- S1: decide the contract (load `synthetic-data.md` first); implement the `extend` factory shape or retire the slot
- S2: skills, `extension-points.md`, manifest `extensions` block

## Acceptance criteria

- [ ] No `ValidateChain*` function is reachable only from tests
- [ ] Chain predict and runtime `ProcessChain` agree on every extension operator admitted or refused
- [ ] A `Components()` key outside the registration's `ComponentSchema` fails at `pulse.New`
- [ ] A feature registration whose `Streamable` disagrees with its factory's value fails at `pulse.New`
- [ ] The synth-distribution slot either authors a distribution end to end or is documented as not an extension category
- [ ] `format_version` stays `"1.1"`
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestExtensions_*` suite extended to the new probes
- Chain-predict vs runtime parity test
- `TestSkillsCoverAllSynthDistributions` if #197 adds a distribution surface

## Update Demand companions

- `docs/src/internals/extension-points.md`; the `adding-*` recipes affected
- CLAUDE.md "Extension Points" (probe-validation bullet)
- `errors/fixup_metadata.go` for any new code; `skills/synthetic-data.md`, `skills/synth-models.md` and `.claude/reference/synthetic-data.md` if #197 changes `synth/`
- [U02b](U02b-extension-contract.md) Landed deviations: strike the items this unit closes

## Human inputs & decisions

- **Open:** whether synth distributions become an extension category (#197) or the slot is retired. Decide in E2-S1.
- **Open:** whether a feature profile can hide `LookupTables` (inherited from U05, below).

## Inherited from U05

- **`LookupTables` have no gating capability** (decision, not a TODO item). They always resolve, even when `capability:labels` and `capability:range_tables` are hidden. Decide whether a profile can hide them (`.claude/reference/feature-profiles.md`, load first). They are expression-function data registered through `Options.Extensions`, not a named-table capability today.

## Inherited from U07

- **Extension test `Interpretation` is checked for structure only.** `pulse.New` validates it with the built-in validators, but nothing probes the declared output keys, while built-ins get two-way runtime probes (`TestInterpretationFieldsHoldAtRuntime`). Decide in E1-S1 whether to probe them the way #195 probes `Components()` keys. If not, record that the gap is deliberate in `docs/src/internals/extension-points.md` (Purpose and Interpretation).

## Inherited from U14

- **Extension zone capability.** Extension operators are never zone-capable (`internal/descriptor/capabilities_zone.go`; an explicit `tz` on one is `PROCESSING_CONFIG`). Decide whether registrations gain a zone declaration (`zone: capable|following`) and which `internal/temporal` helpers (`LoadZone`, `LocalDay`, `LocalParts`, `LocalMidnightUTC`, `Zone.Fork`) become a public, frozen surface in `extend` — `Zone` is still internal.
- `convert --tz`, SPSS local wall-clock export and `--json` local-offset labels are not extension work; they are tracked in [time-zones 00](../v1.0.0-time-zones/00-design.md) (Follow-ups after U14).

## Inherited from U17

- **Extension count semantics** (#216). `return.precision` writes a built-in count aggregation's `data` column and count crosstab cells exact (`countSemanticAggregations`, `internal/descriptor/return_precision.go`), but an extension aggregator is never count-semantic: its figures round like measures. Add an optional declaration on the aggregator registration (and its `extend` factory), probe-validated at `pulse.New` like the other declared flags, and have `countSemanticAggregations` read it. Companions: `docs/src/internals/extension-points.md`, `.claude/reference/update-demand.md` (`Request.Return` row).
