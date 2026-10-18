package slotfail

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFrom_OnlyDataDependentCodesOfTheKind: a coded data-dependent
// refusal of the slot's kind becomes a SlotError carrying its own code,
// message and details (wrapped or not); a configuration code, an
// uncoded error and another kind's code stay fatal.
func TestFrom_OnlyDataDependentCodesOfTheKind(t *testing.T) {
	sep := errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_SEPARATION, "separated", map[string]any{"predictors": []string{"x"}})
	se, ok := From(KindRegressions, fmt.Errorf("fit: %w", sep))
	if !ok || se.Code != string(errors.PROCESSING_REGRESSION_SEPARATION) || se.Message != "separated" ||
		!reflect.DeepEqual(se.Details, map[string]any{"predictors": []string{"x"}}) {
		t.Fatalf("separation: %+v %v", se, ok)
	}
	for name, c := range map[string]struct {
		kind Kind
		err  error
	}{
		"config":     {KindRegressions, errors.NewCodedError(errors.PROCESSING_CONFIG, "bad")},
		"invalid":    {KindRegressions, errors.NewCodedError(errors.PROCESSING_REGRESSION_INVALID_LINK, "bad link")},
		"uncoded":    {KindTests, fmt.Errorf("boom")},
		"other kind": {KindRegressions, errors.NewCodedError(errors.PULSE_TEST_VARIANCE_ZERO, "zero")},
		"test field": {KindTests, errors.NewCodedError(errors.PULSE_TEST_FIELD_NOT_NUMERIC, "categorical")},
		"matrix cfg": {KindMatrices, errors.NewCodedError(errors.PULSE_MATRIX_NOT_IDENTIFIED, "model")},
		"nil":        {KindMatrices, nil},
	} {
		if se, ok := From(c.kind, c.err); ok || se != nil {
			t.Errorf("%s: per-slot %+v, want fatal", name, se)
		}
	}
	for kind, code := range map[Kind]errors.Code{
		KindTests:     errors.PULSE_TEST_INSUFFICIENT_N,
		KindPostTests: errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
		KindMatrices:  errors.PULSE_MATRIX_NOT_PSD,
	} {
		if _, ok := From(kind, errors.NewCodedError(code, "m")); !ok {
			t.Errorf("%s %s: fatal, want per-slot", kind, code)
		}
	}
}

func failedResponse() (*types.Request, *types.Response) {
	se := func(code errors.Code) *types.SlotError { return &types.SlotError{Code: string(code), Message: "m"} }
	req := &types.Request{
		Tests:       []*types.Test{{Type: types.TEST_T}, {Type: types.TEST_WELCH}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS}, {Type: types.REG_GLM, Name: "g"}},
		Matrices:    []types.MatrixSpec{{Name: "a"}, {Name: "b"}},
	}
	resp := &types.Response{
		// Grouped: 2 tests x 2 groups, test-major.
		Tests: []*types.TestResult{
			{Label: "t0"}, {Label: "t0"},
			{Label: "z1", GroupKey: types.AxisKey{"g1"}}, {Label: "z1", GroupKey: types.AxisKey{"g2"}, Error: se(errors.PULSE_TEST_VARIANCE_ZERO)},
		},
		Regressions: []*types.RegressionResult{
			{Type: types.REG_OLS, Error: se(errors.PROCESSING_REGRESSION_RANK_DEFICIENT)},
			{Type: types.REG_GLM, Name: "g", Error: se(errors.PROCESSING_REGRESSION_SEPARATION)},
		},
		Matrices: []types.MatrixResult{{Name: "a"}, {Name: "b", GroupKey: types.AxisKey{"g1"}, Error: se(errors.PULSE_MATRIX_SINGULAR)}},
	}
	return req, resp
}

// TestCollect_IndexesAndLabels: each failed slot is listed in response
// order with its REQUEST slot index — a grouped test's k / groups, a
// matrix by name — its label (an unnamed regression's
// regressions[i]) and a grouped bucket's group key.
func TestCollect_IndexesAndLabels(t *testing.T) {
	req, resp := failedResponse()
	got := Collect(req, resp)
	want := []struct {
		kind  Kind
		index int
		label string
		key   bool
	}{
		{KindTests, 1, "z1", true},
		{KindRegressions, 0, "regressions[0]", false},
		{KindRegressions, 1, "g", false},
		{KindMatrices, 1, "b", true},
	}
	if len(got) != len(want) {
		t.Fatalf("%d failures, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind || g.Index != w.index || g.Label != w.label || (len(g.GroupKey) > 0) != w.key {
			t.Errorf("failure %d: %+v, want %+v", i, g, w)
		}
	}
}

// TestApply_WarnsOrStrictFails: Apply appends one PULSE_SLOT_FAILED
// warning per failed slot (details {slot_kind, index, label, code},
// group_key for a bucket) and leaves the results in place; under strict
// it returns the FIRST failed slot's own code with its slot identity in
// the details and adds no warning.
func TestApply_WarnsOrStrictFails(t *testing.T) {
	req, resp := failedResponse()
	if err := Apply(req, resp, false); err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 4 {
		t.Fatalf("%d warnings, want 4: %+v", len(resp.Warnings), resp.Warnings)
	}
	w := resp.Warnings[2]
	if w.Code != string(errors.PULSE_SLOT_FAILED) || !reflect.DeepEqual(w.Details, map[string]any{
		"slot_kind": "regressions", "index": 1, "label": "g", "code": string(errors.PROCESSING_REGRESSION_SEPARATION),
	}) || w.Message == "" {
		t.Errorf("regression warning %+v", w)
	}
	if k := resp.Warnings[3].Details["group_key"]; !reflect.DeepEqual(k, types.AxisKey{"g1"}) {
		t.Errorf("matrix bucket warning group_key %v", k)
	}
	if resp.Regressions[1].Error == nil {
		t.Error("Apply cleared a failed slot's error")
	}

	req, resp = failedResponse()
	err := Apply(req, resp, true)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_TEST_VARIANCE_ZERO || ce.Details["slot_kind"] != "tests" || ce.Details["index"] != 1 ||
		ce.Details["label"] != "z1" || !reflect.DeepEqual(ce.Details["group_key"], types.AxisKey{"g2"}) {
		t.Fatalf("strict: %#v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("strict added warnings %+v", resp.Warnings)
	}
	if err := Apply(&types.Request{}, &types.Response{}, true); err != nil {
		t.Errorf("no failed slot, strict: %v", err)
	}
}

// TestScope_RewritesEveryFailedSlot: Scope hands each failed slot's
// error to the instance's refusal scoping and keeps the rewrite.
func TestScope_RewritesEveryFailedSlot(t *testing.T) {
	_, resp := failedResponse()
	Scope(resp, func(err error) error {
		ce := err.(*errors.CodedError)
		ce.Message = "scoped " + ce.Message
		return ce
	})
	for _, f := range Collect(failedRequestOnly(), resp) {
		if f.Err.Message != "scoped m" {
			t.Errorf("%s %s: message %q", f.Kind, f.Label, f.Err.Message)
		}
	}
}

func failedRequestOnly() *types.Request {
	req, _ := failedResponse()
	return req
}
