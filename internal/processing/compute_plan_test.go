package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// planFor builds a returnplan.Plan from raw include / exclude spellings
// over the Response root keys — enough of the resolver for the
// ComputePlanFor mapping, which reads only Visit verdicts.
func planFor(t *testing.T, include, exclude []string) *returnplan.Plan {
	t.Helper()
	parse := func(raw []string) []returnplan.Path {
		out := make([]returnplan.Path, 0, len(raw))
		for _, r := range raw {
			p, err := returnplan.Parse(r)
			if err != nil {
				t.Fatalf("Parse(%q): %v", r, err)
			}
			out = append(out, p)
		}
		return out
	}
	roots := []string{"data", "metadata", "tests", "post_tests", "regressions", "matrices", "warnings", "crosstab", "overlays", "components"}
	inc := parse(include)
	if include == nil {
		inc = parse(roots)
	}
	return returnplan.New("custom", inc, parse(exclude), nil, 0, roots)
}

func TestComputePlanFor(t *testing.T) {
	full := FullComputePlan()
	none := full.WithoutComponents()
	cases := []struct {
		name             string
		include, exclude []string
		want             ComputePlan
	}{
		{name: "nil_plan_is_full", want: full},
		{name: "select_all", include: nil, want: full},
		{name: "exclude_components", exclude: []string{"components"}, want: none},
		{name: "standard_like_allowlist", include: []string{"data", "warnings", "metadata", "crosstab", "overlays"},
			want: func() ComputePlan {
				c := none
				c.MatricesSlot, c.Tests, c.PostTests, c.Regressions = false, false, false, false
				c.MatrixAuxiliary, c.MatrixScalars, c.MatrixVectors = false, false, false
				return c
			}()},
		{name: "exclude_aggregations", exclude: []string{"components.aggregations"},
			want: func() ComputePlan { c := full; c.Aggs, c.Groups = false, false; return c }()},
		{name: "exclude_groups_only", exclude: []string{"components.aggregations[*].groups"},
			want: func() ComputePlan { c := full; c.Groups = false; return c }()},
		{name: "exclude_groupers", exclude: []string{"components.groupers"},
			want: func() ComputePlan { c := full; c.Groupers = false; return c }()},
		{name: "exclude_filterers", exclude: []string{"components.filterers"},
			want: func() ComputePlan { c := full; c.Filterers = false; return c }()},
		{name: "exclude_run", exclude: []string{"components.run"},
			want: func() ComputePlan { c := full; c.Run = false; return c }()},
		{name: "exclude_component_matrices", exclude: []string{"components.matrices"},
			want: func() ComputePlan { c := full; c.Matrices = false; return c }()},
		// The matrices slot takes its sub-parts with it; each sub-part
		// alone leaves the slot (and its fold) computed.
		{name: "exclude_matrices_slot", exclude: []string{"matrices"},
			want: func() ComputePlan {
				c := full
				c.MatricesSlot, c.MatrixAuxiliary, c.MatrixScalars, c.MatrixVectors = false, false, false, false
				return c
			}()},
		{name: "exclude_matrix_auxiliary", exclude: []string{"matrices[*].auxiliary"},
			want: func() ComputePlan { c := full; c.MatrixAuxiliary = false; return c }()},
		{name: "exclude_matrix_scalars", exclude: []string{"matrices[*].scalars"},
			want: func() ComputePlan { c := full; c.MatrixScalars = false; return c }()},
		{name: "exclude_matrix_vectors", exclude: []string{"matrices[*].vectors"},
			want: func() ComputePlan { c := full; c.MatrixVectors = false; return c }()},
		{name: "include_matrix_primary", include: []string{"matrices[*].primary"},
			want: ComputePlan{MatricesSlot: true}},
		{name: "exclude_aux_margins", exclude: []string{"components.crosstab.row_margin_aggregations", "components.crosstab.column_margin_aggregations", "components.crosstab.grand_total_aggregations"},
			want: func() ComputePlan { c := full; c.AuxMargins = false; return c }()},
		// A narrow include below a sub-part still computes the sub-part
		// (its slot cannot be built in halves) — and nothing else.
		{name: "narrow_include_keeps_parent", include: []string{"data", "components.aggregations[*].n"},
			want: ComputePlan{Aggs: true}},
		{name: "include_one_aux_margin", include: []string{"components.crosstab.grand_total_aggregations"},
			want: ComputePlan{Crosstab: true, AuxMargins: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p *returnplan.Plan
			if tc.name != "nil_plan_is_full" {
				p = planFor(t, tc.include, tc.exclude)
			}
			if got := ComputePlanFor(p); got != tc.want {
				t.Errorf("ComputePlanFor = %+v\nwant          %+v", got, tc.want)
			}
		})
	}
}

func TestComputePlan_ComponentsHelpers(t *testing.T) {
	full := FullComputePlan()
	off := full.WithoutComponents()
	if off.AnyComponents() {
		t.Errorf("WithoutComponents left a sub-part on: %+v", off)
	}
	if !off.MatricesSlot || !off.Overlays || !off.Tests || !off.PostTests || !off.Regressions {
		t.Errorf("WithoutComponents touched a whole-slot part: %+v", off)
	}
	if back := off.WithComponentsOf(full); back != full {
		t.Errorf("WithComponentsOf(full) = %+v; want full", back)
	}
	p := NewProcessor(nil)
	if p.ComputePlan() != full {
		t.Errorf("a new Processor computes %+v; want FullComputePlan", p.ComputePlan())
	}
	p.SetDisableComponents(true)
	if p.ComputePlan().AnyComponents() {
		t.Error("SetDisableComponents(true) left a sub-part on")
	}
	p.SetDisableComponents(false)
	if p.ComputePlan() != full {
		t.Errorf("SetDisableComponents(false) = %+v; want full", p.ComputePlan())
	}
}

// Run.NullRecords must not depend on `return`: on the buffered
// ungrouped exit it is the first aggregation slot's floor n_null
// (FieldPresent), and when the plan skips the aggregation components
// that carry it, the same FieldPresent count is taken directly — not
// the IsNull scan, which disagrees on a field that carries a wide,
// non-numeric, non-set value.
func TestComputePlan_RunNullRecordsIndependentOfAggs(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 0},
	}}
	records := func() []*Record {
		out := make([]*Record, 6)
		for i := range out {
			out[i] = NewRecordWithWide(schema, map[string]float64{"score": float64(i)}, nil, map[string]any{"w": "opaque"})
		}
		return out
	}
	req := &types.Request{Aggregations: []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "w", Label: "w_n"},
		{Type: types.AGG_MEDIAN, Field: "score", Label: "med"}, // routes buffered
	}}
	run := func(plan ComputePlan) *types.RunComponents {
		p := NewProcessor(schema)
		p.SetComputePlan(plan)
		resp, err := p.Process(context.Background(), req, NewSliceIterator(records()))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if p.LastPath() != PathBuffered {
			t.Fatalf("ran %s; the fixture must take the buffered exit", p.LastPath())
		}
		if resp.Components == nil || resp.Components.Run == nil {
			t.Fatal("no Run components")
		}
		return resp.Components.Run
	}
	want := run(FullComputePlan())
	if want.NullRecords == 0 {
		t.Fatal("fixture has no absent first-slot values: it cannot tell the two counts apart")
	}
	noAggs := FullComputePlan()
	noAggs.Aggs, noAggs.Groups = false, false
	if got := run(noAggs); *got != *want {
		t.Errorf("Run with aggregation components skipped = %+v; want %+v", *got, *want)
	}
}
