package processing

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// slotFailureSchema: x (a trend), z (spread), u = 2z (collinear with
// z), c (constant) and y = 1 + 2x + z.
func slotFailureSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "z", Type: encoding.FieldTypeF64},
		{Name: "u", Type: encoding.FieldTypeF64},
		{Name: "c", Type: encoding.FieldTypeF64},
	}}
}

func slotFailureRecords(schema *encoding.Schema) []*Record {
	recs := make([]*Record, 40)
	for i := range recs {
		x, z := float64(i), float64((i*7)%11)+0.5
		recs[i] = NewRecord(schema, map[string]float64{"x": x, "z": z, "u": 2 * z, "c": 5, "y": 1 + 2*x + z})
		recs[i].SetMergePosition(0, 0, i)
	}
	return recs
}

// slotFailureRequest carries one failing and one good slot of each
// per-slot kind beside an aggregation: a rank-deficient OLS, a
// correlation over a constant column and a singular partial
// correlation. agg is the aggregation (AGG_MEDIAN forces the buffered
// path; AGG_AVERAGE streams).
func slotFailureRequest(agg types.AggregationType) *types.Request {
	return &types.Request{
		Aggregations: []*types.Aggregation{{Type: agg, Field: "y", Label: "a"}},
		Tests: []*types.Test{
			{Type: types.TEST_PEARSON_R, Field: "x", Field2: "c", Label: "bad_t"},
			{Type: types.TEST_PEARSON_R, Field: "x", Field2: "z", Label: "good_t"},
		},
		Regressions: []*types.RegressionSpec{
			{Type: types.REG_OLS, Name: "bad_r", Target: "y", Predictors: []string{"z", "u"}},
			{Type: types.REG_OLS, Name: "good_r", Target: "y", Predictors: []string{"x"}},
		},
		Matrices: []types.MatrixSpec{
			{Name: "bad_m", Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"x", "z", "u"}},
			{Name: "good_m", Type: types.MAT_CORRELATION, Fields: []string{"x", "z"}},
		},
	}
}

// TestSlotFailure_FailsAloneOnEveryPath: a regression, test or matrix
// slot that fails on its data carries the refusal as its Error (its
// identity kept, never nil) while the aggregation and the good slots
// survive — on the streaming path (terminal flush) and the buffered
// path alike, with the same failed and surviving slots.
func TestSlotFailure_FailsAloneOnEveryPath(t *testing.T) {
	var wires []string
	for _, c := range []struct {
		agg  types.AggregationType
		path ProcessPath
	}{{types.AGG_AVERAGE, PathStreaming}, {types.AGG_MEDIAN, PathBuffered}} {
		schema := slotFailureSchema()
		proc := NewProcessor(schema)
		resp, err := proc.Process(context.Background(), slotFailureRequest(c.agg), NewSliceIterator(slotFailureRecords(schema)))
		if err != nil {
			t.Fatalf("%s: Process: %v", c.path, err)
		}
		if proc.LastPath() != c.path {
			t.Fatalf("ran %v, want %v", proc.LastPath(), c.path)
		}
		if len(resp.Data) != 1 || resp.Data[0]["a"] == nil {
			t.Errorf("%s: aggregation lost: %v", c.path, resp.Data)
		}
		checkFailed := func(kind, label string, typ any, se *types.SlotError, want errors.Code) {
			t.Helper()
			if se == nil || se.Code != string(want) || se.Message == "" {
				t.Errorf("%s %s %s: error %+v, want %s", c.path, kind, label, se, want)
			}
		}
		if len(resp.Tests) != 2 || resp.Tests[0] == nil || resp.Tests[1] == nil {
			t.Fatalf("%s: tests %v", c.path, resp.Tests)
		}
		bt := resp.Tests[0]
		checkFailed("test", bt.Label, bt.Type, bt.Error, errors.PULSE_TEST_CORRELATION_UNDEFINED)
		if bt.Label != "bad_t" || bt.Type != types.TEST_PEARSON_R || !math.IsNaN(bt.Statistic) || !math.IsNaN(bt.PValue) {
			t.Errorf("%s: failed test %+v, want its identity and NaN figures", c.path, bt)
		}
		if resp.Tests[1].Error != nil || math.IsNaN(resp.Tests[1].Statistic) {
			t.Errorf("%s: good test %+v", c.path, resp.Tests[1])
		}
		if len(resp.Regressions) != 2 || resp.Regressions[0] == nil || resp.Regressions[1] == nil {
			t.Fatalf("%s: regressions %v", c.path, resp.Regressions)
		}
		br := resp.Regressions[0]
		checkFailed("regression", br.Name, br.Type, br.Error, errors.PROCESSING_REGRESSION_RANK_DEFICIENT)
		if br.Name != "bad_r" || br.Type != types.REG_OLS || br.Coefficients != nil {
			t.Errorf("%s: failed regression %+v, want its identity only", c.path, br)
		}
		if resp.Regressions[1].Error != nil || math.Abs(resp.Regressions[1].Coefficients["x"]-2) > 0.5 {
			t.Errorf("%s: good regression %+v", c.path, resp.Regressions[1])
		}
		if len(resp.Matrices) != 2 {
			t.Fatalf("%s: matrices %v", c.path, resp.Matrices)
		}
		bm := resp.Matrices[0]
		checkFailed("matrix", bm.Name, bm.Type, bm.Error, errors.PULSE_MATRIX_SINGULAR)
		if bm.Name != "bad_m" || bm.Type != types.MAT_PARTIAL_CORRELATION || bm.Primary != nil {
			t.Errorf("%s: failed matrix %+v, want its identity only", c.path, bm)
		}
		if resp.Matrices[1].Error != nil || resp.Matrices[1].Primary == nil {
			t.Errorf("%s: good matrix %+v", c.path, resp.Matrices[1])
		}
		// One Components entry per SURVIVING matrix: the failed slot has
		// no figures to count.
		if resp.Components == nil || len(resp.Components.Matrices) != 1 || resp.Components.Matrices[0].Name != "good_m" {
			t.Errorf("%s: matrix components %+v", c.path, resp.Components)
		}
		b, err := json.Marshal(struct {
			T []*types.TestResult       `json:"t"`
			R []*types.RegressionResult `json:"r"`
			M []types.MatrixResult      `json:"m"`
		}{resp.Tests, resp.Regressions, resp.Matrices})
		if err != nil {
			t.Fatal(err)
		}
		wires = append(wires, string(b))
	}
	if wires[0] != wires[1] {
		t.Errorf("streaming and buffered disagree:\n streaming %s\n buffered  %s", wires[0], wires[1])
	}
}

// TestSlotFailure_WireIsIdentityPlusError: a failed result writes its
// identity and the error only — no statistic, coefficient or matrix
// key, never null — and a slot that did not fail is byte-identical to
// the pre-SlotError form (no `error` key).
func TestSlotFailure_WireIsIdentityPlusError(t *testing.T) {
	se := &types.SlotError{Code: "PULSE_TEST_VARIANCE_ZERO", Message: "m", Details: map[string]any{"k": 1}}
	for name, c := range map[string]struct {
		v    any
		want string
	}{
		"test": {&types.TestResult{Label: "l", Type: types.TEST_T, Statistic: math.NaN(), PValue: math.NaN(), Alpha: 0.05, Error: se},
			`{"label":"l","type":"TEST_T","error":{"code":"PULSE_TEST_VARIANCE_ZERO","message":"m","details":{"k":1}}}`},
		"grouped test": {&types.TestResult{Type: types.TEST_T, GroupKey: types.AxisKey{"a"}, Error: se},
			`{"type":"TEST_T","group_key":["a"],"error":{"code":"PULSE_TEST_VARIANCE_ZERO","message":"m","details":{"k":1}}}`},
		"regression": {&types.RegressionResult{Name: "r", Type: types.REG_GLM, NObs: 4, Error: se},
			`{"name":"r","type":"REG_GLM","error":{"code":"PULSE_TEST_VARIANCE_ZERO","message":"m","details":{"k":1}}}`},
		"matrix": {types.MatrixResult{Name: "m", Type: types.MAT_PCA, Error: se},
			`{"name":"m","type":"MAT_PCA","error":{"code":"PULSE_TEST_VARIANCE_ZERO","message":"m","details":{"k":1}}}`},
		"fit": {types.FitResult{RegressionResult: &types.RegressionResult{Name: "f", Type: types.REG_GLM, Error: se}},
			`{"name":"f","type":"REG_GLM","error":{"code":"PULSE_TEST_VARIANCE_ZERO","message":"m","details":{"k":1}}}`},
	} {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(b) != c.want {
			t.Errorf("%s:\n got  %s\n want %s", name, b, c.want)
		}
	}
	ok, err := json.Marshal(&types.TestResult{Type: types.TEST_T, Statistic: 1, PValue: 0.5, Alpha: 0.05})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ok), `"error"`) {
		t.Errorf("a surviving result writes error: %s", ok)
	}
}

// TestSlotFailure_ConfigFaultStaysFatal: only a data-dependent refusal
// is per-slot — a configuration fault (a test on a field it cannot
// read) still fails the whole run.
func TestSlotFailure_ConfigFaultStaysFatal(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	recs := make([]*Record, 10)
	for i := range recs {
		recs[i] = NewRecord(schema, map[string]float64{"y": float64(i), "g": 0})
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y"}},
		Tests:        []*types.Test{{Type: types.TEST_T, Field: "g", Params: json.RawMessage(`{"mu":1}`)}},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
	if code := hotCode(err); code != errors.PULSE_TEST_FIELD_NOT_NUMERIC || resp != nil {
		t.Fatalf("config fault: resp %v err %v, want the fatal PULSE_TEST_FIELD_NOT_NUMERIC", resp, err)
	}
}

// TestSlotFailure_PostTestFailsAlone: a tier-2 post-test whose result
// rows it cannot carry (a correlation against a constant column) fails
// alone beside a good one.
func TestSlotFailure_PostTestFailsAlone(t *testing.T) {
	schema := slotFailureSchema()
	req := &types.Request{
		Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "x"}}}},
		PostTests: []*types.Test{
			{Type: types.TEST_PEARSON_R, Field: "x", Field2: "c", Label: "bad_p"},
			{Type: types.TEST_PEARSON_R, Field: "x", Field2: "z", Label: "good_p"},
		},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(slotFailureRecords(schema)))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.PostTests) != 2 || resp.PostTests[0] == nil || resp.PostTests[1] == nil {
		t.Fatalf("post tests %v", resp.PostTests)
	}
	if e := resp.PostTests[0].Error; e == nil || e.Code != string(errors.PULSE_TEST_CORRELATION_UNDEFINED) || resp.PostTests[0].Label != "bad_p" {
		t.Errorf("failed post-test %+v", resp.PostTests[0])
	}
	if resp.PostTests[1].Error != nil || math.IsNaN(resp.PostTests[1].Statistic) {
		t.Errorf("good post-test %+v", resp.PostTests[1])
	}
	if len(resp.Data) != 40 {
		t.Errorf("rows lost: %d", len(resp.Data))
	}
}
