package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// crosstabBrandSchema builds a 2-field categorical schema mirroring the
// real user case: a "brand" field whose dictionary carries the four
// brand codes 451,452,453,450 (inserted in dictionary order, NOT the
// desired include order) plus a "channel" field (aud / crdown / online).
func crosstabBrandSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	brandDict := encoding.NewDictionary()
	for _, b := range []string{"450", "451", "452", "453"} {
		if _, err := brandDict.Add(b); err != nil {
			t.Fatalf("brand dict.Add: %v", err)
		}
	}
	channelDict := encoding.NewDictionary()
	for _, c := range []string{"aud", "crdown", "online"} {
		if _, err := channelDict.Add(c); err != nil {
			t.Fatalf("channel dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brandDict},
			{Name: "channel", Type: encoding.FieldTypeCategoricalU8, Dictionary: channelDict},
		},
	}
}

// brandRecord builds a Record with the given brand + channel dictionary
// indices.
func brandRecord(schema *encoding.Schema, brand, channel uint64) *Record {
	return NewRecord(schema, map[string]float64{
		"brand":   float64(brand),
		"channel": float64(channel),
	})
}

// brandRecords returns one record per (brand, channel) combination so
// every axis bucket is populated regardless of ordering.
func brandRecords(schema *encoding.Schema) []*Record {
	var recs []*Record
	for brand := uint64(0); brand < 4; brand++ {
		for channel := uint64(0); channel < 3; channel++ {
			recs = append(recs, brandRecord(schema, brand, channel))
		}
	}
	return recs
}

// axisKeyStrings flattens a partition's Keys into the dictionary label
// sequence for a single-axis partition (one component per key).
func partitionKeys(p *CrosstabAxisPartition) []string {
	return append([]string(nil), p.Keys...)
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPartitionByAxis_RowIncludeOrder_ColumnAlphabetical reproduces the
// user's real case: the row axis carries include ["451","452","453","450"]
// and the column axis carries no include. Rows must emit in the given
// include order; columns must stay alphabetical.
func TestPartitionByAxis_RowIncludeOrder_ColumnAlphabetical(t *testing.T) {
	schema := crosstabBrandSchema(t)
	p := NewProcessor(schema)
	recs := brandRecords(schema)

	rowAxis := []*types.Group{{
		Type:    types.GROUP_CATEGORY,
		Field:   "brand",
		Include: []string{"451", "452", "453", "450"},
	}}
	colAxis := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "channel"}}

	rowPart, err := p.PartitionByAxis(rowAxis, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis rows: %v", err)
	}
	colPart, err := p.PartitionByAxis(colAxis, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis cols: %v", err)
	}

	wantRows := []string{"451", "452", "453", "450"}
	if got := partitionKeys(rowPart); !eqStrings(got, wantRows) {
		t.Errorf("row keys = %v, want include order %v", got, wantRows)
	}
	wantCols := []string{"aud", "crdown", "online"}
	if got := partitionKeys(colPart); !eqStrings(got, wantCols) {
		t.Errorf("col keys = %v, want alphabetical %v", got, wantCols)
	}
}

// TestPartitionByAxis_BothAxesInclude_EachHonorsOwn verifies that when
// both axes carry an include list, each axis follows its OWN order
// independently.
func TestPartitionByAxis_BothAxesInclude_EachHonorsOwn(t *testing.T) {
	schema := crosstabBrandSchema(t)
	p := NewProcessor(schema)
	recs := brandRecords(schema)

	rowAxis := []*types.Group{{
		Type:    types.GROUP_CATEGORY,
		Field:   "brand",
		Include: []string{"453", "451", "450", "452"},
	}}
	colAxis := []*types.Group{{
		Type:    types.GROUP_CATEGORY,
		Field:   "channel",
		Include: []string{"online", "aud", "crdown"},
	}}

	rowPart, err := p.PartitionByAxis(rowAxis, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis rows: %v", err)
	}
	colPart, err := p.PartitionByAxis(colAxis, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis cols: %v", err)
	}

	wantRows := []string{"453", "451", "450", "452"}
	if got := partitionKeys(rowPart); !eqStrings(got, wantRows) {
		t.Errorf("row keys = %v, want %v", got, wantRows)
	}
	wantCols := []string{"online", "aud", "crdown"}
	if got := partitionKeys(colPart); !eqStrings(got, wantCols) {
		t.Errorf("col keys = %v, want %v", got, wantCols)
	}
}

// TestPartitionByAxis_MultiAxisPerPositionInclude verifies that a nested
// (multi-grouper) axis honors per-position include order: position 0
// carries an include, position 1 does not (alphabetical within each
// position-0 bucket).
func TestPartitionByAxis_MultiAxisPerPositionInclude(t *testing.T) {
	schema := crosstabBrandSchema(t)
	p := NewProcessor(schema)
	recs := brandRecords(schema)

	// Nested row axis: brand (include-ordered) then channel (alphabetical).
	axis := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "brand", Include: []string{"452", "450", "451", "453"}},
		{Type: types.GROUP_CATEGORY, Field: "channel"},
	}

	part, err := p.PartitionByAxis(axis, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis: %v", err)
	}

	// Expected composite key order: outer position by brand include order,
	// inner position alphabetical by channel.
	brandOrder := []string{"452", "450", "451", "453"}
	channelOrder := []string{"aud", "crdown", "online"}
	var want []string
	for _, b := range brandOrder {
		for _, c := range channelOrder {
			want = append(want, CompositeAxisKey([]string{b, c}))
		}
	}
	if got := partitionKeys(part); !eqStrings(got, want) {
		t.Errorf("multi-axis keys mismatch\n got=%v\nwant=%v", got, want)
	}
}

// TestPartitionByAxis_MultiAxisBothPositionsInclude verifies that both
// positions of a nested axis honor their own include order.
func TestPartitionByAxis_MultiAxisBothPositionsInclude(t *testing.T) {
	schema := crosstabBrandSchema(t)
	p := NewProcessor(schema)
	recs := brandRecords(schema)

	axis := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "brand", Include: []string{"451", "453", "452", "450"}},
		{Type: types.GROUP_CATEGORY, Field: "channel", Include: []string{"crdown", "online", "aud"}},
	}
	part, err := p.PartitionByAxis(axis, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis: %v", err)
	}

	brandOrder := []string{"451", "453", "452", "450"}
	channelOrder := []string{"crdown", "online", "aud"}
	var want []string
	for _, b := range brandOrder {
		for _, c := range channelOrder {
			want = append(want, CompositeAxisKey([]string{b, c}))
		}
	}
	if got := partitionKeys(part); !eqStrings(got, want) {
		t.Errorf("multi-axis keys mismatch\n got=%v\nwant=%v", got, want)
	}
}

// TestPartitionByAxis_NoIncludeStaysAlphabetical guards byte-identity:
// an axis with no include must stay sorted alphabetically, exactly as
// before the include-order change.
func TestPartitionByAxis_NoIncludeStaysAlphabetical(t *testing.T) {
	schema := crosstabBrandSchema(t)
	p := NewProcessor(schema)
	recs := brandRecords(schema)

	single := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}}
	part, err := p.PartitionByAxis(single, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis: %v", err)
	}
	want := []string{"450", "451", "452", "453"}
	if got := partitionKeys(part); !eqStrings(got, want) {
		t.Errorf("no-include single axis = %v, want %v", got, want)
	}

	nested := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "brand"},
		{Type: types.GROUP_CATEGORY, Field: "channel"},
	}
	npart, err := p.PartitionByAxis(nested, recs)
	if err != nil {
		t.Fatalf("PartitionByAxis nested: %v", err)
	}
	var wantNested []string
	for _, b := range []string{"450", "451", "452", "453"} {
		for _, c := range []string{"aud", "crdown", "online"} {
			wantNested = append(wantNested, CompositeAxisKey([]string{b, c}))
		}
	}
	if got := partitionKeys(npart); !eqStrings(got, wantNested) {
		t.Errorf("no-include nested = %v, want %v", got, wantNested)
	}
}

// TestOrderCompositeKeysByAxisInclude_FlatReuse exercises the shared
// helper directly over flat composite keys + parallel tuples — the exact
// shape the fused path (E2-S2) will decompose into. Position 0 carries an
// include, position 1 does not.
func TestOrderCompositeKeysByAxisInclude_FlatReuse(t *testing.T) {
	axis := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "brand", Include: []string{"452", "451"}},
		{Type: types.GROUP_CATEGORY, Field: "channel"},
	}
	// Deliberately scrambled input order.
	tuples := []types.AxisKey{
		{"451", "online"},
		{"452", "aud"},
		{"451", "aud"},
		{"452", "online"},
	}
	keys := make([]string, len(tuples))
	for i, tup := range tuples {
		parts := make([]string, len(tup))
		for j, v := range tup {
			parts[j] = v.(string)
		}
		keys[i] = CompositeAxisKey(parts)
	}

	got := orderCompositeKeysByAxisInclude(keys, tuples, axis)
	want := []string{
		CompositeAxisKey([]string{"452", "aud"}),
		CompositeAxisKey([]string{"452", "online"}),
		CompositeAxisKey([]string{"451", "aud"}),
		CompositeAxisKey([]string{"451", "online"}),
	}
	if !eqStrings(got, want) {
		t.Errorf("flat reuse order mismatch\n got=%v\nwant=%v", got, want)
	}
}
