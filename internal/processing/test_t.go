package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// tTestRow implements TEST_T (and the TEST_WELCH alias) as a streaming
// row test. It maintains per-group (n, mean, M2) state via Welford's
// recurrence, so the t-statistic is exact to float64 precision for
// well-conditioned inputs without ever materializing the value set.
//
// Two variants share one struct:
//
//   - One-sample: SplitBy empty. The test compares Field's mean against
//     the hypothesized μ supplied in params.mu (default 0). State is one
//     Welford bucket.
//   - Welch two-sample: SplitBy set. Each filter-passing row's split key
//     indexes a Welford bucket. After Finalize, the test compares the
//     two observed groups' means using the Welch-Satterthwaite degrees
//     of freedom approximation. Three or more groups surface as
//     PULSE_TEST_SPLIT_GROUPS_LT_2 (caller asked for two-sample on a
//     k>2 splitter — direct them to TEST_ANOVA_F).
//
// Confidence interval bounds use the inverse t quantile at alpha; the
// implementation searches studentTTwoSidedP via bisection.
type tTestRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field   string
	splitBy string
	alpha   float64
	mu      float64
	one     bool

	// groups holds per-key Welford state in first-seen insertion order
	// so Finalize can return groups deterministically.
	groups map[string]*welfordBucket
	order  []string
}

// tTestParams is the JSON payload for TEST_T.Params. Mu is honored only
// for the one-sample variant. Variant is reserved for future tail
// configuration ("two-sided" / "less" / "greater") and currently ignored.
type tTestParams struct {
	Mu      float64 `json:"mu"`
	Variant string  `json:"variant,omitempty"`
}

func newTTestRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_T requires field")
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
		if f := schema.Field(spec.Field); f != nil {
			if f.Type.IsCategorical() {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
					fmt.Sprintf("TEST_T field %q has non-numeric type %s", spec.Field, f.Type.String()),
					map[string]any{"field": spec.Field, "type": f.Type.String()})
			}
		}
		if spec.SplitBy != "" {
			if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("TEST_T split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
					map[string]any{"split_by": spec.SplitBy, "type": f.Type.String()})
			}
		}
	}
	var mu float64
	if len(spec.Params) > 0 {
		var p tTestParams
		if err := json.Unmarshal(spec.Params, &p); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"TEST_T params: "+err.Error())
		}
		mu = p.Mu
	}
	return &tTestRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		mu:      mu,
		one:     spec.SplitBy == "",
		groups:  make(map[string]*welfordBucket),
	}, nil
}

// UpdateRow folds one record into the appropriate Welford bucket. Rows
// with a null Field or (for two-sample) a null SplitBy are skipped.
func (tt *tTestRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(tt.field)
	if !ok {
		return nil
	}
	var key string
	if tt.one {
		key = ""
	} else {
		s, ok := record.StringValue(tt.splitBy)
		if !ok {
			return nil
		}
		key = s
	}
	b, exists := tt.groups[key]
	if !exists {
		b = &welfordBucket{}
		tt.groups[key] = b
		tt.order = append(tt.order, key)
	}
	b.add(v)
	return nil
}

// Finalize emits the TestResult for the accumulated state. Both variants
// share a common result envelope (statistic, df, p_value, alpha,
// reject_null); per-variant payload (per-group n/mean/variance, CI bounds,
// Cohen's d) lives in Details so the API surface stays predictable.
func (tt *tTestRow) Finalize() (*types.TestResult, error) {
	defer tt.reset()
	if tt.one {
		return tt.finalizeOneSample()
	}
	return tt.finalizeTwoSample()
}

func (tt *tTestRow) reset() {
	tt.groups = make(map[string]*welfordBucket)
	tt.order = nil
}

func (tt *tTestRow) finalizeOneSample() (*types.TestResult, error) {
	b, ok := tt.groups[""]
	if !ok || b.n < 2 {
		var n int64
		if ok {
			n = b.n
		}
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_T one-sample requires n ≥ 2, got %d", n),
			map[string]any{"n": n, "min_required": 2})
	}
	variance := b.sampleVariance()
	if variance == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_T one-sample: sample variance is zero",
			map[string]any{"n": b.n, "mean": b.mean})
	}
	sd := math.Sqrt(variance)
	se := sd / math.Sqrt(float64(b.n))
	tstat := (b.mean - tt.mu) / se
	df := float64(b.n - 1)
	p := studentTTwoSidedP(tstat, df)
	tcrit := studentTInverseTwoSided(tt.alpha, df)
	ciLow := b.mean - tcrit*se
	ciHigh := b.mean + tcrit*se
	res := &types.TestResult{
		Label:      testLabel(tt.spec),
		Type:       types.TEST_T,
		Variant:    "one_sample",
		Statistic:  tstat,
		DF:         df,
		PValue:     p,
		Alpha:      tt.alpha,
		RejectNull: p < tt.alpha,
		Details: map[string]any{
			"mu":       tt.mu,
			"n":        b.n,
			"mean":     b.mean,
			"variance": variance,
			"ci_low":   ciLow,
			"ci_high":  ciHigh,
		},
	}
	return res, nil
}

func (tt *tTestRow) finalizeTwoSample() (*types.TestResult, error) {
	// Use sorted key set so deterministic ordering survives across the
	// underlying hash-map iteration. order preserves first-seen sequence
	// but ordering test labels alphabetically is the more standard
	// scientific convention; tie-break to first-seen for stability.
	keys := append([]string(nil), tt.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_T two-sample requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_T two-sample sees %d groups; use TEST_ANOVA_F for k>2", len(keys)),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	a, b := tt.groups[keys[0]], tt.groups[keys[1]]
	if a.n < 2 || b.n < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_T two-sample requires n ≥ 2 per group, got %d / %d", a.n, b.n),
			map[string]any{"n": []int64{a.n, b.n}, "min_required": 2})
	}
	va := a.sampleVariance()
	vb := b.sampleVariance()
	if va == 0 && vb == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_T two-sample: both groups have zero variance",
			map[string]any{"groups": keys, "means": []float64{a.mean, b.mean}})
	}
	na := float64(a.n)
	nb := float64(b.n)
	se := math.Sqrt(va/na + vb/nb)
	if se == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_T two-sample: standard error is zero",
			map[string]any{"groups": keys})
	}
	diff := a.mean - b.mean
	tstat := diff / se
	// Welch-Satterthwaite degrees of freedom.
	num := va/na + vb/nb
	den := (va*va)/(na*na*(na-1)) + (vb*vb)/(nb*nb*(nb-1))
	df := (num * num) / den
	p := studentTTwoSidedP(tstat, df)
	tcrit := studentTInverseTwoSided(tt.alpha, df)
	ciLow := diff - tcrit*se
	ciHigh := diff + tcrit*se
	// Cohen's d via pooled standard deviation.
	pooled := math.Sqrt(((na-1)*va + (nb-1)*vb) / (na + nb - 2))
	var cohensD float64
	if pooled > 0 {
		cohensD = diff / pooled
	}
	res := &types.TestResult{
		Label:      testLabel(tt.spec),
		Type:       tt.spec.Type, // honor TEST_T vs TEST_WELCH alias
		Variant:    "welch_two_sample",
		Statistic:  tstat,
		DF:         df,
		PValue:     p,
		Alpha:      tt.alpha,
		RejectNull: p < tt.alpha,
		Details: map[string]any{
			"groups":   keys,
			"n":        []int64{a.n, b.n},
			"mean":     []float64{a.mean, b.mean},
			"variance": []float64{va, vb},
			"diff":     diff,
			"ci_low":   ciLow,
			"ci_high":  ciHigh,
			"effect_size": map[string]any{
				"cohens_d": cohensD,
			},
		},
	}
	return res, nil
}
