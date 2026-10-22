# Perceptual maps

## What it is

A perceptual map is a two-dimensional picture in which things that people see as alike sit close together and things seen as different sit far apart. Typical uses: brands placed by how customers rate them, or products placed by their features, with the axes named after what separates them (for example "premium versus budget" and "traditional versus modern").

Pulse does not draw the picture; it computes the coordinates and the distances behind it. You plot them in the tool of your choice.

## When to use it

- You want to show where brands, products or segments sit relative to each other on a set of ratings.
- You want to see which attributes pull items apart and which items compete with each other.

## How to build one in Pulse

A map is built from pieces you already know, in three steps:

1. **Find the axes.** Run a [principal component analysis](pca.md) over the rating items. Keep the first two components and read the loadings to name them. The loadings also place the rating items themselves on the map: items with similar loadings point the same way.
2. **Place each row.** Add `ATTR_PC_SCORE` for component 1 and again for component 2, giving every row a coordinate pair.
3. **Place each item (brand, product, segment).** Group the rows by it and average the two score fields. The group averages are that item's map position.

To check what the picture shows, ask `MAT_DISTANCE` for the grid of distances between the group centres, over the same fields. The grid is exact; the two-axis picture is a flattened view of it and always loses some information. See [distance versus similarity](distance-vs-similarity.md) for the choice of metric.

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
