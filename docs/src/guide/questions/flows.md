# Flows between states

## The question

<!-- docgen:preserve begin guide-flows-question -->
You have rows that record a move from one state to another — a customer's plan last month and this month, a member's tier before and after, a voter's choice in two waves — and you want to know who stays, who switches, where the switchers go, and what the mix will look like if the moves continue.

It sounds like:

- "Where do customers move between plans?"
- "Who switched from silver to gold?"
- "What share of each tier did we keep?"

Build a from-by-to crosstab of counts: each row is a starting state, each column an ending state, so a row read across shows where that state's members went. A retention overlay turns each row into stay, switch and churn rates; a Markov overlay turns the table into the chance of each move (transition probabilities) and projects the mix forward; a symmetry overlay tests whether moves between two states balance out. When the data is one row per snapshot rather than one per move, a previous-state attribute adds each row's earlier state first.
<!-- docgen:preserve end guide-flows-question -->

## Pick your analysis

This question fits data shaped like:

- `from`: one categorical or bool field; `to`: one categorical or bool field

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_PREVIOUS_STATE`](../catalog/attribute.md#op-attr_previous_state) | Adds to each row of a long panel the state the same subject held at its previous date, so moves can be counted from-state by to-state. | intermediate |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_RETENTION`](../catalog/overlay.md#op-overlay_retention) | Retention rates of a from-state by to-state crosstab: for each starting state, the share that stayed, switched or churned. | basic |
| [`OVERLAY_SYMMETRY`](../catalog/overlay.md#op-overlay_symmetry) | Test of symmetry on a before-by-after crosstab of the same units: are moves between two states as common one way as back? | intermediate |
| [`OVERLAY_MARKOV`](../catalog/overlay.md#op-overlay_markov) | Markov chain of a from-state by to-state crosstab: transition probabilities, n-step projections, steady state and time to absorption. | advanced |

## A worked example

**Which tiers hold on to their members, and where do the leavers go?**

The request (example `crosstab-retention`; `pulse examples show crosstab-retention` prints it):

```json
{
  "cohort": {
    "filename": "tier_migrations.pulse",
    "data_dir": ".data"
  },
  "crosstab": {
    "rows": [
      {
        "type": "GROUP_CATEGORY",
        "field": "from_tier"
      }
    ],
    "columns": [
      {
        "type": "GROUP_CATEGORY",
        "field": "to_tier"
      }
    ],
    "cell": {
      "type": "AGG_COUNT",
      "field": "id",
      "label": "n"
    },
    "margins": {
      "rows": true,
      "columns": true,
      "grand": true
    }
  },
  "overlays": [
    {
      "name": "retention",
      "kind": "OVERLAY_RETENTION",
      "scope": "row",
      "params": {
        "absorbing": [
          "churned"
        ]
      }
    }
  ],
  "return": {
    "preset": "standard",
    "precision": 4
  }
}
```

The result:

```json
{
  "metadata": {
    "total_rows": 154,
    "filtered_rows": 154,
    "cohort_file": "tier_migrations.pulse"
  },
  "crosstab": {
    "shape": "matrix",
    "matrix": {
      "row_header": {
        "fields": [
          "from_tier"
        ],
        "types": [
          "GROUP_CATEGORY"
        ]
      },
      "column_header": {
        "fields": [
          "to_tier"
        ],
        "types": [
          "GROUP_CATEGORY"
        ]
      },
      "row_keys": [
        [
          "bronze"
        ],
        [
          "churned"
        ],
        [
          "gold"
        ],
        [
          "silver"
        ]
      ],
      "column_keys": [
        [
          "bronze"
        ],
        [
          "churned"
        ],
        [
          "gold"
        ],
        [
          "silver"
        ]
      ],
      "cells": [
        [
          {
            "value": 30,
            "present": true
          },
          {
            "value": 6,
            "present": true
          },
          {
            "value": 2,
            "present": true
          },
          {
            "value": 8,
            "present": true
          }
        ],
        [
          {
            "present": false
          },
          {
            "value": 10,
            "present": true
          },
          {
            "present": false
          },
          {
            "present": false
          }
        ],
        [
          {
            "value": 1,
            "present": true
          },
          {
            "value": 3,
            "present": true
          },
          {
            "value": 38,
            "present": true
          },
          {
            "value": 5,
            "present": true
          }
        ],
        [
          {
            "value": 6,
            "present": true
          },
          {
            "value": 4,
            "present": true
          },
          {
            "value": 7,
            "present": true
          },
          {
            "value": 34,
            "present": true
          }
        ]
      ],
      "row_margins": [
        {
          "value": 46,
          "present": true
        },
        {
          "value": 10,
          "present": true
        },
        {
          "value": 47,
          "present": true
        },
        {
          "value": 51,
          "present": true
        }
      ],
      "column_margins": [
        {
          "value": 37,
          "present": true
        },
        {
          "value": 23,
          "present": true
        },
        {
          "value": 47,
          "present": true
        },
        {
          "value": 47,
          "present": true
        }
      ],
      "grand_total": {
        "value": 154,
        "present": true
      },
      "cell_label": "n",
      "normalize_applied": "none"
    }
  },
  "overlays": [
    {
      "name": "retention",
      "kind": "OVERLAY_RETENTION",
      "scope": "row",
      "ref": {},
      "payload": {
        "shape": "series",
        "series": {
          "entries": [
            {
              "key": [
                "bronze"
              ],
              "summary": {
                "statistic": 0.6522,
                "parameters": {
                  "churn": 0.1304,
                  "from_total": 46,
                  "switch": 0.2174
                }
              }
            },
            {
              "key": [
                "gold"
              ],
              "summary": {
                "statistic": 0.8085,
                "parameters": {
                  "churn": 0.06383,
                  "from_total": 47,
                  "switch": 0.1277
                }
              }
            },
            {
              "key": [
                "silver"
              ],
              "summary": {
                "statistic": 0.6667,
                "parameters": {
                  "churn": 0.07843,
                  "from_total": 51,
                  "switch": 0.2549
                }
              }
            }
          ]
        }
      },
      "summary": {
        "parameters": {
          "churn": 0.09028,
          "states": 3,
          "stay": 0.7083,
          "switch": 0.2014,
          "total": 144
        }
      }
    }
  ],
  "returned": {
    "preset": "standard",
    "digest": "rp1:d1fda7b27cbd73c09073bf49c8d90fbde3de523876628808c3b5ee17a5825705",
    "precision": 4
  }
}
```

Gold holds on best: about four in five gold members stayed gold, and gold also loses the fewest to churn. Bronze and silver each keep around two in three. Bronze loses the most members to churn, while silver's leavers mostly switch to another tier rather than churn. Each row of the table is a starting tier and each column the tier a member ended in, so read a tier's row across to see where its members went. Churned is named absorbing, so it is not reported as a starting tier and every move into it counts as churn rather than a switch. Each rate rests on around fifty moves, so treat small gaps between tiers with caution.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

<!-- docgen:preserve begin guide-flows-pitfalls -->
- **Read along the row, not down the column.** A row shows where one starting state's members went; a column shows where an ending state's members came from. They answer different questions.
- **Name the states nobody leaves.** A state such as churned has moves in but none out; mark it absorbing so it is counted as churn and not reported as a starting state.
- **The first snapshot has no move.** In snapshot data each customer's first row has no earlier state and reaches no cell; the table counts moves, not customers.
- **A projection assumes the moves stay the same.** A Markov projection repeats the observed rates step after step; it is a what-if under steady conditions, not a forecast.
- **Small rows give unsteady rates.** A starting state with few members can show a striking stay or churn rate by luck; keep the row counts beside the rates.
- **Balanced moves are not no movement.** A symmetry test that does not reject says moves between two states roughly cancel out; plenty of members may still have switched.
<!-- docgen:preserve end guide-flows-pitfalls -->

## Next steps

**Skills:** [`intents`](../skills/intents.md), [`attribute-composition`](../skills/attribute-composition.md), [`overlay-formula-namespace`](../skills/overlay-formula-namespace.md), [`overlay-pairwise`](../skills/overlay-pairwise.md), [`overlay-reference-tests`](../skills/overlay-reference-tests.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`panel-n-sources`](../skills/panel-n-sources.md), [`op-agg-count`](../skills/op-agg-count.md), [`op-group-category`](../skills/op-group-category.md)

**From the command line:**

- `pulse recommend --intent flows --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
