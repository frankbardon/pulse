---
name: tool-compose
kind: tool
description: Execute a batch of processing requests in one round-trip.
type: reference
applies_to: compose, process, mcp
---

## When to use

Multiple distinct `types.Request` payloads against the same or different cohorts. Ecological-regression patterns (slot 1 aggregates, slot 2 regresses over aggregates). Any multi-question authoring loop where individual `pulse_process` calls would be N round-trips.

## Input

`request` (string): JSON-encoded `types.ComposedRequest`. Carries `requests` (the slot list) plus optional Compose-level overlays that fold across slots (see `compose-requests`). Parallel execution: `Options.MaxWorkers`, `PerRequestTimeout`, `FailFast`.

## Output

`descriptor.Envelope` (`format_version: "1.1"`) wrapping a `pulse.ComposedResponse` on `data`:

- `data.responses[]` — full `Response` per slot in input order; each carries its own `Data`, `Metadata`, `Components`, per-Request `Overlays`.
- `data.overlays[]` — `OverlayLayer` per Compose `overlays[i]` spec via `service/compose_overlay.go`. Omitted when no Compose overlays declared.
- `data.overlays[i].warnings[]` — per-layer `OverlayWarning{code, message, details}` diagnostics (cohesion, missing coordinates, panel overflow). Omitted when empty, so overlay-free Compose stays byte-identical (`TestComposedResponse_OverlayFreeByteIdentical`).

## Gotchas

- Compose-overlay multi-slot probes use byte-equal schema match by default. Enable `OverlaySpec.Options.DictPrefixFast` when slots share dictionary-prefix-equal categoricals.
- Panel overlays (`OVERLAY_PROP_Z_PANEL`, `OVERLAY_PANEL_INDEX_VS_REF`) cap Targets at `MaxPanelTargets` (default 16); overflow → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Per-slot errors surface in the slot's envelope; Compose returns the array even when one slot fails (unless `FailFast: true`).

## See

- `compose-requests` — multi-slot patterns, parallel knobs, overlay fold.
- `response-components` — emitted per slot.
- `overlay-system` — Compose-level overlay kinds.
