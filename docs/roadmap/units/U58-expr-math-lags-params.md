---
id: U58
slug: expr-math-lags-params
title: "Query and synth expressions share a deterministic maths set, synth rows read earlier rows, and a request hands expressions named parameters"
track: API & release
size: L
status: done
depends_on: [U53]
soft_depends_on: [U40, U52, U57]
blocks: [U32]
todo_items: [509, 510, 511, 512, 513, 514, 515, 516, 517, 518, 519, 520]
branch: expr-math-lags-params
---

# U58 — expr-math-lags-params

**Outcome:** Query and synth expressions share a deterministic maths set, synth rows read earlier rows, and a request hands expressions named parameters.

**Track:** API & release · **Size:** L · **Depends on:** [U53](U53-custom-regressions.md) · **Soft:** [U40](U40-compose-sweep.md), [U52](U52-synth-realism.md), [U57](U57-transformed-predictors.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

A media-mix synthetic dataset needs a recursive adstock, a seasonal control and a saturation curve inside a synth rule, and a query needs the same maths with bits that do not depend on the machine. This unit adds `internal/detmath` (barriered pure-Go `Exp`, `Log`, `Log1p`, `Expm1`, `Erf`, `Erfinv`, `Sin`, `Cos`, `Tan`, `Atan2`, `Pow` and the `rand/v2` ziggurat), runs query expressions, `FEAT_LOG`, the `**` / `^` operator and all of synth generation on it, adds trigonometry and `pi()` / `e()`, gives synth rules `row()`, `lag()` and a gap-free `monotonic_from`, and adds `Request.Params` (`params.<name>`) so a template fills an expression by value instead of splicing text. `{{}}` and sweep / for_each axis tokens inside an expression are refused.

## References

- `.claude/reference/synthetic-data.md`, `execution-modes.md`, `request-templating.md`
- `internal/skills/request-params.md`, `synth-cross-row.md`, `attribute-composition.md`
- [Embedder migration: Changes from U58](../v1.0.0-api-and-release/03-embedder-migration.md)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#509** `internal/detmath` and query expressions / `FEAT_LOG` on it
- [x] **#510** `sin cos tan atan2 pi() e()`; reserved names; refused in `OVERLAY_FORMULA`
- [x] **#511** the `**` / `^` operator on `detmath.Pow` (in-effort)
- [x] **#512** synth row environment: maths set, `%`, `**`, `now()` refused, collision rules
- [x] **#513** synth `row()` and `monotonic_from` counting accepted rows
- [x] **#514** synth `lag(name, n[, default])`, the `synth-cross-row` skill and the MMM adstock example
- [x] **#515** synth generation, copula and ziggurat on `detmath`; byte-identical per `(spec, seed)` (in-effort)
- [x] **#516** `Request.Params`, its codes and `capability:params`
- [x] **#517** Compose / ProcessChain `params` inheritance; Compose formula overlays read params
- [x] **#518** exact `u64` comparisons across the full range (in-effort)
- [x] **#519** `PULSE_TEMPLATE_EXPR_SPLICE`, `internal/exprslots`, `fit_on` as an expression slot
- [x] **#520** sweep / for_each axis-token refusals (in-effort)

## Scope

**In scope**
- `internal/detmath` and its use by query expressions, `FEAT_LOG`, `**` / `^`, `profile create --fit-shape`, synth generation and the copula
- Trigonometry and the `pi()` / `e()` constants in query and synth expressions (not `OVERLAY_FORMULA`)
- Synth `row()`, `lag()`, accepted-row `monotonic_from`; `now()` refused
- `Request.Params`, Compose / chain inheritance, exact `u64` comparisons
- The template and axis splice refusals and the single expression-slot list

**Out of scope**
- `params.x` as a sweep axis, a `params` slot on `FilterToFileRequest` / `FacetRequest` (handed on below)
- The `--fit-models` OLS capture residual (machine-sensitive; documented)
- The time-effect distributions of [U52](U52-synth-realism.md) #363

## Epics & stories

### E1 — Deterministic maths and trigonometry in query expressions
- S1 the `detmath` kernels, S2 query formulas, `FEAT_LOG`, trig and the `**` operator on them
### E2 — Synth generates a recursive adstock
- S1 the synth math set and `detmath` generation, S2 `row()` and `monotonic_from`, S3 `lag()` and the ziggurat, S4 the skill and the example
### E3 — Request parameters reach expressions
- S1 `Request.Params` and exact `u64`, S2 Compose / chain inheritance, S3 skills, example and book pages
### E4 — Templates drive parameterised expressions safely
- S1 the splice refusals, S2 the roadmap and migration record

## Acceptance criteria

- [x] A query and a synth rule evaluate `exp` / `log` / `pow` / trig to the same bits on every architecture; synth output is byte-identical per `(spec, seed)`
- [x] A synth spec generates a recursive adstock with Fourier controls through `lag()` and `row()`
- [x] `params.<name>` reads the same value on predict and runtime, in Compose and chains, and a template fills it by value
- [x] `{{}}` and axis tokens inside an expression are refused with coded errors
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPayloadSchemaGolden`, `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestSkillTokenBudget`, `TestSkillsCoverFeatureFences`, `TestManifest*Complete`
- The `detmath` kernel suites, the synth determinism and cross-row suites, the params and splice suites

## Update Demand companions

- Skills: `request-params`, `synth-cross-row`, `synthetic-data`, `attribute-composition`, `request-templating`, `compose-sweeps`
- Goldens regenerated with `-update`; `format_version` stays `"1.1"`
- [Embedder migration](../v1.0.0-api-and-release/03-embedder-migration.md) (Changes from U58)

## Handed on to later work

Filed as TODO items in [`TODO.md`](../TODO.md) (ids are TODO ids, not GitHub issues):

| Item | Owner |
|---|---|
| #521 `params.x` as a Compose sweep axis target (`internal/sweep`); also lets a for_each over `ATTR_FORMULA` vary values inside an expression again | [U40](U40-compose-sweep.md) |
| #522 `FilterToFileRequest` and `FacetRequest` have no `params` slot (`params.x` there is `PULSE_PARAMS_UNDECLARED`) | [U40](U40-compose-sweep.md) |
| #523 Residual `u64` float64 binding (exact only to 2^53): `$env` reads, a `let` name, a `u64` column a feature or attribute rewrote | [U35](U35-predict-runtime-parity.md) |
| #524 `vectors.ExprSlots` does not walk features (FEAT_TARGET_ENCODE `params.fit_on`); `internal/exprslots` covers the template / sweep side | [U35](U35-predict-runtime-parity.md) |
| #525 `internal/examples/fixtures/gen.go` uses `NormFloat64` with unbarriered multiply-adds (machine-sensitive if bit-pinned) | [U52](U52-synth-realism.md) |
| #526 The template store overwrites `details.path` with the OS file path when wrapping a load-time fault at `pulse.New` (predates U58) | [U33](U33-v1-release.md) |
| #527 Pre-existing mdBook warning "unclosed HTML tag `<type>`" in `docs/src/contract/payload-schema.md` | [U32](U32-docs-audit.md) |

## Human inputs & decisions

- [x] Decided by the owner in the effort: the `**` / `^` patcher, generation and the copula on `detmath`, the ziggurat port, exact `u64` fields and the axis-splice refusal ride this unit rather than follow-ups.
