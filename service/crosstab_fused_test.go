package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstabFused_Dispatch_RoutesEligibleToFused confirms that an
// eligible crosstab request — count cell, two streamable groupers, no
// margins, no normalization — is accepted by the gate and would route
// through the fused path. We assert via the gate's predicate directly
// because the dispatch swap is intentionally a silent runtime
// optimisation; the behavioural assertion happens in
// TestCrosstabFused_EquivalenceVsBuffered below.
func TestCrosstabFused_Dispatch_RoutesEligibleToFused(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}

	ok, reason := processing.CanFuseCrosstab(req, schema, svc.Extensions())
	if !ok {
		t.Fatalf("CanFuseCrosstab rejected eligible request: %s", reason)
	}

	// Execute end-to-end with fusion enabled (default). Failure here
	// means the fused dispatch is broken even on the simplest case.
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process (fused): %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatalf("fused crosstab missing Matrix payload")
	}
	if len(resp.Crosstab.Matrix.RowKeys) == 0 {
		t.Fatalf("fused crosstab produced zero row keys")
	}
}

// TestCrosstabFused_Dispatch_RoutesIneligibleToBuffered confirms a
// request with a non-mergeable cell aggregator (AGG_MEDIAN) is
// rejected by the gate so it stays on the buffered RunCrosstab path.
// The fused path can't satisfy median's finalize-time sort, so the
// gate's rejection is load-bearing.
func TestCrosstabFused_Dispatch_RoutesIneligibleToBuffered(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_MEDIAN, Field: "value", Label: "med"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}

	if ok, reason := processing.CanFuseCrosstab(req, schema, svc.Extensions()); ok {
		t.Fatalf("CanFuseCrosstab accepted AGG_MEDIAN request that should fall back to buffered; reason=%q", reason)
	}

	// End-to-end execution must still succeed via the buffered path.
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process (buffered fallback): %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatalf("buffered crosstab missing Matrix payload")
	}
}

// TestCrosstabFused_EquivalenceVsBuffered is the byte-equal correctness
// gate. Build a synthetic wide cohort, run the same eligible request
// twice — once with fusion forced off, once with fusion engaged — and
// assert the two MatrixPayloads / Metadata blocks match field-for-field.
// Failure here means the fused path diverges from the buffered RunCrosstab
// for a request the gate accepts; the regression invalidates the entire
// fusion optimisation because byte-equivalence is the load-bearing
// contract.
func TestCrosstabFused_EquivalenceVsBuffered(t *testing.T) {
	schema := wideCrosstabSchema(t, 16)
	records := wideCrosstabRecords(16)
	cfg := setupTestFS(t, "wide.pulse", schema, records)
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "wide.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		}
	}

	svcBuf := New(cfg)
	svcBuf.SetDisableCrosstabFusion(true)
	bufResp, err := svcBuf.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (buffered): %v", err)
	}

	svcFused := New(cfg)
	// Fusion enabled by default; assert the gate accepts the request.
	if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected the equivalence request: %s", reason)
	}
	fusedResp, err := svcFused.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (fused): %v", err)
	}

	if bufResp.Crosstab == nil || bufResp.Crosstab.Matrix == nil {
		t.Fatalf("buffered response missing Matrix payload")
	}
	if fusedResp.Crosstab == nil || fusedResp.Crosstab.Matrix == nil {
		t.Fatalf("fused response missing Matrix payload")
	}

	if !reflect.DeepEqual(bufResp.Crosstab.Matrix.RowKeys, fusedResp.Crosstab.Matrix.RowKeys) {
		t.Errorf("RowKeys differ:\n buffered=%v\n fused   =%v",
			bufResp.Crosstab.Matrix.RowKeys, fusedResp.Crosstab.Matrix.RowKeys)
	}
	if !reflect.DeepEqual(bufResp.Crosstab.Matrix.ColumnKeys, fusedResp.Crosstab.Matrix.ColumnKeys) {
		t.Errorf("ColumnKeys differ:\n buffered=%v\n fused   =%v",
			bufResp.Crosstab.Matrix.ColumnKeys, fusedResp.Crosstab.Matrix.ColumnKeys)
	}
	if !reflect.DeepEqual(bufResp.Crosstab.Matrix.Cells, fusedResp.Crosstab.Matrix.Cells) {
		t.Errorf("Cells differ:\n buffered=%v\n fused   =%v",
			bufResp.Crosstab.Matrix.Cells, fusedResp.Crosstab.Matrix.Cells)
	}
	if !reflect.DeepEqual(bufResp.Crosstab.Matrix.RowMargins, fusedResp.Crosstab.Matrix.RowMargins) {
		t.Errorf("RowMargins differ:\n buffered=%v\n fused   =%v",
			bufResp.Crosstab.Matrix.RowMargins, fusedResp.Crosstab.Matrix.RowMargins)
	}
	if !reflect.DeepEqual(bufResp.Crosstab.Matrix.ColumnMargins, fusedResp.Crosstab.Matrix.ColumnMargins) {
		t.Errorf("ColumnMargins differ:\n buffered=%v\n fused   =%v",
			bufResp.Crosstab.Matrix.ColumnMargins, fusedResp.Crosstab.Matrix.ColumnMargins)
	}
	if !reflect.DeepEqual(bufResp.Crosstab.Matrix.GrandTotal, fusedResp.Crosstab.Matrix.GrandTotal) {
		t.Errorf("GrandTotal differs:\n buffered=%v\n fused   =%v",
			bufResp.Crosstab.Matrix.GrandTotal, fusedResp.Crosstab.Matrix.GrandTotal)
	}
	if bufResp.Metadata == nil || fusedResp.Metadata == nil {
		t.Fatalf("metadata missing: buffered=%v fused=%v", bufResp.Metadata, fusedResp.Metadata)
	}
	if bufResp.Metadata.TotalRows != fusedResp.Metadata.TotalRows {
		t.Errorf("TotalRows differ: buffered=%d fused=%d",
			bufResp.Metadata.TotalRows, fusedResp.Metadata.TotalRows)
	}
	if bufResp.Metadata.FilteredRows != fusedResp.Metadata.FilteredRows {
		t.Errorf("FilteredRows differ: buffered=%d fused=%d",
			bufResp.Metadata.FilteredRows, fusedResp.Metadata.FilteredRows)
	}
	if bufResp.Metadata.CohortFile != fusedResp.Metadata.CohortFile {
		t.Errorf("CohortFile differs: buffered=%q fused=%q",
			bufResp.Metadata.CohortFile, fusedResp.Metadata.CohortFile)
	}
}
