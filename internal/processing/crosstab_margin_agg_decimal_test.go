package processing

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstabAuxMargin_DecimalRuleBothArms: a margin aggregation whose
// built-in operator has no decimal arm, over a decimal128 field, is
// refused PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL by BOTH arms — the
// buffered resolution and the fused construction (the defensive twin of
// CanFuseCrosstab, which declines a decimal auxiliary) — exactly as the
// same aggregator in the cell is. A decimal-arm auxiliary resolves on
// both.
func TestCrosstabAuxMargin_DecimalRuleBothArms(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	dict.Add("a")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, ByteOffset: 1},
	}}
	spec := func(op types.AggregationType) *types.CrosstabSpec {
		return &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "region"},
			Margins:            types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			MarginAggregations: []*types.Aggregation{{Type: op, Field: "amt", Label: "aux"}},
		}
	}
	isDecimalRefusal := func(err error) bool {
		var ce *errors.CodedError
		return stderrors.As(err, &ce) && ce.Code == errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL
	}
	p := NewProcessor(schema)
	if _, err := NewFusedCrosstabState(spec(types.AGG_MEDIAN), schema, &ExtensionRegistry{}); !isDecimalRefusal(err) {
		t.Errorf("fused AGG_MEDIAN over decimal128: %v", err)
	}
	if _, err := p.computeAuxMargins(spec(types.AGG_MEDIAN), nil, &CrosstabAxisPartition{}, &CrosstabAxisPartition{}); !isDecimalRefusal(err) {
		t.Errorf("buffered AGG_MEDIAN over decimal128: %v", err)
	}
	if _, err := NewFusedCrosstabState(spec(types.AGG_SUM), schema, &ExtensionRegistry{}); isDecimalRefusal(err) {
		t.Errorf("fused AGG_SUM over decimal128 refused: %v", err)
	}
	if _, err := p.computeAuxMargins(spec(types.AGG_SUM), nil, &CrosstabAxisPartition{}, &CrosstabAxisPartition{}); isDecimalRefusal(err) {
		t.Errorf("buffered AGG_SUM over decimal128 refused: %v", err)
	}
}
