package types

import "strconv"

// CrosstabMeasureField and CrosstabMeasureType are the MEASURE row-axis
// position a multi-measure crosstab (CrosstabSpec.Cells) appends to the
// row header — the PAIR-axis precedent of the pairwise overlays and the
// TOTAL-column precedent of the implicit Total column. Every row key on
// such a payload ends with its measure's effective label.
const (
	CrosstabMeasureField = "_measure"
	CrosstabMeasureType  = "MEASURE"
)

// CrosstabMeasureMargins is one measure's column margins and grand total
// on a multi-measure MatrixPayload (MatrixPayload.Measures). The fields
// follow the single-measure MatrixPayload.ColumnMargins / GrandTotal
// emission rule exactly: ColumnMargins only when margins.columns is
// displayed, GrandTotal Present only when margins.grand is.
type CrosstabMeasureMargins struct {
	// Label is the measure's effective label (Label, else TYPE_field).
	Label string `json:"label"`
	// ColumnMargins carries the measure's per-column margin in
	// ColumnKeys order.
	ColumnMargins []MatrixCell `json:"column_margins,omitempty"`
	// GrandTotal is the measure's grand-total margin.
	GrandTotal MatrixCell `json:"grand_total"`
}

// CrosstabMeasureComponents is one measure's column-margin and
// grand-total components on a multi-measure crosstab
// (CrosstabComponents.Measures) — the per-measure twins of the
// single-measure CrosstabComponents slots of the same names, under the
// same admission and display rules. A record reaches a margin only if it
// reached a CELL.
type CrosstabMeasureComponents struct {
	// Label is the measure's effective label.
	Label string `json:"label"`

	ColumnMarginCounts       []int                                `json:"column_margin_counts,omitempty"`
	ColumnMarginComponents   []map[string]any                     `json:"column_margin_components,omitempty"`
	ColumnMarginAggregations []map[string]MarginAggregationFigure `json:"column_margin_aggregations,omitempty"`
	GrandTotalCount          int                                  `json:"grand_total_count,omitempty"`
	GrandTotalComponents     map[string]any                       `json:"grand_total_components,omitempty"`
	GrandTotalAggregations   map[string]MarginAggregationFigure   `json:"grand_total_aggregations,omitempty"`
}

// HasMeasures reports whether the spec declares the multi-measure Cells
// form. A one-entry Cells is still the multi-measure form: its payload
// carries the MEASURE row position, so the shape never depends on how
// many measures were declared.
func (s *CrosstabSpec) HasMeasures() bool {
	return s != nil && len(s.Cells) > 0
}

// CellAggregations returns every cell aggregation the spec declares:
// Cells when the multi-measure form is set, otherwise the single Cell
// (nil when neither is). Entries may be nil only on an unvalidated spec.
// The one walk every slot visitor (field references, params, weights,
// projection) uses, so a measure is never skipped.
func (s *CrosstabSpec) CellAggregations() []*Aggregation {
	if s == nil {
		return nil
	}
	if len(s.Cells) > 0 {
		return s.Cells
	}
	if s.Cell != nil {
		return []*Aggregation{s.Cell}
	}
	return nil
}

// CellSlotPath returns the request path of CellAggregations()[i]:
// "crosstab.cells[i]" on the multi-measure form, else "crosstab.cell".
func (s *CrosstabSpec) CellSlotPath(i int) string {
	if s.HasMeasures() {
		return "crosstab.cells[" + strconv.Itoa(i) + "]"
	}
	return "crosstab.cell"
}

// MeasureLabels returns each declared measure's effective label in
// declaration order (nil on the single-measure form).
func (s *CrosstabSpec) MeasureLabels() []string {
	if !s.HasMeasures() {
		return nil
	}
	out := make([]string, len(s.Cells))
	for i, a := range s.Cells {
		out[i] = AggregationLabelOf(a)
	}
	return out
}

// MeasureSpec returns the single-measure spec measure i runs as: a
// shallow copy with Cell = Cells[i] and Cells cleared. Axes, margins,
// normalize, shape and margin_aggregations are shared, so every measure
// sees the same axis keys and admission — the property the fold that
// stitches the measures back together relies on.
func (s *CrosstabSpec) MeasureSpec(i int) *CrosstabSpec {
	cp := *s
	cp.Cell = s.Cells[i]
	cp.Cells = nil
	return &cp
}

// MeasureRowHeader returns the multi-measure row header: the grouper
// fields and types followed by the MEASURE position.
func (s *CrosstabSpec) MeasureRowHeader() AxisHeader {
	return AxisHeader{
		Fields: append(AxisFieldNames(s.Rows), CrosstabMeasureField),
		Types:  append(AxisTypes(s.Rows), CrosstabMeasureType),
	}
}

// CellsFaultKind classifies a structural defect in the Cell / Cells
// pair. Like MarginAggregationFaultKind it carries no error code: predict
// and execution each map a kind onto their own coded surface, sharing
// the DETECTION so the two validators refuse the same specs.
type CellsFaultKind string

const (
	// CellsFaultBothSet marks a spec carrying both Cell and Cells.
	CellsFaultBothSet CellsFaultKind = "both_set"
	// CellsFaultNilEntry marks a null element in Cells.
	CellsFaultNilEntry CellsFaultKind = "nil_entry"
	// CellsFaultMissingType marks a Cells entry that has no Type after
	// smart defaults (no Type and no field whose schema type infers one).
	CellsFaultMissingType CellsFaultKind = "missing_type"
	// CellsFaultDuplicateLabel marks an effective label two Cells
	// entries share: the measure is addressed by label on every row key,
	// CellLabels entry and Measures entry, so a duplicate is ambiguous.
	CellsFaultDuplicateLabel CellsFaultKind = "duplicate_label"
)

// CellsFault is one structural defect in the Cell / Cells pair. Message
// and Details render verbatim on both validators.
type CellsFault struct {
	Kind    CellsFaultKind
	Message string
	Details map[string]any
}

// CellsFaults returns every structural defect in the Cell / Cells pair,
// in declaration order. Neither being set is not a fault here: that is
// PULSE_CROSSTAB_MISSING_CELL, which both validators raise on their own.
// A margin_aggregations label colliding with a measure label is reported
// by MarginAggregationFaults.
func (s *CrosstabSpec) CellsFaults() []CellsFault {
	if !s.HasMeasures() {
		return nil
	}
	var faults []CellsFault
	if s.Cell != nil {
		faults = append(faults, CellsFault{
			Kind:    CellsFaultBothSet,
			Message: "crosstab cell and cells are mutually exclusive — set cell for one measure or cells for several",
			Details: map[string]any{"cells": len(s.Cells)},
		})
	}
	seen := map[string]int{}
	for i, a := range s.Cells {
		if a == nil {
			faults = append(faults, CellsFault{
				Kind:    CellsFaultNilEntry,
				Message: "crosstab cells contains a null entry",
				Details: map[string]any{"index": i},
			})
			continue
		}
		if a.Type == "" {
			faults = append(faults, CellsFault{
				Kind:    CellsFaultMissingType,
				Message: "crosstab cells entry has no aggregation type and none could be inferred from its field",
				Details: map[string]any{"index": i, "field": a.Field},
			})
			continue
		}
		label := AggregationLabelOf(a)
		if first, dup := seen[label]; dup {
			faults = append(faults, CellsFault{
				Kind: CellsFaultDuplicateLabel,
				Message: "crosstab cells effective label is claimed twice: " + label +
					" — each measure is addressed by its label on every row key, so the two would be indistinguishable",
				Details: map[string]any{"index": i, "first_index": first, "label": label},
			})
			continue
		}
		seen[label] = i
	}
	return faults
}
