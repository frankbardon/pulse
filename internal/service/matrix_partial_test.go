package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
	"gonum.org/v1/gonum/mat"
)

// matrix_partial_test.go is MAT_PARTIAL_CORRELATION end to end: the R
// oracle (ppcor::pcor / pcor.test, corpcor::cor2pcor on the weighted
// correlation), the control list (members and outside fields, which
// join the fold and its missing-data mode), the shared PSD guard
// (fatal PULSE_MATRIX_NOT_PSD or the Higham repair) and the singular
// refusal.

// partialOracle is one mv_partial_correlation.json case.
type partialOracle struct {
	Fixture  string       `json:"fixture"`
	Members  []string     `json:"members"`
	Controls []string     `json:"controls"`
	Missing  string       `json:"missing"`
	Weight   string       `json:"weight"`
	Partial  [][]*float64 `json:"partial"`
}

// partialOracleTol is the oracle tolerance: 1e-10 absolute + relative.
// The R side inverts with LAPACK (solve), Pulse with the FMA-free
// reference InverseSPD; on these well-conditioned fixtures the two
// agree to ~1e-13.
const partialOracleTol = 1e-10

func loadMV(t *testing.T, name string, into any) (fixtures string) {
	t.Helper()
	refDir, err := filepath.Abs("../processing/testdata/reference")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(refDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.LastIndex(string(raw), "\n// golden-hash:"); i >= 0 {
		raw = raw[:i]
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return filepath.Join(refDir, "fixtures")
}

func fixtureFS(t *testing.T, dir string) *fs.Config {
	t.Helper()
	cfg, err := fs.New(fs.WithFs(afero.NewOsFs()), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	return cfg
}

func partialSpec(name string, fields []string, params map[string]any, weight string) types.MatrixSpec {
	raw, _ := json.Marshal(params)
	s := types.MatrixSpec{Name: name, Type: types.MAT_PARTIAL_CORRELATION, Fields: fields, Params: raw}
	switch weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

// TestMatrixPartialCorrelation_MatchesROracle pins every case of
// mv_partial_correlation.json — attitude, mtcars, weighted and nulls;
// control "all" and a control list; listwise and pairwise; unweighted,
// frequency (= the rep() expansion) and probability weights (the
// weighted correlation's cor2pcor) — in two request forms that must
// agree: the controls OUTSIDE the vector (they join the fold) and the
// controls INSIDE it (they leave the output axis).
func TestMatrixPartialCorrelation_MatchesROracle(t *testing.T) {
	var golden struct {
		Cases []partialOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_partial_correlation.json", &golden)
	cfg := fixtureFS(t, dir)
	if len(golden.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	for _, c := range golden.Cases {
		ctl := "all"
		if len(c.Controls) > 0 {
			ctl = strings.Join(c.Controls, "+")
		}
		t.Run(fmt.Sprintf("%s/%s/%s/%s", c.Fixture, ctl, c.Missing, c.Weight), func(t *testing.T) {
			params := map[string]any{"missing": c.Missing}
			specs := []types.MatrixSpec{}
			if len(c.Controls) == 0 {
				specs = append(specs, partialSpec("all", c.Members, params, c.Weight))
			} else {
				params["control"] = c.Controls
				specs = append(specs,
					partialSpec("outside", c.Members, params, c.Weight),
					partialSpec("inside", append(append([]string(nil), c.Members...), c.Controls...), params, c.Weight))
			}
			resp := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: specs,
			})
			for k, res := range resp.Matrices {
				if !reflect.DeepEqual(res.Primary.RowKeys, c.Members) {
					t.Fatalf("%s axis = %v, want the non-control members %v", specs[k].Name, res.Primary.RowKeys, c.Members)
				}
				if findWarning(res, errors.PULSE_MATRIX_NOT_PSD) != nil {
					t.Errorf("%s: the oracle input is PSD, yet the guard repaired it", specs[k].Name)
				}
				for i := range c.Partial {
					for j := range c.Partial[i] {
						g := res.Primary.Values[i][j]
						if c.Partial[i][j] == nil {
							if !math.IsNaN(g) {
								t.Errorf("%s [%d][%d] = %v, R gives NA", specs[k].Name, i, j, g)
							}
							continue
						}
						if w := *c.Partial[i][j]; !(math.Abs(g-w) <= partialOracleTol*(1+math.Abs(w))) {
							t.Errorf("%s [%d][%d] = %.17g, R = %.17g", specs[k].Name, i, j, g, w)
						}
					}
				}
			}
		})
	}
}

// TestMatrixPartialCorrelation_NotPSDGuard: the nonpsd_pairwise
// fixture's pairwise correlation is not PSD. MAT_PARTIAL_CORRELATION
// (a decomposition operator) refuses it FATALLY with the code itself —
// never PROCESSING_INTERNAL — naming the repair; under
// params.repair "nearest" it runs on the nearest correlation matrix
// (Matrix::nearPD, corr = TRUE) and warns with the Frobenius
// adjustment, and its primary is that matrix's partials. MAT_CORRELATION
// on the same input keeps the detection-only warning and refuses
// params.repair (decomposition operators only).
func TestMatrixPartialCorrelation_NotPSDGuard(t *testing.T) {
	var golden struct {
		Cases []struct {
			Do2Eigen  bool        `json:"do2eigen"`
			Fields    []string    `json:"fields"`
			Nearest   [][]float64 `json:"nearest"`
			Frobenius float64     `json:"frobenius_adjustment"`
		} `json:"cases"`
	}
	dir := loadMV(t, "mv_near_pd.json", &golden)
	cfg := fixtureFS(t, dir)
	var nearest [][]float64
	var frob float64
	var fields []string
	for _, c := range golden.Cases {
		if c.Do2Eigen {
			nearest, frob, fields = c.Nearest, c.Frobenius, c.Fields
		}
	}
	cohort := &types.Cohort{Filename: filepath.Join(dir, "nonpsd_pairwise.pulse")}

	_, err := New(cfg).Process(context.Background(), &types.Request{Cohort: cohort,
		Matrices: []types.MatrixSpec{partialSpec("pc", fields, map[string]any{"missing": "pairwise"}, "")}})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_NOT_PSD {
		t.Fatalf("unrepaired error = %v, want the fatal PULSE_MATRIX_NOT_PSD", err)
	}
	if ce.Details["matrix"] != "pc" || ce.Details["member"] != "z" {
		t.Errorf("refusal details = %v", ce.Details)
	}
	if !strings.Contains(ce.Message, `repair "nearest"`) {
		t.Errorf("refusal message %q does not name the repair", ce.Message)
	}
	md, _ := errors.MetadataFor(errors.PULSE_MATRIX_NOT_PSD)
	if len(md.Fixups) == 0 || !strings.Contains(md.Fixups[0].Hint, `repair: "nearest"`) {
		t.Errorf("PULSE_MATRIX_NOT_PSD fixups %v do not lead with repair: \"nearest\"", md.Fixups)
	}

	resp := processMatrices(t, cfg, &types.Request{Cohort: cohort, Matrices: []types.MatrixSpec{
		partialSpec("pc", fields, map[string]any{"missing": "pairwise", "repair": "nearest"}, ""),
		{Name: "r", Type: types.MAT_CORRELATION, Fields: fields, Params: json.RawMessage(`{"missing": "pairwise"}`)},
	}})
	pc, r := resp.Matrices[0], resp.Matrices[1]
	w := findWarning(pc, errors.PULSE_MATRIX_NOT_PSD)
	if w == nil {
		t.Fatalf("repaired run carries no PULSE_MATRIX_NOT_PSD warning (%v)", matWarningCodes(pc))
	}
	if adj, _ := w.Details["frobenius_adjustment"].(float64); math.Abs(adj-frob) > 1e-9 {
		t.Errorf("frobenius_adjustment %v, nearPD %v", adj, frob)
	}
	if w.Details["repair"] != "nearest" || w.Details["converged"] != true {
		t.Errorf("repair warning details = %v", w.Details)
	}
	want := gonumPartials(t, nearest)
	for i := range want {
		for j := range want[i] {
			// The repaired matrix sits 1.5e-8 from singular, so its
			// partials amplify the 1e-9 repair agreement ~1e4-fold.
			if d := math.Abs(pc.Primary.Values[i][j] - want[i][j]); !(d <= 1e-4) {
				t.Errorf("repaired partial [%d][%d] = %.17g, nearPD-derived %.17g", i, j, pc.Primary.Values[i][j], want[i][j])
			}
		}
	}
	if findWarning(r, errors.PULSE_MATRIX_NOT_PSD) == nil {
		t.Errorf("MAT_CORRELATION lost its detection-only warning (%v)", matWarningCodes(r))
	}

	_, err = New(cfg).Process(context.Background(), &types.Request{Cohort: cohort, Matrices: []types.MatrixSpec{
		{Name: "r", Type: types.MAT_CORRELATION, Fields: fields, Params: json.RawMessage(`{"missing": "pairwise", "repair": "nearest"}`)}}})
	if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" {
		t.Errorf("MAT_CORRELATION params.repair error = %v, want bad_params", err)
	}
}

// gonumPartials is the control-"all" partial correlation of rows via
// gonum's general inverse (independent of the engine's InverseSPD).
func gonumPartials(t *testing.T, rows [][]float64) [][]float64 {
	t.Helper()
	n := len(rows)
	d := mat.NewDense(n, n, nil)
	for i := range rows {
		for j := range rows[i] {
			d.Set(i, j, rows[i][j])
		}
	}
	var inv mat.Dense
	if err := inv.Inverse(d); err != nil {
		t.Fatal(err)
	}
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, n)
		for j := range out[i] {
			if i == j {
				out[i][j] = 1
				continue
			}
			out[i][j] = -inv.At(i, j) / math.Sqrt(inv.At(i, i)*inv.At(j, j))
		}
	}
	return out
}

// TestMatrixPartialCorrelation_Singular: a member that is a linear
// combination of the others (x3 = x1 + x2) has no precision matrix —
// PULSE_MATRIX_SINGULAR with the E1-S1 diagnostics (rank,
// condition_number, dependent_fields) and the matrix name. A control
// list that leaves the dependency among the controls and outputs
// refuses the same way.
func TestMatrixPartialCorrelation_Singular(t *testing.T) {
	rows := make([][5]float64, 30)
	for i := range rows {
		a, b := float64(i%7)+0.25*float64(i), float64((i*i)%11)
		rows[i] = [5]float64{a, b, a + b, 1, 1}
	}
	cfg := writeMatrixCohort(t, "s.pulse", rows)
	for _, params := range []map[string]any{{}, {"control": []string{"x3"}}} {
		_, err := New(cfg).Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "s.pulse"},
			Matrices: []types.MatrixSpec{partialSpec("m", x123, params, "")}})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_SINGULAR {
			t.Fatalf("params %v: error = %v, want PULSE_MATRIX_SINGULAR", params, err)
		}
		if ce.Details["matrix"] != "m" || ce.Details["rank"] != 2 {
			t.Errorf("params %v: details = %v, want matrix m, rank 2", params, ce.Details)
		}
		if !reflect.DeepEqual(ce.Details["dependent_fields"], []string{"x1", "x2", "x3"}) {
			t.Errorf("params %v: dependent_fields = %v", params, ce.Details["dependent_fields"])
		}
	}
}

// TestMatrixPartialCorrelation_ControlJoinsFold: a control outside the
// vector joins the co-moment and its missing-data mode — listwise, a
// row whose CONTROL is null is dropped like a row with a null member
// (components n), and the result equals the same matrix with the
// control inside the vector (bit for bit: the folded columns are the
// same set in the same order). The output axis never carries a control,
// and an "all" request differs (it controls for every member).
func TestMatrixPartialCorrelation_ControlJoinsFold(t *testing.T) {
	rows := matrixRows(400)
	nulls := 0
	for i := range rows {
		if i%5 == 2 {
			rows[i][3] = math.NaN() // the control (f) is null
		}
		if math.IsNaN(rows[i][0]) || math.IsNaN(rows[i][1]) || math.IsNaN(rows[i][3]) {
			nulls++
		}
	}
	cfg := writeMatrixCohort(t, "c.pulse", rows)
	resp := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Matrices: []types.MatrixSpec{
		partialSpec("outside", []string{"x1", "x2", "x3"}, map[string]any{"control": []string{"f"}}, ""),
		partialSpec("inside", []string{"x1", "x2", "x3", "f"}, map[string]any{"control": []string{"f"}}, ""),
		partialSpec("all", []string{"x1", "x2", "x3", "f"}, map[string]any{}, ""),
	}})
	outside, inside, all := resp.Matrices[0], resp.Matrices[1], resp.Matrices[2]
	if want := []string{"x1", "x2", "x3"}; !reflect.DeepEqual(outside.Primary.RowKeys, want) || !reflect.DeepEqual(inside.Primary.RowKeys, want) {
		t.Fatalf("axes %v / %v, want %v", outside.Primary.RowKeys, inside.Primary.RowKeys, want)
	}
	if a, b := mustJSON(t, outside.Primary.Values), mustJSON(t, inside.Primary.Values); a != b {
		t.Errorf("outside control %s differs from inside control %s", a, b)
	}
	if mustJSON(t, all.Primary.Values) == mustJSON(t, inside.Primary.Values) {
		t.Error("control-all equals the control-list result")
	}
	comps := resp.Components.Matrices
	if want := len(rows) - nulls; comps[0].N != want || comps[1].N != want {
		t.Errorf("components n = %d / %d, want %d (rows with every member and the control present)", comps[0].N, comps[1].N, want)
	}
	if comps[0].NListwiseDropped != nulls {
		t.Errorf("n_listwise_dropped = %d, want %d", comps[0].NListwiseDropped, nulls)
	}
}

// TestMatrixPartialCorrelation_Refusals: params.control and
// params.repair are checked at resolution (SERVICE_VALIDATION
// bad_params, or the vector rules for an outside control).
func TestMatrixPartialCorrelation_Refusals(t *testing.T) {
	cfg := writeMatrixCohort(t, "r.pulse", matrixRows(50))
	for _, c := range []struct {
		params string
		code   errors.Code
	}{
		{`{"control": []}`, errors.SERVICE_VALIDATION},
		{`{"control": "some"}`, errors.SERVICE_VALIDATION},
		{`{"control": ["x1", "x1"]}`, errors.SERVICE_VALIDATION},
		{`{"control": ["x*"]}`, errors.SERVICE_VALIDATION},
		{`{"control": ["x1", "x2", "x3"]}`, errors.SERVICE_VALIDATION},
		{`{"control": ["nope"]}`, errors.SERVICE_VALIDATION},
		{`{"repair": "clip"}`, errors.SERVICE_VALIDATION},
		{`{"summary": {"top_pairs": 1}}`, errors.SERVICE_VALIDATION},
	} {
		_, err := New(cfg).Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "r.pulse"},
			Matrices: []types.MatrixSpec{{Name: "m", Type: types.MAT_PARTIAL_CORRELATION, Fields: x123, Params: json.RawMessage(c.params)}}})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != c.code {
			t.Errorf("params %s: error = %v, want %s", c.params, err, c.code)
		}
	}
}
