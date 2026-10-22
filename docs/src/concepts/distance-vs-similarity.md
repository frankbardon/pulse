# Distance versus similarity

## What it is

Both put a number on how alike two things are, where a thing is a row, a segment or a profile across several fields.

- A **distance** is large when two things differ and zero when they are identical.
- A **similarity** is large when two things are alike and small when they differ. It is the mirror image of distance.

The choice of **metric** inside each matters more than the choice between them. A metric is the rule for combining the field-by-field gaps. Pulse names several: `euclidean` (straight-line gap), `manhattan` (gaps added up), `cosine` (the angle between two profiles, ignoring their size), `correlation` (do two profiles rise and fall together), `jaccard` and `hamming` (for yes/no memberships), `aitchison` (for shares of a whole) and `mahalanobis` (allowing for how fields vary together).

## When to use it

- Segmenting: clustering groups rows that are close, and `MAT_DISTANCE` tells you how far apart the resulting segments are.
- Matching: "customers like this one" means scoring each row's similarity to a target profile (`ATTR_PROFILE_SIMILARITY`) or its distance from one (`ATTR_VEC_DISTANCE`).
- Comparing a row with its segment's centre, or a segment with a saved reference population.

## How to read it in Pulse

- `MAT_DISTANCE` compares segment centres: its `primary` block is a segment-by-segment grid with zeros on the diagonal; `auxiliary.centroids` holds each centre and `sizes` the rows behind it. Bigger means further apart.
- Row-level attributes add one number to every row: similarity attributes score higher for a closer match, distance attributes score lower.
- Convert freely: a similarity can become a distance as 1 minus the similarity (the `cosine` distance is exactly this), but do not compare numbers across different metrics; each has its own scale.

The decision that goes wrong most often is the metric. Declare what the numbers mean on the vector (`kind`: measure, scale, composition or binary) and Pulse picks a suitable default and warns about a poor fit (`PULSE_VECTOR_METRIC_UNSUITED`). The [vector-metrics skill](../guide/skills/vector-metrics.md) has the table.

## Common traps

- **Cosine on ratings.** Cosine ignores level, so a respondent who rates everything 1 and one who rates everything 5 look identical. For ratings, correlate or centre first; see [vector-metrics](../guide/skills/vector-metrics.md).
- **Euclidean on shares.** Shares that sum to a whole call for `aitchison`, not a straight-line gap.
- **Units.** A field in thousands swamps a field in single digits. Standardize (`params.standardize`) so each field counts equally.
- **Shared absences.** For yes/no memberships, decide whether two things both lacking a trait make them alike. `jaccard` says no; `hamming` says yes.
- **Distance between centres is not spread.** How far apart two centres are says nothing about how much the segments' members overlap: two tight segments can sit close yet barely overlap, and two widely spread ones can sit far apart yet overlap heavily. `MAT_DISTANCE` gives centres only, with no p-values.

## Where next

- Operators: [`op-mat-distance`](../guide/skills/op-mat-distance.md), [`op-attr-profile-similarity`](../guide/skills/op-attr-profile-similarity.md), [`op-group-kmeans`](../guide/skills/op-group-kmeans.md).
- Guides: [Segment into groups](../guide/questions/segment.md).
- Skills: [vector-metrics](../guide/skills/vector-metrics.md), [multivariate-design](../guide/skills/multivariate-design.md).
- Related concepts: [perceptual maps](perceptual-maps.md), [correlation matrix](correlation-matrix.md).
- Glossary: [distance](../guide/glossary.md#term-distance), [similarity](../guide/glossary.md#term-similarity).
