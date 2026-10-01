package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// distinctMarginSchema backs the union-vs-sum margin fixture: a brand
// row axis, an audience column axis, and the respondent id the cell
// aggregator dedupes on.
func distinctMarginSchema() *encoding.Schema {
	brandDict := encoding.NewDictionary()
	brandDict.Add("alpha")
	brandDict.Add("beta")
	brandDict.Add("gamma")

	audienceDict := encoding.NewDictionary()
	audienceDict.Add("core")
	audienceDict.Add("affluent")

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: brandDict},
			{Name: "audience", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: audienceDict},
			{Name: "respondent", Type: encoding.FieldTypeU32, ByteOffset: 2, CsvColumnIdx: 2},
		},
	}
}

// distinctMarginRecords is a deliberately OVERLAPPING fixture: the same
// respondent answers for several brands, which is the whole point — a
// margin summed from cells double-counts every shared respondent.
//
//	                 core                 affluent
//	alpha       {1,2,3,4}  -> 4           {1,2}  -> 2
//	beta          {3,4,5}  -> 3             {2}  -> 1
//	gamma         {5,6,7}  -> 3                  -> (empty)
//
//	column margin core     = |{1..7}| = 7   (sum of cells would be 10)
//	column margin affluent = |{1,2}|  = 2   (sum of cells would be  3)
//	row    margin alpha    = |{1,2,3,4}| = 4 (sum of cells would be 6)
//	grand  margin          = |{1..7}| = 7   (sum of cells would be 13)
func distinctMarginRecords() [][]uint64 {
	return [][]uint64{
		{0, 0, 1}, {0, 0, 2}, {0, 0, 3}, {0, 0, 4},
		{1, 0, 3}, {1, 0, 4}, {1, 0, 5},
		{2, 0, 5}, {2, 0, 6}, {2, 0, 7},
		{0, 1, 1}, {0, 1, 2},
		{1, 1, 2},
	}
}

// TestCrosstab_DistinctCountMarginIsUnionNotCellSum is the regression
// test protecting the MarginIndependent classification.
//
// AGG_DISTINCT_COUNT was declared MarginSummable, which is
// mathematically wrong — summing per-cell distinct counts double-counts
// every key present in more than one cell. The engine has always
// ignored the label: both crosstab paths accumulate row / column /
// grand margins in INDEPENDENT aggregator instances fed record by
// record, so the margin that comes back is the true union. That is what
// licenses the fourth class (MarginIndependent, admitted by the fused
// gate) over the honest-sounding MarginRecompute, which would keep the
// same numbers and silently cost the fused path.
//
// The measured Orbit-side counterpart on the live Visa cohort is a
// column margin of 109,013 distinct respondents against a cell sum of
// 673,270 (research/weighted-distinct-feasibility.md §10). That cohort
// is not reproducible here, so the fixture above reproduces the
// PROPERTY at hand-checkable scale: every asserted figure is the union
// and every "never" figure is the cell sum.
//
// Both dispatch arms are driven, because the classification is a fused-
// gate token and a regression that moved the request to the buffered
// path would leave a fused-only assertion green.
func TestCrosstab_DistinctCountMarginIsUnionNotCellSum(t *testing.T) {
	schema := distinctMarginSchema()
	cfg := setupTestFS(t, "distinct.pulse", schema, distinctMarginRecords())
	ctx := context.Background()

	newReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "distinct.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "audience"}},
				Cell:    &types.Aggregation{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "uniques"},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		}
	}

	// Non-vacuity: the fused arm below is only meaningful while the gate
	// admits this request. If MarginIndependent ever falls out of the
	// admitted set, both sub-tests would silently exercise the buffered
	// path and still pass.
	if ok, reason := processing.CanFuseCrosstab(newReq(), schema, nil); !ok {
		t.Fatalf("AGG_DISTINCT_COUNT crosstab declined fusion: %q — MarginIndependent must stay admitted by processing.CanFuseCrosstab", reason)
	}

	for _, path := range []struct {
		name       string
		disableFus bool
	}{
		{"fused", false},
		{"buffered", true},
	} {
		t.Run(path.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(path.disableFus)

			resp, err := svc.Process(ctx, newReq())
			if err != nil {
				t.Fatalf("Process crosstab: %v", err)
			}
			matrix := resp.Crosstab.Matrix

			// Cells first — they are exact today and must stay so.
			for _, want := range []struct {
				brand, audience string
				n               float64
			}{
				{"alpha", "core", 4}, {"beta", "core", 3}, {"gamma", "core", 3},
				{"alpha", "affluent", 2}, {"beta", "affluent", 1},
			} {
				ri, okR := indexOfKey(matrix.RowKeys, want.brand)
				ci, okC := indexOfKey(matrix.ColumnKeys, want.audience)
				if !okR || !okC {
					t.Fatalf("missing axis key (%s, %s)", want.brand, want.audience)
				}
				if got := matrix.Cells[ri][ci].Scalar(); !floatClose(got, want.n, 0.001) {
					t.Errorf("cell (%s, %s) = %v, want %v", want.brand, want.audience, got, want.n)
				}
			}

			// Column margins: the union, never the sum of cells.
			for _, want := range []struct {
				audience        string
				union, neverSum float64
			}{
				{"core", 7, 10},
				{"affluent", 2, 3},
			} {
				ci, ok := indexOfKey(matrix.ColumnKeys, want.audience)
				if !ok {
					t.Fatalf("missing column key %q", want.audience)
				}
				got := matrix.ColumnMargins[ci].Scalar()
				if floatClose(got, want.neverSum, 0.001) {
					t.Errorf("column margin (%s) = %v — that is the SUM of cells; the margin must be the union (%v)", want.audience, got, want.union)
				}
				if !floatClose(got, want.union, 0.001) {
					t.Errorf("column margin (%s) = %v, want %v (distinct respondents in the column)", want.audience, got, want.union)
				}
			}

			// Row margin: alpha's two cells sum to 6; the union is 4.
			ri, ok := indexOfKey(matrix.RowKeys, "alpha")
			if !ok {
				t.Fatal("missing row key alpha")
			}
			if got := matrix.RowMargins[ri].Scalar(); floatClose(got, 6, 0.001) {
				t.Errorf("row margin (alpha) = %v — that is the SUM of cells; want the union 4", got)
			} else if !floatClose(got, 4, 0.001) {
				t.Errorf("row margin (alpha) = %v, want 4", got)
			}

			// Grand margin: 13 cells summed, 7 distinct respondents.
			if !matrix.GrandTotal.Present {
				t.Fatal("grand margin not present")
			}
			if got := matrix.GrandTotal.Scalar(); floatClose(got, 13, 0.001) {
				t.Errorf("grand margin = %v — that is the SUM of cells; want the union 7", got)
			} else if !floatClose(got, 7, 0.001) {
				t.Errorf("grand margin = %v, want 7", got)
			}
		})
	}
}
