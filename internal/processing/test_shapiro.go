package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// shapiroWilkRow implements TEST_SHAPIRO_WILK as a tier-1 buffered
// row test for normality: the Shapiro-Francia W' with Royston's (1993)
// z-transform — exactly nortest::sf.test, which pins it
// (TestReferenceOracle_ShapiroWilk, relative 1e-10).
//
//  1. Sort the sample x_(1) ≤ … ≤ x_(n).
//  2. Blom (1958) expected normal order statistics
//     m_i = Φ⁻¹((i − 3/8) / (n + 1/4)) (R's qnorm(ppoints(n, 3/8))).
//  3. W' = cor(x, m)² — the Shapiro-Francia simplification
//     a_i ∝ m_i of the Shapiro-Wilk weights.
//  4. Royston's transform to z; p = Φ(−z) (shapiroFranciaStat).
//
// Royston's transform is calibrated for 5 ≤ n ≤ 5000: n < 5 is refused
// (PULSE_TEST_INSUFFICIENT_N, a per-slot failure), n > 5000 runs with an
// advisory warning. A constant group has no W' (cor(x, m) is 0 / 0;
// sf.test answers NA) and is refused too (PULSE_TEST_VARIANCE_ZERO, a
// per-slot failure; U36 E1-S7 — it used to report W' = 1, p = 1). It carries the TEST_SHAPIRO_WILK name because it
// lands in the normality-test slot; R's shapiro.test (Royston 1995, AS
// R94 weights) is a different statistic, and the result's Variant
// reports "shapiro_francia".
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
	if cerr := fieldaccept.RequiredSlotsRefusal(spec.Type, false, fieldaccept.TestSlotsOf(spec)); cerr != nil {
		return nil, cerr
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
		if cerr := testFieldTypeRefusal(spec, schema); cerr != nil {
			return nil, cerr
		}
		if spec.SplitBy != "" {
			if cerr := fieldaccept.Check(string(spec.Type), fieldaccept.SlotSplitBy, spec.SplitBy, schema); cerr != nil {
				return nil, cerr
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
		k, ok := record.LevelValue(s.splitBy)
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
		if cerr := shapiroFranciaMinNRefusal("TEST_SHAPIRO_WILK", key, n); cerr != nil {
			return nil, cerr
		}
		sort.Float64s(vs)
		if cerr := shapiroFranciaZeroVarianceRefusal("TEST_SHAPIRO_WILK", key, vs); cerr != nil {
			return nil, cerr
		}
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

// shapiroFranciaMinN is the smallest sample Royston's Shapiro-Francia
// transform is calibrated for (nortest::sf.test refuses below it too).
const shapiroFranciaMinN = 5

// shapiroFranciaMinNRefusal refuses a group of n < shapiroFranciaMinN
// rows: PULSE_TEST_INSUFFICIENT_N, a data-dependent per-slot failure.
// op names the operator (and arm) in the message.
func shapiroFranciaMinNRefusal(op, group string, n int) *errors.CodedError {
	if n >= shapiroFranciaMinN {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
		fmt.Sprintf("%s requires n ≥ %d per group (Royston's Shapiro-Francia p is uncalibrated below it); %q has %d",
			op, shapiroFranciaMinN, group, n),
		map[string]any{"group": group, "n": n, "min_required": shapiroFranciaMinN})
}

// shapiroFranciaZeroVarianceRefusal refuses a constant *sorted* group:
// PULSE_TEST_VARIANCE_ZERO, a data-dependent per-slot failure. The test
// is exact — first = last — so a constant whose float mean is inexact
// (a run of 0.1) never leaks a rounding-residue W'.
func shapiroFranciaZeroVarianceRefusal(op, group string, sorted []float64) *errors.CodedError {
	if len(sorted) == 0 || sorted[0] != sorted[len(sorted)-1] {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
		fmt.Sprintf("%s: group %q is constant (zero variance), so W' is undefined", op, group),
		map[string]any{"group": group, "n": len(sorted), "value": sorted[0]})
}

// shapiroFranciaStat computes W', z, p for a *sorted*, non-constant
// sample of n ≥ shapiroFranciaMinN rows (the callers refuse smaller and
// constant ones) — the arithmetic of nortest::sf.test. Returns the
// optional warning string for n above 5000 or a degenerate sample.
//
// Blom expected order statistics: m_i = Φ⁻¹((i − 3/8) / (n + 1/4)).
// W' = cor(x, m)² = (Σ (x_i − x̄)(m_i − m̄))² / (Σ (x_i − x̄)² Σ (m_i − m̄)²).
//
// Royston's transformation for Shapiro-Francia (Royston 1993):
//
//	u = log(n)
//	μ_z   = −1.2725 + 1.0521(log(u) − u)
//	σ_z   = 1.0308 − 0.26758(log(u) + 2/u)
//	y     = log(1 − W')
//	z     = (y − μ_z) / σ_z
//	p     = Φ(−z)
func shapiroFranciaStat(sorted []float64) (W, z, p float64, warn string) {
	n := len(sorted)
	if n > 5000 {
		warn = "n above the 5000-row support bound; treat the p-value as advisory and consider asymptotic alternatives"
	}
	// Expected normal order statistics (R: qnorm(ppoints(n, a = 3/8))).
	m := make([]float64, n)
	var meanM, meanX float64
	for i := range n {
		frac := (float64(i+1) - 0.375) / (float64(n) + 0.25)
		m[i] = statdist.NormalQuantile(frac)
		meanM += m[i]
		meanX += sorted[i]
	}
	meanM /= float64(n)
	meanX /= float64(n)
	var ssX, ssM, num float64
	for i, v := range sorted {
		dx, dm := v-meanX, m[i]-meanM
		ssX += dx * dx
		ssM += dm * dm
		num += dx * dm
	}
	if ssM == 0 {
		return 0, 0, 1, "degenerate Blom coefficients"
	}
	W = num * num / (ssM * ssX)
	if W > 1 {
		W = 1
	}
	u := math.Log(float64(n))
	muZ := -1.2725 + 1.0521*(math.Log(u)-u)
	sigmaZ := 1.0308 - 0.26758*(math.Log(u)+2/u)
	if sigmaZ <= 0 {
		return W, 0, 1, "Royston σ_z non-positive"
	}
	logOne := math.Log(1 - W)
	z = (logOne - muZ) / sigmaZ
	p = normalUpperTailP(z)
	return W, z, p, warn
}
