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
		Means: "Each off-diagonal cell is Pearson r for its row and column members: how closely the two follow a straight line together, from -1 to +1; 0 means no straight-line link. The diagonal is 1.",
		Sign:  correlationSign,
		Caveats: []string{
			causationCaveatText,
			"A null cell means a member had no spread (constant, or too few rows), so its correlation is undefined, not 0.",
			"An r near zero rules out only a straight-line link; a curved relationship can still be strong.",
			"The matrix reports no p-values; run TEST_PEARSON_R on a pair to test it.",
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
