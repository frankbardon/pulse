package descriptor

import (
	"context"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// windows_first_catalog_test.go: predict and the engine read ONE
// windows-first decision, over the same bound-model catalog
// (mmm-followups E4-S6). A request windows-first only through a bound
// model's terms admits a regression, a holdout and a vector member
// reading the window label — the walk, the vector echo, streamability
// and the post-window plan all over the catalog — and the engine runs
// it over the same catalog.

// catalogTriggeredRequest: m's one term is the window label lag_n, so
// the aggregation over its score p turns the mode on; nothing else
// reads lag_n but the regression, the holdout and vector v.
func catalogTriggeredRequest() *types.Request {
	return &types.Request{
		Windows:      []*types.Window{lagWindow("lag_n")},
		Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: []byte(`{"fit":"fm"}`)}},
		Aggregations: []*types.Aggregation{sumField("p", "s")},
		Regressions: []*types.RegressionSpec{
			{Type: types.REG_OLS, Target: "m", Predictors: []string{"lag_n"}},
			{Type: types.REG_OLS, Target: "m", Predictors: []string{"n"}, Holdout: "lag_n > 3"},
		},
		Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{"n", "lag_n"}}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Vector: "v"}},
	}
}

// catalogTriggeredSet binds fm over the term lag_n (hand-made result).
func catalogTriggeredSet(t *testing.T) *model.Set {
	t.Helper()
	b, err := model.Bind("fm", &types.RegressionSpec{Type: types.REG_OLS, Target: "m", Predictors: []string{"lag_n"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{model.InterceptKey: 1, "lag_n": 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	set := &model.Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	return set
}

// TestWindowsFirst_CatalogTriggeredPredictMatchesRuntime: over the
// catalog the walk admits the request (no
// PULSE_REGRESSION_WINDOW_UNAVAILABLE, no unknown member), the vector
// echo resolves the window member, streamability names windows-first
// and the post-window plan is windows-first; the engine, handed the same
// set, runs it — buffered, the regressions and the matrix fitted.
func TestWindowsFirst_CatalogTriggeredPredictMatchesRuntime(t *testing.T) {
	schema := fieldRefSchema()
	set := catalogTriggeredSet(t)
	req := catalogTriggeredRequest()

	if got := fieldRefRefusalsWithModels(req, schema, nil, nil, set); len(got) != 0 {
		t.Fatalf("walk over the catalog refused: %v", got)
	}
	env := descriptor.NewEnvelope(nil)
	result := &descriptor.PredictResult{}
	predictVectors(env, result, req, schema, false, set)
	if !reflect.DeepEqual(result.ResolvedVectors["v"], []string{"n", "lag_n"}) {
		t.Fatalf("resolved vectors %v, want v = [n lag_n]", result.ResolvedVectors)
	}
	ok, reasons := computeStreamable(req, schema, (&PredictOptions{}).withModels(set))
	if ok || !strings.Contains(strings.Join(reasons, "|"), "windows-first") {
		t.Fatalf("streamable %v, reasons %v; want buffered, windows-first", ok, reasons)
	}
	if plan := classifyPostWindow(req, schema, nil, set); plan == nil || !plan.WindowsFirst {
		t.Fatalf("post-window plan %+v, want windows-first", plan)
	}
	if processing.CanStreamRequest(req, schema) {
		t.Fatal("runtime streams a windows-first request")
	}

	recs := make([]*processing.Record, 20)
	for i := range recs {
		recs[i] = processing.NewRecord(schema, map[string]float64{"n": float64(i%6) + 0.3*float64(i), "m": float64(i*i%7) + float64(i), "cat": 0})
		recs[i].SetMergePosition(0, 0, i)
	}
	p := processing.NewProcessor(schema)
	p.SetModels(set)
	resp, err := p.Process(context.Background(), req, processing.NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	if len(resp.Regressions) != 2 || resp.Regressions[0].Error != nil || resp.Regressions[1].Error != nil || len(resp.Matrices) != 1 {
		t.Fatalf("runtime regressions %+v, matrices %+v", resp.Regressions, resp.Matrices)
	}
}

// TestCrosstab_UnreadWindowPredictMatchesRuntime: a crosstab window no
// axis, cell or margin aggregation reads is refused by the walk (predict
// and the service's entry check) with the engine's RunCrosstab code; a
// crosstab with regressions is admitted, predicted unfused like the
// runtime gate, and its regressions run (processing's
// TestCrosstab_RegressionsFitOnBufferedArm).
func TestCrosstab_UnreadWindowPredictMatchesRuntime(t *testing.T) {
	schema := fieldRefSchema()
	ct := func() *types.CrosstabSpec {
		return &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "m", Interval: 5}}, Cell: sumField("n", "c"), Shape: types.CrosstabShapeLong}
	}
	unread := &types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Crosstab: ct()}
	refs := FieldRefRefusals(unread, schema, nil)
	if len(refs) != 1 || refs[0].Code != errors.PULSE_WINDOW_INVALID || refs[0].Details["reason"] != "crosstab_window_unread" {
		t.Fatalf("walk refusals %v, want the one crosstab_window_unread", refs)
	}
	_, err := processing.NewProcessor(schema).RunCrosstab(context.Background(), unread, nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != refs[0].Code || !reflect.DeepEqual(ce.Details, refs[0].Details) {
		t.Fatalf("runtime %v, want the walk's %v", err, refs[0])
	}
	read := &types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Crosstab: ct()}
	read.Crosstab.Cell = sumField("lag_n", "c")
	if got := FieldRefRefusals(read, schema, nil); len(got) != 0 {
		t.Fatalf("windows-first crosstab refused: %v", got)
	}

	withReg := &types.Request{Crosstab: ct(), Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "m", Predictors: []string{"n"}}}}
	if got := FieldRefRefusals(withReg, schema, nil); len(got) != 0 {
		t.Fatalf("crosstab with regressions refused: %v", got)
	}
	res := predictFromBytes(buildTestPulseFile(t, schema), withReg, nil).Data.(*descriptor.PredictResult)
	fused, _ := processing.CanFuseCrosstab(withReg, schema, nil)
	if fused || res.CrosstabFusable == nil || *res.CrosstabFusable || res.Streamable {
		t.Fatalf("fusion: runtime %v, predict %v (streamable %v); want both buffered", fused, res.CrosstabFusable, res.Streamable)
	}
	if !strings.Contains(strings.Join(res.StreamableReasons, "|"), "crosstab with regressions") {
		t.Fatalf("reasons %v lack the regressions reason", res.StreamableReasons)
	}
}
