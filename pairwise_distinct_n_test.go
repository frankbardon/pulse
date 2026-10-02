package pulse

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// End-to-end coverage for OVERLAY_PAIRWISE_* n_source=n_within_distinct
// over a memmap cohort shaped like the filed defect:
//
//   - a TWO-LEVEL row axis — GROUP_SET_PER_ELEMENT on a multi-select
//     `brand` (the FAN-OUT outer level) then GROUP_CATEGORY on the
//     single-response `segment` (the inner level);
//   - MORE THAN ONE RECORD PER RESPONDENT, so the per-cell record count
//     is an inflated respondent count;
//   - an AGG_DISTINCT_SUM cell keyed on `respondent`, which is what puts
//     a true distinct-KEY cardinality on CellComponents.
//
// The failure being pinned is silent: `n_within` sums CellCounts across
// the slab and returns 6 where the respondent base is 3, so every
// pairwise p-value renders, correctly formatted, off a doubled n.
//
// Fixture (each respondent contributes TWO identical records):
//
//	id   brand            segment  wave  value
//	101  {acme, zenith}   urban    w1    30
//	102  {acme}           urban    w1    50
//	103  {acme}           rural    w1    40
//	104  {zenith}         urban    w1    40
//	105  {zenith}         rural    w2    90
//
// At column w1, with the slab prefix fixing `brand` (n_within_depth=0):
//
//	brand=acme   rows (acme,urban) n=4 d=2 + (acme,rural) n=2 d=1 -> n=6 d=3
//	brand=zenith rows (zenith,urban) n=4 d=2 + (zenith,rural) absent -> n=4 d=2
//
// 101 lands in BOTH brand slabs — that is the fan-out, and it is legal
// here because it sits INSIDE the fixed prefix: it multiplies slabs, not
// cells, so each slab stays a partition of its own respondents.
const (
	pwDistinctCohort = "pairwise_distinct.pulse"
	pwDistinctStride = 15
)

type pwDistinctRow struct {
	respondent uint32
	value      float64
	brandMask  uint8
	segment    uint8
	wave       uint8
}

// pwDistinctFixtureRows returns the 10 records (5 respondents × 2).
// Bit 0 of brandMask is "acme", bit 1 is "zenith"; segment 0 = urban,
// 1 = rural; wave 0 = w1, 1 = w2.
func pwDistinctFixtureRows() []pwDistinctRow {
	base := []pwDistinctRow{
		{respondent: 101, value: 30, brandMask: 0b11, segment: 0, wave: 0},
		{respondent: 102, value: 50, brandMask: 0b01, segment: 0, wave: 0},
		{respondent: 103, value: 40, brandMask: 0b01, segment: 1, wave: 0},
		{respondent: 104, value: 40, brandMask: 0b10, segment: 0, wave: 0},
		{respondent: 105, value: 90, brandMask: 0b10, segment: 1, wave: 1},
	}
	out := make([]pwDistinctRow, 0, len(base)*2)
	for _, r := range base {
		out = append(out, r, r)
	}
	return out
}

// writePairwiseDistinctCohort lays the fixture out byte by byte rather
// than through the importer: the record stride and the set bitmask are
// the properties under test on the decode side, so the fixture must be
// an independent oracle.
func writePairwiseDistinctCohort(t *testing.T, memFs afero.Fs) {
	t.Helper()

	brandDict := encoding.NewDictionary()
	for _, b := range []string{"acme", "zenith"} {
		if _, err := brandDict.Add(b); err != nil {
			t.Fatalf("brandDict.Add(%q): %v", b, err)
		}
	}
	segmentDict := encoding.NewDictionary()
	for _, s := range []string{"urban", "rural"} {
		if _, err := segmentDict.Add(s); err != nil {
			t.Fatalf("segmentDict.Add(%q): %v", s, err)
		}
	}
	waveDict := encoding.NewDictionary()
	for _, w := range []string{"w1", "w2"} {
		if _, err := waveDict.Add(w); err != nil {
			t.Fatalf("waveDict.Add(%q): %v", w, err)
		}
	}

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "respondent", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 4},
		{Name: "brand", Type: encoding.FieldTypeSetU8, ByteOffset: 12, Dictionary: brandDict},
		{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 13, Dictionary: segmentDict},
		{Name: "wave", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 14, Dictionary: waveDict},
	}}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range pwDistinctFixtureRows() {
		rec := make([]byte, pwDistinctStride)
		binary.LittleEndian.PutUint32(rec[0:4], r.respondent)
		binary.LittleEndian.PutUint64(rec[4:12], math.Float64bits(r.value))
		rec[12] = r.brandMask
		rec[13] = r.segment
		rec[14] = r.wave
		buf.Write(rec)
	}
	if err := afero.WriteFile(memFs, pwDistinctCohort, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// pwDistinctRequest is the two-level-row-axis crosstab with an
// AGG_DISTINCT_SUM cell keyed on respondent. nSource empty leaves the
// overlay off entirely.
func pwDistinctRequest(nSource string) *Request {
	req := &Request{
		Cohort: &types.Cohort{Filename: pwDistinctCohort},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_SET_PER_ELEMENT, Field: "brand"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}},
			Cell: &types.Aggregation{
				Type:   types.AGG_DISTINCT_SUM,
				Field:  "value",
				Label:  "distinct_value",
				Params: json.RawMessage(`{"distinct_by":"respondent"}`),
			},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	if nSource != "" {
		req.Overlays = []types.OverlaySpec{{
			Name:   "pw",
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  types.OverlayScopeRow,
			Params: json.RawMessage(`{"n_source":"` + nSource + `","n_within_depth":0}`),
		}}
	}
	return req
}

func pwDistinctRowIndex(t *testing.T, mx *types.MatrixPayload, brand, segment string) int {
	t.Helper()
	for i, k := range mx.RowKeys {
		if len(k) == 2 && k[0] == brand && k[1] == segment {
			return i
		}
	}
	t.Fatalf("row key (%s, %s) not found in %v", brand, segment, mx.RowKeys)
	return -1
}

func pwDistinctColumnIndex(t *testing.T, mx *types.MatrixPayload, wave string) int {
	t.Helper()
	for i, k := range mx.ColumnKeys {
		if len(k) == 1 && k[0] == wave {
			return i
		}
	}
	t.Fatalf("column key (%s) not found in %v", wave, mx.ColumnKeys)
	return -1
}

// TestPairwiseNWithinDistinct_MemmapSlabLegs asserts BOTH legs under
// BOTH modes on the real cohort: n_within reports the inflated record
// count, n_within_distinct the respondent count.
func TestPairwiseNWithinDistinct_MemmapSlabLegs(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Process(context.Background(), pwDistinctRequest(""))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected a MATRIX crosstab payload")
	}
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatal("expected Response.Components.Crosstab")
	}
	mx := resp.Crosstab.Matrix
	// A one-slot compose view builds the same components-bearing MATRIX
	// host the crosstab overlay fold does.
	host := processing.NewComposeHostView([]*types.Response{resp}).Slot(0).Matrix()

	agg, key, admitted := host.AdmitsDistinctKeyN()
	if !admitted {
		t.Fatalf("AGG_DISTINCT_SUM cell host not admitted (identified as %q)", agg)
	}
	if agg != types.AGG_DISTINCT_SUM || key != "distinct_count" {
		t.Fatalf("AdmitsDistinctKeyN = (%q, %q), want (AGG_DISTINCT_SUM, distinct_count)", agg, key)
	}

	w1 := pwDistinctColumnIndex(t, mx, "w1")
	acmeUrban := pwDistinctRowIndex(t, mx, "acme", "urban")
	zenithUrban := pwDistinctRowIndex(t, mx, "zenith", "urban")

	cases := []struct {
		name         string
		row          int
		wantRecords  int
		wantDistinct int
	}{
		{"acme leg", acmeUrban, 6, 3},
		{"zenith leg", zenithUrban, 4, 2},
	}
	for _, tc := range cases {
		gotRecords, ok := host.RowSlabN(tc.row, w1, 1)
		if !ok {
			t.Fatalf("%s: RowSlabN not ok", tc.name)
		}
		if gotRecords != tc.wantRecords {
			t.Errorf("%s: n_within slab = %d records, want %d", tc.name, gotRecords, tc.wantRecords)
		}
		gotDistinct, ok := host.RowSlabDistinctN(tc.row, w1, 1)
		if !ok {
			t.Fatalf("%s: RowSlabDistinctN not ok", tc.name)
		}
		if gotDistinct != tc.wantDistinct {
			t.Errorf("%s: n_within_distinct slab = %d respondents, want %d", tc.name, gotDistinct, tc.wantDistinct)
		}
		if gotRecords == gotDistinct {
			t.Errorf("%s: record count and respondent count agree (%d) — the fixture no longer inflates", tc.name, gotRecords)
		}
	}
}

// TestPairwiseNWithinDistinct_MemmapPValues drives the same cohort
// through the overlay both ways and checks each p-value against an
// INDEPENDENT pooled two-proportion z written here, so the assertion
// does not borrow the implementation it is testing.
func TestPairwiseNWithinDistinct_MemmapPValues(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pairP := func(nSource string) float64 {
		t.Helper()
		resp, err := p.Process(context.Background(), pwDistinctRequest(nSource))
		if err != nil {
			t.Fatalf("Process(%s): %v", nSource, err)
		}
		if len(resp.Overlays) != 1 {
			t.Fatalf("Process(%s): expected 1 overlay layer, got %d", nSource, len(resp.Overlays))
		}
		omx := resp.Overlays[0].Payload.Matrix
		if omx == nil {
			t.Fatalf("Process(%s): nil overlay matrix", nSource)
		}
		hostMx := resp.Crosstab.Matrix
		w1 := pwDistinctColumnIndex(t, hostMx, "w1")
		wantA := hostMx.RowKeys[pwDistinctRowIndex(t, hostMx, "acme", "urban")]
		wantB := hostMx.RowKeys[pwDistinctRowIndex(t, hostMx, "zenith", "urban")]
		labelA, labelB := wantA[0].(string)+"|"+wantA[1].(string), wantB[0].(string)+"|"+wantB[1].(string)
		for r, k := range omx.RowKeys {
			if len(k) == 2 && k[0] == labelA && k[1] == labelB {
				cell := omx.Cells[r][w1]
				if !cell.Present {
					t.Fatalf("Process(%s): pair (%s, %s) cell absent", nSource, labelA, labelB)
				}
				v, _ := cell.Value.(float64)
				return v
			}
		}
		t.Fatalf("Process(%s): pair (%s, %s) not emitted; keys %v", nSource, labelA, labelB, omx.RowKeys)
		return 0
	}

	// Cell values: (acme,urban) sums the FIRST value per distinct
	// respondent — 30 + 50 = 80; (zenith,urban) 30 + 40 = 70. p_source
	// defaults to cell_value_pct, so the proportions are 0.80 and 0.70.
	gotWithin := pairP(types.PairwiseNSourceNWithin)
	gotDistinct := pairP(types.PairwiseNSourceNWithinDistinct)

	wantWithin := pwPooledPropZ(0.80, 6, 0.70, 4)
	wantDistinct := pwPooledPropZ(0.80, 3, 0.70, 2)

	if math.Abs(gotWithin-wantWithin) > 1e-9 {
		t.Errorf("n_within p = %v, want %v (legs 6 and 4 records)", gotWithin, wantWithin)
	}
	if math.Abs(gotDistinct-wantDistinct) > 1e-9 {
		t.Errorf("n_within_distinct p = %v, want %v (legs 3 and 2 respondents)", gotDistinct, wantDistinct)
	}
	if math.Abs(gotWithin-gotDistinct) < 1e-9 {
		t.Fatal("both modes produced the same p-value; the distinct leg is not reaching the slab")
	}
}

// pwPooledPropZ is a deliberately independent pooled-SE two-proportion
// z two-sided p-value, written from the textbook formula so the
// assertions above do not reuse processing.twoProportionZ.
func pwPooledPropZ(p1 float64, n1 int, p2 float64, n2 int) float64 {
	n1f, n2f := float64(n1), float64(n2)
	pooled := (p1*n1f + p2*n2f) / (n1f + n2f)
	se := math.Sqrt(pooled * (1 - pooled) * (1/n1f + 1/n2f))
	z := (p1 - p2) / se
	return math.Erfc(math.Abs(z) / math.Sqrt2)
}

// TestPairwiseNWithinDistinct_MemmapRefusesFrequencyCell is the
// admission refusal on a REAL cohort: same shape, AGG_FREQUENCY cell.
// Its "distinct_count" component counts distinct answer VALUES, and a
// key-presence gate would happily report it as a respondent base.
func TestPairwiseNWithinDistinct_MemmapRefusesFrequencyCell(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := pwDistinctRequest(types.PairwiseNSourceNWithinDistinct)
	req.Crosstab.Cell = &types.Aggregation{
		Type:  types.AGG_FREQUENCY,
		Field: "segment",
		Label: "freq_segment",
	}
	_, err = p.Process(context.Background(), req)
	if err == nil {
		t.Fatal("expected an AGG_FREQUENCY cell host to be refused")
	}
	msg := err.Error()
	for _, want := range []string{
		string(types.AGG_FREQUENCY),
		string(types.AGG_DISTINCT_SUM),
		string(types.AGG_DISTINCT_COUNT),
	} {
		if !bytes.Contains([]byte(msg), []byte(want)) {
			t.Errorf("refusal %q does not name %q", msg, want)
		}
	}
}
