# Segment into groups

## The question

<!-- docgen:preserve begin guide-segment-question -->
You have rows described by one or more measures — customers by age and income, products by price and sales, respondents by their answers — and you want to sort them into groups: tiers along one measure, or clusters of rows that look alike across several.

It sounds like:

- "Are there natural groups of customers?"
- "Split income into four equal-sized tiers."
- "Which customers fall into the high-value segment?"

For tiers on one measure, cut it into ranges: equal-sized groups (quantiles), equal-width bands, or breakpoints you choose. For groups across several measures, a clustering grouper such as k-means places every row with the nearest of a set number of centres. A filter keeps one segment you have already defined, for example by a rule on several fields.
<!-- docgen:preserve end guide-segment-question -->

## Pick your analysis

This question fits data shaped like:

- `measures`: 1 or more numeric fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Groupers

| Use | In plain words | Level |
|---|---|---|
| [`GROUP_RANGE`](../catalog/grouper.md#op-group_range) | Splits a number into bands of equal width, such as ages 20-30 and 30-40, to see how values spread or to compare bands. | basic |
| [`GROUP_ROUNDED`](../catalog/grouper.md#op-group_rounded) | Groups a number by rounding it down to a multiple of a step, such as 23 and 27 both to 20, naming each group by that multiple. | basic |
| [`GROUP_NUMERIC_RANGES`](../catalog/grouper.md#op-group_numeric_ranges) | Labels each row with the named number range its value falls in, such as a train/holdout split on a row index or custom score bands. | intermediate |
| [`GROUP_QUANTILE`](../catalog/grouper.md#op-group_quantile) | Splits the rows into equal-sized groups by rank of a number, such as quartiles or deciles of spend, to compare top and bottom. | intermediate |
| [`GROUP_KMEANS`](../catalog/grouper.md#op-group_kmeans) | Finds k segments of rows that are alike across several numbers at once, such as customer types by spend, visits and tenure. | advanced |

### Feature operators

| Use | In plain words | Level |
|---|---|---|
| [`FEAT_BUCKETIZE`](../catalog/feature.md#op-feat_bucketize) | Puts each value into an ordered bin, using cut points you give or bins holding about equal numbers of records, and adds the bin number. | basic |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_DISTANCE`](../catalog/matrix.md#op-mat_distance) | How far apart segments are across several numbers at once: the gap between each pair of segment centres. | advanced |

## A worked example

**Do customers fall into natural groups by age and income?**

The request (example `group_kmeans`; `pulse examples show group_kmeans` prints it):

```json cohort=customers
{
  "aggregations": [
    {
      "type": "AGG_COUNT",
      "field": "age",
      "label": "customers"
    },
    {
      "type": "AGG_AVERAGE",
      "field": "age",
      "label": "mean_age"
    },
    {
      "type": "AGG_AVERAGE",
      "field": "income",
      "label": "mean_income"
    }
  ],
  "cohort": {
    "filename": "customers.pulse",
    "data_dir": ".data"
  },
  "groups": [
    {
      "type": "GROUP_KMEANS",
      "fields": [
        "age",
        "income"
      ],
      "params": {
        "k": 3,
        "seed": 7
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
  "data": [
    {
      "GROUP_KMEANS_age": "cluster_1",
      "customers": 86,
      "mean_age": 33.29,
      "mean_income": 3.304e+04
    },
    {
      "GROUP_KMEANS_age": "cluster_2",
      "customers": 85,
      "mean_age": 64.35,
      "mean_income": 3.664e+04
    },
    {
      "GROUP_KMEANS_age": "cluster_3",
      "customers": 29,
      "mean_age": 48.52,
      "mean_income": 9.655e+04
    }
  ],
  "metadata": {
    "total_rows": 200,
    "filtered_rows": 200,
    "cohort_file": "customers.pulse"
  },
  "returned": {
    "preset": "standard",
    "digest": "rp1:3740c6cf7d1b2603620bc368cb97f40488ddae2bc40c4df12447e16826612255",
    "precision": 4
  }
}
```

K-means splits them into the three groups it was asked for: a large younger group, an older group on a similar typical income, and a small group whose average income is roughly three times the others'. Read that as a convenient split rather than a discovery. The younger and older groups look like two halves of one continuous age range cut near the middle, and the high-income group is the long upper tail of incomes rather than a separate population. The algorithm always returns as many groups as you ask for, whether or not the data has them; check the share of the spread the groups explain (`between_ss_ratio` in the grouper's components, which the full preset shows), try a different number of groups, and ask what each group means for a decision before naming segments. Age and income are standardised first, so both count equally despite their different units. Each row's cluster label sits in a column named after the grouper and its first field.

Another worked example:

**Which brands do customers see as alike, and what separates them?**

The request (example `perceptual_map`; `pulse examples show perceptual_map` prints it):

```json cohort=brand_ratings
{
  "attributes": [
    {
      "type": "ATTR_PC_SCORE",
      "label": "map_x",
      "fields": [
        "quality",
        "premium",
        "value",
        "modern",
        "innovative",
        "heritage"
      ],
      "params": {
        "component": 1
      }
    },
    {
      "type": "ATTR_PC_SCORE",
      "label": "map_y",
      "fields": [
        "quality",
        "premium",
        "value",
        "modern",
        "innovative",
        "heritage"
      ],
      "params": {
        "component": 2
      }
    }
  ],
  "cohort": {
    "filename": "brand_ratings.pulse",
    "data_dir": ".data"
  },
  "matrices": [
    {
      "type": "MAT_PCA",
      "name": "axes",
      "fields": [
        "quality",
        "premium",
        "value",
        "modern",
        "innovative",
        "heritage"
      ],
      "params": {
        "components": 2
      }
    },
    {
      "type": "MAT_DISTANCE",
      "name": "brand_distance",
      "fields": [
        "map_x",
        "map_y"
      ],
      "params": {
        "between": {
          "type": "GROUP_CATEGORY",
          "field": "brand"
        }
      }
    },
    {
      "type": "MAT_DISTANCE",
      "name": "rating_distance",
      "fields": [
        "quality",
        "premium",
        "value",
        "modern",
        "innovative",
        "heritage"
      ],
      "params": {
        "between": {
          "type": "GROUP_CATEGORY",
          "field": "brand"
        },
        "standardize": true
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
    "total_rows": 600,
    "filtered_rows": 600,
    "cohort_file": "brand_ratings.pulse"
  },
  "matrices": [
    {
      "name": "axes",
      "type": "MAT_PCA",
      "primary": {
        "kind": "rectangular",
        "encoding": "full",
        "row_keys": [
          "quality",
          "premium",
          "value",
          "modern",
          "innovative",
          "heritage"
        ],
        "column_keys": [
          "PC1",
          "PC2"
        ],
        "values": [
          [
            0.9008,
            -0.07459
          ],
          [
            0.911,
            -0.05469
          ],
          [
            -0.873,
            0.08528
          ],
          [
            0.09919,
            0.8086
          ],
          [
            0.04737,
            0.7829
          ],
          [
            -0.09551,
            -0.7766
          ]
        ]
      },
      "auxiliary": {
        "eigenvectors": {
          "kind": "rectangular",
          "encoding": "full",
          "row_keys": [
            "quality",
            "premium",
            "value",
            "modern",
            "innovative",
            "heritage"
          ],
          "column_keys": [
            "PC1",
            "PC2"
          ],
          "values": [
            [
              0.5785,
              -0.05432
            ],
            [
              0.5851,
              -0.03983
            ],
            [
              -0.5606,
              0.0621
            ],
            [
              0.0637,
              0.5888
            ],
            [
              0.03042,
              0.5701
            ],
            [
              -0.06134,
              -0.5656
            ]
          ]
        }
      },
      "vectors": {
        "communalities": [
          0.817,
          0.8329,
          0.7694,
          0.6636,
          0.6152,
          0.6123
        ],
        "cumulative": [
          0.4041,
          0.7184,
          0.8172,
          0.9037,
          0.9604,
          1
        ],
        "eigenvalues": [
          2.425,
          1.886,
          0.5928,
          0.5191,
          0.3402,
          0.2375
        ],
        "explained_variance": [
          0.4041,
          0.3143,
          0.0988,
          0.08651,
          0.05669,
          0.03959
        ],
        "kmo_msa": [
          0.7203,
          0.7004,
          0.7925,
          0.65,
          0.6699,
          0.6864
        ]
      },
      "scalars": {
        "bartlett_chisq": 1296,
        "bartlett_df": 15,
        "bartlett_p": 3.225e-267,
        "components_retained": 2,
        "kmo": 0.7138
      }
    },
    {
      "name": "brand_distance",
      "type": "MAT_DISTANCE",
      "primary": {
        "kind": "square_symmetric",
        "encoding": "full",
        "row_keys": [
          "brand_a",
          "brand_b",
          "brand_c",
          "brand_d",
          "brand_e"
        ],
        "column_keys": [
          "brand_a",
          "brand_b",
          "brand_c",
          "brand_d",
          "brand_e"
        ],
        "values": [
          [
            0,
            1.385,
            1.924,
            2.293,
            1.211
          ],
          [
            1.385,
            0,
            2.245,
            1.775,
            1.063
          ],
          [
            1.924,
            2.245,
            0,
            1.251,
            1.183
          ],
          [
            2.293,
            1.775,
            1.251,
            0,
            1.085
          ],
          [
            1.211,
            1.063,
            1.183,
            1.085,
            0
          ]
        ]
      },
      "auxiliary": {
        "centroids": {
          "kind": "rectangular",
          "encoding": "full",
          "row_keys": [
            "brand_a",
            "brand_b",
            "brand_c",
            "brand_d",
            "brand_e"
          ],
          "column_keys": [
            "map_x",
            "map_y"
          ],
          "values": [
            [
              1.035,
              0.6046
            ],
            [
              0.7985,
              -0.76
            ],
            [
              -0.8856,
              0.7245
            ],
            [
              -0.9607,
              -0.5243
            ],
            [
              0.01273,
              -0.04479
            ]
          ]
        }
      },
      "vectors": {
        "counts": [
          120,
          120,
          120,
          120,
          120
        ],
        "sizes": [
          120,
          120,
          120,
          120,
          120
        ]
      }
    },
    {
      "name": "rating_distance",
      "type": "MAT_DISTANCE",
      "primary": {
        "kind": "square_symmetric",
        "encoding": "full",
        "row_keys": [
          "brand_a",
          "brand_b",
          "brand_c",
          "brand_d",
          "brand_e"
        ],
        "column_keys": [
          "brand_a",
          "brand_b",
          "brand_c",
          "brand_d",
          "brand_e"
        ],
        "values": [
          [
            0,
            1.92,
            3,
            3.477,
            1.83
          ],
          [
            1.92,
            0,
            3.327,
            2.763,
            1.571
          ],
          [
            3,
            3.327,
            0,
            1.726,
            1.763
          ],
          [
            3.477,
            2.763,
            1.726,
            0,
            1.66
          ],
          [
            1.83,
            1.571,
            1.763,
            1.66,
            0
          ]
        ]
      },
      "auxiliary": {
        "centroids": {
          "kind": "rectangular",
          "encoding": "full",
          "row_keys": [
            "brand_a",
            "brand_b",
            "brand_c",
            "brand_d",
            "brand_e"
          ],
          "column_keys": [
            "quality",
            "premium",
            "value",
            "modern",
            "innovative",
            "heritage"
          ],
          "values": [
            [
              5.233,
              5.508,
              3.042,
              4.558,
              4.575,
              3.458
            ],
            [
              5.15,
              5.225,
              3.167,
              3.55,
              3.383,
              4.525
            ],
            [
              2.708,
              2.683,
              4.908,
              4.542,
              4.55,
              3.592
            ],
            [
              2.858,
              2.65,
              4.933,
              3.467,
              3.517,
              4.442
            ],
            [
              4.008,
              4.142,
              4.067,
              4.008,
              3.925,
              4.033
            ]
          ]
        },
        "centroids_standardized": {
          "kind": "rectangular",
          "encoding": "full",
          "row_keys": [
            "brand_a",
            "brand_b",
            "brand_c",
            "brand_d",
            "brand_e"
          ],
          "column_keys": [
            "quality",
            "premium",
            "value",
            "modern",
            "innovative",
            "heritage"
          ],
          "values": [
            [
              0.8648,
              0.9566,
              -0.8301,
              0.5223,
              0.5875,
              -0.5737
            ],
            [
              0.8067,
              0.7718,
              -0.7244,
              -0.4652,
              -0.6092,
              0.5355
            ],
            [
              -0.8938,
              -0.886,
              0.7484,
              0.506,
              0.5624,
              -0.435
            ],
            [
              -0.7893,
              -0.9077,
              0.7695,
              -0.5468,
              -0.4753,
              0.4489
            ],
            [
              0.01161,
              0.06523,
              0.03664,
              -0.01632,
              -0.06528,
              0.02426
            ]
          ]
        }
      },
      "vectors": {
        "counts": [
          120,
          120,
          120,
          120,
          120
        ],
        "sizes": [
          120,
          120,
          120,
          120,
          120
        ]
      }
    }
  ],
  "components": {
    "run": {
      "total_records": 600,
      "filtered_records": 600,
      "null_records": 0
    },
    "matrices": [
      {
        "name": "axes",
        "type": "MAT_PCA",
        "n": 600,
        "n_null": 0,
        "n_listwise_dropped": 0
      },
      {
        "name": "brand_distance",
        "type": "MAT_DISTANCE",
        "n": 600,
        "n_null": 0,
        "n_listwise_dropped": 0,
        "operator": {
          "buckets": 5,
          "metric": "euclidean",
          "n_null_key": 0,
          "standardize": false
        }
      },
      {
        "name": "rating_distance",
        "type": "MAT_DISTANCE",
        "n": 600,
        "n_null": 0,
        "n_listwise_dropped": 0,
        "operator": {
          "buckets": 5,
          "metric": "euclidean",
          "n_null_key": 0,
          "standardize": true
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

Two axes carry most of the picture. The first is premium versus budget: quality and premium load one way, value the other. The second is modern versus traditional: modern and innovative against heritage. The four outer brands sit one to a quadrant with the fifth near the middle. The closest pairs share a side of the price axis, the two premium brands and the two budget ones, so price separates customers' views more than style does; brands on opposite corners are the furthest apart. The distance grid over the six ratings themselves ranks those corner pairs the same way, so the flat picture does not mislead here. The middle brand is about equally close to all four and owns no position of its own. Distances between centres carry no p-value, so treat a small gap as no gap until a test says otherwise.

## How to read the result

### `ATTR_PC_SCORE`

**`value`**

The row's score on the chosen principal component of the fields, standardized so the fitted rows average 0 with a standard deviation of 1: 0 is an average row, +1 is one standard deviation toward the end where the fields with positive loadings are high.

**Caveats:**

- The sign is a convention (MAT_PCA's: each component's largest loading is positive), not a meaning: check MAT_PCA's loadings on the same fields before calling a high score good or bad.
- A component summarises the spread the fields share; it does not say why they move together, and a later component carries less of their variance and is noisier.
- On the correlation every field counts equally; on the covariance a field with a large spread dominates the score.
- The fit is over every filtered row (or the fit_on subset), not the row's group, so scores are comparable across groups. Under a row weight the fit is weighted; a row with no usable weight is left out of the fit but still scored.
- A row missing any field reads null, and every row reads null, with a warning, when the fit has no more complete rows than fields or a field is constant or a combination of others.

More on reading it: [ATTR_PC_SCORE](../reading/descriptive.md#op-attr_pc_score).

### `MAT_PCA`

**`primary.values`**

Each cell is a loading: how strongly the row's measure is tied to the column's component, the correlation between the two on a correlation input. A component is named by the measures with the largest loadings; components are listed from the one that summarises the most spread down.

**Sign:**

- `+`: the measure rises with the component
- `-`: the measure falls as the component rises; a component's overall sign is a convention (its largest loading is made positive, or under a rotation its loadings sum positive)

**Caveats:**

- Unrotated, a measure loading on several components at once is common and does not mean it measures several things; params.rotation varimax redistributes the kept variance into a cleaner pattern without changing the communalities.
- Components summarise the measures' total spread; they do not show that an underlying cause exists.
- On a covariance input the loadings are in the measures' own units, so they are not comparable across measures.

**`scalars.kmo`**

The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a component summary is more worthwhile.

**Bands** (convention: Kaiser (1974); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.5 | unacceptable |
| from 0.5 to below 0.6 | miserable |
| from 0.6 to below 0.7 | mediocre |
| from 0.7 to below 0.8 | middling |
| from 0.8 to below 0.9 | meritorious |
| 0.9 and above | marvelous |

**Caveats:**

- Null when the correlation table is singular (a measure is an exact combination of others).
- The per-measure version of this reading is also reported; a measure with a low value fits the set poorly.

**`scalars.bartlett_p`**

Follows the shared reading: see [Reading a p-value](../reading/test.md#shared-p-value).

**`scalars.bartlett_chisq`**

Bartlett's test statistic: how far the correlation table is from one in which no measure is related to any other. Large values mean the measures are related enough to summarise.

**Caveats:**

- With many rows it is almost always large; read KMO for how worthwhile a summary is.
- It rests on the inference size: the rows (listwise), the summed weight or the effective sample size under a weight, or the smallest pair under pairwise deletion.

More on reading it: [MAT_PCA](../reading/matrix.md#op-mat_pca).

## Pitfalls

<!-- docgen:preserve begin guide-segment-pitfalls -->
- **Clustering always returns the number of groups you ask for.** It finds groups whether or not the data has them; a clean split can be a convenient cut through one continuous spread. Check how much of the spread the groups explain, and try a different number of groups.
- **A segment is only useful if it means something.** Describe each group in plain terms (younger, high income, recent buyers) and check that the difference matters for a decision before you name it.
- **Units decide what counts as near.** A measure in dollars swamps one in years unless the fields are standardised first; check that the clustering puts the measures on a common scale.
- **Quantile tiers have equal counts, not equal widths.** The top tier of a skewed measure can span a far wider range than the others; read each tier's minimum and maximum.
- **Segments found and tested on the same data look better than they are.** If you then compare the segments on the measures that built them, they will differ by construction; test them on a different outcome or on fresh data.
- **Results can shift with the starting point.** K-means depends on its starting centres; fix the seed so the same data gives the same groups, and treat a segment that moves between runs with care.
<!-- docgen:preserve end guide-segment-pitfalls -->

## Next steps

**Related questions:**

- [Distribution shape](distribution_shape.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`op-agg-average`](../skills/op-agg-average.md), [`op-agg-count`](../skills/op-agg-count.md), [`op-group-kmeans`](../skills/op-group-kmeans.md), [`op-attr-pc-score`](../skills/op-attr-pc-score.md), [`op-group-category`](../skills/op-group-category.md), [`op-mat-distance`](../skills/op-mat-distance.md), [`op-mat-pca`](../skills/op-mat-pca.md)

**From the command line:**

- `pulse recommend --intent segment --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
