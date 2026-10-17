package banner_test

import (
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/banner"
	"github.com/frankbardon/pulse/types"
)

func group(field string) *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: field} }

func spec() *types.BannerSpec {
	return &types.BannerSpec{
		Request: &types.Request{
			Cohort:    &types.Cohort{Filename: "c.pulse"},
			Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "wave", Values: []string{"w2"}}},
			Overlays:  []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow}},
			Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{group("brand")},
				Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "brand"},
			},
		},
		Sections: []types.BannerSection{
			{Label: "all"},
			{Label: "aware", Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "aware", Values: []string{"yes"}}},
				Rows:  []*types.Group{group("kpi")},
				Cells: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "kpi", Label: "a"}, {Type: types.AGG_COUNT, Field: "kpi2", Label: "b"}}},
		},
		Groups: []types.BannerGroup{
			{Label: "Gender", Columns: []*types.Group{group("gender")}},
			{Label: "Age", Columns: []*types.Group{group("age")}},
		},
		Total:    true,
		Overlays: []types.OverlaySpec{{Name: "sig", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn}},
	}
}

// TestExpandCompose_SlotsAndOverrides pins the expansion: explicit slots
// first, then per section the Total slot and one slot per group, each
// labelled `<section>:<block>`, the section's filterers appended to the
// base's, its rows / cells replacing the base crosstab's, the group's
// columns set, the banner overlays appended to group slots only; and
// every slot a deep copy (mutating one touches neither the spec nor a
// sibling).
func TestExpandCompose_SlotsAndOverrides(t *testing.T) {
	explicit := &types.Request{Label: "x", Cohort: &types.Cohort{Filename: "c.pulse"}}
	sp := spec()
	before, _ := json.Marshal(sp)
	exp, err := banner.ExpandCompose(&types.ComposedRequest{Requests: []*types.Request{explicit}, Banner: sp}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := exp.Composed.Requests
	if exp.Composed.Banner != nil || exp.Explicit != 1 || len(reqs) != 7 || reqs[0] != explicit {
		t.Fatalf("expansion = %+v", exp)
	}
	wantLabels := []string{"x", "all:Total", "all:Gender", "all:Age", "aware:Total", "aware:Gender", "aware:Age"}
	for i, l := range wantLabels {
		if reqs[i].Label != l {
			t.Fatalf("slot %d label %q, want %q", i, reqs[i].Label, l)
		}
	}
	if exp.Banner.Start != 1 || exp.Banner.BaseOverlays != 1 || len(exp.Banner.Sections) != 2 ||
		exp.Banner.Sections[1].Blocks[2] != (banner.Block{Label: "Age", Slot: "aware:Age", Index: 6}) ||
		!exp.Banner.Sections[0].Blocks[0].Total {
		t.Fatalf("layout = %+v", exp.Banner)
	}
	total, gender, awareAge := reqs[1], reqs[2], reqs[6]
	if len(total.Crosstab.Columns) != 0 || len(total.Overlays) != 1 {
		t.Fatalf("Total slot columns %v overlays %d, want none / the base's one", total.Crosstab.Columns, len(total.Overlays))
	}
	if gender.Crosstab.Columns[0].Field != "gender" || len(gender.Overlays) != 2 || gender.Overlays[1].Name != "sig" {
		t.Fatalf("Gender slot = %+v", gender)
	}
	if len(awareAge.Filterers) != 2 || awareAge.Filterers[1].Field != "aware" || awareAge.Crosstab.Rows[0].Field != "kpi" ||
		awareAge.Crosstab.Cell != nil || len(awareAge.Crosstab.Cells) != 2 {
		t.Fatalf("aware:Age slot = %+v / %+v", awareAge, awareAge.Crosstab)
	}
	if gender.Crosstab.Cell == nil || gender.Crosstab.Rows[0].Field != "brand" || len(gender.Filterers) != 1 {
		t.Fatalf("all:Gender keeps the base crosstab: %+v", gender.Crosstab)
	}
	gender.Crosstab.Rows[0].Field = "mutated"
	gender.Filterers[0].Values[0] = "mutated"
	if reqs[3].Crosstab.Rows[0].Field != "brand" || reqs[3].Filterers[0].Values[0] != "w2" {
		t.Fatal("two banner slots share a pointer")
	}
	if after, _ := json.Marshal(sp); string(after) != string(before) {
		t.Fatal("expansion or a slot mutation changed the spec")
	}
}

// TestExpandCompose_PreflightCountsBannerSlots: the slot limit sees
// explicit + sweep + banner slots before any banner slot is built.
func TestExpandCompose_PreflightCountsBannerSlots(t *testing.T) {
	var seen int
	_, err := banner.ExpandCompose(&types.ComposedRequest{
		Requests: []*types.Request{{Cohort: &types.Cohort{Filename: "c.pulse"}}},
		Sweep: &types.SweepSpec{Axes: []types.SweepAxis{{Name: "op", Values: []any{"AGG_SUM", "AGG_MAX"}}},
			Request: json.RawMessage(`{"cohort":{"filename":"c.pulse"},"aggregations":[{"type":"{{op}}","field":"x"}]}`)},
		Banner: spec(),
	}, func(n int) error { seen = n; return stderrors.New("stop") })
	if err == nil || seen != 1+2+6 {
		t.Fatalf("preflight saw %d (err %v), want 9", seen, err)
	}
}

// TestValidate_FirstFault pins the rule order and the details.
func TestValidate_FirstFault(t *testing.T) {
	sp := spec()
	sp.Groups[1].Label = "Gender"
	sp.Sections[0].Label = ""
	ce := banner.Validate(sp)
	if ce == nil || ce.Code != errors.PULSE_BANNER_INVALID || ce.Details["reason"] != banner.ReasonLabelEmpty ||
		ce.Details["field"] != "banner.sections[0].label" {
		t.Fatalf("got %v %+v, want the section label first", ce, ce.Details)
	}
	sp.Sections[0].Label = "all"
	ce = banner.Validate(sp)
	if ce == nil || ce.Details["reason"] != banner.ReasonLabelDuplicate || ce.Details["label"] != "Gender" {
		t.Fatalf("got %+v", ce)
	}
	if banner.Validate(nil) != nil {
		t.Fatal("a nil banner is not valid")
	}
}

// TestCount pins sections × (groups + Total).
func TestCount(t *testing.T) {
	sp := spec()
	if got := banner.Count(sp); got != 6 {
		t.Fatalf("Count = %d, want 6", got)
	}
	sp.Total = false
	if got := banner.Count(sp); got != 4 {
		t.Fatalf("Count = %d, want 4", got)
	}
	if banner.Count(nil) != 0 {
		t.Fatal("nil count")
	}
}

// TestExpandCompose_ScopesLabels: a base `labels` binding rides only the
// banner slots whose crosstab rows or columns group by its field, so no
// Total or other-group slot carries a binding it cannot apply; a binding
// no banner slot groups by stays on every slot (and warns there, as on a
// hand-written slot).
func TestExpandCompose_ScopesLabels(t *testing.T) {
	sp := spec()
	sp.Request.Labels = []*types.LabelBinding{
		{Field: "gender", Table: "g"},
		{Field: "brand", Table: "b"},
		{Field: "kpi", Table: "k"},
		{Field: "nowhere", Table: "n"},
	}
	exp, err := banner.ExpandCompose(&types.ComposedRequest{Banner: sp}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := func(r *types.Request) []string {
		out := []string{}
		for _, b := range r.Labels {
			out = append(out, b.Field)
		}
		return out
	}
	want := map[string][]string{
		"all:Total":    {"brand", "nowhere"},
		"all:Gender":   {"gender", "brand", "nowhere"},
		"all:Age":      {"brand", "nowhere"},
		"aware:Total":  {"kpi", "nowhere"},
		"aware:Gender": {"gender", "kpi", "nowhere"},
		"aware:Age":    {"kpi", "nowhere"},
	}
	for _, r := range exp.Composed.Requests {
		got, w := fields(r), want[r.Label]
		if len(got) != len(w) {
			t.Fatalf("slot %s labels %v, want %v", r.Label, got, w)
		}
		for i := range w {
			if got[i] != w[i] {
				t.Fatalf("slot %s labels %v, want %v", r.Label, got, w)
			}
		}
	}
	if len(sp.Request.Labels) != 4 {
		t.Fatal("scoping changed the spec")
	}
	// No labels: no slot gains a `labels` key.
	exp, err = banner.ExpandCompose(&types.ComposedRequest{Banner: spec()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range exp.Composed.Requests {
		if r.Labels != nil {
			t.Fatalf("slot %s gained labels %v", r.Label, r.Labels)
		}
	}
}

// TestExpandCompose_SectionOverlaysReplace: a section's own overlays
// REPLACE the banner-level overlays on that section's group slots (the
// base request's overlays still lead every slot, the Total slot still
// receives neither), and the layout records each section's overlay list.
func TestExpandCompose_SectionOverlaysReplace(t *testing.T) {
	sp := spec()
	sp.Sections[1].Overlays = []types.OverlaySpec{
		{Name: "mean", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn},
		{Name: "mean2", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn},
	}
	exp, err := banner.ExpandCompose(&types.ComposedRequest{Banner: sp}, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := func(r *types.Request) []string {
		out := []string{}
		for _, o := range r.Overlays {
			out = append(out, o.Name)
		}
		return out
	}
	want := map[string][]string{
		"all:Total":    {""},
		"all:Gender":   {"", "sig"},
		"all:Age":      {"", "sig"},
		"aware:Total":  {""},
		"aware:Gender": {"", "mean", "mean2"},
		"aware:Age":    {"", "mean", "mean2"},
	}
	for _, r := range exp.Composed.Requests {
		got, w := names(r), want[r.Label]
		if len(got) != len(w) {
			t.Fatalf("slot %s overlays %v, want %v", r.Label, got, w)
		}
		for i := range w {
			if got[i] != w[i] {
				t.Fatalf("slot %s overlays %v, want %v", r.Label, got, w)
			}
		}
	}
	all, aware := exp.Banner.Sections[0], exp.Banner.Sections[1]
	if all.OwnOverlays || len(all.Overlays) != 1 || all.Overlays[0].Name != "sig" {
		t.Fatalf("section all overlays = %+v (own %v)", all.Overlays, all.OwnOverlays)
	}
	if !aware.OwnOverlays || len(aware.Overlays) != 2 || aware.Overlays[1].Name != "mean2" {
		t.Fatalf("section aware overlays = %+v (own %v)", aware.Overlays, aware.OwnOverlays)
	}
}
