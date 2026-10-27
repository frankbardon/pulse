package types

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestCrosstabSpec_CellsFaults pins the shared Cells detection both
// validators map onto codes: both slots set, a null entry, an untyped
// entry and a duplicate effective label, each reported once, in
// declaration order; the single-measure form reports nothing.
func TestCrosstabSpec_CellsFaults(t *testing.T) {
	t.Parallel()

	count := func(f string) *Aggregation { return &Aggregation{Type: AGG_COUNT, Field: f} }
	cases := []struct {
		name string
		spec *CrosstabSpec
		want []CellsFaultKind
	}{
		{"single cell", &CrosstabSpec{Cell: count("x")}, nil},
		{"clean", &CrosstabSpec{Cells: []*Aggregation{count("x"), count("y")}}, nil},
		{"both set", &CrosstabSpec{Cell: count("x"), Cells: []*Aggregation{count("y")}}, []CellsFaultKind{CellsFaultBothSet}},
		{"null entry", &CrosstabSpec{Cells: []*Aggregation{count("x"), nil}}, []CellsFaultKind{CellsFaultNilEntry}},
		{"untyped entry", &CrosstabSpec{Cells: []*Aggregation{{Field: "x"}}}, []CellsFaultKind{CellsFaultMissingType}},
		{"duplicate label", &CrosstabSpec{Cells: []*Aggregation{count("x"), {Type: AGG_SUM, Field: "y", Label: "AGG_COUNT_x"}}},
			[]CellsFaultKind{CellsFaultDuplicateLabel}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []CellsFaultKind
			for _, f := range c.spec.CellsFaults() {
				got = append(got, f.Kind)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("faults = %v, want %v", got, c.want)
			}
		})
	}
}

// TestCrosstabSpec_MarginAggregationFaultsSeedEveryMeasure: an auxiliary
// label may collide with ANY measure's label, not only Cell's.
func TestCrosstabSpec_MarginAggregationFaultsSeedEveryMeasure(t *testing.T) {
	t.Parallel()

	spec := &CrosstabSpec{
		Cells:              []*Aggregation{{Type: AGG_COUNT, Field: "x", Label: "n"}, {Type: AGG_SUM, Field: "x", Label: "total"}},
		MarginAggregations: []*Aggregation{{Type: AGG_COUNT, Field: "w", Label: "total"}},
	}
	faults := spec.MarginAggregationFaults()
	if len(faults) != 1 || faults[0].Kind != MarginAggregationFaultDuplicateLabel {
		t.Fatalf("faults = %+v, want one duplicate_label", faults)
	}
}

// TestCrosstabSpec_MeasureHelpers pins the walk every slot visitor uses
// and the single-measure spec each measure runs as.
func TestCrosstabSpec_MeasureHelpers(t *testing.T) {
	t.Parallel()

	a := &Aggregation{Type: AGG_COUNT, Field: "x", Label: "n"}
	b := &Aggregation{Type: AGG_AVERAGE, Field: "x"}
	spec := &CrosstabSpec{
		Rows:  []*Group{{Type: GROUP_CATEGORY, Field: "r"}},
		Cells: []*Aggregation{a, b},
	}
	if !spec.HasMeasures() || !reflect.DeepEqual(spec.CellAggregations(), []*Aggregation{a, b}) {
		t.Fatalf("CellAggregations = %v", spec.CellAggregations())
	}
	if got := spec.MeasureLabels(); !reflect.DeepEqual(got, []string{"n", "AGG_AVERAGE_x"}) {
		t.Errorf("MeasureLabels = %v", got)
	}
	if spec.CellSlotPath(1) != "crosstab.cells[1]" {
		t.Errorf("CellSlotPath = %q", spec.CellSlotPath(1))
	}
	ms := spec.MeasureSpec(1)
	if ms.Cell != b || ms.Cells != nil || ms.HasMeasures() || spec.Cells == nil {
		t.Errorf("MeasureSpec(1) = %+v; spec.Cells = %v", ms, spec.Cells)
	}
	h := spec.MeasureRowHeader()
	if !reflect.DeepEqual(h.Fields, []string{"r", CrosstabMeasureField}) || !reflect.DeepEqual(h.Types, []string{"GROUP_CATEGORY", CrosstabMeasureType}) {
		t.Errorf("MeasureRowHeader = %+v", h)
	}
	single := &CrosstabSpec{Cell: a}
	if single.HasMeasures() || single.CellSlotPath(0) != "crosstab.cell" || len(single.CellAggregations()) != 1 {
		t.Errorf("single form: %v %q %v", single.HasMeasures(), single.CellSlotPath(0), single.CellAggregations())
	}
	if (&CrosstabSpec{}).CellAggregations() != nil {
		t.Error("no cell: CellAggregations must be nil")
	}
}

// TestCrosstabSpec_CellsAbsentByteIdentical: a single-measure spec and
// its payload marshal with no cells / cell_labels / measures key.
func TestCrosstabSpec_CellsAbsentByteIdentical(t *testing.T) {
	t.Parallel()

	spec := &CrosstabSpec{Rows: []*Group{{Type: GROUP_CATEGORY, Field: "r"}}, Cell: &Aggregation{Type: AGG_COUNT, Field: "x"}}
	payload := &MatrixPayload{CellLabel: "AGG_COUNT_x"}
	comps := &CrosstabComponents{CellCounts: [][]int{{1}}}
	for v, keys := range map[any][]string{
		spec:    {`"cells"`, `"measures"`},
		payload: {`"cell_labels"`, `"measures"`},
		comps:   {`"measures"`},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			if strings.Contains(string(b), key) {
				t.Errorf("%T marshals %s on the single-measure form: %s", v, key, b)
			}
		}
	}
}
