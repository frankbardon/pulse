package descriptor

import "github.com/frankbardon/pulse/descriptor"

// matrixInterpretations is the Interpretation registry for the MAT_*
// operators, keyed by type; builtinInterpretations merges it with the
// other category maps. Paths read a MatrixResult: primary.values (every
// cell of the primary matrix) and scalars.<key> (manifest
// output_keys.scalars). MAT_COVARIANCE's cells are in the members' own
// units, so it declares none.
var matrixInterpretations = map[string][]descriptor.Interpretation{
	"MAT_CORRELATION":         interpMatCorrelation,
	"MAT_PARTIAL_CORRELATION": interpMatPartialCorrelation,
}

var interpMatPartialCorrelation = []descriptor.Interpretation{
	bandedBy(ConventionCohenR, descriptor.Interpretation{
		Field: "primary.values",
		Means: "Each off-diagonal cell is the correlation of its row and column members once the held-fixed fields are taken out of both (every other member, or the params.control fields), from -1 to +1. 0 means no straight-line link is left after holding them fixed. The diagonal is 1.",
		Sign:  correlationSign,
		Caveats: []string{
			causationCaveatText,
			"A partial r can be much smaller than, or even opposite in sign to, the plain r when the held-fixed fields drive both members; holding fixed a field that is itself an outcome of the pair can also create a link that is not there.",
			"A null cell means the input had an undefined correlation (a member with no spread, or too few rows), so no cell can be computed.",
			"Under params.repair the cells come from the nearest consistent correlation table, not the observed one; the PULSE_MATRIX_NOT_PSD warning says how far it moved.",
			"The matrix reports no p-values.",
		},
	}),
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
