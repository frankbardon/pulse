---
id: U02
slug: public-surface
title: "The public Go API is deliberate, and CI guards it"
track: API & release
size: L
status: not-started
depends_on: []
soft_depends_on: []
blocks: [U04, U07, U15, U20, U32]
todo_items: [1, 2, 3, 4]
branch: public-surface
---

# U02 — public-surface

**Outcome:** The public Go API is deliberate, and CI guards it.

**Track:** API & release · **Size:** L · **Depends on:** none · **Unblocks:** [U04](U04-profiles-model.md), [U07](U07-guidance-metadata.md), [U15](U15-linalg-core.md), [U20](U20-observability.md), [U32](U32-docs-audit.md)

## Summary

Decide which packages remain importable at v1.0.0 and move the rest under `internal/`, re-exporting through the root facade where public signatures need it. Then add an API-compatibility check so the frozen surface can't break silently.

## References

**Theme documents (read before starting):**
- [api-and-release 00 — Public Go surface](../v1.0.0-api-and-release/00-public-surface.md) — Process, Catalog template, Starting hypothesis

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#1** (1. API surface & release pipeline › Public Go surface) Downstream usage catalog completed (maintainer)
- [ ] **#2** (1. API surface & release pipeline › Public Go surface) Classification per package decided (public / public-narrowed / internal)
- [ ] **#3** (1. API surface & release pipeline › Public Go surface) Package moves and narrowing done; facade re-exports added
- [ ] **#4** (1. API surface & release pipeline › Public Go surface) API-compatibility check (`gorelease` / `apidiff`) in CI against the latest tag

## Scope

**In scope**
- Maintainer's downstream catalog (input)
- Per-package classification: public / public-narrowed / internal
- Package moves and narrowing; facade re-exports
- API-compat check in CI against the latest tag

**Out of scope**
- Writing `STABILITY.md` (that is U32, once this list is final)
- Behaviour changes of any kind: this unit is moves and re-exports only

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(public-surface/E<n>-S<m>): …`; close each epic with `milestone(public-surface/E<n>): vertical slice complete — <epic title>`.

### E1 — The surface is decided
- S1: maintainer completes the catalog (template in rel0)
- S2: classification table committed to `api-and-release/00` (replace the hypothesis column with decisions)

### E2 — Internals move out of the public API
- S1: move `processing` / `service` (and others classed internal) under `internal/`, fixing imports mechanically
- S2: narrow `encoding` / `io` / `descriptor` / `synth` / `template` / `mcp` to their public subset; facade re-exports
- S3: update CLAUDE.md architecture block, `docs/src/internals/packages.md`, agent definitions and every recipe that names a moved path

### E3 — CI guards the surface
- S1: `gorelease` (or `apidiff`) job comparing against the latest tag; documented override for intentional pre-1.0 breaks

## Acceptance criteria

- [ ] The downstream library builds against the branch with only import-path updates for symbols classed public
- [ ] No package outside the agreed public list is importable
- [ ] All existing tests and goldens pass unchanged: no behaviour change
- [ ] The API-compat CI job runs on PRs and fails on an incompatible change to a public package
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Existing import-boundary gates (`TestPredictNoExecutionImports`, `TestMCPCore_NoSDKImport`, `TestTemplatePackage_ImportBoundary`) updated to the new paths and still green
- New CI job: API compatibility

## Update Demand companions

- CLAUDE.md "Architecture" block and every path reference in `.claude/reference/*.md`
- `docs/src/internals/*` recipes
- `.claude/agents/*.md` path mentions

## Human inputs & decisions

- **Blocking input:** the downstream catalog (decided: the maintainer produces it)

## Notes

- Land this before other units touch the moved packages, to avoid rebasing every in-flight unit across a mass move.
- Pre-1.0, the compat check can be advisory until the v1.0.0 tag exists.
