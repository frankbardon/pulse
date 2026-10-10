package descriptor

import "github.com/frankbardon/pulse/descriptor"

// matrixInterpretations is the Interpretation registry for the MAT_*
// operators, keyed by type; builtinInterpretations merges it with the
// other category maps. Paths read a MatrixResult: primary.values (every
// cell of the primary matrix) and scalars.<key> (manifest
// output_keys.scalars). MAT_COVARIANCE's cells are in the members' own
// units, so it declares none.
var matrixInterpretations = map[string][]descriptor.Interpretation{
	"MAT_CORRELATION": interpMatCorrelation,
}

var interpMatCorrelation = []descriptor.Interpretation{
	bandedBy(ConventionCohenR, descriptor.Interpretation{
		Field: "primary.values",
		Means: "Each off-diagonal cell is the correlation of its row and column members under params.method, from -1 to +1. pearson (default): r, how closely the two follow a straight line together; spearman: rho, r on the members' ranks, how steadily one rises or falls with the other; kendall: tau-b, the share of agreeing minus disagreeing row pairs, tie-adjusted. 0 means no such link. The diagonal is 1.",
		Sign:  correlationSign,
		Caveats: []string{
			causationCaveatText,
			"A null cell means a member had no spread (constant, or too few rows), so its correlation is undefined, not 0.",
			"A Pearson r near zero rules out only a straight-line link; a curved relationship can still be strong.",
			"The bands are Cohen's for r; Kendall's tau-b runs smaller than r or rho for the same strength (about two thirds of rho), so read a kendall cell against lower cut-offs.",
			"The matrix reports no p-values; run TEST_PEARSON_R, TEST_SPEARMAN_R or TEST_KENDALL_TAU on a pair to test it.",
		},
	}),
	{
		Field: "scalars.determinant",
		Means: "The determinant of the correlation table: 1 when no member is linearly related to the others, falling toward 0 as members become linear combinations of one another.",
		Caveats: []string{
			"Null when the table is not positive definite, including when any cell is null.",
			"No published convention bands it; a value near 0 flags near-redundant members.",
		},
	},
}
