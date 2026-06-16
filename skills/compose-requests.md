---
name: compose-requests
description: ComposedRequest batch semantics — order-preserving slot-by-index dispatch, optional parallel execution, slot labels, post-slot Compose-overlay fold that decorates results without mutating per-slot Components.
type: guide
kind: design
applies_to: compose, predict
covers: [ComposedRequest, pulse_compose, OverlayLayer]
---

# Compose requests

`ComposedRequest` bundles N independent `Request` objects into one round-trip. Use it when several analyses share a cohort — Pulse loads + decodes the cohort once and dispatches every slot against the same record set.

```jsonc
{
  "requests": [
    {"cohort": {"filename": "students.pulse"},
     "aggregations": [{"type": "AGG_COUNT", "field": "score"}],
     "filterers":    [{"type": "FILTER_INCLUDE", "field": "grade", "values": ["A"]}]},
    {"cohort": {"filename": "students.pulse"},
     "aggregations": [{"type": "AGG_AVERAGE", "field": "score"}],
     "groups":       [{"type": "GROUP_CATEGORY", "field": "department"}]}
  ]
}
```

## Order-preserving slot dispatch

Slots execute in declared order; the response is an array in the same order. Each slot carries its own `Response` shell — per-slot `Metadata`, `Aggregations`, `Crosstab`, `Components`, etc. Filter state in slot `i` does not affect slot `j`.

```jsonc
[
  {"data": [...], "metadata": {"total_rows": 1000, "filtered_rows": 50}},
  {"data": [...], "metadata": {"total_rows": 1000, "filtered_rows": 1000}}
]
```

## Shared cohort optimisation

When every slot's `cohort.filename` resolves to the same path, the orchestrator decodes the file once and shares record buffers across slots. Mixed cohorts fall through to per-slot independent decode.

## Slot labels

Each `Request` carries an optional `label` (`"label,omitempty"`). The engine auto-fills empty labels with `request_<index+1>` (1-based) against a clone — your `*Request` pointer is never mutated. Labels are the resolution key for Compose-only overlay `Reference` / `Target` lookups.

Two slots resolving to the same final label (caller duplicates OR caller-vs-auto collision) reject with `PULSE_COMPOSE_LABEL_COLLISION` before any slot runs. Set explicit labels when an overlay references the slot by name; omit otherwise and accept the default. The slot is additive — omitting it keeps wire bytes and `CanonicalHash` output byte-identical to pre-label callers.

## Parallel execution

`ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}` (CLI: `pulse api compose --parallel N --fail-fast --timeout 30s`) gates a bounded worker pool. `MaxWorkers <= 1` forces serial. `FailFast: true` cancels in-flight slots on the first error; default mode runs every slot and collects per-slot errors. Determinism: response order matches request order regardless of completion order.

## Post-slot Compose-overlay fold

`ComposedRequest.Overlays []OverlaySpec` runs AFTER every slot finalises. The fold reads each slot's already-emitted `Response.Crosstab` / `Response.Data` / `Response.Components` (read-only) and writes a sibling `Response.Overlays[i]` entry. **The fold never mutates per-slot `Components` or the per-slot payload** — overlays are an additive decoration keyed to host coordinates (see `skills/overlay-system.md`).

Compose-only kinds (`OVERLAY_PROP_Z_PANEL`, `OVERLAY_PANEL_INDEX_VS_REF`, etc.) resolve `Reference.SlotLabel` / `Target.SlotLabel` against the auto-or-explicit labels above. Schema-divergence (per-axis grouper-kind tuple mismatch) is the most common reject; the no-execute companion `descriptor.ValidateCompose(req)` walks every overlay against per-slot request shapes (MATRIX / SERIES / SCALAR) and populates `ComposeValidationResult.OverlaysSchemaDivergence []SlotPair` with `(ReferenceLabel, TargetLabel, Reason)` for every offender. Run it before paying for `pulse_compose`.

### Optional fast-path knob

`OverlaySpec.Options.DictPrefixFast bool` — multi-slot schema-match via byte-equal dictionary prefix probe. Requires embedder to verify prefix-equal dicts. Default `false`. `MaxPanelTargets int` caps `OVERLAY_PROP_Z_PANEL` / `OVERLAY_PANEL_INDEX_VS_REF` Targets; overflow → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP` (default 16).

## Per-slot Components contract (v0.20.0)

Every slot's `Response.Components` is emitted independently — the universal floor (`{n, n_null}` on aggregators, `{total_n, n_null}` on groupers, `{n_in, n_out, n_null_input}` on filterers) plus per-operator `Operator` map ride the per-slot response. The Compose-overlay fold runs AFTER per-slot Components emission and treats them as read-only inputs; overlays never rewrite the per-slot `Components` block. Consumers can render a per-slot Components view immediately and a Compose-overlay decoration layer on top.

## Validate before executing

`pulse_predict` has no batch mode in v1 — loop per slot to catch field typos, missing categorical dicts, or aggregator-type mismatches before paying for `pulse_compose`. For `ComposedRequest.Overlays` use `descriptor.ValidateCompose` (no-execute, walks every spec against per-slot shapes).

## See

- `response-components` — per-slot Components shape + universal floor + per-operator keys.
- `overlay-system` — Compose-only overlay catalog and resolution rules.
- `streaming-and-watching` — `Request.Hash()` for per-slot cache keys; `StreamResult` is Process-only (not Compose).
- `process-chain` — sequential pipeline alternative when slots depend on each other.
