package sem

import (
	stderrors "errors"
	"math"
	"os"
	"strings"
	"testing"
)

// fitInvariance fits the brand-survey model named by a lavaan
// multi-group reference with the reference's group.equal /
// group.partial, the groups marked as given.
func fitInvariance(t *testing.T, refName, data, model string, reference int) (*GroupFit, lavaanGroupRef) {
	t.Helper()
	ref := loadLavaanGroupRef(t, "testdata/"+refName+"_lavaan.json")
	desc, err := os.ReadFile("testdata/" + model + "_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewGroupModel(string(desc), ModelOptions{GroupEqual: ref.Options.GroupEqual, GroupPartial: ref.Options.GroupPartial})
	if err != nil {
		t.Fatal(err)
	}
	groups := readCSVGroupMoments(t, "testdata/"+data+"_data.csv", "wave", ref.Labels, m.Observed)
	groups[reference].Reference = true
	gf, err := FitGroups(m, groups, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !gf.Converged {
		t.Fatalf("%s: %s", refName, gf.Status)
	}
	return gf, ref
}

func groupRow(t *testing.T, gf *GroupFit, g int, key string) Estimate {
	t.Helper()
	for _, e := range gf.Groups[g].Estimates {
		if e.Key() == key {
			return e
		}
	}
	t.Fatalf("group %d has no row %q", g, key)
	return Estimate{}
}

// TestSEM_ScalarLatentMeansRecoverTruth: the scalar fit (equal loadings
// and intercepts) frees the second wave's latent means, and they recover
// the fixture's true shifts in marker units — value −0.30·0.80 = −0.24,
// service +0.70·0.75 = +0.525, quality 0 — within two standard errors;
// the reference wave's stay fixed at 0.
func TestSEM_ScalarLatentMeansRecoverTruth(t *testing.T) {
	t.Parallel()

	gf, _ := fitInvariance(t, "brand_mg_scalar", "brand", "brand_ms", 0)
	for f, truth := range map[string]float64{"value": -0.24, "service": 0.525, "quality": 0} {
		ref, e := groupRow(t, gf, 0, f+" ~1"), groupRow(t, gf, 1, f+" ~1")
		if ref.Free || ref.Value != 0 {
			t.Errorf("reference wave %s mean: free %v value %g, want fixed 0", f, ref.Free, ref.Value)
		}
		if !e.Free || !(math.Abs(e.Value-truth) <= 2*e.SE) {
			t.Errorf("wave 2 %s mean = %.4f (SE %.4f, free %v), truth %g: want within 2 SE", f, e.Value, e.SE, e.Free, truth)
		}
	}
}

// TestSEM_InvarianceReferenceGroup: the reference group is the one whose
// latent means stay at 0 (lavaan's first group), the others' freed
// under equal intercepts. With the regressions equal too, marking
// either wave the reference is one model reparameterised: the same
// chi-square, and each wave's freed latent means the negation of the
// other's.
func TestSEM_InvarianceReferenceGroup(t *testing.T) {
	t.Parallel()

	desc, err := os.ReadFile("testdata/brand_ms_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewGroupModel(string(desc), ModelOptions{GroupEqual: []string{"loadings", "intercepts", "regressions"}})
	if err != nil {
		t.Fatal(err)
	}
	fit := func(reference int) *GroupFit {
		groups := readCSVGroupMoments(t, "testdata/brand_data.csv", "wave", []string{"1", "2"}, m.Observed)
		groups[reference].Reference = true
		gf, err := FitGroups(m, groups, Options{})
		if err != nil || !gf.Converged {
			t.Fatalf("reference %d: %v %+v", reference, err, gf)
		}
		return gf
	}
	first, second := fit(0), fit(1)
	if relTol(first.Stats.Chi2, second.Stats.Chi2) > 1e-8 || first.Stats.DoF != second.Stats.DoF {
		t.Errorf("chi2 %g / %g, dof %g / %g: want one model", first.Stats.Chi2, second.Stats.Chi2, first.Stats.DoF, second.Stats.DoF)
	}
	for _, f := range []string{"quality", "value", "service"} {
		a, b := groupRow(t, first, 1, f+" ~1"), groupRow(t, second, 0, f+" ~1")
		if za, zb := groupRow(t, first, 0, f+" ~1"), groupRow(t, second, 1, f+" ~1"); za.Free || zb.Free {
			t.Errorf("%s: the reference wave's mean must stay fixed at 0", f)
		}
		if !a.Free || !b.Free || math.Abs(a.Value+b.Value) > 1e-6 || relTol(a.SE, b.SE) > 1e-4 {
			t.Errorf("%s mean: wave 2 %g (SE %g) under reference 1, wave 1 %g (SE %g) under reference 2: want negated", f, a.Value, a.SE, b.Value, b.SE)
		}
	}
}

// TestSEM_ParseModifiers covers lavaan's per-group modifiers and NA:
// c(...) lists read per group, NA frees a parameter (a first indicator
// too, leaving no marker), a labelled first indicator stays the marker.
func TestSEM_ParseModifiers(t *testing.T) {
	t.Parallel()

	desc := "f =~ x1 + c(a, b)*x2 + c(0.5, NA)*x3\ng =~ NA*x4 + x5 + x6\nh =~ m*x7 + x8\nx1 ~ c(0, NA)*1"
	m, err := NewGroupModel(desc, ModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Arity() != 2 || m.CheckGroupCount(2) != nil || m.CheckGroupCount(3) == nil {
		t.Fatalf("arity %d; CheckGroupCount(2) %v, (3) %v", m.Arity(), m.CheckGroupCount(2), m.CheckGroupCount(3))
	}
	models, err := m.groupModels([]int{0, 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]Estimate{
		{"x2 ~ f": {Free: true, Label: "a"}, "x3 ~ f": {Value: 0.5}, "x1 ~1": {Value: 0}, "x4 ~ g": {Free: true}, "x5 ~ g": {Free: true}, "x7 ~ h": {Value: 1, Label: "m"}},
		{"x2 ~ f": {Free: true, Label: "b"}, "x3 ~ f": {Free: true}, "x1 ~1": {Free: true}, "x4 ~ g": {Free: true}, "x5 ~ g": {Free: true}, "x7 ~ h": {Value: 1, Label: "m"}},
	}
	for g, mg := range models {
		got := map[string]Estimate{}
		for _, e := range SpecifiedRows(mg) {
			got[e.Key()] = e
		}
		for key, w := range want[g] {
			e := got[key]
			var start float64
			for _, p := range mg.params {
				if p.name == e.Param {
					start = p.start
				}
			}
			if e.Free != w.Free || e.Label != w.Label || (!w.Free && start != w.Value) {
				t.Errorf("group %d row %q: free %v label %q start %g, want %+v", g, key, e.Free, e.Label, start, w)
			}
		}
	}
	for d, msg := range map[string]string{
		"f =~ x1 + c(a, b)*x2 + c(1, 2, 3)*x3": "list 2 and 3",
		"f =~ x1 + c(a,,b)*x2":                 "one number, NA or label per group",
		"f =~ x1 + c(a-b, c)*x2":               "one number, NA or label per group",
	} {
		if _, err := NewGroupModel(d, ModelOptions{}); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: err %v, want %q", d, err, msg)
		}
	}
	if _, err := NewModelWithOptions("f =~ x1 + c(a, b)*x2 + x3", ModelOptions{MeanStructure: true}); err == nil || !strings.Contains(err.Error(), "need a multi-group fit") {
		t.Errorf("one group, two-value modifier: err %v", err)
	}
	if _, err := NewModel("f =~ x1 + c(a)*x2 + x3", false); err != nil {
		t.Errorf("a one-value modifier on one group: %v", err)
	}
}

// TestSEM_InvarianceRefusals: an unknown group_equal value, thresholds
// (no ordinal estimator) and a group_partial naming no parameter are
// GroupSpecErrors naming the knob and the entry; FitGroups refuses
// levels the modifiers cannot read and two reference groups.
func TestSEM_InvarianceRefusals(t *testing.T) {
	t.Parallel()

	desc := "f =~ x1 + x2 + x3\ny ~ f"
	for _, c := range []struct {
		opt          ModelOptions
		param, value string
	}{
		{ModelOptions{GroupEqual: []string{"loadings", "weights"}}, "group_equal", "weights"},
		{ModelOptions{GroupEqual: []string{"thresholds"}}, "group_equal", "thresholds"},
		{ModelOptions{GroupEqual: []string{"loadings"}, GroupPartial: []string{"f=~x9"}}, "group_partial", "f=~x9"},
		{ModelOptions{GroupPartial: []string{"x1 ~ 1", "y ~~ x1"}}, "group_partial", "y ~~ x1"},
	} {
		_, err := NewGroupModel(desc, c.opt)
		var ge *GroupSpecError
		if !stderrors.As(err, &ge) || ge.Param != c.param || ge.Value != c.value {
			t.Errorf("%+v: err %v, want a %s refusal of %q", c.opt, err, c.param, c.value)
		}
	}
	if _, err := NewGroupModel(desc, ModelOptions{GroupPartial: []string{"f =~ x2", "x1~1", "f~~f", "y~f", "x2~~x2"}}); err != nil {
		t.Errorf("partial rows lavaan spells (spaces allowed): %v", err)
	}
	m, err := NewGroupModel("f =~ x1 + c(a, b)*x2 + x3", ModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g := []GroupMoments{{Label: "1", Level: 0}, {Label: "2", Level: 0}}
	if _, err := FitGroups(m, g, Options{}); err == nil || !strings.Contains(err.Error(), "distinct position") {
		t.Errorf("two groups at one modifier level: err %v", err)
	}
	g = []GroupMoments{{Label: "1", Level: 0, Reference: true}, {Label: "2", Level: 1, Reference: true}}
	if _, err := FitGroups(m, g, Options{}); err == nil || !strings.Contains(err.Error(), "more than one reference") {
		t.Errorf("two reference groups: err %v", err)
	}
}
