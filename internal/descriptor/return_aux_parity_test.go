package descriptor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// return_aux_parity_test.go (U28 E4-G1): the `standard` preset keeps the
// matrix inference auxiliaries a spec asked for — per spec — and predict
// sizes them exactly where the runtime emits them. An external test
// package: it runs the root facade (which imports this package).

// auxParityPulse imports a 60-row cohort over x1..x3 (x3 null on every
// seventh row, so pairwise differs from listwise) and returns the
// instance and the .pulse file's bytes.
func auxParityPulse(t *testing.T) (*pulse.Pulse, []byte) {
	t.Helper()
	var csv bytes.Buffer
	csv.WriteString("x1,x2,x3\n")
	for i := range 60 {
		x1 := float64(i%11) + 0.5*float64(i%3)
		x2 := float64((i*7)%13) - 0.25*float64(i%5)
		x3 := ""
		if i%7 != 0 {
			x3 = strconv.FormatFloat(float64((i*5)%17)+0.1*x1, 'f', 3, 64)
		}
		fmt.Fprintf(&csv, "%g,%g,%s\n", x1, x2, x3)
	}
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "m.csv", csv.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "m.csv"}); err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(afs, "imports/m.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

func auxSpec(name string, typ types.MatrixType, params string, mult *types.Multiplicity) types.MatrixSpec {
	s := types.MatrixSpec{Name: name, Type: typ, Fields: []string{"x1", "x2", "x3"}, Multiplicity: mult}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// auxParitySpecs mixes, in ONE request, specs with and without
// inference and multiplicity: the keep rule is per spec.
func auxParitySpecs() []types.MatrixSpec {
	holm := &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	return []types.MatrixSpec{
		auxSpec("inf_pairwise", types.MAT_CORRELATION, `{"inference": true, "missing": "pairwise"}`, nil),
		auxSpec("plain_pairwise", types.MAT_CORRELATION, `{"missing": "pairwise"}`, nil),
		auxSpec("pca", types.MAT_PCA, `{"components": 2}`, nil),
		auxSpec("mult_pairwise", types.MAT_CORRELATION, `{"missing": "pairwise"}`, holm),
		auxSpec("partial_inf", types.MAT_PARTIAL_CORRELATION, `{"inference": true}`, nil),
		auxSpec("spearman_inf", types.MAT_CORRELATION, `{"inference": true, "method": "spearman"}`, nil),
	}
}

// wireMatrices decodes resp's wire form's matrices.
func wireMatrices(t *testing.T, resp *types.Response) ([]map[string]json.RawMessage, []byte) {
	t.Helper()
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Matrices []map[string]json.RawMessage `json:"matrices"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Matrices, raw
}

func auxKeys(t *testing.T, m map[string]json.RawMessage) []string {
	t.Helper()
	keys := []string{}
	if raw, ok := m["auxiliary"]; ok {
		var aux map[string]json.RawMessage
		if err := json.Unmarshal(raw, &aux); err != nil {
			t.Fatal(err)
		}
		for k := range aux {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// TestReturnAuxiliary_PredictRuntimeParity: under every preset and the
// exclude / include spellings around the new rule, the auxiliary keys
// predict sizes per spec equal the keys the runtime writes on the wire —
// and under `standard` those are exactly the inference keys the spec
// asked for (n beside p under pairwise; the multiplicity twins and the
// echo on a claimed spec), nothing on a spec without inference.
func TestReturnAuxiliary_PredictRuntimeParity(t *testing.T) {
	p, data := auxParityPulse(t)
	std := types.ReturnPresetStandard
	inference := []string{"ci_lower", "ci_upper", "p"}
	pairwise := []string{"ci_lower", "ci_upper", "n", "p"}
	member := []string{"ci_lower", "ci_upper", "n", "p", "p_adjusted", "significant_adjusted"}
	for _, c := range []struct {
		name string
		ret  *types.Return
		// want is the standard-rule expectation per spec (nil: only
		// the parity is asserted).
		want [][]string
		// echo is whether mult_pairwise carries its multiplicity echo.
		echo bool
	}{
		{name: "absent", echo: true},
		{name: "full", ret: &types.Return{Preset: types.ReturnPresetFull}, echo: true},
		{name: "standard", ret: &types.Return{Preset: std}, echo: true,
			want: [][]string{pairwise, {}, {}, member, inference, inference}},
		{name: "minimal", ret: &types.Return{Preset: types.ReturnPresetMinimal},
			want: [][]string{{}, {}, {}, {}, {}, {}}},
		{name: "standard_exclude_p", ret: &types.Return{Preset: std, Exclude: []string{"matrices[*].auxiliary.p"}}, echo: true,
			want: [][]string{{"ci_lower", "ci_upper"}, {}, {}, {"ci_lower", "ci_upper", "p_adjusted", "significant_adjusted"}, {"ci_lower", "ci_upper"}, {"ci_lower", "ci_upper"}}},
		{name: "standard_exclude_n", ret: &types.Return{Preset: std, Exclude: []string{"matrices[*].auxiliary.n"}}, echo: true,
			want: [][]string{inference, {}, {}, {"ci_lower", "ci_upper", "p", "p_adjusted", "significant_adjusted"}, inference, inference}},
		{name: "standard_include_n", ret: &types.Return{Preset: std, Include: []string{"matrices[*].auxiliary.n"}}, echo: true,
			want: [][]string{pairwise, {"n"}, {}, member, inference, inference}},
		{name: "standard_exclude_auxiliary", ret: &types.Return{Preset: std, Exclude: []string{"matrices[*].auxiliary"}}, echo: true,
			want: [][]string{{}, {}, {}, {}, {}, {}}},
		{name: "include_p_only", ret: &types.Return{Include: []string{"matrices[*].auxiliary.p"}},
			want: [][]string{{"p"}, {}, {}, {"p"}, {"p"}, {"p"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Cohort: &types.Cohort{Filename: "imports/m.pulse"}, Matrices: auxParitySpecs(), Return: c.ret}
			predicted, err := descx.PredictedMatrixAuxiliaryKeys(data, req)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			wire, _ := wireMatrices(t, resp)
			if len(wire) != len(req.Matrices) || len(predicted) != len(req.Matrices) {
				t.Fatalf("%d results, %d predicted, want %d", len(wire), len(predicted), len(req.Matrices))
			}
			for i, m := range wire {
				got := auxKeys(t, m)
				if !slices.Equal(got, predicted[i]) {
					t.Errorf("%s: runtime auxiliary %v, predict %v", req.Matrices[i].Name, got, predicted[i])
				}
				if c.want != nil && !slices.Equal(got, c.want[i]) {
					t.Errorf("%s: auxiliary %v, want %v", req.Matrices[i].Name, got, c.want[i])
				}
			}
			if _, has := wire[3]["multiplicity"]; has != c.echo {
				t.Errorf("mult_pairwise multiplicity echo present = %v, want %v", has, c.echo)
			}
		})
	}
}

// legacyStandard is the `standard` selection before E4-G1, spelled as
// an include list: a response with no inference or multiplicity must
// put exactly these bytes on the wire under the new `standard`.
var legacyStandard = []string{
	"data", "warnings", "metadata", "crosstab",
	"matrices[*].name", "matrices[*].type", "matrices[*].group_key", "matrices[*].group_header",
	"matrices[*].primary", "matrices[*].vectors", "matrices[*].scalars",
	"tests", "post_tests", "regressions", "overlays",
}

// TestReturnAuxiliary_NoInferenceByteIdentical: a request with no
// inference and no multiplicity writes the same bytes under the new
// `standard` as under the pre-E4-G1 selection (only the `returned`
// marker's digest names the new selection), and `full` / `minimal` are
// untouched.
func TestReturnAuxiliary_NoInferenceByteIdentical(t *testing.T) {
	p, _ := auxParityPulse(t)
	specs := []types.MatrixSpec{
		auxSpec("plain_pairwise", types.MAT_CORRELATION, `{"missing": "pairwise", "summary": {"top_pairs": 1}}`, nil),
		auxSpec("pca", types.MAT_PCA, `{"components": 2, "rotation": "varimax"}`, nil),
		auxSpec("cov", types.MAT_COVARIANCE, `{"missing": "pairwise"}`, nil),
	}
	run := func(ret *types.Return) []byte {
		t.Helper()
		resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "imports/m.pulse"}, Matrices: specs, Return: ret})
		if err != nil {
			t.Fatal(err)
		}
		resp.Returned = nil
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	now := run(&types.Return{Preset: types.ReturnPresetStandard})
	legacy := run(&types.Return{Include: legacyStandard})
	if !bytes.Equal(now, legacy) {
		t.Errorf("standard without inference moved:\n now   %s\n legacy %s", now, legacy)
	}
	if bytes.Contains(now, []byte(`"auxiliary"`)) {
		t.Errorf("standard without inference emitted an auxiliary: %s", now)
	}
	full := run(&types.Return{Preset: types.ReturnPresetFull})
	if absent := run(nil); !bytes.Equal(full, absent) {
		t.Errorf("full is not the identity")
	}
}
