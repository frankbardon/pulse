---
name: tool-compose
kind: tool
description: Execute a batch of processing requests in one round-trip.
type: reference
applies_to: compose, process, mcp
---

## When to use

CALL TO RUN SEVERAL REQUESTS IN ONE ROUND-TRIP.
Distinct `types.Request` payloads against the same or different cohorts: ecological regression (slot 1 aggregates, slot 2 regresses over them) or any loop that would take N round-trips.

## Input

`request` (string): JSON-encoded `types.ComposedRequest`. Carries `requests` (the slot list) plus optional Compose-level overlays that fold across slots (see `compose-requests`).<!-- feature: capability:compose_sweep --> An optional `sweep` expands one request body over axes into more slots (`compose-sweeps`); a sweep body's missing `return` defaults to `standard`.<!-- /feature --><!-- feature: capability:compose_fits --> An optional `fits` list runs named regressions or vector batteries once before the slots and binds each fit to every slot by name (`compose-fits`).<!-- /feature --><!-- feature: capability:models --> A request's `models` / `populations` load saved fits by name.<!-- /feature --><!-- feature: capability:compose_banner --> An optional `banner` expands sections × column groups (+ Total) into crosstab slots (`compose-banner`).<!-- /feature --> Parallel execution: `Options.MaxWorkers`, `PerRequestTimeout`, `FailFast`. Each slot's `return` shapes its response; a top-level `return` (`overlays…` paths only) shapes `data.overlays` after the fold.

## Output

`descriptor.Envelope` (`format_version: "1.1"`) wrapping a `pulse.ComposedResponse` on `data`:

- `data.responses[]` — full `Response` per slot in input order; each carries its slot's final `label` (caller's or `request_<n>`) plus its own `Data`, `Metadata`, `Components`, per-Request `Overlays`.
- `data.overlays[]` — one `OverlayLayer` per Compose `overlays[i]` spec; omitted when none is declared.
- `data.overlays[i].warnings[]` — per-layer `OverlayWarning{code, message, details}` (cohesion, missing coordinates, panel overflow); omitted when empty, so overlay-free Compose stays byte-identical.
<!-- feature: capability:compose_banner -->
- `data.banner` — the stitched banner tables (column blocks, per-block letters); omitted without a banner.
<!-- /feature -->
- `data.warnings[]` — batch-level diagnostics no slot owns<!-- feature: capability:compose_fits --> (a fit save's overwrite, `details.fit`)<!-- /feature -->; omitted when empty.

## Gotchas

- Compose overlays match slot schemas byte-equal by default; `OverlaySpec.Options.DictPrefixFast` accepts dictionary-prefix-equal categoricals.
- Panel overlays cap Targets at `MaxPanelTargets` (default 16); overflow → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Per-slot errors surface in that slot's envelope; Compose still returns unless `FailFast: true`.

## See

- `compose-requests` (slots, parallel knobs, the overlay fold), `response-components`, `overlay-system`, `compose-sweeps`, `compose-fits`, `compose-banner`.
