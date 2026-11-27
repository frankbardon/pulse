# Perceptual maps

## What it is

A perceptual map is a two-dimensional picture in which things that people see as alike sit close together and things seen as different sit far apart. Typical uses: brands placed by how customers rate them, or products placed by their features, with the axes named after what separates them (for example "premium versus budget" and "traditional versus modern").

Pulse does not draw the picture; it computes the coordinates and the distances behind it. You plot them in the tool of your choice.

## When to use it

- You want to show where brands, products or segments sit relative to each other on a set of ratings.
- You want to see which attributes pull items apart and which items compete with each other.

## How to build one in Pulse

A map is built from pieces you already know, in three steps, and all three fit in one request:

1. **Find the axes.** Run a [principal component analysis](pca.md) (`MAT_PCA`) over the rating items. Keep the first two components and read the loadings to name them. The loadings also place the rating items themselves on the map: items with similar loadings point the same way.
2. **Place each row.** Add `ATTR_PC_SCORE` for component 1 and again for component 2, giving every row a coordinate pair. By default each fits itself on the request's rows, so no loadings are passed in, and its sign and order match `MAT_PCA`'s.
3. **Place each item (brand, product, segment).** Ask `MAT_DISTANCE` over the two score fields with `params.between` grouping by the item. Its `auxiliary.centroids` are each item's mean scores, which is its map position, and its `primary` grid holds the distances between those centres.

The two-axis picture is a flattened view of the ratings and always loses some information. To check what it shows, run a second `MAT_DISTANCE` with the same `between` over the rating items themselves (with `standardize`): that grid is exact. See [distance versus similarity](distance-vs-similarity.md) for the choice of metric.

The story below maps five brands from six ratings (`pulse examples show perceptual_map` prints the request; its cohorts: [Get the data](../examples/library.md#get-the-data)). The first component is premium versus budget, the second modern versus traditional; `brand_distance` gives the map grid and the centroids, `rating_distance` the check.

```json
{{#include ../../../internal/examples/testdata/stories/perceptual_map.json}}
```

## How to read it

- Close together means alike on the fields you used; far apart means different. Only distances matter, not the position of the origin.
- An axis is only as good as its name. Name it from the largest loadings, and ask whether that name would convince a colleague.
- The share of variation the first two components carry (`cumulative` in the PCA output) tells you how much of the truth the flat picture holds. If it is low, the map misleads; consider a third axis or the plain distance grid.
- Component signs are arbitrary: you can flip an axis without changing anything.

## Common traps

- **The map is only as good as the ratings.** If respondents rated everything alike, the map shows noise.
- **Ratings with different levels.** People who rate everything high and people who rate everything low separate on the first component for reasons that are not about the brands. Centre each row's ratings first (subtract the row's own average rating from each of its ratings), or use a correlation-based similarity.
- **Too few rows per item.** Averages from a handful of rows move a lot.
- **Over-reading small gaps.** Pulse's centre distances carry no p-values; two close points are not proven different, and two far points are not proven far.
- **Distances in the plot are not the distances in the data.** Always verify a surprising pair against the distance grid.

## Where next

- Operators: [`op-mat-pca`](../guide/skills/op-mat-pca.md), [`op-attr-pc-score`](../guide/skills/op-attr-pc-score.md), [`op-mat-distance`](../guide/skills/op-mat-distance.md).
- Guides: [Measure a construct](../guide/questions/measure_construct.md), [Segment into groups](../guide/questions/segment.md).
- Skills: [multivariate-design](../guide/skills/multivariate-design.md), [vector-metrics](../guide/skills/vector-metrics.md).
- Related concepts: [principal component analysis](pca.md), [distance versus similarity](distance-vs-similarity.md), [correlation matrix](correlation-matrix.md).
