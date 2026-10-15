package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// categoricalRungs are the three categorical field types.
func categoricalRungs() []encoding.FieldType {
	return []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32}
}

// categoricalNumericReaders are the operators whose value is arithmetic
// on a field's stored number — over a categorical, its dictionary index
// (E2-S4, governing rule 1). Each body puts the field under judgement
// at $F.
var categoricalNumericReaders = map[string]string{
	"AGG_SUM":           `{"aggregations":[{"type":"AGG_SUM","field":"$F"}]}`,
	"AGG_AVERAGE":       `{"aggregations":[{"type":"AGG_AVERAGE","field":"$F"}]}`,
	"AGG_MIN":           `{"aggregations":[{"type":"AGG_MIN","field":"$F"}]}`,
	"AGG_MAX":           `{"aggregations":[{"type":"AGG_MAX","field":"$F"}]}`,
	"AGG_STDDEV":        `{"aggregations":[{"type":"AGG_STDDEV","field":"$F"}]}`,
	"AGG_VARIANCE":      `{"aggregations":[{"type":"AGG_VARIANCE","field":"$F"}]}`,
	"AGG_RANGE":         `{"aggregations":[{"type":"AGG_RANGE","field":"$F"}]}`,
	"AGG_ZSCORE":        `{"aggregations":[{"type":"AGG_ZSCORE","field":"$F"}]}`,
	"AGG_MEDIAN":        `{"aggregations":[{"type":"AGG_MEDIAN","field":"$F"}]}`,
	"AGG_PERCENTILE":    `{"aggregations":[{"type":"AGG_PERCENTILE","field":"$F","params":{"percentile":90}}]}`,
	"AGG_SKEWNESS":      `{"aggregations":[{"type":"AGG_SKEWNESS","field":"$F"}]}`,
	"AGG_KURTOSIS":      `{"aggregations":[{"type":"AGG_KURTOSIS","field":"$F"}]}`,
	"AGG_WEIGHTED_MEAN": `{"aggregations":[{"type":"AGG_WEIGHTED_MEAN","field":"$F","params":{"weight_field":"y"}}]}`,
	"AGG_DISTINCT_SUM":  `{"aggregations":[{"type":"AGG_DISTINCT_SUM","field":"$F","params":{"distinct_by":"subj"}}]}`,
	"AGG_CI_LOWER":      `{"aggregations":[{"type":"AGG_CI_LOWER","field":"$F","params":{"confidence":0.95,"method":"normal"}}]}`,
	"AGG_CI_UPPER":      `{"aggregations":[{"type":"AGG_CI_UPPER","field":"$F","params":{"confidence":0.95,"method":"normal"}}]}`,
	"AGG_RATIO num":     `{"aggregations":[{"type":"AGG_RATIO","field":"x","params":{"numerator_field":"$F","denominator_field":"y"}}]}`,
	"AGG_RATIO den":     `{"aggregations":[{"type":"AGG_RATIO","field":"x","params":{"numerator_field":"y","denominator_field":"$F"}}]}`,
	"FEAT_LOG":          `{"features":[{"type":"FEAT_LOG","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_SQRT":         `{"features":[{"type":"FEAT_SQRT","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_BUCKETIZE":    `{"features":[{"type":"FEAT_BUCKETIZE","field":"$F","params":{"quantiles":4}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"crosstab cell":     `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"subj"}],"cell":{"type":"AGG_SUM","field":"$F"}}}`,
	"crosstab margin":   `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"subj"}],"cell":{"type":"AGG_COUNT","field":"x"},"margin_aggregations":[{"type":"AGG_AVERAGE","field":"$F","label":"m"}]}}`,
	"grouped":           `{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_MEDIAN","field":"$F"}]}`,
}

// categoricalReaders are the aggregators where a categorical is
// meaningful — presence, the value's frequency, its mode, its distinct
// count — which keep accepting every categorical rung.
var categoricalReaders = map[string]string{
	"AGG_COUNT":          `{"aggregations":[{"type":"AGG_COUNT","field":"$F"}]}`,
	"AGG_NULL_COUNT":     `{"aggregations":[{"type":"AGG_NULL_COUNT","field":"$F"}]}`,
	"AGG_MODE":           `{"aggregations":[{"type":"AGG_MODE","field":"$F"}]}`,
	"AGG_MODE_COUNT":     `{"aggregations":[{"type":"AGG_MODE_COUNT","field":"$F"}]}`,
	"AGG_FREQUENCY":      `{"aggregations":[{"type":"AGG_FREQUENCY","field":"$F","params":{"value":"a"}}]}`,
	"AGG_DISTINCT_COUNT": `{"aggregations":[{"type":"AGG_DISTINCT_COUNT","field":"$F"}]}`,
}

func categoricalRequest(t *testing.T, body, cohort string) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	req.Cohort = &types.Cohort{Filename: cohort}
	return &req
}

// TestCategoricalNumericReaders_RuntimeRefusesPredictAgrees pins E2-S4:
// every numeric reader over a categorical rung is refused
// PULSE_FIELD_TYPE_UNSUPPORTED by Process (never a sum, mean or log of a
// dictionary index), and predict reports exactly that one error — no
// PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL warning beside it — at every
// slot that builds the operator.
func TestCategoricalNumericReaders_RuntimeRefusesPredictAgrees(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for name, tmpl := range categoricalNumericReaders {
		for _, ft := range categoricalRungs() {
			body := strings.ReplaceAll(tmpl, "$F", "t_"+ft.String())
			_, rerr := p.Process(ctx, categoricalRequest(t, body, cohort))
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
				t.Errorf("%s × %s runtime: %v, want %s", name, ft, rerr, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
			}
			env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, body, cohort))
			if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_FIELD_TYPE_UNSUPPORTED) {
				var got []string
				for _, e := range env.Errors {
					got = append(got, e.Code+" "+e.Message)
				}
				t.Errorf("%s × %s predict: %v, want one %s", name, ft, got, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
			}
			for _, w := range env.Warnings {
				if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
					t.Errorf("%s × %s predict: retired warning %s %q", name, ft, w.Code, w.Message)
				}
			}
		}
	}
}

// TestCategoricalReaders_StillRun pins the other verdict: the
// aggregators a categorical is meaningful for run on both sides, with
// no categorical warning (the cohort's other advisories aside).
func TestCategoricalReaders_StillRun(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for name, tmpl := range categoricalReaders {
		for _, ft := range categoricalRungs() {
			body := strings.ReplaceAll(tmpl, "$F", "t_"+ft.String())
			if _, rerr := p.Process(ctx, categoricalRequest(t, body, cohort)); rerr != nil {
				t.Errorf("%s × %s runtime refused: %v", name, ft, rerr)
			}
			env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, body, cohort))
			for _, e := range env.Errors {
				t.Errorf("%s × %s predict refused: %s %s", name, ft, e.Code, e.Message)
			}
			for _, w := range env.Warnings {
				if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
					t.Errorf("%s × %s predict: retired warning %q", name, ft, w.Message)
				}
			}
		}
	}
}

// TestFacetNotMeaningfulForCategorical_RuntimeMatchesPredict: a facet
// asking for numeric_percentiles / include_histogram over a categorical
// or packed_bool field computes neither for it (the discrete arm has no
// numeric summary), and says so with
// PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL — predict as an envelope
// warning, the runtime as a FacetResult.Warnings entry "CODE: message"
// (the facet result's warnings are strings, so details ride only
// predict). Same count and messages on both sides; a numeric field
// draws none.
func TestFacetNotMeaningfulForCategorical_RuntimeMatchesPredict(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	ctx := context.Background()
	code := string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL)
	for name, c := range map[string]struct {
		fields []string
		pcts   []float64
		hist   bool
		want   int
	}{
		"percentiles":  {[]string{"g", "x", "t_packed_bool"}, []float64{0.5}, false, 2},
		"histogram":    {[]string{"t_categorical_u16", "x"}, nil, true, 1},
		"both":         {[]string{"g", "t_packed_bool", "x"}, []float64{0.25, 0.75}, true, 4},
		"numeric only": {[]string{"x", "y"}, []float64{0.5}, true, 0},
		"neither":      {[]string{"g"}, nil, false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			req := func() *types.FacetRequest {
				r := &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: c.fields, NumericPercentiles: c.pcts}
				if c.hist {
					r.IncludeHistogram, r.HistogramRange = true, [2]float64{0, 10}
				}
				return r
			}
			env, err := p.PredictFacet(ctx, req())
			if err != nil || len(env.Errors) != 0 {
				t.Fatalf("predict: %v %+v", err, env)
			}
			var predicted []string
			for _, w := range env.Warnings {
				if w.Code == code {
					predicted = append(predicted, w.Code+": "+w.Message)
					if w.Details["field"] == nil || w.Details["type"] == nil {
						t.Fatalf("predict details %v, want field and type", w.Details)
					}
				}
			}
			res, err := p.FacetSchema(ctx, req())
			if err != nil {
				t.Fatalf("runtime: %v", err)
			}
			var ran []string
			for _, w := range res.Warnings {
				if strings.HasPrefix(w, code+": ") {
					ran = append(ran, w)
				}
			}
			if len(predicted) != c.want || strings.Join(predicted, "\n") != strings.Join(ran, "\n") {
				t.Fatalf("predict %q, runtime %q, want %d each", predicted, ran, c.want)
			}
		})
	}
}
