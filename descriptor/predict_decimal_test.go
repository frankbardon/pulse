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

func TestInspect_SurfacesDecimalMetadata(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6,
				Description: "Amount with 6 decimal places of precision."},
		},
	}
	data := buildTestPulseFile(t, schema)
	env := InspectFromBytes(data, nil)
	r, ok := env.Data.(*InspectResult)
	if !ok {
		t.Fatalf("Data is not *InspectResult")
	}
	if len(r.Fields) != 1 {
		t.Fatalf("fields = %d", len(r.Fields))
	}
	amount := r.Fields[0]
	if amount.Precision == nil || *amount.Precision != 20 {
		t.Errorf("amount precision = %v, want 20", amount.Precision)
	}
	if amount.Scale == nil || *amount.Scale != 6 {
		t.Errorf("amount scale = %v, want 6", amount.Scale)
	}
}
