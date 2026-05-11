package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// shapiroWilkRow implements TEST_SHAPIRO_WILK as a tier-1 buffered
// row test for normality.
//
// Algorithm (Royston 1992, AS R94, with Shapiro-Francia fallback for
// small n where the Royston coefficients are tabulated identically):
//
//  1. Sort the sample x_(1) ≤ … ≤ x_(n).
//  2. Approximate the expected normal order statistics via Blom (1958):
//     m_i = Φ⁻¹((i − 3/8) / (n + 1/4)).
//  3. Royston's polynomial-tuned coefficients a_i replace the
//     Shapiro-Wilk weight matrix that is tabulated for n ≤ 50 in
//     AS R94. The pure Shapiro-Francia simplification a_i = m_i / √Σm²
//     is used here for portability; this is the well-known
//     Shapiro-Francia variant W'. For n ≥ 5 the two statistics differ
//     by < 2 % and have nearly identical power.
//  4. W' = (Σ a_i x_(i))² / Σ (x_i − x̄)².
//  5. Transform W' to a z-score via Royston's polynomial coefficients
//     (different polynomials for n ≤ 11 vs n ≥ 12); p = 1 − Φ(z).
//
// Caveat: the variant shipped is Shapiro-Francia (uses Blom approximate
// expected order statistics with a_i ∝ m_i). It carries the
// TEST_SHAPIRO_WILK name because it lands in the normality-test slot;
// the result Variant field reports "shapiro_francia".
type shapiroWilkRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	groups map[string][]float64
	order  []string
}

func newShapiroWilkRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_SHAPIRO_WILK requires field")
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	if schema != nil {
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_SHAPIRO_WILK field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if spec.SplitBy != "" {
			if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("TEST_SHAPIRO_WILK split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
					map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
			}
		}
	}
	return &shapiroWilkRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		groups:  make(map[string][]float64),
	}, nil
}

func (s *shapiroWilkRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(s.field)
	if !ok {
		return nil
	}
	key := ""
	if s.splitBy != "" {
		k, ok := record.StringValue(s.splitBy)
		if !ok {
			return nil
		}
		key = k
	}
	if _, exists := s.groups[key]; !exists {
		s.order = append(s.order, key)
	}
	s.groups[key] = append(s.groups[key], v)
	return nil
}

func (s *shapiroWilkRow) Finalize() (*types.TestResult, error) {
	defer s.reset()
	keys := append([]string(nil), s.order...)
	sort.Strings(keys)
	// Aggregate result. When SplitBy is set, emit one entry per group;
	// otherwise the single bucket has key "".
	perGroup := make([]map[string]any, 0, len(keys))
	var headlineW, headlineP float64
	var headlineWarn []string
	for _, key := range keys {
		vs := append([]float64(nil), s.groups[key]...)
		n := len(vs)
		if n < 3 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_SHAPIRO_WILK requires n ≥ 3 per group; %q has %d", key, n),
				map[string]any{"group": key, "n": n, "min_required": 3})
		}
		sort.Float64s(vs)
		W, z, p, warn := shapiroFranciaStat(vs)
		entry := map[string]any{
			"n":       n,
			"w":       W,
			"z":       z,
			"p_value": p,
		}
		if key != "" {
			entry["group"] = key
		}
		if warn != "" {
			entry["warning"] = warn
			headlineWarn = append(headlineWarn, fmt.Sprintf("%s: %s", key, warn))
		}
		perGroup = append(perGroup, entry)
		// Headline result tracks the worst (smallest) p across groups
		// so the single Statistic / PValue slots stay informative.
		if len(perGroup) == 1 || p < headlineP {
			headlineP = p
			headlineW = W
		}
	}
	res := &types.TestResult{
		Label:      testLabel(s.spec),
		Type:       types.TEST_SHAPIRO_WILK,
		Variant:    "shapiro_francia",
		Statistic:  headlineW,
		PValue:     headlineP,
		Alpha:      s.alpha,
		RejectNull: headlineP < s.alpha,
		Details: map[string]any{
			"per_group": perGroup,
		},
	}
	res.Warnings = append(res.Warnings, headlineWarn...)
	return res, nil
}

func (s *shapiroWilkRow) reset() {
	s.groups = make(map[string][]float64)
	s.order = nil
}

// shapiroFranciaStat computes W', z, p for a *sorted* sample. Returns
// the optional warning string for n-bound or low-variance.
//
// Blom expected order statistics: m_i = Φ⁻¹((i − 3/8) / (n + 1/4)).
// a_i = m_i / √Σm_i².
// W' = (Σ a_i x_(i))² / Σ (x_i − x̄)².
//
// p-value: under H₀ (normal), W' ≈ 1 with small variance. Royston's
// transformation for Shapiro-Francia (n ≥ 5):
//   u = log(n)
//   μ_z   = −1.2725 + 1.0521(log(u) − u)
//   σ_z   = 1.0308 − 0.26758(log(u) + 2/u)
//   y     = log(1 − W')
//   z     = (y − μ_z) / σ_z
//   p     = 1 − Φ(z)
func shapiroFranciaStat(sorted []float64) (W, z, p float64, warn string) {
	n := len(sorted)
	if n > 5000 {
		warn = "n above the 5000-row support bound; treat the p-value as advisory and consider asymptotic alternatives"
	}
	// Expected normal order statistics.
	m := make([]float64, n)
	var mSqSum float64
	for i := range n {
		frac := (float64(i+1) - 0.375) / (float64(n) + 0.25)
		m[i] = inverseNormalCDF(frac)
		mSqSum += m[i] * m[i]
	}
	if mSqSum == 0 {
		return 0, 0, 1, "degenerate Blom coefficients"
	}
	// W' numerator and SS denominator.
	var meanX float64
	for _, v := range sorted {
		meanX += v
	}
	meanX /= float64(n)
	var ss, num float64
	for i, v := range sorted {
		d := v - meanX
		ss += d * d
		num += m[i] * v
	}
	if ss == 0 {
		return 1, 0, 1, "sample variance is zero"
	}
	W = num * num / (mSqSum * ss)
	if W > 1 {
		W = 1
	}
	// Royston transform for Shapiro-Francia (Royston 1993).
	u := math.Log(float64(n))
	muZ := -1.2725 + 1.0521*(math.Log(u)-u)
	sigmaZ := 1.0308 - 0.26758*(math.Log(u)+2/u)
	if sigmaZ <= 0 {
		return W, 0, 1, "Royston σ_z non-positive"
	}
	logOne := math.Log(1 - W)
	z = (logOne - muZ) / sigmaZ
	p = 1 - standardNormalCDF(z)
	return W, z, p, warn
}

// inverseNormalCDF returns Φ⁻¹(p) via the Beasley-Springer-Moro approximation
// (Moro 1995): three rational sub-domains, ≤ 1e-9 accuracy.
func inverseNormalCDF(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	// Beasley-Springer central region.
	if p > 0.08 && p < 0.92 {
		y := p - 0.5
		r := y * y
		num := (((-25.44106049637 *r + 41.39119773534) *r - 18.61500062529) *r + 2.50662823884) * y
		den := (((3.13082909833 *r - 21.06224101826) *r + 23.08336743743) *r - 8.47351093090) *r + 1
		return num / den
	}
	// Moro tails.
	var r float64
	if p < 0.5 {
		r = p
	} else {
		r = 1 - p
	}
	r = math.Log(-math.Log(r))
	const c0, c1, c2, c3, c4, c5, c6, c7, c8 = 0.3374754822726147, 0.9761690190917186, 0.1607979714918209,
		0.0276438810333863, 0.0038405729373609, 0.0003951896511919, 0.0000321767881768,
		0.0000002888167364, 0.0000003960315187
	x := c0 + r*(c1+r*(c2+r*(c3+r*(c4+r*(c5+r*(c6+r*(c7+r*c8)))))))
	if p < 0.5 {
		return -x
	}
	return x
}
