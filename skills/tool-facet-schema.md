---
name: tool-facet-schema
kind: tool
description: Multi-field rich facet — counts, nulls, percentiles, histograms, additive contributions.
type: reference
applies_to: facet, mcp
---

## When to use

Multi-field rollups, counts (not just distinct values), null tallies, numeric streaming stats, fixed-width histograms, top-K truncation, additive-contribution counts. The faceted-UI / discovery sibling to `pulse_facet`. Also reusable for "if I added value V to my filter, what is the surviving population" — that's `AdditiveFields`.

## Input

`request` (string): JSON-encoded `pulse.FacetRequest`. Fields: `cohort.filename`, `fields` (`string[]`), `filterers` (`Filterer[]`), `additive_fields` (`string[]`), `discrete_top_k` (`int`), `numeric_percentiles` (`float[]`), `include_histogram` (`bool`), `histogram_bins` (`int`), `histogram_range` (`[min, max]`). FACET-host overlays ride `FacetRequest.Overlays`.

## Output

`descriptor.Envelope` wrapping per-field summaries: discrete value/count lists with null tallies for categorical/boolean/geo fields; streaming `count`/`sum`/`min`/`max`/`mean`/`stddev` for numeric fields; optional `Percentiles`, `Histogram`, `AdditiveContributions`. `Response.Components` reports the facet floor + per-field operator state. FACET-host overlays appear in `Response.Overlays`.

## Gotchas

- `numeric_percentiles` forces a buffered per-field sort — not streaming.
- `additive_fields` strips that field's own filter clauses before counting — designed for UI panels that surface "what else could you add".
- FACET-host overlays (`OVERLAY_INDEX_VS_POP`, `OVERLAY_ZSCORE_VS_POP`, `OVERLAY_CHISQ_VS_POP`, `OVERLAY_KS_VS_POP`) wire via `service.applyFacetOverlays` (FacetSchema-buffered-exit hook).

## See

- `facet-design` — full endpoint contract + overlay wiring.
- `response-components` — emitted floor + per-field state.
- `overlay-system` — FACET-host overlay kinds.
