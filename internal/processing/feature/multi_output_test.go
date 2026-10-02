package feature

import (
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeCategoricalSchema(t *testing.T, name string, values ...string) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range values {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: name, Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Description: "category field"},
		},
	}
}

// stringRecord lets us seed both numeric and string accessors for ONE_HOT
// without depending on the parent processing.Record.
type stringRecord struct {
	*fakeRecord
}

func newStringRecord(field, value string) *stringRecord {
	r := newFakeRecord()
	r.str[field] = value
	return &stringRecord{fakeRecord: r}
}

func TestOneHot_Compute(t *testing.T) {
	schema := makeCategoricalSchema(t, "region", "north", "south", "east")

	records := []Record{
		newStringRecord("region", "north"),
		newStringRecord("region", "south"),
		newStringRecord("region", "north"),
	}

	c, err := newOneHot(&types.Feature{Field: "region"}, schema)
	if err != nil {
		t.Fatalf("newOneHot: %v", err)
	}
	out, err := c.Compute(records, "region")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if got := len(out); got != 3 {
		t.Fatalf("expected 3 columns (one per category), got %d", got)
	}
	if out["region_north"].Values[0] != 1 || out["region_north"].Values[2] != 1 {
		t.Errorf("region_north should be 1 at indexes 0,2; got %v", out["region_north"].Values)
	}
	if out["region_south"].Values[1] != 1 {
		t.Errorf("region_south should be 1 at index 1")
	}
	// region_east must exist with all zeros even with no matching rows.
	col, ok := out["region_east"]
	if !ok {
		t.Fatal("expected region_east column for the third dictionary entry")
	}
	for i, v := range col.Values {
		if v != 0 {
			t.Errorf("region_east[%d]=%f, want 0", i, v)
		}
	}
}

func TestOneHot_LabelPrefix(t *testing.T) {
	schema := makeCategoricalSchema(t, "color", "red", "blue")
	records := []Record{newStringRecord("color", "red")}

	c, err := newOneHot(&types.Feature{Field: "color", Label: "hue"}, schema)
	if err != nil {
		t.Fatalf("newOneHot: %v", err)
	}
	out, err := c.Compute(records, "color")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if _, ok := out["hue_red"]; !ok {
		t.Errorf("expected label-prefixed column hue_red, got keys: %v", keys(out))
	}
}

func TestOneHot_RequiresCategoricalField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
	_, err := newOneHot(&types.Feature{Field: "score"}, schema)
	if err == nil {
		t.Error("expected error for non-categorical field")
	}
}

func TestOneHot_RequiresField(t *testing.T) {
	if _, err := newOneHot(&types.Feature{}, nil); err == nil {
		t.Error("expected error for missing field")
	}
}

func TestOneHot_FieldNotInSchema(t *testing.T) {
	schema := makeCategoricalSchema(t, "region", "n", "s")
	_, err := newOneHot(&types.Feature{Field: "missing"}, schema)
	if err == nil {
		t.Error("expected error for missing field")
	}
}

func TestDateFeatures_Compute(t *testing.T) {
	// 2024-03-15 in UTC = 19797 days since Unix epoch.
	d := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	days := d.Unix() / 86400

	r := newFakeRecord()
	r.num["dt"] = float64(days)
	records := []Record{r}

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "dt", Type: encoding.FieldTypeDate},
		},
	}

	c, err := newDateFeatures(&types.Feature{Field: "dt"}, schema)
	if err != nil {
		t.Fatalf("newDateFeatures: %v", err)
	}
	out, err := c.Compute(records, "dt")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if v := out["dt_year"].Values[0]; v != 2024 {
		t.Errorf("dt_year=%f, want 2024", v)
	}
	if v := out["dt_month"].Values[0]; v != 3 {
		t.Errorf("dt_month=%f, want 3", v)
	}
	if v := out["dt_day"].Values[0]; v != 15 {
		t.Errorf("dt_day=%f, want 15", v)
	}
	if v := out["dt_quarter"].Values[0]; v != 1 {
		t.Errorf("dt_quarter=%f, want 1 (March)", v)
	}
	// March 15 2024 is a Friday → Weekday()==5.
	if v := out["dt_dow"].Values[0]; v != 5 {
		t.Errorf("dt_dow=%f, want 5 (Friday)", v)
	}
}

func TestDateFeatures_NullPropagation(t *testing.T) {
	r := newFakeRecord()
	r.nulls["dt"] = true
	records := []Record{r}

	c, err := newDateFeatures(&types.Feature{Field: "dt"}, nil)
	if err != nil {
		t.Fatalf("newDateFeatures: %v", err)
	}
	out, _ := c.Compute(records, "dt")

	for col, v := range out {
		if !v.Nulls[0] {
			t.Errorf("expected null at [0] for column %s", col)
		}
	}
}

func TestDateFeatures_RequiresDateType(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}},
	}
	_, err := newDateFeatures(&types.Feature{Field: "x"}, schema)
	if err == nil {
		t.Error("expected error for non-date field type")
	}
}

func TestDateFeatures_RequiresField(t *testing.T) {
	if _, err := newDateFeatures(&types.Feature{}, nil); err == nil {
		t.Error("expected error for missing field")
	}
}

func TestMultiOutputFeatures_Registered(t *testing.T) {
	for _, ft := range []types.FeatureType{types.FEAT_ONE_HOT, types.FEAT_DATE_FEATURES} {
		if _, ok := Lookup(ft); !ok {
			t.Errorf("expected %s to be registered", ft)
		}
	}
}

func keys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
