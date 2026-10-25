package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem_ordinal_test.go is MAT_SEM's ordinal estimators (params.estimator
// "dwls" / "wlsmv", params.ordered) across predict and the runtime:
// predict reports the buffered downgrade the runtime takes, files the
// threshold rows' p-values in the spec's family on the right basis, and
// refuses what the runtime refuses; a Compose SEM fit over an ordinal
// battery is refused alike. The figures themselves are held to lavaan
// in internal/sem and internal/service.

var brandOrdered = []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3"}

// semFamily is the p-values a MAT_SEM result's estimates carry.
func semFamily(res types.MatrixResult) (n, thresholds int) {
	est := res.Auxiliary["estimates"]
	for i, row := range est.Values {
		if !math.IsNaN(row[4]) {
			n++
			if strings.Contains(est.RowKeys[i], " | ") {
				thresholds++
			}
		}
	}
	return n, thresholds
}

// TestSEM_OrdinalPredictMatchesRuntime: an ordinal spec predicts
// buffered (the runtime's verdict), and predict's p_values count the
// threshold rows: on f64 ratings (no schema bound) a lower bound the
// run meets or exceeds, basis lower_bound; on a packed_bool ordered
// member (two categories) the exact count, basis dictionary.
func TestSEM_OrdinalPredictMatchesRuntime(t *testing.T) {
	for _, c := range []struct {
		name    string
		wave    encoding.FieldType
		model   string
		ordered []string
		basis   string
	}{
		{"f64 ratings", encoding.FieldTypeCategoricalU8, "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3", brandOrdered, descriptor.PValueBasisLowerBound},
		{"packed_bool member", encoding.FieldTypePackedBool, "quality =~ q1 + q2 + q3 + wave", []string{"wave"}, descriptor.PValueBasisDictionary},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, fsys := brandGroupCohort{csv: "brand", wave: c.wave}.build(t, pulse.Options{})
			params, _ := json.Marshal(map[string]any{"model": c.model, "estimator": "wlsmv", "ordered": c.ordered})
			fields := []string{"q1", "q2", "q3"}
			if c.name == "f64 ratings" {
				fields = brandOrdered
			} else {
				fields = append(fields, "wave")
			}
			// A packed_bool member rides a vector coercing it to 0 / 1.
			req := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
				Vectors: []types.VectorSpec{{Name: "items", Fields: fields, Coerce: types.VectorCoerceBinary}},
				Matrices: []types.MatrixSpec{{
					Name: "sem", Type: types.MAT_SEM, Vector: "items", Params: params,
					Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm},
				}}}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req)
			if len(env.Errors) > 0 {
				t.Fatalf("predict refused: %s %s %v", env.Errors[0].Code, env.Errors[0].Message, env.Errors[0].Details)
			}
			pr := env.Data.(*descriptor.PredictResult)
			if pr.Streamable || len(pr.Matrices) != 1 || pr.Matrices[0].Streamable || pr.Matrices[0].Mergeable {
				t.Fatalf("predict streamable %v, matrix %+v: an ordinal spec buffers and never merges", pr.Streamable, pr.Matrices)
			}
			res := semGroupRun(t, p, req)
			if res.Error != nil {
				t.Fatalf("slot error %+v", res.Error)
			}
			m, th := semFamily(res)
			if th == 0 {
				t.Fatal("no threshold row carries a p: the check is vacuous")
			}
			if res.Multiplicity == nil || res.Multiplicity.M != m {
				t.Fatalf("echo %+v, want m %d", res.Multiplicity, m)
			}
			pv := pr.PValues
			if pv == nil || pv.Basis != c.basis {
				t.Fatalf("predict p_values %+v, want basis %s", pv, c.basis)
			}
			switch c.basis {
			case descriptor.PValueBasisDictionary:
				if pv.Total != m {
					t.Errorf("predict total %d, runtime %d", pv.Total, m)
				}
			default:
				if pv.Total > m || pv.Total < m-th+len(c.ordered) {
					t.Errorf("predict total %d, runtime %d (%d thresholds): not the lower bound", pv.Total, m, th)
				}
			}
		})
	}
}

// TestSEM_OrdinalRefusalsMatchPredict: every ordinal config refusal is
// the same entry in predict; an ordinal battery in a Compose fit is
// PULSE_FIT_INVALID sem_ordinal in Compose and PredictCompose alike.
func TestSEM_OrdinalRefusalsMatchPredict(t *testing.T) {
	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, params := range []map[string]any{
		{"group": nil, "estimator": "wlsmv"},
		{"group": nil, "ordered": brandOrdered},
		{"group": nil, "estimator": "probit"},
		{"estimator": "wlsmv", "ordered": brandOrdered},
		{"group": nil, "estimator": "dwls", "ordered": brandOrdered, "solver": "slsqp"},
		{"group": nil, "estimator": "dwls", "ordered": []string{"quality"}},
	} {
		req := semGroupRequest(t, "brand_ord", params)
		_, err := p.Process(ctx, req)
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" {
			t.Errorf("%v: %v, want bad_params", params, err)
			continue
		}
		sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req), err)
	}
	fit := semGroupRequest(t, "brand_ord", map[string]any{"group": nil, "estimator": "wlsmv", "ordered": brandOrdered})
	creq := &types.ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: fit}},
		Requests: []*types.Request{{Cohort: &types.Cohort{Filename: "brand.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q1"}}}}}
	_, err := p.Compose(ctx, creq)
	var ce *errors.CodedError
	if !asCoded(err, &ce) || ce.Code != errors.PULSE_FIT_INVALID || ce.Details["reason"] != "sem_ordinal" {
		t.Fatalf("compose: %v (%v), want PULSE_FIT_INVALID sem_ordinal", err, semDetailsOf(ce))
	}
	penv, perr := p.PredictCompose(ctx, creq)
	if perr != nil {
		t.Fatal(perr)
	}
	sameEntry(t, penv, err)
}
