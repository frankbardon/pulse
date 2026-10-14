package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// updateFittedGolden regenerates testdata/fitted_state/scores.golden.json
// (`go test ./internal/processing/ -run TestFittedState_FitOnGolden
// -update-fitted-golden`). The golden was first written BEFORE the
// fitted-state refactor, so a pass proves every existing fit_on path
// kept its bits; never regenerate it to make a refactor pass.
var updateFittedGolden = flag.Bool("update-fitted-golden", false, "rewrite the fitted-attribute score golden")

const fittedGoldenPath = "testdata/fitted_state/scores.golden.json"

// fittedGoldenCase is one fitted vector attribute configuration run
// over an R-oracle fixture under each of its weight kinds.
type fittedGoldenCase struct {
	name    string
	typ     types.AttributeType
	fixture string
	fields  []string
	// split names the fixture column whose level fit_on keeps (level);
	// "" fits on every filtered row.
	split, level string
	params       map[string]any
	weights      []string
	// archExact marks a configuration whose whole scoring path is
	// FMA-free reference arithmetic (linalg InverseSPD, the metric
	// registry): its stored bits hold on every architecture. Every
	// other configuration reads a gonum decomposition (SymEigen / SVD)
	// or a libm-backed CDF, whose values may differ in the last ulps
	// across architectures (matrix-and-vectors.md, Package surface), so
	// the stored golden pins it within fittedCrossArchTol instead.
	archExact bool
}

// fittedCrossArchTol bounds a non-archExact score's distance from its
// stored bits, relative to 1 + the entry's largest |score|. The worst
// amd64-vs-arm64 drift measured on these fixtures is ~1e-14 of scale;
// any real model change moves scores by orders of magnitude more.
const fittedCrossArchTol = 1e-12

var allWeights = []string{"none", "frequency", "probability"}

// fittedGoldenCases spans every fitted operator and model branch:
// Mahalanobis d2/p, PC correlation/covariance/varimax, factor
// minres/ML/principal-axis with a rotation and both score methods (a
// Heywood fit included), and both fitted comparison metrics — fit_on
// and the three weight bases throughout.
func fittedGoldenCases() []fittedGoldenCase {
	att := []string{"rating", "complaints", "privileges", "learning"}
	att7 := []string{"rating", "complaints", "privileges", "learning", "raises", "critical", "advance"}
	mt := []string{"mpg", "disp", "hp", "wt"}
	tf := []string{"v1", "v2", "v3", "v4", "v5", "v6"}
	nl := []string{"x1", "x2", "x3", "x4"}
	return []fittedGoldenCase{
		{name: "maha_d2_attitude", typ: types.ATTR_MAHALANOBIS, fixture: "attitude", fields: att, weights: allWeights, archExact: true},
		{name: "maha_p_mtcars_fit_on", typ: types.ATTR_MAHALANOBIS, fixture: "mtcars", fields: mt, split: "am", level: "0",
			params: map[string]any{"output": "p"}, weights: allWeights},
		{name: "maha_d2_nulls", typ: types.ATTR_MAHALANOBIS, fixture: "nulls", fields: nl, weights: allWeights, archExact: true},
		{name: "pc1_corr_attitude", typ: types.ATTR_PC_SCORE, fixture: "attitude", fields: att7,
			params: map[string]any{"component": 1}, weights: allWeights},
		{name: "pc2_cov_mtcars_fit_on", typ: types.ATTR_PC_SCORE, fixture: "mtcars", fields: mt, split: "am", level: "1",
			params: map[string]any{"component": 2, "on": "covariance"}, weights: allWeights},
		{name: "pc2_varimax_attitude", typ: types.ATTR_PC_SCORE, fixture: "attitude", fields: att7,
			params: map[string]any{"component": 2, "rotation": "varimax", "components": 3}, weights: allWeights},
		{name: "fa_minres_regression_two_factor", typ: types.ATTR_FACTOR_SCORE, fixture: "two_factor", fields: tf,
			params: map[string]any{"factor": 1, "factors": 2, "rotation": "varimax"}, weights: allWeights},
		{name: "fa_ml_bartlett_two_factor", typ: types.ATTR_FACTOR_SCORE, fixture: "two_factor", fields: tf,
			params: map[string]any{"factor": 2, "factors": 2, "extraction": "ml", "rotation": "oblimin", "method": "bartlett"}, weights: allWeights},
		{name: "fa_pa_kaiser_attitude", typ: types.ATTR_FACTOR_SCORE, fixture: "attitude", fields: att7,
			params: map[string]any{"factor": 1, "extraction": "principal_axis"}, weights: allWeights},
		{name: "fa_heywood", typ: types.ATTR_FACTOR_SCORE, fixture: "heywood", fields: []string{"h1", "h2", "h3"},
			params: map[string]any{"factor": 1, "factors": 1}, weights: []string{"none"}},
		{name: "dist_std_euclidean_attitude", typ: types.ATTR_VEC_DISTANCE, fixture: "attitude", fields: att,
			params: map[string]any{"metric": "std_euclidean", "reference": []float64{60, 65, 50, 55}}, weights: allWeights, archExact: true},
		{name: "dist_mahalanobis_mtcars_fit_on", typ: types.ATTR_VEC_DISTANCE, fixture: "mtcars", fields: mt, split: "am", level: "0",
			params: map[string]any{"metric": "mahalanobis", "reference": []float64{20, 200, 140, 3}}, weights: allWeights, archExact: true},
		{name: "dist_mahalanobis_nulls", typ: types.ATTR_VEC_DISTANCE, fixture: "nulls", fields: nl,
			params: map[string]any{"metric": "mahalanobis", "reference": []float64{0, 0, 0, 0}}, weights: allWeights, archExact: true},
	}
}

// fittedGoldenEntry is one (case, weight) run: every row's score bits
// (hex, NaN for a null) and the Response warning codes in order.
type fittedGoldenEntry struct {
	Bits     []string `json:"bits"`
	Warnings []string `json:"warnings"`
}

func fittedGoldenKey(c fittedGoldenCase, weight string) string { return c.name + "/" + weight }

// fittedGoldenRequest builds c's request under weight with extra params
// merged over c.params (nil: none); fitOn adds c's fit_on predicate.
func fittedGoldenRequest(c fittedGoldenCase, weight string, fitOn bool, extra map[string]any) *types.Request {
	params := map[string]any{}
	for k, v := range c.params {
		params[k] = v
	}
	if fitOn && c.split != "" {
		params["fit_on"] = []map[string]any{{"type": "FILTER_INCLUDE", "field": "grp", "values": []string{c.level}}}
	}
	for k, v := range extra {
		params[k] = v
	}
	attr := &types.Attribute{Type: c.typ, Label: "s", Fields: append([]string(nil), c.fields...)}
	if len(params) > 0 {
		attr.Params, _ = json.Marshal(params)
	}
	return &types.Request{Weight: oracleWeight(weight), Attributes: []*types.Attribute{attr}}
}

// runFittedGolden runs build on both arms (streaming two-pass and
// buffered, which must agree bitwise) and returns the golden entry.
func runFittedGolden(t *testing.T, c fittedGoldenCase, build func() *types.Request) fittedGoldenEntry {
	t.Helper()
	return runFittedGoldenWith(t, c, nil, build)
}

// runFittedGoldenWith is runFittedGolden with models bound.
func runFittedGoldenWith(t *testing.T, c fittedGoldenCase, models *model.Set, build func() *types.Request) fittedGoldenEntry {
	t.Helper()
	schema, _ := hotellingFixture(t, c.fixture, c.split)
	mk := func() []*Record { _, recs := hotellingFixture(t, c.fixture, c.split); return recs }
	streamed, buffered, resp := runFittedArms(t, schema, mk, "s", models, build)
	if !sameBits(streamed, buffered) {
		t.Fatalf("streaming two-pass and buffered scores differ:\n%v\n%v", streamed, buffered)
	}
	e := fittedGoldenEntry{Bits: make([]string, len(streamed)), Warnings: []string{}}
	for i, v := range streamed {
		if math.IsNaN(v) {
			e.Bits[i] = "NaN"
			continue
		}
		e.Bits[i] = strconv.FormatUint(math.Float64bits(v), 16)
	}
	for _, w := range resp.Warnings {
		e.Warnings = append(e.Warnings, w.Code)
	}
	return e
}

func readFittedGolden(t *testing.T) map[string]fittedGoldenEntry {
	t.Helper()
	raw, err := os.ReadFile(fittedGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update-fitted-golden ONLY on a deliberate score change)", fittedGoldenPath, err)
	}
	var out map[string]fittedGoldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// fittedGoldenDiff compares a run against its STORED golden entry: the
// warnings, the row count and every null position exactly; the score
// bits exactly when exact (c.archExact), else within fittedCrossArchTol
// of 1 + the entry's largest |score|. "" means they agree.
func fittedGoldenDiff(exact bool, got, want fittedGoldenEntry) string {
	if fmt.Sprint(got.Warnings) != fmt.Sprint(want.Warnings) {
		return fmt.Sprintf("warnings %v, want %v", got.Warnings, want.Warnings)
	}
	if len(got.Bits) != len(want.Bits) {
		return fmt.Sprintf("%d rows, want %d", len(got.Bits), len(want.Bits))
	}
	decode := func(h string) float64 {
		u, err := strconv.ParseUint(h, 16, 64)
		if err != nil {
			return math.NaN()
		}
		return math.Float64frombits(u)
	}
	scale := 0.0
	for _, h := range want.Bits {
		if h != "NaN" {
			scale = math.Max(scale, math.Abs(decode(h)))
		}
	}
	for i := range want.Bits {
		g, w := got.Bits[i], want.Bits[i]
		if g == w {
			continue
		}
		if exact || g == "NaN" || w == "NaN" {
			return fmt.Sprintf("row %d: bits %s, want %s", i, g, w)
		}
		gv, wv := decode(g), decode(w)
		if !(math.Abs(gv-wv) <= fittedCrossArchTol*(1+scale)) {
			return fmt.Sprintf("row %d: %.17g, want %.17g (beyond %g of scale %g)", i, gv, wv, fittedCrossArchTol, scale)
		}
	}
	return ""
}

// liveFitOn runs c's fit_on request under weight in THIS process: the
// bitwise reference every same-process equality compares against (the
// stored golden is cross-architecture and pinned by FitOnGolden).
func liveFitOn(t *testing.T, c fittedGoldenCase, weight string) fittedGoldenEntry {
	t.Helper()
	return runFittedGolden(t, c, func() *types.Request { return fittedGoldenRequest(c, weight, true, nil) })
}

// TestFittedState_FitOnGolden pins every fitted attribute's per-row
// score bits on the fit_on path (written before the fitted-state
// refactor): the refactor moved the models onto a prebuilt state and
// every existing result kept its bits. An archExact configuration is
// pinned to the bit on every architecture; the rest to the bit on the
// capturing architecture and within fittedCrossArchTol elsewhere (see
// fittedGoldenDiff) — the bitwise refactor proof for those is
// TestFittedState_PrebuiltMatchesFitOn's same-process equality.
func TestFittedState_FitOnGolden(t *testing.T) {
	got := map[string]fittedGoldenEntry{}
	exact := map[string]bool{}
	scored := 0
	for _, c := range fittedGoldenCases() {
		for _, w := range c.weights {
			t.Run(fittedGoldenKey(c, w), func(t *testing.T) {
				e := runFittedGolden(t, c, func() *types.Request { return fittedGoldenRequest(c, w, true, nil) })
				for _, b := range e.Bits {
					if b != "NaN" {
						scored++
						break
					}
				}
				got[fittedGoldenKey(c, w)] = e
				exact[fittedGoldenKey(c, w)] = c.archExact
			})
		}
	}
	if *updateFittedGolden {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		raw, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(fittedGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fittedGoldenPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d entries", len(keys))
		return
	}
	want := readFittedGolden(t)
	if len(want) != len(got) {
		t.Fatalf("golden has %d entries, run produced %d", len(want), len(got))
	}
	if scored < len(got)/2 {
		t.Fatalf("vacuous golden: only %d of %d runs scored a row", scored, len(got))
	}
	for k, g := range got {
		w, ok := want[k]
		if !ok {
			t.Errorf("%s: not in the golden", k)
			continue
		}
		if d := fittedGoldenDiff(exact[k], g, w); d != "" {
			t.Errorf("%s: score bits moved from the golden: %s\n got %v\nwant %v", k, d, g, w)
		}
	}
}

// captureFittedState folds c's fit_on request (under weight) through
// the real factory and bind, buffered over the fixture, and returns the
// closed fit as a model.VectorFit named name (AttributeVectorFit).
func captureFittedState(t *testing.T, c fittedGoldenCase, weight, name string) *model.VectorFit {
	t.Helper()
	schema, recs := hotellingFixture(t, c.fixture, c.split)
	p := NewProcessor(schema)
	req := p.prepare(fittedGoldenRequest(c, weight, true, nil))
	attr := req.Attributes[0]
	factory, ok := p.exts.LookupAttribute(attr.Type)
	if !ok {
		t.Fatalf("%s not registered", attr.Type)
	}
	comp, err := factory(attr, schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.bindAttribute(comp); err != nil {
		t.Fatal(err)
	}
	if _, ok := AttributeVectorFit(comp, name); ok {
		t.Fatal("a fit was captured before Finalize")
	}
	if _, err := comp.Compute(recs, "s"); err != nil {
		t.Fatal(err)
	}
	vf, ok := AttributeVectorFit(comp, name)
	if !ok {
		t.Fatalf("%s: no fit captured", c.name)
	}
	return vf
}

// reversedFit is vf with its axes stored in reverse order (labels,
// members, mean and co-moment permuted together) — the same fit, laid
// out differently.
func reversedFit(vf *model.VectorFit) *model.VectorFit {
	p := vf.P()
	out := *vf
	out.Labels, out.Members, out.Mean = make([]string, p), make([]string, p), make([]float64, p)
	out.M2 = make([][]float64, p)
	for i := 0; i < p; i++ {
		r := p - 1 - i
		out.Labels[i], out.Members[i], out.Mean[i] = vf.Labels[r], vf.Members[r], vf.Mean[r]
		out.M2[i] = make([]float64, p)
		for j := 0; j < p; j++ {
			out.M2[i][j] = vf.M2[r][p-1-j]
		}
	}
	return &out
}

func modelSetOf(t *testing.T, fits ...*model.VectorFit) *model.Set {
	t.Helper()
	set := &model.Set{}
	for _, vf := range fits {
		if err := set.AddVector(vf); err != nil {
			t.Fatal(err)
		}
	}
	return set
}

// TestFittedState_PrebuiltMatchesFitOn is the prebuilt-state contract:
// every fitted attribute configuration's fit, captured from its fit_on
// fold and bound back through params.fit — on a request with NO weight
// (the fit's own basis governs) and NO fit_on — scores every row to the
// bit of the live fit_on run in the same process, on both arms, with
// the same warnings; a fit stored in a different axis order aligns by
// label to the same bits. Same-process, so bitwise on every
// architecture (the stored golden is FitOnGolden's business).
func TestFittedState_PrebuiltMatchesFitOn(t *testing.T) {
	for _, c := range fittedGoldenCases() {
		for _, w := range c.weights {
			t.Run(fittedGoldenKey(c, w), func(t *testing.T) {
				want := liveFitOn(t, c, w)
				vf := captureFittedState(t, c, w, "pop")
				fitParams := map[string]any{"fit": "pop"}
				for name, bound := range map[string]*model.VectorFit{"as captured": vf, "reversed": reversedFit(vf)} {
					set := modelSetOf(t, bound)
					got := runFittedGoldenWith(t, c, set, func() *types.Request {
						req := fittedGoldenRequest(c, "none", false, fitParams)
						return req
					})
					if fmt.Sprint(got) != fmt.Sprint(want) {
						t.Fatalf("%s: params.fit scores differ from the live fit_on run\n got %v\nwant %v", name, got, want)
					}
				}
			})
		}
	}
}

// TestFittedState_SwappedStateMoves is the falsification arm of the
// equality above: a bound state whose mean or co-moment is perturbed
// (swapped between members, labels kept) no longer reproduces the
// fit_on scores — the equality reads the state, not the rows.
func TestFittedState_SwappedStateMoves(t *testing.T) {
	for _, c := range fittedGoldenCases()[:4] {
		want := liveFitOn(t, c, "frequency")
		for name, mut := range map[string]func(*model.VectorFit){
			"mean": func(v *model.VectorFit) { v.Mean[0], v.Mean[1] = v.Mean[1], v.Mean[0] },
			"m2": func(v *model.VectorFit) {
				v.M2[0][0], v.M2[1][1] = v.M2[1][1], v.M2[0][0]
			},
			"mean_and_m2": func(v *model.VectorFit) {
				v.Mean, v.M2[0] = append([]float64(nil), v.M2[0]...), append([]float64(nil), v.Mean...)
			},
		} {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				vf := captureFittedState(t, c, "frequency", "pop")
				vf.M2 = append([][]float64(nil), vf.M2...)
				for i := range vf.M2 {
					vf.M2[i] = append([]float64(nil), vf.M2[i]...)
				}
				mut(vf)
				got := runFittedGoldenWith(t, c, modelSetOf(t, vf), func() *types.Request {
					return fittedGoldenRequest(c, "none", false, map[string]any{"fit": "pop"})
				})
				if fmt.Sprint(got) == fmt.Sprint(want) {
					t.Fatalf("a %s-perturbed state still reproduces the fit_on scores: the equality test cannot bite", name)
				}
			})
		}
	}
}

// TestFittedState_FitRefusals: params.fit is judged before any record —
// fit beside fit_on and an empty name are SERVICE_VALIDATION param
// refusals (reasons fit_and_fit_on / bad_fit), on every fitted type; an
// unbound name is PULSE_MODEL_UNKNOWN, a regression's
// PULSE_MODEL_KIND_MISMATCH; a fit whose
// labels are not the slot's is PULSE_VECTOR_REFERENCE_MISMATCH naming
// the fit; fit on a row-local comparison metric is refused.
func TestFittedState_FitRefusals(t *testing.T) {
	schema, _ := hotellingFixture(t, "attitude", "")
	fields := []string{"rating", "complaints", "privileges", "learning"}
	vf := &model.VectorFit{Name: "pop", Labels: []string{"rating", "complaints", "privileges", "raises"}, Members: fields,
		Basis: model.VectorBasisUnweighted, N: 10, NMass: 10, SumWeights: 10, SumWeightsSq: 10,
		Mean: make([]float64, 4), M2: [][]float64{{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 1, 0}, {0, 0, 0, 1}}}
	set := modelSetOf(t, vf)
	if err := set.Add(&model.Bound{Name: "reg", Type: types.REG_OLS}); err != nil {
		t.Fatal(err)
	}
	base := map[types.AttributeType]map[string]any{
		types.ATTR_MAHALANOBIS:  {},
		types.ATTR_PC_SCORE:     {"component": 1},
		types.ATTR_FACTOR_SCORE: {"factor": 1, "factors": 1},
		types.ATTR_VEC_DISTANCE: {"metric": "mahalanobis", "reference": []float64{0, 0, 0, 0}},
	}
	run := func(typ types.AttributeType, extra map[string]any) error {
		params := map[string]any{}
		for k, v := range base[typ] {
			params[k] = v
		}
		for k, v := range extra {
			params[k] = v
		}
		raw, _ := json.Marshal(params)
		req := &types.Request{Attributes: []*types.Attribute{{Type: typ, Label: "s", Fields: fields, Params: raw}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "rating", Label: "n"}}}
		p := NewProcessor(schema)
		p.SetModels(set)
		_, recs := hotellingFixture(t, "attitude", "")
		_, err := p.Process(context.Background(), req, NewSliceIterator(recs))
		return err
	}
	codeOf := func(err error) (errors.Code, map[string]any) {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			return "", nil
		}
		return ce.Code, ce.Details
	}
	for typ := range base {
		t.Run(string(typ), func(t *testing.T) {
			fitOn := []map[string]any{{"type": "FILTER_INCLUDE", "field": "grp", "values": []string{"all"}}}
			if code, d := codeOf(run(typ, map[string]any{"fit": "pop", "fit_on": fitOn})); code != errors.SERVICE_VALIDATION || d["reason"] != "fit_and_fit_on" {
				t.Fatalf("fit + fit_on: %v %v", code, d)
			}
			if code, d := codeOf(run(typ, map[string]any{"fit": "pop", "fit_on": "filtered"})); code != errors.SERVICE_VALIDATION || d["reason"] != "fit_and_fit_on" {
				t.Fatalf("fit + fit_on filtered: %v %v", code, d)
			}
			if code, d := codeOf(run(typ, map[string]any{"fit": ""})); code != errors.SERVICE_VALIDATION || d["reason"] != "bad_fit" {
				t.Fatalf("empty fit: %v %v", code, d)
			}
			if code, d := codeOf(run(typ, map[string]any{"fit": "nope"})); code != errors.PULSE_MODEL_UNKNOWN || d["attribute"] != "s" {
				t.Fatalf("unknown fit: %v %v", code, d)
			}
			if code, d := codeOf(run(typ, map[string]any{"fit": "reg"})); code != errors.PULSE_MODEL_KIND_MISMATCH || d["fit"] != "reg" || d["kind"] != "regression" || d["attribute"] != "s" {
				t.Fatalf("regression fit: %v %v", code, d)
			}
			code, d := codeOf(run(typ, map[string]any{"fit": "pop"}))
			if code != errors.PULSE_VECTOR_REFERENCE_MISMATCH || d["fit"] != "pop" || d["reason"] != vectors.ReferenceReasonLabelsDiffer {
				t.Fatalf("label mismatch: %v %v", code, d)
			}
			if !reflect.DeepEqual(d["missing"], []string{"learning"}) || !reflect.DeepEqual(d["extra"], []string{"raises"}) {
				t.Fatalf("label mismatch details: %v", d)
			}
			// params.labels over inline fields realigns the slot.
			if err := run(typ, map[string]any{"fit": "pop", "labels": vf.Labels}); err != nil {
				t.Fatalf("labels realigned: %v", err)
			}
		})
	}
	if code, d := codeOf(run(types.ATTR_VEC_DISTANCE, map[string]any{"fit": "pop", "metric": "euclidean"})); code != errors.SERVICE_VALIDATION || d["reason"] != "fit_on_row_local" {
		t.Fatalf("fit on a row-local metric: %v %v", code, d)
	}
}

// TestFittedState_NamedVectorLabels: a slot over a NAMED vector aligns a
// bound fit to the vector's labels (the literal-member rewrite writes
// them out), so a fit captured under those labels scores to the bit of
// its fit_on fold, and params.labels beside a named vector is refused.
func TestFittedState_NamedVectorLabels(t *testing.T) {
	c := fittedGoldenCases()[0] // maha_d2_attitude
	want := liveFitOn(t, c, "none")
	vf := captureFittedState(t, c, "none", "pop")
	labels := []string{"Rating", "Complaints", "Privileges", "Learning"}
	vf.Labels = append([]string(nil), labels...)
	vf = reversedFit(vf)
	got := runFittedGoldenWith(t, c, modelSetOf(t, vf), func() *types.Request {
		return &types.Request{
			Vectors:    []types.VectorSpec{{Name: "battery", Fields: c.fields, Labels: labels}},
			Attributes: []*types.Attribute{{Type: c.typ, Label: "s", Vector: "battery", Params: json.RawMessage(`{"fit":"pop"}`)}},
		}
	})
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("named-vector fit scores differ from the live fit_on run\n got %v\nwant %v", got, want)
	}
	if _, ce := vectors.ResolveAttributes(&types.Request{
		Vectors:    []types.VectorSpec{{Name: "battery", Fields: c.fields, Labels: labels}},
		Attributes: []*types.Attribute{{Type: c.typ, Label: "s", Vector: "battery", Params: json.RawMessage(`{"fit":"pop","labels":["a","b","c","d"]}`)}},
	}, fixtureSchema(t, c.fixture), nil, nil); ce == nil || ce.Details["reason"] != "labels_on_vector" {
		t.Fatalf("labels beside a named vector: %v", ce)
	}
}

func fixtureSchema(t *testing.T, fixture string) *encoding.Schema {
	t.Helper()
	schema, _ := hotellingFixture(t, fixture, "")
	return schema
}

// TestFittedState_BoundIsRowLocal: a fitted slot bound to a prebuilt
// fit takes no pre-pass. Beside a two-pass ATTR_ZSCORE that reads its
// label, the streaming two-pass drive plans ONE prepass layer (the
// bound slot is a row-local input, available on scan 0) by both the
// routing predicate and the built computers, where the same slot with
// fit_on plans two; the z-scores equal the buffered arm's bit for bit.
func TestFittedState_BoundIsRowLocal(t *testing.T) {
	c := fittedGoldenCases()[0] // maha_d2_attitude
	set := modelSetOf(t, captureFittedState(t, c, "none", "pop"))
	build := func(bound bool) *types.Request {
		req := fittedGoldenRequest(c, "none", !bound, nil)
		if bound {
			req = fittedGoldenRequest(c, "none", false, map[string]any{"fit": "pop"})
		}
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_ZSCORE, Field: "s", Label: "z"})
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "z", Label: "sum_z"}}
		return req
	}
	schema, _ := hotellingFixture(t, c.fixture, c.split)
	for _, bound := range []bool{true, false} {
		want := map[bool]int{true: 1, false: 2}[bound]
		p := NewProcessor(schema)
		p.SetModels(set)
		req := p.prepare(build(bound))
		if got := planTwoPassLayers(req.Attributes, p.exts).layerCount(); got != want {
			t.Fatalf("bound=%v: routing plan has %d prepass layers, want %d", bound, got, want)
		}
		_, plan, err := p.buildTwoPassStages(req.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		if got := plan.layerCount(); got != want {
			t.Fatalf("bound=%v: built plan has %d prepass layers, want %d", bound, got, want)
		}
	}
	var runs [2][]float64
	for i, forceBuffered := range []bool{false, true} {
		_, recs := hotellingFixture(t, c.fixture, c.split)
		req := build(true)
		if forceBuffered {
			req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "z", Label: "med_z"})
		}
		p := NewProcessor(schema)
		p.SetModels(set)
		if _, err := p.Process(context.Background(), req, NewSliceIterator(recs)); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]ProcessPath{false: PathStreaming, true: PathBuffered}[forceBuffered]; p.LastPath() != want {
			t.Fatalf("ran %v, want %v", p.LastPath(), want)
		}
		for _, r := range recs {
			v, ok := r.NumericValue("z")
			if !ok {
				v = math.NaN()
			}
			runs[i] = append(runs[i], v)
		}
	}
	if !sameBits(runs[0], runs[1]) {
		t.Fatalf("streaming two-pass z over a bound slot differs from buffered:\n%v\n%v", runs[0], runs[1])
	}
}
