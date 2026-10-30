package integration_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// sem_ordinal_group_test.go is MAT_SEM's ordinal estimators across
// groups (params.group with estimator "dwls" / "wlsmv") and under a
// bootstrap (params.resample) end to end through the facade: the
// brand-survey cohort split by wave against lavaan's multi-group WLSMV
// fits (internal/sem/testdata brand_ordmg_*_lavaan.json,
// fit_lavaan_wlsmv.R), predict ≡ runtime on the estimates rows, their
// p-values and every new refusal, and a seeded ordinal bootstrap's bits.

// ordinalGroupRef is a multi-group fit_lavaan_wlsmv.R reference.
type ordinalGroupRef struct {
	Lavaan  string `json:"lavaan"`
	Options struct {
		Estimator  string   `json:"estimator"`
		Ordered    []string `json:"ordered"`
		GroupEqual []string `json:"group_equal"`
	} `json:"options"`
	N      float64             `json:"n"`
	Labels []string            `json:"group_labels"`
	Fit    map[string]*float64 `json:"fit"`
	Params []struct {
		LHS, Op, RHS, Group string
		Free                bool
		Est, SE, Z, P       *float64
		CILower             *float64 `json:"ci_lower"`
		CIUpper             *float64 `json:"ci_upper"`
		StdAll              *float64 `json:"std_all"`
	} `json:"params"`
	Groups []struct {
		Label string  `json:"label"`
		N     float64 `json:"n"`
	} `json:"groups"`
}

// ordinalGroupRequest is a grouped ordinal MAT_SEM over the model's
// members (fields), with params over the model's defaults.
func ordinalGroupRequest(t *testing.T, model string, fields []string, params map[string]any) *types.Request {
	t.Helper()
	desc, err := os.ReadFile(semGroupTestdata + model + "_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{"model": string(desc), "group": "wave", "estimator": "wlsmv", "ordered": brandOrdered}
	for k, v := range params {
		if v == nil {
			delete(all, k)
			continue
		}
		all[k] = v
	}
	raw, _ := json.Marshal(all)
	return &types.Request{
		Cohort:   &types.Cohort{Filename: "brand.pulse"},
		Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: fields, Params: raw}},
	}
}

var ordinalScaledKeys = map[string]string{
	"chi2_scaled": "chisq.scaled", "chi2_scaled_p_value": "pvalue.scaled", "scaling_factor": "chisq.scaling.factor",
	"chi2_baseline_scaled": "baseline.chisq.scaled", "cfi_scaled": "cfi.scaled", "tli_scaled": "tli.scaled",
	"rmsea_scaled": "rmsea.scaled",
}

// TestSEM_OrdinalGroupsProcessMatchesLavaan: the configural fit, the
// threshold + loading invariant one and the unequal-wave structural fit
// under thresholds, loadings and intercepts match lavaan's WLSMV row
// for row — every "[level]" estimate (thresholds, scale factors, delta
// residual variances, freed intercepts and latent means), the joint and
// per-level scalars, the scaled test — and predict sizes the estimates
// rows the run emits.
func TestSEM_OrdinalGroupsProcessMatchesLavaan(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ ref, csv, model string }{
		{"brand_ordmg_configural", "brand", "brand_ord"},
		{"brand_ordmg_thload", "brand", "brand_ord"},
		{"brand_ordmg_scalar", "brand_uneq", "brand_ms"},
	} {
		t.Run(c.ref, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(semGroupTestdata + c.ref + "_lavaan.json")
			if err != nil {
				t.Fatal(err)
			}
			var ref ordinalGroupRef
			if err := json.Unmarshal(raw, &ref); err != nil {
				t.Fatal(err)
			}
			fields := brandOrdered
			if c.model == "brand_ms" {
				fields = brandMembers
			}
			params := map[string]any{"estimator": strings.ToLower(ref.Options.Estimator)}
			if len(ref.Options.GroupEqual) > 0 {
				params["group_equal"] = ref.Options.GroupEqual
			}
			req := ordinalGroupRequest(t, c.model, fields, params)
			p, fsys := brandGroupCohort{csv: c.csv, wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
			res := semGroupRun(t, p, req)
			if res.Error != nil || res.Scalars["converged"] != 1 {
				t.Fatalf("error %+v, converged %v, warnings %+v", res.Error, res.Scalars["converged"], res.Warnings)
			}
			est := res.Auxiliary["estimates"]
			rows := map[string][]float64{}
			for i, k := range est.RowKeys {
				rows[k] = est.Values[i]
			}
			matched := map[string]int{}
			for _, r := range ref.Params {
				key := r.LHS + " " + r.Op + " " + r.RHS
				switch r.Op {
				case "=~":
					key = r.RHS + " ~ " + r.LHS
				case "~1":
					key = r.LHS + " ~1"
				}
				key = "[" + r.Group + "] " + key
				row, ok := rows[key]
				if !ok && r.Op == "~~" {
					row, ok = rows["["+r.Group+"] "+r.RHS+" ~~ "+r.LHS]
				}
				if !ok {
					t.Errorf("lavaan row %q missing", key)
					continue
				}
				matched[r.Op]++
				near := func(what string, g float64, w *float64) {
					if w == nil || !(mgRel(g, *w) <= 1e-4 || math.Abs(g-*w) <= 1e-6) {
						t.Errorf("%s %s = %.10g, lavaan %v", key, what, g, w)
					}
				}
				near("estimate", row[0], r.Est)
				near("std_estimate", row[1], r.StdAll)
				if !r.Free {
					continue
				}
				near("std_err", row[2], r.SE)
				near("z_value", row[3], r.Z)
				near("ci_lower", row[5], r.CILower)
				near("ci_upper", row[6], r.CIUpper)
				if r.P == nil || !(math.Abs(row[4]-*r.P) <= 1e-4) {
					t.Errorf("%s p = %g, lavaan %v", key, row[4], r.P)
				}
			}
			total := 0
			for _, n := range matched {
				total += n
			}
			if total != len(est.RowKeys) || matched["|"] != 2*4*len(brandOrdered) || matched["~*~"] != 2*len(brandOrdered) {
				t.Errorf("matched %v (%d), the result has %d rows", matched, total, len(est.RowKeys))
			}
			for k, lk := range mgScalarKeys {
				if w := ref.Fit[lk]; w == nil {
					if !math.IsNaN(res.Scalars[k]) {
						t.Errorf("scalar %s = %.10g, lavaan NA", k, res.Scalars[k])
					}
				} else if !(mgRel(res.Scalars[k], *w) <= 1e-3 || math.Abs(res.Scalars[k]-*w) <= 1e-5) {
					t.Errorf("scalar %s = %.10g, lavaan %s %v", k, res.Scalars[k], lk, *w)
				}
			}
			for k, lk := range ordinalScaledKeys {
				if w := ref.Fit[lk]; w == nil || !(mgRel(res.Scalars[k], *w) <= 1e-3 || math.Abs(res.Scalars[k]-*w) <= 1e-5) {
					t.Errorf("scalar %s = %.10g, lavaan %s %v", k, res.Scalars[k], lk, refVal(w))
				}
			}
			for _, g := range ref.Groups {
				if n := res.Scalars["n_group_"+g.Label]; n != g.N {
					t.Errorf("n_group_%s = %v, lavaan %v", g.Label, n, g.N)
				}
			}
			if res.Scalars["n"] != ref.N {
				t.Errorf("n = %v, lavaan %v", res.Scalars["n"], ref.N)
			}
			// Predict sizes every level's rows: the model's, four
			// thresholds per f64 rating (no schema bound), a scale row
			// per ordered member.
			env := predictEnvelope(t, p, fsys, "brand.pulse", req)
			if len(env.Errors) > 0 {
				t.Fatalf("predict refused: %+v", env.Errors[0])
			}
			pr := env.Data.(*descriptor.PredictResult)
			if pr.Streamable || pr.Matrices[0].Streamable {
				t.Fatal("a grouped ordinal spec predicts streamable; it buffers")
			}
			if got := predictEstimatesRows(t, fsys, req); got != len(est.RowKeys) {
				t.Errorf("predict sizes %d estimates rows, runtime %d", got, len(est.RowKeys))
			}
		})
	}
}

// refVal is a reference figure for a message (NaN for lavaan's NA).
func refVal(w *float64) float64 {
	if w == nil {
		return math.NaN()
	}
	return *w
}

// predictEstimatesRows is the estimates row count predict sizes for
// the request's MAT_SEM (vectors.ResolveMatrices over the cohort's
// schema): every known level's model rows, threshold-row estimates
// and scale rows, then the := rows — semEstimateRows' arithmetic.
func predictEstimatesRows(t *testing.T, fsys afero.Fs, req *types.Request) int {
	t.Helper()
	schema, err := schemaLoaderFor(fsys)("brand.pulse")
	if err != nil {
		t.Fatal(err)
	}
	plans, cerr := vectors.ResolveMatrices(req, schema, nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	sp := plans[0].SEM
	rows := len(sem.SpecifiedRows(sp.Model)) + sp.ThresholdRowsEstimate() + sp.ScaleRows()
	return rows*max(len(sp.GroupLevels), 1) + sp.Model.DefinedCount()
}

// TestSEM_OrdinalGroupsPValuesMatchPredict: a member multiplicity block
// over a grouped ordinal fit corrects every level's free rows,
// thresholds and free scale factors; predict's p_values count is the
// run's lower bound on f64 ratings and the run's own on packed_bool
// ordered members (two categories each: thresholds known, no free
// scale).
func TestSEM_OrdinalGroupsPValuesMatchPredict(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		fields  []string
		model   string
		ordered []string
		geq     []string
		basis   string
	}{
		{"f64 ratings", brandOrdered, "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3", brandOrdered,
			[]string{"thresholds", "loadings"}, descriptor.PValueBasisLowerBound},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
			params, _ := json.Marshal(map[string]any{"model": c.model, "estimator": "wlsmv", "ordered": c.ordered,
				"group": "wave", "group_equal": c.geq})
			req := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
				Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: c.fields, Params: params,
					Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm}}}}
			res := semGroupRun(t, p, req)
			if res.Error != nil {
				t.Fatalf("slot error %+v", res.Error)
			}
			m, th := semFamily(res)
			scales := 0
			for i, k := range res.Auxiliary["estimates"].RowKeys {
				if strings.Contains(k, " ~*~ ") && !math.IsNaN(res.Auxiliary["estimates"].Values[i][4]) {
					scales++
				}
			}
			if th != 2*4*len(c.ordered) || scales != len(c.ordered) {
				t.Fatalf("%d threshold and %d scale p-values: the check is vacuous", th, scales)
			}
			if res.Multiplicity == nil || res.Multiplicity.M != m {
				t.Fatalf("echo %+v, want m %d", res.Multiplicity, m)
			}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req)
			pv := env.Data.(*descriptor.PredictResult).PValues
			if pv == nil || pv.Basis != c.basis {
				t.Fatalf("predict p_values %+v, want basis %s", pv, c.basis)
			}
			// Lower bound: one threshold per member per level, no free
			// scale (an unknown bound might be binary).
			if want := m - th + 2*len(c.ordered) - scales; pv.Total != want {
				t.Errorf("predict total %d, want the runtime's %d less its data-dependent rows = %d", pv.Total, m, want)
			}
		})
	}
	t.Run("packed_bool members", func(t *testing.T) {
		p, fsys := binaryItemsCohort(t)
		params, _ := json.Marshal(map[string]any{"model": "f =~ b1 + b2 + b3 + b4", "estimator": "wlsmv",
			"ordered": []string{"b1", "b2", "b3", "b4"}, "group": "wave", "group_equal": []string{"thresholds", "loadings"}})
		req := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
			Vectors: []types.VectorSpec{{Name: "items", Fields: []string{"b1", "b2", "b3", "b4"}, Coerce: types.VectorCoerceBinary}},
			Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Vector: "items", Params: params,
				Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm}}}}
		res := semGroupRun(t, p, req)
		if res.Error != nil {
			t.Fatalf("slot error %+v", res.Error)
		}
		m, th := semFamily(res)
		if th != 2*4 {
			t.Fatalf("%d threshold p-values, want one per binary member per level", th)
		}
		if res.Multiplicity == nil || res.Multiplicity.M != m {
			t.Fatalf("echo %+v, want m %d", res.Multiplicity, m)
		}
		env := predictEnvelope(t, p, fsys, "brand.pulse", req)
		if len(env.Errors) > 0 {
			t.Fatalf("predict refused %+v", env.Errors[0])
		}
		pv := env.Data.(*descriptor.PredictResult).PValues
		if pv == nil || pv.Basis != descriptor.PValueBasisDictionary || pv.Total != m {
			t.Fatalf("predict p_values %+v, want the runtime's %d on basis dictionary", pv, m)
		}
		// 44: the count internal/descriptor's
		// TestMatrixAuxiliaryNodes_SEMOrdinalGrouped pins predict's sizing at.
		if got := len(res.Auxiliary["estimates"].RowKeys); got != 44 || predictEstimatesRows(t, fsys, req) != got {
			t.Errorf("predict sizes %d estimates rows, runtime %d", got, len(res.Auxiliary["estimates"].RowKeys))
		}
	})
	// u4 ratings: the schema bounds each member at 16 categories (> 2,
	// so non-binary): predict files 15 thresholds per member per level
	// (an upper bound, basis dictionary) and every non-reference level's
	// free scale factor, the same scale rows the runtime frees.
	t.Run("u4 members", func(t *testing.T) {
		p, fsys := itemsCohort(t, encoding.FieldTypeU4, func(v float64) any { return uint64(v) })
		params, _ := json.Marshal(map[string]any{"model": "f =~ b1 + b2 + b3 + b4", "estimator": "wlsmv",
			"ordered": []string{"b1", "b2", "b3", "b4"}, "group": "wave", "group_equal": []string{"thresholds", "loadings"}})
		req := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
			Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: []string{"b1", "b2", "b3", "b4"}, Params: params,
				Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm}}}}
		res := semGroupRun(t, p, req)
		if res.Error != nil {
			t.Fatalf("slot error %+v", res.Error)
		}
		m, th := semFamily(res)
		scales := 0
		for i, k := range res.Auxiliary["estimates"].RowKeys {
			if strings.Contains(k, " ~*~ ") && !math.IsNaN(res.Auxiliary["estimates"].Values[i][4]) {
				scales++
			}
		}
		if th == 0 || scales != 4 {
			t.Fatalf("%d threshold and %d scale p-values: the check is vacuous", th, scales)
		}
		env := predictEnvelope(t, p, fsys, "brand.pulse", req)
		if len(env.Errors) > 0 {
			t.Fatalf("predict refused %+v", env.Errors[0])
		}
		pv := env.Data.(*descriptor.PredictResult).PValues
		if pv == nil || pv.Basis != descriptor.PValueBasisDictionary {
			t.Fatalf("predict p_values %+v, want basis dictionary", pv)
		}
		if want := m - th + 15*4*2; pv.Total != want {
			t.Errorf("predict total %d, want the runtime's %d with 15 thresholds per member per level = %d", pv.Total, m, want)
		}
	})
}

// binaryItemsCohort is the brand survey's waves (categorical_u8) with
// four packed_bool items b1..b4 — q1, q2, v1, s1 at 3 or above.
func binaryItemsCohort(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	return itemsCohort(t, encoding.FieldTypePackedBool, func(v float64) any { return v >= 3 })
}

// itemsCohort is the brand survey's waves (categorical_u8) with four
// items b1..b4 of type typ — q1, q2, v1, s1 through conv.
func itemsCohort(t *testing.T, typ encoding.FieldType, conv func(float64) any) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	cols := readBrandColumns(t)
	fields := []encoding.Field{{Name: "wave", Type: encoding.FieldTypeCategoricalU8, Description: "Synthetic survey wave"}}
	src := []string{"q1", "q2", "v1", "s1"}
	for i := range src {
		fields = append(fields, encoding.Field{Name: "b" + strconv.Itoa(i+1), Type: typ, Description: "Synthetic brand-survey rating item"})
	}
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "brand.pulse", encoding.Schema{Fields: fields}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := range cols["wave"] {
		row := pulse.CohortRow{strconv.Itoa(int(cols["wave"][r]))}
		for _, c := range src {
			row = append(row, conv(cols[c][r]))
		}
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// readBrandColumns reads brand_data.csv by column.
func readBrandColumns(t *testing.T) map[string][]float64 {
	t.Helper()
	f, err := os.Open(semGroupTestdata + "brand_data.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]float64{}
	for _, rec := range recs[1:] {
		for i, h := range recs[0] {
			v, err := strconv.ParseFloat(rec[i], 64)
			if err != nil {
				t.Fatal(err)
			}
			out[h] = append(out[h], v)
		}
	}
	return out
}

// TestSEM_OrdinalParameterizationRefusal: params.parameterization
// "theta" is SERVICE_VALIDATION parameterization_unsupported, "delta"
// (lavaan's default) fits, any other value — or the knob without an
// ordinal estimator — is bad_params; predict refuses each with the same
// entry.
func TestSEM_OrdinalParameterizationRefusal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, c := range []struct {
		params map[string]any
		reason string
	}{
		{map[string]any{"parameterization": "theta"}, "parameterization_unsupported"},
		{map[string]any{"group": nil, "parameterization": "theta"}, "parameterization_unsupported"},
		{map[string]any{"parameterization": "probit"}, "bad_params"},
		{map[string]any{"estimator": nil, "ordered": nil, "parameterization": "delta"}, "bad_params"},
	} {
		req := ordinalGroupRequest(t, "brand_ord", brandOrdered, c.params)
		_, err := p.Process(ctx, req)
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != c.reason || ce.Details["param"] != "parameterization" {
			t.Errorf("%v: %v (%v), want %s on parameterization", c.params, err, semDetailsOf(ce), c.reason)
			continue
		}
		sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req), err)
	}
	res := semGroupRun(t, p, ordinalGroupRequest(t, "brand_ord", brandOrdered, map[string]any{"parameterization": "delta", "estimator": "dwls"}))
	if res.Error != nil || res.Scalars["converged"] != 1 {
		t.Fatalf("parameterization delta: %+v", res.Error)
	}
}

// TestSEM_OrdinalGroupsDataRefusals: the data-dependent refusals of a
// grouped ordinal fit fail the slot alone (PULSE_SLOT_FAILED beside the
// slot's own error) while predict accepts the request: a category one
// wave lacks (the coding is the pooled rows') is
// PULSE_MATRIX_INSUFFICIENT_N empty_category naming the level, and a
// group_partial threshold the data do not hold is
// PULSE_MATRIX_INSUFFICIENT_N partial_threshold_absent.
func TestSEM_OrdinalGroupsDataRefusals(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	// rows 1..600 are wave 1: a filter dropping wave 2's v2 = 1 rows
	// leaves category 1 held by wave 1 alone.
	req := ordinalGroupRequest(t, "brand_ord", brandOrdered, nil)
	req.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `!(wave == "2" && v2 == 1)`}}
	for _, c := range []struct {
		req    *types.Request
		reason string
	}{
		{req, "empty_category"},
		{ordinalGroupRequest(t, "brand_ord", brandOrdered, map[string]any{"group_equal": []string{"thresholds"}, "group_partial": []string{"q1|t9"}}), "partial_threshold_absent"},
	} {
		env := predictEnvelope(t, p, fsys, "brand.pulse", c.req)
		if len(env.Errors) > 0 {
			t.Fatalf("%s: predict refused %+v; the refusal is data-dependent", c.reason, env.Errors[0])
		}
		res := semGroupRun(t, p, c.req)
		if res.Error == nil || res.Error.Code != string(errors.PULSE_MATRIX_INSUFFICIENT_N) || res.Error.Details["reason"] != c.reason {
			t.Fatalf("%s: slot error %+v", c.reason, res.Error)
		}
		if c.reason == "empty_category" && (res.Error.Details["level"] != "2" || res.Error.Details["variable"] != "v2") {
			t.Errorf("empty_category details %v, want level 2 variable v2", res.Error.Details)
		}
	}
}
