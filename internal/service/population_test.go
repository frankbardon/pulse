package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// population_test.go pins population files (E2-S3): a Compose vector
// fit's `save` writes a .pulse-population.json, and a plain Process on
// ANOTHER cohort loading it through `populations` scores every
// consumer — MAT_* and fitted attributes — to the bit of the Compose
// slot that read the fit by name. Load refusals carry the model-file
// codes, located by details.population.

const (
	popOtherCohort = "other.pulse"
	popPath        = "pops/brand" + model.PopulationSuffix
)

// popService is vecFitsService plus a second cohort with the same
// columns over different rows.
func popService(t *testing.T) (*Service, afero.Fs) {
	t.Helper()
	cfg := setupTestFS(t, vecFitsCohort, vecFitsSchema(), vecFitsRecords())
	other := make([][]uint64, 80)
	for i := range other {
		a := float64((i*13)%11) - 0.5*float64(i%4)
		b := -0.2*a + float64((i*5)%7) + 0.03*float64(i)
		c := 0.4*a - 0.1*b + float64((i*3)%6)
		other[i] = []uint64{uint64(i), math.Float64bits(a), math.Float64bits(b), math.Float64bits(c)}
	}
	if err := afero.WriteFile(cfg.Fs(), popOtherCohort, writePulseFile(t, vecFitsSchema(), other), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(cfg), cfg.Fs()
}

// popConsumers are the slots that read a vector fit by name, over the
// other cohort: MAT_COVARIANCE (members in a permuted order — matched
// by label), MAT_PCA, ATTR_MAHALANOBIS and ATTR_PC_SCORE.
func popConsumers(fit string) []*types.Request {
	on := func(label string) *types.Request {
		return &types.Request{Label: label, Cohort: &types.Cohort{Filename: popOtherCohort}}
	}
	cov := on("cov")
	cov.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "m", Fields: []string{"c", "a", "b"}, Params: json.RawMessage(`{"fit":"` + fit + `"}`)}}
	pca := on("pca")
	pca.Matrices = []types.MatrixSpec{{Type: types.MAT_PCA, Name: "m", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(`{"fit":"` + fit + `","components":2}`)}}
	mahal := on("mahal")
	mahal.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(`{"fit":"` + fit + `"}`)}}
	mahal.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "sum_d"}, {Type: types.AGG_MAX, Field: "d", Label: "max_d"}}
	pc := on("pc")
	pc.Attributes = []*types.Attribute{{Type: types.ATTR_PC_SCORE, Label: "s", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(`{"fit":"` + fit + `","component":1}`)}}
	pc.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "sum_s"}, {Type: types.AGG_MIN, Field: "s", Label: "min_s"}}
	return []*types.Request{cov, pca, mahal, pc}
}

// responseBits is every float a slot emitted (matrix values in row
// order, then data values in key order), as bits.
func responseBits(t *testing.T, resp *types.Response) []uint64 {
	t.Helper()
	var out []uint64
	for _, m := range resp.Matrices {
		for _, row := range m.Primary.Values {
			for _, v := range row {
				out = append(out, math.Float64bits(v))
			}
		}
	}
	for _, row := range resp.Data {
		for _, k := range []string{"sum_d", "max_d", "sum_s", "min_s"} {
			if v, ok := row[k].(float64); ok {
				out = append(out, math.Float64bits(v))
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("slot emitted no figure: %+v", resp)
	}
	return out
}

// TestPopulation_SaveThenLoadMatchesFit: the acceptance round trip.
// Compose fits a battery over weeks 0–59 of one cohort, saves it, and
// four slots over another cohort read it with params.fit; a plain
// Process over that other cohort loading the saved file under another
// name gives every slot's figures to the bit. The file is a valid
// population file, the fit's `fits` entry is unchanged by the save,
// and serial and parallel Compose write identically.
func TestPopulation_SaveThenLoadMatchesFit(t *testing.T) {
	t.Parallel()

	for _, battery := range []string{"matrix", "attribute"} {
		t.Run(battery, func(t *testing.T) {
			t.Parallel()

			svc, fsys := popService(t)
			fitReq := vecWeeks("", "0", "59")
			if battery == "matrix" {
				fitReq.Matrices = []types.MatrixSpec{vecMatrix(types.MAT_COVARIANCE, "cov", "")}
			} else {
				fitReq.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"a", "b", "c"}}}
			}
			c := &types.ComposedRequest{
				Fits:     []types.FitSpec{{Name: "pop", Request: fitReq, Save: popPath}},
				Requests: popConsumers("pop"),
			}
			composed, err := svc.Compose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(composed.Warnings) != 0 {
				t.Fatalf("first save warned: %+v", composed.Warnings)
			}
			data, err := afero.ReadFile(fsys, popPath)
			if err != nil {
				t.Fatalf("no population file: %v", err)
			}
			f, err := model.DecodePopulationFile(popPath, data)
			if err != nil {
				t.Fatal(err)
			}
			if f.Name != "pop" || f.Provenance.Cohort != vecFitsCohort || f.Provenance.RequestDigest == "" || f.N != 60 {
				t.Fatalf("file %+v", f)
			}
			if v := composed.Fits["pop"].Vector; v == nil || v.N != 60 || len(v.Labels) != 3 {
				t.Fatalf("fits[pop] = %+v", composed.Fits["pop"])
			}

			for i, slot := range popConsumers("brand") {
				slot.Populations = []types.PopulationRef{{Name: "brand", Path: popPath}}
				resp, err := svc.Process(context.Background(), slot)
				if err != nil {
					t.Fatalf("%s: %v", slot.Label, err)
				}
				got, want := responseBits(t, resp), responseBits(t, composed.Responses[i])
				if len(got) != len(want) {
					t.Fatalf("%s: %d figures, want %d", slot.Label, len(got), len(want))
				}
				for j := range want {
					if got[j] != want[j] {
						t.Fatalf("%s: figure %d = %v, want %v (the loaded population is not the fit)",
							slot.Label, j, math.Float64frombits(got[j]), math.Float64frombits(want[j]))
					}
				}
			}
			// The slots do not reproduce their own rows: a fit-free twin
			// differs, so the comparison above can bite.
			own := popConsumers("x")[0]
			own.Matrices[0].Params = nil
			ownResp, err := svc.Process(context.Background(), own)
			if err != nil {
				t.Fatal(err)
			}
			if b := responseBits(t, ownResp); b[0] == responseBits(t, composed.Responses[0])[0] {
				t.Fatal("the other cohort's own covariance equals the fit's: the test cannot bite")
			}

			// Serial and parallel write the same bytes.
			if _, err := svc.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 3}); err != nil {
				t.Fatal(err)
			}
			again, err := afero.ReadFile(fsys, popPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != string(data) {
				t.Fatalf("parallel save differs:\n%s\nvs\n%s", again, data)
			}
		})
	}
}

// TestPopulation_OverwriteWarns: a second save over an existing file
// replaces it and warns PULSE_MODEL_OVERWRITTEN with details.fit, in the
// batch warnings.
func TestPopulation_OverwriteWarns(t *testing.T) {
	t.Parallel()

	svc, _ := popService(t)
	fitReq := vecWeeks("", "0", "59")
	fitReq.Matrices = []types.MatrixSpec{vecMatrix(types.MAT_CORRELATION, "corr", "")}
	c := &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "pop", Request: fitReq, Save: popPath}},
		Requests: []*types.Request{vecWeeks("n", "0", "9")},
	}
	c.Requests[0].Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}}
	for run := 0; run < 2; run++ {
		resp, err := svc.Compose(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			if len(resp.Warnings) != 0 {
				t.Fatalf("first save warned: %+v", resp.Warnings)
			}
			continue
		}
		if len(resp.Warnings) != 1 {
			t.Fatalf("warnings = %+v, want one overwrite", resp.Warnings)
		}
		w := resp.Warnings[0]
		if w.Code != string(perr.PULSE_MODEL_OVERWRITTEN) || w.Details["fit"] != 0 || w.Details["path"] != popPath || w.Details["name"] != "pop" ||
			!strings.Contains(w.Message, "population") {
			t.Fatalf("warning %+v", w)
		}
	}
}

// TestPopulation_LoadRefusals: a bad `populations` entry is refused
// before any record is read with the model-file code, located by
// details.population; a population name shares the namespace with
// `models`; params.fit naming an unloaded name lists the populations as
// available vector fits.
func TestPopulation_LoadRefusals(t *testing.T) {
	t.Parallel()

	svc, fsys := popService(t)
	fitReq := vecWeeks("", "0", "59")
	fitReq.Matrices = []types.MatrixSpec{vecMatrix(types.MAT_COVARIANCE, "cov", "")}
	if _, err := svc.Compose(context.Background(), &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "pop", Request: fitReq, Save: popPath}},
		Requests: []*types.Request{popConsumers("pop")[0]},
	}); err != nil {
		t.Fatal(err)
	}
	_ = afero.WriteFile(fsys, "pops/bad"+model.PopulationSuffix, []byte(`{"population_format_version": 1, "labels": []}`), 0o644)
	_ = afero.WriteFile(fsys, "pops/v9"+model.PopulationSuffix, []byte(`{"population_format_version": 9}`), 0o644)

	load := func(refs ...types.PopulationRef) *types.Request {
		r := popConsumers("brand")[0]
		r.Populations = refs
		return r
	}
	pr := func(name, path string) types.PopulationRef { return types.PopulationRef{Name: name, Path: path} }
	collide := load(pr("brand", popPath))
	collide.Models = []types.ModelRef{{Name: "brand", Path: "m" + model.FileSuffix}}
	cases := []struct {
		name   string
		req    *types.Request
		code   perr.Code
		reason string
	}{
		{"missing", load(pr("brand", "pops/none"+model.PopulationSuffix)), perr.PULSE_MODEL_FILE_UNREADABLE, model.ReasonFileMissing},
		{"invalid", load(pr("brand", "pops/bad"+model.PopulationSuffix)), perr.PULSE_MODEL_FILE_INVALID, model.ReasonFileSchema},
		{"version", load(pr("brand", "pops/v9"+model.PopulationSuffix)), perr.PULSE_MODEL_VERSION_UNSUPPORTED, ""},
		{"model suffix", load(pr("brand", "m"+model.FileSuffix)), perr.PULSE_MODEL_REF_INVALID, model.ReasonSaveSuffix},
		{"name collides with a model", collide, perr.PULSE_MODEL_NAME_COLLISION, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Process(context.Background(), tc.req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("details %+v, want reason %s", ce.Details, tc.reason)
			}
			if tc.code != perr.PULSE_MODEL_NAME_COLLISION && ce.Details["population"] != 0 {
				t.Fatalf("not located at the entry: %+v", ce.Details)
			}
		})
	}
	unknown := popConsumers("nope")[0]
	unknown.Populations = []types.PopulationRef{pr("brand", popPath)}
	_, err := svc.Process(context.Background(), unknown)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_UNKNOWN {
		t.Fatalf("err = %v", err)
	}
	if av, _ := ce.Details["available"].([]string); len(av) != 1 || av[0] != "brand" {
		t.Fatalf("available = %v, want the loaded population", ce.Details["available"])
	}
}
