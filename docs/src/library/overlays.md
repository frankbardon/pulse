# Overlays

**Audience:** embedders and CLI users who want a derived figure beside a
result: a share, an index, a change, a significance test.

An overlay reads a finished result and writes one layer to
`Response.Overlays[i]`, in spec order, keyed to the result's own
coordinates. It never changes `Response.Data` or the crosstab matrix, so
a request with overlays returns the same figures as one without, plus
the layers. Specs ride the request's `overlays` list:

```json cohort=experiment
{
  "cohort": {"filename": "experiment.pulse"},
  "crosstab": {
    "rows": [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell": {"type": "AGG_COUNT", "field": "id"},
    "margins": {"rows": true, "columns": true, "grand": true}
  },
  "overlays": [
    {"name": "col_share", "kind": "OVERLAY_SHARE_OF_COL", "scope": "cell", "ref": {"margin": {"axis": "column"}}},
    {"name": "vs_total", "kind": "OVERLAY_INDEX_VS_MARGIN", "scope": "cell", "ref": {"margin": {"axis": "column"}}}
  ]
}
```

## Choosing a kind

The result you have (the host) decides which kinds apply: a crosstab, a
grouped request (a series), a facet, a Compose slot set or a process
chain. Then pick the reference the figure is read against:

| Reference | Share / index / delta / z | Tests |
|---|---|---|
| A crosstab margin | `OVERLAY_SHARE_OF_ROW`, `OVERLAY_SHARE_OF_COL`, `OVERLAY_SHARE_OF_TOTAL`, `OVERLAY_INDEX_VS_MARGIN`, `OVERLAY_DELTA_VS_MARGIN`, `OVERLAY_ZSCORE_VS_MARGIN` | `OVERLAY_CHISQ_ROW`, `OVERLAY_CHISQ_COL`, `OVERLAY_CHISQ_MATRIX`, `OVERLAY_STD_RESIDUAL`, `OVERLAY_FISHER_EXACT_CELL` |
| Other columns or rows of the same crosstab | | `OVERLAY_PAIRWISE_PROP_Z`, `OVERLAY_PAIRWISE_PROBIT_T`, `OVERLAY_PAIRWISE_TWO_MEANS_Z`, `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`, `OVERLAY_PAIRWISE_WELCH_T` (significance letters with `params.letters`) |
| A series' grand total | `OVERLAY_SHARE_OF_TOTAL`, `OVERLAY_INDEX_VS_TOTAL`, `OVERLAY_ZSCORE_VS_TOTAL` | |
| Another group of the series | `OVERLAY_INDEX_VS_SIBLING`, `OVERLAY_DELTA_VS_SIBLING` | |
| An earlier point of an ordered series | `OVERLAY_INDEX_VS_PRIOR`, `OVERLAY_DELTA_VS_PRIOR`, `OVERLAY_INDEX_VS_BASELINE`, `OVERLAY_DELTA_VS_BASELINE`, `OVERLAY_INDEX_VS_ROLLING_MEAN`, `OVERLAY_ZSCORE_VS_ROLLING`, `OVERLAY_YOY` | `OVERLAY_T_VS_PRIOR`, `OVERLAY_Z_VS_PRIOR`, `OVERLAY_PROP_Z_VS_PRIOR` |
| A reference Compose slot | `OVERLAY_INDEX_VS_REF`, `OVERLAY_DELTA_VS_REF`, `OVERLAY_PANEL_INDEX_VS_REF` (every target against one reference), `OVERLAY_RANK` | `OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_PROP_Z_CELL`, `OVERLAY_PROP_Z_PANEL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`, `OVERLAY_CHISQ_VS_REF` |
| An earlier chain stage | `OVERLAY_INDEX_VS_STAGE`, `OVERLAY_DELTA_VS_STAGE` | |
| A population cohort (facet) | `OVERLAY_INDEX_VS_POP`, `OVERLAY_ZSCORE_VS_POP` | `OVERLAY_CHISQ_VS_POP`, `OVERLAY_KS_VS_POP` |

Compose kinds compare slots coordinate by coordinate, so the reference
and target slots must share a result shape (a crosstab, a series or a
scalar; else `PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT`). Keys are joined by
label; `options.dict_prefix_fast` compares dictionary indexes instead
and fails with `PULSE_OVERLAY_DICT_PREFIX_DRIFT` when the slots'
dictionaries do not share a prefix.

An index is 100 × value / reference, a delta value − reference, a z-score
(value − reference) / SD. Some kinds model the whole table instead of
comparing cells: `OVERLAY_CORRESPONDENCE` (correspondence analysis),
`OVERLAY_MARKOV` and `OVERLAY_RETENTION` (a from × to transition
crosstab), `OVERLAY_SYMMETRY` (McNemar / Bowker on a before × after
table), `OVERLAY_RAKE` (the table fitted to target margins) and
`OVERLAY_MATRIX_GROUP_SUMMARY` (across the group buckets of a matrix
result). `OVERLAY_FORMULA` computes your own expression per cell or
group.

Inferential overlays join a request's `multiplicity` correction
([Multiple-Comparison Correction](multiplicity.md)) and read weights as
[Row Weighting](weighting.md) describes.

## Where the detail lives

- Each kind's params, accepted hosts and scopes: its skill
  (`op-overlay-<kind>`, through `pulse_skills_get`) and the
  [Overlays catalog](../guide/catalog/overlay.md). The manifest's
  `overlays[]` lists the same per instance.
- The framework (spec keys, reference families, payload shapes):
  the `overlay-system` skill.
- Reading a layer: [Reading overlay results](../guide/reading/overlay.md).
- Compose-level overlays across slots:
  [Compose Banners](compose-banner.md) and the `compose-requests` skill.
- Writing your own kind: [Adding an Overlay](../internals/adding-overlay.md).
