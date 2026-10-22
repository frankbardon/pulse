# Correlation matrix

## What it is

A correlation matrix is a grid that shows, for every pair of numeric fields, how closely the two move together in a straight line. Each cell is a number from -1 to 1. Near 1 means the fields rise and fall together, near -1 means one rises as the other falls, near 0 means no straight-line link. A field against itself is always 1, so the diagonal is all ones, and the grid reads the same above and below the diagonal.

It is a table of many two-field correlations laid out at once, so you can see the whole picture instead of testing one pair at a time.

## When to use it

- You have a battery of related fields (survey items, usage measures) and want to see which ones go together before you do anything else with them.
- You are about to build a score, run a principal component analysis or fit a regression, and want a first look at overlap between the fields.
- You want to spot fields that say nearly the same thing (near 1 or -1), which is a warning sign for a regression.

If you only care about one pair, ask the [relationship question](../guide/questions/relationship.md) instead: it gives a test, an interval and a p-value for that pair.

## How to read it in Pulse

Declare the fields once as a vector and ask for `MAT_CORRELATION` over it (`pulse examples show correlation_matrix` prints a runnable request). The result's `primary` block holds the grid: `row_keys` and `column_keys` name the fields, and `values` holds the cells in the same order.

- Read the sign first (do they move together or apart), then the size.
- Rank-based methods (`spearman`, `kendall`) via `params.method` cope better with outliers and with ratings that are really ordered categories.
- `params.missing` chooses what happens to rows with a gap: `listwise` drops the whole row from every cell, `pairwise` uses every row that has both fields of a pair, so cells may rest on different rows. Under pairwise the result also reports how many rows each cell used.
- Add `params.inference` to get a p-value and confidence interval for every pair. With many pairs, chance alone will make a few look strong: add a `multiplicity` block (see the [multiplicity-correction skill](../guide/skills/multiplicity-correction.md)).

Every cell is the same figure that a single two-field test reports. The story below is a one-pair run: its `statistic` is that pair's correlation, the number a matrix cell would hold.

```json
{{#include ../../../internal/examples/testdata/stories/pearson_correlation.json}}
```

## Common traps

- **Correlation is not cause.** A strong cell says two fields move together, not that one drives the other.
- **Straight lines only.** A curved relationship can show a cell near 0.
- **Outliers.** A few extreme rows can create or hide a strong cell. Try a rank method as a cross-check.
- **Nearly identical fields.** Cells close to 1 mean the fields are redundant; keep one when you regress.
- **Thin pairs.** Under pairwise handling a cell can rest on few rows. Check the per-pair row counts before trusting it.
- **Mixed meaning.** Reverse-worded survey items correlate negatively with the rest by design; flip them before judging a scale (see [reliability on the measure-a-construct guide](../guide/questions/measure_construct.md)).

## Where next

- Operator: [`op-mat-correlation`](../guide/skills/op-mat-correlation.md), and [`op-mat-partial-correlation`](../guide/skills/op-mat-partial-correlation.md) for the link left after the other fields are held fixed.
- Guides: [Relationship between measures](../guide/questions/relationship.md), [Measure a construct](../guide/questions/measure_construct.md).
- Skills: [matrix-results](../guide/skills/matrix-results.md), [multivariate-design](../guide/skills/multivariate-design.md).
- Related concepts: [principal component analysis](pca.md), [distance versus similarity](distance-vs-similarity.md).
- Glossary: [correlation](../guide/glossary.md#term-correlation).
