package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// ---------------------------------------------------------------------
// Fixture: two independent set fields so a fan on the row axis and a fan
// on the column axis produce genuinely different key sets (N x M rather
// than the degenerate N x N a shared field would give).
// ---------------------------------------------------------------------

// fanoutCrosstabSchema carries a categorical "region", two set fields
// ("tags" 4 labels, "chans" 3 labels) and an f64 "value" cell target.
func fanoutCrosstabSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	mkDict := func(entries ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, e := range entries {
			if _, err := d.Add(e); err != nil {
				t.Fatalf("dict.Add(%q): %v", e, err)
			}
		}
		return d
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: mkDict("north", "south")},
			{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: mkDict("VISA", "MC", "AMEX", "DISC"), Nullable: true},
			{Name: "chans", Type: encoding.FieldTypeSetU8, Dictionary: mkDict("WEB", "POS", "ATM"), Nullable: true},
			{Name: "value", Type: encoding.FieldTypeF64, Nullable: true},
		},
	}
}

// Bit positions in the two set dictionaries.
const (
	tagVISA = 1 << 0
	tagMC   = 1 << 1
	tagAMEX = 1 << 2
	tagDISC = 1 << 3

	chWEB = 1 << 0
	chPOS = 1 << 1
	chATM = 1 << 2
)

// fanoutRecord builds one record. A negative value marks the cell field
// null so the {n, n_null} universal floor is exercised under fan-out;
// nullTags / nullChans mark the corresponding set field null (distinct
// from an empty mask, which is a valid "no selection").
type fanoutRow struct {
	region    uint64
	tags      uint64
	chans     uint64
	value     float64
	nullValue bool
	nullTags  bool
	nullChans bool
}

func (r fanoutRow) build(schema *encoding.Schema) *Record {
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
		map[string]any{"tags": r.tags, "chans": r.chans})
}

// fanoutRecords is the shared cohort: multi-label rows (the fan actually
// fires), single-label rows, an empty mask, a null set, a null cell
// value, and both-axis fans.
func fanoutRecords(schema *encoding.Schema) []*Record {
	rows := []fanoutRow{
		// Three tags x two channels -> 6 cells from one record.
		{region: 0, tags: tagVISA | tagMC | tagAMEX, chans: chWEB | chPOS, value: 10},
		// Single label on both axes -> one cell.
		{region: 0, tags: tagVISA, chans: chWEB, value: 20},
		// Empty tags mask: no selection, row axis does not resolve.
		{region: 1, tags: 0, chans: chATM, value: 30},
		// Null tags: row axis does not resolve, column axis still does.
		{region: 1, tags: 0, chans: chPOS, value: 40, nullTags: true},
		// Null chans: column axis does not resolve, row axis still does.
		{region: 1, tags: tagDISC, chans: 0, value: 50, nullChans: true},
		// Both axes fan, null cell value -> n_null under fan-out.
		{region: 1, tags: tagMC | tagDISC, chans: chWEB | chATM, nullValue: true},
		// Two tags, three channels -> 6 cells.
		{region: 0, tags: tagAMEX | tagDISC, chans: chWEB | chPOS | chATM, value: 7},
		// Wholly unresolved record: contributes to the grand margin only.
		{region: 1, tags: 0, chans: 0, value: 60, nullTags: true, nullChans: true},
	}
	out := make([]*Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.build(schema))
	}
	return out
}

func setGroup(field string) *types.Group {
	return &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: field}
}

func catGroup(field string) *types.Group {
	return &types.Group{Type: types.GROUP_CATEGORY, Field: field}
}

// assertFusedBufferedParity runs one request down both paths over the
// same records and asserts the wire forms agree. Buffered is the oracle:
// the fused path is contracted to be byte-equal, so the assertion is
// against the buffered output rather than hand-computed numbers.
//
// The fusion gate is asserted FIRST (E2-S4): a request the gate rejects
// would take the buffered arm in production, so a parity assertion over
// it proves nothing — the comparison has to be against a request that
// really does fuse. Overlays and Warnings are diffed alongside the
// matrix so the E1 fold stays covered under fan-out.
func assertFusedBufferedParity(t *testing.T, schema *encoding.Schema, req *types.Request, recs []*Record) {
	t.Helper()
	assertFusableCrosstab(t, schema, req)
	bufResp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("buffered RunCrosstab: %v", err)
	}
	fusedResp, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("fused RunCrosstabFused: %v", err)
	}
	if want, got := jsonOf(t, bufResp.Crosstab), jsonOf(t, fusedResp.Crosstab); want != got {
		t.Errorf("Crosstab diverges:\nbuffered: %s\nfused:    %s", want, got)
	}
	if want, got := jsonOf(t, bufResp.Data), jsonOf(t, fusedResp.Data); want != got {
		t.Errorf("Data diverges:\nbuffered: %s\nfused:    %s", want, got)
	}
	if want, got := jsonOf(t, bufResp.Components), jsonOf(t, fusedResp.Components); want != got {
		t.Errorf("Components diverge:\nbuffered: %s\nfused:    %s", want, got)
	}
	if want, got := jsonOf(t, bufResp.Overlays), jsonOf(t, fusedResp.Overlays); want != got {
		t.Errorf("Overlays diverge:\nbuffered: %s\nfused:    %s", want, got)
	}
	if want, got := jsonOf(t, bufResp.Warnings), jsonOf(t, fusedResp.Warnings); want != got {
		t.Errorf("Warnings diverge:\nbuffered: %s\nfused:    %s", want, got)
	}
	assertMetadataEqual(t, bufResp.Metadata, fusedResp.Metadata)
}

// assertFusableCrosstab fails the test unless the fusion gate admits
// the request. Every parity assertion in this package funnels through
// it so a comparison can never silently degrade into two buffered runs
// agreeing with each other.
func assertFusableCrosstab(t *testing.T, schema *encoding.Schema, req *types.Request) {
	t.Helper()
	if ok, reason := CanFuseCrosstab(req, schema, nil); !ok {
		t.Fatalf("CanFuseCrosstab rejected the request under test: %s", reason)
	}
}

// TestFusedCrosstab_FanOutRoutingMatchesBuffered is the E2-S3 oracle
// gate. Every case puts at least one GROUP_SET_PER_ELEMENT position on
// an axis so the cartesian fan fires, and asserts the fused response is
// byte-equal to the buffered one — cells, margins, normalization
// denominators and the {n, n_null} universal floor alike.
func TestFusedCrosstab_FanOutRoutingMatchesBuffered(t *testing.T) {
	zero := 0
	cases := []struct {
		name string
		spec *types.CrosstabSpec
	}{
		{
			name: "row axis fans, column axis single-key",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "column axis fans, row axis single-key",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "both axes fan out",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "fan behind a single-key position",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region"), setGroup("tags")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "fan ahead of a single-key position",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags"), catGroup("region")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "fan with Include filtering",
			spec: &types.CrosstabSpec{
				Rows: []*types.Group{
					{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags", Include: []string{"MC", "VISA"}},
				},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "normalize row at leaf with fanning rows",
			spec: &types.CrosstabSpec{
				Rows:      []*types.Group{setGroup("tags")},
				Columns:   []*types.Group{catGroup("region")},
				Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:     types.CrosstabShapeMatrix,
				Normalize: types.CrosstabNormalizeRow,
				Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Partial-depth denominator whose level sits BEHIND the fan:
			// the prefix (region) is shared by every leaf key the record
			// fans into, so the partial margin must count the record once,
			// not once per label.
			name: "normalize_level prefix ahead of the fan",
			spec: &types.CrosstabSpec{
				Rows:           []*types.Group{catGroup("region"), setGroup("tags")},
				Columns:        []*types.Group{catGroup("region")},
				Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:          types.CrosstabShapeMatrix,
				Normalize:      types.CrosstabNormalizeRow,
				NormalizeLevel: &zero,
				Margins:        types.CrosstabMargins{Rows: true},
			},
		},
		{
			// Partial-depth denominator whose level IS the fanning
			// position: the record lands in one partial bucket per label.
			name: "normalize_level prefix at the fanning position",
			spec: &types.CrosstabSpec{
				Rows:           []*types.Group{setGroup("tags"), catGroup("region")},
				Columns:        []*types.Group{catGroup("region")},
				Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:          types.CrosstabShapeMatrix,
				Normalize:      types.CrosstabNormalizeRow,
				NormalizeLevel: &zero,
				Margins:        types.CrosstabMargins{Rows: true},
			},
		},
		{
			name: "normalize_within with a fanning row axis",
			spec: &types.CrosstabSpec{
				Rows:            []*types.Group{setGroup("tags")},
				Columns:         []*types.Group{catGroup("region"), setGroup("chans")},
				Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:           types.CrosstabShapeMatrix,
				Normalize:       types.CrosstabNormalizeRow,
				NormalizeWithin: &zero,
				Margins:         types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "normalize_within with both axes fanning",
			spec: &types.CrosstabSpec{
				Rows:            []*types.Group{setGroup("tags")},
				Columns:         []*types.Group{setGroup("chans")},
				Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:           types.CrosstabShapeMatrix,
				Normalize:       types.CrosstabNormalizeColumn,
				NormalizeWithin: &zero,
				Margins:         types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "long shape with a fanning axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "value", Label: "avg"},
				Shape:   types.CrosstabShapeLong,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			recs := fanoutRecords(schema)
			req := &types.Request{Crosstab: tc.spec}
			assertFusedBufferedParity(t, schema, req, recs)
		})
	}
}

// TestFusedCrosstab_FanOutSingleRecordProducesFullProduct pins the
// routing arithmetic without going through the buffered oracle: one
// record selecting 3 tags and 2 channels must land in 3x2 = 6 distinct
// cells, feed 3 row margins and 2 column margins, and hit the grand
// margin exactly once.
func TestFusedCrosstab_FanOutSingleRecordProducesFullProduct(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	rec := fanoutRow{region: 0, tags: tagVISA | tagMC | tagAMEX, chans: chWEB | chPOS, value: 5}.build(schema)
	spec := &types.CrosstabSpec{
		Rows:    []*types.Group{setGroup("tags")},
		Columns: []*types.Group{setGroup("chans")},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}
	state, err := NewFusedCrosstabState(spec, schema, &ExtensionRegistry{})
	if err != nil {
		t.Fatalf("NewFusedCrosstabState: %v", err)
	}
	state.AddTotalRow()
	if err := state.Update(rec); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got, want := len(state.rowKeys), 3; got != want {
		t.Errorf("interned row keys = %d, want %d (%v)", got, want, state.rowKeys)
	}
	if got, want := len(state.colKeys), 2; got != want {
		t.Errorf("interned column keys = %d, want %d (%v)", got, want, state.colKeys)
	}
	// Every (row, col) slot must be addressable and carry exactly one
	// record — the matrix must not be ragged after multi-key interning.
	total := 0
	for r := range state.cellCounts {
		if got, want := len(state.cellCounts[r]), len(state.colKeys); got != want {
			t.Fatalf("cellCounts row %d width = %d, want %d (ragged matrix)", r, got, want)
		}
		if got, want := len(state.cells[r]), len(state.colKeys); got != want {
			t.Fatalf("cells row %d width = %d, want %d (ragged matrix)", r, got, want)
		}
		if got, want := len(state.cellNNull[r]), len(state.colKeys); got != want {
			t.Fatalf("cellNNull row %d width = %d, want %d (ragged matrix)", r, got, want)
		}
		for c := range state.cellCounts[r] {
			if state.cellCounts[r][c] != 1 {
				t.Errorf("cellCounts[%d][%d] = %d, want 1", r, c, state.cellCounts[r][c])
			}
			total += state.cellCounts[r][c]
		}
	}
	if want := 6; total != want {
		t.Errorf("sum(cellCounts) = %d, want %d", total, want)
	}
	if state.includedRecords != 6 {
		t.Errorf("includedRecords = %d, want 6", state.includedRecords)
	}
	for r, n := range state.rowMarginCount {
		if n != 1 {
			t.Errorf("rowMarginCount[%d] = %d, want 1", r, n)
		}
	}
	if got, want := len(state.rowMarginCount), 3; got != want {
		t.Errorf("len(rowMarginCount) = %d, want %d", got, want)
	}
	for c, n := range state.colMarginCount {
		if n != 1 {
			t.Errorf("colMarginCount[%d] = %d, want 1", c, n)
		}
	}
	if got, want := len(state.colMarginCount), 2; got != want {
		t.Errorf("len(colMarginCount) = %d, want %d", got, want)
	}
	// Grand margin is once per record regardless of fan-out. Row margins
	// therefore do NOT sum to the grand total on a fanning axis — that
	// non-additivity is what buffered produces and is deliberate.
	if state.grandMarginCount != 1 {
		t.Errorf("grandMarginCount = %d, want 1 (once per record)", state.grandMarginCount)
	}
}

// TestFusedCrosstab_FanOutMarginNonAdditivity documents the wart in an
// executable form: on a fanning axis the row margins over-count relative
// to the grand margin, and the fused path must reproduce that rather
// than "fix" it — otherwise the same request returns different numbers
// depending on which path it took.
func TestFusedCrosstab_FanOutMarginNonAdditivity(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	recs := []*Record{
		fanoutRow{region: 0, tags: tagVISA | tagMC | tagAMEX, chans: chWEB, value: 1}.build(schema),
	}
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{setGroup("tags")},
		Columns: []*types.Group{catGroup("region")},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Grand: true},
	}}
	bufResp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}
	fusedResp, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("fused: %v", err)
	}
	sumRowMargins := func(m *types.MatrixPayload) float64 {
		var total float64
		for _, cell := range m.RowMargins {
			if cell.Present {
				total += coerceFloat64(cell.Value)
			}
		}
		return total
	}
	bufSum := sumRowMargins(bufResp.Crosstab.Matrix)
	fusedSum := sumRowMargins(fusedResp.Crosstab.Matrix)
	if bufSum != fusedSum {
		t.Fatalf("row-margin sums diverge: buffered %v fused %v", bufSum, fusedSum)
	}
	if bufSum != 3 {
		t.Fatalf("expected the 3-label record counted 3x across row margins, got %v", bufSum)
	}
	grand := fusedResp.Crosstab.Matrix.GrandTotal
	if !grand.Present || coerceFloat64(grand.Value) != 1 {
		t.Fatalf("grand total = %+v, want the record counted exactly once", grand)
	}
}

// TestFusedCrosstab_FanOutPerAxisNullityIndependence pins the per-axis
// nullity rule under fan-out: a record whose row axis fans but whose
// column axis is unresolved still feeds every row margin it fans into
// and the grand margin, and lands in no cell.
func TestFusedCrosstab_FanOutPerAxisNullityIndependence(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	recs := []*Record{
		// Row axis fans 2-way, column set is null.
		fanoutRow{region: 0, tags: tagVISA | tagMC, nullChans: true, value: 3}.build(schema),
		// Column axis fans 2-way, row set is null.
		fanoutRow{region: 0, chans: chWEB | chPOS, nullTags: true, value: 4}.build(schema),
	}
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{setGroup("tags")},
		Columns: []*types.Group{setGroup("chans")},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}}
	assertFusedBufferedParity(t, schema, req, recs)

	state, err := NewFusedCrosstabState(req.Crosstab, schema, &ExtensionRegistry{})
	if err != nil {
		t.Fatalf("NewFusedCrosstabState: %v", err)
	}
	for _, r := range recs {
		state.AddTotalRow()
		if err := state.Update(r); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	if state.includedRecords != 0 {
		t.Errorf("includedRecords = %d, want 0 (no record resolved both axes)", state.includedRecords)
	}
	if got, want := len(state.rowMarginCount), 2; got != want {
		t.Errorf("row margins = %d, want %d", got, want)
	}
	for r, n := range state.rowMarginCount {
		if n != 1 {
			t.Errorf("rowMarginCount[%d] = %d, want 1", r, n)
		}
	}
	for c, n := range state.colMarginCount {
		if n != 1 {
			t.Errorf("colMarginCount[%d] = %d, want 1", c, n)
		}
	}
	if state.grandMarginCount != 2 {
		t.Errorf("grandMarginCount = %d, want 2", state.grandMarginCount)
	}
}

// TestFusedCrosstab_PartialDepthMarginGatesOnPrefixDepth pins the
// partial-depth (normalize_level) denominator rule the E2-S3 routing
// table states: the margin updates once per deduped prefix key AT ITS OWN
// DEPTH, so a record whose prefix positions all resolve feeds it even
// when a DEEPER position is null and the record therefore lands in no
// leaf row bucket. That is what buffered does — its denominator is
// PartitionByAxis(spec.Rows[:level+1], filtered), which never looks past
// level — and gating the fused update on full-axis resolution instead
// shrinks the denominator and inflates every normalized cell.
//
// Both a single-key axis and a fanning axis are covered: the rule is
// about depth, not about fan-out, and single-key is where it regressed.
func TestFusedCrosstab_PartialDepthMarginGatesOnPrefixDepth(t *testing.T) {
	zero := 0
	cases := []struct {
		name string
		rows []*types.Group
		recs func(*encoding.Schema) []*Record
	}{
		{
			name: "single-key leaf unresolved",
			rows: []*types.Group{
				catGroup("region"),
				{Type: types.GROUP_RANGE, Field: "value", Interval: 10},
			},
			recs: func(schema *encoding.Schema) []*Record {
				return []*Record{
					fanoutRow{region: 0, value: 10}.build(schema),
					fanoutRow{region: 0, value: 20}.build(schema),
					// Null "value" leaves the leaf position unresolved but
					// the region prefix still bucketed on the buffered path.
					fanoutRow{region: 0, nullValue: true}.build(schema),
				}
			},
		},
		{
			name: "fanning leaf unresolved",
			rows: []*types.Group{
				catGroup("region"),
				setGroup("tags"),
			},
			recs: func(schema *encoding.Schema) []*Record {
				return []*Record{
					fanoutRow{region: 0, tags: tagVISA | tagMC, value: 10}.build(schema),
					fanoutRow{region: 0, tags: tagAMEX, value: 20}.build(schema),
					// Null set: the leaf fan resolves to nothing, but the
					// region prefix partition still holds this record.
					fanoutRow{region: 0, nullTags: true, value: 30}.build(schema),
					// Empty mask: same story, no selection.
					fanoutRow{region: 0, tags: 0, value: 40}.build(schema),
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			req := &types.Request{Crosstab: &types.CrosstabSpec{
				Rows:           tc.rows,
				Columns:        []*types.Group{catGroup("region")},
				Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:          types.CrosstabShapeMatrix,
				Normalize:      types.CrosstabNormalizeRow,
				NormalizeLevel: &zero,
				Margins:        types.CrosstabMargins{Rows: true},
			}}
			assertFusedBufferedParity(t, schema, req, tc.recs(schema))
		})
	}
}

// TestFusedCrosstab_SingleKeyUnaffectedByFanOutRouting is the
// byte-identity control for the epic: with no multi-key grouper anywhere
// the per-key routing loops degenerate to exactly one iteration each, so
// every single-key request must keep matching buffered. Reuses the
// canonical (region, segment, value) fixture the pre-E2 fused tests use.
func TestFusedCrosstab_SingleKeyUnaffectedByFanOutRouting(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecords(schema)
	zero := 0
	cases := []struct {
		name string
		spec *types.CrosstabSpec
	}{
		{
			name: "plain matrix with every margin",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region")},
				Columns: []*types.Group{catGroup("segment")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "normalize_level on a two-position row axis",
			spec: &types.CrosstabSpec{
				Rows:           []*types.Group{catGroup("region"), catGroup("segment")},
				Columns:        []*types.Group{catGroup("segment")},
				Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:          types.CrosstabShapeMatrix,
				Normalize:      types.CrosstabNormalizeRow,
				NormalizeLevel: &zero,
				Margins:        types.CrosstabMargins{Rows: true},
			},
		},
		{
			name: "normalize_within",
			spec: &types.CrosstabSpec{
				Rows:            []*types.Group{catGroup("region")},
				Columns:         []*types.Group{catGroup("segment")},
				Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:           types.CrosstabShapeMatrix,
				Normalize:       types.CrosstabNormalizeRow,
				NormalizeWithin: &zero,
				Margins:         types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFusedBufferedParity(t, schema, &types.Request{Crosstab: tc.spec}, recs)
		})
	}
}
