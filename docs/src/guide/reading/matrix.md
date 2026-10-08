# Reading matrix results

How to read the standardised results of the matrix operators this instance offers. Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.

## Matrix operators

<a id="op-mat_correlation"></a>

### `MAT_CORRELATION`

How closely every pair in a set of numeric fields follows a straight line together, as one square table of r values from -1 to 1. See its [catalog entry](../catalog/matrix.md#op-mat_correlation).

#### `primary.values`

Each off-diagonal cell is Pearson r for its row and column members: how closely the two follow a straight line together, from -1 to +1; 0 means no straight-line link. The diagonal is 1.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.1 | very small |
| from 0.1 to below 0.3 | small |
| from 0.3 to below 0.5 | medium |
| 0.5 and above | large |

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- Correlation is not causation: a third factor may drive both fields.
- A null cell means a member had no spread (constant, or too few rows), so its correlation is undefined, not 0.
- An r near zero rules out only a straight-line link; a curved relationship can still be strong.
- The matrix reports no p-values; run TEST_PEARSON_R on a pair to test it.

#### `scalars.determinant`

The determinant of the correlation table: 1 when no member is linearly related to the others, falling toward 0 as members become linear combinations of one another.

**Caveats:**

- Null when the table is not positive definite, including when any cell is null.
- No published convention bands it; a value near 0 flags near-redundant members.
