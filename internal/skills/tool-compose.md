---
name: tool-compose
kind: tool
description: Execute a batch of processing requests in one round-trip.
type: reference
applies_to: compose, process, mcp
---

## When to use

CALL TO RUN SEVERAL REQUESTS IN ONE ROUND-TRIP.
Distinct requests over one or more cohorts: ecological regression (slot 1 aggregates, slot 2 regresses over them) or any loop that would take N round-trips.

## Input

The batch's keys at top level (no wrapper): `requests` (the slot list, each a full request with its own `cohort`) plus optional Compose-level `overlays` that fold across slots (see `compose-requests`).<!-- feature: capability:compose_sweep --> An optional `sweep` expands one request body over axes into more slots (`compose-sweeps`); a sweep body's missing `return` defaults to `standard`.<!-- /feature --><!-- feature: capability:compose_sweep_adaptive --> `de` / `bo` search a `budget` (`compose-sweep-adaptive`).<!-- /feature --><!-- feature: capability:compose_fits --> An optional `fits` list runs named regressions or vector batteries once before the slots and binds each fit to every slot by name (`compose-fits`).<!-- /feature --><!-- feature: capability:models --> A request's `models` / `populations` load saved fits by name.<!-- /feature --><!-- feature: capability:compose_banner --> An optional `banner` expands sections × column groups (+ Total) into crosstab slots (`compose-banner`).<!-- /feature --> Each slot's `return` shapes its response (default `standard` on MCP); a top-level `return` (`overlays…` paths only) shapes `data.overlays` after the fold.

## Output

The composed response (no envelope):

- `responses[]` — one response per slot in input order (a process response); each carries its final `label` (caller's or `request_<n>`).
- `overlays[]` — one layer per Compose `overlays[i]` spec; omitted when none is declared. `overlays[i].warnings[]` are coded `{code, message, details}` (cohesion, missing coordinates, panel cap), omitted when empty.
<!-- feature: capability:compose_banner -->
- `banner` — the stitched banner tables (column blocks, per-block letters); omitted without a banner.
<!-- /feature -->
- `warnings[]` — batch-level diagnostics no slot owns<!-- feature: capability:compose_fits --> (a fit save's overwrite, `details.fit`)<!-- /feature -->; omitted when empty.

## Gotchas

- Compose overlays match slot schemas exactly by default; an overlay's `options.dict_prefix_fast: true` accepts dictionary-prefix-equal categoricals.
- Panel overlays cap `targets` at `options.max_panel_targets` (default 16); overflow → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- A slot that fails validation or cannot open its cohort fails the WHOLE batch: one tool error naming the slot in `details.request` (0-based). A test / regression / matrix slot that fails on its data keeps its place and warns `PULSE_SLOT_FAILED`.

## See

- `compose-requests` (slots, parallel knobs, the overlay fold), `response-components`, `overlay-system`, `compose-sweeps`, `compose-fits`, `compose-banner`.
