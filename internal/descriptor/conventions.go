package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
)

// effectConvention is one named, sourced effect-size convention: the
// single source of every built-in Interpretation.Bands set. The
// independently transcribed fixture testdata/conventions.json is held
// equal to this registry by TestConventionRegistryMatchesFixture, and
// every built-in Interpretation that declares Bands must match exactly
// one entry (TestBuiltinBandsCiteRegisteredConvention).
//
// The registry is for built-ins only: an extension's Convention stays
// free text, validated structurally by ValidateInterpretations.
type effectConvention struct {
	// ID is the stable registry key (<source><year>_<statistic>).
	ID string
	// Citation is the Interpretation.Convention text the bands carry.
	Citation string
	// Statistics names the outputs the convention may band: an
	// effect-size key (details.effect_size.<key>) or a statistic name the
	// fixture binds an operator's "statistic" field to.
	Statistics []string
	// Thresholds are the inclusive lower bounds of the named bands, in
	// ascending order (Cohen's small / medium / large).
	Thresholds []float64
	// Labels names the bands: Labels[0] is the band open below
	// Thresholds[0], Labels[i] the band starting at Thresholds[i-1].
	// len(Labels) == len(Thresholds)+1.
	Labels []string
	// Abs is true when the bands apply to the absolute value (signed
	// statistics such as r and d); it is copied to Interpretation.Abs.
	Abs bool
	// SymmetricLog is true for a ratio statistic whose bands apply to
	// max(x, 1/x) — equivalently |ln x| — so an odds ratio of 0.1 reads
	// like one of 10. The Interpretation must say so in prose; Abs stays
	// false because the fold is a reciprocal, not a sign flip.
	SymmetricLog bool
}

// Bands renders the convention as Interpretation bands: the first band
// open below, the last open above, each Min inclusive and Max exclusive.
func (c effectConvention) Bands() []descriptor.Band {
	out := make([]descriptor.Band, 0, len(c.Labels))
	for i, label := range c.Labels {
		var b descriptor.Band
		if i > 0 {
			b.Min = bandBound(c.Thresholds[i-1])
		}
		if i < len(c.Thresholds) {
			b.Max = bandBound(c.Thresholds[i])
		}
		b.Label = label
		out = append(out, b)
	}
	return out
}

// Convention IDs referenced by built-in Interpretations.
const (
	ConventionCohenD    = "cohen1988_d"
	ConventionCohenEta2 = "cohen1988_eta2"
	ConventionCohenR    = "cohen1988_r"
	ConventionCohenW    = "cohen1988_w"
	ConventionCohenOR   = "cohen1988_or"
)

// cohenLabels names Cohen's three benchmarks plus the region below
// "small", which Cohen leaves unnamed; Pulse calls it "negligible".
var cohenLabels = []string{"negligible", "small", "medium", "large"}

// builtinConventions is the convention registry. Thresholds are Cohen's
// (1988) benchmarks as tabled in Cohen (1992, Table 1, p. 157); the
// sources per entry live in testdata/conventions.json.
var builtinConventions = map[string]effectConvention{
	// Standardised mean difference d (also g, Glass's delta), and
	// Cohen's h for two proportions, whose benchmarks are the same
	// .20 / .50 / .80 (Cohen 1992, Table 1 row 5).
	ConventionCohenD: {
		ID: ConventionCohenD, Citation: "Cohen (1988)",
		Statistics: []string{"cohens_d", "hedges_g", "glass_delta", "cohens_h"},
		Thresholds: []float64{0.2, 0.5, 0.8}, Labels: cohenLabels, Abs: true,
	},
	// Share of variance: eta squared, partial eta squared, omega squared,
	// epsilon squared. Cohen's f of .10 / .25 / .40 rendered as
	// f^2 / (1 + f^2) = .0099 / .0588 / .1379, conventionally rounded.
	ConventionCohenEta2: {
		ID: ConventionCohenEta2, Citation: "Cohen (1988)",
		Statistics: []string{"eta_squared", "partial_eta_squared", "omega_squared", "epsilon_squared"},
		Thresholds: []float64{0.01, 0.06, 0.14}, Labels: cohenLabels,
	},
	// Product-moment correlation r, on |r|.
	ConventionCohenR: {
		ID: ConventionCohenR, Citation: "Cohen (1988)",
		Statistics: []string{"pearson_r"},
		Thresholds: []float64{0.1, 0.3, 0.5}, Labels: cohenLabels, Abs: true,
	},
	// Cohen's w for chi-square tests; phi on a 2x2 table (w == phi at
	// df* = 1). Not for Cramer's V on larger tables, whose benchmarks
	// scale by 1/sqrt(df*) — that rule is a caveat, never a band set.
	ConventionCohenW: {
		ID: ConventionCohenW, Citation: "Cohen (1988)",
		Statistics: []string{"cohens_w", "phi"},
		Thresholds: []float64{0.1, 0.3, 0.5}, Labels: cohenLabels,
	},
	// Odds ratio, via d = ln(OR) * sqrt(3) / pi applied to Cohen's d
	// benchmarks; symmetric on max(OR, 1/OR).
	ConventionCohenOR: {
		ID: ConventionCohenOR, Citation: "Cohen (1988), via Chen, Cohen & Chen (2010)",
		Statistics: []string{"odds_ratio"},
		Thresholds: []float64{1.44, 2.48, 4.27}, Labels: cohenLabels, SymmetricLog: true,
	},
}

// conventionOf returns the registered convention id, panicking on an
// unknown id: every lookup is a package-level registry literal, so a
// typo fails the first test that loads the package.
func conventionOf(id string) effectConvention {
	c, ok := builtinConventions[id]
	if !ok {
		panic("descriptor: unknown effect-size convention " + id)
	}
	return c
}

// conventionBands returns the bands of convention id.
func conventionBands(id string) []descriptor.Band { return conventionOf(id).Bands() }

// conventionCitation returns the Convention text of convention id.
func conventionCitation(id string) string { return conventionOf(id).Citation }

// conventionIDs returns every registered convention ID, sorted.
func conventionIDs() []string {
	out := make([]string, 0, len(builtinConventions))
	for id := range builtinConventions {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
