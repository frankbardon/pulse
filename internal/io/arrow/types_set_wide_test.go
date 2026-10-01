package arrow

import (
	"testing"

	"github.com/apache/arrow-go/v18/arrow"

	"github.com/frankbardon/pulse/encoding"
)

// TestArrow_TypeFromPulseWideSetRungs pins the wide rungs onto the same
// LIST<UTF8> analogue the narrow rungs use. Without an arm here the
// default case hands back Float64, and a set_u128 / set_u256 column
// would export as a float column — no error, just a destroyed column.
func TestArrow_TypeFromPulseWideSetRungs(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32, encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		dt := TypeFromPulse(ft)
		if dt.ID() != arrow.LIST {
			t.Errorf("TypeFromPulse(%s) = %v, want LIST", ft, dt)
			continue
		}
		lt, ok := dt.(*arrow.ListType)
		if !ok {
			t.Errorf("TypeFromPulse(%s) = %T, want *arrow.ListType", ft, dt)
			continue
		}
		if lt.Elem().ID() != arrow.STRING {
			t.Errorf("TypeFromPulse(%s) element = %v, want UTF8", ft, lt.Elem())
		}
	}
}

// TestArrow_FieldFromPulseWideSetRungs covers the schema-building
// wrapper the exporter actually calls, so the mapping is pinned at the
// surface as well as at TypeFromPulse.
func TestArrow_FieldFromPulseWideSetRungs(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		af := FieldFromPulse(encoding.Field{Name: "issuers", Type: ft, Nullable: true})
		if af.Type.ID() != arrow.LIST {
			t.Errorf("FieldFromPulse(%s) type = %v, want LIST", ft, af.Type)
		}
		if !af.Nullable {
			t.Errorf("FieldFromPulse(%s) lost the nullable flag", ft)
		}
	}
}

// TestArrow_ListStartsProvisionalAndWidens documents the Arrow import
// contract: a LIST column enters as the narrowest rung and the shared
// inference ladder (io/infer.go) widens it against MaxSetEntries as the
// dictionary fills, now all the way to set_u256.
func TestArrow_ListStartsProvisionalAndWidens(t *testing.T) {
	for _, dt := range []arrow.DataType{
		arrow.ListOf(arrow.BinaryTypes.String),
		arrow.LargeListOf(arrow.BinaryTypes.String),
		arrow.FixedSizeListOf(3, arrow.BinaryTypes.String),
	} {
		if got := TypeToPulse(dt); got != encoding.FieldTypeSetU8 {
			t.Errorf("TypeToPulse(%v) = %s, want set_u8 (provisional)", dt, got)
		}
	}
	// The provisional start is only useful if the ladder above it
	// reaches the wide rungs; assert the ceiling the widening walks to.
	if got := encoding.FieldTypeSetU256.MaxSetEntries(); got != 256 {
		t.Errorf("widest set rung holds %d entries, want 256", got)
	}
}
