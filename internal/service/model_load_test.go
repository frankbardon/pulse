package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// model_load_test.go pins Request.models through the service: a loaded
// model scores bit for bit like the in-compose fit that saved it (OLS,
// GLM, Bayes), on every surface a Request runs (Process, ProcessStream,
// a chain stage, Facet, Materialize, a Compose slot and fit), across
// cohorts; and every load refusal fires before any record is read and
// before anything in a batch is written.

const loadPath = "models/m" + model.FileSuffix

// scorers are the scorable regression types, each fit on the fits
// cohort.
func scorers() map[string]types.RegressionSpec {
	return map[string]types.RegressionSpec{
		"ols":   {Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x", "week"}},
		"glm":   {Type: types.REG_GLM, Name: "m", Target: "week", Family: "poisson", Predictors: []string{"x"}},
		"bayes": {Type: types.REG_BAYES_LINEAR, Name: "m", Target: "y", Predictors: []string{"x"}},
	}
}

// scoreReq scores fit "m" on every row of the fits cohort and sums the
// scores; models loads it from a file (nil: a Compose fit binds it).
func scoreReq(label string, models []types.ModelRef) *types.Request {
	return &types.Request{
		Label:        label,
		Cohort:       &types.Cohort{Filename: fitsCohort},
		Models:       models,
		Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p", Label: "sum_p"}, {Type: types.AGG_COUNT, Field: "p", Label: "n"}},
	}
}

func loadM() []types.ModelRef { return []types.ModelRef{{Name: "m", Path: loadPath}} }

// materializeScores returns the per-row ATTR_REG_PREDICT scores of a
// materialize run of scoreReq's attribute under ctx.
func materializeScores(t *testing.T, svc *Service, ctx context.Context, models []types.ModelRef) []float64 {
	t.Helper()
	req := &types.Request{
		Cohort:     &types.Cohort{Filename: fitsCohort},
		Models:     models,
		Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
	}
	sink := &valueSink{}
	if err := svc.Materialize(ctx, req, sink); err != nil {
		t.Fatal(err)
	}
	out := make([]float64, len(sink.rows))
	for i, r := range sink.rows {
		out[i] = r[0].(float64)
	}
	return out
}

// TestModelLoad_ScoresBitEqualToFit: for every scorable type, a Compose
// fit saves its model; a later Process loading the file scores exactly
// what the Compose slot scored, and a Materialize loading it scores
// every row bit for bit like a Materialize under the in-compose fit.
func TestModelLoad_ScoresBitEqualToFit(t *testing.T) {
	for name, spec := range scorers() {
		t.Run(name, func(t *testing.T) {
			svc := fitsService(t)
			ctx := context.Background()
			fit := fitRequest("0", "90")
			s := spec
			fit.Regressions = []*types.RegressionSpec{&s}
			out, err := svc.Compose(ctx, &types.ComposedRequest{
				Fits:     []types.FitSpec{{Name: "m", Request: fit, Save: loadPath}},
				Requests: []*types.Request{scoreReq("in_compose", nil)},
			})
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := svc.Process(ctx, scoreReq("loaded", loadM()))
			if err != nil {
				t.Fatal(err)
			}
			want, got := out.Responses[0].Data[0], loaded.Data[0]
			if got["n"] != float64(104) || !bitsEqual(got["sum_p"].(float64), want["sum_p"].(float64)) {
				t.Fatalf("loaded %v, in-compose %v", got, want)
			}

			set, _, _, err := svc.runComposeFits(ctx, []types.FitSpec{{Name: "m", Request: fit}}, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			inFit := materializeScores(t, svc, model.WithSet(ctx, set), nil)
			fromFile := materializeScores(t, svc, ctx, loadM())
			if len(inFit) != 104 || len(fromFile) != 104 {
				t.Fatalf("rows %d / %d, want 104", len(inFit), len(fromFile))
			}
			for i := range inFit {
				if !bitsEqual(inFit[i], fromFile[i]) {
					t.Fatalf("row %d: loaded %v, in-compose %v", i, fromFile[i], inFit[i])
				}
			}
		})
	}
}

// saveModel fits the OLS of y on x over weeks [0, 90] and saves it at
// loadPath.
func saveModel(t *testing.T, svc *Service) {
	t.Helper()
	req := fitRequest("0", "90")
	req.Regressions[0].Save = loadPath
	if _, err := svc.Process(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

// TestModelLoad_Surfaces: the loaded model resolves wherever a Request
// runs — ProcessStream, a chain's first and later stage, a Facet's
// FILTER_EXPRESSION coef(), a Compose slot and a fit request — each
// scoring what a standalone Process scores.
func TestModelLoad_Surfaces(t *testing.T) {
	svc := fitsService(t)
	ctx := context.Background()
	saveModel(t, svc)
	base, err := svc.Process(ctx, scoreReq("base", loadM()))
	if err != nil {
		t.Fatal(err)
	}
	want := base.Data[0]["sum_p"].(float64)

	t.Run("ProcessStream", func(t *testing.T) {
		it, err := svc.ProcessStream(ctx, scoreReq("s", loadM()))
		if err != nil {
			t.Fatal(err)
		}
		row, ok, err := it.Next(ctx)
		if err != nil || !ok || !bitsEqual(row["sum_p"].(float64), want) {
			t.Fatalf("stream row %v (%v, %v), want sum_p %v", row, ok, err, want)
		}
	})

	t.Run("ProcessChain", func(t *testing.T) {
		stage0 := scoreReq("", loadM())
		stage0.Cohort = nil
		stage0.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "p", Label: "sum_p"}, {Type: types.AGG_SUM, Field: "x", Label: "x"}}
		// Stage 1 scores the stage-0 row (its "x" column is Σx) with
		// its own load: intercept + slope·Σx.
		stage1 := &types.Request{
			Models:       loadM(),
			Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "q", Params: json.RawMessage(`{"fit":"m"}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q", Label: "sum_q"}},
		}
		resp, err := svc.ProcessChain(ctx, &types.ChainRequest{
			Cohort: &types.Cohort{Filename: fitsCohort},
			Stages: []*types.ChainStage{{Name: "score", Request: stage0}, {Name: "rescore", Request: stage1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Stages[0].Data[0]["sum_p"].(float64); !bitsEqual(got, want) {
			t.Fatalf("stage 0 sum_p %v, want %v", got, want)
		}
		b := loadedBound(t, svc)
		sumX := resp.Stages[0].Data[0]["x"].(float64)
		if got, w := resp.Final.Data[0]["sum_q"].(float64), b.Intercept+b.Coefficients[0]*sumX; math.Abs(got-w) > 1e-9*math.Abs(w) {
			t.Fatalf("stage 1 score %v, want %v", got, w)
		}
	})

	t.Run("Facet", func(t *testing.T) {
		b := loadedBound(t, svc)
		res, err := svc.FacetSchema(ctx, &types.FacetRequest{
			Cohort:    &types.Cohort{Filename: fitsCohort},
			Fields:    []string{"week"},
			Models:    loadM(),
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `coef("m", "x") > ` + strconv.FormatFloat(b.Coefficients[0]-1, 'g', -1, 64)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.FilteredRecords != 104 {
			t.Fatalf("filtered %d, want every row (coef > coef - 1)", res.FilteredRecords)
		}
	})

	t.Run("Compose slot and fit request", func(t *testing.T) {
		// The fit request loads m and regresses y on its score p: the
		// fit sees its own load; the slot loads m under another name.
		fit := fitRequest("0", "103")
		fit.Models = loadM()
		fit.Attributes = []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}}
		fit.Regressions[0].Predictors = []string{"p"}
		slot := scoreReq("slot", []types.ModelRef{{Name: "m", Path: loadPath}})
		for mode, out := range composeBoth(t, svc, &types.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "on_score", Request: fit}},
			Requests: []*types.Request{slot},
		}) {
			if got := out.Responses[0].Data[0]["sum_p"].(float64); !bitsEqual(got, want) {
				t.Fatalf("%s: slot sum_p %v, want %v", mode, got, want)
			}
			if c := out.Fits["on_score"].Coefficients["p"]; !(c > 0.5 && c < 1.5) {
				t.Fatalf("%s: fit on the loaded score has slope %v, want ~1", mode, c)
			}
		}
	})
}

func loadedBound(t *testing.T, svc *Service) *model.Bound {
	t.Helper()
	b, err := readModel(t, svc.fs.Fs(), loadPath).Bind("m")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestModelLoad_CrossCohort: a model fit on one cohort scores another
// cohort holding the same predictor columns — the provenance names the
// training cohort, and nothing compares it.
func TestModelLoad_CrossCohort(t *testing.T) {
	svc := fitsService(t)
	ctx := context.Background()
	saveModel(t, svc)
	other := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 8},
	}}
	rows := make([][]uint64, 10)
	for i := range rows {
		rows[i] = []uint64{math.Float64bits(float64(i) * 1.5), uint64(i % 3)}
	}
	if err := afero.WriteFile(svc.fs.Fs(), "other.pulse", writePulseFile(t, other, rows), 0o644); err != nil {
		t.Fatal(err)
	}
	b := loadedBound(t, svc)
	if b.Result == nil || readModel(t, svc.fs.Fs(), loadPath).Provenance.Cohort != fitsCohort {
		t.Fatal("vacuous: the model does not record its training cohort")
	}
	req := scoreReq("other", loadM())
	req.Cohort = &types.Cohort{Filename: "other.pulse"}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var want float64
	for i := range rows {
		want += b.Intercept + b.Coefficients[0]*float64(i)*1.5
	}
	if got := resp.Data[0]["sum_p"].(float64); resp.Data[0]["n"] != float64(10) || math.Abs(got-want) > 1e-9*math.Abs(want) {
		t.Fatalf("other cohort scored %v over %v rows, want %v over 10", got, resp.Data[0]["n"], want)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("a cross-cohort load warns: %+v", resp.Warnings)
	}
}

// TestModelLoad_Refusals: each load fault is refused with its code
// before any record is read (Process), and inside a Compose batch before
// any fit or slot runs, so the fit's own save is never written.
func TestModelLoad_Refusals(t *testing.T) {
	svc := fitsService(t)
	ctx := context.Background()
	saveModel(t, svc)
	fsys := svc.fs.Fs()
	_ = afero.WriteFile(fsys, "bad"+model.FileSuffix, []byte("{nope"), 0o644)
	_ = afero.WriteFile(fsys, "v9"+model.FileSuffix, []byte(`{"model_format_version": 9}`), 0o644)
	_ = fsys.MkdirAll("dir"+model.FileSuffix, 0o755)
	dict := encoding.NewDictionary()
	dict.Add("a")
	dict.Add("b")
	catSchema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: dict},
	}}
	if err := afero.WriteFile(fsys, "cat.pulse", writePulseFile(t, catSchema, [][]uint64{{0}, {1}}), 0o644); err != nil {
		t.Fatal(err)
	}
	noX := &encoding.Schema{Fields: []encoding.Field{{Name: "z", Type: encoding.FieldTypeF64, ByteOffset: 0}}}
	if err := afero.WriteFile(fsys, "nox.pulse", writePulseFile(t, noX, [][]uint64{{0}}), 0o644); err != nil {
		t.Fatal(err)
	}
	with := func(models []types.ModelRef, cohort string) *types.Request {
		r := scoreReq("r", models)
		if cohort != "" {
			r.Cohort = &types.Cohort{Filename: cohort}
		}
		return r
	}
	cases := []struct {
		name   string
		req    *types.Request
		code   perr.Code
		reason string
	}{
		{"missing", with([]types.ModelRef{{Name: "m", Path: "none" + model.FileSuffix}}, ""), perr.PULSE_MODEL_FILE_UNREADABLE, model.ReasonFileMissing},
		{"unreadable", with([]types.ModelRef{{Name: "m", Path: "dir" + model.FileSuffix}}, ""), perr.PULSE_MODEL_FILE_UNREADABLE, model.ReasonFileUnreadable},
		{"invalid json", with([]types.ModelRef{{Name: "m", Path: "bad" + model.FileSuffix}}, ""), perr.PULSE_MODEL_FILE_INVALID, model.ReasonFileJSON},
		{"version", with([]types.ModelRef{{Name: "m", Path: "v9" + model.FileSuffix}}, ""), perr.PULSE_MODEL_VERSION_UNSUPPORTED, ""},
		{"ref", with([]types.ModelRef{{Name: "m", Path: "m.json"}}, ""), perr.PULSE_MODEL_REF_INVALID, model.ReasonSaveSuffix},
		{"twice", with([]types.ModelRef{{Name: "m", Path: loadPath}, {Name: "m", Path: loadPath}}, ""), perr.PULSE_MODEL_NAME_COLLISION, ""},
		{"predictor missing", with(loadM(), "nox.pulse"), perr.PULSE_MODEL_PREDICTOR_MISSING, ""},
		{"predictor not numeric", with(loadM(), "cat.pulse"), perr.PULSE_MODEL_PREDICTOR_NOT_NUMERIC, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Process(ctx, tc.req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("reason %v, want %s", ce.Details["reason"], tc.reason)
			}
			// In a batch: refused before the fit runs, so its save is
			// never written; located at the slot.
			fit := fitRequest("0", "90")
			saved := "batch/" + tc.name + model.FileSuffix
			runtimeFault := tc.code == perr.PULSE_MODEL_PREDICTOR_MISSING || tc.code == perr.PULSE_MODEL_PREDICTOR_NOT_NUMERIC
			for _, workers := range []int{1, 2} {
				if runtimeFault && workers > 1 {
					continue // a slot's own refusal: the parallel arm aggregates it
				}
				c := &types.ComposedRequest{
					Fits:     []types.FitSpec{{Name: "f", Request: fit, Save: saved}},
					Requests: []*types.Request{fitsSlot("ok", "0", "90"), tc.req},
				}
				var err error
				if workers == 1 {
					_, err = svc.Compose(ctx, c)
				} else {
					_, err = svc.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: workers})
				}
				var bce *perr.CodedError
				if !stderrors.As(err, &bce) || bce.Code != tc.code || bce.Details["request"] != 1 {
					t.Fatalf("workers %d: compose %v, want %s at request 1", workers, err, tc.code)
				}
				if !runtimeFault {
					if ok, _ := afero.Exists(fsys, saved); ok {
						t.Fatalf("workers %d: the fit saved %s before the load refusal", workers, saved)
					}
				}
			}
		})
	}

	// A slot model named like a fit: refused before the fit runs, both
	// bindings named.
	c := &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "m", Request: fitRequest("0", "90"), Save: "batch/collide" + model.FileSuffix}},
		Requests: []*types.Request{scoreReq("s", loadM())},
	}
	_, err := svc.Compose(ctx, c)
	var ce *perr.CodedError
	want := map[string]any{"name": "m", "request": 0, "sources": []any{
		map[string]any{"slot": "fits", "index": 0},
		map[string]any{"slot": "models", "index": 0, "path": loadPath}}}
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_NAME_COLLISION || !reflect.DeepEqual(ce.Details, want) {
		t.Fatalf("fit/model collision: %v %+v", err, ce)
	}
	if ok, _ := afero.Exists(fsys, "batch/collide"+model.FileSuffix); ok {
		t.Fatal("the fit saved before the collision refusal")
	}
}
