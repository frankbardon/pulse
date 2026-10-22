# Measure a construct

## The question

<!-- docgen:preserve begin guide-measure_construct-question -->
You have several questions or measures meant to capture one underlying idea — engagement, satisfaction, trust — and you want to know whether they really hang together, which items belong, and how to combine them into one score per row.

It sounds like:

- "Do these six engagement questions measure one thing?"
- "Combine these items into a satisfaction score."
- "Which question in the battery doesn't fit with the rest?"

Start with a reliability analysis: Cronbach's alpha and McDonald's omega say how consistently the items move together, and per-item figures (item-total correlation, alpha if deleted) point at an item that does not belong. To ask how many ideas the items capture, run a principal-component or factor analysis. Then add a scale score, a component score or a factor score to every row.
<!-- docgen:preserve end guide-measure_construct-question -->

## Pick your analysis

This question fits data shaped like:

- `items`: 2 or more numeric fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_SCALE_SCORE`](../catalog/attribute.md#op-attr_scale_score) | Adds to every row its score on a rating battery: the average of its answered items or a prorated total, reverse-worded items flipped. | intermediate |
| [`ATTR_FACTOR_SCORE`](../catalog/attribute.md#op-attr_factor_score) | Adds to every row its score on one factor behind several related numeric fields: an estimate of the hidden trait they measure together. | advanced |
| [`ATTR_PC_SCORE`](../catalog/attribute.md#op-attr_pc_score) | Adds to every row its score on one principal component of several related numeric fields: one number standing in for what they share. | advanced |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_BARTLETT_SPHERICITY`](../catalog/test.md#op-test_bartlett_sphericity) | Checks whether a set of numeric fields are related at all, the first check before summarising them as a few underlying dimensions. | advanced |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_RELIABILITY`](../catalog/matrix.md#op-mat_reliability) | How consistently a set of rating items measures one thing, before you add them up into a score, with a check of how each item fits the rest. | intermediate |
| [`MAT_FACTOR`](../catalog/matrix.md#op-mat_factor) | Which hidden qualities a set of related measures reflects, and how strongly each measure is tied to each, before building scale scores. | advanced |
| [`MAT_PCA`](../catalog/matrix.md#op-mat_pca) | How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many. | advanced |
| [`MAT_SEM`](../catalog/matrix.md#op-mat_sem) | Whether a theory of how measures hang together holds: the hidden qualities they reflect, how those drive each other, how well it fits. | advanced |

## A worked example

**Do these six engagement questions measure one thing well enough to add up into a single score?**

The request (example `scale_reliability`; `pulse examples show scale_reliability` prints it):

```json
{
  "cohort": {
    "filename": "battery.pulse",
    "data_dir": ".data"
  },
  "matrices": [
    {
      "type": "MAT_RELIABILITY",
      "name": "battery",
      "fields": [
        "bat_energy",
        "bat_proud",
        "bat_absorbed",
        "bat_recommend",
        "bat_time_flies",
        "bat_dread"
      ],
      "params": {
        "reverse": [
          "bat_dread"
        ],
        "scale_min": 1,
        "scale_max": 5
      }
    }
  ],
  "return": {
    "precision": 4
  }
}
```

The result:

```json
{
  "metadata": {
    "total_rows": 300,
    "filtered_rows": 300,
    "cohort_file": "battery.pulse"
  },
  "matrices": [
    {
      "name": "battery",
      "type": "MAT_RELIABILITY",
      "primary": {
        "kind": "square_symmetric",
        "encoding": "full",
        "row_keys": [
          "bat_energy",
          "bat_proud",
          "bat_absorbed",
          "bat_recommend",
          "bat_time_flies",
          "bat_dread"
        ],
        "column_keys": [
          "bat_energy",
          "bat_proud",
          "bat_absorbed",
          "bat_recommend",
          "bat_time_flies",
          "bat_dread"
        ],
        "values": [
          [
            1,
            0.5724,
            0.5089,
            0.3953,
            0.4718,
            0.48
          ],
          [
            0.5724,
            1,
            0.4519,
            0.4365,
            0.4602,
            0.4461
          ],
          [
            0.5089,
            0.4519,
            1,
            0.3562,
            0.3644,
            0.4581
          ],
          [
            0.3953,
            0.4365,
            0.3562,
            1,
            0.373,
            0.3825
          ],
          [
            0.4718,
            0.4602,
            0.3644,
            0.373,
            1,
            0.3644
          ],
          [
            0.48,
            0.4461,
            0.4581,
            0.3825,
            0.3644,
            1
          ]
        ]
      },
      "vectors": {
        "alpha_if_deleted": [
          0.7762,
          0.7806,
          0.7963,
          0.8086,
          0.8031,
          0.7967
        ],
        "item_mean": [
          2.933,
          2.96,
          3.003,
          2.997,
          2.913,
          2.977
        ],
        "item_sd": [
          0.9961,
          1.004,
          1.01,
          0.9763,
          0.9912,
          0.976
        ],
        "item_total_r": [
          0.6694,
          0.649,
          0.5767,
          0.5165,
          0.5442,
          0.5743
        ]
      },
      "scalars": {
        "alpha": 0.8221,
        "alpha_standardized": 0.8219,
        "mean_inter_item_r": 0.4348,
        "omega": 0.8236
      }
    }
  ],
  "components": {
    "run": {
      "total_records": 300,
      "filtered_records": 300,
      "null_records": 0
    },
    "matrices": [
      {
        "name": "battery",
        "type": "MAT_RELIABILITY",
        "n": 300,
        "n_null": 0,
        "n_listwise_dropped": 0,
        "operator": {
          "converged": true,
          "iterations": 19
        }
      }
    ]
  },
  "returned": {
    "preset": "full",
    "digest": "rp1:2a47356146fe7626329ca77359f19495ac57bb6a599dfdd7658729be64597be6",
    "precision": 4
  }
}
```

Yes. Cronbach's alpha and McDonald's omega both come out in the range usually read as good reliability, and every alpha if deleted sits below the full-scale alpha, so dropping any one item would make the scale less reliable, not more. Each item's corrected item-total correlation is moderate to strong: every question moves with the rest. The dread item is worded the other way round, so the request reverses it before scoring; leave that out and the item works against the others and alpha falls sharply. Items that move together are not yet one dimension, which a principal-component or factor analysis checks, and a consistent score is not by itself a score of what its name claims.

## How to read the result

### `MAT_RELIABILITY`

**`scalars.alpha`**

Cronbach's alpha: how consistently the items measure one shared quality, from the share of the summed score's spread that the items share. 1 means the items move in lockstep; it can fall below 0 when items pull against each other, which usually means a reverse-worded item was not reversed.

**Bands** (convention: George & Mallery (2003); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.5 | unacceptable |
| from 0.5 to below 0.6 | poor |
| from 0.6 to below 0.7 | questionable |
| from 0.7 to below 0.8 | acceptable |
| from 0.8 to below 0.9 | good |
| 0.9 and above | excellent |

**Caveats:**

- Alpha rises with the number of items, so a long battery can reach a high alpha with weakly related items.
- A high alpha does not show the items measure only one thing.
- Alpha assumes every item is equally tied to the shared quality; when the ties differ it understates reliability, which omega allows for.

**`scalars.alpha_standardized`**

Alpha computed on the items' correlations instead of their raw spreads: the alpha the battery would have if every item were first put on the same scale.

**Bands** (convention: George & Mallery (2003); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.5 | unacceptable |
| from 0.5 to below 0.6 | poor |
| from 0.6 to below 0.7 | questionable |
| from 0.7 to below 0.8 | acceptable |
| from 0.8 to below 0.9 | good |
| 0.9 and above | excellent |

**Caveats:**

- It differs from alpha when the items' spreads differ; report the one that matches how the score is built (raw sums or standardised items).

**`scalars.omega`**

McDonald's omega: the share of the summed score's spread due to the one shared quality, from a one-factor fit that lets each item's tie to it differ. Read on the same 0 to 1 scale as alpha; it is usually at least as high.

**Caveats:**

- No published convention bands omega; it is not read against the alpha bands.
- Null with a warning when the battery has 2 items, when the fit puts an item's leftover spread at or below zero (a Heywood case), or when a pairwise table is inconsistent and params.repair is not set. When the fit does not converge, omega is kept as the last iterate's value and a warning says so; treat it as provisional.
- The fit assumes one shared quality; when the items reflect several, omega from one factor misstates reliability.

**`scalars.mean_inter_item_r`**

The average correlation among the items, the strength of the typical pair; standardized alpha is built from it and the item count.

**Caveats:**

- An average hides spread: one item unrelated to the rest pulls it down; check the table and item_total_r.
- Like any correlation it shows the items move together, not that one causes another.

More on reading it: [MAT_RELIABILITY](../reading/matrix.md#op-mat_reliability).

## Pitfalls

<!-- docgen:preserve begin guide-measure_construct-pitfalls -->
- **Reverse-worded items must be flipped first.** An item worded the other way round ("I dread starting work") runs against the rest; leave it unflipped and it drags alpha down sharply. Name it in the reliability parameters with the scale's minimum and maximum.
- **A high alpha is not one dimension.** Alpha rises with the number of items and with any shared correlation; two related ideas can still give a high alpha. Use a principal-component or factor analysis to see how many dimensions there are.
- **Reliable is not valid.** Items that move together measure something consistently; whether it is the idea the score's name claims is a separate question the figures cannot answer.
- **Drop items for reasons, not only figures.** An alpha if deleted above the full-scale alpha marks a weak item; check its wording before removing it, because a narrower scale can be more reliable and less useful.
- **Missing answers change the score.** Decide how many items a row must answer to get a score, and treat a row with too few as missing rather than zero.
- **A warning about a factor fit means the figures are not usable.** A Heywood case or a fit that did not converge usually means an item does not belong with the rest, or there are too few items; read the warning before the omega.
<!-- docgen:preserve end guide-measure_construct-pitfalls -->

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Relationship between measures](relationship.md)

**Skills:** [`intents`](../skills/intents.md), [`attribute-composition`](../skills/attribute-composition.md), [`statistical-testing`](../skills/statistical-testing.md), [`op-mat-reliability`](../skills/op-mat-reliability.md)

**From the command line:**

- `pulse recommend --intent measure_construct --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
