---
id: U56
slug: operation-panic-recovery
title: "A panic raised inside an operation becomes a coded error at the facade instead of crashing the host"
track: Embedder operations
size: S
status: not-started
depends_on: [U20]
soft_depends_on: [U34]
blocks: [U32]
todo_items: [231]
branch: operation-panic-recovery
---

# U56 — operation-panic-recovery

**Outcome:** A panic raised inside an operation becomes a coded error at the facade instead of crashing the host.

**Track:** Embedder operations · **Size:** S · **Depends on:** [U20](U20-observability.md) · **Soft:** [U34](U34-extension-validation.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

[U20](U20-observability.md)'s hooks are panic-safe (a panicking hook is recovered and counted in `pulse_hook_panics_total`), but a panic raised INSIDE an operation, most plausibly a faulty `extend` operator or expr function, still unwinds through the facade and crashes the host. This unit recovers it at the instrumented facade boundary (`observeOn`), turns it into a coded error, and ends the observed operation with that code so logs, hooks and metrics report it. Split out of [U34](U34-extension-validation.md), which it is not about.

## References

- `.claude/reference/observability.md` (load first)
- `docs/src/library/observability.md`, `docs/src/internals/extension-points.md`
- [api-and-embedder-operations 02 — Observability](../v1.0.0-embedder-operations/02-observability.md) (Panic recovery)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [ ] **#231** Recover a panic raised inside an operation (a faulty extension operator or expr function, an engine bug) into a coded error at the facade instead of crashing the host; only hook panics are recovered today

## Scope

**In scope**
- Recovery at the instrumented facade boundary (`observeOn`) for every observed operation
- A new `PROCESSING_*` or `SERVICE_*` code with `codeMetadata` (Message + at least one Fixup) and an `internal/descriptor/error_owners.go` owner
- The observed operation ends with that code, so logs, hooks and metrics report it
- The off path (no logger, hooks or metrics) stays allocation-identical

**Out of scope**
- Recovering panics in goroutines the engine owns, unless the first decision below says so

## Epics & stories

### E1 — A panicking operation returns a coded error
- S1: decide the recovery scope, add the code and the recovery at `observeOn`, with a test per operation kind using a panicking extension operator and a panicking expr function
- S2: docs, the observability reference and the error-code row

## Acceptance criteria

- [ ] A panicking extension operator and a panicking expr function each return a coded error from every observed facade method; the host does not crash
- [ ] The observed operation ends with the code (log, hook and metric agree)
- [ ] The off path is allocation-identical (benchmark or allocation test)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestCodesHaveFixups`, `TestManifestErrorCodesComplete`, `TestErrorOwners_Complete`
- `TestObservedMethodsCoverSurface`, `TestObservabilityDocCoversEnums`

## Update Demand companions

- `errors/fixup_metadata.go` and `internal/descriptor/error_owners.go`
- `.claude/reference/observability.md`, `docs/src/library/observability.md`, `docs/src/internals/extension-points.md`
- The error-code row in the CLAUDE.md Update Demand table

## Human inputs & decisions

- **Open:** whether the engine's own panics (not just extensions') are recovered or left to crash. Decide in E1-S1.
