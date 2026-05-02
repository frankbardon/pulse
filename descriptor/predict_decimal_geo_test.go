package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestPredict_DecimalAggValidity(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6,
				Description: "Amount in USD with 6 decimal places of precision."},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "amount"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	hit := false
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL) {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL warning, got warnings: %v", env.Warnings)
	}
}

func TestPredict_GeoAggValidity(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "loc", Type: encoding.FieldTypePointF64,
				Description: "Pickup location as packed lat/lon doubles."},
			{Name: "score", Type: encoding.FieldTypeF64,
				Description: "Numeric score field for the cohort entry."},
		},
	}
	data := buildTestPulseFile(t, schema)
	// AGG_SUM on point_f64 is invalid.
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "loc"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	hit := false
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_GEO) {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected PULSE_AGG_NOT_MEANINGFUL_FOR_GEO warning, got warnings: %v", env.Warnings)
	}

	// AGG_GEO_CENTROID on f64 is invalid (target type mismatch).
	req2 := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_GEO_CENTROID, Field: "score"},
		},
	}
	env = PredictFromBytes(data, req2, nil)
	if len(env.Errors) == 0 {
		t.Errorf("expected error for AGG_GEO_CENTROID on f64, got none")
	}
}

func TestPredict_GeoFiltererValidity(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64,
				Description: "Numeric score for the cohort entry."},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_GEO_WITHIN, Field: "score", Expression: "POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Errorf("expected error for FILTER_GEO_WITHIN on f64 field")
	}
}

func TestInspect_SurfacesDecimalMetadata(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6,
				Description: "Amount with 6 decimal places of precision."},
			{Name: "cell", Type: encoding.FieldTypeH3Cell, H3Resolution: 9,
				Description: "H3 cell index recorded at native resolution 9."},
		},
	}
	data := buildTestPulseFile(t, schema)
	env := InspectFromBytes(data, nil)
	r, ok := env.Data.(*InspectResult)
	if !ok {
		t.Fatalf("Data is not *InspectResult")
	}
	if len(r.Fields) != 2 {
		t.Fatalf("fields = %d", len(r.Fields))
	}
	amount := r.Fields[0]
	if amount.Precision == nil || *amount.Precision != 20 {
		t.Errorf("amount precision = %v, want 20", amount.Precision)
	}
	if amount.Scale == nil || *amount.Scale != 6 {
		t.Errorf("amount scale = %v, want 6", amount.Scale)
	}
	cell := r.Fields[1]
	if cell.H3Resolution == nil || *cell.H3Resolution != 9 {
		t.Errorf("cell h3 resolution = %v, want 9", cell.H3Resolution)
	}
}
