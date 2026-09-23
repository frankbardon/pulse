package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E2-S3 — wide-set groupers as crosstab axes.
//
// Fused-crosstab eligibility is decided on operator SHAPE (does the
// grouper key per record) and never on set width, so a wide-set axis is
// deliberately ELIGIBLE. There is no decimal128-style width gate and
// these tests exist so adding one is a visible change rather than a
// quiet regression.

// ctwSchema carries a categorical region, a 206-member set_u256 "tags"
// and a 120-member set_u128 "chans" — two independent wide set fields so
// a row fan and a column fan produce an N x M grid rather than the
// degenerate N x N a shared field would give.
func ctwSchema(t testing.TB) *encoding.Schema {
	t.Helper()
	mkDict := func(n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			if _, err := d.Add(gswLabel(i)); err != nil {
				t.Fatalf("dict.Add(%d): %v", i, err)
			}
		}
		return d
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: mkDict(2)},
			{Name: "tags", Type: encoding.FieldTypeSetU256, Dictionary: mkDict(206), Nullable: true},
			{Name: "chans", Type: encoding.FieldTypeSetU128, Dictionary: mkDict(120), Nullable: true},
			{Name: "value", Type: encoding.FieldTypeF64, Nullable: true},
		},
	}
}

// ctwRow describes one record in bit-index terms. Nil tags/chans slices
// mean an EMPTY mask (a valid "no selection"); nullTags / nullChans mean
// a genuinely null field, which resolves no axis key at all.
type ctwRow struct {
	region    uint64
	tags      []int
	chans     []int
	value     float64
	nullValue bool
	nullTags  bool
	nullChans bool
}

func (r ctwRow) build(schema *encoding.Schema) *Record {
	nulls := map[string]bool{}
	if r.nullValue {
		nulls["value"] = true
	}
	if r.nullTags {
		nulls["tags"] = true
	}
	if r.nullChans {
		nulls["chans"] = true
	}
	return NewRecordWithWide(schema,
		map[string]float64{"region": float64(r.region), "value": r.value},
		nulls,
		map[string]any{"tags": gswMask(r.tags...), "chans": gswMask(r.chans...)})
}

// ctwRows is the shared wide cohort. EVERY selected bit sits at or above
// 64 except where a low bit is needed to prove the low word still works,
// so a key derivation that lost a word would collapse most of the grid.
func ctwRows() []ctwRow {
	return []ctwRow{
		// Fan on both axes, all bits in high words: 3 tags x 2 chans.
		{region: 0, tags: []int{70, 130, 200}, chans: []int{65, 100}, value: 10},
		// Low-word bits still resolve.
		{region: 0, tags: []int{1}, chans: []int{2}, value: 20},
		// Word-boundary neighbours must not collapse together.
		{region: 1, tags: []int{64}, chans: []int{64}, value: 30},
		{region: 1, tags: []int{63}, chans: []int{63}, value: 40},
		// Empty tags mask: no row key; the column axis still resolves.
		{region: 1, tags: nil, chans: []int{65}, value: 50},
		// Null tags: same outcome as an empty mask for the row axis, but
		// via the null channel rather than a zero mask.
		{region: 1, tags: nil, chans: []int{100}, value: 60, nullTags: true},
		// Null cell value under a high-word fan -> n_null in the floor.
		{region: 0, tags: []int{130, 205}, chans: []int{119}, nullValue: true},
		// Wholly unresolved: grand margin only.
		{region: 1, tags: nil, chans: nil, value: 70, nullTags: true, nullChans: true},
	}
}

func ctwRecords(schema *encoding.Schema) []*Record {
	rows := ctwRows()
	out := make([]*Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.build(schema))
	}
	return out
}

func ctwGroup(gt types.GroupType, field string) *types.Group {
	return &types.Group{Type: gt, Field: field}
}

func ctwRequest(rows, cols *types.Group) *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{rows},
			Columns: []*types.Group{cols},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
}

// TestCanFuseCrosstab_WideSetAxesAreEligible records the deliberate
// decision: a wide-set axis FUSES. The gate reads operator shape (does
// the grouper derive a key per record) and never the field's width, so
// the answer is identical at every rung. If a width gate is ever added
// this test is the thing that must change first, with a stated reason.
func TestCanFuseCrosstab_WideSetAxesAreEligible(t *testing.T) {
	schema := ctwSchema(t)
	for _, gt := range []types.GroupType{types.GROUP_SET_VALUE, types.GROUP_SET_PER_ELEMENT} {
		for _, field := range []string{"tags", "chans"} { // set_u256, set_u128
			t.Run(string(gt)+"/"+field, func(t *testing.T) {
				// Row axis.
				req := ctwRequest(ctwGroup(gt, field), ctwGroup(types.GROUP_CATEGORY, "region"))
				if ok, reason := CanFuseCrosstab(req, schema, nil); !ok {
					t.Errorf("row axis rejected: %s", reason)
				}
				// Column axis.
				req = ctwRequest(ctwGroup(types.GROUP_CATEGORY, "region"), ctwGroup(gt, field))
				if ok, reason := CanFuseCrosstab(req, schema, nil); !ok {
					t.Errorf("column axis rejected: %s", reason)
				}
			})
		}
	}
	// Both axes wide-set at once.
	req := ctwRequest(ctwGroup(types.GROUP_SET_PER_ELEMENT, "tags"), ctwGroup(types.GROUP_SET_VALUE, "chans"))
	if ok, reason := CanFuseCrosstab(req, schema, nil); !ok {
		t.Errorf("wide set on BOTH axes rejected: %s", reason)
	}
}

// TestCrosstabWide_SetAxesFusedMatchesBuffered drives every wide-set
// axis combination down both arms. assertFusedBufferedParity asserts the
// fusion gate FIRST, so the comparison can never degrade into two
// buffered runs agreeing with each other.
func TestCrosstabWide_SetAxesFusedMatchesBuffered(t *testing.T) {
	schema := ctwSchema(t)
	recs := ctwRecords(schema)
	cases := []struct {
		name string
		rows *types.Group
		cols *types.Group
	}{
		{"per_element rows x category cols", ctwGroup(types.GROUP_SET_PER_ELEMENT, "tags"), ctwGroup(types.GROUP_CATEGORY, "region")},
		{"category rows x per_element cols", ctwGroup(types.GROUP_CATEGORY, "region"), ctwGroup(types.GROUP_SET_PER_ELEMENT, "tags")},
		{"set_value rows x category cols", ctwGroup(types.GROUP_SET_VALUE, "chans"), ctwGroup(types.GROUP_CATEGORY, "region")},
		{"category rows x set_value cols", ctwGroup(types.GROUP_CATEGORY, "region"), ctwGroup(types.GROUP_SET_VALUE, "chans")},
		{"per_element rows x per_element cols", ctwGroup(types.GROUP_SET_PER_ELEMENT, "tags"), ctwGroup(types.GROUP_SET_PER_ELEMENT, "chans")},
		{"set_value rows x set_value cols", ctwGroup(types.GROUP_SET_VALUE, "tags"), ctwGroup(types.GROUP_SET_VALUE, "chans")},
		{"per_element rows x set_value cols", ctwGroup(types.GROUP_SET_PER_ELEMENT, "tags"), ctwGroup(types.GROUP_SET_VALUE, "chans")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFusedBufferedParity(t, schema, ctwRequest(tc.rows, tc.cols), recs)
		})
	}
}

// TestCrosstabWide_PerElementRowAxisHitsGeneratedTruth is the
// non-oracle half. A parity diff between two arms shares any decode bug
// they have in common, so the cell figures here are computed by hand
// from ctwRows and asserted on BOTH arms independently.
func TestCrosstabWide_PerElementRowAxisHitsGeneratedTruth(t *testing.T) {
	schema := ctwSchema(t)
	recs := ctwRecords(schema)
	req := ctwRequest(ctwGroup(types.GROUP_SET_PER_ELEMENT, "tags"), ctwGroup(types.GROUP_CATEGORY, "region"))
	assertFusableCrosstab(t, schema, req)

	// Row keys: every tag bit selected by a non-null record.
	// bit 1, 63, 64, 70, 130, 200, 205.
	wantRows := []int{1, 63, 64, 70, 130, 200, 205}
	// (tag bit, region) -> summed value.
	wantCells := map[int]map[string]float64{
		1:   {"L000": 20},
		63:  {"L001": 40},
		64:  {"L001": 30},
		70:  {"L000": 10},
		130: {"L000": 10}, // the null-value record contributes 0 to the sum
		200: {"L000": 10},
		205: {"L000": 0},
	}

	for _, arm := range []struct {
		name string
		run  func() (*types.Response, error)
	}{
		{"buffered", func() (*types.Response, error) {
			return runBufferedCrosstabWithComponents(t, schema, req, recs, false)
		}},
		{"fused", func() (*types.Response, error) {
			return runFusedCrosstabViaRunner(t, schema, req, recs, false)
		}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			resp, err := arm.run()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			m := resp.Crosstab.Matrix
			if m == nil {
				t.Fatal("no matrix payload")
			}
			if got, want := len(m.RowKeys), len(wantRows); got != want {
				t.Fatalf("row keys = %d (%v), want %d — a lost word collapses high bits", got, m.RowKeys, want)
			}
			rowIdx := map[string]int{}
			for i, rk := range m.RowKeys {
				s, _ := rk[0].(string)
				rowIdx[s] = i
			}
			colIdx := map[string]int{}
			for i, ck := range m.ColumnKeys {
				s, _ := ck[0].(string)
				colIdx[s] = i
			}
			for _, bit := range wantRows {
				label := gswLabel(bit)
				ri, ok := rowIdx[label]
				if !ok {
					t.Fatalf("row key %q missing from %v — bit %d was truncated away", label, m.RowKeys, bit)
				}
				for region, want := range wantCells[bit] {
					ci, ok := colIdx[region]
					if !ok {
						t.Fatalf("column key %q missing from %v", region, m.ColumnKeys)
					}
					cell := m.Cells[ri][ci]
					if !cell.Present {
						t.Fatalf("(%s,%s) not present", label, region)
					}
					if got, _ := cell.Value.(float64); got != want {
						t.Errorf("(%s,%s) = %v, want %v", label, region, cell.Value, want)
					}
				}
			}
			// Margins recompute from RAW ROWS, not from the cells: under
			// fan-out the row margins over-count, so the grand total is
			// the plain sum over every filter-passing record's value
			// (nulls contribute nothing to AGG_SUM).
			if !m.GrandTotal.Present {
				t.Fatal("grand margin missing")
			}
			var wantGrand float64
			for _, r := range ctwRows() {
				if !r.nullValue {
					wantGrand += r.value
				}
			}
			if got, _ := m.GrandTotal.Value.(float64); got != wantGrand {
				t.Errorf("grand margin = %v, want %v (sum over raw rows, not over cells)", m.GrandTotal.Value, wantGrand)
			}
		})
	}
}

// TestCrosstabWide_SetValueAxisKeysCarryEveryWord pins the atomic-mask
// axis: two records whose chans masks differ ONLY above bit 64 must land
// on two different axis keys, on both arms.
func TestCrosstabWide_SetValueAxisKeysCarryEveryWord(t *testing.T) {
	schema := ctwSchema(t)
	recs := []*Record{
		ctwRow{region: 0, chans: []int{3, 100}, value: 1}.build(schema),
		ctwRow{region: 0, chans: []int{3, 101}, value: 2}.build(schema),
		ctwRow{region: 0, chans: []int{3, 100}, value: 4}.build(schema),
	}
	req := ctwRequest(ctwGroup(types.GROUP_SET_VALUE, "chans"), ctwGroup(types.GROUP_CATEGORY, "region"))
	assertFusableCrosstab(t, schema, req)

	want := map[string]float64{
		gswExpectedKey(3, 100): 5,
		gswExpectedKey(3, 101): 2,
	}
	for _, arm := range []struct {
		name string
		run  func() (*types.Response, error)
	}{
		{"buffered", func() (*types.Response, error) {
			return runBufferedCrosstabWithComponents(t, schema, req, recs, false)
		}},
		{"fused", func() (*types.Response, error) {
			return runFusedCrosstabViaRunner(t, schema, req, recs, false)
		}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			resp, err := arm.run()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			m := resp.Crosstab.Matrix
			if len(m.RowKeys) != len(want) {
				t.Fatalf("row keys = %v, want %d distinct keys", m.RowKeys, len(want))
			}
			for i, rk := range m.RowKeys {
				key, _ := rk[0].(string)
				w, known := want[key]
				if !known {
					t.Fatalf("unexpected row key %q (want %v)", key, want)
				}
				if got, _ := m.Cells[i][0].Value.(float64); got != w {
					t.Errorf("row %q = %v, want %v", key, m.Cells[i][0].Value, w)
				}
			}
		})
	}
}
