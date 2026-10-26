# Mix and share

## The question

<!-- docgen:preserve begin guide-composition-question -->
You have a categorical field — region, plan, answer option, the brands a respondent ticked — and you want to know its mix: how the rows split across the categories, what share each option has, and whether that mix differs from one group to another.

It sounds like:

- "What's our customer mix by region?"
- "What share of each region's customers converted?"
- "Which attributes go with which brand?"

Counts per category answer the first question; shares answer the rest. In a crosstab the share can run along the row (each row sums to one), down the column (each column sums to one) or over the whole table, and the three answer different questions, so choose the direction from the sentence you want to write.
<!-- docgen:preserve end guide-composition-question -->

## Pick your analysis

This question fits data shaped like:

- `category`: one categorical, bool or set field; `by`: optionally one categorical or bool field

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_FREQUENCY`](../catalog/aggregator.md#op-agg_frequency) | How many rows hold one chosen value of a field, such as the Yes answers, over all rows or per group. | basic |
| [`AGG_MODE_COUNT`](../catalog/aggregator.md#op-agg_mode_count) | How many rows share a field's most common value, over all rows or per group. | basic |
| [`AGG_RATIO`](../catalog/aggregator.md#op-agg_ratio) | Total of one field divided by the total of another, over all rows or per group, such as revenue per order. | basic |
| [`AGG_SET_FREQUENCY`](../catalog/aggregator.md#op-agg_set_frequency) | For a multi-select field, how many rows chose each option, over all rows or per group. | basic |
| [`AGG_SUM`](../catalog/aggregator.md#op-agg_sum) | Total of a numeric field, over all rows or per group. | basic |

### Groupers

| Use | In plain words | Level |
|---|---|---|
| [`GROUP_CATEGORY`](../catalog/grouper.md#op-group_category) | Splits the rows into one group per distinct value of a field, such as one group per region or per answer option. | basic |
| [`GROUP_SET_PER_ELEMENT`](../catalog/grouper.md#op-group_set_per_element) | Splits a multi-select answer into one group per option, counting each row once in every option it picked. | intermediate |
| [`GROUP_SET_VALUE`](../catalog/grouper.md#op-group_set_value) | Groups the rows by the exact combination of options picked in a multi-select answer, such as A only, A and B, or none. | intermediate |

### Window operators

| Use | In plain words | Level |
|---|---|---|
| [`WIN_SHARE`](../catalog/window.md#op-win_share) | Adds each row's share of its partition's total, or the running share down the ordered rows, such as the share of defaults caught so far. | basic |
| [`WIN_LIFT`](../catalog/window.md#op-win_lift) | Adds how many times its share of one column a row holds of another, such as a decile's share of defaults over its share of loans. | intermediate |

### Feature operators

| Use | In plain words | Level |
|---|---|---|
| [`FEAT_FREQUENCY_ENCODE`](../catalog/feature.md#op-feat_frequency_encode) | Replaces each category with the share of records that carry it, one number that says how common the category is. | intermediate |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_INDEX_VS_TOTAL`](../catalog/overlay.md#op-overlay_index_vs_total) | Each group's value on a grouped result as an index value against the sum over all groups (group / total x 100). | basic |
| [`OVERLAY_SHARE_OF_COL`](../catalog/overlay.md#op-overlay_share_of_col) | Each crosstab cell as a share of its column margin (0 to 1): the mix of rows within each column, as a 100% stacked column shows it. | basic |
| [`OVERLAY_SHARE_OF_ROW`](../catalog/overlay.md#op-overlay_share_of_row) | Each crosstab cell as a share of its row margin (0 to 1): the mix of columns within each row, as a 100% stacked bar shows it. | basic |
| [`OVERLAY_SHARE_OF_TOTAL`](../catalog/overlay.md#op-overlay_share_of_total) | Each crosstab cell or group of a grouped result as a share of the grand total (0 to 1): what part of the whole each piece makes up. | basic |
| [`OVERLAY_CORRESPONDENCE`](../catalog/overlay.md#op-overlay_correspondence) | Correspondence analysis of a crosstab: a perceptual map placing rows and columns in a few dimensions so similar profiles sit close together. | advanced |
| [`OVERLAY_RAKE`](../catalog/overlay.md#op-overlay_rake) | Rake a crosstab to known row and column totals: the fitted table, per-cell adjustment factors and whether the fit converged. | advanced |

## A worked example

**What share of customers in each region converted?**

The request (example `crosstab_row_normalize_conversion_rate`; `pulse examples show crosstab_row_normalize_conversion_rate` prints it):

```json
{
  "cohort": {
    "filename": "experiment.pulse",
    "data_dir": ".data"
  },
  "crosstab": {
    "rows": [
      {
        "type": "GROUP_CATEGORY",
        "field": "region"
      }
    ],
    "columns": [
      {
        "type": "GROUP_CATEGORY",
        "field": "converted"
      }
    ],
    "cell": {
      "type": "AGG_COUNT",
      "field": "id",
      "label": "n"
    },
    "margins": {
      "rows": true
    },
    "normalize": "row"
  },
  "return": {
    "precision": 4
  }
}
```

The result:

```json
{
  "metadata": {
    "total_rows": 400,
    "filtered_rows": 400,
    "cohort_file": "experiment.pulse"
  },
  "crosstab": {
    "shape": "matrix",
    "matrix": {
      "row_header": {
        "fields": [
          "region"
        ],
        "types": [
          "GROUP_CATEGORY"
        ]
      },
      "column_header": {
        "fields": [
          "converted"
        ],
        "types": [
          "GROUP_CATEGORY"
        ]
      },
      "row_keys": [
        [
          "east"
        ],
        [
          "north"
        ],
        [
          "south"
        ],
        [
          "west"
        ]
      ],
      "column_keys": [
        [
          "no"
        ],
        [
          "yes"
        ]
      ],
      "cells": [
        [
          {
            "value": 0.6458,
            "present": true
          },
          {
            "value": 0.3542,
            "present": true
          }
        ],
        [
          {
            "value": 0.5556,
            "present": true
          },
          {
            "value": 0.4444,
            "present": true
          }
        ],
        [
          {
            "value": 0.6146,
            "present": true
          },
          {
            "value": 0.3854,
            "present": true
          }
        ],
        [
          {
            "value": 0.6514,
            "present": true
          },
          {
            "value": 0.3486,
            "present": true
          }
        ]
      ],
      "row_margins": [
        {
          "value": 96,
          "present": true
        },
        {
          "value": 99,
          "present": true
        },
        {
          "value": 96,
          "present": true
        },
        {
          "value": 109,
          "present": true
        }
      ],
      "grand_total": {
        "present": false
      },
      "cell_label": "n",
      "normalize_applied": "row"
    }
  },
  "components": {
    "crosstab": {
      "cell_counts": [
        [
          62,
          34
        ],
        [
          55,
          44
        ],
        [
          59,
          37
        ],
        [
          71,
          38
        ]
      ],
      "cell_components": [
        [
          {
            "n": 62,
            "n_null": 0
          },
          {
            "n": 34,
            "n_null": 0
          }
        ],
        [
          {
            "n": 55,
            "n_null": 0
          },
          {
            "n": 44,
            "n_null": 0
          }
        ],
        [
          {
            "n": 59,
            "n_null": 0
          },
          {
            "n": 37,
            "n_null": 0
          }
        ],
        [
          {
            "n": 71,
            "n_null": 0
          },
          {
            "n": 38,
            "n_null": 0
          }
        ]
      ],
      "row_margin_counts": [
        96,
        99,
        96,
        109
      ],
      "row_margin_components": [
        {
          "n": 96,
          "n_null": 0
        },
        {
          "n": 99,
          "n_null": 0
        },
        {
          "n": 96,
          "n_null": 0
        },
        {
          "n": 109,
          "n_null": 0
        }
      ],
      "row_key_components": [
        {
          "count": 96,
          "key": "east",
          "label": "east"
        },
        {
          "count": 99,
          "key": "north",
          "label": "north"
        },
        {
          "count": 96,
          "key": "south",
          "label": "south"
        },
        {
          "count": 109,
          "key": "west",
          "label": "west"
        }
      ],
      "column_key_components": [
        {
          "count": 247,
          "key": "no",
          "label": "no"
        },
        {
          "count": 153,
          "key": "yes",
          "label": "yes"
        }
      ],
      "included_records": 400
    }
  },
  "returned": {
    "preset": "full",
    "digest": "rp1:2a47356146fe7626329ca77359f19495ac57bb6a599dfdd7658729be64597be6",
    "precision": 4
  }
}
```

Each row splits one region's customers into those who converted and those who did not, so the two cells of a row sum to one and the yes cell is that region's conversion rate. Most regions convert a little over a third of their customers; north converts noticeably more. With around a hundred customers per region a gap that size can easily arise by chance: shares describe the mix, they do not test it, so run a chi-square test across the regions before calling north different. The row margin keeps each region's raw count beside its shares.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

<!-- docgen:preserve begin guide-composition-pitfalls -->
- **Say what the share is a share of.** The same cell is a different share of its row, of its column and of the whole table; name the base every time you report one.
- **A share hides the count behind it.** A category with a striking share may rest on a handful of rows; keep the margin counts beside the shares and treat small bases with care.
- **A different mix is not yet a tested difference.** Shares describe the data; to say two groups' mixes really differ, add a chi-square test or a cell-level significance overlay.
- **Multi-select answers do not sum to one.** When a respondent can tick several options, option shares add up to more than the whole; read each as the share of rows choosing that option.
- **Missing values change the base.** Rows with no category are left out of the shares by default; check how many there were before comparing mixes.
- **Survey data may need a weight.** An unweighted mix describes the sample; when some groups are over- or under-represented, set the request's weight or rake the sample to known population totals.
<!-- docgen:preserve end guide-composition-pitfalls -->

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Benchmark against a reference](benchmark.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-formula-namespace`](../skills/overlay-formula-namespace.md), [`overlay-pairwise`](../skills/overlay-pairwise.md), [`overlay-reference-tests`](../skills/overlay-reference-tests.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`panel-n-sources`](../skills/panel-n-sources.md), [`window-design`](../skills/window-design.md), [`windows-first`](../skills/windows-first.md), [`op-agg-count`](../skills/op-agg-count.md), [`op-group-category`](../skills/op-group-category.md)

**From the command line:**

- `pulse recommend --intent composition --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
