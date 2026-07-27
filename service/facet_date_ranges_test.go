package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestFacet_FilterDateRanges_Integration proves FILTER_DATE_RANGES is
// auto-available to the facet path via FacetRequest.Filterers with no
// facet-specific wiring: once row-local streamable-registered, the filter
// narrows the accumulated record set single-pass. The cohort spans three
// dates; a launch+growth range set keeps two and drops the third.
func TestFacet_FilterDateRanges_Integration(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "created", Type: encoding.FieldTypeDate, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{1, epochDays(t, "2006-01-02", "2024-02-01")}, // launch — kept
		{2, epochDays(t, "2006-01-02", "2024-05-01")}, // growth — kept
		{3, epochDays(t, "2006-01-02", "2023-01-01")}, // pre-range — dropped
	}
	cfg := setupTestFS(t, "cohort.pulse", schema, records)
	svc := New(cfg)

	params := []byte(`{"ranges":[` +
		`{"label":"launch","start":"2024-01-01","end":"2024-03-31"},` +
		`{"label":"growth","start":"2024-04-01","end":"2024-09-30"}]}`)

	res, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Fields: []string{"id"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_DATE_RANGES, Field: "created", Params: params},
		},
	})
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if res.FilteredRecords != 2 {
		t.Fatalf("FilteredRecords = %d, want 2 (launch+growth kept, pre-range dropped)", res.FilteredRecords)
	}
	if res.TotalRecords != 3 {
		t.Errorf("TotalRecords = %d, want 3", res.TotalRecords)
	}
}
