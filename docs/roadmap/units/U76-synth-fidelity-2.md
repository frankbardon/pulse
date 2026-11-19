---
id: U76
slug: synth-fidelity-2
title: "Synthetic fidelity gaps close with captured, measured and bounded behaviour."
track: API & release
size: M
status: done
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

Shipped in six epics: coefficient comparison and the pair floor (E1), a bounded disclosure pass (E2), datetime volume and the local clock (E3), portable fixtures (E4), cohort union (E5) and close-out (E6). Every owned item shipped in-unit; three discoveries are handed on (below).

## Owned TODO items

- [x] **#594** Synth `datetime` profile capture keeps only the UTC weekday and hour marginals over a UTC day range: no per-day or per-month volume intensity (a trend or a seasonal swing in row volume regenerates flat) and no local-zone profile (the schema carries no zone). A per-day / per-month intensity capture is a new profile field plus a new `uniform_datetime` param. Found in U52 (E1-S2) · [U76](units/U76-synth-fidelity-2.md)
- [x] **#595** The fidelity report's `disclosure` DCR pass is bounded only by its sample cap (`--disclosure-sample`, default 10,000) and charged to `max_estimated_memory`; its time is sample × source rows × columns with no limit of its own. Whether to add a dedicated `max_disclosure_comparisons`-style limit is an owner call. Found in U52 (E3-S2) · [U76](units/U76-synth-fidelity-2.md)
- [x] **#596** The fidelity `utility.model_fits` per-coefficient `ratio` is unstable when the source coefficient is near 0 (the fixture's no-effect `age` reads 16.6); documented in `docs/src/cli/synth-from-profile.md`, not changed. Decide whether to report an absolute or SE-scaled difference instead of (or beside) the ratio. Found in U52 (E2-S3) · [U76](units/U76-synth-fidelity-2.md)
- [x] **#597** Profile pair arbitration has no minimum strength: when two pairs contend for one target with strengths both ≈ 0, the winner is effectively arbitrary (seen on the casebook privacy source: a no-effect `region` took `contract` instead of `plan`). Add a floor or a tie-break toward declared order below it. Found in U52 (E3-S4) · [U76](units/U76-synth-fidelity-2.md)
- [x] **#525** `internal/examples/fixtures/gen.go` draws with `math/rand/v2` `NormFloat64` and unbarriered multiply-adds, so its fixtures are machine-sensitive if they are ever bit-pinned; move it onto `detmath` and the portable ziggurat before any fixture is digest-pinned. Found in U58 (E2-S3) · [U76](units/U76-synth-fidelity-2.md)
- [x] **#433** One request comparing a real cohort against its synthetic twin (the privacy-safe-sharing showcase's 14 slots): `groups` cannot span two cohorts, so the comparison still needs a slot per cohort side · [U76](units/U76-synth-fidelity-2.md)

## What shipped

- **E1 (#596, #597).** `UtilityCoefficient` gains `difference`, `z` and `ratio_reliable` beside the unchanged `ratio`; pair arbitration has a noise floor, `Spec.PairStrengthFloor` (default `synth.DefaultPairStrengthFloor` = 0.02): below it the first-declared bidder keeps the target, `0` restores the old rule.
- **E2 (#595).** `Limits.MaxDisclosureComparisons` (`max_disclosure_comparisons`, default 5 x 10^10, `-1` unbounded), checked by the `Synth` pre-flight before generation; the distance pass honours a cancelled context.
- **E3 (#594).** Month volume (`month_weights` on `uniform_date` / `uniform_datetime`, `MonthStart` / `Months` on the profile, `month_tvd` on the shape) and a local capture clock (`ProfileOptions.TimeZone` / `TimeZoneField`, CLI `--time-zone` / `--time-zone-field`, a cohort's default zone field). **E3-S3 (owner-added):** the fidelity `shape` is read on that same clock (`clock: "local"`, `time_zone` / `time_zone_field`), so a zoned twin is compared on the local hour and weekday it was drawn on.
- **E4 (#525).** `internal/examples/fixtures` is a compiled, pinned generator (math/rand/v2 PCG, `detmath`); `TestFixtures_RegenerateByteIdentical` byte-compares every CSV. Nine CSVs were redrawn, so 11 story goldens and the Analysis Guide figures moved (clustered covariance `session_minutes` is now significant; the crosstab row-normalised prose now has west lowest).
- **E5 (#433).** Cohort union: `union` / `union_source_field` on Request, ChainRequest, FacetRequest, SampleRequest and LookupRequest; `PULSE_UNION_*` codes; `capability:union`; manifest `union` block; model provenance `union`; `pulse_sample` takes a union; observability `Cohort` joins member paths with `+`. Non-union fix: Compose and chain predict now refuse a colliding join (`PULSE_JOIN_FIELD_COLLISION`) like the runtime.

## Outcome / decisions

The default pair floor moves the churn fixture's seed-1 `contract` winner (plan to region); the default disclosure bound refuses a pass past 5 x 10^10 comparisons; a >= 2-month date / datetime twin draws its day in one `Float64`, so later fields' streams shift. All three are behaviour breaks with an opt-out (migration doc, "Changes from synth-fidelity-2 (U76)").

## Handed-on

| Item | To | Why |
|---|---|---|
| #736 a fidelity `shape` for a `date` field | [U33](U33-v1-release.md) | a date twin's month volume is regenerated but not measured; needs `TimeShapeFidelity.HourTVD` optional (an API change) |
| #737 `## See` annotation pruning | [U32](U32-docs-audit.md) | `seeTags` / `renderSeeSection` do not prune an item whose annotation holds a backtick span when its skill is pruned |
| #738 request form for `pulse sample` | [U33](U33-v1-release.md) | the CLI has only `--input`, so sampling a union is library / MCP only |
