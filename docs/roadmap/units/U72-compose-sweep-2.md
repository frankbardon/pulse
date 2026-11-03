---
id: U72
slug: compose-sweep-2
title: "The Compose sweep follow-ups are closed: axis profiles, item fan-out in a sweep body, streamed fits, params axes and the remaining search strategies and test gaps"
track: API & release
size: L
status: not-started
depends_on: [U77, U78]
soft_depends_on: [U71]
blocks: [U32]
todo_items: [298, 380, 392, 521, 522, 548, 579, 580]
branch: compose-sweep-2
---

# U72 — compose-sweep-2

**Outcome:** The Compose sweep follow-ups are closed: axis profiles, item fan-out in a sweep body, streamed fits, params axes and the remaining search strategies and test gaps.

**Track:** API & release · **Size:** L · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first) · **Soft:** U71

## Summary

Compose sweep follow-ups: a per-axis profile in the ranking output, `for_each` inside a sweep body, a terminal `fits` line on `compose --stream`, `params.x` as a sweep axis target, a `params` slot on `FilterToFileRequest` and `FacetRequest`, evolutionary and Bayesian-optimisation search, and two untested sweep failure paths. Soft-depends on U71 because sweep and optimize share the Compose surface.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#298** Per-axis profile in the ranking output (minimum of the metric over the other axes, per axis value) for the decay identifiability reading (worked-example gap 15) · [U72](units/U72-compose-sweep-2.md)
- [ ] **#380** A `for_each` item inside a compose `sweep` body (lexical scoping of item axes under sweep axes); refused for v1 with `nested_in_sweep`, can grow without a break. Found in U41 (interview) · [U72](units/U72-compose-sweep-2.md)
- [ ] **#392** `pulse api compose --stream` ends with an optional terminal `{"fits":…}` NDJSON line, like the sweep's `ranking` line; today a stream carries no fit result (documented). Found in U42 (E1-S1) · [U72](units/U72-compose-sweep-2.md)
- [ ] **#521** `params.x` as a Compose sweep axis target (`internal/sweep`), so a sweep varies a request parameter instead of splicing text; it also gives a for_each over ATTR_FORMULA a way to vary values inside an expression again (the axis-splice refusal removed the text route). Found in U58 (E4-S1) · [U72](units/U72-compose-sweep-2.md)
- [ ] **#522** `FilterToFileRequest` and `FacetRequest` have no `params` slot, so `params.x` there is `PULSE_PARAMS_UNDECLARED`; add the slot (additive, `capability:params`, payload-schema golden) or document the omission. Found in U58 (E3-S1) · [U72](units/U72-compose-sweep-2.md)
- [ ] **#548** Evolutionary (CMA-ES / differential evolution) and Bayesian-optimisation sweep modes. They are adaptive (each draw depends on earlier results), which breaks the expand-then-run design and predict's exact slot count; needs its own design. Deferred from U60 · [U72](units/U72-compose-sweep-2.md)
- [ ] **#579** A sweep body whose regression `models` paths vary by axis is untested for a sweep-bound fit (the bound fit uses the winner's loaded files). Deferred from U63 · [U72](units/U72-compose-sweep-2.md)
- [ ] **#580** A parallel non-FailFast phase-0 failure aggregates a `SERVICE_INTERNAL` that lists only the phase-0 (sweep) failures, because the explicit slots never run: intended, but documented only in code. Document it in `execution-modes/compose.md` and the skill. Deferred from U63 · [U72](units/U72-compose-sweep-2.md)
