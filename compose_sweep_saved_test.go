package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_sweep_saved_test.go pins a SAVED sweep-bound model: a
// `fits[].from_sweep` fit with `save` writes its winning axes (model
// file feature sweep_axes), and a later request loading the file
// (`models: [{name, path}]`) reads them through sweep_axis() — in a
// filter, an ATTR_FORMULA, a window param, an AGG_CONSTANT expr, an
// optimize objective and a Materialize run — exactly as the batch that
// fitted it did; predict judges the loaded model's axes statically, as
// the runtime does.

const savedBestPath = "models/best.pulse-model.json"

// savedSweepRun runs the sweep-axis batch with the bound fit "best"
// saved, and returns the instance, its Fs, the batch's own readout row
// and the winning axes.
func savedSweepRun(t *testing.T) (*Pulse, afero.Fs, map[string]any, map[string]any) {
	t.Helper()
	fsys := fitsFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	c := sweepAxisBatch(t)
	c.Fits[0].Save = savedBestPath
	c.Fits = append(c.Fits, types.FitSpec{Name: "plain", Request: facadeFitRequest("0", "90"), Save: "models/plain.pulse-model.json"})
	out, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Ranking) != 2 || len(out.Responses) == 0 || out.Responses[0].Label != "readout" {
		t.Fatalf("ranking %+v responses %d", out.Ranking, len(out.Responses))
	}
	return p, fsys, out.Responses[0].Data[0], out.Ranking[0].Axes
}

// loadedReadout is sweepAxisBatch's readout slot as a plain request
// loading the saved model under "best".
func loadedReadout() *Request {
	return &Request{
		Cohort:     &types.Cohort{Filename: fitsFacadeCohort},
		Models:     []types.ModelRef{{Name: "best", Path: savedBestPath}, {Name: "plain", Path: "models/plain.pulse-model.json"}},
		Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `week < sweep_axis("best", "hi")`}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "mult", Expression: multiplierSrc}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "week", Label: "n"},
			{Type: types.AGG_MAX, Field: "mult", Label: "mult"},
			{Type: types.AGG_CONSTANT, Label: "tv", Params: json.RawMessage(`{"type":"f64","expr":"sweep_axis(\"best\", \"tv\")"}`)},
		},
	}
}

// TestSweepSaved_FileCarriesWinningAxes: the saved sweep-bound file lists
// feature sweep_axes holding the winning slot's axis values as raw JSON;
// the plain fit's file carries no sweep_axes.
func TestSweepSaved_FileCarriesWinningAxes(t *testing.T) {
	t.Parallel()

	_, fsys, _, win := savedSweepRun(t)
	data, err := afero.ReadFile(fsys, savedBestPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Features  []string                   `json:"features"`
		SweepAxes map[string]json.RawMessage `json:"sweep_axes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Features, []string{"sweep_axes"}) || len(doc.SweepAxes) != 2 {
		t.Fatalf("features %v sweep_axes %v:\n%s", doc.Features, doc.SweepAxes, data)
	}
	for k, v := range win {
		if string(doc.SweepAxes[k]) != v.(json.Number).String() {
			t.Errorf("sweep_axes[%s] = %s, winning %v", k, doc.SweepAxes[k], v)
		}
	}
	plain, err := afero.ReadFile(fsys, "models/plain.pulse-model.json")
	if err != nil {
		t.Fatal(err)
	}
	var pd map[string]any
	_ = json.Unmarshal(plain, &pd)
	if _, ok := pd["sweep_axes"]; ok {
		t.Fatalf("plain fit's file carries sweep_axes:\n%s", plain)
	}
}

// TestSweepSaved_LoadedReadsWinningAxes: a new request loading the saved
// model reads the winning axes — its filter keeps the same rows, its
// formula and its AGG_CONSTANT expr give the same figures, bit for bit,
// as the fitting batch's slot; predict judges it valid.
func TestSweepSaved_LoadedReadsWinningAxes(t *testing.T) {
	t.Parallel()

	p, _, row, win := savedSweepRun(t)
	ctx := context.Background()
	req := loadedReadout()
	if pr, err := p.Predict(ctx, req); err != nil || len(pr.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, pr)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	got := resp.Data[0]
	tv, _ := win["tv"].(json.Number).Float64()
	if got["n"] != row["n"] || math.Float64bits(got["mult"].(float64)) != math.Float64bits(row["mult"].(float64)) {
		t.Errorf("loaded readout n %v mult %v, fitting batch n %v mult %v", got["n"], got["mult"], row["n"], row["mult"])
	}
	if c, ok := got["tv"].(float64); !ok || math.Float64bits(c) != math.Float64bits(tv) {
		t.Errorf("AGG_CONSTANT sweep_axis tv = %v, winning %v", got["tv"], tv)
	}
}

// TestSweepSaved_WindowDecayReadsLoadedAxis: a WIN_DISTRIBUTED_LAG decay
// of sweep_axis("best", "tv") over the loaded model runs exactly as the
// literal winning decay; predict folds the loaded model's winning axis
// (nothing under unresolved_scalars), never an error.
func TestSweepSaved_WindowDecayReadsLoadedAxis(t *testing.T) {
	t.Parallel()

	p, _, _, win := savedSweepRun(t)
	ctx := context.Background()
	req := adstockSlot(strconv.Quote(winnerDecaySrc))
	req.Models = []types.ModelRef{{Name: "best", Path: savedBestPath}}
	pr, err := p.Predict(ctx, req)
	if err != nil || len(pr.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, pr)
	}
	if pr.UnresolvedScalars != nil {
		t.Fatalf("a loaded model's axis left unresolved: %+v", pr.UnresolvedScalars)
	}
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	lit, err := p.Process(ctx, adstockSlot(win["tv"].(json.Number).String()))
	if err != nil {
		t.Fatal(err)
	}
	g, _ := got.Data[0]["s"].(float64)
	w, _ := lit.Data[0]["s"].(float64)
	if math.Float64bits(g) != math.Float64bits(w) || g == 0 {
		t.Errorf("loaded-axis decay sum %v, literal %v", g, w)
	}
}

// TestSweepSaved_OptimizeAndMaterialize: an optimize block loading the
// saved model maximises the same objective the fitting batch did, and a
// Materialize run over the loaded model writes the same rows and
// formula column.
func TestSweepSaved_OptimizeAndMaterialize(t *testing.T) {
	t.Parallel()

	p, _, row, _ := savedSweepRun(t)
	ctx := context.Background()
	mult := row["mult"].(float64)

	batch := &ComposedRequest{
		Requests: []*Request{facadeFitSlot("s")},
		Optimize: []types.OptimizeSpec{{
			Name:      "spend",
			Models:    []types.ModelRef{{Name: "best", Path: savedBestPath}},
			Variables: []types.OptimizeVariable{{Name: "v", Lower: types.BoundValue(0), Upper: types.BoundValue(10)}},
			Objective: multiplierSrc + ` * v`,
			Direction: types.OptimizeMaximize,
		}},
	}
	if env, err := p.PredictCompose(ctx, batch); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict compose: %v %+v", err, env)
	}
	out, err := p.Compose(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Optimizations) != 1 || math.Abs(out.Optimizations[0].Objective-10*mult) > 1e-9*mult {
		t.Errorf("optimize %+v, want objective %v", out.Optimizations, 10*mult)
	}

	m := loadedReadout()
	m.Aggregations = nil
	if env, err := p.PredictMaterialize(ctx, m); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict materialize: %v %+v", err, env)
	}
	res, err := p.Materialize(ctx, m, "derived_best.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if float64(res.Records) != row["n"].(float64) {
		t.Errorf("materialized %d records, fitting batch kept %v", res.Records, row["n"])
	}
	back, err := p.Process(ctx, &Request{Cohort: &types.Cohort{Filename: "derived_best.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "mult", Label: "mult"}}})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := back.Data[0]["mult"].(float64); math.Float64bits(g) != math.Float64bits(mult) {
		t.Errorf("materialized mult %v, fitting batch %v", g, mult)
	}
}

// TestSweepSaved_RefusalsMatchPredict: over a loaded model, an unknown
// axis (axis_unknown, judged from the file's axes) and a file without
// sweep_axes (not_sweep_bound) are the same coded error in Predict,
// Process, PredictMaterialize / Materialize and an optimize block.
func TestSweepSaved_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, _, _, _ := savedSweepRun(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, src, reason string
	}{
		{"unknown axis", `sweep_axis("best", "decay")`, "axis_unknown"},
		{"not sweep bound", `sweep_axis("plain", "tv")`, "not_sweep_bound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := loadedReadout()
			req.Attributes[0].Expression = tc.src
			pr, err := p.Predict(ctx, req)
			if err != nil || len(pr.Errors) != 1 {
				t.Fatalf("predict: %v %+v", err, pr)
			}
			pe := pr.Errors[0]
			if pe.Code != string(perr.PULSE_MODEL_TERM_UNKNOWN) || pe.Details["reason"] != tc.reason {
				t.Fatalf("predict %s %v", pe.Code, pe.Details)
			}
			check := func(name string, err error) {
				var ce *perr.CodedError
				if !stderrors.As(err, &ce) {
					t.Fatalf("%s: uncoded %v", name, err)
				}
				if string(ce.Code) != pe.Code || ce.Details["reason"] != tc.reason || ce.Details["axis"] != pe.Details["axis"] {
					t.Errorf("%s: runtime %s %v, predict %s %v", name, ce.Code, ce.Details, pe.Code, pe.Details)
				}
			}
			_, err = p.Process(ctx, req)
			check("Process", err)
			m := loadedReadout()
			m.Aggregations = nil
			m.Attributes[0].Expression = tc.src
			env, err := p.PredictMaterialize(ctx, m)
			if err != nil || len(env.Errors) != 1 || env.Errors[0].Code != pe.Code || env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict materialize: %v %+v", err, env)
			}
			_, err = p.Materialize(ctx, m, "refused.pulse", MaterializeOptions{})
			check("Materialize", err)

			batch := &ComposedRequest{
				Requests: []*Request{facadeFitSlot("s")},
				Optimize: []types.OptimizeSpec{{
					Name:      "spend",
					Models:    []types.ModelRef{{Name: "best", Path: savedBestPath}, {Name: "plain", Path: "models/plain.pulse-model.json"}},
					Variables: []types.OptimizeVariable{{Name: "v", Lower: types.BoundValue(0), Upper: types.BoundValue(10)}},
					Objective: tc.src + ` * v`,
					Direction: types.OptimizeMaximize,
				}},
			}
			cenv, err := p.PredictCompose(ctx, batch)
			if err != nil || len(cenv.Errors) != 1 || cenv.Errors[0].Code != pe.Code || cenv.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict compose: %v %+v", err, cenv)
			}
			_, err = p.Compose(ctx, batch)
			check("Compose optimize", err)
		})
	}
}
