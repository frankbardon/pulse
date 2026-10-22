# Principal component analysis

## What it is

Principal component analysis (PCA) takes a set of numeric fields that overlap and rebuilds them as a few new combined measures, called components, that carry most of the information. The first component is the single weighted blend of your fields that varies the most across rows; the second is the next most varying blend that is unrelated to the first, and so on. Ten overlapping survey items often collapse into one or two components.

It is a way to ask how many separate things your fields are really measuring, and which fields belong to which.

## When to use it

- You have many correlated fields and want a handful of summary scores.
- You want to know whether a battery of items measures one idea or several.
- You want to reduce overlap before a regression or a clustering.

If you believe a hidden trait causes the answers and want to model that, look at factor analysis (`MAT_FACTOR`) instead. PCA just summarises; it makes no claim about causes. The [Measure a construct guide](../guide/questions/measure_construct.md) shows when to reach for which, with a worked reliability story on a six-item battery.

## How to read it in Pulse

Declare the fields as a vector and ask for `MAT_PCA` (`pulse examples show principal_components` prints a runnable request). Check these in order:

1. **Is it worth doing?** The result reports a KMO figure (how well the fields share variation; higher is better) and Bartlett's sphericity test (are the fields related at all). If they fail, the fields have too little in common to summarise.
2. **How many components?** `eigenvalues`, `explained_variance` and `cumulative` list every component with its share of the total variation. A big first share and then a sharp drop means one dominant dimension. By default Pulse keeps components with an eigenvalue above 1; set `params.components` to choose a number or a variance share.
3. **What does each mean?** The `primary` block holds the loadings: one row per field, one column per component (`PC1`, `PC2`, ...). Large loadings, in size, say which fields make up a component; name the component after them.
4. **Per-field fit.** `communalities` say how much of each field the kept components explain; a low one means the field does not belong with the rest.

Component signs are arbitrary: a flipped sign says nothing. Only the pattern of large and small loadings matters.

To put a component on every row, add `ATTR_PC_SCORE` with the component number: each row gets its standardized score, which you can then average by group or feed to a regression.

## Common traps

- **Scale matters.** The default works on correlations, so field units do not dominate. Working on covariances (`params.on`) lets large-unit fields take over; use it only on purpose.
- **Too few rows.** Components built from a small sample are unstable. Check the row count in the result's components before reading detail.
- **Reading too much into small loadings.** Focus on the largest ones.
- **Rotation.** `params.rotation` makes components easier to name, but it spreads the variation across them, so read `rotated_variance` afterwards.
- **Missing data.** `listwise` drops whole rows; `pairwise` can give an inconsistent grid (`PULSE_MATRIX_NOT_PSD`), which `params.repair` can fix.

## Where next

- Operators: [`op-mat-pca`](../guide/skills/op-mat-pca.md), [`op-attr-pc-score`](../guide/skills/op-attr-pc-score.md), [`op-mat-factor`](../guide/skills/op-mat-factor.md), [`op-mat-reliability`](../guide/skills/op-mat-reliability.md).
- Guides: [Measure a construct](../guide/questions/measure_construct.md).
- Skills: [multivariate-outputs](../guide/skills/multivariate-outputs.md), [multivariate-design](../guide/skills/multivariate-design.md).
- Related concepts: [correlation matrix](correlation-matrix.md), [perceptual maps](perceptual-maps.md).
- Glossary: [principal-component](../guide/glossary.md#term-principal-component), [eigenvalue](../guide/glossary.md#term-eigenvalue), [loading](../guide/glossary.md#term-loading).
