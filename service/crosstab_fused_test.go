package service

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
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

// setupHugeRequestCohort writes the same 200-field × 10K-row synth
// cohort that drives the canonical perf bench to an in-memory FS. The
// 10K-row scale is small enough for tests to run in well under a
// second while still exercising the full fused/buffered code paths
// against the canonical request shape (categorical row × quarter+
// categorical cols × AGG_SUM weight × all margins × normalize=row ×
// normalize_within=0). Returns the FS config + the schema so callers
// can build requests against the same field names.
func setupHugeRequestCohort(t *testing.T) (*fs.Config, *encoding.Schema) {
	t.Helper()
	const (
		fieldCount = 200
		rowCount   = 10_000
	)
	schema, payload := buildWideCohort(t, fieldCount, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "huge.pulse", buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg, schema
}

// hugeRequestSpec builds the canonical huge-request.json-shaped
// CrosstabSpec: brand row × (quarter waveDate + cardFeeling) cols ×
// AGG_SUM(weight) × all margins × normalize=row × normalize_within=0.
// This is the load-bearing equivalence shape — the fused path must
// produce byte-equal output to the buffered path on this exact spec
// or the canonical 1M-row bench numbers are meaningless.
func hugeRequestSpec() *types.CrosstabSpec {
	fiscalOffset := -3
	return &types.CrosstabSpec{
		Rows: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "brand"},
		},
		Columns: []*types.Group{
			{
				Type:   types.GROUP_DATE,
				Field:  "waveDate",
				Params: dateParams("quarter", &fiscalOffset),
			},
			{Type: types.GROUP_CATEGORY, Field: "cardFeeling"},
		},
		Cell: &types.Aggregation{
			Type:  types.AGG_SUM,
			Field: "weight",
			Label: "sum_weight",
		},
		Margins:         types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		Shape:           types.CrosstabShapeMatrix,
		Normalize:       types.CrosstabNormalizeRow,
		NormalizeWithin: intPtr(0),
	}
}

// runFusedAndBuffered drives the same request through (a) the fused
// dispatch (default) and (b) the buffered path forced via
// SetDisableCrosstabFusion(true). Asserts the gate accepts the
// request when fusable is true so we don't silently regress the
// gate's reach. Each path gets a fresh Service so the toggle is
// honoured per-call.
func runFusedAndBuffered(
	t *testing.T,
	cfg *fs.Config,
	schema *encoding.Schema,
	buildReq func() *types.Request,
	fusable bool,
) (bufResp, fusedResp *types.Response) {
	t.Helper()
	ctx := context.Background()

	if fusable {
		if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, nil); !ok {
			t.Fatalf("CanFuseCrosstab rejected expected-fusable request: %s", reason)
		}
	} else {
		if ok, _ := processing.CanFuseCrosstab(buildReq(), schema, nil); ok {
			t.Fatalf("CanFuseCrosstab accepted expected-non-fusable request")
		}
	}

	svcBuf := New(cfg)
	svcBuf.SetDisableCrosstabFusion(true)
	bufResp, err := svcBuf.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (buffered): %v", err)
	}

	svcFused := New(cfg)
	svcFused.SetDisableCrosstabFusion(false)
	fusedResp, err = svcFused.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (fused-default): %v", err)
	}
	return bufResp, fusedResp
}

// assertMatrixByteEqual deep-compares two MatrixPayloads field-for-
// field. Used by the E2E equivalence golden to assert byte-equal
// emission on every field that downstream consumers (Prism, terminal
// grid, the JSON envelope) read.
func assertMatrixByteEqual(t *testing.T, buf, fused *types.MatrixPayload) {
	t.Helper()
	if buf == nil || fused == nil {
		t.Fatalf("matrix payload nil: buffered=%v fused=%v", buf, fused)
	}
	if !reflect.DeepEqual(buf.RowHeader, fused.RowHeader) {
		t.Errorf("RowHeader differs:\n buffered=%+v\n fused   =%+v", buf.RowHeader, fused.RowHeader)
	}
	if !reflect.DeepEqual(buf.ColumnHeader, fused.ColumnHeader) {
		t.Errorf("ColumnHeader differs:\n buffered=%+v\n fused   =%+v", buf.ColumnHeader, fused.ColumnHeader)
	}
	if !reflect.DeepEqual(buf.RowKeys, fused.RowKeys) {
		t.Errorf("RowKeys differ:\n buffered=%v\n fused   =%v", buf.RowKeys, fused.RowKeys)
	}
	if !reflect.DeepEqual(buf.ColumnKeys, fused.ColumnKeys) {
		t.Errorf("ColumnKeys differ:\n buffered=%v\n fused   =%v", buf.ColumnKeys, fused.ColumnKeys)
	}
	if !reflect.DeepEqual(buf.Cells, fused.Cells) {
		t.Errorf("Cells differ:\n buffered=%v\n fused   =%v", buf.Cells, fused.Cells)
	}
	if !reflect.DeepEqual(buf.RowMargins, fused.RowMargins) {
		t.Errorf("RowMargins differ:\n buffered=%v\n fused   =%v", buf.RowMargins, fused.RowMargins)
	}
	if !reflect.DeepEqual(buf.ColumnMargins, fused.ColumnMargins) {
		t.Errorf("ColumnMargins differ:\n buffered=%v\n fused   =%v", buf.ColumnMargins, fused.ColumnMargins)
	}
	if !reflect.DeepEqual(buf.GrandTotal, fused.GrandTotal) {
		t.Errorf("GrandTotal differs:\n buffered=%v\n fused   =%v", buf.GrandTotal, fused.GrandTotal)
	}
	if buf.CellLabel != fused.CellLabel {
		t.Errorf("CellLabel differs: buffered=%q fused=%q", buf.CellLabel, fused.CellLabel)
	}
	if buf.NormalizeApplied != fused.NormalizeApplied {
		t.Errorf("NormalizeApplied differs: buffered=%q fused=%q", buf.NormalizeApplied, fused.NormalizeApplied)
	}
}

// TestCrosstabFused_E2EEquivalence_HugeRequestShape is the canonical
// equivalence golden. Runs the huge-request.json-shaped crosstab
// (categorical row × quarter+categorical cols × AGG_SUM × all margins
// × normalize=row × normalize_within=0) through the fused and
// buffered service paths against the same 200-field × 10K-row synth
// cohort and asserts byte-equal MatrixPayload across RowKeys,
// ColumnKeys, every Cell, all margins, normalize values, headers,
// and cell label. Failure here invalidates the entire fusion
// optimisation: the canonical 1M-row bench numbers depend on this
// exact shape staying correct.
func TestCrosstabFused_E2EEquivalence_HugeRequestShape(t *testing.T) {
	cfg, schema := setupHugeRequestCohort(t)
	buildReq := func() *types.Request {
		return &types.Request{
			Cohort:   &types.Cohort{Filename: "huge.pulse"},
			Crosstab: hugeRequestSpec(),
		}
	}
	bufResp, fusedResp := runFusedAndBuffered(t, cfg, schema, buildReq, true)
	assertMatrixByteEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)
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
}

// TestCrosstabFused_E2EEquivalence_AggCount swaps the canonical
// AGG_SUM cell for AGG_COUNT while keeping the same wide cohort and
// dual-grouper column axis. AGG_COUNT is the simplest mergeable
// summable aggregator — used as the smoke test that the dispatch
// stays equivalent across cell-aggregator changes.
func TestCrosstabFused_E2EEquivalence_AggCount(t *testing.T) {
	cfg, schema := setupHugeRequestCohort(t)
	buildReq := func() *types.Request {
		spec := hugeRequestSpec()
		spec.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "weight", Label: "n"}
		// AGG_COUNT denominator interacts with normalize/margins
		// independently of AGG_SUM so leave the rest of the spec
		// (margins, normalize, normalize_within) intact.
		return &types.Request{
			Cohort:   &types.Cohort{Filename: "huge.pulse"},
			Crosstab: spec,
		}
	}
	bufResp, fusedResp := runFusedAndBuffered(t, cfg, schema, buildReq, true)
	assertMatrixByteEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)
}

// TestCrosstabFused_E2EEquivalence_NormalizeColumnWithin0 mirrors the
// canonical axis: instead of normalize=row + normalize_within=0
// (fixing the outer column prefix), it normalizes by column +
// normalize_within=0 (fixing the row-axis prefix — but the row axis
// has only one grouper, so within=0 selects the full row tuple).
// The mirror-axis path is symmetric in code with the canonical path;
// regressing one path without the other should surface here.
func TestCrosstabFused_E2EEquivalence_NormalizeColumnWithin0(t *testing.T) {
	cfg, schema := setupHugeRequestCohort(t)
	buildReq := func() *types.Request {
		spec := hugeRequestSpec()
		spec.Normalize = types.CrosstabNormalizeColumn
		// Keep NormalizeWithin=0 — but the row axis has 1 grouper,
		// so within=0 fixes the full row prefix. The buffered and
		// fused paths must agree on this corner.
		return &types.Request{
			Cohort:   &types.Cohort{Filename: "huge.pulse"},
			Crosstab: spec,
		}
	}
	bufResp, fusedResp := runFusedAndBuffered(t, cfg, schema, buildReq, true)
	assertMatrixByteEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)
}

// TestCrosstabFused_E2EEquivalence_LongShape exercises the long-form
// emission path on the canonical cohort. Long-shape with every margin
// tag enabled emits cell rows + per-row-margin rows + per-column-
// margin rows + grand-margin row, each tagged with _margin=row|column|
// grand or _margin_within=… when partial-depth normalization applies.
// Both paths must emit the same multiset of rows (canonical sort key
// comparison handles map-iteration nondeterminism).
func TestCrosstabFused_E2EEquivalence_LongShape(t *testing.T) {
	cfg, schema := setupHugeRequestCohort(t)
	buildReq := func() *types.Request {
		spec := hugeRequestSpec()
		spec.Shape = types.CrosstabShapeLong
		// Long shape doesn't carry a MatrixPayload; the data rows
		// land on Response.Data. Long shape with all margins is the
		// most data-row-dense variant — surfaces any per-row
		// emission divergence.
		return &types.Request{
			Cohort:   &types.Cohort{Filename: "huge.pulse"},
			Crosstab: spec,
		}
	}
	bufResp, fusedResp := runFusedAndBuffered(t, cfg, schema, buildReq, true)

	if bufResp.Crosstab == nil || fusedResp.Crosstab == nil {
		t.Fatalf("missing Crosstab on response: buffered=%v fused=%v", bufResp.Crosstab, fusedResp.Crosstab)
	}
	if bufResp.Crosstab.Shape != fusedResp.Crosstab.Shape {
		t.Errorf("Shape differs: buffered=%q fused=%q", bufResp.Crosstab.Shape, fusedResp.Crosstab.Shape)
	}
	if bufResp.Crosstab.Matrix != nil || fusedResp.Crosstab.Matrix != nil {
		t.Error("expected Matrix nil on long-shape result")
	}
	if len(bufResp.Data) != len(fusedResp.Data) {
		t.Errorf("long-shape row count differs: buffered=%d fused=%d", len(bufResp.Data), len(fusedResp.Data))
	}
	// Sort both row sequences by stable JSON key so map iteration
	// order doesn't blow up the comparison.
	want := canonicaliseDataRows(t, bufResp.Data)
	got := canonicaliseDataRows(t, fusedResp.Data)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("long-shape rows differ\n want=%v\n got =%v", want, got)
	}
}

// TestCrosstabFused_E2EEquivalence_NormalizeLevelNestedRow exercises
// partial-depth normalization on a nested row axis. NormalizeLevel=0
// truncates the row axis to its parent grouper as the denominator —
// each (parent-row, col) slab's cells sum to 1.0 within the parent
// row. The interplay with multi-grouper rows is where the buffered
// and fused paths historically drifted; covering it here keeps the
// regression net wide.
func TestCrosstabFused_E2EEquivalence_NormalizeLevelNestedRow(t *testing.T) {
	cfg, schema := setupHugeRequestCohort(t)
	zero := 0
	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "huge.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "brand"},
					{Type: types.GROUP_CATEGORY, Field: "cardFeeling"},
				},
				Columns: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "cardFeeling"},
				},
				Cell: &types.Aggregation{
					Type:  types.AGG_SUM,
					Field: "weight",
					Label: "sum_weight",
				},
				Shape:          types.CrosstabShapeMatrix,
				Normalize:      types.CrosstabNormalizeRow,
				NormalizeLevel: &zero,
			},
		}
	}
	bufResp, fusedResp := runFusedAndBuffered(t, cfg, schema, buildReq, true)
	assertMatrixByteEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)
}

// TestCrosstabFused_E2EEquivalence_NonFusableAggMedian asserts that
// AGG_MEDIAN cell — which forces a finalize-time sort over the raw
// per-cell record set — is rejected by the fusion gate and that the
// buffered path still produces a correct (non-empty) MatrixPayload.
// The non-fusable variant tests are guard rails: they prove the gate
// declines the right requests AND the buffered fallback keeps
// working when the gate declines.
func TestCrosstabFused_E2EEquivalence_NonFusableAggMedian(t *testing.T) {
	cfg, schema := setupHugeRequestCohort(t)
	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "huge.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cardFeeling"}},
				Cell: &types.Aggregation{
					Type:  types.AGG_MEDIAN,
					Field: "weight",
					Label: "med_weight",
				},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}
	// AGG_MEDIAN must be rejected by the gate. Both buffered and
	// fused-default executions take the same buffered path — they
	// must produce identical output.
	bufResp, fusedResp := runFusedAndBuffered(t, cfg, schema, buildReq, false)
	if bufResp.Crosstab == nil || bufResp.Crosstab.Matrix == nil {
		t.Fatalf("buffered AGG_MEDIAN response missing Matrix payload")
	}
	if fusedResp.Crosstab == nil || fusedResp.Crosstab.Matrix == nil {
		t.Fatalf("fused-default AGG_MEDIAN response missing Matrix payload")
	}
	// Same buffered code path on both sides — output must match.
	assertMatrixByteEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)
}

// TestCrosstabFused_E2EEquivalence_NonFusableWithTests asserts that
// a request carrying req.Tests is rejected by the fusion gate and
// the buffered path runs the tests correctly. Stat tests fold over
// the buffered row set after aggregation; the fused path can't
// satisfy that without buffering, so the gate rejection is
// load-bearing. Uses TEST_ANOVA_F (k-way comparison) because the
// brand axis has 16 distinct values; TEST_T would reject k>2 groups.
// We don't deeply assert test math here — the statistical-testing
// test suite owns that — only that the buffered path returns a
// Response with Tests populated and the same MatrixPayload as the
// fused-default execution (which fell back to the same buffered
// code path).
func TestCrosstabFused_E2EEquivalence_NonFusableWithTests(t *testing.T) {
	cfg, schema := setupHugeRequestCohort(t)
	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "huge.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cardFeeling"}},
				Cell: &types.Aggregation{
					Type:  types.AGG_SUM,
					Field: "weight",
					Label: "sum_weight",
				},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			Tests: []*types.Test{
				{
					Type:    types.TEST_ANOVA_F,
					Field:   "weight",
					SplitBy: "brand",
					Label:   "f_weight_by_brand",
				},
			},
		}
	}
	bufResp, fusedResp := runFusedAndBuffered(t, cfg, schema, buildReq, false)
	if bufResp.Crosstab == nil || bufResp.Crosstab.Matrix == nil {
		t.Fatalf("buffered tests-request response missing Matrix payload")
	}
	if fusedResp.Crosstab == nil || fusedResp.Crosstab.Matrix == nil {
		t.Fatalf("fused-default tests-request response missing Matrix payload")
	}
	// Both paths ran the buffered code (gate rejected fusion).
	assertMatrixByteEqual(t, bufResp.Crosstab.Matrix, fusedResp.Crosstab.Matrix)
	// Tests slot is populated on both sides — both executed the
	// post-crosstab tier-1 test pass on the buffered record set.
	if len(bufResp.Tests) == 0 {
		t.Errorf("buffered Tests empty; expected TEST_T result")
	}
	if len(fusedResp.Tests) == 0 {
		t.Errorf("fused-default Tests empty; expected TEST_T result")
	}
}

// canonicaliseDataRows serialises each row to a canonical JSON
// representation so a stable sort makes long-shape comparison robust
// to map-iteration nondeterminism. The fused and buffered long-shape
// emitters both write deterministic columns — sorting by the JSON
// key alphabet preserves the equality invariant without forcing
// either emitter to commit to a row order.
func canonicaliseDataRows(t *testing.T, rows []map[string]any) []string {
	t.Helper()
	out := make([]string, len(rows))
	for i, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("canonicaliseDataRows: marshal row %d: %v", i, err)
		}
		out[i] = string(b)
	}
	// In-place sort using a stable comparator.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// TestCrosstabFused_OverlaysRouteFusedAndMatchBuffered is the E1-S1
// end-to-end check. Before E1-S1 the gate carried an
// "overlays force buffered" arm, so an overlay-carrying crosstab never
// reached processCrosstabFused. It now does, and the fused response
// must carry the same Response.Overlays / Response.Warnings the
// buffered service produces for the identical request.
func TestCrosstabFused_OverlaysRouteFusedAndMatchBuffered(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "ct.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			Overlays: []types.OverlaySpec{{
				Name:  "row_index",
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			}},
		}
	}

	svcFused := New(cfg)
	// The dispatch condition itself: the gate must now admit overlays.
	if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected overlay-carrying request: %s", reason)
	}
	fusedResp, err := svcFused.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (fused): %v", err)
	}

	svcBuf := New(cfg)
	svcBuf.SetDisableCrosstabFusion(true)
	bufResp, err := svcBuf.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (buffered): %v", err)
	}

	if len(fusedResp.Overlays) != 1 {
		t.Fatalf("fused response carries %d overlay layers, want 1", len(fusedResp.Overlays))
	}

	marshal := func(v any) string {
		b, mErr := json.Marshal(v)
		if mErr != nil {
			t.Fatalf("json.Marshal: %v", mErr)
		}
		return string(b)
	}
	if want, got := marshal(bufResp.Overlays), marshal(fusedResp.Overlays); want != got {
		t.Errorf("Overlays diverge:\n buffered=%s\n fused   =%s", want, got)
	}
	if want, got := marshal(bufResp.Warnings), marshal(fusedResp.Warnings); want != got {
		t.Errorf("Warnings diverge:\n buffered=%s\n fused   =%s", want, got)
	}
	if want, got := marshal(bufResp.Crosstab), marshal(fusedResp.Crosstab); want != got {
		t.Errorf("Crosstab host payload diverges:\n buffered=%s\n fused   =%s", want, got)
	}
}

// weightedCrosstabSchema is crosstabSchema plus an f64 weight column so
// AGG_WEIGHTED_MEAN has a Params.weight_field target. Byte offsets are
// explicit to mirror the sibling fixtures in this package.
func weightedCrosstabSchema() *encoding.Schema {
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	regionDict.Add("east")

	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: segmentDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 10, CsvColumnIdx: 3},
		},
	}
}

// weightedCrosstabRecords puts three (value, weight) observations in each
// of the six (region, segment) cells. Values stay inside 0..100 so the
// default PAIRWISE_PROP_Z p_source (cell value as a percentage) is a
// legal proportion; weights vary per cell so sum_weights is not constant.
func weightedCrosstabRecords() [][]uint64 {
	type obs struct {
		region, segment uint64
		value, weight   float64
	}
	fixture := []obs{
		{0, 0, 40, 1}, {0, 0, 50, 3}, {0, 0, 60, 2},
		{0, 1, 20, 2}, {0, 1, 30, 1}, {0, 1, 25, 5},
		{1, 0, 55, 4}, {1, 0, 65, 1}, {1, 0, 60, 3},
		{1, 1, 35, 1}, {1, 1, 45, 2}, {1, 1, 40, 4},
		{2, 0, 70, 2}, {2, 0, 80, 2}, {2, 0, 75, 1},
		{2, 1, 10, 3}, {2, 1, 15, 1}, {2, 1, 12, 2},
	}
	out := make([][]uint64, 0, len(fixture))
	for _, o := range fixture {
		out = append(out, []uint64{
			o.region, o.segment,
			math.Float64bits(o.value), math.Float64bits(o.weight),
		})
	}
	return out
}

// TestCrosstabFused_PairwisePropZOverWeightedMeanEndToEnd is the E1-S2
// service-layer companion to
// processing.TestFusedCrosstab_PairwisePropZOverWeightedMeanMatchesBuffered.
// The processing test drives the two orchestrators directly; this one
// goes through Service.Process so the real dispatch in service/crosstab.go
// picks the arm, with SetDisableCrosstabFusion(true) as the buffered
// oracle.
//
// AGG_WEIGHTED_MEAN + OVERLAY_PAIRWISE_PROP_Z is the shape this effort
// exists to unblock: a component-READING overlay over a weighted cell.
// It only reaches processCrosstabFused because E1-S1 dropped the
// "overlays force buffered" gate arm, so the gate assertion below is
// load-bearing — without it the two Process calls would both be buffered
// and the parity check would be vacuous.
func TestCrosstabFused_PairwisePropZOverWeightedMeanEndToEnd(t *testing.T) {
	schema := weightedCrosstabSchema()
	cfg := setupTestFS(t, "wm.pulse", schema, weightedCrosstabRecords())
	ctx := context.Background()

	cases := []struct {
		name    string
		nSource string
	}{
		{"default_cell_n", ""},
		{"cell_weight_sum", types.PairwiseNSourceCellWeightSum},
		{"row_margin_n", types.PairwiseNSourceRowMarginN},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, err := json.Marshal(types.PairwiseOverlayParams{NSource: tc.nSource})
			if err != nil {
				t.Fatalf("marshal pairwise params: %v", err)
			}
			buildReq := func() *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: "wm.pulse"},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
						Cell: &types.Aggregation{
							Type:   types.AGG_WEIGHTED_MEAN,
							Field:  "value",
							Label:  "wmean",
							Params: json.RawMessage(`{"weight_field":"weight"}`),
						},
						Shape:   types.CrosstabShapeMatrix,
						Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					},
					Overlays: []types.OverlaySpec{{
						Name:   "pz",
						Kind:   types.OverlayKindPairwisePropZ,
						Scope:  types.OverlayScopeRow,
						Params: params,
					}},
				}
			}

			svcFused := New(cfg)
			if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
				t.Fatalf("CanFuseCrosstab rejected AGG_WEIGHTED_MEAN + PAIRWISE_PROP_Z: %s", reason)
			}
			fusedResp, err := svcFused.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("Process (fused): %v", err)
			}

			svcBuf := New(cfg)
			svcBuf.SetDisableCrosstabFusion(true)
			bufResp, err := svcBuf.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("Process (buffered): %v", err)
			}

			if len(fusedResp.Overlays) != 1 {
				t.Fatalf("fused response carries %d overlay layers, want 1", len(fusedResp.Overlays))
			}
			// Non-vacuity: at least one real p-value, otherwise the
			// comparisons below would agree on two empty matrices.
			present := 0
			if mx := fusedResp.Overlays[0].Payload.Matrix; mx != nil {
				for _, row := range mx.Cells {
					for _, cell := range row {
						if cell.Present {
							present++
						}
					}
				}
			}
			if present == 0 {
				t.Fatalf("PROP_Z layer produced zero present p-values; fixture no longer exercises the kernel")
			}

			marshal := func(v any) string {
				b, mErr := json.Marshal(v)
				if mErr != nil {
					t.Fatalf("json.Marshal: %v", mErr)
				}
				return string(b)
			}
			if want, got := marshal(bufResp.Overlays), marshal(fusedResp.Overlays); want != got {
				t.Errorf("Overlays diverge:\n buffered=%s\n fused   =%s", want, got)
			}
			if want, got := marshal(bufResp.Warnings), marshal(fusedResp.Warnings); want != got {
				t.Errorf("Warnings diverge:\n buffered=%s\n fused   =%s", want, got)
			}
			if want, got := marshal(bufResp.Components), marshal(fusedResp.Components); want != got {
				t.Errorf("Components diverge:\n buffered=%s\n fused   =%s", want, got)
			}
			if want, got := marshal(bufResp.Crosstab), marshal(fusedResp.Crosstab); want != got {
				t.Errorf("Crosstab host payload diverges:\n buffered=%s\n fused   =%s", want, got)
			}
		})
	}
}
