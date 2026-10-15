package pulse

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// filter_to_file_models_test.go pins FilterToFileRequest.Models (#393):
// a coef() predicate reads a loaded model exactly as Process's filter
// does, the model file's content enters the dedup (predicate) hash, and
// an instance without capability:models refuses the key as an unknown
// field.

// writeCoefModel saves an OLS model file at path whose x coefficient is
// slope (intercept 0).
func writeCoefModel(t *testing.T, fsys afero.Fs, path string, slope float64) {
	t.Helper()
	f, err := model.NewFile("m", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{model.InterceptKey: 0, "x": slope}}, model.Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

const coefFilterExpr = `x > coef("m", "x") * 4`

func coefFilterRequest() *FilterToFileRequest {
	return &FilterToFileRequest{
		SourcePath: fitsFacadeCohort,
		Expression: coefFilterExpr,
		OutputDir:  "out",
		Models:     []types.ModelRef{ref("m", facadeModel)},
	}
}

// processSumX runs SUM(x) + COUNT over cohort, filtered by expr when
// set, binding the model at facadeModel.
func numOf(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := toFloat(v)
	if !ok {
		t.Fatalf("not a number: %T %v", v, v)
	}
	return f
}

func processSumX(t *testing.T, p *Pulse, cohort, expr string) (float64, float64) {
	t.Helper()
	req := &Request{
		Cohort: &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "sum_x"},
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		},
	}
	if expr != "" {
		req.Models = []types.ModelRef{ref("m", facadeModel)}
		req.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	row := resp.Data[0]
	return numOf(t, row["sum_x"]), numOf(t, row["n"])
}

// TestFilterToFileWithRequest_ModelsBindCoef: a coef() predicate with
// Models writes exactly the rows Process's equivalent filter keeps;
// without Models the same predicate is PULSE_MODEL_UNKNOWN.
func TestFilterToFileWithRequest_ModelsBindCoef(t *testing.T) {
	fsys := fitsFacadeFS(t)
	writeCoefModel(t, fsys, facadeModel, 2)
	_ = fsys.MkdirAll("out", 0o755)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	res, err := p.FilterToFileWithRequest(ctx, coefFilterRequest())
	if err != nil {
		t.Fatalf("FilterToFileWithRequest: %v", err)
	}
	wantSum, wantN := processSumX(t, p, fitsFacadeCohort, coefFilterExpr)
	if wantN == 0 || wantN == 104 {
		t.Fatalf("vacuous fixture: filter keeps %v of 104 rows", wantN)
	}
	if float64(res.RowCount) != wantN {
		t.Errorf("RowCount = %d, Process keeps %v", res.RowCount, wantN)
	}
	gotSum, gotN := processSumX(t, p, res.OutputPath, "")
	if gotSum != wantSum || gotN != wantN {
		t.Errorf("output sum(x), n = %v, %v; Process filter = %v, %v", gotSum, gotN, wantSum, wantN)
	}

	bare := coefFilterRequest()
	bare.Models = nil
	_, err = p.FilterToFileWithRequest(ctx, bare)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_UNKNOWN {
		t.Errorf("no Models: err = %v, want PULSE_MODEL_UNKNOWN", err)
	}

	missing := coefFilterRequest()
	missing.Models = []types.ModelRef{ref("m", "models/none"+model.FileSuffix)}
	_, err = p.FilterToFileWithRequest(ctx, missing)
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_FILE_UNREADABLE {
		t.Errorf("missing model file: err = %v, want PULSE_MODEL_FILE_UNREADABLE", err)
	}
}

// TestFilterToFileWithRequest_ModelIdentityInDedupHash: rewriting the
// model file (same name and path, different fit) lands on a different
// output that is not reused; an unchanged model reuses its output.
func TestFilterToFileWithRequest_ModelIdentityInDedupHash(t *testing.T) {
	fsys := fitsFacadeFS(t)
	writeCoefModel(t, fsys, facadeModel, 2)
	_ = fsys.MkdirAll("out", 0o755)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := p.FilterToFileWithRequest(ctx, coefFilterRequest())
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.FilterToFileWithRequest(ctx, coefFilterRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !again.Reused || again.OutputPath != first.OutputPath {
		t.Errorf("unchanged model: reused=%v path %s vs %s", again.Reused, again.OutputPath, first.OutputPath)
	}

	writeCoefModel(t, fsys, facadeModel, 1)
	changed, err := p.FilterToFileWithRequest(ctx, coefFilterRequest())
	if err != nil {
		t.Fatal(err)
	}
	if changed.Reused || changed.OutputPath == first.OutputPath {
		t.Fatalf("changed model reused the stale output: %+v", changed)
	}
	_, wantN := processSumX(t, p, fitsFacadeCohort, coefFilterExpr)
	if float64(changed.RowCount) != wantN || changed.RowCount == first.RowCount {
		t.Errorf("changed model RowCount = %d (first %d), Process keeps %v", changed.RowCount, first.RowCount, wantN)
	}
}

// TestFilterToFileWithRequest_NoModelsHashUnchanged: a request without
// Models hashes exactly as before the field existed, so every output a
// model-free request already wrote is still reused.
func TestFilterToFileWithRequest_NoModelsHashUnchanged(t *testing.T) {
	req := &FilterToFileRequest{Expression: "age >= 20"}
	legacy := types.CanonicalHash("filter_predicate", struct {
		Expression string            `json:"expression,omitempty"`
		Filterers  []*types.Filterer `json:"filterers,omitempty"`
	}{Expression: req.Expression})
	if got := req.predicateHash(nil); got != legacy {
		t.Errorf("model-free predicate hash moved: %s vs %s", got, legacy)
	}
}

// TestHiddenModels_FilterToFileRefusedAsUnknownField: an instance
// without capability:models refuses FilterToFileRequest.Models as
// PULSE_REQUEST_UNKNOWN_FIELD; a profile enabling it runs.
func TestHiddenModels_FilterToFileRefusedAsUnknownField(t *testing.T) {
	fsys := fitsFacadeFS(t)
	writeCoefModel(t, fsys, facadeModel, 2)
	_ = fsys.MkdirAll("out", 0o755)
	feats := []string{"capability:process", "capability:filter_to_file", "FILTER_EXPRESSION"}
	h, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "no-models", Features: feats}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = h.FilterToFileWithRequest(ctx, coefFilterRequest())
	assertSlotRefusal(t, slotGateFromErr(err), "models", []string{"models"})

	on, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "models", Features: append(feats, "capability:models")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := on.FilterToFileWithRequest(ctx, coefFilterRequest()); err != nil {
		t.Fatalf("models enabled: %v", err)
	}
}
