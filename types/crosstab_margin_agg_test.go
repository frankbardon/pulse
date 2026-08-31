package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

// baseSpec is the canonical two-axis spec every test in this file
// varies. Deliberately minimal: rows, columns, cell, one margin.
func baseSpec() *CrosstabSpec {
	return &CrosstabSpec{
		Rows:    []*Group{{Type: GROUP_CATEGORY, Field: "brand"}},
		Columns: []*Group{{Type: GROUP_CATEGORY, Field: "audience"}},
		Cell:    &Aggregation{Type: AGG_COUNT, Field: "respondent"},
		Margins: CrosstabMargins{Rows: true},
	}
}

// TestCrosstabSpec_MarginAggregationsAbsentByteIdentical pins the
// additive guarantee: a spec that does not declare the slot marshals to
// exactly the bytes it marshalled to before the slot existed. The
// expectation is a literal rather than a round-trip so a lost
// `omitempty` — which would emit `"margin_aggregations":null` into every
// crosstab request on the wire — fails here rather than silently
// widening the payload for every existing consumer.
func TestCrosstabSpec_MarginAggregationsAbsentByteIdentical(t *testing.T) {
	const want = `{"rows":[{"type":"GROUP_CATEGORY","field":"brand"}],` +
		`"columns":[{"type":"GROUP_CATEGORY","field":"audience"}],` +
		`"cell":{"type":"AGG_COUNT","field":"respondent"},` +
		`"margins":{"rows":true}}`

	for _, tc := range []struct {
		name string
		spec *CrosstabSpec
	}{
		{"absent", baseSpec()},
		{"explicitly nil", func() *CrosstabSpec {
			s := baseSpec()
			s.MarginAggregations = nil
			return s
		}()},
		{"empty slice", func() *CrosstabSpec {
			s := baseSpec()
			s.MarginAggregations = []*Aggregation{}
			return s
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.spec)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != want {
				t.Errorf("marshalled crosstab spec changed shape\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// TestCrosstabSpec_MarginAggregationsRoundTrip proves the slot is a real
// wire field once declared — it survives an encode/decode cycle under
// the documented JSON name.
func TestCrosstabSpec_MarginAggregationsRoundTrip(t *testing.T) {
	spec := baseSpec()
	spec.MarginAggregations = []*Aggregation{
		{Type: AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	if _, ok := decoded["margin_aggregations"]; !ok {
		t.Fatalf("declared margin aggregations did not marshal under \"margin_aggregations\"; got %s", raw)
	}

	var back CrosstabSpec
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back.MarginAggregations, spec.MarginAggregations) {
		t.Errorf("round trip lost the slot: got %+v, want %+v",
			back.MarginAggregations, spec.MarginAggregations)
	}
}

// TestCrosstabSpec_MarginAggregationLabels pins the labelling rule the
// components surface will key on: an explicit Label wins, otherwise
// TYPE_field — the same rule CellLabel already applies.
func TestCrosstabSpec_MarginAggregationLabels(t *testing.T) {
	spec := baseSpec()
	spec.MarginAggregations = []*Aggregation{
		{Type: AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
		{Type: AGG_SUM, Field: "weight"},
		nil,
	}
	got := spec.MarginAggregationLabels()
	want := []string{"base", "AGG_SUM_weight", ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MarginAggregationLabels() = %v, want %v", got, want)
	}
	if !spec.HasMarginAggregations() {
		t.Error("HasMarginAggregations() = false with entries declared")
	}
	if baseSpec().HasMarginAggregations() {
		t.Error("HasMarginAggregations() = true with no entries declared")
	}
	if (*CrosstabSpec)(nil).HasMarginAggregations() {
		t.Error("HasMarginAggregations() on a nil spec must be false")
	}
	if got := (*CrosstabSpec)(nil).MarginAggregationLabels(); got != nil {
		t.Errorf("MarginAggregationLabels() on a nil spec = %v, want nil", got)
	}
}

// TestCrosstabSpec_MarginAggregationFaults is the shared-detection gate.
// Predict and execution each render these faults through their own coded
// surface; sharing the DETECTION is what stops the two validators
// drifting on which specs they refuse.
func TestCrosstabSpec_MarginAggregationFaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		aux  []*Aggregation
		cell *Aggregation
		want []MarginAggregationFaultKind
	}{
		{
			name: "clean",
			aux:  []*Aggregation{{Type: AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"}},
			want: nil,
		},
		{
			name: "nil entry",
			aux:  []*Aggregation{nil},
			want: []MarginAggregationFaultKind{MarginAggregationFaultNilEntry},
		},
		{
			name: "missing type",
			aux:  []*Aggregation{{Field: "respondent"}},
			want: []MarginAggregationFaultKind{MarginAggregationFaultMissingType},
		},
		{
			name: "duplicate derived label",
			aux: []*Aggregation{
				{Type: AGG_SUM, Field: "weight"},
				{Type: AGG_SUM, Field: "weight"},
			},
			want: []MarginAggregationFaultKind{MarginAggregationFaultDuplicateLabel},
		},
		{
			name: "duplicate explicit label",
			aux: []*Aggregation{
				{Type: AGG_SUM, Field: "weight", Label: "base"},
				{Type: AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
			},
			want: []MarginAggregationFaultKind{MarginAggregationFaultDuplicateLabel},
		},
		{
			// The cell aggregator's own margin already occupies its
			// label in the margin-components namespace, so an auxiliary
			// that reuses it would overwrite the figure it sits beside.
			name: "collides with the cell label",
			cell: &Aggregation{Type: AGG_COUNT, Field: "respondent", Label: "base"},
			aux:  []*Aggregation{{Type: AGG_SUM, Field: "weight", Label: "base"}},
			want: []MarginAggregationFaultKind{MarginAggregationFaultDuplicateLabel},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := baseSpec()
			if tc.cell != nil {
				spec.Cell = tc.cell
			}
			spec.MarginAggregations = tc.aux

			faults := spec.MarginAggregationFaults()
			var kinds []MarginAggregationFaultKind
			for _, f := range faults {
				kinds = append(kinds, f.Kind)
				if f.Message == "" {
					t.Errorf("fault %v carries no message", f.Kind)
				}
				if f.Details == nil {
					t.Errorf("fault %v carries no details", f.Kind)
				}
			}
			if !reflect.DeepEqual(kinds, tc.want) {
				t.Errorf("fault kinds = %v, want %v", kinds, tc.want)
			}
		})
	}

	if got := (*CrosstabSpec)(nil).MarginAggregationFaults(); got != nil {
		t.Errorf("MarginAggregationFaults() on a nil spec = %v, want nil", got)
	}
}

// TestCrosstabSpec_MarginAggregationsObserved pins the "declared but
// nothing will carry it" predicate. Auxiliary aggregations land in the
// margin accumulators only, so a spec that emits no margin at all
// computes them into nowhere.
func TestCrosstabSpec_MarginAggregationsObserved(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*CrosstabSpec)
		want bool
	}{
		{"no margin, no normalize", func(s *CrosstabSpec) { s.Margins = CrosstabMargins{} }, false},
		{"row margin", func(s *CrosstabSpec) { s.Margins = CrosstabMargins{Rows: true} }, true},
		{"column margin", func(s *CrosstabSpec) { s.Margins = CrosstabMargins{Columns: true} }, true},
		{"grand margin", func(s *CrosstabSpec) { s.Margins = CrosstabMargins{Grand: true} }, true},
		{"normalize implies a margin", func(s *CrosstabSpec) {
			s.Margins = CrosstabMargins{}
			s.Normalize = CrosstabNormalizeColumn
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := baseSpec()
			tc.mut(spec)
			if got := spec.MarginAggregationsObserved(); got != tc.want {
				t.Errorf("MarginAggregationsObserved() = %v, want %v", got, tc.want)
			}
		})
	}
}
