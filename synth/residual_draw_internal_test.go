package synth

import (
	"bytes"
	"math"
	mrand "math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// The determinism criterion this story carries is a property of the
// component ASSIGNMENT, and the end-to-end byte-identity tests
// (synth/residual_draw_test.go) can only observe it indirectly — they
// prove the output did not move, not that it did not move for the right
// reason. These unit tests read the assignment directly.

// drawersNamed builds compiled-drawer stand-ins in the given order.
// buildResidualCorrelator reads only the field name and stamps only the
// component index, so a full compile through buildModelDrawers would add
// fixture weight without adding coverage.
func drawersNamed(names ...string) []*modelDrawer {
	out := make([]*modelDrawer, len(names))
	for i, n := range names {
		out[i] = &modelDrawer{field: n, order: i, residual: -1}
	}
	return out
}

// TestBuildResidualCorrelator_ComponentOrderIsDrawerOrder pins the one
// choice the whole determinism argument rests on: components are
// assigned in DRAWER order (which buildModelDrawers has already fixed to
// schema field order), never in the order the correlation list names its
// pairs.
//
// The list below names the participants in reverse and with endpoints
// swapped — the same matrix by symmetry, a different serialization — and
// must produce the same assignment.
func TestBuildResidualCorrelator_ComponentOrderIsDrawerOrder(t *testing.T) {
	drawers := drawersNamed("alpha", "beta", "gamma")
	rc, warnings, err := buildResidualCorrelator([]CorrelationSpec{
		{A: "gamma", B: "beta", Correlation: 0.2},
		{A: "gamma", B: "alpha", Correlation: 0.3},
		{A: "beta", B: "alpha", Correlation: 0.4},
	}, drawers)
	if err != nil {
		t.Fatalf("buildResidualCorrelator: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("a fully supplied matrix warned: %v", warnings)
	}
	want := []string{"alpha", "beta", "gamma"}
	for i, n := range want {
		if rc.fields[i] != n {
			t.Fatalf("component %d is %q, want %q — order followed the correlation list", i, rc.fields[i], n)
		}
		if drawers[i].residual != i {
			t.Fatalf("drawer %q stamped with component %d, want %d", drawers[i].field, drawers[i].residual, i)
		}
	}
}

// TestBuildResidualCorrelator_SkipsUnmodelledEndpoints: a correlation
// naming a field no drawer produced cannot be honoured, and the pair
// must be dropped WITHOUT claiming a component — an index handed to
// nothing would silently shift every later participant's draw.
func TestBuildResidualCorrelator_SkipsUnmodelledEndpoints(t *testing.T) {
	drawers := drawersNamed("alpha", "beta", "gamma")
	rc, warnings, err := buildResidualCorrelator([]CorrelationSpec{
		{A: "alpha", B: "ghost", Correlation: 0.5},
		{A: "beta", B: "gamma", Correlation: 0.5},
	}, drawers)
	if err != nil {
		t.Fatalf("buildResidualCorrelator: %v", err)
	}
	if len(rc.fields) != 2 || rc.fields[0] != "beta" || rc.fields[1] != "gamma" {
		t.Fatalf("participants = %v, want [beta gamma]", rc.fields)
	}
	if drawers[0].residual != -1 {
		t.Errorf("alpha claimed component %d despite its only pair being dropped", drawers[0].residual)
	}
	var named bool
	for _, w := range warnings {
		if len(w) > 0 && containsAll(w, "ghost", "no surviving linear model") {
			named = true
		}
	}
	if !named {
		t.Errorf("the dropped endpoint was not named; warnings = %v", warnings)
	}
}

// TestBuildResidualCorrelator_NilBelowTwoParticipants: one participant
// is a 1x1 matrix whose only content is the scalar 1. Returning a
// correlator for it would consume an RNG draw per row to produce a plain
// standard normal the drawer would have drawn for itself, which is the
// difference between "inert" and "byte-identical".
func TestBuildResidualCorrelator_NilBelowTwoParticipants(t *testing.T) {
	drawers := drawersNamed("alpha", "beta")
	rc, _, err := buildResidualCorrelator([]CorrelationSpec{
		{A: "alpha", B: "ghost", Correlation: 0.5},
	}, drawers)
	if err != nil {
		t.Fatalf("buildResidualCorrelator: %v", err)
	}
	if rc != nil {
		t.Fatalf("built a correlator over %v; want nil below two participants", rc.fields)
	}
	for _, d := range drawers {
		if d.residual != -1 {
			t.Errorf("drawer %q was stamped with component %d despite no correlator", d.field, d.residual)
		}
	}
	// The nil correlator must be safe to call unconditionally — drawRow
	// does exactly that rather than branching.
	rc.draw(mrand.New(mrand.NewPCG(1, 2)))
}

// TestResidualCorrelator_DrawRealizesRequestedCorrelation is the
// arithmetic check under the statistical ones: the shared vector's
// components must actually carry the requested correlation, and each
// must be marginally standard normal (a Cholesky factor whose rows do
// not have unit norm would correlate correctly and rescale every
// residual, which no marginal assertion downstream would notice for a
// `normal` target).
func TestResidualCorrelator_DrawRealizesRequestedCorrelation(t *testing.T) {
	const target = 0.7
	rc, _, err := buildResidualCorrelator(
		[]CorrelationSpec{{A: "alpha", B: "beta", Correlation: target}},
		drawersNamed("alpha", "beta"))
	if err != nil {
		t.Fatalf("buildResidualCorrelator: %v", err)
	}
	rng := mrand.New(mrand.NewPCG(7, 9))
	const n = 50000
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i := 0; i < n; i++ {
		rc.draw(rng)
		xs[i] = rc.component(0)
		ys[i] = rc.component(1)
	}
	if got := corrOf(xs, ys); math.Abs(got-target) > 0.02 {
		t.Errorf("component correlation = %.4f, want within 0.02 of %.2f", got, target)
	}
	for i, col := range [][]float64{xs, ys} {
		m, sd := momentsOf(col)
		if math.Abs(m) > 0.03 {
			t.Errorf("component %d mean = %.4f, want ~0", i, m)
		}
		if math.Abs(sd-1) > 0.03 {
			t.Errorf("component %d std = %.4f, want ~1 — the factor rescaled the residual", i, sd)
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func momentsOf(x []float64) (mean, std float64) {
	for _, v := range x {
		mean += v
	}
	mean /= float64(len(x))
	for _, v := range x {
		std += (v - mean) * (v - mean)
	}
	return mean, math.Sqrt(std / float64(len(x)-1))
}

func corrOf(a, b []float64) float64 {
	ma, sa := momentsOf(a)
	mb, sb := momentsOf(b)
	cov := 0.0
	for i := range a {
		cov += (a[i] - ma) * (b[i] - mb)
	}
	cov /= float64(len(a) - 1)
	return cov / (sa * sb)
}

// TestSpecFromProfile_TranslatesMeasuredResidualPairsOnly is the wiring
// half E3-S1 deliberately left undone: the captured section now reaches
// the Spec, and the MEASURED / UNMEASURED distinction the capture went
// to some length to preserve has to survive the translation.
//
// An unmeasured pair must reach Spec.ResidualCorrelations as nothing at
// all. Writing it out as a zero would present a gap as a measurement,
// and would do so irreversibly — factorCorrelations counts a supplied
// zero as supplied, so the "completed by assumption" line that is the
// reader's only remaining signal would fall silent too.
func TestSpecFromProfile_TranslatesMeasuredResidualPairsOnly(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{
		IncludeStats:            true,
		FitModels:               true,
		FitResidualCorrelations: true,
		TopK:                    8,
		Seed:                    5,
	})
	if prof.ResidualCorrelations == nil || len(prof.ResidualCorrelations.Pairs) == 0 {
		t.Fatal("fixture captured no measured residual pairs; nothing below proves anything")
	}

	spec, _ := SpecFromProfile(prof, 100)
	modelled := make(map[string]bool, len(spec.Models))
	for _, m := range spec.Models {
		modelled[m.Field] = true
	}

	want := 0
	for _, p := range prof.ResidualCorrelations.Pairs {
		if modelled[p.A] && modelled[p.B] {
			want++
		}
	}
	if want == 0 {
		t.Fatal("no captured pair survived to a modelled spec target; the fixture proves nothing")
	}
	if len(spec.ResidualCorrelations) != want {
		t.Fatalf("spec carries %d residual correlations, want %d", len(spec.ResidualCorrelations), want)
	}
	for _, gap := range prof.ResidualCorrelations.Unmeasured {
		for _, got := range spec.ResidualCorrelations {
			if (got.A == gap.A && got.B == gap.B) || (got.A == gap.B && got.B == gap.A) {
				t.Errorf("unmeasured pair (%s, %s) was translated into a measured rho of %g",
					gap.A, gap.B, got.Correlation)
			}
		}
	}
	for _, got := range spec.ResidualCorrelations {
		if !modelled[got.A] || !modelled[got.B] {
			t.Errorf("residual correlation (%s, %s) names a field with no model on the spec", got.A, got.B)
		}
	}
}

// TestSpecFromProfile_NoResidualSectionLeavesSpecUntouched: a document
// captured without --residual-correlations — which is every document
// written before the flag existed — must produce a spec with no residual
// structure at all, so generation draws every residual independently
// exactly as it did.
func TestSpecFromProfile_NoResidualSectionLeavesSpecUntouched(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{
		IncludeStats: true,
		FitModels:    true,
		TopK:         8,
		Seed:         5,
	})
	if prof.ResidualCorrelations != nil {
		t.Fatal("the fixture captured a residual section without the flag")
	}
	spec, _ := SpecFromProfile(prof, 100)
	if len(spec.Models) == 0 {
		t.Fatal("the fixture landed no models; the assertion below would be vacuous")
	}
	if spec.ResidualCorrelations != nil {
		t.Fatalf("a residual-free document produced %d residual correlations", len(spec.ResidualCorrelations))
	}
}

// decodeCohort reads every record into per-field columns: numerics as
// float64, dictionary-bearing fields resolved to their level text. The
// end-to-end test needs BOTH from one cohort (a residual is a value
// minus a prediction, and the prediction reads a categorical level), and
// the package-external helpers that do each half separately are not
// reachable from here.
func decodeCohort(t *testing.T, data []byte) (nums map[string][]float64, cats map[string][]string) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	nums, cats = map[string][]float64{}, map[string][]string{}
	rr := encoding.NewRecordReader(r, schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		for i := range schema.Fields {
			f := &schema.Fields[i]
			if f.Dictionary != nil {
				cats[f.Name] = append(cats[f.Name], f.Dictionary.Resolve(uint32(values[f.Name])))
				continue
			}
			nums[f.Name] = append(nums[f.Name], values[f.Name])
		}
	}
	return nums, cats
}

// modelResiduals recovers each row's residual for one modelled field by
// evaluating the spec's own model against the drawn row and subtracting.
// Only categorical_level terms are evaluated because the fixture carries
// only those; a set_option term would silently contribute nothing here
// and is refused rather than mis-scored.
func modelResiduals(t *testing.T, m FieldModelSpec, nums map[string][]float64, cats map[string][]string) []float64 {
	t.Helper()
	values := nums[m.Field]
	out := make([]float64, len(values))
	for i, v := range values {
		pred := m.Intercept
		for _, p := range m.Predictors {
			if p.Kind != ModelPredictorCategoricalLevel {
				t.Fatalf("fixture grew a %q predictor this helper cannot evaluate", p.Kind)
			}
			col, ok := cats[p.Field]
			if !ok {
				t.Fatalf("predictor field %q is not a categorical column in the generated cohort", p.Field)
			}
			if col[i] == p.Level {
				pred += p.Coefficient
			}
		}
		out[i] = v - pred
	}
	return out
}

// TestResidualCorrelations_CapturedTargetIsRealizedEndToEnd closes the
// loop the story is actually about: a cohort whose residuals carry
// structure its raw values do not is captured, translated and
// regenerated, and the structure has to come back out.
//
// The fixture is the one the capture tests use, chosen because it
// separates the two quantities by construction — alpha and beta share a
// predictor AND share residual noise at rho ~ 0.8, while gamma shares
// neither. A generator that mistook the RAW correlation for the residual
// one would overshoot on (alpha, beta); one that ignored the section
// would land at ~0. Both are far outside the tolerance below.
//
// gamma is the documented boundary of this feature and is asserted as
// such rather than worked around. Nothing explains it, so selection
// emits a ZERO-PREDICTOR model for it — which E3-S1 deliberately admits
// as a capture participant (it still has a residual: its whole deviation
// from its own mean) but which modelSpecFromProfile drops at translation
// under E2-S1's rule, because a field nothing explains is better served
// by its captured conditional pair than by an empty model. So its
// captured residual pairs have no model to attach to at generation time
// and must reach the spec as nothing at all. That is a real loss of
// reach — on a wide cohort roughly half the modelled targets carry no
// predictor — and it is recorded here so the next reader meets it as a
// decision rather than as a surprise.
func TestResidualCorrelations_CapturedTargetIsRealizedEndToEnd(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{
		IncludeStats:            true,
		FitModels:               true,
		FitResidualCorrelations: true,
		TopK:                    8,
		Seed:                    5,
	})
	if prof.ResidualCorrelations == nil {
		t.Fatal("the fixture captured no residual submatrix")
	}
	targetAB, ok := residualPair(prof.ResidualCorrelations, "alpha", "beta")
	if !ok {
		t.Fatalf("alpha x beta was not measured: %+v", prof.ResidualCorrelations)
	}
	if math.Abs(targetAB.Rho) < 0.5 {
		t.Fatalf("the fixture's captured rho is %.3f; too weak to distinguish applied from ignored", targetAB.Rho)
	}
	if _, ok := residualPair(prof.ResidualCorrelations, "alpha", "gamma"); !ok {
		t.Fatalf("alpha x gamma was not measured; the boundary assertion below would be vacuous")
	}

	spec, warnings := SpecFromProfile(prof, 20000)
	if len(spec.ResidualCorrelations) != 1 {
		t.Fatalf("spec carries %d residual correlations, want exactly the one whose endpoints both kept a model; SpecFromProfile warnings = %v",
			len(spec.ResidualCorrelations), warnings)
	}
	byField := make(map[string]FieldModelSpec, len(spec.Models))
	for _, m := range spec.Models {
		byField[m.Field] = m
	}
	if _, ok := byField["gamma"]; ok {
		t.Fatal("gamma kept a zero-predictor model; the boundary this test documents has moved and its comment is now wrong")
	}
	for _, f := range []string{"alpha", "beta"} {
		if _, ok := byField[f]; !ok {
			t.Fatalf("%q landed no model; SpecFromProfile warnings = %v", f, warnings)
		}
	}

	data, res, err := SynthBytes(spec, Options{Seed: 13})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Logf("generation warnings: %v", res.Warnings)
	}
	nums, cats := decodeCohort(t, data)
	ra := modelResiduals(t, byField["alpha"], nums, cats)
	rb := modelResiduals(t, byField["beta"], nums, cats)

	if got := corrOf(ra, rb); math.Abs(got-targetAB.Rho) > 0.05 {
		t.Errorf("regenerated residual corr(alpha, beta) = %.4f, want within 0.05 of the captured %.4f",
			got, targetAB.Rho)
	}
}
