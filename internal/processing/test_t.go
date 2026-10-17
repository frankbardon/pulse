package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/internal/weighting"
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
// Confidence interval bounds use the inverse t quantile at alpha
// (statdist.StudentTInverseTwoSided: bracketed Newton on log statdist.StudentTTwoSidedP).
type tTestRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field   string
	splitBy string
	alpha   float64
	mu      float64
	one     bool
	// reference is params.reference (two-sample): the split_by level
	// read second, so diff = other − reference ("" = alphabetical).
	reference string
	// cuped is the params.covariate adjustment's state (two-sample;
	// nil without a covariate): it replaces groups as the fold target.
	cuped *cupedArms

	// groups holds per-key weighted Welford state in first-seen
	// insertion order so Finalize can return groups deterministically.
	groups map[string]*weighting.Welford
	order  []string

	// testWeight is the slot's stamped weight: unweighted every row
	// weighs 1; weighted, each group's moments are read on w* (N*_g).
	testWeight
}

// tTestParams is the JSON payload for TEST_T.Params. Mu is honored only
// for the one-sample variant; Reference and Covariate (refused without
// split_by) only for the two-sample one.
type tTestParams struct {
	Mu        float64 `json:"mu"`
	Reference string  `json:"reference"`
	Covariate string  `json:"covariate"`
}

// twoGroupParams is the params payload of a test whose only param is
// params.reference (TEST_MANN_WHITNEY_U): the split_by level every
// signed figure is read against. The mean tests read meanTestParams.
type twoGroupParams struct {
	Reference string `json:"reference"`
}

// decodeTwoGroupParams reads a twoGroupParams test's params (absent =
// no reference) and refuses a reference that names no split_by level
// (fieldaccept.ReferenceRefusal, the refusal predict raises).
func decodeTwoGroupParams(spec *types.Test, schema *encoding.Schema) (string, error) {
	var p twoGroupParams
	if err := paramread.DecodeOptional(paramread.Test, string(spec.Type), spec.Params, &p); err != nil {
		return "", errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(spec.Type)+" params: "+err.Error())
	}
	if cerr := fieldaccept.ReferenceRefusal(string(spec.Type), spec.SplitBy, p.Reference, schema); cerr != nil {
		return "", cerr
	}
	return p.Reference, nil
}

// referenceOrder orders a two-group test's two sorted split keys so the
// non-reference group is first (bucket a) and params.reference second
// (bucket b): every signed figure then reads other − reference. An
// empty reference keeps the alphabetical order. Neither key being the
// reference (a level the filtered data — or this group of a grouped
// run — never shows) is PULSE_TEST_REFERENCE_MISSING.
func referenceOrder(op types.TestType, reference string, keys []string) ([]string, error) {
	switch reference {
	case "", keys[1]:
		return keys, nil
	case keys[0]:
		return []string{keys[1], keys[0]}, nil
	}
	return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_REFERENCE_MISSING,
		fmt.Sprintf("%s params.reference %q is neither observed split group (%q, %q)", op, reference, keys[0], keys[1]),
		map[string]any{"type": string(op), "reference": reference, "groups": keys})
}

// noteReference echoes params.reference in Details (absent when unset,
// so a request without one is byte-identical).
func noteReference(details map[string]any, reference string) {
	if reference != "" {
		details["reference"] = reference
	}
}

func newTTestRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
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
	var mu float64
	var reference, covariate string
	if spec.Type == types.TEST_T {
		var p tTestParams
		if err := paramread.DecodeOptional(paramread.Test, string(spec.Type), spec.Params, &p); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"TEST_T params: "+err.Error())
		}
		mu, reference, covariate = p.Mu, p.Reference, p.Covariate
		if cerr := fieldaccept.ReferenceRefusal(string(spec.Type), spec.SplitBy, reference, schema); cerr != nil {
			return nil, cerr
		}
		if cerr := fieldaccept.CovariateRefusal(string(spec.Type), spec.Field, spec.SplitBy, covariate, schema); cerr != nil {
			return nil, cerr
		}
	} else {
		p, err := decodeMeanTestParams(spec, schema)
		if err != nil {
			return nil, err
		}
		reference, covariate = p.Reference, p.Covariate
	}
	return &tTestRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		splitBy:    spec.SplitBy,
		alpha:      alpha,
		mu:         mu,
		reference:  reference,
		cuped:      newCupedArms(covariate),
		one:        spec.SplitBy == "",
		groups:     make(map[string]*weighting.Welford),
		testWeight: newTestWeight(spec),
	}, nil
}

// UpdateRow folds one record into the appropriate Welford bucket. Rows
// with a null Field or (for two-sample) a null SplitBy are skipped; a
// weighted test then skips a row whose weight is invalid or zero.
func (tt *tTestRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(tt.field)
	if !ok {
		return nil
	}
	var key string
	if tt.one {
		key = ""
	} else {
		s, ok := record.LevelValue(tt.splitBy)
		if !ok {
			return nil
		}
		key = s
	}
	if tt.cuped != nil {
		x, ok := tt.cuped.covariateValue(record)
		if !ok {
			return nil
		}
		w, ok := tt.rowWeight(record)
		if !ok {
			return nil
		}
		if tt.cuped.add(key, x, v, w) {
			tt.order = append(tt.order, key)
		}
		return nil
	}
	w, ok := tt.rowWeight(record)
	if !ok {
		return nil
	}
	b, exists := tt.groups[key]
	if !exists {
		b = &weighting.Welford{}
		tt.groups[key] = b
		tt.order = append(tt.order, key)
	}
	b.Add(v, w)
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
	tt.groups = make(map[string]*weighting.Welford)
	tt.order = nil
	tt.cuped.reset()
}

func (tt *tTestRow) finalizeOneSample() (*types.TestResult, error) {
	b, ok := tt.groups[""]
	if !ok || b.N < 2 {
		var n int64
		if ok {
			n = b.N
		}
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_T one-sample requires n ≥ 2, got %d", n),
			map[string]any{"n": n, "min_required": 2})
	}
	variance := b.Variance(tt.basis)
	if variance == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_T one-sample: sample variance is zero",
			map[string]any{"n": b.N, "mean": b.Mean})
	}
	nStar := b.NStar(tt.basis)
	sd := math.Sqrt(variance)
	se := sd / math.Sqrt(nStar)
	tstat := (b.Mean - tt.mu) / se
	df := nStar - 1
	p := statdist.StudentTTwoSidedP(tstat, df)
	tcrit := statdist.StudentTInverseTwoSided(tt.alpha, df)
	ciLow := b.Mean - tcrit*se
	ciHigh := b.Mean + tcrit*se
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
			"n":        b.N,
			"mean":     b.Mean,
			"variance": variance,
			"ci_low":   ciLow,
			"ci_high":  ciHigh,
		},
	}
	setEffectSize(res.Details, "cohens_d", cohensDOneSample(b.Mean, tt.mu, sd))
	tt.noteScalar(res.Details, b.SumW, b.SumWSq)
	tt.checkNEff(tt.spec, false, "", b.NEff(), 2)
	return res, nil
}

func (tt *tTestRow) finalizeTwoSample() (*types.TestResult, error) {
	// Use sorted key set so deterministic ordering survives across the
	// underlying hash-map iteration. order preserves first-seen sequence
	// but ordering test labels alphabetically is the more standard
	// scientific convention; params.reference then moves its level
	// second (referenceOrder), before any arithmetic.
	keys := append([]string(nil), tt.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_T two-sample requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_T two-sample sees %d groups", len(keys))+remedyAnovaForK(nil),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	keys, err := referenceOrder(tt.spec.Type, tt.reference, keys)
	if err != nil {
		return nil, err
	}
	a, b := tt.groups[keys[0]], tt.groups[keys[1]]
	// Under params.covariate the test runs on the adjusted outcome's
	// moments (test_cuped.go).
	var cf *cupedFacts
	if tt.cuped != nil {
		adjA, adjB, f := tt.cuped.adjust(tt.cuped.groups[keys[0]], tt.cuped.groups[keys[1]])
		a, b, cf = &adjA, &adjB, &f
	}
	if a.N < 2 || b.N < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_T two-sample requires n ≥ 2 per group, got %d / %d", a.N, b.N),
			map[string]any{"n": []int64{a.N, b.N}, "min_required": 2})
	}
	va := a.Variance(tt.basis)
	vb := b.Variance(tt.basis)
	if va == 0 && vb == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_T two-sample: both groups have zero variance",
			map[string]any{"groups": keys, "means": []float64{a.Mean, b.Mean}})
	}
	// N*_g per group (n_g unweighted): Welch–Satterthwaite on w*.
	na := a.NStar(tt.basis)
	nb := b.NStar(tt.basis)
	se := math.Sqrt(va/na + vb/nb)
	if se == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_T two-sample: standard error is zero",
			map[string]any{"groups": keys})
	}
	diff := a.Mean - b.Mean
	tstat := diff / se
	// Welch-Satterthwaite degrees of freedom.
	num := va/na + vb/nb
	den := (va*va)/(na*na*(na-1)) + (vb*vb)/(nb*nb*(nb-1))
	df := (num * num) / den
	p := statdist.StudentTTwoSidedP(tstat, df)
	tcrit := statdist.StudentTInverseTwoSided(tt.alpha, df)
	ciLow := diff - tcrit*se
	ciHigh := diff + tcrit*se
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
			"n":        []int64{a.N, b.N},
			"mean":     []float64{a.Mean, b.Mean},
			"variance": []float64{va, vb},
			"diff":     diff,
			"ci_low":   ciLow,
			"ci_high":  ciHigh,
		},
	}
	noteReference(res.Details, tt.reference)
	if cf != nil {
		cf.note(res.Details, tt.basis, num)
	}
	setEffectSize(res.Details, "cohens_d", cohensDTwoSample(diff, na, va, nb, vb))
	tt.noteGroups(res.Details, []*weighting.Welford{a, b})
	tt.checkNEff(tt.spec, true, keys[0], a.NEff(), 2)
	tt.checkNEff(tt.spec, true, keys[1], b.NEff(), 2)
	return res, nil
}
