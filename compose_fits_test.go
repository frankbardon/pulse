package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// compose_fits_test.go pins ComposedRequest.Fits at the facade: predict
// and runtime refuse the same batches with the same coded error, the
// predict result lists each fit statically, a hidden capability refuses
// the slot as an unknown field, and `fits` rides outside the Compose
// `return`.

const fitsFacadeCohort = "fits_facade.pulse"

func fitsFacadeFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := 0; i < 104; i++ {
		x := float64((i*37)%17) + 0.25*float64(i%3)
		y := 3 + 2*x + 0.0005*float64(i*i) + 0.1*float64((i*13)%7-3)
		rows = append(rows, []string{strconv.Itoa(i), jsonNumber(x), jsonNumber(y), []string{"north", "south"}[i%2]})
	}
	createTestPulseFile(t, fsys, fitsFacadeCohort, []string{"week", "x", "y", "region"}, rows)
	return fsys
}

func facadeFitRequest(lo, hi string) *Request {
	return &Request{
		Cohort:      &types.Cohort{Filename: fitsFacadeCohort},
		Filterers:   []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{lo, hi}}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}}},
	}
}

func facadeFitSlot(label string) *Request {
	return &Request{
		Label:        label,
		Cohort:       &types.Cohort{Filename: fitsFacadeCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "sum_y"}},
	}
}

// TestComposeFits_PredictRuntimeParity: for every fit refusal — each
// PULSE_FIT_INVALID reason, PULSE_MODEL_NAME_COLLISION and a fit
// request's own refusal (an unknown predictor) — PredictCompose reports
// exactly the error Compose and ComposeParallel return: code, message
// and details (details.fit included).
func TestComposeFits_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	noRegs := facadeFitRequest("0", "90")
	noRegs.Regressions = nil
	twoRegs := facadeFitRequest("0", "90")
	twoRegs.Regressions = append(twoRegs.Regressions, &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"week"}})
	grouped := facadeFitRequest("0", "90")
	grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	unknownPredictor := facadeFitRequest("0", "90")
	unknownPredictor.Regressions[0].Predictors = []string{"nope"}
	cases := []struct {
		name string
		fits []types.FitSpec
		code perr.Code
	}{
		{"regression_count zero", []types.FitSpec{{Name: "a", Request: noRegs}}, perr.PULSE_FIT_INVALID},
		{"regression_count two", []types.FitSpec{{Name: "a", Request: facadeFitRequest("0", "9")}, {Name: "b", Request: twoRegs}}, perr.PULSE_FIT_INVALID},
		{"grouped", []types.FitSpec{{Name: "a", Request: grouped}}, perr.PULSE_FIT_INVALID},
		{"name_missing", []types.FitSpec{{Request: facadeFitRequest("0", "9")}}, perr.PULSE_FIT_INVALID},
		{"request_missing", []types.FitSpec{{Name: "a"}}, perr.PULSE_FIT_INVALID},
		{"collision", []types.FitSpec{{Name: "a", Request: facadeFitRequest("0", "9")}, {Name: "a", Request: facadeFitRequest("0", "19")}}, perr.PULSE_MODEL_NAME_COLLISION},
		{"fit request refused", []types.FitSpec{{Name: "a", Request: facadeFitRequest("0", "9")}, {Name: "b", Request: unknownPredictor}}, ""},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposedRequest{Fits: tc.fits, Requests: []*Request{facadeFitSlot("s")}}
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if tc.code != "" && perr.Code(pe.Code) != tc.code {
				t.Fatalf("predict code = %s, want %s", pe.Code, tc.code)
			}
			if pe.Details["fit"] == nil {
				t.Errorf("predict refusal not located: %v", pe.Details)
			}
			runs := map[string]func() error{
				"Compose": func() error { _, err := p.Compose(ctx, c); return err },
				"ComposeParallel": func() error {
					_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
					return err
				},
			}
			for name, run := range runs {
				var ce *perr.CodedError
				if err := run(); !stderrors.As(err, &ce) {
					t.Fatalf("%s: uncoded %v", name, err)
				}
				if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
					t.Errorf("%s: runtime %s %q %v\n predict %s %q %v", name, ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
				}
			}
		})
	}
}

// TestPredictCompose_ListsFits: a valid batch predicts valid and lists
// each fit statically in declaration order — name, regression type,
// target, predictors as written and cohort.
func TestPredictCompose_ListsFits(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	glm := facadeFitRequest("0", "103")
	glm.Regressions[0] = &types.RegressionSpec{Type: types.REG_GLM, Family: "poisson", Target: "week", Predictors: []string{"x", "y"}}
	c := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "train", Request: facadeFitRequest("0", "90")}, {Name: "counts", Request: glm}},
		Requests: []*Request{facadeFitSlot("s")},
	}
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("errors: %+v", env.Errors)
	}
	res := env.Data.(*ComposePredictResult)
	want := []descriptor.FitSummary{
		{Name: "train", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Cohort: fitsFacadeCohort},
		{Name: "counts", Type: types.REG_GLM, Target: "week", Predictors: []string{"x", "y"}, Cohort: fitsFacadeCohort},
	}
	if !res.Valid || !reflect.DeepEqual(res.Fits, want) {
		t.Fatalf("valid=%v fits=%+v", res.Valid, res.Fits)
	}
	// A fit-free batch reports no fits key.
	env, _ = p.PredictCompose(context.Background(), &ComposedRequest{Requests: []*Request{facadeFitSlot("s")}})
	raw, _ := json.Marshal(env.Data)
	if bytes.Contains(raw, []byte(`"fits"`)) {
		t.Fatalf("fit-free predict carries fits: %s", raw)
	}
}

// TestHiddenComposeFits_RefusedAsUnknownField: an instance offering
// Compose and REG_OLS but not capability:compose_fits refuses a set
// `fits` slot as PULSE_REQUEST_UNKNOWN_FIELD on Compose, ComposeParallel
// and PredictCompose; the unprofiled control runs it.
func TestHiddenComposeFits_RefusedAsUnknownField(t *testing.T) {
	t.Parallel()

	fsys := fitsFacadeFS(t)
	control, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	feats := []string{"capability:compose", string(types.AGG_SUM), string(types.REG_OLS), string(types.FILTER_RANGE)}
	h, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "no-fits", Features: feats}})
	if err != nil {
		t.Fatal(err)
	}
	hidden := descx.HiddenSlotKeys(&types.ComposedRequest{}, h.svc.InstanceSnapshot())
	if !slices.Contains(hidden, "fits") {
		t.Fatalf("vacuous: the profile does not hide fits (hidden %v)", hidden)
	}
	if !slices.Contains(descx.HiddenSlotKeys(&types.ComposedResponse{}, h.svc.InstanceSnapshot()), "fits") {
		t.Fatal("the response fits key is not hidden with the capability")
	}
	ctx := context.Background()
	c := &ComposedRequest{Fits: []types.FitSpec{{Name: "a", Request: facadeFitRequest("0", "90")}}, Requests: []*Request{facadeFitSlot("s")}}
	if resp, err := control.Compose(ctx, c); err != nil || resp.Fits["a"] == nil {
		t.Fatalf("default instance: %v %v", resp, err)
	}
	entries := map[string]func(*Pulse) *slotGateOutcome{
		"Compose": func(p *Pulse) *slotGateOutcome { _, err := p.Compose(ctx, c); return slotGateFromErr(err) },
		"ComposeParallel": func(p *Pulse) *slotGateOutcome {
			_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2})
			return slotGateFromErr(err)
		},
		"PredictCompose": func(p *Pulse) *slotGateOutcome {
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			return slotGateFromEnv(env)
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			assertSlotRefusal(t, run(h), "fits", hidden)
		})
	}
}

// TestComposeFits_OutsideComposeReturn: a Compose-level `return` shapes
// the top-level overlays only; `fits` is always emitted whole, at full
// precision, like `ranking`.
func TestComposeFits_OutsideComposeReturn(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "train", Request: facadeFitRequest("0", "90")}},
		Requests: []*Request{facadeFitSlot("s")},
		Return:   &types.Return{Include: []string{"overlays"}, Precision: 1},
	}
	resp, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Returned == nil {
		t.Fatal("vacuous: the Compose return did not shape the response")
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Fits map[string]struct {
			Coefficients map[string]float64 `json:"coefficients"`
		} `json:"fits"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	got, ok := wire.Fits["train"]
	if !ok {
		t.Fatalf("fits dropped by the Compose return: %s", raw)
	}
	if want := resp.Fits["train"].Coefficients["x"]; got.Coefficients["x"] != want {
		t.Fatalf("fits coefficient rounded: wire %v, want %v", got.Coefficients["x"], want)
	}
}
