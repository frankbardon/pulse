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

// propZRow implements TEST_PROP_Z as a streaming row test: a
// two-proportion z-test on the conversion (success) rate of a
// categorical field, split into two groups by SplitBy.
//
// Spec:
//
//	field    — categorical column whose dictionary contains the success value
//	split_by — categorical column producing exactly two groups
//	params   — {"success": "<dictionary value>"}
//
// State is a pair of counters per split group: (n, successes). Each
// record contributes ±1 to either successes (when field == success
// value) or just to n. Finalize emits the pooled z-statistic, the
// per-group rates, and a Wald confidence interval on the rate diff.
type propZRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	success string
	alpha   float64

	groups map[string]*propGroup
	order  []string
}

type propGroup struct {
	n         int64
	successes int64
}

type propZParams struct {
	Success string `json:"success"`
}

func newPropZRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PROP_Z requires field (categorical outcome column)")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PROP_Z requires split_by (categorical group column)")
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
	var params propZParams
	if len(spec.Params) > 0 {
		if err := json.Unmarshal(spec.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"TEST_PROP_Z params: "+err.Error())
		}
	}
	if params.Success == "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SUCCESS_VALUE_MISSING,
			"TEST_PROP_Z requires params.success (the dictionary value treated as a success)",
			map[string]any{"type": string(types.TEST_PROP_Z)})
	}
	if schema != nil {
		for axis, name := range map[string]string{"field": spec.Field, "split_by": spec.SplitBy} {
			if f := schema.Field(name); f != nil && !f.Type.IsCategorical() {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("TEST_PROP_Z %s %q must be categorical, got %s", axis, name, f.Type.String()),
					map[string]any{"axis": axis, "field": name, "field_type": f.Type.String()})
			}
		}
	}
	return &propZRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		success: params.Success,
		alpha:   alpha,
		groups:  make(map[string]*propGroup),
	}, nil
}

func (p *propZRow) UpdateRow(record *Record) error {
	outcome, oOk := record.StringValue(p.field)
	if !oOk {
		return nil
	}
	key, kOk := record.StringValue(p.splitBy)
	if !kOk {
		return nil
	}
	g, exists := p.groups[key]
	if !exists {
		g = &propGroup{}
		p.groups[key] = g
		p.order = append(p.order, key)
	}
	g.n++
	if outcome == p.success {
		g.successes++
	}
	return nil
}

func (p *propZRow) Finalize() (*types.TestResult, error) {
	defer p.reset()
	keys := append([]string(nil), p.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_PROP_Z requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_PROP_Z sees %d groups; specify a two-group SplitBy or use TEST_CHISQ", len(keys)),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	a, b := p.groups[keys[0]], p.groups[keys[1]]
	if a.n < 1 || b.n < 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PROP_Z requires n ≥ 1 per group, got %d / %d", a.n, b.n),
			map[string]any{"n": []int64{a.n, b.n}})
	}
	na, nb := float64(a.n), float64(b.n)
	pa := float64(a.successes) / na
	pb := float64(b.successes) / nb
	pooled := float64(a.successes+b.successes) / (na + nb)
	if pooled == 0 || pooled == 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_PROP_Z: pooled proportion is 0 or 1; z-statistic undefined",
			map[string]any{"pooled": pooled})
	}
	sePool := math.Sqrt(pooled * (1 - pooled) * (1/na + 1/nb))
	z := (pa - pb) / sePool
	pvalue := 2 * (1 - standardNormalCDF(math.Abs(z)))
	// Wald (unpooled) standard error for the CI on the rate diff.
	seUnpool := math.Sqrt(pa*(1-pa)/na + pb*(1-pb)/nb)
	// Two-sided normal critical value at alpha/2 via the inverse erf.
	zcrit := math.Sqrt2 * inverseErf(1-p.alpha)
	ciLow := (pa - pb) - zcrit*seUnpool
	ciHigh := (pa - pb) + zcrit*seUnpool
	return &types.TestResult{
		Label:      testLabel(p.spec),
		Type:       types.TEST_PROP_Z,
		Variant:    "two_proportion_pooled",
		Statistic:  z,
		PValue:     pvalue,
		Alpha:      p.alpha,
		RejectNull: pvalue < p.alpha,
		Details: map[string]any{
			"success":    p.success,
			"groups":     keys,
			"n":          []int64{a.n, b.n},
			"successes":  []int64{a.successes, b.successes},
			"proportion": []float64{pa, pb},
			"diff":       pa - pb,
			"pooled":     pooled,
			"ci_low":     ciLow,
			"ci_high":    ciHigh,
		},
	}, nil
}

func (p *propZRow) reset() {
	p.groups = make(map[string]*propGroup)
	p.order = nil
}

// inverseErf approximates erf⁻¹(x) via the Winitzki rational
// approximation. Used to derive the two-sided normal critical value
// for the CI bounds: z_{α/2} = √2 · erf⁻¹(1 − α). Accurate to ~1.5e-3
// over the alpha range used by this test (α ∈ (0, 1)).
func inverseErf(x float64) float64 {
	if x <= -1 {
		return math.Inf(-1)
	}
	if x >= 1 {
		return math.Inf(1)
	}
	const a = 0.147
	lnTerm := math.Log(1 - x*x)
	first := 2/(math.Pi*a) + lnTerm/2
	inner := first*first - lnTerm/a
	root := math.Sqrt(math.Sqrt(inner) - first)
	if x < 0 {
		return -root
	}
	return root
}
