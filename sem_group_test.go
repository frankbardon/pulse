package pulse_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// sem_group_test.go is MAT_SEM's joint multi-group fit (params.group)
// end to end through the facade: the brand-survey cohort split by wave
// against lavaan's group = "wave" fits (internal/sem/testdata
// brand_mg_*_lavaan.json, fit_lavaan.R), the same bits under every
// decode / shard worker count and a shard archive, a frequency weight
// equal to the expanded rows within each group, and the refusals —
// config-time ones identical in predict and runtime, data-dependent
// ones failing the slot alone.

const semGroupTestdata = "internal/sem/testdata/"

// brandGroupCohort is how one cohort of the brand-survey CSV is built.
type brandGroupCohort struct {
	csv string
	// wave is the group field's type: categorical_u8 (labels "1" /
	// "2"), packed_bool (wave 2 true) or u8.
	wave encoding.FieldType
	// rows keeps the first rows only (0: all).
	rows int
	// weight adds a frequency weight column w; repeat writes each row
	// that many times (the weight's expansion).
	weight, repeat func(r int) int
	// shards cuts the cohort into a shard archive of that many rows per
	// shard (0: a single file).
	shards int
	// dict pre-seeds a categorical wave's dictionary (levels without
	// rows included), in order.
	dict []string
	// fsys is the filesystem to write on (nil: a fresh MemMapFs).
	fsys afero.Fs
	// relabel rewrites a categorical wave's label per row r (nil: the
	// CSV's own), e.g. to split a wave in two.
	relabel func(r int, wave string) string
}

// build writes the cohort as "brand.pulse" on c.fsys (a fresh
// in-memory filesystem when nil) and returns an instance over it with
// opts.
func (c brandGroupCohort) build(t *testing.T, opts pulse.Options) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	f, err := os.Open(semGroupTestdata + c.csv + "_data.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	header := recs[0]
	fields := make([]encoding.Field, len(header))
	for i, h := range header {
		fields[i] = encoding.Field{Name: h, Type: encoding.FieldTypeF64, Description: "Synthetic brand-survey rating " + h}
		if h == "wave" {
			fields[i] = encoding.Field{Name: h, Type: c.wave, Description: "Synthetic survey wave"}
			if c.dict != nil {
				d := encoding.NewDictionary()
				for _, l := range c.dict {
					if _, err := d.Add(l); err != nil {
						t.Fatal(err)
					}
				}
				fields[i].Dictionary = d
			}
		}
	}
	if c.weight != nil {
		fields = append(fields, encoding.Field{Name: "w", Type: encoding.FieldTypeF64, Description: "Synthetic frequency weight"})
	}
	fsys := c.fsys
	if fsys == nil {
		fsys = afero.NewMemMapFs()
	}
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	var bopts pulse.CohortBuilderOptions
	if c.shards > 0 {
		bopts.Shards = &pulse.ShardSplit{MaxRecords: c.shards}
	}
	b, err := p.NewCohortBuilder(context.Background(), "brand.pulse", encoding.Schema{Fields: fields}, bopts)
	if err != nil {
		t.Fatal(err)
	}
	body := recs[1:]
	if c.rows > 0 {
		body = body[:c.rows]
	}
	for r, rec := range body {
		row := make(pulse.CohortRow, len(fields))
		for i, s := range rec {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				t.Fatal(err)
			}
			if header[i] != "wave" {
				row[i] = v
				continue
			}
			switch c.wave {
			case encoding.FieldTypeCategoricalU8:
				row[i] = s
				if c.relabel != nil {
					row[i] = c.relabel(r, s)
				}
			case encoding.FieldTypePackedBool:
				row[i] = v == 2
			default:
				row[i] = uint64(v)
			}
		}
		if c.weight != nil {
			row[len(fields)-1] = float64(c.weight(r))
		}
		times := 1
		if c.repeat != nil {
			times = c.repeat(r)
		}
		for range times {
			if err := b.Append(row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// brandMembers are the brand models' observed variables.
var brandMembers = []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3", "recommend"}

func semGroupRequest(t *testing.T, model string, params map[string]any) *types.Request {
	t.Helper()
	desc, err := os.ReadFile(semGroupTestdata + model + "_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{"model": string(desc), "group": "wave"}
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
		Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: brandMembers, Params: raw}},
	}
}

func semGroupRun(t *testing.T, p *pulse.Pulse, req *types.Request) types.MatrixResult {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Matrices) != 1 {
		t.Fatalf("%d matrices", len(resp.Matrices))
	}
	return resp.Matrices[0]
}

// mgLavaan is a multi-group fit_lavaan.R reference.
type mgLavaan struct {
	Lavaan    string              `json:"lavaan"`
	N         []float64           `json:"n"`
	NTotal    float64             `json:"n_total"`
	Labels    []string            `json:"group_labels"`
	Observed  []string            `json:"observed"`
	Fit       map[string]*float64 `json:"fit"`
	Chi2Group []float64           `json:"chi2_group"`
	Options   struct {
		GroupEqual   []string `json:"group_equal"`
		GroupPartial []string `json:"group_partial"`
	} `json:"options"`
	Params []struct {
		LHS, Op, RHS, Group string
		Label               string
		Free                bool
		Est, SE, Z, P       *float64
		CILower             *float64 `json:"ci_lower"`
		CIUpper             *float64 `json:"ci_upper"`
		StdAll              *float64 `json:"std_all"`
	} `json:"params"`
	Groups []struct {
		ImpliedCov [][]float64 `json:"implied_cov"`
		ImpliedMu  []float64   `json:"implied_mean"`
		SampleMu   []float64   `json:"sample_mean"`
		SampleCov  [][]float64 `json:"sample_cov"`
	} `json:"groups"`
}

var mgScalarKeys = map[string]string{
	"chi2": "chisq", "dof": "df", "chi2_p_value": "pvalue", "chi2_baseline": "baseline.chisq",
	"dof_baseline": "baseline.df", "cfi": "cfi", "tli": "tli", "nfi": "nfi", "rmsea": "rmsea",
	"aic": "aic", "bic": "bic", "loglik": "logl", "gfi": "gfi_lisrel", "agfi": "agfi_lisrel", "n_params": "npar",
}

func mgRel(a, b float64) float64 { return math.Abs(a-b) / math.Max(math.Abs(b), 1e-8) }

// checkMGAgainstLavaan holds a grouped MAT_SEM result to a lavaan
// multi-group reference: every "[level] key" estimate row, the joint
// and per-group scalars, the per-level auxiliaries, the aligned
// vectors and the primary (the reference level's implied covariance).
func checkMGAgainstLavaan(t *testing.T, res types.MatrixResult, ref mgLavaan, reference int) {
	t.Helper()
	if res.Error != nil || res.Scalars["converged"] != 1 {
		t.Fatalf("error %+v, converged %v, warnings %+v", res.Error, res.Scalars["converged"], res.Warnings)
	}
	est := res.Auxiliary["estimates"]
	wantCols := []string{"estimate", "std_estimate", "std_err", "z_value", "p_value", "ci_lower", "ci_upper"}
	if est == nil || !reflect.DeepEqual(est.ColumnKeys, wantCols) {
		t.Fatalf("estimates = %+v", est)
	}
	rows := map[string][]float64{}
	for i, k := range est.RowKeys {
		rows[k] = est.Values[i]
	}
	groupsAny, _ := res.Vectors["group"].([]any)
	groups := make([]string, len(groupsAny))
	for i, g := range groupsAny {
		groups[i], _ = g.(string)
	}
	labels, _ := res.Vectors["label"].([]string)
	params, _ := res.Vectors["parameter"].([]string)
	if len(groups) != len(est.RowKeys) || len(labels) != len(est.RowKeys) || len(params) != len(est.RowKeys) {
		t.Fatalf("vectors group / label / parameter = %d / %d / %d, estimates %d", len(groups), len(labels), len(params), len(est.RowKeys))
	}
	labelOf := map[string]string{}
	for i, k := range est.RowKeys {
		// A := row (a defined parameter) belongs to no group: unprefixed,
		// vectors.group null.
		if groupsAny[i] == nil && strings.Contains(k, " := ") {
			labelOf[k] = labels[i]
			continue
		}
		if !strings.HasPrefix(k, "["+groups[i]+"] ") {
			t.Fatalf("row %q carries vectors.group %q", k, groups[i])
		}
		labelOf[k] = labels[i]
	}
	matched := 0
	for _, r := range ref.Params {
		key := r.LHS + " " + r.Op + " " + r.RHS
		switch r.Op {
		case "=~":
			key = r.RHS + " ~ " + r.LHS
		case "~1":
			key = r.LHS + " ~1"
		}
		if r.Op != ":=" {
			key = "[" + r.Group + "] " + key
		}
		row, ok := rows[key]
		if !ok && r.Op == "~~" {
			row, ok = rows["["+r.Group+"] "+r.RHS+" ~~ "+r.LHS]
		}
		if !ok {
			t.Errorf("lavaan row %q missing", key)
			continue
		}
		matched++
		near := func(what string, g float64, w *float64) {
			if w == nil || !(mgRel(g, *w) <= 1e-4) {
				t.Errorf("%s %s = %.10g, lavaan %v", key, what, g, w)
			}
		}
		near("estimate", row[0], r.Est)
		near("std_estimate", row[1], r.StdAll)
		// lavaan labels a group.equal row ".pN." (its class); only a
		// description label is the model's — on a fixed row too.
		if r.Label != "" && !strings.HasPrefix(r.Label, ".p") && labelOf[key] != r.Label {
			t.Errorf("%s vectors.label = %q, lavaan %q", key, labelOf[key], r.Label)
		}
		if !r.Free && r.Op != ":=" {
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
	if matched != len(est.RowKeys) {
		t.Errorf("matched %d lavaan rows, the result has %d", matched, len(est.RowKeys))
	}
	for k, lk := range mgScalarKeys {
		if w := ref.Fit[lk]; w == nil {
			// lavaan's NA (NFI past the baseline's dof): undefined.
			if !math.IsNaN(res.Scalars[k]) {
				t.Errorf("scalar %s = %.10g, lavaan reports none", k, res.Scalars[k])
			}
		} else if !(mgRel(res.Scalars[k], *w) <= 1e-3) {
			t.Errorf("scalar %s = %.10g, lavaan %s %v", k, res.Scalars[k], lk, w)
		}
	}
	if res.Scalars["n"] != ref.NTotal {
		t.Errorf("n = %v, lavaan %v", res.Scalars["n"], ref.NTotal)
	}
	pos := map[string]int{}
	for i, v := range ref.Observed {
		pos[v] = i
	}
	for g, l := range ref.Labels {
		if c := res.Scalars["chi2_group_"+l]; !(mgRel(c, ref.Chi2Group[g]) <= 1e-3) {
			t.Errorf("chi2_group_%s = %g, lavaan %g", l, c, ref.Chi2Group[g])
		}
		if n := res.Scalars["n_group_"+l]; n != ref.N[g] {
			t.Errorf("n_group_%s = %g, lavaan %g", l, n, ref.N[g])
		}
		rg := ref.Groups[g]
		for name, want := range map[string][]float64{"implied_means." + l: rg.ImpliedMu, "observed_means." + l: rg.SampleMu} {
			mv := res.Auxiliary[name]
			if mv == nil || !reflect.DeepEqual(mv.RowKeys, brandMembers) {
				t.Fatalf("auxiliary.%s = %+v", name, mv)
			}
			for r, f := range brandMembers {
				if !(mgRel(mv.Values[r][0], want[pos[f]]) <= 1e-4) {
					t.Errorf("%s %s = %g, lavaan %g", name, f, mv.Values[r][0], want[pos[f]])
				}
			}
		}
		imp, resid := res.Auxiliary["implied_covariance."+l], res.Auxiliary["residual_covariance."+l]
		if imp == nil || resid == nil {
			t.Fatalf("level %s lacks implied / residual covariance", l)
		}
		for r, a := range brandMembers {
			for c, b := range brandMembers {
				w := rg.ImpliedCov[pos[a]][pos[b]]
				if !(mgRel(imp.Values[r][c], w) <= 1e-4) {
					t.Errorf("implied_covariance.%s %s,%s = %g, lavaan %g", l, a, b, imp.Values[r][c], w)
				}
				if d := resid.Values[r][c] - (rg.SampleCov[pos[a]][pos[b]] - w); !(math.Abs(d) <= 1e-4*math.Max(math.Abs(w), 1)) {
					t.Errorf("residual_covariance.%s %s,%s off lavaan's by %g", l, a, b, d)
				}
				if g == reference && res.Primary.Values[r][c] != imp.Values[r][c] {
					t.Fatalf("primary %s,%s = %g, the reference level's implied covariance %g", a, b, res.Primary.Values[r][c], imp.Values[r][c])
				}
			}
		}
	}
	want := 1 + 4*len(ref.Labels)
	if len(res.Auxiliary) != want {
		keys := make([]string, 0, len(res.Auxiliary))
		for k := range res.Auxiliary {
			keys = append(keys, k)
		}
		t.Errorf("auxiliary keys %v, want estimates + 4 per level", keys)
	}
}

func loadMGLavaan(t *testing.T, name string) mgLavaan {
	t.Helper()
	raw, err := os.ReadFile(semGroupTestdata + name + "_lavaan.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref mgLavaan
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	if ref.Lavaan == "" || len(ref.Labels) != 2 {
		t.Fatalf("%s: not a two-group lavaan reference", name)
	}
	return ref
}

// TestSEM_MultiGroupProcessMatchesLavaan: the configural fit (equal
// groups) and the misfitting one (600 / 400 rows, a label shared across
// groups) match lavaan's group = "wave" fits through the engine;
// group_reference picks the primary.
func TestSEM_MultiGroupProcessMatchesLavaan(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ ref, csv, model string }{
		{"brand_mg_configural", "brand", "brand_ms"},
		{"brand_mg_misfit", "brand_uneq", "brand_mgmis"},
	} {
		t.Run(c.ref, func(t *testing.T) {
			t.Parallel()

			ref := loadMGLavaan(t, c.ref)
			p, _ := brandGroupCohort{csv: c.csv, wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
			checkMGAgainstLavaan(t, semGroupRun(t, p, semGroupRequest(t, c.model, nil)), ref, 0)
			checkMGAgainstLavaan(t, semGroupRun(t, p, semGroupRequest(t, c.model, map[string]any{"group_reference": "2"})), ref, 1)
		})
	}
}

// TestSEM_MultiGroupFieldTypesAgree: a grouped fit on every supported
// group field type gives the same estimates (only the level labels
// differ). That each is the same bits under every worker knob and over
// a shard archive is the MAT_SEM invariance family's
// (TestSEMInvariance_{Archive,Heavy}, multigroup_*).
func TestSEM_MultiGroupFieldTypesAgree(t *testing.T) {
	t.Parallel()

	var base []float64
	for _, wave := range []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypePackedBool, encoding.FieldTypeU8} {
		p, _ := brandGroupCohort{csv: "brand_uneq", wave: wave}.build(t, pulse.Options{})
		serial := semGroupRun(t, p, semGroupRequest(t, "brand_mgmis", nil))
		if serial.Error != nil {
			t.Fatalf("%s: %+v", wave, serial.Error)
		}
		var flat []float64
		for _, row := range serial.Auxiliary["estimates"].Values {
			flat = append(flat, row...)
		}
		if base == nil {
			base = flat
			continue
		}
		for i := range flat {
			if flat[i] != base[i] && !(math.IsNaN(flat[i]) && math.IsNaN(base[i])) {
				t.Fatalf("estimate figure %d = %g on %s, %g on categorical_u8", i, flat[i], wave, base[i])
			}
		}
	}
}

// TestSEM_MultiGroupFrequencyWeight: a frequency weight applies within
// each group — n_g = Σw — so the fit matches the expanded rows'.
func TestSEM_MultiGroupFrequencyWeight(t *testing.T) {
	t.Parallel()

	w := func(r int) int { return 1 + r%3 }
	pw, _ := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeCategoricalU8, weight: w}.build(t, pulse.Options{})
	pe, _ := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeCategoricalU8, repeat: w}.build(t, pulse.Options{})
	req := semGroupRequest(t, "brand_mgmis", nil)
	plain := semGroupRun(t, pe, req)
	req.Matrices[0].Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	weighted := semGroupRun(t, pw, req)
	if plain.Error != nil || weighted.Error != nil {
		t.Fatalf("errors %+v / %+v", plain.Error, weighted.Error)
	}
	for _, k := range []string{"n", "n_group_1", "n_group_2"} {
		if weighted.Scalars[k] != plain.Scalars[k] {
			t.Errorf("%s weighted %v, expanded %v", k, weighted.Scalars[k], plain.Scalars[k])
		}
	}
	if weighted.Scalars["n_group_1"] != 1200 {
		t.Errorf("n_group_1 = %v, want Σw = 1200", weighted.Scalars["n_group_1"])
	}
	for k, v := range plain.Scalars {
		if k == "iterations" {
			continue // the solver's path, not a figure: the sums differ in their last bits
		}
		if !(mgRel(weighted.Scalars[k], v) <= 1e-6) && !(math.Abs(weighted.Scalars[k]-v) <= 1e-9) {
			t.Errorf("scalar %s weighted %.12g, expanded %.12g", k, weighted.Scalars[k], v)
		}
	}
	a, b := weighted.Auxiliary["estimates"], plain.Auxiliary["estimates"]
	for i := range b.Values {
		for j, v := range b.Values[i] {
			if g := a.Values[i][j]; !(mgRel(g, v) <= 1e-6) && !(math.Abs(g-v) <= 1e-9) && !(math.IsNaN(g) && math.IsNaN(v)) {
				t.Errorf("%s %s weighted %.12g, expanded %.12g", b.RowKeys[i], b.ColumnKeys[j], g, v)
			}
		}
	}
}

// TestSEM_MultiGroupRefusals: the config-time refusals are coded and
// identical in predict and runtime; the data-dependent ones (fewer than
// two levels with rows, a level thinner than the model, a reference
// level without rows) fail the slot alone.
func TestSEM_MultiGroupRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cat, catFS := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, c := range []struct {
		name   string
		cohort brandGroupCohort
		mut    func(*types.Request)
		reason string
	}{
		{"request groups", brandGroupCohort{}, func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}}
		}, "group_with_groups"},
		{"f64 group field", brandGroupCohort{}, func(r *types.Request) {
			r.Matrices[0].Params = setParam(r.Matrices[0].Params, "group", "q1")
			r.Matrices[0].Fields = []string{"q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3", "recommend"}
			r.Matrices[0].Params = setParam(r.Matrices[0].Params, "model", "f =~ q2 + q3 + v1 + v2 + v3 + s1 + s2 + s3 + recommend")
		}, "group_field_type"},
		{"one dictionary level", brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeCategoricalU8, rows: 600}, nil, "group_one_level"},
		{"unknown reference", brandGroupCohort{}, func(r *types.Request) {
			r.Matrices[0].Params = setParam(r.Matrices[0].Params, "group_reference", "3")
		}, "bad_params"},
		{"group is a member", brandGroupCohort{}, func(r *types.Request) {
			r.Matrices[0].Params = setParam(r.Matrices[0].Params, "group", "q1")
		}, "bad_params"},
		{"meanstructure false", brandGroupCohort{}, func(r *types.Request) {
			r.Matrices[0].Params = setParam(r.Matrices[0].Params, "meanstructure", false)
		}, "bad_params"},
		{"unknown field", brandGroupCohort{}, func(r *types.Request) {
			r.Matrices[0].Params = setParam(r.Matrices[0].Params, "group", "absent")
		}, "bad_params"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, fsys := cat, catFS
			if c.cohort.csv != "" {
				p, fsys = c.cohort.build(t, pulse.Options{})
			}
			req := func() *types.Request {
				r := semGroupRequest(t, "brand_mgmis", nil)
				if c.mut != nil {
					c.mut(r)
				}
				return r
			}
			_, rerr := p.Process(ctx, req())
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != c.reason {
				t.Fatalf("runtime error %v (details %v), want SERVICE_VALIDATION reason %s", rerr, semDetailsOf(ce), c.reason)
			}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req())
			sameEntry(t, env, rerr)
			if env.Data.(*descriptor.PredictResult).Valid {
				t.Fatal("predict Valid=true")
			}
		})
	}
	t.Run("group_reference without group", func(t *testing.T) {
		req := semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "group_reference": "1"})
		_, rerr := cat.Process(ctx, req)
		var ce *errors.CodedError
		if !asCoded(rerr, &ce) || ce.Details["param"] != "group_reference" {
			t.Fatalf("runtime error %v, want a group_reference refusal", rerr)
		}
		sameEntry(t, predictEnvelope(t, cat, catFS, "brand.pulse", semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "group_reference": "1"})), rerr)
	})

	for _, c := range []struct {
		name   string
		cohort brandGroupCohort
		params map[string]any
		code   errors.Code
		reason string
	}{
		// 600 wave-1 rows then 5 wave-2 rows: fewer than the model's 10
		// observed variables.
		{"level too thin", brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeU8, rows: 605}, nil, errors.PULSE_MATRIX_INSUFFICIENT_N, "level_too_thin"},
		// an integer field's levels are its values: wave 1 alone.
		{"one level with rows", brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeU8, rows: 600}, nil, errors.PULSE_MATRIX_INSUFFICIENT_N, "one_level"},
		{"reference without rows", brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeU8}, map[string]any{"group_reference": "7"}, errors.PULSE_MATRIX_INSUFFICIENT_N, "reference_absent"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			p, _ := c.cohort.build(t, pulse.Options{})
			res := semGroupRun(t, p, semGroupRequest(t, "brand_mgmis", c.params))
			if res.Error == nil || res.Error.Code != string(c.code) || res.Error.Details["reason"] != c.reason {
				t.Fatalf("slot error %+v, want %s reason %s", res.Error, c.code, c.reason)
			}
			if res.Primary != nil || res.Name != "sem" {
				t.Fatalf("a failed slot keeps its identity and no figure: %+v", res)
			}
		})
	}
}

func setParam(raw json.RawMessage, k string, v any) json.RawMessage {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	m[k] = v
	out, _ := json.Marshal(m)
	return out
}

func asCoded(err error, ce **errors.CodedError) bool { return stderrors.As(err, ce) }

func semDetailsOf(ce *errors.CodedError) map[string]any {
	if ce == nil {
		return nil
	}
	return ce.Details
}
