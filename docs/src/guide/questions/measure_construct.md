# Measure a construct

## The question

Questions of this kind sound like:

- Do these questions measure one thing?
- Combine these items into a score.

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

No worked example is published for this question yet. `pulse recommend --intent measure_construct` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Relationship between measures](relationship.md)

**Skills:** [`intents`](../skills/intents.md), [`attribute-composition`](../skills/attribute-composition.md), [`statistical-testing`](../skills/statistical-testing.md)

**From the command line:**

- `pulse recommend --intent measure_construct --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
