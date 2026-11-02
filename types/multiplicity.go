package types

// Multiplicity is a multiple-comparison correction block: which
// correction Method to apply, which Family of p-values is corrected
// together, and the Alpha an overlay's adjusted significance flag reads.
// It rides as an optional `multiplicity` slot on Request, Test (tier-1
// and post-tests), OverlaySpec (the Request and Facet overlay hosts),
// MatrixSpec (MAT_CORRELATION / MAT_PARTIAL_CORRELATION, where a block
// implies params.inference), ComposeOverlaySpec and ComposedRequest.
//
// Every field falls through on its own when empty: slot → request (a
// Compose slot: then the ComposedRequest) → pulse.Options.
// DefaultMultiplicity → none, and an omitted family takes the
// surface's default (`request` for tests, `layer` for overlays,
// `matrix` for a matrix spec).
// `{"method":"none"}` is the explicit opt-out. Raw p-values are never
// modified; the correction rides beside them.
//
// Unrelated to LookupMultiplicity (the key-multiplicity mode of a
// point lookup). A nil block is absent: every slot is `omitempty`, so a
// request without one is byte-identical on the wire and under
// CanonicalHash.
type Multiplicity struct {
	// Method is the correction procedure (MultiplicityMethod*). Empty
	// inherits.
	Method MultiplicityMethod `json:"method,omitempty"`

	// Family names which p-values are corrected together
	// (MultiplicityFamily*). Empty inherits, then takes the surface
	// default.
	Family MultiplicityFamily `json:"family,omitempty"`

	// Alpha is the significance level an overlay's
	// `significant_adjusted` flag compares against, in the open
	// interval (0, 1); zero inherits, then DefaultMultiplicityAlpha. A
	// test reads its own Test.Alpha instead, so Alpha set on a Test's
	// own block is refused.
	Alpha float64 `json:"alpha,omitempty"`
}

// MultiplicityMethod names one multiple-comparison correction
// procedure. The values match R's stats::p.adjust method names (`bh`
// and `by` are R's `BH` / `BY`).
type MultiplicityMethod string

const (
	// MultiplicityMethodNone applies no correction: the explicit
	// opt-out.
	MultiplicityMethodNone MultiplicityMethod = "none"
	// MultiplicityMethodBonferroni multiplies every p by the family
	// size m (family-wise error).
	MultiplicityMethodBonferroni MultiplicityMethod = "bonferroni"
	// MultiplicityMethodHolm is Holm's step-down procedure (family-wise
	// error, uniformly more powerful than Bonferroni).
	MultiplicityMethodHolm MultiplicityMethod = "holm"
	// MultiplicityMethodBH is the Benjamini-Hochberg step-up procedure
	// (false discovery rate under independence or positive dependence).
	MultiplicityMethodBH MultiplicityMethod = "bh"
	// MultiplicityMethodBY is the Benjamini-Yekutieli step-up procedure
	// (false discovery rate under arbitrary dependence).
	MultiplicityMethodBY MultiplicityMethod = "by"
)

// AllMultiplicityMethods returns every correction method, in a stable
// order.
func AllMultiplicityMethods() []MultiplicityMethod {
	return []MultiplicityMethod{
		MultiplicityMethodNone,
		MultiplicityMethodBonferroni,
		MultiplicityMethodHolm,
		MultiplicityMethodBH,
		MultiplicityMethodBY,
	}
}

// MultiplicityFamily names which p-values are corrected together.
type MultiplicityFamily string

const (
	// MultiplicityFamilyLayer corrects one overlay layer's p-values
	// together. The overlay default.
	MultiplicityFamilyLayer MultiplicityFamily = "layer"
	// MultiplicityFamilyRow corrects each row of one MATRIX overlay
	// layer separately.
	MultiplicityFamilyRow MultiplicityFamily = "row"
	// MultiplicityFamilyColumn corrects each column of one MATRIX
	// overlay layer separately.
	MultiplicityFamilyColumn MultiplicityFamily = "column"
	// MultiplicityFamilyRequest pools one Request's tests, post-tests
	// and `request`-family overlays. The test default.
	MultiplicityFamilyRequest MultiplicityFamily = "request"
	// MultiplicityFamilyCompose pools every `compose`-family member
	// across a ComposedRequest's slots and its Compose-host overlay
	// layers. Valid only inside Compose.
	MultiplicityFamilyCompose MultiplicityFamily = "compose"
	// MultiplicityFamilyMatrix corrects the p-values of one matrix
	// result together: the off-diagonal upper-triangle cells of a
	// square_symmetric matrix (m = p(p-1)/2 for p members — the
	// diagonal and the mirrored lower triangle are not separate
	// hypotheses), every cell of a rectangular one; one family per
	// group bucket on a grouped request. The matrix spec default; valid
	// only on a MatrixSpec's block.
	MultiplicityFamilyMatrix MultiplicityFamily = "matrix"
)

// MultiplicityFamilyMargins is the family a regression's marginal
// effects (RegressionSpec.Margins) form on their own under the
// regression's multiplicity block: one family per regression, apart
// from its coefficients' `request` / `compose` family, so adding margins
// never moves a coefficient's p_adjusted. An ECHO value only
// (RegressionMarginsResult.Multiplicity.Family): never requested — a
// block naming it is PULSE_MULTIPLICITY_INVALID — and so not in
// AllMultiplicityFamilies (the requestable set).
const MultiplicityFamilyMargins MultiplicityFamily = "margins"

// AllMultiplicityFamilies returns every requestable correction family,
// in a stable order (the echo-only MultiplicityFamilyMargins is not one).
func AllMultiplicityFamilies() []MultiplicityFamily {
	return []MultiplicityFamily{
		MultiplicityFamilyLayer,
		MultiplicityFamilyRow,
		MultiplicityFamilyColumn,
		MultiplicityFamilyRequest,
		MultiplicityFamilyCompose,
		MultiplicityFamilyMatrix,
	}
}

// DefaultMultiplicityAlpha is the overlay significance level a
// resolved multiplicity block carries when nothing sets `alpha`.
const DefaultMultiplicityAlpha = 0.05

// AppliedMultiplicity echoes a correction that ran over a result's
// p-value: the resolved Method and Family, the Alpha its
// `significant_adjusted` flag compared against (a test's own
// Test.Alpha), and M, the family size — the number of defined
// (non-NaN) p-values corrected together. It rides beside the raw
// p-value, never in place of it, and is absent when no correction ran
// (no block resolved, or the method resolved to none).
//
// A `row` / `column` family is one family per row / column index of
// the layer's own MATRIX payload: MPer carries each family's size,
// index-aligned with the matrix rows (row) or columns (column), and M
// is their sum — every defined p-value the layer corrected. MPer is
// absent for every other family.
//
// Excluded counts the family's members whose p-value was undefined
// (NaN — e.g. a grouped test's sparse group or partition, whose
// statistic and p_value are null) and so left out of M; their
// p_adjusted is null. It is absent (0) when every member was defined,
// so a family with no undefined p-value is byte-identical. For a `row`
// / `column` family it is the sum over the layer's row / column
// families, like M.
type AppliedMultiplicity struct {
	Method   MultiplicityMethod `json:"method"`
	Family   MultiplicityFamily `json:"family"`
	Alpha    float64            `json:"alpha"`
	M        int                `json:"m"`
	MPer     []int              `json:"m_per,omitempty"`
	Excluded int                `json:"excluded,omitempty"`
}
