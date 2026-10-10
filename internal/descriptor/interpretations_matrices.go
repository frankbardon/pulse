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
	"MAT_RELIABILITY":         interpMatReliability,
}

// interpMatReliability bands alpha by George & Mallery (2003) and
// leaves omega unbanded: no omega-specific convention was verified, and
// the alpha bands are not transferred to it.
var interpMatReliability = []descriptor.Interpretation{
	bandedBy(ConventionGeorgeMalleryAlpha, descriptor.Interpretation{
		Field: "scalars.alpha",
		Means: "Cronbach's alpha: how consistently the items measure one shared quality, from the share of the summed score's spread that the items share. 1 means the items move in lockstep; it can fall below 0 when items pull against each other, which usually means a reverse-worded item was not reversed.",
		Caveats: []string{
			"Alpha rises with the number of items, so a long battery can reach a high alpha with weakly related items.",
			"A high alpha does not show the items measure only one thing.",
			"Alpha assumes every item is equally tied to the shared quality; when the ties differ it understates reliability, which omega allows for.",
		},
	}),
	bandedBy(ConventionGeorgeMalleryAlpha, descriptor.Interpretation{
		Field: "scalars.alpha_standardized",
		Means: "Alpha computed on the items' correlations instead of their raw spreads: the alpha the battery would have if every item were first put on the same scale.",
		Caveats: []string{
			"It differs from alpha when the items' spreads differ; report the one that matches how the score is built (raw sums or standardised items).",
		},
	}),
	{
		Field: "scalars.omega",
		Means: "McDonald's omega: the share of the summed score's spread due to the one shared quality, from a one-factor fit that lets each item's tie to it differ. Read on the same 0 to 1 scale as alpha; it is usually at least as high.",
		Caveats: []string{
			"No published convention bands omega; it is not read against the alpha bands.",
			"Null with a warning when the battery has 2 items, when the fit puts an item's leftover spread at or below zero (a Heywood case), or when a pairwise table is inconsistent and params.repair is not set.",
			"The fit assumes one shared quality; when the items reflect several, omega from one factor misstates reliability.",
		},
	},
	{
		Field: "scalars.mean_inter_item_r",
		Means: "The average correlation among the items, the strength of the typical pair; standardized alpha is built from it and the item count.",
		Caveats: []string{
			"An average hides spread: one item unrelated to the rest pulls it down; check the table and item_total_r.",
			"Like any correlation it shows the items move together, not that one causes another.",
		},
	},
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
