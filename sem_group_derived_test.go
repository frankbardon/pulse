package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// sem_group_derived_test.go is MAT_SEM grouped by a column the request
// computes (a derived group): params.group names an attribute label,
// params.group_levels optionally declares its levels — value, label,
// order (= the c(...) position) and count. A derived fit over the same
// rows is the same bits as the cohort-field fit; a row whose value is
// not a declared level (or not integral) is a listwise drop counted in
// n_listwise_dropped and warned once per slot
// (PULSE_MATRIX_GROUP_LEVEL_UNDECLARED); the config-time refusals are
// identical in predict and runtime.

// fromTo declares the levels 1 ("from") and 2 ("to").
var fromTo = []map[string]any{{"value": 1, "name": "from"}, {"value": 2, "name": "to"}}

// derivedGroupRequest is semGroupRequest grouped by "period", an
// ATTR_FORMULA of expr.
func derivedGroupRequest(t *testing.T, model, expr string, params map[string]any) *types.Request {
	t.Helper()
	all := map[string]any{"group": "period"}
	for k, v := range params {
		all[k] = v
	}
	req := semGroupRequest(t, model, all)
	req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "period", Expression: expr}}
	return req
}

// sameFigures fails unless a and b hold the same figures, bit for bit
// (NaN equal to NaN).
func sameFigures(t *testing.T, what string, a, b [][]float64) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s: %d rows, want %d", what, len(a), len(b))
	}
	for i := range a {
		for j := range a[i] {
			if math.Float64bits(a[i][j]) != math.Float64bits(b[i][j]) && !(math.IsNaN(a[i][j]) && math.IsNaN(b[i][j])) {
				t.Fatalf("%s row %d col %d = %v, want %v", what, i, j, a[i][j], b[i][j])
			}
		}
	}
}

// TestSEM_DerivedGroupMatchesFieldGroup: grouping by a computed copy of
// the u8 wave field is the field fit, bit for bit — with declared
// levels under their names (from / to), without them under the
// observed values.
func TestSEM_DerivedGroupMatchesFieldGroup(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	field := semGroupRun(t, p, semGroupRequest(t, "brand_mgmis", nil))
	declared := semGroupRun(t, p, derivedGroupRequest(t, "brand_mgmis", "wave", map[string]any{"group_levels": fromTo}))
	observed := semGroupRun(t, p, derivedGroupRequest(t, "brand_mgmis", "wave", nil))
	for _, r := range []types.MatrixResult{field, declared, observed} {
		if r.Error != nil {
			t.Fatalf("slot failed: %+v", r.Error)
		}
	}
	fe, de, oe := field.Auxiliary["estimates"], declared.Auxiliary["estimates"], observed.Auxiliary["estimates"]
	sameFigures(t, "declared", de.Values, fe.Values)
	sameFigures(t, "observed", oe.Values, fe.Values)
	rename := strings.NewReplacer("[1] ", "[from] ", "[2] ", "[to] ")
	for i, k := range fe.RowKeys {
		if want := rename.Replace(k); de.RowKeys[i] != want {
			t.Fatalf("declared row %d = %q, want %q", i, de.RowKeys[i], want)
		}
		if oe.RowKeys[i] != k {
			t.Fatalf("observed row %d = %q, want %q", i, oe.RowKeys[i], k)
		}
	}
	for _, c := range []struct{ field, derived string }{
		{"n_group_1", "n_group_from"}, {"n_group_2", "n_group_to"}, {"chi2_group_1", "chi2_group_from"}, {"chi2_group_2", "chi2_group_to"},
	} {
		if declared.Scalars[c.derived] != field.Scalars[c.field] || observed.Scalars[c.field] != field.Scalars[c.field] {
			t.Errorf("%s = %v (declared %s %v, observed %v)", c.field, field.Scalars[c.field], c.derived, declared.Scalars[c.derived], observed.Scalars[c.field])
		}
	}
	if g, _ := declared.Vectors["group"].([]any); len(g) == 0 || g[0] != "from" {
		t.Errorf("vectors.group = %v, want the declared names", declared.Vectors["group"])
	}
	if declared.Scalars["chi2"] != field.Scalars["chi2"] {
		t.Errorf("chi2 %v, field %v", declared.Scalars["chi2"], field.Scalars["chi2"])
	}
}

// wavePairModel gives the service path a label per declared level and
// reads the from -> to change.
const wavePairModel = "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3\n" +
	"recommend ~ quality + value + c(bfrom, bto)*service\nshift := bto - bfrom"

// wavePairRequest groups the three-wave u8 cohort by the request's
// from_wave / to_wave pair: period 1 on from, 2 on to, 0 elsewhere.
func wavePairRequest(t *testing.T, from, to int) *types.Request {
	t.Helper()
	req := derivedGroupRequest(t, "brand_mgmis", "wave == params.to_wave ? 2 : (wave == params.from_wave ? 1 : 0)",
		map[string]any{"model": wavePairModel, "group_levels": fromTo})
	req.Params = map[string]any{"from_wave": json.Number(itoa(from)), "to_wave": json.Number(itoa(to))}
	return req
}

// threeWaveU8 is the brand rows as a u8 wave: 600 of wave 1, 300 of
// wave 2, 300 of wave 3.
func threeWaveU8(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8, relabel: func(r int, w string) string {
		if r >= 900 {
			return "3"
		}
		return w
	}}.build(t, opts)
	return p
}

// definedRow is the estimates row of the := line named name.
func definedRow(t *testing.T, res types.MatrixResult, name string) []float64 {
	t.Helper()
	est := res.Auxiliary["estimates"]
	for i, k := range est.RowKeys {
		if strings.HasPrefix(k, name+" :=") {
			return est.Values[i]
		}
	}
	t.Fatalf("no := row %s in %v", name, est.RowKeys)
	return nil
}

// TestSEM_DerivedGroupWavePair is the headline: a params-driven wave
// pair, reversed included, fits from -> to over the two waves' rows;
// the third wave's rows are undeclared — dropped listwise, counted,
// warned once and lifted to the envelope.
func TestSEM_DerivedGroupWavePair(t *testing.T) {
	t.Parallel()

	p := threeWaveU8(t, pulse.Options{})
	rows := map[int]float64{1: 600, 2: 300, 3: 300}
	shifts := map[[2]int]float64{}
	for _, pair := range [][2]int{{1, 2}, {2, 3}, {3, 1}, {1, 3}} {
		from, to := pair[0], pair[1]
		resp, err := p.Process(context.Background(), wavePairRequest(t, from, to))
		if err != nil {
			t.Fatalf("%v: Process: %v", pair, err)
		}
		res := resp.Matrices[0]
		if res.Error != nil {
			t.Fatalf("%v: slot failed: %+v", pair, res.Error)
		}
		if res.Scalars["n_group_from"] != rows[from] || res.Scalars["n_group_to"] != rows[to] {
			t.Errorf("%v: n_group_from %v, n_group_to %v, want %v / %v", pair, res.Scalars["n_group_from"], res.Scalars["n_group_to"], rows[from], rows[to])
		}
		undeclared := 1200 - rows[from] - rows[to]
		if got := matrixDropped(t, resp); got != int(undeclared) {
			t.Errorf("%v: n_listwise_dropped = %d, want %v", pair, got, undeclared)
		}
		for _, ws := range [][]*types.ResponseWarning{res.Warnings, resp.Warnings} {
			w := warningOf(ws, errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED)
			if w == nil {
				t.Fatalf("%v: warnings %+v, want PULSE_MATRIX_GROUP_LEVEL_UNDECLARED", pair, ws)
			}
			if w.Details["count"] != int64(undeclared) || w.Details["group_field"] != "period" || w.Details["matrix"] != "sem" {
				t.Errorf("%v: details %v, want count %v, group_field period, matrix sem", pair, w.Details, undeclared)
			}
			if _, bare := w.Details["group"]; bare {
				t.Errorf("%v: details carry a bare group key: %v", pair, w.Details)
			}
		}
		_, bf := semRow(t, res, "[from] recommend ~ service")
		_, bt := semRow(t, res, "[to] recommend ~ service")
		shift := definedRow(t, res, "shift")
		if shift[0] != bt[0]-bf[0] {
			t.Errorf("%v: shift %v, want to - from = %v", pair, shift[0], bt[0]-bf[0])
		}
		shifts[pair] = shift[0]
	}
	// Reversed: 3 -> 1 reads the opposite change of 1 -> 3.
	if a, b := shifts[[2]int{3, 1}], shifts[[2]int{1, 3}]; !(math.Abs(a+b) <= 1e-6*math.Max(math.Abs(b), 1)) {
		t.Errorf("shift 3->1 = %v, 1->3 = %v: want opposite", a, b)
	}
}

// matrixDropped is the one matrix slot's n_listwise_dropped.
func matrixDropped(t *testing.T, resp *types.Response) int {
	t.Helper()
	if resp.Components == nil || len(resp.Components.Matrices) != 1 {
		t.Fatalf("no matrix components: %+v", resp.Components)
	}
	return resp.Components.Matrices[0].NListwiseDropped
}

// TestSEM_DerivedGroupUndeclaredExact: declared values match integral
// row values exactly — 2 + 1 ulp, 1.5 and the third wave are
// undeclared — and a derived group without group_levels reads the
// integral values only, so its non-integral rows warn too.
func TestSEM_DerivedGroupUndeclaredExact(t *testing.T) {
	t.Parallel()

	p := threeWaveU8(t, pulse.Options{})
	for _, c := range []struct {
		name   string
		expr   string
		params map[string]any
	}{
		{"ulp above a level", "wave == 3 ? 2.0000000000000004 : wave", map[string]any{"group_levels": fromTo}},
		{"half", "wave == 3 ? 1.5 : wave", map[string]any{"group_levels": fromTo}},
		{"observed non-integral", "wave == 3 ? 1.5 : wave", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			resp, err := p.Process(context.Background(), derivedGroupRequest(t, "brand_mgmis", c.expr, c.params))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			res := resp.Matrices[0]
			if res.Error != nil {
				t.Fatalf("slot failed: %+v", res.Error)
			}
			w := warningOf(resp.Warnings, errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED)
			if w == nil || w.Details["count"] != int64(300) {
				t.Fatalf("warnings %+v, want UNDECLARED count 300", resp.Warnings)
			}
			if got := matrixDropped(t, resp); got != 300 {
				t.Errorf("n_listwise_dropped = %d, want 300", got)
			}
			if res.Scalars["n"] != 900 {
				t.Errorf("n = %v, want 900", res.Scalars["n"])
			}
		})
	}
}

// TestSEM_DerivedGroupNullIsListwise: a null computed group value —
// null or NaN — is a listwise drop like a null group field, never an
// undeclared level.
func TestSEM_DerivedGroupNullIsListwise(t *testing.T) {
	t.Parallel()

	p := threeWaveU8(t, pulse.Options{})
	for _, expr := range []string{"wave == 3 ? nil : wave", "wave == 3 ? 0.0/0.0 : wave"} {
		resp, err := p.Process(context.Background(), derivedGroupRequest(t, "brand_mgmis", expr, map[string]any{"group_levels": fromTo}))
		if err != nil {
			t.Fatalf("%s: Process: %v", expr, err)
		}
		if got := matrixDropped(t, resp); got != 300 || resp.Matrices[0].Scalars["n"] != 900 {
			t.Errorf("%s: n_listwise_dropped %d, n %v, want 300 / 900", expr, got, resp.Matrices[0].Scalars["n"])
		}
		if w := warningOf(resp.Warnings, errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED); w != nil {
			t.Errorf("%s: a null group warns undeclared: %v", expr, w.Details)
		}
	}
}

// TestSEM_DerivedGroupUndeclaredStrict: under Strict the undeclared
// rows fail the run with the warning's own code, at its slot.
func TestSEM_DerivedGroupUndeclaredStrict(t *testing.T) {
	t.Parallel()

	p := threeWaveU8(t, pulse.Options{Strict: true})
	_, err := p.Process(context.Background(), wavePairRequest(t, 1, 2))
	var ce *errors.CodedError
	if !asCoded(err, &ce) || ce.Code != errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED {
		t.Fatalf("Strict run error %v, want PULSE_MATRIX_GROUP_LEVEL_UNDECLARED", err)
	}
	if ce.Details["index"] != 0 {
		t.Errorf("details %v, want index 0", ce.Details)
	}
	// Every row declared: Strict runs clean.
	if _, err := p.Process(context.Background(), derivedGroupRequest(t, "brand_mgmis", "wave == 3 ? 2 : wave", map[string]any{"group_levels": fromTo})); err != nil {
		t.Fatalf("Strict run over declared rows only: %v", err)
	}
}

// TestSEM_DerivedGroupEmptyDeclaredLevel: a declared level without
// rows is no group — no n_group_ scalar (absent, as a cohort level
// without rows) — and warns PULSE_MATRIX_GROUP_LEVEL_EMPTY by its name.
func TestSEM_DerivedGroupEmptyDeclaredLevel(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	levels := append(append([]map[string]any(nil), fromTo...), map[string]any{"value": 3, "name": "later"})
	resp, err := p.Process(context.Background(), derivedGroupRequest(t, "brand_mgmis", "wave", map[string]any{"group_levels": levels}))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	res := resp.Matrices[0]
	if res.Error != nil {
		t.Fatalf("slot failed: %+v", res.Error)
	}
	w := warningOf(resp.Warnings, errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY)
	if w == nil {
		t.Fatalf("warnings %+v, want PULSE_MATRIX_GROUP_LEVEL_EMPTY", resp.Warnings)
	}
	if lv, _ := w.Details["levels"].([]string); len(lv) != 1 || lv[0] != "later" || w.Details["group_field"] != "period" {
		t.Errorf("details %v, want levels [later], group_field period", w.Details)
	}
	if _, ok := res.Scalars["n_group_later"]; ok {
		t.Error("n_group_later present: an empty level is no group")
	}
	if res.Scalars["n_group_from"] != 600 || res.Scalars["n_group_to"] != 400 {
		t.Errorf("n_group_from %v, n_group_to %v", res.Scalars["n_group_from"], res.Scalars["n_group_to"])
	}
	if warningOf(resp.Warnings, errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED) != nil {
		t.Error("every row is declared: no UNDECLARED warning")
	}
}

// TestSEM_DerivedGroupRefusals: every config-time refusal of a derived
// group, and the existing group checks over one, refuse identically in
// predict and runtime.
func TestSEM_DerivedGroupRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	lv := func(levels ...map[string]any) map[string]any { return map[string]any{"group_levels": levels} }
	one := func(v any, name ...string) map[string]any {
		m := map[string]any{"value": v}
		if len(name) > 0 {
			m["name"] = name[0]
		}
		return m
	}
	cmodel := "quality =~ q1 + q2 + q3\nvalue =~ v1 + l*v2 + v3 + s1 + l*s2 + s3\nrecommend ~ quality + c(a1, a2)*value"
	for _, c := range []struct {
		name   string
		params map[string]any
		mut    func(*types.Request)
		reason string
		param  string
	}{
		{"non-integral value", lv(one(1), one(2.5)), nil, "bad_params", "group_levels"},
		{"value out of range", lv(one(1), one(1e300)), nil, "bad_params", "group_levels"},
		{"missing value", lv(one(1), map[string]any{"name": "x"}), nil, "bad_params", "group_levels"},
		{"duplicate value", lv(one(1, "a"), one(1, "b")), nil, "bad_params", "group_levels"},
		{"duplicate name", lv(one(1, "a"), one(2, "a")), nil, "bad_params", "group_levels"},
		{"name equals another's value", lv(one(1, "2"), one(2)), nil, "bad_params", "group_levels"},
		{"empty name", lv(one(1, ""), one(2)), nil, "bad_params", "group_levels"},
		{"one level", lv(one(1)), nil, "bad_params", "group_levels"},
		{"group_levels without group", map[string]any{"group": nil, "group_levels": fromTo}, func(r *types.Request) { r.Attributes = nil }, "bad_params", "group_levels"},
		{"group_levels on a cohort field", map[string]any{"group": "wave", "group_levels": fromTo}, nil, "bad_params", "group_levels"},
		{"undeclared reference", map[string]any{"group_levels": fromTo, "group_reference": "1"}, nil, "bad_params", "group_reference"},
		{"observed reference not integral", map[string]any{"group_reference": "x"}, nil, "bad_params", "group_reference"},
		{"c(...) without group_levels", map[string]any{"model": cmodel}, nil, "bad_params", "model"},
		{"c(...) arity mismatch", map[string]any{"model": cmodel, "group_levels": []map[string]any{one(1), one(2), one(3)}}, nil, "bad_params", "model"},
		{"unknown name", map[string]any{"group": "absent"}, nil, "bad_params", "group"},
		{"request groups", nil, func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}}
		}, "group_with_groups", "group"},
		{"group is a member", map[string]any{"model": "f =~ q1 + q2 + q3 + v1 + v2 + v3 + s1 + s2 + s3 + recommend + period"}, func(r *types.Request) {
			r.Matrices[0].Fields = append(append([]string(nil), brandMembers...), "period")
		}, "bad_params", "group"},
		{"standardizing vector", nil, func(r *types.Request) {
			r.Vectors = []types.VectorSpec{{Name: "b", Fields: brandMembers, Standardize: types.VectorStandardizeZScore}}
			r.Matrices[0].Fields, r.Matrices[0].Vector = nil, "b"
		}, "bad_params", "group"},
		{"params.fit", map[string]any{"fit": "f"}, nil, "bad_params", "group"},
		{"non-f64 computed column", nil, func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Label: "period", Field: "wave", SubjectField: "q1", OrderField: "q2"}}
		}, "group_field_type", "group"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := func() *types.Request {
				r := derivedGroupRequest(t, "brand_mgmis", "wave", c.params)
				if c.mut != nil {
					c.mut(r)
				}
				return r
			}
			_, rerr := p.Process(ctx, req())
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != c.reason || ce.Details["param"] != c.param {
				t.Fatalf("runtime error %v (details %v), want SERVICE_VALIDATION reason %s param %s", rerr, semDetailsOf(ce), c.reason, c.param)
			}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req())
			sameEntry(t, env, rerr)
			if env.Data.(*descriptor.PredictResult).Valid {
				t.Fatal("predict Valid=true")
			}
		})
	}
}

// TestSEM_DerivedGroupArchiveMatchesFile: over a shard archive the
// undeclared count merges across shards, so the answer — figures,
// warning and n_listwise_dropped — is the single file's (the worker
// knobs are the invariance family's, derived_group).
func TestSEM_DerivedGroupArchiveMatchesFile(t *testing.T) {
	t.Parallel()

	file, _ := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	archive, _ := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeU8, shards: 230}.build(t, pulse.Options{ShardWorkers: 3})
	var counts []any
	var dropped []int
	var figures [][][]float64
	for _, p := range []*pulse.Pulse{file, archive} {
		resp, err := p.Process(context.Background(), derivedGroupRequest(t, "brand_mgmis", "q1 >= 5 ? 7 : wave", map[string]any{"group_levels": fromTo}))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		w := warningOf(resp.Warnings, errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED)
		if w == nil {
			t.Fatalf("warnings %+v, want UNDECLARED", resp.Warnings)
		}
		counts, dropped = append(counts, w.Details["count"]), append(dropped, matrixDropped(t, resp))
		figures = append(figures, resp.Matrices[0].Auxiliary["estimates"].Values)
	}
	if counts[0] != counts[1] || dropped[0] != dropped[1] || int64(dropped[0]) != counts[0] {
		t.Fatalf("count file %v archive %v, n_listwise_dropped file %d archive %d", counts[0], counts[1], dropped[0], dropped[1])
	}
	sameFigures(t, "archive", figures[1], figures[0])
}

// TestSEM_DerivedGroupPredictLevels: predict reports a derived group's
// declared levels (basis "declared", labels in declaration order — an
// upper bound, as a dictionary's) and the run fits those levels; an
// undeclared derived group's levels are the data's ("unknown", no
// list), sized at the shared observed-level bound.
func TestSEM_DerivedGroupPredictLevels(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	later := append(append([]map[string]any(nil), fromTo...), map[string]any{"value": 3, "name": "later"})
	for _, c := range []struct {
		name   string
		params map[string]any
		basis  string
		levels []string // predict's
		fitted []string // the run's
	}{
		{"declared", map[string]any{"group_levels": fromTo}, "declared", []string{"from", "to"}, []string{"from", "to"}},
		{"declared_empty_level", map[string]any{"group_levels": later}, "declared", []string{"from", "to", "later"}, []string{"from", "to"}},
		{"observed", nil, "unknown", nil, []string{"1", "2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			env := predictEnvelope(t, p, fsys, "brand.pulse", derivedGroupRequest(t, "brand_mgmis", "wave", c.params))
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %v", env.Errors)
			}
			mp := env.Data.(*descriptor.PredictResult).Matrices[0]
			if mp.GroupLevelsBasis != c.basis || strings.Join(mp.GroupLevels, ",") != strings.Join(c.levels, ",") || (c.levels == nil) != (mp.GroupLevels == nil) {
				t.Errorf("predict group levels %v (%q), want %v (%q)", mp.GroupLevels, mp.GroupLevelsBasis, c.levels, c.basis)
			}
			states := int64(len(c.levels))
			if c.levels == nil {
				states = vectors.SEMObservedGroupLevelLimit
			}
			if mp.EstimatedBuckets == nil || *mp.EstimatedBuckets != 1 || mp.EstimatedBytes == nil || *mp.EstimatedBytes%(mp.AccumulatorBytes*(1+states)) != 0 {
				t.Errorf("estimated_bytes %v, want a multiple of %d states x %d", mp.EstimatedBytes, 1+states, mp.AccumulatorBytes)
			}
			res := semGroupRun(t, p, derivedGroupRequest(t, "brand_mgmis", "wave", c.params))
			if res.Error != nil {
				t.Fatalf("slot error %+v", res.Error)
			}
			if got := runLevels(t, res); strings.Join(got, ",") != strings.Join(c.fitted, ",") {
				t.Errorf("fitted levels %v, want %v", got, c.fitted)
			}
		})
	}
}

// TestSEM_DerivedGroupComputedNamesPredict: predict resolves a group
// named by a feature output or a window label as runtime does — the
// static c(...) arity check refuses both alike, and with the arity
// right predict is clean.
func TestSEM_DerivedGroupComputedNamesPredict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	cmodel := "quality =~ q1 + q2 + q3\nvalue =~ v1 + l*v2 + v3 + s1 + l*s2 + s3\nrecommend ~ quality + c(a1, a2)*value"
	three := []map[string]any{{"value": 1}, {"value": 2}, {"value": 3}}
	for _, c := range []struct {
		name, group string
		mut         func(*types.Request)
	}{
		{"feature output", "SQRT_wave", func(r *types.Request) {
			r.Attributes = nil
			r.Features = []*types.Feature{{Type: types.FEAT_SQRT, Field: "wave"}}
		}},
		{"window label", "wave_lag", func(r *types.Request) {
			r.Attributes = nil
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "wave", Label: "wave_lag", OrderBy: []types.OrderKey{{Field: "q1"}}}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := func(levels []map[string]any) *types.Request {
				r := derivedGroupRequest(t, "brand_mgmis", "wave", map[string]any{"group": c.group, "model": cmodel, "group_levels": levels})
				c.mut(r)
				return r
			}
			_, rerr := p.Process(ctx, req(three))
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["param"] != "model" || ce.Details["group_field"] != c.group {
				t.Fatalf("runtime error %v (details %v), want the c(...) arity refusal on %s", rerr, semDetailsOf(ce), c.group)
			}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req(three))
			sameEntry(t, env, rerr)
			if env.Data.(*descriptor.PredictResult).Valid {
				t.Fatal("predict Valid=true")
			}
			ok := predictEnvelope(t, p, fsys, "brand.pulse", req(fromTo[:2]))
			if len(ok.Errors) != 0 {
				t.Fatalf("predict with the arity right: %+v", *ok.Errors[0])
			}
			if mp := ok.Data.(*descriptor.PredictResult).Matrices[0]; mp.GroupLevelsBasis != "declared" {
				t.Errorf("basis %q, want declared", mp.GroupLevelsBasis)
			}
		})
	}
}

// TestSEM_DerivedGroupObservedLevelLimit: an undeclared derived group
// holds one state per observed level, at most
// vectors.SEMObservedGroupLevelLimit — predict's state bound. One more
// distinct level fails the slot alone with
// PULSE_MATRIX_GROUP_LEVEL_LIMIT (identity kept, PULSE_SLOT_FAILED),
// on a single file and across shards alike; at the bound the run goes
// on (here to the per-level row check). Strict fails the run with the
// slot's own code; declared levels are never capped by the data.
func TestSEM_DerivedGroupObservedLevelLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	limit := int(vectors.SEMObservedGroupLevelLimit)
	rowIndex := func(r int) int { return r }
	file, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8, weight: rowIndex}.build(t, pulse.Options{})
	archive, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8, weight: rowIndex, shards: 100}.build(t, pulse.Options{ShardWorkers: 3})
	strict, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8, weight: rowIndex}.build(t, pulse.Options{Strict: true})
	// w is the row index: "w < n ? w : 0" has exactly n distinct values.
	levels := func(n int) string { return "w < " + itoa(n) + " ? w : 0" }
	for _, p := range []*pulse.Pulse{file, archive} {
		resp, err := p.Process(ctx, derivedGroupRequest(t, "brand_mgmis", levels(limit+1), nil))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		res := resp.Matrices[0]
		if res.Error == nil || res.Error.Code != string(errors.PULSE_MATRIX_GROUP_LEVEL_LIMIT) || res.Error.Details["limit"] != float64(limit) && res.Error.Details["limit"] != limit {
			t.Fatalf("slot error %+v, want PULSE_MATRIX_GROUP_LEVEL_LIMIT limit %d", res.Error, limit)
		}
		if res.Primary != nil || res.Name != "sem" {
			t.Fatalf("a failed slot keeps its identity and no figure: %+v", res)
		}
		if warningOf(resp.Warnings, errors.PULSE_SLOT_FAILED) == nil {
			t.Fatalf("warnings %+v, want PULSE_SLOT_FAILED", resp.Warnings)
		}
		at := semGroupRun(t, p, derivedGroupRequest(t, "brand_mgmis", levels(limit), nil))
		if at.Error == nil || at.Error.Code != string(errors.PULSE_MATRIX_INSUFFICIENT_N) {
			t.Fatalf("at the bound: slot error %+v, want the per-level row check (INSUFFICIENT_N)", at.Error)
		}
	}
	_, err := strict.Process(ctx, derivedGroupRequest(t, "brand_mgmis", levels(limit+1), nil))
	var ce *errors.CodedError
	if !asCoded(err, &ce) || ce.Code != errors.PULSE_MATRIX_GROUP_LEVEL_LIMIT {
		t.Fatalf("Strict run error %v, want PULSE_MATRIX_GROUP_LEVEL_LIMIT", err)
	}
	declared := semGroupRun(t, file, derivedGroupRequest(t, "brand_mgmis", levels(limit+1), map[string]any{"group_levels": fromTo}))
	if declared.Error != nil && declared.Error.Code == string(errors.PULSE_MATRIX_GROUP_LEVEL_LIMIT) {
		t.Fatalf("declared levels capped: %+v", declared.Error)
	}
}
