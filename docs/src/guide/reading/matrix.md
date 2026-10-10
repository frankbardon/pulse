# Reading matrix results

How to read the standardised results of the matrix operators this instance offers. Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.

## Matrix operators

<a id="op-mat_correlation"></a>

### `MAT_CORRELATION`

How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1. See its [catalog entry](../catalog/matrix.md#op-mat_correlation).

#### `primary.values`

Each off-diagonal cell is the correlation of its row and column members under params.method, from -1 to +1. pearson (default): r, how closely the two follow a straight line together; spearman: rho, r on the members' ranks, how steadily one rises or falls with the other; kendall: tau-b, the share of agreeing minus disagreeing row pairs, tie-adjusted. 0 means no such link. The diagonal is 1.

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
- A Pearson r near zero rules out only a straight-line link; a curved relationship can still be strong.
- The bands are Cohen's for r; Kendall's tau-b runs smaller than r or rho for the same strength (about two thirds of rho), so read a kendall cell against lower cut-offs.
- The matrix reports no p-values; run TEST_PEARSON_R, TEST_SPEARMAN_R or TEST_KENDALL_TAU on a pair to test it.

#### `scalars.determinant`

The determinant of the correlation table: 1 when no member is linearly related to the others, falling toward 0 as members become linear combinations of one another.

**Caveats:**

- Null when the table is not positive definite, including when any cell is null.
- No published convention bands it; a value near 0 flags near-redundant members.

<a id="op-mat_partial_correlation"></a>

### `MAT_PARTIAL_CORRELATION`

How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1. See its [catalog entry](../catalog/matrix.md#op-mat_partial_correlation).

#### `primary.values`

Each off-diagonal cell is the correlation of its row and column members once the held-fixed fields are taken out of both (every other member, or the params.control fields), from -1 to +1. 0 means no straight-line link is left after holding them fixed. The diagonal is 1.

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
- A partial r can be much smaller than, or even opposite in sign to, the plain r when the held-fixed fields drive both members; holding fixed a field that is itself an outcome of the pair can also create a link that is not there.
- A null cell means the input had an undefined correlation (a member with no spread, or too few rows), so no cell can be computed.
- Under params.repair the cells come from the nearest consistent correlation table, not the observed one; the PULSE_MATRIX_NOT_PSD warning says how far it moved.
- The matrix reports no p-values.
