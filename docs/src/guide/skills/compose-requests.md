```yaml
name: compose-requests
description: ComposedRequest batch semantics — order-preserving slot-by-index dispatch, optional parallel execution, slot labels, post-slot Compose-overlay fold that decorates results without mutating per-slot Components.
type: guide
kind: design
applies_to: compose, predict
covers: [ComposedRequest, pulse_compose, OverlayLayer]
requires: [capability:compose]
```

# Compose requests

`ComposedRequest` bundles N independent `Request` objects into one round-trip (`pulse_compose`, `pulse api compose`, `pulse.Compose`). Each slot is a complete `Request` — its own cohort, filters, groups and aggregations — and runs exactly as it would alone.

## When to compose

- **Several independent questions in one call** — a summary, a breakdown and a crosstab of one survey, or one request over several cohorts (a parameter grid: [`compose-sweeps`](compose-sweeps.md)).
- **Comparing slots** — a Compose-host overlay reads two or more finished slots and decorates the comparison (reference vs target, or a multi-slot panel).

Not a fit when a later step consumes an earlier step's OUTPUT rows — that is a chain ([`process-chain`](process-chain.md)). Every slot opens and decodes its own cohort: composing saves round-trips, not decode work.

```jsonc
{
  "requests": [
    {"label": "a_grades",
     "cohort": {"filename": "students.pulse"},
     "aggregations": [{"type": "<aggregator>", "field": "score"}],
     "filterers":    [{"type": "<value filter>", "field": "grade", "values": ["A"]}]},
    {"label": "by_dept",
     "cohort": {"filename": "students.pulse"},
     "aggregations": [{"field": "score"}],
     "groups":       [{"field": "department"}]}
  ]
}
```

`<…>` = any operator the manifest lists for that slot. Slot 2 leans on smart defaults: a field with no `type` gets the default for its schema type ([`request-envelope`](request-envelope.md)).

## Order-preserving slot dispatch

The response is a `ComposedResponse` object `{responses, overlays}`: `responses[i]` answers `requests[i]`, whatever order slots finished in. Each slot carries its own full `Response` — `metadata`, `data`, `crosstab`, `components`. Filter state in slot `i` never affects slot `j`. A slot's `return` shapes `responses[i]`; a top-level `return` takes `overlays…` paths only.

```jsonc
{
  "responses": [
    {"data": [...], "metadata": {"total_rows": 1000, "filtered_rows": 50}},
    {"data": [...], "metadata": {"total_rows": 1000, "filtered_rows": 1000}}
  ],
  "overlays": [ /* one OverlayLayer per Compose overlay spec; key omitted when there are none */ ]
}
```

## Slot labels

Each `Request` takes an optional `label`. An empty label is auto-filled with `request_<index+1>` (1-based) on a clone — the caller's request is never mutated. Labels are how a Compose overlay names its `Reference` / `Target` slots, so set them explicitly whenever an overlay refers to a slot.

Two slots resolving to the same final label (duplicates, or a caller label colliding with an auto label) ⇒ `PULSE_COMPOSE_LABEL_COLLISION` before any slot runs. Omitting labels keeps wire bytes and `CanonicalHash` identical to label-free callers.

## Parallel execution

`pulse.ComposeParallel` with `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}` runs slots on a bounded pool (`MaxWorkers` 0 ⇒ GOMAXPROCS; 1 ⇒ serial). CLI: `pulse api compose --parallel N` (default 1 = serial, 0 = GOMAXPROCS) and `--no-fail-fast`. `FailFast` cancels in-flight siblings on the first error; without it every slot runs and failures fold into one `SERVICE_INTERNAL {failed_indices, first_error}`. Response order always matches request order.

**Located refusals.** A slot's own refusal (an unsupported zone, more than one join) keeps its code and gains `details.request` — the 0-based slot index — serial and parallel alike.

## Post-slot Compose-overlay fold

`ComposedRequest.Overlays` runs AFTER every slot finalises. The fold reads each slot's finished `crosstab` / `data` / `components` (read-only) and writes one sibling `overlays[i]` layer per spec. **It never mutates a slot's payload or `components`** — overlays are additive decorations keyed to host coordinates ([`overlay-system`](overlay-system.md)).

Compose-host overlay kinds resolve `Reference.SlotLabel` / `Target.SlotLabel` against the labels above; find them among the manifest `overlays[]` entries serving `compare_groups` or `benchmark` and read their atomic skills. The usual refusal is schema divergence — the reference and target slots group or crosstab on different axes; keep the compared slots' shapes identical and vary only the filter or cohort. Per-layer diagnostics land on `overlays[i].warnings`.

Library knobs on `OverlaySpec.Options`: `DictPrefixFast` (match slot schemas by byte-equal dictionary PREFIX; only when verified) and `MaxPanelTargets` (default 16; overflow ⇒ `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`).

## Multiplicity

`ComposedRequest.multiplicity` is every slot's default; `compose` family pools all slots and Compose-host layers, the rest stay per slot; serial == parallel, `--stream` emits no corrections ([`multiplicity-correction`](multiplicity-correction.md)).

## Per-slot Components contract

Every slot's `components` is emitted independently — the universal floor (`{n, n_null}` per aggregator, `{total_n, n_null}` per grouper, `{n_in, n_out, n_null_input}` per filterer) plus each operator's own keys. The overlay fold runs AFTER that emission and treats it as read-only input, so a client can render per-slot components immediately and the overlay layer on top. `--no-components` / `DisableComponents` suppresses them on every slot ([`response-components`](response-components.md)).

## Validate before executing

`pulse_predict` takes one `Request`: loop it over the slots to catch field typos, missing dictionaries and type mismatches before paying for `pulse_compose`. Compose overlay specs are checked when the fold runs, after the slots — a mislabelled overlay costs the full batch; check every `SlotLabel` against your labels.

## See

[`response-components`](response-components.md) (per-slot components shape) · [`overlay-system`](overlay-system.md) (overlay hosts and resolution) · [`streaming-and-watching`](streaming-and-watching.md) (`Request.Hash()` per-slot cache keys; `--stream` emits per-row events and skips the overlay fold) · [`process-chain`](process-chain.md) (when slots depend on each other).
