package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// processOrSlotErr runs req and returns the run's error — or, when the
// run succeeded, its first failed slot's refusal (tests, post-tests,
// regressions, then matrices) as the coded error it was raised as: a
// data-dependent refusal fails its slot alone (per-slot partial
// success), the response carrying it as a SlotError.
func processOrSlotErr(ctx context.Context, p *pulse.Pulse, req *types.Request) (*types.Response, error) {
	resp, err := p.Process(ctx, req)
	if err != nil {
		return nil, err
	}
	if se := firstSlotError(resp); se != nil {
		return resp, perr.NewCodedErrorWithDetails(perr.Code(se.Code), se.Message, se.Details)
	}
	return resp, nil
}

// firstSlotError is resp's first failed slot's error, nil when none
// failed.
func firstSlotError(resp *types.Response) *types.SlotError {
	if resp == nil {
		return nil
	}
	for _, rs := range [][]*types.TestResult{resp.Tests, resp.PostTests} {
		for _, r := range rs {
			if r != nil && r.Error != nil {
				return r.Error
			}
		}
	}
	for _, r := range resp.Regressions {
		if r != nil && r.Error != nil {
			return r.Error
		}
	}
	for i := range resp.Matrices {
		if resp.Matrices[i].Error != nil {
			return resp.Matrices[i].Error
		}
	}
	return nil
}

// slotCohort imports a 40-row cohort: g (categorical a/b alternating),
// x (0..39), y (1 when x > 19 else 0: x separates it completely),
// z (spread), u = 2z (collinear with z) and v (y-correlated noise).
func slotCohort(t *testing.T, opts pulse.Options) (*pulse.Pulse, afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"g", "x", "y", "z", "u", "v"}
	var fields []encoding.Field
	off := 0
	for i, n := range names {
		f := encoding.Field{Name: n, Type: encoding.FieldTypeF64, CsvColumnIdx: i, ByteOffset: off}
		if n == "g" {
			f.Type, f.Dictionary = encoding.FieldTypeCategoricalU8, encoding.NewDictionary()
		}
		off += f.Type.ByteSize()
		fields = append(fields, f)
	}
	var b strings.Builder
	b.WriteString(strings.Join(names, ",") + "\n")
	for i := range 40 {
		y := 0
		if i > 19 {
			y = 1
		}
		z := float64((i*7)%11) + 0.5
		fmt.Fprintf(&b, "%s,%d,%d,%g,%g,%g\n", []string{"a", "b"}[i%2], i, y, z, 2*z, float64((i*13)%17)+float64(y))
	}
	if err := afero.WriteFile(fs, "slot.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	job := &pio.ImportJob{Source: csv.NewReader(fs, "slot.csv"), Target: "slot.pulse", Schema: &encoding.Schema{Fields: fields}, FS: fs}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import: %v", err)
	}
	return p, fs, "slot.pulse"
}

// separatedGLM is a REG_GLM binomial whose predictor separates its
// target completely: PROCESSING_REGRESSION_SEPARATION.
func separatedGLM(name string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_GLM, Name: name, Family: "binomial", Target: "y", Predictors: []string{"x"}}
}

// failingSlotsRequest: a count, a separated GLM beside a good OLS, a good
// test and a singular partial correlation beside a good correlation.
func failingSlotsRequest(cohort string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		Tests:        []*types.Test{{Type: types.TEST_PEARSON_R, Field: "x", Field2: "v", Label: "r"}},
		Regressions:  []*types.RegressionSpec{separatedGLM("default"), {Type: types.REG_OLS, Name: "ols", Target: "v", Predictors: []string{"x"}}},
		Matrices: []types.MatrixSpec{
			{Name: "pc", Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"x", "z", "u"}},
			{Name: "cor", Type: types.MAT_CORRELATION, Fields: []string{"x", "v"}},
		},
	}
}

// slotWarnings is resp's PULSE_SLOT_FAILED warnings.
func slotWarnings(resp *types.Response) []*types.ResponseWarning {
	var out []*types.ResponseWarning
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PULSE_SLOT_FAILED) {
			out = append(out, w)
		}
	}
	return out
}

// TestSlotFailure_PartialSuccessByDefault: a separated GLM and a
// singular matrix fail only their own slots — each result keeps its
// identity and carries `error` (the refusal's own code), numeric fields
// absent on the wire — while the aggregation, the good regression, the
// test and the good matrix survive; one PULSE_SLOT_FAILED warning per
// failed slot carries {slot_kind, index, label, code}; the run returns
// no error.
func TestSlotFailure_PartialSuccessByDefault(t *testing.T) {
	p, _, cohort := slotCohort(t, pulse.Options{})
	resp, b := processJSON(t, p, failingSlotsRequest(cohort))
	if len(resp.Data) != 1 || resp.Data[0]["n"] != int64(40) && resp.Data[0]["n"] != float64(40) {
		t.Errorf("aggregation lost: %v", resp.Data)
	}
	glm := resp.Regressions[0]
	if glm == nil || glm.Error == nil || glm.Error.Code != string(perr.PROCESSING_REGRESSION_SEPARATION) || glm.Name != "default" || glm.Type != types.REG_GLM {
		t.Fatalf("separated GLM %+v", glm)
	}
	if resp.Regressions[1].Error != nil || resp.Regressions[1].Coefficients == nil {
		t.Errorf("good OLS %+v", resp.Regressions[1])
	}
	if resp.Tests[0].Error != nil {
		t.Errorf("good test failed: %+v", resp.Tests[0].Error)
	}
	if m := resp.Matrices[0]; m.Error == nil || m.Error.Code != string(perr.PULSE_MATRIX_SINGULAR) || m.Primary != nil {
		t.Errorf("singular matrix %+v", m)
	}
	if resp.Matrices[1].Error != nil || resp.Matrices[1].Primary == nil {
		t.Errorf("good matrix %+v", resp.Matrices[1])
	}
	var wire struct {
		Regressions []map[string]json.RawMessage `json:"regressions"`
		Matrices    []map[string]json.RawMessage `json:"matrices"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if keys := mapKeys(wire.Regressions[0]); !reflect.DeepEqual(keys, []string{"error", "name", "type"}) {
		t.Errorf("failed regression wire keys %v, want name / type / error only", keys)
	}
	if keys := mapKeys(wire.Matrices[0]); !reflect.DeepEqual(keys, []string{"error", "name", "type"}) {
		t.Errorf("failed matrix wire keys %v, want name / type / error only", keys)
	}
	ws := slotWarnings(resp)
	want := []map[string]any{
		{"slot_kind": "regressions", "index": 0, "label": "default", "code": string(perr.PROCESSING_REGRESSION_SEPARATION)},
		{"slot_kind": "matrices", "index": 0, "label": "pc", "code": string(perr.PULSE_MATRIX_SINGULAR)},
	}
	if len(ws) != len(want) {
		t.Fatalf("slot warnings %+v, want %d", ws, len(want))
	}
	for i, w := range want {
		if !reflect.DeepEqual(ws[i].Details, w) {
			t.Errorf("warning %d details %v, want %v", i, ws[i].Details, w)
		}
	}
}

func mapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSlotFailure_StrictFailsWithTheSlotsOwnCode: under Options.Strict
// the same request fails the run with the first failed slot's OWN code
// (never a generic one), its slot identity in the details.
func TestSlotFailure_StrictFailsWithTheSlotsOwnCode(t *testing.T) {
	p, _, cohort := slotCohort(t, pulse.Options{Strict: true})
	resp, err := p.Process(context.Background(), failingSlotsRequest(cohort))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION || resp != nil {
		t.Fatalf("strict: resp %v err %v, want PROCESSING_REGRESSION_SEPARATION", resp, err)
	}
	if ce.Details["slot_kind"] != "regressions" || ce.Details["index"] != 0 || ce.Details["label"] != "default" {
		t.Errorf("strict details %v", ce.Details)
	}
}

// TestSlotFailure_Grouped: on a grouped request each bucket of a matrix
// fails alone (group_key on the result and the warning) and the
// regression — fitted over every filtered row — fails alone too, while
// the grouped rows survive.
func TestSlotFailure_Grouped(t *testing.T) {
	p, _, cohort := slotCohort(t, pulse.Options{})
	req := failingSlotsRequest(cohort)
	req.Tests = nil
	req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	resp, _ := processJSON(t, p, req)
	if len(resp.Data) != 2 {
		t.Fatalf("grouped rows %v", resp.Data)
	}
	if r := resp.Regressions[0]; r.Error == nil || r.Error.Code != string(perr.PROCESSING_REGRESSION_SEPARATION) {
		t.Errorf("grouped run's regression %+v", r)
	}
	var failed, good int
	for _, m := range resp.Matrices {
		switch {
		case m.Name == "pc" && m.Error != nil && len(m.GroupKey) == 1:
			failed++
		case m.Name == "cor" && m.Error == nil && m.Primary != nil:
			good++
		default:
			t.Errorf("grouped matrix %+v", m)
		}
	}
	if failed != 2 || good != 2 {
		t.Errorf("%d failed / %d good buckets, want 2 / 2", failed, good)
	}
	keys := 0
	for _, w := range slotWarnings(resp) {
		if w.Details["slot_kind"] == "matrices" && w.Details["group_key"] != nil && w.Details["index"] == 0 {
			keys++
		}
	}
	if keys != 2 {
		t.Errorf("matrix bucket warnings %+v, want one per bucket", slotWarnings(resp))
	}
}

// TestSlotFailure_ComposeHosted: a Compose slot whose regression fails
// keeps its response (the failed slot plus its warning) and the other
// Compose slot is untouched; under Strict the batch fails with the
// slot's own code.
func TestSlotFailure_ComposeHosted(t *testing.T) {
	p, _, cohort := slotCohort(t, pulse.Options{})
	mk := func() *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{
			{Cohort: &types.Cohort{Filename: cohort}, Label: "fit",
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
				Regressions:  []*types.RegressionSpec{separatedGLM("default")}},
			{Cohort: &types.Cohort{Filename: cohort}, Label: "sum",
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}},
		}}
	}
	out, err := p.Compose(context.Background(), mk())
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	fit := out.Responses[0]
	if fit.Regressions[0].Error == nil || fit.Regressions[0].Error.Code != string(perr.PROCESSING_REGRESSION_SEPARATION) || len(fit.Data) != 1 {
		t.Errorf("compose slot %+v", fit)
	}
	if ws := slotWarnings(fit); len(ws) != 1 || ws[0].Details["label"] != "default" {
		t.Errorf("compose slot warnings %+v", fit.Warnings)
	}
	if other := out.Responses[1]; len(other.Data) != 1 || len(slotWarnings(other)) != 0 {
		t.Errorf("other compose slot %+v", other)
	}

	strict, _, cohort := slotCohort(t, pulse.Options{Strict: true})
	_ = cohort
	var ce *perr.CodedError
	if _, err := strict.Compose(context.Background(), mk()); !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION {
		t.Fatalf("strict compose: %v, want PROCESSING_REGRESSION_SEPARATION", err)
	}
}

// TestSlotFailure_ComposeFitStillFailsWithItsCode: a Compose `fits`
// entry whose regression fails on its data has no model to bind, so the
// batch fails with the fit's own code (not a generic internal one).
func TestSlotFailure_ComposeFitStillFailsWithItsCode(t *testing.T) {
	p, _, cohort := slotCohort(t, pulse.Options{})
	_, err := p.Compose(context.Background(), &types.ComposedRequest{
		Fits: []types.FitSpec{{Name: "m", Request: &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Regressions: []*types.RegressionSpec{separatedGLM("")}}}},
		Requests: []*types.Request{{Cohort: &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}}},
	})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION {
		t.Fatalf("compose fit: %v, want PROCESSING_REGRESSION_SEPARATION", err)
	}
}

// TestSlotFailure_ConfigFaultFatal: a configuration fault beside a
// failing slot is still fatal with its own code — only data-dependent
// refusals fail alone.
func TestSlotFailure_ConfigFaultFatal(t *testing.T) {
	p, _, cohort := slotCohort(t, pulse.Options{})
	req := failingSlotsRequest(cohort)
	req.Tests = []*types.Test{{Type: types.TEST_T, Field: "g", Params: json.RawMessage(`{"mu":1}`)}}
	_, err := p.Process(context.Background(), req)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEST_FIELD_NOT_NUMERIC {
		t.Fatalf("config fault: %v, want the fatal PULSE_TEST_FIELD_NOT_NUMERIC", err)
	}
}

// TestSlotFailure_ReturnKeepsTheError: a failed slot's `error` is a
// KEEP path — no preset or include list shapes it away (minimal keeps
// the failed regression's error beside its identity).
func TestSlotFailure_ReturnKeepsTheError(t *testing.T) {
	p, _, cohort := slotCohort(t, pulse.Options{})
	for name, ret := range map[string]*types.Return{
		"minimal":      {Preset: types.ReturnPresetMinimal},
		"standard":     {Preset: types.ReturnPresetStandard},
		"include only": {Include: []string{"regressions[*].coefficients"}},
	} {
		req := failingSlotsRequest(cohort)
		req.Return = ret
		resp, b := processJSON(t, p, req)
		if r := resp.Regressions[0]; r == nil || r.Error == nil || r.Error.Code != string(perr.PROCESSING_REGRESSION_SEPARATION) {
			t.Errorf("%s: failed regression shaped to %+v", name, r)
		}
		if !strings.Contains(string(b), `"error":{"code":"PROCESSING_REGRESSION_SEPARATION"`) {
			t.Errorf("%s: wire lost the slot error: %s", name, b)
		}
	}
	req := failingSlotsRequest(cohort)
	req.Return = &types.Return{Exclude: []string{"regressions[*].error"}}
	if _, b := processJSON(t, p, req); strings.Contains(string(b), `"error":{"code":"PROCESSING_REGRESSION_SEPARATION"`) {
		t.Errorf("an explicit exclude kept the slot error: %s", b)
	}
}

// TestSlotFailure_SaveSkipsAFailedFit: a regression `save` whose fit
// failed on its data writes no model file (there is no model); the
// run succeeds with the slot's warning.
func TestSlotFailure_SaveSkipsAFailedFit(t *testing.T) {
	p, fs, cohort := slotCohort(t, pulse.Options{})
	req := failingSlotsRequest(cohort)
	req.Regressions[0].Save = "models/default.pulse-model.json"
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if ok, _ := afero.Exists(fs, "models/default.pulse-model.json"); ok {
		t.Error("a failed fit wrote a model file")
	}
	if len(slotWarnings(resp)) == 0 {
		t.Error("no slot warning for the failed fit")
	}
}
