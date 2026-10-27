package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// The cell aggregator's OWN margins (row / column / grand, and every
// normalize denominator built from them — leaf, partial-depth
// normalize_level and cross-axis normalize_within) follow the cell
// admission rule: a record reaches a margin only if it reached a CELL,
// i.e. both axis keys resolved. A respondent whose GROUP_SET_PER_ELEMENT
// set is null or empty, or whose axis key a grouper Include excluded,
// was never asked the question the axis encodes, so column % is "% of
// those asked" rather than "% of everyone" (U47 #325, a recorded
// behaviour break).
//
// The assertion is differential rather than hand-computed: a crosstab
// over the admitted records plus a set of never-admitted extras must
// return byte-identical cells, margins and margin components to the same
// crosstab over the admitted records alone — on BOTH dispatch arms. That
// states the rule directly ("those rows change nothing") and covers every
// normalize mode without a table of expected percentages.

// ownMarginBase is a cohort where every record resolves both the tags and
// chans set axes and the region axis.
func ownMarginBase(schema *encoding.Schema) []*Record {
	rows := []fanoutRow{
		{region: 0, tags: tagVISA, chans: chWEB, value: 1},
		{region: 0, tags: tagMC, chans: chPOS, value: 2},
		{region: 1, tags: tagVISA | tagMC, chans: chWEB, value: 4},
		{region: 1, tags: tagAMEX, chans: chATM | chPOS, value: 8},
		{region: 0, tags: tagAMEX, chans: chATM, value: 3},
	}
	out := make([]*Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.build(schema))
	}
	return out
}

// ownMarginUnasked are records that reach no cell when tags (or chans)
// is an axis: a null set and an empty mask on each set field. Each still
// resolves every OTHER axis, so under the pre-U47 rule it would land in
// the opposite axis's margin, the grand margin and every denominator.
func ownMarginUnasked(schema *encoding.Schema, field string) []*Record {
	var rows []fanoutRow
	switch field {
	case "tags":
		rows = []fanoutRow{
			{region: 0, nullTags: true, chans: chWEB, value: 16},
			{region: 1, tags: 0, chans: chPOS, value: 32},
		}
	case "chans":
		rows = []fanoutRow{
			{region: 0, tags: tagVISA, nullChans: true, value: 64},
			{region: 1, tags: tagMC, chans: 0, value: 128},
		}
	}
	out := make([]*Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.build(schema))
	}
	return out
}

// ownMarginWire is the slice of a response the rule governs: the matrix
// (cells, normalised values, margin values, grand total) and the margin
// counts + components. IncludedRecords / ExcludedRecords and Run are
// deliberately left out — they count the extras by design.
func ownMarginWire(t *testing.T, resp *types.Response) string {
	t.Helper()
	out := map[string]any{"crosstab": resp.Crosstab, "data": resp.Data}
	if resp.Components != nil && resp.Components.Crosstab != nil {
		c := resp.Components.Crosstab
		out["row_margin_counts"] = c.RowMarginCounts
		out["row_margin_components"] = c.RowMarginComponents
		out["column_margin_counts"] = c.ColumnMarginCounts
		out["column_margin_components"] = c.ColumnMarginComponents
		out["grand_total_count"] = c.GrandTotalCount
		out["grand_total_components"] = c.GrandTotalComponents
		out["cell_counts"] = c.CellCounts
	}
	return jsonOf(t, out)
}

type ownMarginCase struct {
	name   string
	spec   func() *types.CrosstabSpec
	base   func(*encoding.Schema) []*Record
	extras func(*encoding.Schema) []*Record
}

func ownMarginCases() []ownMarginCase {
	zero := 0
	all := types.CrosstabMargins{Rows: true, Columns: true, Grand: true}
	sum := func() *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"}
	}
	tagsExtras := func(s *encoding.Schema) []*Record { return ownMarginUnasked(s, "tags") }
	chansExtras := func(s *encoding.Schema) []*Record { return ownMarginUnasked(s, "chans") }
	bothExtras := func(s *encoding.Schema) []*Record {
		return append(tagsExtras(s), chansExtras(s)...)
	}
	var cases []ownMarginCase
	for _, mode := range []types.CrosstabNormalize{
		types.CrosstabNormalizeNone, types.CrosstabNormalizeRow,
		types.CrosstabNormalizeColumn, types.CrosstabNormalizeTotal,
	} {
		cases = append(cases,
			ownMarginCase{
				name: "set on rows/" + string(mode),
				spec: func() *types.CrosstabSpec {
					return &types.CrosstabSpec{Rows: []*types.Group{setGroup("tags")},
						Columns: []*types.Group{catGroup("region")}, Cell: sum(),
						Shape: types.CrosstabShapeMatrix, Margins: all, Normalize: mode}
				},
				base: ownMarginBase, extras: tagsExtras,
			},
			ownMarginCase{
				name: "set on columns/" + string(mode),
				spec: func() *types.CrosstabSpec {
					return &types.CrosstabSpec{Rows: []*types.Group{catGroup("region")},
						Columns: []*types.Group{setGroup("tags")}, Cell: sum(),
						Shape: types.CrosstabShapeMatrix, Margins: all, Normalize: mode}
				},
				base: ownMarginBase, extras: tagsExtras,
			},
			ownMarginCase{
				name: "set on both axes/" + string(mode),
				spec: func() *types.CrosstabSpec {
					return &types.CrosstabSpec{Rows: []*types.Group{setGroup("tags")},
						Columns: []*types.Group{setGroup("chans")}, Cell: sum(),
						Shape: types.CrosstabShapeMatrix, Margins: all, Normalize: mode}
				},
				base: ownMarginBase, extras: bothExtras,
			},
			ownMarginCase{
				// The Include-excluded key: south respondents resolve the
				// chans axis but are excluded on the row axis.
				name: "include-excluded row key/" + string(mode),
				spec: func() *types.CrosstabSpec {
					return &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"north"}}},
						Columns: []*types.Group{setGroup("chans")}, Cell: sum(),
						Shape: types.CrosstabShapeMatrix, Margins: all, Normalize: mode}
				},
				base: func(s *encoding.Schema) []*Record {
					var out []*Record
					for _, r := range ownMarginBase(s) {
						if v, _ := r.NumericValue("region"); v == 0 {
							out = append(out, r)
						}
					}
					return out
				},
				extras: func(s *encoding.Schema) []*Record {
					var out []*Record
					for _, r := range ownMarginBase(s) {
						if v, _ := r.NumericValue("region"); v == 1 {
							out = append(out, r)
						}
					}
					return out
				},
			},
		)
	}
	cases = append(cases,
		ownMarginCase{
			// Partial-depth: the region prefix denominator must not count
			// a respondent whose deeper tags position never resolved.
			name: "normalize_level row prefix",
			spec: func() *types.CrosstabSpec {
				return &types.CrosstabSpec{
					Rows:    []*types.Group{catGroup("region"), setGroup("tags")},
					Columns: []*types.Group{setGroup("chans")}, Cell: sum(),
					Shape: types.CrosstabShapeMatrix, Margins: all,
					Normalize: types.CrosstabNormalizeRow, NormalizeLevel: &zero}
			},
			base: ownMarginBase, extras: tagsExtras,
		},
		ownMarginCase{
			name: "normalize_level column prefix",
			spec: func() *types.CrosstabSpec {
				return &types.CrosstabSpec{
					Rows:    []*types.Group{setGroup("chans")},
					Columns: []*types.Group{catGroup("region"), setGroup("tags")}, Cell: sum(),
					Shape: types.CrosstabShapeMatrix, Margins: all,
					Normalize: types.CrosstabNormalizeColumn, NormalizeLevel: &zero}
			},
			base: ownMarginBase, extras: tagsExtras,
		},
		ownMarginCase{
			// Cross-axis: the (region prefix, chans) denominator.
			name: "normalize_within",
			spec: func() *types.CrosstabSpec {
				return &types.CrosstabSpec{
					Rows:    []*types.Group{catGroup("region"), setGroup("tags")},
					Columns: []*types.Group{setGroup("chans")}, Cell: sum(),
					Shape: types.CrosstabShapeMatrix, Margins: all,
					Normalize: types.CrosstabNormalizeColumn, NormalizeWithin: &zero}
			},
			base: ownMarginBase, extras: tagsExtras,
		},
		ownMarginCase{
			name: "long shape margins",
			spec: func() *types.CrosstabSpec {
				return &types.CrosstabSpec{Rows: []*types.Group{setGroup("tags")},
					Columns: []*types.Group{catGroup("region")}, Cell: sum(),
					Shape: types.CrosstabShapeLong, Margins: all}
			},
			base: ownMarginBase, extras: tagsExtras,
		},
		ownMarginCase{
			// A count cell: the margin count is the column % base itself.
			name: "count cell",
			spec: func() *types.CrosstabSpec {
				return &types.CrosstabSpec{Rows: []*types.Group{setGroup("tags")},
					Columns: []*types.Group{setGroup("chans")},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
					Shape:   types.CrosstabShapeMatrix, Margins: all,
					Normalize: types.CrosstabNormalizeColumn}
			},
			base: ownMarginBase, extras: bothExtras,
		},
	)
	return cases
}

// TestCrosstab_OwnMarginsFollowCellAdmission is the #325 acceptance test:
// a null-set, an empty-set and an Include-excluded respondent change no
// margin and no normalised cell, on either arm.
func TestCrosstab_OwnMarginsFollowCellAdmission(t *testing.T) {
	t.Parallel()

	for _, tc := range ownMarginCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema := fanoutCrosstabSchema(t)
			base := tc.base(schema)
			extras := tc.extras(schema)
			if len(extras) == 0 {
				t.Fatal("fixture drift: no unasked records")
			}
			withExtras := append(append([]*Record{}, base...), extras...)
			req := &types.Request{Crosstab: tc.spec()}
			assertFusableCrosstab(t, schema, req)

			arms := map[string]func([]*Record) (*types.Response, error){
				"buffered": func(recs []*Record) (*types.Response, error) {
					return runBufferedCrosstabWithComponents(t, schema, req, recs, false)
				},
				"fused": func(recs []*Record) (*types.Response, error) {
					return runFusedCrosstabViaRunner(t, schema, req, recs, false)
				},
			}
			for arm, run := range arms {
				want, err := run(base)
				if err != nil {
					t.Fatalf("%s over admitted records: %v", arm, err)
				}
				got, err := run(withExtras)
				if err != nil {
					t.Fatalf("%s with unasked records: %v", arm, err)
				}
				if w, g := ownMarginWire(t, want), ownMarginWire(t, got); w != g {
					t.Errorf("%s: unasked records moved a margin or a normalised cell "+
						"(a record reaches a margin only if it reached a cell)\n"+
						"admitted only: %s\nwith unasked:  %s", arm, w, g)
				}
			}
		})
	}
}

// TestCrosstab_OwnMarginAdmissionFusedMatchesBuffered drives the same
// corpus (admitted + unasked records together) down both arms and diffs
// the whole wire form — the TestCrosstab_BufferedAuxMarginMatchesFused
// pattern for the cell's own margins. A differential test alone passes
// when both arms are wrong in the same direction; this one catches the
// arms disagreeing.
func TestCrosstab_OwnMarginAdmissionFusedMatchesBuffered(t *testing.T) {
	t.Parallel()

	for _, tc := range ownMarginCases() {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			recs := append(tc.base(schema), tc.extras(schema)...)
			assertFusedBufferedParity(t, schema, &types.Request{Crosstab: tc.spec()}, recs)
		})
	}
	t.Run("shared fan-out corpus", func(t *testing.T) {
		schema := fanoutCrosstabSchema(t)
		for _, mode := range []types.CrosstabNormalize{
			types.CrosstabNormalizeNone, types.CrosstabNormalizeRow,
			types.CrosstabNormalizeColumn, types.CrosstabNormalizeTotal,
		} {
			req := &types.Request{Crosstab: &types.CrosstabSpec{
				Rows:      []*types.Group{setGroup("tags")},
				Columns:   []*types.Group{setGroup("chans")},
				Cell:      &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:     types.CrosstabShapeMatrix,
				Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Normalize: mode,
			}}
			assertFusedBufferedParity(t, schema, req, fanoutRecords(schema))
		}
	})
}
