---
id: U72
slug: compose-sweep-2
title: "The Compose sweep follow-ups are closed: axis profiles, item fan-out in a sweep body, streamed fits, params axes and the remaining search strategies and test gaps"
track: API & release
size: L
status: done
depends_on: [U77, U78]
soft_depends_on: [U71]
blocks: [U32]
todo_items: [298, 380, 392, 521, 522, 548, 579, 580, 691, 704]
branch: compose-sweep-2
---

# U72 — compose-sweep-2

**Outcome:** The Compose sweep follow-ups are closed: axis profiles, item fan-out in a sweep body, streamed fits, params axes and the remaining search strategies and test gaps.

**Track:** API & release · **Size:** L · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first) · **Soft:** U71

## Summary

Compose sweep follow-ups: a per-axis profile in the ranking output, `for_each` inside a sweep body, a terminal `fits` line on `compose --stream`, `params.x` as a sweep axis target, a `params` slot on `FilterToFileRequest` and `FacetRequest`, evolutionary and Bayesian-optimisation search, and two untested sweep failure paths. Soft-depends on U71 because sweep and optimize share the Compose surface.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#298** Per-axis profile in the ranking output (minimum of the metric over the other axes, per axis value) for the decay identifiability reading (worked-example gap 15). Done (compose-sweep-2 E2-S3, E3-S1): `rank.profile: true` adds a top-level `ranking_profile` `{axis: [{value, best, label, n}]}` read over the full ranking; a range axis is binned by `rank.profile_bins` (entries add `lo` / `hi`); off is byte-identical and the profile never moves a derived seed · [U72](units/U72-compose-sweep-2.md)
- [x] **#380** A `for_each` item inside a compose `sweep` body (lexical scoping of item axes under sweep axes); refused for v1 with `nested_in_sweep`, can grow without a break. Found in U41 (interview). Done (compose-sweep-2 E2-S2): a `for_each` on any of the five request item kinds fans out inside a sweep body (`nested_in_sweep` removed); an item axis naming a sweep axis is `PULSE_FOR_EACH_INVALID` `axis_shadowed`. A Compose `optimize` `for_each` runs beside the sweep, its axes never meeting the sweep's · [U72](units/U72-compose-sweep-2.md)
- [x] **#392** `pulse api compose --stream` ends with an optional terminal `{"fits":…}` NDJSON line, like the sweep's `ranking` line; today a stream carries no fit result (documented). Found in U42 (E1-S1). Done (compose-sweep-2 E2-S4): `pulse api compose --stream --stream-fits` (library `pulse.WriteComposeStream` with `ComposeStreamOptions{TerminalFits}`) ends the stream with one `{"fits":{...}}` line equal to the buffered `fits`, only when the request declares fits; off is byte-identical · [U72](units/U72-compose-sweep-2.md)
- [x] **#521** `params.x` as a Compose sweep axis target (`internal/sweep`), so a sweep varies a request parameter instead of splicing text; it also gives a for_each over ATTR_FORMULA a way to vary values inside an expression again (the axis-splice refusal removed the text route). Found in U58 (E4-S1). Done (compose-sweep-2 E2-S1): a sweep axis `target: "params.<name>"` writes its value into each slot's params (refusals `target_invalid`, `target_duplicate`, `axis_target_conflict`), predict and runtime alike, gated by `capability:params` · [U72](units/U72-compose-sweep-2.md)
- [x] **#522** `FilterToFileRequest` and `FacetRequest` have no `params` slot, so `params.x` there is `PULSE_PARAMS_UNDECLARED`; add the slot (additive, `capability:params`, payload-schema golden) or document the omission. Found in U58 (E3-S1). Done (compose-sweep-2 E2-S1): `FilterToFileRequest.Params` (library surface, request hash includes it, a params-free hash unchanged); `FacetRequest` already carried `params` · [U72](units/U72-compose-sweep-2.md)
- [x] **#548** Evolutionary (CMA-ES / differential evolution) and Bayesian-optimisation sweep modes. They are adaptive (each draw depends on earlier results), which breaks the expand-then-run design and predict's exact slot count; needs its own design. Deferred from U60. Done (compose-sweep-2 E3-S2, E3-S3, E4-S1, E4-S2): a shared adaptive executor (`budget` + `rank`, rounds as barriers inside phase 0, predict `expanded_count` = budget with `adaptive: true` and `null` later-round labels, `capability:compose_sweep_adaptive`), `mode: "de"` (differential evolution, rand/1/bin, SciPy oracle) and `mode: "bo"` (Gaussian-process surrogate + expected improvement, scikit-optimize oracle). CMA-ES was not built: differential evolution is the evolutionary arm · [U72](units/U72-compose-sweep-2.md)
- [x] **#579** A sweep body whose regression `models` paths vary by axis is untested for a sweep-bound fit (the bound fit uses the winner's loaded files). Deferred from U63. Done (compose-sweep-2 E1-S2): `TestComposeSweepBind_WinnerModelFiles` pins that a `from_sweep` fit binds the winner's `models` files; no bug found · [U72](units/U72-compose-sweep-2.md)
- [x] **#580** A parallel non-FailFast phase-0 failure aggregates a `SERVICE_INTERNAL` that lists only the phase-0 (sweep) failures, because the explicit slots never run: intended, but documented only in code. Document it in `execution-modes/compose.md` and the skill. Deferred from U63. Done (compose-sweep-2 E1-S1): documented in `execution-modes/compose.md` and `skills/compose-sweep-bind.md`, alongside the #704 aggregate · [U72](units/U72-compose-sweep-2.md)
- [x] **#691** Flaky: `TestRequestTimeout_TripsInsideSlots/ComposeParallel/FailFast` (`internal/service/request_timeout_test.go:324`) failed once under heavy local load (load average ~80) with "no nested entry ran (calls = 1)": a fail-fast ComposeParallel run can finish before any nested slot reaches the deadline poll. Make it independent of scheduling (e.g. gate the slots on a barrier so every nested entry starts before the deadline trips). Found in test-hygiene (E1-G1 fix). Done (compose-sweep-2 E1-S2): the slots are gated on a barrier, not a clock; passes `-count=50 -race` and `GOMAXPROCS=1` · [U72](units/U72-compose-sweep-2.md)
- [x] **#704** Non-fail-fast `ComposeParallel` whose sweep phase 0 fails returns early with the phase-0 failures only, while a later-phase failure aggregates the whole batch's `SERVICE_INTERNAL` (`failed_indices`, `first_error`); pre-existing, found in library-gaps E5-S2. Make the two consistent (sibling of #580, which documents the current behaviour). Done (compose-sweep-2 E1-S1): a non-fail-fast `ComposeParallel` whose sweep phase 0 fails now returns the `SERVICE_INTERNAL` aggregate (`failed_indices`, `first_error`) plus `skipped_indices`, the explicit slots that never ran; serial `Compose` and fail-fast keep the located slot error (owner call) · [U72](units/U72-compose-sweep-2.md)

## Handed on from U79

[U79](U79-library-gaps.md#handed-on) extended the `nested_in_sweep` refusal that #380 owns to a `for_each` on an aggregation or a matrix spec inside a sweep body, like a window's. U79's note also listed a Compose `optimize` block: that was wrong. An `optimize` block is a `ComposedRequest` sibling of the sweep, never inside its body, so its `for_each` was never refused there — it runs beside the sweep and its axes never meet the sweep's (pinned by `TestForEachItems_OptimizeBesideSweep`, compose-sweep-2 E2-S2). #380 lifted the refusal for every request item kind.

## Decisions kept

- A failed sweep phase 0 under non-fail-fast `ComposeParallel` adds `skipped_indices` to the `SERVICE_INTERNAL` aggregate; serial `Compose` and fail-fast keep the located slot error (owner confirmed).
- The ranking profile is the top-level `ranking_profile` key, a sibling of the `ranking` array (owner confirmed); `rank.profile` and `rank.profile_bins` never enter the derived seed.
- Range axes keep the shipped `{name, range: {min, max, scale, integer}}` form, and grid / zip refuse them as `range_under_enumeration` (owner confirmed; the PRD's flat form and `range_not_enumerable` were dropped).
- The `de` and `bo` knobs are nested blocks, `de {population, f, cr}` and `bo {init, n_init, batch}` (owner confirmed). Both modes ride `capability:compose_sweep_adaptive`; no feature row per mode.
- `de` is rand/1/bin with synchronous generations and `<=` selection; `bo` is expected improvement with no exploration margin, constant liar (`cl_min`) for a batch, and `bo_surrogate_failed` for a surrogate that cannot be fitted. Both are outcome-pinned against an oracle (SciPy, scikit-optimize); trajectories cannot match.
- CMA-ES (named in #548 beside differential evolution) was not built: differential evolution is the evolutionary arm.
- `FilterToFileRequest.Params` is library-only (no CLI or MCP surface); `FacetRequest` already had `params`.
- `--stream-fits` writes one terminal `{"fits":{...}}` line only when the request declares fits; `--stream-fits` without `--stream` is ignored.

## Handed-on

None: every owned item shipped in-unit.
