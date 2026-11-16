package prevstate

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestWindowKeys: a lagged category joins the key view on its source's
// dictionary; a request lagging no category returns the view itself.
func TestWindowKeys(t *testing.T) {
	t.Parallel()

	d := encoding.NewDictionary()
	_, _ = d.Add("a")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "t", Type: encoding.FieldTypeU16},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
	}}
	numeric := []*types.Window{{Type: types.WIN_LAG, Field: "t", Label: "lt"}}
	if got := WindowKeys(schema, schema, numeric); got != schema {
		t.Fatal("a numeric lag must return the view itself")
	}
	got := WindowKeys(schema, schema, []*types.Window{{Type: types.WIN_LAG, Field: "c", Label: "lc"}})
	f := got.Field("lc")
	if got == schema || f == nil || f.Type != encoding.FieldTypeCategoricalU8 || f.Dictionary != d || !f.Nullable || len(schema.Fields) != 2 {
		t.Fatalf("view %+v", got.Fields)
	}
}
