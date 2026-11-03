---
id: U76
slug: synth-fidelity-2
title: "Synthetic fidelity gaps close with captured, measured and bounded behaviour."
track: API & release
size: M
status: not-started
depends_on: [U77, U78]
soft_depends_on: []
blocks: [U32]
todo_items: [594, 595, 596, 597, 525, 433]
branch: synth-fidelity-2
---

# U76 — synth-fidelity-2

**Outcome:** Synthetic fidelity gaps close with captured, measured and bounded behaviour.

**Track:** API & release · **Size:** M · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first)

## Summary

Synthetic-data fidelity depth: datetime volume intensity capture, a bounded disclosure pass, a stable model-fit ratio, a minimum pair strength, portable fixture generation, and real-versus-synthetic comparison in one request.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#594** Synth `datetime` profile capture keeps only the UTC weekday and hour marginals over a UTC day range: no per-day or per-month volume intensity (a trend or a seasonal swing in row volume regenerates flat) and no local-zone profile (the schema carries no zone). A per-day / per-month intensity capture is a new profile field plus a new `uniform_datetime` param. Found in U52 (E1-S2) · [U76](units/U76-synth-fidelity-2.md)
- [ ] **#595** The fidelity report's `disclosure` DCR pass is bounded only by its sample cap (`--disclosure-sample`, default 10,000) and charged to `max_estimated_memory`; its time is sample × source rows × columns with no limit of its own. Whether to add a dedicated `max_disclosure_comparisons`-style limit is an owner call. Found in U52 (E3-S2) · [U76](units/U76-synth-fidelity-2.md)
- [ ] **#596** The fidelity `utility.model_fits` per-coefficient `ratio` is unstable when the source coefficient is near 0 (the fixture's no-effect `age` reads 16.6); documented in `docs/src/cli/synth-from-profile.md`, not changed. Decide whether to report an absolute or SE-scaled difference instead of (or beside) the ratio. Found in U52 (E2-S3) · [U76](units/U76-synth-fidelity-2.md)
- [ ] **#597** Profile pair arbitration has no minimum strength: when two pairs contend for one target with strengths both ≈ 0, the winner is effectively arbitrary (seen on the casebook privacy source: a no-effect `region` took `contract` instead of `plan`). Add a floor or a tie-break toward declared order below it. Found in U52 (E3-S4) · [U76](units/U76-synth-fidelity-2.md)
- [ ] **#525** `internal/examples/fixtures/gen.go` draws with `math/rand/v2` `NormFloat64` and unbarriered multiply-adds, so its fixtures are machine-sensitive if they are ever bit-pinned; move it onto `detmath` and the portable ziggurat before any fixture is digest-pinned. Found in U58 (E2-S3) · [U76](units/U76-synth-fidelity-2.md)
- [ ] **#433** One request comparing a real cohort against its synthetic twin (the privacy-safe-sharing showcase's 14 slots): `groups` cannot span two cohorts, so the comparison still needs a slot per cohort side · [U76](units/U76-synth-fidelity-2.md)
