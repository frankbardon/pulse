---
id: U74
slug: memory-bounds
title: "A 50M-row archive run fits in 16 GB without GOMEMLIMIT, with bounded merge windows, streamed output and a soft memory limit"
track: Embedder operations
size: M
status: not-started
depends_on: [U77, U78, U73]
soft_depends_on: []
blocks: [U32]
todo_items: [494, 501, 502, 503, 361]
branch: memory-bounds
---

# U74 — memory-bounds

**Outcome:** A 50M-row archive run fits in 16 GB without GOMEMLIMIT, with bounded merge windows, streamed output and a soft memory limit.

**Track:** Embedder operations · **Size:** M · **Depends on:** U77, U78, U73 (U77, U78: CI and test hygiene land first; U73 is a hard dependency)

## Summary

Memory bounds on the archive path: `total()` on the parallel arms for grouped, multi-layer and chain requests, a streamed `--json` envelope, a bounded merge window for in-flight shard partials, and a soft memory limit. Closes with #361, the 50M-row / 16 GB acceptance, which stays open until the retail example runs inside 16 GB without `GOMEMLIMIT`. U73 is a hard dependency.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#494** Grouped, multi-layer and `ProcessChain` `total()` on the parallel arms (#399 delivered the ungrouped, one-layer form). Grouped needs per-partition pre-pass publication before phase 2; multi-layer needs one parallel pre-pass per layer (`TotalsPrepass` per layer, `twoPassPlan.needed[L]`); chains need a two-phase stage runtime and a validator twin. Sites: `internal/mergegate/gate.go` (`ParallelTotals`, `ChainRefusal`), `internal/processing/formula_total_merge.go`, `internal/service/totals_prepass.go`, `internal/service/chain.go`, `internal/descriptor/chain.go`. Found in U51 (E3-S1) · [U74](units/U74-memory-bounds.md)
- [ ] **#501** Stream the CLI `--json` envelope's `data` rows for `pulse api process`, `process-chain` and `compose` instead of building the whole response and encoding it in one `json.Marshal`: write the envelope head, then each row as it is produced, then the tail, byte-identical to today's output (a digest test on every arm, weighted and `Return` included). The CLI's whole-response encoding is the peak of `pulse api process-chain --json` on the 1/20 retention archive (5.6 GB footprint); an E5-S12 prototype cut it 23%. One of the three holders that keep the 50M retention chain above #361's 16 GB without `GOMEMLIMIT` (owner-accepted miss, U51 E5-S2). Sites: `internal/cli/json.go` (`writeEnvelope*`), `internal/cli/api.go`, `descriptor.NewEnvelope`. Found in U51 (E5-S12) · [U74](units/U74-memory-bounds.md)
- [ ] **#502** Bound the in-flight shard partials to a merge window equal to the shard workers (today `limits.ShardMergeWindow` admits 2 × workers): on the dense 1/20 retention shape max RSS fell 1.90 → 1.82 GB and on spend 1.33 → 0.85 GB. When a shard holds about half the customers, as at 50M, the partials are the stage-0 peak, so this is one of the three holders that keep the 50M retention chain above #361's 16 GB without `GOMEMLIMIT` (owner-accepted miss, U51 E5-S2). Predict's `descx.ShardMergeWindow` and its memory estimate must move with the runtime (a predict ≡ runtime parity test on the window). Sites: `internal/limits/memory.go` (`ShardMergeWindow`, `ShardMergeWindowWithin`), `internal/descriptor/limits.go` (`ShardMergeWindow`), `internal/service/shard_reduce.go`. Found in U51 (E5-S12) · [U74](units/U74-memory-bounds.md)
- [ ] **#503** A Pulse soft memory limit: a CLI flag and a `pulse.Options` budget (and possibly a default derived from physical RAM) that set the Go runtime soft limit (`debug.SetMemoryLimit`) for the run, so a large grouped run fits the machine without the caller knowing about `GOMEMLIMIT`. On the 50M retention chain `GOMEMLIMIT=14GiB` fit at a 14.0–14.8 GiB peak footprint with identical output and finished faster than the unlimited run (113–117 s against 181–187 s; less paging), against a 17.3–23.1 GiB footprint without it: #361's "16 GB without `GOMEMLIMIT`" was missed and accepted by the owner (U51 E5-S2). Needs an Update Demand pass (CLI leaf flag, `Options` field, env-var rules) and a decision on whether the default may change behaviour. Found in U51 (E5-S12) · [U74](units/U74-memory-bounds.md)
- [ ] **#361** Acceptance: the retail example runs at 50M rows on 4 vCPU / 16 GB without `GOMEMLIMIT`; archive header operations finish in under a second; customer lookups work on the archive; the customer chains run on every customer, not a 1-in-10 panel; the bench program is replaced by CLI flags · [U74](units/U74-memory-bounds.md) — *partial, 16 GB part missed and accepted by the owner (E5-S2 at 9fb6872b): every per-step target is met, including `api predict` on the archive (0.20 s, 52 MB); without `GOMEMLIMIT` the 50M retention chain peaks at a 17.3–23.1 GiB footprint (13.0–14.6 GiB RSS), more than 16 GB; with `GOMEMLIMIT=14GiB` it peaks at 14.8 GiB in 117 s with identical output. Open because the text says "16 GB without `GOMEMLIMIT`"; remaining work filed as #501, #502, #503*
