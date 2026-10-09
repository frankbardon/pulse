```yaml
name: tool-compose
kind: tool
description: Execute a batch of processing requests in one round-trip.
type: reference
applies_to: compose, process, mcp
```

## When to use

CALL TO RUN SEVERAL REQUESTS IN ONE ROUND-TRIP.
Distinct `types.Request` payloads against the same or different cohorts: ecological regression (slot 1 aggregates, slot 2 regresses over aggregates) or any multi-question loop that would otherwise take N round-trips.

## Input

`request` (string): JSON-encoded `types.ComposedRequest`. Carries `requests` (the slot list) plus optional Compose-level overlays that fold across slots (see [`compose-requests`](compose-requests.md)). Parallel execution: `Options.MaxWorkers`, `PerRequestTimeout`, `FailFast`. Each slot's `return` shapes its response; a top-level `return` (`overlays…` paths only) shapes `data.overlays` after the fold.

## Output

`descriptor.Envelope` (`format_version: "1.1"`) wrapping a `pulse.ComposedResponse` on `data`:

- `data.responses[]` — full `Response` per slot in input order; each carries its own `Data`, `Metadata`, `Components`, per-Request `Overlays`.
- `data.overlays[]` — `OverlayLayer` per Compose `overlays[i]` spec via `internal/service/compose_overlay.go`. Omitted when no Compose overlays declared.
- `data.overlays[i].warnings[]` — per-layer `OverlayWarning{code, message, details}` diagnostics (cohesion, missing coordinates, panel overflow). Omitted when empty, so overlay-free Compose stays byte-identical (`TestComposedResponse_OverlayFreeByteIdentical`).

## Gotchas

- Compose-overlay multi-slot probes use byte-equal schema match by default. Enable `OverlaySpec.Options.DictPrefixFast` when slots share dictionary-prefix-equal categoricals.
- Panel overlays cap Targets at `MaxPanelTargets` (default 16); overflow → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Per-slot errors surface in that slot's envelope; Compose still returns unless `FailFast: true`.

## See

- [`compose-requests`](compose-requests.md) — multi-slot patterns, parallel knobs, overlay fold.
- [`response-components`](response-components.md) — emitted per slot.
- [`overlay-system`](overlay-system.md) — Compose-level overlay kinds.
