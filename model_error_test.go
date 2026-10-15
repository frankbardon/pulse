package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// model_error_test.go pins the model-error aggregators (AGG_MAPE /
// AGG_RMSE / AGG_MAE, fit-scoring E2-S1) at the facade: a holdout split
// over week_index with an existing range grouper, and predict / runtime
// parity for every refusal.

const merrCohort = "merr_weeks.pulse"

// merrHoldoutBump is the deviation the holdout weeks carry off the
// exact training line y = 5 + 2x. Train weeks lie ON the line, so the
// fit recovers it exactly (to rounding) and the train error is ~0.
func merrHoldoutBump(i int) float64 { return float64((i%5)-2) * 1.5 }

func merrX(i int) float64 { return float64((i*37)%17) + 1 }

func merrFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := 0; i <= 103; i++ {
		x := merrX(i)
		y := 5 + 2*x
		if i >= 91 {
			y += merrHoldoutBump(i)
		}
		rows = append(rows, []string{strconv.Itoa(i), jsonNumber(x), jsonNumber(y), []string{"north", "south"}[i%2]})
	}
	createTestPulseFile(t, fsys, merrCohort, []string{"week_index", "x", "y", "region"}, rows)
	return fsys
}

func merrSlot() *Request {
	agg := func(op types.AggregationType, label string) *types.Aggregation {
		return &types.Aggregation{Type: op, Field: "y", Label: label, Params: json.RawMessage(`{"prediction_field":"pred"}`)}
	}
	return &Request{
		Label:      "score",
		Cohort:     &types.Cohort{Filename: merrCohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "pred", Params: json.RawMessage(`{"fit":"m"}`)}},
		// week_index 0–90 / 91–103: GROUP_RANGE's half-open [0, 91) and
		// [91, 182) buckets.
		Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "week_index", Interval: 91}},
		Aggregations: []*types.Aggregation{agg(types.AGG_MAPE, "mape"), agg(types.AGG_RMSE, "rmse"), agg(types.AGG_MAE, "mae")},
	}
}

// TestModelError_HoldoutSplitInOneSlot: one compose fit on weeks 0–90,
// one slot scoring every week and grouping by week_index range, answers
// the train and the holdout error side by side. The oracle is computed
// from the generating constants, never from Pulse: holdout residual
// y − ŷ is exactly the bump (the fit is the generating line).
func TestModelError_HoldoutSplitInOneSlot(t *testing.T) {
	p, err := New(Options{FS: merrFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := &ComposedRequest{
		Fits: []types.FitSpec{{Name: "m", Request: &Request{
			Cohort:      &types.Cohort{Filename: merrCohort},
			Filterers:   []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week_index", Values: []string{"0", "90"}}},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}}},
		}}},
		Requests: []*Request{merrSlot()},
	}
	resp, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if fit := resp.Fits["m"]; fit == nil || fit.NObs != 91 {
		t.Fatalf("fit = %+v, want n = 91 (weeks 0–90)", fit)
	}
	var sumAbs, sumSq, sumPct float64
	for i := 91; i <= 103; i++ {
		d := merrHoldoutBump(i)
		y := 5 + 2*merrX(i) + d
		sumAbs += math.Abs(d)
		sumSq += d * d
		sumPct += math.Abs(d) / math.Abs(y)
	}
	const nHold = 13
	want := map[string]map[string]float64{
		"0-91":   {"mape": 0, "rmse": 0, "mae": 0},
		"91-182": {"mape": sumPct / nHold, "rmse": math.Sqrt(sumSq / nHold), "mae": sumAbs / nHold},
	}
	data := resp.Responses[0].Data
	if len(data) != 2 {
		t.Fatalf("data = %v, want two week_index ranges", data)
	}
	for _, row := range data {
		w, ok := want[row["week_index"].(string)]
		if !ok {
			t.Fatalf("unexpected bucket %v", row["week_index"])
		}
		for label, wv := range w {
			got := row[label].(float64)
			if math.Abs(got-wv) > 1e-9*math.Max(1, wv) {
				t.Errorf("%s %s = %.17g, want %.17g", row["week_index"], label, got, wv)
			}
		}
	}
	if want["91-182"]["mape"] < 0.01 {
		t.Fatalf("fixture: holdout MAPE %v too small to tell the buckets apart", want["91-182"]["mape"])
	}
}

// TestModelError_PredictMatchesRuntime: every model-error refusal
// predict reports is exactly the runtime's — code, message, details —
// and every accepted slot is accepted by both.
func TestModelError_PredictMatchesRuntime(t *testing.T) {
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, "merr_types.pulse", []string{"y", "p", "region"},
		[][]string{{"1", "1.5", "north"}, {"2", "2.5", "south"}, {"3", "2", "north"}})
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, field, params string
		code                perr.Code // "" = accepted
	}{
		{"accepted", "y", `{"prediction_field":"p"}`, ""},
		{"missing prediction_field", "y", `{}`, perr.PROCESSING_CONFIG},
		{"malformed params", "y", `{"prediction_field":3}`, perr.PROCESSING_CONFIG},
		{"categorical prediction", "y", `{"prediction_field":"region"}`, perr.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"categorical actual", "region", `{"prediction_field":"p"}`, perr.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"unknown prediction column", "y", `{"prediction_field":"nope"}`, perr.SERVICE_VALIDATION},
	}
	ctx := context.Background()
	for _, op := range []types.AggregationType{types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE} {
		for _, tc := range cases {
			t.Run(string(op)+"/"+tc.name, func(t *testing.T) {
				req := &Request{
					Cohort:       &types.Cohort{Filename: "merr_types.pulse"},
					Aggregations: []*types.Aggregation{{Type: op, Field: tc.field, Label: "e", Params: json.RawMessage(tc.params)}},
				}
				data, err := afero.ReadFile(fsys, "merr_types.pulse")
				if err != nil {
					t.Fatal(err)
				}
				env, err := p.PredictBytes(ctx, data, req)
				if err != nil {
					t.Fatal(err)
				}
				_, rerr := p.Process(ctx, req)
				if tc.code == "" {
					if len(env.Errors) != 0 || rerr != nil {
						t.Fatalf("accepted slot refused: predict %+v runtime %v", env.Errors, rerr)
					}
					return
				}
				var ce *perr.CodedError
				if !stderrors.As(rerr, &ce) || ce.Code != tc.code {
					t.Fatalf("runtime err = %v, want %s", rerr, tc.code)
				}
				if len(env.Errors) == 0 {
					t.Fatalf("predict accepted what the runtime refused (%v)", rerr)
				}
				pe := env.Errors[0]
				if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
					t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
				}
			})
		}
	}
}
