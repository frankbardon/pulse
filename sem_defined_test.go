package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// sem_defined_test.go is MAT_SEM's lavaan defined parameters (`name :=
// expr` model lines) end to end through the facade: the := rows of the
// brand-survey mediation (with and without a mean structure — the
// covariance-only fit then follows lavaan's conventions) and of a joint
// two-wave fit (a path times a latent mean change) against lavaan's
// parameterEstimates (internal/sem/testdata brand_def_*_lavaan.json,
// brand_mg_defined_lavaan.json); the := rows joining the spec's own
// multiplicity family; the config refusals, identical in predict; a
// definition undefined at the estimate.

// semDefLavaan is a single-group fit_lavaan.R reference.
type semDefLavaan struct {
	Fit    map[string]*float64 `json:"fit"`
	Params []struct {
		LHS, Op, RHS, Label string
		Free                bool
		Est, SE, Z, P       *float64
		CILower             *float64 `json:"ci_lower"`
		CIUpper             *float64 `json:"ci_upper"`
		StdAll              *float64 `json:"std_all"`
	} `json:"params"`
}

func semRow(t *testing.T, res types.MatrixResult, key string) (int, []float64) {
	t.Helper()
	est := res.Auxiliary["estimates"]
	for i, k := range est.RowKeys {
		if k == key {
			return i, est.Values[i]
		}
	}
	t.Fatalf("no estimates row %q in %v", key, est.RowKeys)
	return 0, nil
}

// TestSEM_DefinedProcessMatchesLavaan: the := rows close the estimates
// table, each matching lavaan's estimate, delta-method SE, z, p, Wald
// interval and std.all; vectors.parameter names them and vectors.free
// is false on them. Without a mean structure the := line switches the
// covariance-only description to lavaan's conventions: the ci columns
// appear and the fit indices are lavaan's.
func TestSEM_DefinedProcessMatchesLavaan(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, c := range []struct {
		ref    string
		params map[string]any
	}{
		{"brand_def_ms", map[string]any{"group": nil, "meanstructure": true}},
		{"brand_def_cov", map[string]any{"group": nil}},
	} {
		t.Run(c.ref, func(t *testing.T) {
			var ref semDefLavaan
			raw, err := os.ReadFile(semGroupTestdata + c.ref + "_lavaan.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &ref); err != nil {
				t.Fatal(err)
			}
			res := semGroupRun(t, p, semGroupRequest(t, "brand_def", c.params))
			if res.Error != nil || res.Scalars["converged"] != 1 || len(res.Warnings) != 0 {
				t.Fatalf("error %+v converged %v warnings %+v", res.Error, res.Scalars["converged"], res.Warnings)
			}
			est := res.Auxiliary["estimates"]
			if !reflect.DeepEqual(est.ColumnKeys, []string{"estimate", "std_estimate", "std_err", "z_value", "p_value", "ci_lower", "ci_upper"}) {
				t.Fatalf("columns %v: a := line runs under lavaan conventions", est.ColumnKeys)
			}
			names, _ := res.Vectors["parameter"].([]string)
			free, _ := res.Vectors["free"].([]bool)
			defs := 0
			for _, r := range ref.Params {
				if r.Op != ":=" {
					continue
				}
				defs++
				key := r.LHS + " := " + r.RHS
				i, row := semRow(t, res, key)
				if i < len(est.RowKeys)-5 {
					t.Errorf("row %q at %d: the := rows close the table", key, i)
				}
				if names[i] != r.LHS || free[i] {
					t.Errorf("%s: vectors parameter %q free %v, want %q false", key, names[i], free[i], r.LHS)
				}
				for j, w := range []*float64{r.Est, r.StdAll, r.SE, r.Z, nil, r.CILower, r.CIUpper} {
					if w != nil && !(mgRel(row[j], *w) <= 1e-4) {
						t.Errorf("%s %s = %.10g, lavaan %.10g", key, est.ColumnKeys[j], row[j], *w)
					}
				}
				if !(math.Abs(row[4]-*r.P) <= 1e-4) {
					t.Errorf("%s p = %g, lavaan %g", key, row[4], *r.P)
				}
			}
			if defs != 5 {
				t.Fatalf("the fixture has %d := rows, want 5", defs)
			}
			for k, lk := range mgScalarKeys {
				if w := ref.Fit[lk]; w == nil || !(mgRel(res.Scalars[k], *w) <= 1e-3) {
					t.Errorf("scalar %s = %.10g, lavaan %s %v", k, res.Scalars[k], lk, w)
				}
			}
		})
	}
}

// TestSEM_DefinedMultiGroupProcessMatchesLavaan: on a joint fit the :=
// rows come after every level's rows, unprefixed, with vectors.group
// null and vectors.label the name — the cross-group contribution
// (a shared path times the wave change in a latent mean) matching
// lavaan with the rest of the fit.
func TestSEM_DefinedMultiGroupProcessMatchesLavaan(t *testing.T) {
	t.Parallel()

	ref := loadMG(t, "brand_mg_defined")
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	res := semGroupRun(t, p, semGroupRequest(t, "brand_mgdef", map[string]any{"group_equal": ref.Options.GroupEqual}))
	checkMGAgainstLavaan(t, res, ref, 0)
	groups, _ := res.Vectors["group"].([]any)
	labels, _ := res.Vectors["label"].([]string)
	est := res.Auxiliary["estimates"]
	n := 0
	for i, k := range est.RowKeys {
		if !strings.Contains(k, " := ") {
			if n > 0 {
				t.Fatalf("row %q follows a := row", k)
			}
			continue
		}
		n++
		if strings.HasPrefix(k, "[") || groups[i] != nil || labels[i] != strings.Fields(k)[0] {
			t.Errorf("row %q: group %v label %q, want unprefixed, null and the name", k, groups[i], labels[i])
		}
	}
	if n != 4 {
		t.Fatalf("%d := rows, want 4", n)
	}
	raw, _ := json.Marshal(res.Vectors["group"])
	if !strings.HasSuffix(string(raw), "null,null,null,null]") {
		t.Errorf("vectors.group on the wire %s, want null on the := rows", raw)
	}
}

func loadMG(t *testing.T, name string) mgLavaan {
	t.Helper()
	var ref mgLavaan
	raw, err := os.ReadFile(semGroupTestdata + name + "_lavaan.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

// TestSEM_DefinedMultiplicity: a MAT_SEM spec's own multiplicity block
// corrects every estimates row carrying a p — the free rows and the :=
// rows — in one family: auxiliary.p_adjusted is the method's
// adjustment of exactly those p-values (one column, null on a fixed
// row), significant_adjusted flags it against alpha, the echo counts
// them, and the raw estimates are byte-identical to the run without the
// block. An inherited request block never reaches the spec. Predict
// counts the same family.
func TestSEM_DefinedMultiplicity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, grouped := range []bool{false, true} {
		model, params := "brand_def", map[string]any{"group": nil, "meanstructure": true}
		if grouped {
			model, params = "brand_mgdef", map[string]any{"group_equal": []string{"loadings", "intercepts"}}
		}
		base := semGroupRun(t, p, semGroupRequest(t, model, params))
		for _, method := range []types.MultiplicityMethod{types.MultiplicityMethodHolm, types.MultiplicityMethodBH} {
			req := semGroupRequest(t, model, params)
			req.Matrices[0].Multiplicity = &types.Multiplicity{Method: method}
			res := semGroupRun(t, p, req)
			a, _ := json.Marshal(res.Auxiliary["estimates"])
			b, _ := json.Marshal(base.Auxiliary["estimates"])
			if string(a) != string(b) {
				t.Fatal("the raw estimates moved under a multiplicity block")
			}
			est := res.Auxiliary["estimates"]
			var family []float64
			var rows []int
			defs := 0
			for i, row := range est.Values {
				if !math.IsNaN(row[4]) {
					family, rows = append(family, row[4]), append(rows, i)
					if strings.Contains(est.RowKeys[i], " := ") {
						defs++
					}
				}
			}
			if defs == 0 {
				t.Fatal("no := row joined the family")
			}
			want, err := multiplicity.Adjust(multiplicity.Method(method), family)
			if err != nil {
				t.Fatal(err)
			}
			adj, sig := res.Auxiliary["p_adjusted"], res.Auxiliary["significant_adjusted"]
			if adj == nil || sig == nil || !reflect.DeepEqual(adj.RowKeys, est.RowKeys) || !reflect.DeepEqual(adj.ColumnKeys, []string{"p_adjusted"}) ||
				!reflect.DeepEqual(sig.ColumnKeys, []string{"significant_adjusted"}) || adj.Kind != types.MatrixKindRectangular {
				t.Fatalf("twins %+v / %+v", adj, sig)
			}
			in := map[int]float64{}
			for k, i := range rows {
				in[i] = want[k]
			}
			for i := range est.RowKeys {
				w, ok := in[i]
				got, flag := adj.Values[i][0], sig.Values[i][0]
				switch {
				case !ok && (!math.IsNaN(got) || !math.IsNaN(flag)):
					t.Errorf("%s: a row without a p carries %g / %g", est.RowKeys[i], got, flag)
				case ok && got != w:
					t.Errorf("%s p_adjusted = %g, want %g", est.RowKeys[i], got, w)
				case ok && flag != map[bool]float64{true: 1, false: 0}[w < 0.05]:
					t.Errorf("%s significant_adjusted = %g at p_adjusted %g", est.RowKeys[i], flag, w)
				}
			}
			echo := &types.AppliedMultiplicity{Method: method, Family: types.MultiplicityFamilyMatrix, Alpha: 0.05, M: len(family)}
			if !reflect.DeepEqual(res.Multiplicity, echo) {
				t.Errorf("echo %+v, want %+v", res.Multiplicity, echo)
			}
			pv := predictEnvelope(t, p, fsys, "brand.pulse", req).Data.(*descriptor.PredictResult).PValues
			if pv == nil || pv.Total != len(family) || pv.Uncorrected != 0 {
				t.Errorf("predict p_values %+v, want total %d all corrected", pv, len(family))
			}
		}
		// An inherited (request-level) block never reaches MAT_SEM.
		req := semGroupRequest(t, model, params)
		req.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res := resp.Matrices[0]; res.Auxiliary["p_adjusted"] != nil || res.Multiplicity != nil {
			t.Error("an inherited multiplicity block reached MAT_SEM")
		}
	}
}

// TestSEM_DefinedRefusals: a := line reading no label, reading a later
// (or its own) line, or reusing a name, and a lavaan constraint line,
// are config refusals — SERVICE_VALIDATION with their reason — that
// predict reports identically.
func TestSEM_DefinedRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	base := "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3\nrecommend ~ bq*quality + bv*value + bs*service\n"
	for line, want := range map[string][2]string{
		"d := bq * zz":             {"defined_invalid", "unknown_label"},
		"d := e + bq\ne := bv":     {"defined_invalid", "forward_reference"},
		"d := d * 2":               {"defined_invalid", "forward_reference"},
		"bq := bv":                 {"defined_invalid", "duplicate"},
		"d := bq +":                {"defined_invalid", "syntax"},
		"bq == bv":                 {"constraint_unsupported", "=="},
		"bs > 0":                   {"constraint_unsupported", ">"},
		"d := bq\ndd := d < 1 * 2": {"defined_invalid", "syntax"},
	} {
		t.Run(line, func(t *testing.T) {
			req := func() *types.Request {
				return semGroupRequest(t, "brand_def", map[string]any{"group": nil, "model": base + line})
			}
			_, rerr := p.Process(ctx, req())
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != want[0] || ce.Details["param"] != "model" {
				t.Fatalf("runtime error %v (%v), want SERVICE_VALIDATION %s", rerr, semDetailsOf(ce), want[0])
			}
			key := "problem"
			if want[0] == "constraint_unsupported" {
				key = "constraint"
			}
			if ce.Details[key] != want[1] {
				t.Errorf("details.%s = %v, want %s", key, ce.Details[key], want[1])
			}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req())
			sameEntry(t, env, rerr)
		})
	}
}

// TestSEM_DefinedNonFinite: a := line undefined at the estimate (a
// division by zero) nulls its row — and the row reading it — with
// PULSE_MATRIX_DEFINED_NONFINITE, the fit and the other rows standing.
func TestSEM_DefinedNonFinite(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	model := "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3\nrecommend ~ bq*quality + bv*value + bs*service\n" +
		"r := bq/(bv - bv)\nq := r + 1\nok := bq*bv"
	res := semGroupRun(t, p, semGroupRequest(t, "brand_def", map[string]any{"group": nil, "model": model}))
	var w *types.ResponseWarning
	for i := range res.Warnings {
		if res.Warnings[i].Code == string(errors.PULSE_MATRIX_DEFINED_NONFINITE) {
			w = res.Warnings[i]
		}
	}
	if w == nil || !reflect.DeepEqual(w.Details["defined"], []string{"r", "q"}) {
		t.Fatalf("warnings %+v, want PULSE_MATRIX_DEFINED_NONFINITE over r, q", res.Warnings)
	}
	for _, key := range []string{"r := bq/(bv-bv)", "q := r+1"} {
		_, row := semRow(t, res, key)
		for j, v := range row {
			if !math.IsNaN(v) {
				t.Errorf("%s column %d = %g, want null", key, j, v)
			}
		}
	}
	if _, row := semRow(t, res, "ok := bq*bv"); math.IsNaN(row[0]) || math.IsNaN(row[2]) || res.Scalars["converged"] != 1 {
		t.Errorf("the defined row beside the undefined ones: %v", row)
	}
	raw, _ := json.Marshal(res)
	if !strings.Contains(string(raw), `"r := bq/(bv-bv)"`) {
		t.Error("the undefined row is missing from the wire")
	}
}
