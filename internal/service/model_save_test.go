package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// model_save_test.go pins the model-file save: a RegressionSpec.Save or
// FitSpec.Save writes a file that loads back to the bit-identical bound
// model, atomically, warning on overwrite and refusing a bad path before
// any record is read.

const savePath = "models/m" + model.FileSuffix

func readModel(t *testing.T, fsys afero.Fs, path string) *model.File {
	t.Helper()
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	f, err := model.DecodeFile(path, data)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return f
}

func bitsEqual(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

// assertSameBound: every scoring figure bit for bit.
func assertSameBound(t *testing.T, got, want *model.Bound) {
	t.Helper()
	if got.Type != want.Type || got.Target != want.Target || got.Family != want.Family || got.Link != want.Link ||
		!reflect.DeepEqual(got.Predictors, want.Predictors) || !reflect.DeepEqual(got.Terms, want.Terms) {
		t.Fatalf("identity: got %+v\nwant %+v", got, want)
	}
	scal := [][2]float64{
		{got.Intercept, want.Intercept}, {got.InterceptStdErr, want.InterceptStdErr},
		{got.InterceptCILower, want.InterceptCILower}, {got.InterceptCIUpper, want.InterceptCIUpper}, {got.ConfLevel, want.ConfLevel},
	}
	for i, p := range scal {
		if !bitsEqual(p[0], p[1]) {
			t.Errorf("intercept figure %d = %v, want %v", i, p[0], p[1])
		}
	}
	for _, s := range [][2][]float64{{got.Coefficients, want.Coefficients}, {got.StdErrors, want.StdErrors}, {got.CILower, want.CILower}, {got.CIUpper, want.CIUpper}} {
		for i := range s[1] {
			if !bitsEqual(s[0][i], s[1][i]) {
				t.Errorf("predictor %s figure = %v, want %v", want.Predictors[i], s[0][i], s[1][i])
			}
		}
	}
	if (got.Vcov == nil) != (want.Vcov == nil) {
		t.Fatalf("vcov presence differs")
	}
	if want.Vcov != nil {
		for i := range want.Vcov.Values {
			for j := range want.Vcov.Values[i] {
				if !bitsEqual(got.Vcov.Values[i][j], want.Vcov.Values[i][j]) {
					t.Errorf("vcov[%d][%d] = %v, want %v", i, j, got.Vcov.Values[i][j], want.Vcov.Values[i][j])
				}
			}
		}
	}
}

// selCohort is y = 1 + 2x + noise with z a pure-noise column, so a
// backward BIC selection over {x, z} keeps x only.
const selCohort = "sel.pulse"

func writeSelCohort(t *testing.T, fsys afero.Fs) {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "z", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 16},
	}}
	rows := make([][]uint64, 200)
	for i := range rows {
		x := float64(i % 23)
		z := float64((i*7919)%101) / 10
		y := 1 + 2*x + float64((i*31)%11-5)/4
		rows[i] = []uint64{math.Float64bits(x), math.Float64bits(z), math.Float64bits(y)}
	}
	if err := afero.WriteFile(fsys, selCohort, writePulseFile(t, schema, rows), 0o644); err != nil {
		t.Fatal(err)
	}
}

func saveRequest(spec *types.RegressionSpec) *types.Request {
	spec.Save = savePath
	return &types.Request{Cohort: &types.Cohort{Filename: fitsCohort}, Regressions: []*types.RegressionSpec{spec}}
}

// TestModelSave_ProcessRoundTripsBitExact: for every scorable regression
// type and fit shape — plain, vcov, selection, resample, weighted, and a
// grouped request (whose regression is cohort-wide) — the saved file
// loads back to exactly the Bound the in-request result binds to.
func TestModelSave_ProcessRoundTripsBitExact(t *testing.T) {
	cases := []struct {
		name string
		spec *types.RegressionSpec
		mod  func(*types.Request)
	}{
		{"ols vcov", &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x", "week"}, Vcov: true}, nil},
		{"glm", &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Target: "week", Family: "poisson", Predictors: []string{"x"}}, nil},
		{"bayes", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Name: "m", Target: "y", Predictors: []string{"x"}}, nil},
		{"selection", &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x", "z"}, Selection: "backward", Criterion: "bic"}, func(r *types.Request) {
			r.Cohort.Filename = selCohort
		}},
		{"resample", &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}, Resample: "jackknife"}, nil},
		{"weighted", &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"},
			Weight: types.SlotWeightOf(types.WeightSpec{Field: "x", Kind: types.WeightKindProbability})}, nil},
		{"grouped request", &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}}, func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 52}}
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := fitsService(t)
			writeSelCohort(t, svc.fs.Fs())
			req := saveRequest(tc.spec)
			if tc.mod != nil {
				tc.mod(req)
			}
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Warnings) != 0 {
				t.Fatalf("a first save warns: %+v", resp.Warnings)
			}
			f := readModel(t, svc.fs.Fs(), savePath)
			want, err := model.Bind("m", tc.spec, resp.Regressions[0])
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.Bind("m")
			if err != nil {
				t.Fatal(err)
			}
			assertSameBound(t, got, want)
			if tc.spec.Selection != "" && len(got.Predictors) >= len(got.Terms) {
				t.Errorf("vacuous: selection kept every candidate (%v of %v)", got.Predictors, got.Terms)
			}
			res := resp.Regressions[0]
			if f.NObs != res.NObs || !bitsEqual(float64(f.SumWeights), res.SumWeights) || !bitsEqual(float64(f.NEff), res.NEff) || !bitsEqual(float64(f.DF), res.DF) {
				t.Errorf("sample sizes: file %+v, result n=%d Σw=%v neff=%v df=%v", f, res.NObs, res.SumWeights, res.NEff, res.DF)
			}
			if f.Provenance.Cohort != req.Cohort.Filename || !strings.HasPrefix(f.Provenance.Fingerprint, "sha256:") || f.Provenance.RequestDigest != req.Hash() {
				t.Errorf("provenance = %+v", f.Provenance)
			}
		})
	}
}

// TestModelSave_OverwriteWarns: a second save to the same path replaces
// the file and reports PULSE_MODEL_OVERWRITTEN (details.path, name,
// regression).
func TestModelSave_OverwriteWarns(t *testing.T) {
	svc := fitsService(t)
	first := saveRequest(&types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}})
	if _, err := svc.Process(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := saveRequest(&types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"week"}})
	second.Regressions = append([]*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}}, second.Regressions...)
	resp, err := svc.Process(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one overwrite", resp.Warnings)
	}
	w := resp.Warnings[0]
	if w.Code != string(perr.PULSE_MODEL_OVERWRITTEN) || w.Details["path"] != savePath || w.Details["name"] != "m" || w.Details["regression"] != 1 {
		t.Fatalf("warning = %+v", w)
	}
	if f := readModel(t, svc.fs.Fs(), savePath); !reflect.DeepEqual(f.Predictors, []string{"week"}) {
		t.Fatalf("file not replaced: predictors %v", f.Predictors)
	}
}

// TestModelSave_ReturnNeverSkipsASave: a `return` that drops the
// regressions still fits (and saves) a regression that sets save.
func TestModelSave_ReturnNeverSkipsASave(t *testing.T) {
	svc := fitsService(t)
	req := saveRequest(&types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}})
	req.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "s"}}
	req.Return = &types.Return{Include: []string{"data"}}
	if _, err := svc.Process(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	readModel(t, svc.fs.Fs(), savePath)
}

// TestModelSave_PathRefusedBeforeRun: an invalid path is
// PULSE_MODEL_SAVE_INVALID before any record is read, and writes nothing.
func TestModelSave_PathRefusedBeforeRun(t *testing.T) {
	svc := fitsService(t)
	req := saveRequest(&types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}})
	req.Regressions[0].Save = "../escape" + model.FileSuffix
	_, err := svc.Process(context.Background(), req)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_SAVE_INVALID || ce.Details["reason"] != model.ReasonSaveEscapesRoot || ce.Details["regression"] != 0 {
		t.Fatalf("err = %v", err)
	}
	if names := listAll(t, svc.fs.Fs()); !reflect.DeepEqual(names, []string{fitsCohort}) {
		t.Fatalf("files after a refused save: %v", names)
	}
}

// faultFs fails one stage of an atomic write: the temp file's create
// (OpenFile), its Write (after writing half), its Sync, or the Rename.
type faultFs struct {
	afero.Fs
	stage string
}

var errInjected = stderrors.New("injected fault")

func (f faultFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if f.stage == "create" && strings.HasSuffix(name, ".tmp") {
		return nil, errInjected
	}
	file, err := f.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return faultFile{File: file, stage: f.stage}, nil
}

func (f faultFs) Rename(oldname, newname string) error {
	if f.stage == "rename" {
		return errInjected
	}
	return f.Fs.Rename(oldname, newname)
}

type faultFile struct {
	afero.File
	stage string
}

func (f faultFile) Write(p []byte) (int, error) {
	if f.stage == "write" {
		n, _ := f.File.Write(p[:len(p)/2])
		return n, errInjected
	}
	return f.File.Write(p)
}

func (f faultFile) Sync() error {
	if f.stage == "sync" {
		return errInjected
	}
	return f.File.Sync()
}

func listAll(t *testing.T, fsys afero.Fs) []string {
	t.Helper()
	var out []string
	if err := afero.Walk(fsys, "", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			out = append(out, strings.TrimPrefix(p, string(os.PathSeparator)))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// TestModelSave_FailedWriteLeavesTargetIntact: a failure at any stage of
// the atomic write is PULSE_MODEL_WRITE_FAILED naming the stage; the
// existing file keeps its bytes and no temp file is left behind.
func TestModelSave_FailedWriteLeavesTargetIntact(t *testing.T) {
	for _, stage := range []string{"create", "write", "sync", "rename"} {
		t.Run(stage, func(t *testing.T) {
			base := setupTestFS(t, fitsCohort, fitsSchema(), fitsRecords()).Fs()
			const old = "previous model bytes"
			if err := base.MkdirAll("models", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := afero.WriteFile(base, savePath, []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := fs.New(fs.WithFs(faultFs{Fs: base, stage: stage}))
			if err != nil {
				t.Fatal(err)
			}
			svc := New(cfg)
			req := saveRequest(&types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}})
			_, err = svc.Process(context.Background(), req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_WRITE_FAILED || ce.Details["stage"] != stage || ce.Details["path"] != savePath {
				t.Fatalf("err = %v", err)
			}
			if got, _ := afero.ReadFile(base, savePath); string(got) != old {
				t.Fatalf("target changed: %q", got)
			}
			want := []string{fitsCohort, savePath}
			sort.Strings(want)
			if names := listAll(t, base); !reflect.DeepEqual(names, want) {
				t.Fatalf("files after a failed write: %v, want %v", names, want)
			}
		})
	}
}

// TestModelSave_FitsSave: a `fits` entry's save writes the bound model
// under the fit's name; it loads back bit-identical to the model every
// slot reads; a second batch overwrites with a batch warning
// (details.fit), serial and parallel alike — a save on the fit
// request's own regression included.
func TestModelSave_FitsSave(t *testing.T) {
	o := observeFits(t)
	svc := fitsService(t)
	regSave := fitRequest("0", "103")
	regSave.Regressions[0].Save = "models/all" + model.FileSuffix
	c := &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "train", Request: fitRequest("0", "90"), Save: savePath},
			{Name: "all", Request: regSave},
		},
		Requests: []*types.Request{fitsSlot("s", "0", "10")},
	}
	first, err := svc.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Warnings) != 0 {
		t.Fatalf("first batch warns: %+v", first.Warnings)
	}
	f := readModel(t, svc.fs.Fs(), savePath)
	if f.Name != "train" {
		t.Fatalf("saved under %q", f.Name)
	}
	loaded, err := f.Bind("train")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := o.slots["s"].Lookup("train")
	if err != nil {
		t.Fatal(err)
	}
	assertSameBound(t, loaded, bound)
	readModel(t, svc.fs.Fs(), "models/all"+model.FileSuffix)

	for mode, resp := range composeBoth(t, svc, c) {
		if len(resp.Warnings) != 2 {
			t.Fatalf("%s: warnings = %+v, want two overwrites", mode, resp.Warnings)
		}
		// Entry order: fit 0's own save, then fit 1's regression save.
		for i, w := range resp.Warnings {
			if w.Code != string(perr.PULSE_MODEL_OVERWRITTEN) || w.Details["fit"] != i {
				t.Errorf("%s: warning %d = %+v", mode, i, w)
			}
		}
		raw, _ := json.Marshal(resp)
		if !strings.Contains(string(raw), `"warnings":[{"code":"PULSE_MODEL_OVERWRITTEN"`) {
			t.Errorf("%s: warnings not on the wire: %s", mode, raw)
		}
	}
}

// TestModelSave_FitsPathRefusedBeforeAnyFit: a later fit's bad save
// path refuses the batch before any fit runs, so the earlier fit writes
// nothing.
func TestModelSave_FitsPathRefusedBeforeAnyFit(t *testing.T) {
	o := observeFits(t)
	svc := fitsService(t)
	c := &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "a", Request: fitRequest("0", "90"), Save: savePath},
			{Name: "b", Request: fitRequest("0", "90"), Save: "b.json"},
		},
		Requests: []*types.Request{fitsSlot("s", "0", "10")},
	}
	_, err := svc.Compose(context.Background(), c)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_SAVE_INVALID || ce.Details["fit"] != 1 {
		t.Fatalf("err = %v", err)
	}
	if len(o.runs) != 0 {
		t.Fatalf("fits ran: %v", o.runs)
	}
	if names := listAll(t, svc.fs.Fs()); !reflect.DeepEqual(names, []string{fitsCohort}) {
		t.Fatalf("files: %v", names)
	}
}
