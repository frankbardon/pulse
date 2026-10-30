package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// scoreAttr is an ATTR_REG_PREDICT slot scoring fit under label.
func scoreAttr(fit, label string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: label, Params: json.RawMessage(`{"fit":"` + fit + `"}`)}
}

// failedFitBatch: fits "m" (a separated GLM) and "ok" (a good OLS); slot
// "scored" scores both and averages each score, slot "other" counts.
func failedFitBatch(cohort string) *types.ComposedRequest {
	c := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	return &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "m", Request: &types.Request{Cohort: c(), Regressions: []*types.RegressionSpec{separatedGLM("")}}},
			{Name: "ok", Request: &types.Request{Cohort: c(), Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "v", Predictors: []string{"x"}}}}},
		},
		Requests: []*types.Request{
			{Cohort: c(), Label: "scored",
				Attributes: []*types.Attribute{scoreAttr("m", "p_m"), scoreAttr("ok", "p_ok")},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "x", Label: "n"},
					{Type: types.AGG_AVERAGE, Field: "p_m", Label: "mean_m"},
					{Type: types.AGG_AVERAGE, Field: "p_ok", Label: "mean_ok"},
				}},
			{Cohort: c(), Label: "other", Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}},
		},
	}
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

// TestSlotFailure_ComposeFitFailsAlone: a Compose fit that fails on its
// data fails alone — its fits entry carries `error` (the fit's own code;
// identity + error only on the wire), its PULSE_SLOT_FAILED warning
// lifts to the batch with details.fit, the good fit binds and scores,
// the ATTR_REG_PREDICT slot over the failed fit is an all-null column
// (its mean null, every row in n_null) with one PULSE_FIT_UNAVAILABLE
// warning naming the fit, and every other aggregation and slot is
// computed normally — serial and parallel byte-identical.
func TestSlotFailure_ComposeFitFailsAlone(t *testing.T) {
	t.Parallel()

	p, _, cohort := slotCohort(t, pulse.Options{})
	ctx := context.Background()
	serial, err := p.Compose(ctx, failedFitBatch(cohort))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	parallel, err := p.ComposeParallel(ctx, failedFitBatch(cohort), pulse.ComposeOptions{MaxWorkers: 4})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	sb, _ := json.Marshal(serial)
	pb, _ := json.Marshal(parallel)
	if string(sb) != string(pb) {
		t.Errorf("serial and parallel differ\n%s\n%s", sb, pb)
	}
	for name, out := range map[string]*types.ComposedResponse{"serial": serial, "parallel": parallel} {
		m := out.Fits["m"]
		if m == nil || m.RegressionResult == nil || m.Error == nil || m.Error.Code != string(perr.PROCESSING_REGRESSION_SEPARATION) || m.Coefficients != nil {
			t.Fatalf("%s: failed fit entry %+v", name, m)
		}
		if ok := out.Fits["ok"]; ok == nil || ok.RegressionResult == nil || ok.Error != nil || ok.Coefficients == nil {
			t.Errorf("%s: good fit entry %+v", name, ok)
		}
		var fitWarned bool
		for _, w := range out.Warnings {
			if w.Code == string(perr.PULSE_SLOT_FAILED) && w.Details["fit"] == 0 && w.Details["code"] == string(perr.PROCESSING_REGRESSION_SEPARATION) {
				fitWarned = true
			}
		}
		if !fitWarned {
			t.Errorf("%s: no batch PULSE_SLOT_FAILED warning with details.fit 0: %+v", name, out.Warnings)
		}
		scored := out.Responses[0]
		row := scored.Data[0]
		if n, _ := asFloat(row["n"]); n != 40 {
			t.Errorf("%s: count %v", name, row["n"])
		}
		if v, ok := asFloat(row["mean_m"]); ok && !math.IsNaN(v) {
			t.Errorf("%s: mean over the failed fit's score %v, want undefined", name, row["mean_m"])
		}
		if v, ok := asFloat(row["mean_ok"]); !ok || math.IsNaN(v) {
			t.Errorf("%s: mean over the good fit's score %v", name, row["mean_ok"])
		}
		if c := scored.Components; c != nil {
			for _, a := range c.Aggregations {
				if a.Label == "mean_m" && a.NNull != 40 {
					t.Errorf("%s: mean_m n_null %d, want 40", name, a.NNull)
				}
			}
		}
		var unavailable []*types.ResponseWarning
		for _, w := range scored.Warnings {
			if w.Code == string(perr.PULSE_FIT_UNAVAILABLE) {
				unavailable = append(unavailable, w)
			}
		}
		if len(unavailable) != 1 || !reflect.DeepEqual(unavailable[0].Details, map[string]any{
			"fit": "m", "attribute": "p_m", "code": string(perr.PROCESSING_REGRESSION_SEPARATION)}) {
			t.Errorf("%s: PULSE_FIT_UNAVAILABLE warnings %+v", name, unavailable)
		}
		if other := out.Responses[1]; len(other.Data) != 1 || len(other.Warnings) != 0 {
			t.Errorf("%s: other slot %+v", name, other)
		}
	}
	var wire struct {
		Fits map[string]map[string]json.RawMessage `json:"fits"`
	}
	if err := json.Unmarshal(sb, &wire); err != nil {
		t.Fatal(err)
	}
	if keys := mapKeys(wire.Fits["m"]); !reflect.DeepEqual(keys, []string{"error", "name", "type"}) && !reflect.DeepEqual(keys, []string{"error", "type"}) {
		t.Errorf("failed fit wire keys %v, want identity + error only", keys)
	}
}

// TestSlotFailure_ComposeFitStrict: under Strict a fit that fails on its
// data fails the whole batch with the fit's OWN code, located by
// details.fit — serial and parallel.
func TestSlotFailure_ComposeFitStrict(t *testing.T) {
	t.Parallel()

	p, _, cohort := slotCohort(t, pulse.Options{Strict: true})
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"serial": func() error { _, err := p.Compose(ctx, failedFitBatch(cohort)); return err },
		"parallel": func() error {
			_, err := p.ComposeParallel(ctx, failedFitBatch(cohort), pulse.ComposeOptions{MaxWorkers: 4})
			return err
		},
	} {
		var ce *perr.CodedError
		if err := run(); !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION || ce.Details["fit"] != 0 {
			t.Errorf("%s strict: %v, want PROCESSING_REGRESSION_SEPARATION with details.fit 0", name, err)
		}
	}
}

// TestSlotFailure_ComposeFitSaveSkipped: a failed fit writes no `save`
// file (there is no model to write).
func TestSlotFailure_ComposeFitSaveSkipped(t *testing.T) {
	t.Parallel()

	p, fs, cohort := slotCohort(t, pulse.Options{})
	batch := failedFitBatch(cohort)
	batch.Fits[0].Save = "models/m.pulse-model.json"
	if _, err := p.Compose(context.Background(), batch); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if ok, _ := afero.Exists(fs, "models/m.pulse-model.json"); ok {
		t.Error("a failed fit's save wrote a model file")
	}
}

// readersBatch: fits "m" (a separated GLM), "ok" (a good OLS) and "pop"
// (a vector fit whose singular MAT_PARTIAL_CORRELATION battery fails);
// slot "formula" reads coef() of m (row-local and two-pass total()) and
// of ok, slot "vector" reads pop
// through a fitted attribute's params.fit and a {population}
// reference, slot "matrix" reads pop through a MAT_* params.fit beside
// a good matrix, slot "other" counts.
func readersBatch(cohort string) *types.ComposedRequest {
	c := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	xzu := []string{"x", "z", "u"}
	count := &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"}
	return &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "m", Request: &types.Request{Cohort: c(), Regressions: []*types.RegressionSpec{separatedGLM("")}}},
			{Name: "ok", Request: &types.Request{Cohort: c(), Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "v", Predictors: []string{"x"}}}}},
			{Name: "pop", Request: &types.Request{Cohort: c(),
				Matrices: []types.MatrixSpec{{Name: "prec", Type: types.MAT_PARTIAL_CORRELATION, Fields: xzu}}}},
		},
		Requests: []*types.Request{
			{Cohort: c(), Label: "formula",
				Attributes: []*types.Attribute{
					{Type: types.ATTR_FORMULA, Label: "c", Expression: `coef("m", "x") * x`},
					{Type: types.ATTR_FORMULA, Label: "c_ok", Expression: `coef("ok", "x") * x`},
					{Type: types.ATTR_FORMULA, Label: "ct", Expression: `x / total(coef("m", "x") * x)`},
				},
				Aggregations: []*types.Aggregation{count,
					{Type: types.AGG_AVERAGE, Field: "c", Label: "mean_c"},
					{Type: types.AGG_AVERAGE, Field: "c_ok", Label: "mean_c_ok"},
					{Type: types.AGG_AVERAGE, Field: "ct", Label: "mean_ct"}}},
			{Cohort: c(), Label: "vector",
				Attributes: []*types.Attribute{
					{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: xzu, Params: json.RawMessage(`{"fit":"pop"}`)},
					{Type: types.ATTR_PROFILE_SIMILARITY, Label: "sim", Fields: xzu, Params: json.RawMessage(`{"metric":"cosine","reference":{"population":"pop"}}`)},
				},
				Aggregations: []*types.Aggregation{count,
					{Type: types.AGG_AVERAGE, Field: "md", Label: "mean_md"},
					{Type: types.AGG_AVERAGE, Field: "sim", Label: "mean_sim"}}},
			{Cohort: c(), Label: "matrix",
				Aggregations: []*types.Aggregation{count},
				Matrices: []types.MatrixSpec{
					{Name: "cov", Type: types.MAT_COVARIANCE, Fields: xzu, Params: json.RawMessage(`{"fit":"pop"}`)},
					{Name: "cor", Type: types.MAT_CORRELATION, Fields: []string{"x", "v"}},
				}},
			{Cohort: c(), Label: "other", Aggregations: []*types.Aggregation{count}},
		},
	}
}

// fitUnavailable is resp's PULSE_FIT_UNAVAILABLE warnings' details.
func fitUnavailable(resp *types.Response) []map[string]any {
	var out []map[string]any
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PULSE_FIT_UNAVAILABLE) {
			out = append(out, w.Details)
		}
	}
	return out
}

// TestSlotFailure_ComposeFitReadersFailAlone: every reader of a failed
// Compose fit fails alone. An attribute — coef() in ATTR_FORMULA, a
// fitted vector attribute's params.fit, a {population} reference — is
// an all-null column (its mean null, every row in n_null) with one
// PULSE_FIT_UNAVAILABLE {fit, attribute, code} warning, as
// ATTR_REG_PREDICT; a MAT_* slot's params.fit fails that slot alone
// (the fit's own code plus details.fit, a PULSE_SLOT_FAILED warning);
// the good fit's reader, the good matrix and every other slot compute —
// serial, parallel and DecodeWorkers 1 / 4 byte-identical.
func TestSlotFailure_ComposeFitReadersFailAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var ref []byte
	for _, workers := range []int{1, 4} {
		p, _, cohort := slotCohort(t, pulse.Options{DecodeWorkers: workers})
		serial, err := p.Compose(ctx, readersBatch(cohort))
		if err != nil {
			t.Fatalf("workers %d: Compose: %v", workers, err)
		}
		parallel, err := p.ComposeParallel(ctx, readersBatch(cohort), pulse.ComposeOptions{MaxWorkers: 4})
		if err != nil {
			t.Fatalf("workers %d: ComposeParallel: %v", workers, err)
		}
		sb, _ := json.Marshal(serial)
		pb, _ := json.Marshal(parallel)
		if string(sb) != string(pb) {
			t.Errorf("workers %d: serial and parallel differ\n%s\n%s", workers, sb, pb)
		}
		if ref == nil {
			ref = sb
		} else if string(ref) != string(sb) {
			t.Errorf("DecodeWorkers 1 and %d differ\n%s\n%s", workers, ref, sb)
		}
		for name, out := range map[string]*types.ComposedResponse{"serial": serial, "parallel": parallel} {
			name = fmt.Sprintf("workers %d %s", workers, name)
			nullMean := func(resp *types.Response, label string) {
				t.Helper()
				if v, ok := asFloat(resp.Data[0][label]); ok && !math.IsNaN(v) {
					t.Errorf("%s: %s %v, want undefined", name, label, resp.Data[0][label])
				}
				if c := resp.Components; c != nil {
					for _, a := range c.Aggregations {
						if a.Label == label && a.NNull != 40 {
							t.Errorf("%s: %s n_null %d, want 40", name, label, a.NNull)
						}
					}
				}
			}
			formula := out.Responses[0]
			if n, _ := asFloat(formula.Data[0]["n"]); n != 40 {
				t.Errorf("%s: formula count %v", name, formula.Data[0]["n"])
			}
			nullMean(formula, "mean_c")
			nullMean(formula, "mean_ct")
			if v, ok := asFloat(formula.Data[0]["mean_c_ok"]); !ok || math.IsNaN(v) {
				t.Errorf("%s: mean over the good fit's coef %v", name, formula.Data[0]["mean_c_ok"])
			}
			sep := string(perr.PROCESSING_REGRESSION_SEPARATION)
			sing := string(perr.PULSE_MATRIX_SINGULAR)
			if got := fitUnavailable(formula); !reflect.DeepEqual(got, []map[string]any{
				{"fit": "m", "attribute": "c", "code": sep}, {"fit": "m", "attribute": "ct", "code": sep}}) {
				t.Errorf("%s: formula PULSE_FIT_UNAVAILABLE %v", name, got)
			}
			vector := out.Responses[1]
			nullMean(vector, "mean_md")
			nullMean(vector, "mean_sim")
			if got := fitUnavailable(vector); !reflect.DeepEqual(got, []map[string]any{
				{"fit": "pop", "attribute": "md", "code": sing},
				{"fit": "pop", "attribute": "sim", "code": sing},
			}) {
				t.Errorf("%s: vector PULSE_FIT_UNAVAILABLE %v", name, got)
			}
			for _, w := range vector.Warnings {
				if w.Code == string(perr.PULSE_VECTOR_REFERENCE_MISSING) {
					t.Errorf("%s: an unavailable population also counted as a missing reference: %+v", name, w)
				}
			}
			matrix := out.Responses[2]
			if len(matrix.Matrices) != 2 {
				t.Fatalf("%s: matrices %+v", name, matrix.Matrices)
			}
			if m := matrix.Matrices[0]; m.Name != "cov" || m.Type != types.MAT_COVARIANCE || m.Error == nil || m.Error.Code != sing ||
				m.Error.Details["fit"] != "pop" || m.Primary != nil {
				t.Errorf("%s: matrix over the failed fit %+v (error %+v)", name, m, m.Error)
			}
			if m := matrix.Matrices[1]; m.Error != nil || m.Primary == nil {
				t.Errorf("%s: good matrix %+v", name, m)
			}
			if ws := slotWarnings(matrix); len(ws) != 1 || !reflect.DeepEqual(ws[0].Details, map[string]any{
				"slot_kind": "matrices", "index": 0, "label": "cov", "code": sing}) {
				t.Errorf("%s: matrix slot warnings %+v", name, ws)
			}
			if n, _ := asFloat(matrix.Data[0]["n"]); n != 40 {
				t.Errorf("%s: matrix slot count %v", name, matrix.Data[0]["n"])
			}
			if other := out.Responses[3]; len(other.Data) != 1 || len(other.Warnings) != 0 {
				t.Errorf("%s: other slot %+v", name, other)
			}
		}
	}
}

// TestSlotFailure_ComposeFitReadersStrict: under Strict every reader's
// batch fails with the failed fit's OWN code located by details.fit —
// the fit fails before any reader runs — serial and parallel alike.
func TestSlotFailure_ComposeFitReadersStrict(t *testing.T) {
	t.Parallel()

	p, _, cohort := slotCohort(t, pulse.Options{Strict: true})
	ctx := context.Background()
	for i, label := range []string{"formula", "vector", "matrix"} {
		mk := func() *types.ComposedRequest {
			b := readersBatch(cohort)
			b.Requests = []*types.Request{b.Requests[i]}
			return b
		}
		for arm, run := range map[string]func() error{
			"serial": func() error { _, err := p.Compose(ctx, mk()); return err },
			"parallel": func() error {
				_, err := p.ComposeParallel(ctx, mk(), pulse.ComposeOptions{MaxWorkers: 4})
				return err
			},
		} {
			var ce *perr.CodedError
			if err := run(); !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION || ce.Details["fit"] != 0 {
				t.Errorf("%s %s strict: %v, want PROCESSING_REGRESSION_SEPARATION with details.fit 0", label, arm, err)
			}
		}
	}
}

// TestSlotFailure_ComposeFitStrictLowestIndexWins: under Strict with
// two failing fits (m, index 0, and pop, index 2) the parallel fit
// stage reports the LOWEST-index failure, as the serial loop does — a
// later fit's failure never cancels an earlier one before it finishes.
func TestSlotFailure_ComposeFitStrictLowestIndexWins(t *testing.T) {
	t.Parallel()

	p, _, cohort := slotCohort(t, pulse.Options{Strict: true})
	ctx := context.Background()
	for range 30 {
		b := readersBatch(cohort)
		b.Requests = b.Requests[3:]
		// pop first in launch order would race m without the rule:
		// m (the GLM) runs longer than pop's battery.
		var ce *perr.CodedError
		_, err := p.ComposeParallel(ctx, b, pulse.ComposeOptions{MaxWorkers: 4})
		if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION || ce.Details["fit"] != 0 {
			t.Fatalf("parallel strict: %v, want fit 0's PROCESSING_REGRESSION_SEPARATION", err)
		}
	}
}

// TestSlotFailure_ComposeFitFatalReaders: the readers that cannot
// degrade stay refused with the fit's own code plus details.fit — a
// FILTER_EXPRESSION naming the failed fit (a filter cannot be null; the
// refusal comes at build, so it fires even when no row reaches the
// filter) and a coef() whose fit name is not a literal (unknown until
// a row evaluates it).
func TestSlotFailure_ComposeFitFatalReaders(t *testing.T) {
	t.Parallel()

	p, _, cohort := slotCohort(t, pulse.Options{})
	ctx := context.Background()
	for name, edit := range map[string]func(*types.Request){
		"filter": func(r *types.Request) {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `x > coef("m", "x")`}}
		},
		"filter no row reaches": func(r *types.Request) {
			r.Filterers = []*types.Filterer{
				{Type: types.FILTER_EXPRESSION, Expression: `x < 0`},
				{Type: types.FILTER_EXPRESSION, Expression: `x > coef("m", "x")`},
			}
		},
		"dynamic coef": func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "d", Expression: `coef("m" + "", "x")`}}
		},
	} {
		for arm, run := range map[string]func(*types.ComposedRequest) error{
			"serial": func(b *types.ComposedRequest) error { _, err := p.Compose(ctx, b); return err },
			"parallel": func(b *types.ComposedRequest) error {
				_, err := p.ComposeParallel(ctx, b, pulse.ComposeOptions{MaxWorkers: 4, FailFast: true})
				return err
			},
		} {
			b := readersBatch(cohort)
			edit(b.Requests[3])
			var ce *perr.CodedError
			if err := run(b); !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION || ce.Details["fit"] != "m" {
				t.Errorf("%s %s: %v, want PROCESSING_REGRESSION_SEPARATION with details.fit m", name, arm, err)
			}
		}
	}
}

// TestSlotFailure_ComposeVectorFitFailsAlone: a vector fit whose battery
// fails on its data (a singular MAT_PARTIAL_CORRELATION) fails alone — its entry
// is {"vector": {operator, error}}, nothing binds, the other slots run —
// and fails the batch with its own code under Strict.
func TestSlotFailure_ComposeVectorFitFailsAlone(t *testing.T) {
	t.Parallel()

	mk := func(cohort string) *types.ComposedRequest {
		return &types.ComposedRequest{
			Fits: []types.FitSpec{{Name: "pop", Request: &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Matrices: []types.MatrixSpec{{Name: "prec", Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"x", "z", "u"}}}}}},
			Requests: []*types.Request{{Cohort: &types.Cohort{Filename: cohort},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}}},
		}
	}
	p, _, cohort := slotCohort(t, pulse.Options{})
	out, err := p.Compose(context.Background(), mk(cohort))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	pop := out.Fits["pop"]
	if pop == nil || pop.Vector == nil || pop.Vector.Error == nil || pop.Vector.Error.Code != string(perr.PULSE_MATRIX_SINGULAR) ||
		pop.Vector.Operator != string(types.MAT_PARTIAL_CORRELATION) {
		t.Fatalf("failed vector fit entry %+v", pop)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"pop":{"vector":{"operator":"MAT_PARTIAL_CORRELATION","error":{"code":"PULSE_MATRIX_SINGULAR"`) {
		t.Errorf("failed vector fit wire %s", b)
	}
	if len(out.Responses[0].Data) != 1 {
		t.Errorf("slot %+v", out.Responses[0])
	}
	var ce *perr.CodedError
	strict, _, cohort := slotCohort(t, pulse.Options{Strict: true})
	if _, err := strict.Compose(context.Background(), mk(cohort)); !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MATRIX_SINGULAR || ce.Details["fit"] != 0 {
		t.Fatalf("strict: %v, want PULSE_MATRIX_SINGULAR with details.fit 0", err)
	}
}

// TestSlotFailure_ConfigFaultFatal: a configuration fault beside a
// failing slot is still fatal with its own code — only data-dependent
// refusals fail alone.
func TestSlotFailure_ConfigFaultFatal(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// TestSlotFailure_ShapiroBelowFiveFailsAlone (U36 E1-S3): a
// TEST_SHAPIRO_WILK over fewer than 5 rows — where Royston's
// Shapiro-Francia p is uncalibrated and nortest::sf.test refuses — fails
// its slot alone with PULSE_TEST_INSUFFICIENT_N (identity + error, no
// figures on the wire, one PULSE_SLOT_FAILED warning) beside a surviving
// aggregation and test; Strict fails the run with that code. Predict
// reads no record, so the refusal is not foreseeable: it accepts the
// same request with no error and lists the test (predict ≡ runtime on
// everything the schema decides).
func TestSlotFailure_ShapiroBelowFiveFailsAlone(t *testing.T) {
	t.Parallel()

	req := func(cohort string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x < 4"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			Tests: []*types.Test{
				{Type: types.TEST_SHAPIRO_WILK, Field: "z", Label: "sw"},
				{Type: types.TEST_PEARSON_R, Field: "x", Field2: "z", Label: "r"},
			},
		}
	}
	p, _, cohort := slotCohort(t, pulse.Options{})
	pred, err := p.Predict(context.Background(), req(cohort))
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if b, _ := json.Marshal(pred); strings.Contains(string(b), string(perr.PULSE_TEST_INSUFFICIENT_N)) {
		t.Errorf("predict reports the data-dependent refusal: %s", b)
	}
	resp, b := processJSON(t, p, req(cohort))
	sw := resp.Tests[0]
	if sw == nil || sw.Error == nil || sw.Error.Code != string(perr.PULSE_TEST_INSUFFICIENT_N) ||
		sw.Label != "sw" || sw.Type != types.TEST_SHAPIRO_WILK || sw.Error.Details["min_required"] != 5 {
		t.Fatalf("shapiro slot %+v (error %+v)", sw, sw.Error)
	}
	if resp.Tests[1].Error != nil || len(resp.Data) != 1 {
		t.Errorf("surviving slots: test %+v data %v", resp.Tests[1], resp.Data)
	}
	var wire struct {
		Tests []map[string]json.RawMessage `json:"tests"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if keys := mapKeys(wire.Tests[0]); !reflect.DeepEqual(keys, []string{"error", "label", "type"}) {
		t.Errorf("failed test wire keys %v, want label / type / error only", keys)
	}
	ws := slotWarnings(resp)
	want := map[string]any{"slot_kind": "tests", "index": 0, "label": "sw", "code": string(perr.PULSE_TEST_INSUFFICIENT_N)}
	if len(ws) != 1 || !reflect.DeepEqual(ws[0].Details, want) {
		t.Errorf("slot warnings %+v, want one with %v", ws, want)
	}

	strict, _, cohort := slotCohort(t, pulse.Options{Strict: true})
	_, err = strict.Process(context.Background(), req(cohort))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEST_INSUFFICIENT_N || ce.Details["slot_kind"] != "tests" {
		t.Fatalf("strict: %v, want PULSE_TEST_INSUFFICIENT_N on the tests slot", err)
	}
}

// TestSlotFailure_ShapiroZeroVarianceFailsAlone (U36 E1-S7): a
// TEST_SHAPIRO_WILK over a constant column — no W' exists (0 / 0;
// nortest::sf.test answers NA) — fails its slot alone with
// PULSE_TEST_VARIANCE_ZERO on the row arm (tests) and the post arm
// (post_tests): identity + error, no figures, one PULSE_SLOT_FAILED
// warning, the other slots intact; Strict fails the run with the code.
// It used to report W' = 1, p = 1 with a warning. Predict reads no
// record, so it accepts the request without naming the refusal.
func TestSlotFailure_ShapiroZeroVarianceFailsAlone(t *testing.T) {
	t.Parallel()

	// x < 10 keeps ten rows on which y is 0: constant.
	base := func(cohort string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x < 10"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y", Label: "m"}},
		}
	}
	for _, c := range []struct {
		kind string
		req  func(string) *types.Request
		slot func(*types.Response) []*types.TestResult
	}{
		{"tests", func(cohort string) *types.Request {
			r := base(cohort)
			r.Tests = []*types.Test{
				{Type: types.TEST_SHAPIRO_WILK, Field: "y", Label: "sw"},
				{Type: types.TEST_PEARSON_R, Field: "x", Field2: "z", Label: "r"},
			}
			return r
		}, func(r *types.Response) []*types.TestResult { return r.Tests }},
		// One result row per x (ten rows), each m = 0: a constant column.
		{"post_tests", func(cohort string) *types.Request {
			r := base(cohort)
			r.Groups = []*types.Group{{Type: types.GROUP_ROUNDED, Field: "x", Interval: 1}}
			r.PostTests = []*types.Test{{Type: types.TEST_SHAPIRO_WILK, Field: "m", Label: "sw"}}
			return r
		}, func(r *types.Response) []*types.TestResult { return r.PostTests }},
	} {
		t.Run(c.kind, func(t *testing.T) {
			p, _, cohort := slotCohort(t, pulse.Options{})
			pred, err := p.Predict(context.Background(), c.req(cohort))
			if err != nil {
				t.Fatalf("predict: %v", err)
			}
			if b, _ := json.Marshal(pred); strings.Contains(string(b), string(perr.PULSE_TEST_VARIANCE_ZERO)) {
				t.Errorf("predict reports the data-dependent refusal: %s", b)
			}
			resp, b := processJSON(t, p, c.req(cohort))
			results := c.slot(resp)
			sw := results[0]
			if sw == nil || sw.Error == nil || sw.Error.Code != string(perr.PULSE_TEST_VARIANCE_ZERO) ||
				sw.Label != "sw" || sw.Type != types.TEST_SHAPIRO_WILK {
				t.Fatalf("shapiro slot %+v", sw)
			}
			if len(sw.Warnings) != 0 {
				t.Errorf("failed slot keeps the old warning: %v", sw.Warnings)
			}
			if c.kind == "tests" && resp.Tests[1].Error != nil {
				t.Errorf("surviving test failed: %+v", resp.Tests[1])
			}
			if len(resp.Data) == 0 {
				t.Errorf("aggregation rows lost")
			}
			var top map[string]json.RawMessage
			var wire []map[string]json.RawMessage
			if err := json.Unmarshal(b, &top); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(top[c.kind], &wire); err != nil {
				t.Fatal(err)
			}
			if keys := mapKeys(wire[0]); !reflect.DeepEqual(keys, []string{"error", "label", "type"}) {
				t.Errorf("failed %s wire keys %v, want label / type / error only", c.kind, keys)
			}
			ws := slotWarnings(resp)
			want := map[string]any{"slot_kind": c.kind, "index": 0, "label": "sw", "code": string(perr.PULSE_TEST_VARIANCE_ZERO)}
			if len(ws) != 1 || !reflect.DeepEqual(ws[0].Details, want) {
				t.Errorf("slot warnings %+v, want one with %v", ws, want)
			}

			strict, _, cohort := slotCohort(t, pulse.Options{Strict: true})
			_, err = strict.Process(context.Background(), c.req(cohort))
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEST_VARIANCE_ZERO || ce.Details["slot_kind"] != c.kind {
				t.Fatalf("strict: %v, want PULSE_TEST_VARIANCE_ZERO on the %s slot", err, c.kind)
			}
		})
	}
}
