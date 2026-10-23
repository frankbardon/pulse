package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// regression_holdout_test.go: RegressionSpec.Holdout end to end
// (transformed-predictors E3-S1) — the predicate partitions the filtered
// records into fit / held-out / null rows, the fit runs on the fit rows
// only and the held-out rows are scored on the response scale; predict
// and Process refuse a bad predicate alike.

// holdoutCohort writes t (1..11), x = t, y and w as holdout.pulse:
// y = 2x + 1 exactly on t ≤ 7 (the rows a `t_lag >= 7` holdout fits),
// 18 / 17 / 24 on t = 8..10 (residuals +1 / −2 / +3 off the exact fit)
// and null on t = 11 (held out, unscorable). w is 1 on every fitted row
// and 1 / 2 / 0 / 1 on t = 8..11.
func holdoutCohort(t *testing.T) (*Service, []byte) {
	t.Helper()
	ys := map[int]float64{8: 18, 9: 17, 10: 24, 11: 0}
	ws := map[int]float64{8: 1, 9: 2, 10: 0, 11: 1}
	var rows [][]float64
	for i := 1; i <= 11; i++ {
		x := float64(i)
		y, w := 2*x+1, 1.0
		if v, ok := ys[i]; ok {
			y, w = v, ws[i]
		}
		rows = append(rows, []float64{float64(i), x, y, w})
	}
	cfg := fs.NewMemMap()
	writeF64Cohort(t, cfg, "holdout.pulse", []string{"t", "x", "y", "w"}, rows, func(r, c int) bool { return r == 10 && c == 2 })
	data, err := afero.ReadFile(cfg.Fs(), "holdout.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), data
}

// lagT is WIN_LAG of t in t order, labelled t_lag: null on t = 1.
func lagT() *types.Window {
	return &types.Window{Type: types.WIN_LAG, Field: "t", Label: "t_lag", OrderBy: []types.OrderKey{{Field: "t"}}}
}

func holdoutRequest(holdout string, mutate func(*types.RegressionSpec)) *types.Request {
	spec := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Holdout: holdout}
	if mutate != nil {
		mutate(spec)
	}
	return &types.Request{
		Cohort:      &types.Cohort{Filename: "holdout.pulse"},
		Windows:     []*types.Window{lagT()},
		Regressions: []*types.RegressionSpec{spec},
	}
}

// wantHoldout is the hand-folded figure set over (y, ŷ = 2x + 1, w).
func wantHoldout(ys, yhat, ws []float64) (rmse, mae, r2 float64) {
	var sq, ab, sw, wy float64
	for i := range ys {
		d := ys[i] - yhat[i]
		sq += ws[i] * d * d
		ab += ws[i] * math.Abs(d)
		sw += ws[i]
		wy += ws[i] * ys[i]
	}
	mean := wy / sw
	var sst float64
	for i := range ys {
		sst += ws[i] * (ys[i] - mean) * (ys[i] - mean)
	}
	return math.Sqrt(sq / sw), ab / sw, 1 - sq/sst
}

func holdoutClose(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

// TestHoldout_PartitionsFitsAndScores: a holdout reading a window label
// (t_lag, copied onto the records only because the holdout reads it)
// splits the rows — t = 1 null (n_null), t = 2..7 fitted (the exact line
// 2x + 1, n_obs 6), t = 8..11 held out — and the held-out figures are
// the hand fold over t = 8..10 (t = 11's null target drops listwise).
func TestHoldout_PartitionsFitsAndScores(t *testing.T) {
	svc, _ := holdoutCohort(t)
	resp, err := svc.Process(context.Background(), holdoutRequest("t_lag >= 7", nil))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	r := onlyRegression(t, resp)
	if r.NObs != 6 || !holdoutClose(r.Coefficients["x"], 2) || !holdoutClose(r.Coefficients["(intercept)"], 1) {
		t.Fatalf("fit n_obs %d, coefficients %v; want 6 rows on y = 2x + 1", r.NObs, r.Coefficients)
	}
	h := r.Holdout
	if h == nil {
		t.Fatal("holdout figures absent")
	}
	rmse, mae, r2 := wantHoldout([]float64{18, 17, 24}, []float64{17, 19, 21}, []float64{1, 1, 1})
	if h.N != 3 || h.NNull != 1 || !holdoutClose(h.RMSE, rmse) || !holdoutClose(h.MAE, mae) || !holdoutClose(h.R2, r2) {
		t.Fatalf("holdout = %+v; want n 3, n_null 1, rmse %v, mae %v, r2 %v", *h, rmse, mae, r2)
	}

	// Without the holdout the same slot fits every row and carries no
	// holdout block.
	plain, err := svc.Process(context.Background(), holdoutRequest("", nil))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if p := onlyRegression(t, plain); p.Holdout != nil || p.NObs != 10 {
		t.Fatalf("plain fit: holdout %+v, n_obs %d; want absent, 10", p.Holdout, p.NObs)
	}
}

// TestHoldout_ReadsPostWindowAttribute: the predicate reads a
// post-window attribute's label (an ATTR_FORMULA over the window label),
// partitioning the rows exactly as the window label itself does.
func TestHoldout_ReadsPostWindowAttribute(t *testing.T) {
	svc, data := holdoutCohort(t)
	req := holdoutRequest("lag2 >= 14", nil)
	req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "lag2", Expression: "t_lag == nil ? nil : t_lag * 2"}}
	if pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{}); len(pred.Errors) != 0 {
		t.Fatalf("predict refused: %+v", pred.Errors)
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	r := onlyRegression(t, resp)
	if r.NObs != 6 || r.Holdout == nil || r.Holdout.N != 3 || r.Holdout.NNull != 1 {
		t.Fatalf("n_obs %d, holdout %+v; want 6, n 3, n_null 1", r.NObs, r.Holdout)
	}
}

// TestHoldout_Weighted: under the slot weight the losses fold Σw·e² /
// Σw and r2 reads the weighted mean; the zero-weight held-out row
// (t = 10) is not scored and n stays a raw count.
func TestHoldout_Weighted(t *testing.T) {
	svc, _ := holdoutCohort(t)
	resp, err := svc.Process(context.Background(), holdoutRequest("t_lag >= 7", func(s *types.RegressionSpec) {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	r := onlyRegression(t, resp)
	h := r.Holdout
	rmse, mae, r2 := wantHoldout([]float64{18, 17}, []float64{17, 19}, []float64{1, 2})
	if h == nil || h.N != 2 || h.NNull != 1 || !holdoutClose(h.RMSE, rmse) || !holdoutClose(h.MAE, mae) || !holdoutClose(h.R2, r2) {
		t.Fatalf("holdout = %+v; want n 2, n_null 1, rmse %v, mae %v, r2 %v", h, rmse, mae, r2)
	}
}

// TestHoldout_EmptyFailsTheSlotAlone: a predicate holding out no
// scorable row fails the slot alone — its result carries the slot's code
// (PROCESSING_REGRESSION_INSUFFICIENT_DATA, reason holdout_empty) and
// the response warns PULSE_SLOT_FAILED — and Strict escalates it with
// the slot's own code.
func TestHoldout_EmptyFailsTheSlotAlone(t *testing.T) {
	for name, holdout := range map[string]string{"true on none": "t > 100", "only an unscorable row": "t == 11"} {
		t.Run(name, func(t *testing.T) {
			svc, _ := holdoutCohort(t)
			req := holdoutRequest(holdout, nil)
			req.Regressions = append(req.Regressions, &types.RegressionSpec{Name: "other", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}})
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			failed := resp.Regressions[0]
			if failed.Error == nil || failed.Error.Code != string(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA) ||
				failed.Error.Details["reason"] != "holdout_empty" || failed.Coefficients != nil {
				t.Fatalf("failed slot = %+v", failed)
			}
			if other := resp.Regressions[1]; other.Error != nil || other.NObs != 10 {
				t.Fatalf("sibling slot = %+v; want a plain fit", other)
			}
			warned := false
			for _, w := range resp.Warnings {
				warned = warned || w.Code == string(errors.PULSE_SLOT_FAILED)
			}
			if !warned {
				t.Fatalf("no PULSE_SLOT_FAILED warning: %+v", resp.Warnings)
			}

			svc.SetStrict(true)
			_, err = svc.Process(context.Background(), holdoutRequest(holdout, nil))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA {
				t.Fatalf("Strict Process err = %v, want PROCESSING_REGRESSION_INSUFFICIENT_DATA", err)
			}
		})
	}
}

// TestHoldout_PredictRuntimeParity: a predicate that does not compile as
// a boolean, names an unknown column, calls total() or reads a window
// label no record carries is refused by Process and by predict with the
// same code and message.
func TestHoldout_PredictRuntimeParity(t *testing.T) {
	svc, data := holdoutCohort(t)
	aggregated := func(holdout string) *types.Request {
		return &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "s"}},
			Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "s", Label: "lag_s", OrderBy: []types.OrderKey{{Field: "s"}}}},
			Regressions:  []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Holdout: holdout}},
		}
	}
	cases := map[string]struct {
		req  *types.Request
		code errors.Code
	}{
		"not boolean":       {holdoutRequest("x + 1", nil), errors.PULSE_FORMULA_COMPILE_ERROR},
		"unknown name":      {holdoutRequest("nope > 1", nil), errors.PULSE_FORMULA_COMPILE_ERROR},
		"not boolean lag":   {holdoutRequest("t_lag * 2", nil), errors.PULSE_FORMULA_COMPILE_ERROR},
		"total call":        {holdoutRequest("total(x) > 1", nil), errors.PULSE_FORMULA_TOTAL_SITE},
		"grouped window":    {aggregated("lag_s > 1"), errors.PULSE_REGRESSION_WINDOW_UNAVAILABLE},
		"unparseable":       {holdoutRequest("x >", nil), errors.PULSE_FORMULA_COMPILE_ERROR},
		"fine for contrast": {holdoutRequest("x > 7", nil), ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.req.Cohort = &types.Cohort{Filename: "holdout.pulse"}
			_, err := svc.Process(context.Background(), c.req)
			pred := descx.Predict(bytes.NewReader(data), c.req, &descx.PredictOptions{})
			if c.code == "" {
				if err != nil || len(pred.Errors) != 0 {
					t.Fatalf("Process err %v, predict errors %+v; want both to accept", err, pred.Errors)
				}
				return
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("Process err = %v, want %s", err, c.code)
			}
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message {
					found = true
				}
			}
			if !found {
				t.Fatalf("predict errors %+v lack %s %q", pred.Errors, ce.Code, ce.Message)
			}
		})
	}
}

// TestHoldout_ProjectsItsFields: a predicate over a schema field nothing
// else reads (t; no window widens the decode here) still sees it — the
// projected decode keeps the holdout's names.
func TestHoldout_ProjectsItsFields(t *testing.T) {
	svc, _ := holdoutCohort(t)
	svc.SetProjectBufferedFields(true)
	req := holdoutRequest("t >= 8", nil)
	req.Windows = nil
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	r := onlyRegression(t, resp)
	if r.NObs != 7 || r.Holdout == nil || r.Holdout.N != 3 || r.Holdout.NNull != 0 {
		t.Fatalf("n_obs %d, holdout %+v; want 7, n 3, n_null 0", r.NObs, r.Holdout)
	}
}
