# Multiple-Comparison Correction

Run many tests and some will come out significant by luck. A
`multiplicity` block adds corrected p-values beside the raw ones, so a
reader can tell a real hit from one the number of tests made likely.
Raw `p_value` / `reject_null` never change, and a request with no block
(the default) is byte-identical to before; `format_version` stays `"1.1"`.

```json
{
  "cohort": "survey",
  "tests": [
    {"type": "TEST_T", "field": "score", "split_by": "arm"},
    {"type": "TEST_PROP_Z", "field": "converted", "split_by": "arm"}
  ],
  "multiplicity": {"method": "holm"}
}
```

## The block

`multiplicity: {method, family, alpha}`, every key optional.

| Key | Values | Meaning |
|---|---|---|
| `method` | `none`, `bonferroni`, `holm`, `bh`, `by` | the procedure; matches R `p.adjust` (`bh` / `by` are R's `BH` / `BY`) |
| `family` | `layer`, `row`, `column`, `request`, `compose` | which p-values are corrected together |
| `alpha` | a number in (0, 1), default `0.05` | the level an overlay's `significant_adjusted` reads (a test uses its own `alpha`) |

`bonferroni` and `holm` control the family-wise error rate (the chance
of any false positive in the family); `bh` and `by` control the false
discovery rate (the expected share of false positives among the hits),
`by` under any dependence. `{"method": "none"}` is the explicit opt-out.

It rides on the `Request`, each `tests[]` / `post_tests[]` entry, each
overlay spec, the `ComposedRequest` and its overlay specs, and
engine-wide as `Options.DefaultMultiplicity`. Each key falls through on
its own: slot, request, `ComposedRequest`, engine default, none.

## Families

| Family | Corrects together | Where |
|---|---|---|
| `layer` | one overlay layer | overlays (the overlay default) |
| `row` / `column` | each row / column of one layer's matrix | overlays with a MATRIX payload |
| `request` | the request's tests, post-tests and `request`-family overlays | tests (the test default), request overlays |
| `compose` | every `compose` member across all slots and Compose-host layers | inside Compose |

An inherited family the surface does not offer takes the surface
default; an explicit one is refused (`PULSE_MULTIPLICITY_INVALID`):
`row` / `column` on a non-matrix kind, `compose` outside Compose,
`request` on a Compose-host overlay, `request` / `compose` on a facet
overlay. Members of one `request` / `compose` family that resolve to
different methods are `PULSE_MULTIPLICITY_CONFLICT`. `alpha` on a test's
own block is refused (the test's `alpha` is used), and so is an explicit
correction on Tukey HSD, which is already family-wise and never joins a
family.

`row` and `column` build one family per row or column index of the
layer's own matrix, purely by coordinate: a pairwise overlay's rows are
the compared groups, and every element of a panel cell joins its cell's
family. Families never cross layers.

## Outputs

All additive, present only when a correction ran:

- **Tests**: `p_adjusted`, `significant_adjusted` and
  `multiplicity {method, family, alpha, m}` beside `p_value`.
- **Overlay summaries**: `p_adjusted` / `significant_adjusted` beside the
  raw figure (`p_value`, or the `statistic` that the t / z-versus-reference
  kinds carry their p in).
- **Overlay matrices**: parallel `payload.p_adjusted` /
  `payload.significant_adjusted` matrices on identical headers and
  coordinates; a panel cell's vector maps element for element.
- **Overlay layers**: a `multiplicity` echo, with `m_per` for `row` /
  `column` (each family's size, aligned with the rows or columns).

`m` is the number of defined p-values corrected together. An undefined
(NaN) p is left out of `m` and shows `p_adjusted: null`. Field shapes:
[Payload JSON Schema](../contract/payload-schema.md).

## Where it runs

- **Process**: every arm (serial, streaming, fused or buffered crosstab,
  join, shard, parallel decode) corrects in one place after the result is
  built.
- **Compose**: after all slots and Compose-host overlays finish. Serial
  and `ComposeParallel` agree byte for byte; `--stream` emits slot rows
  only, with no corrections; a failed slot aborts before the fold.
- **ProcessChain**: each stage corrects its own request; no family spans
  stages, and the whole-chain overlays take no block.
- **Facet**: each overlay layer corrects on its own.

## Predict

`pulse predict` applies the same rules to a request without running it
and reports `p_values {total, uncorrected, basis, threshold}`: how many
inferential p-values the request emits and how many no block reaches.
`threshold` is 10 (`descriptor.MultiplicityTriggerThreshold`); an
`uncorrected` count at or above it is the cue to add a block. `basis` is
`exact`, `dictionary` (assumes every dictionary entry of a category axis
becomes a bucket, so it can over- or under-count) or `lower_bound` (an
axis it cannot size counts as one). Request predict reports it; facet
predict counts its inferential overlays (one p-value each) and compose
predict counts its own overlays, always as a `lower_bound`, beside each
slot's advisories. Chain predict reports none.

## Feature profiles

The block is the feature `capability:multiplicity`. On an instance that
hides it, the key is refused as an unknown field, it is absent from the
payload schema, and predict omits `p_values`. See
[Feature Profiles](feature-profiles.md).

## Not corrected

Regression coefficient p-values are out of scope. An extension `TEST_*`
joins families like a built-in
([Extension Points](../internals/extension-points.md)).
