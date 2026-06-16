package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func TestCrosstab_DecodePlanEquivalence_HugeRequestShape(t *testing.T) {
	const (
		fieldCount = 200
		rowCount   = 10_000
		brandCard  = 13
		feelCard   = 30
	)

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/crosstab_decode_plan.pulse"

	schema, payload := buildHugeRequestCohort(t, fieldCount, rowCount, brandCard, feelCard)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	svc := New(cfg)

	mkReq := func() *types.Request {
		fiscalOffset := -3
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Crosstab: &types.CrosstabSpec{
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
			},
		}
	}

	ctx := context.Background()
	start := time.Now()

	// Plan-on side — the public default path. svc.Process →
	// processCrosstab → applyCrosstabProjection installs the keep
	// filter on the iterator, which builds and caches a DecodePlan.
	// streamingIterator.Next() then routes to
	// RecordReader.ReadRecordWithWidePlan for every record.
	planOnResp, err := svc.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("Process (plan-on): %v", err)
	}
	if planOnResp.Crosstab == nil || planOnResp.Crosstab.Matrix == nil {
		t.Fatal("plan-on: expected matrix payload")
	}

	// Plan-off side — mirror processCrosstab end-to-end but clear the
	// iterator's plan pointer right after applyCrosstabProjection so
	// streamingIterator.Next() takes the case-arm that calls
	// RecordReader.ReadRecordWithWideProjected instead. Same retained
	// set, same processor, same RunCrosstab. The plan field is
	// unexported but reachable from inside package service.
	planOffResp, err := processCrosstabWithoutPlan(ctx, svc, mkReq())
	if err != nil {
		t.Fatalf("processCrosstabWithoutPlan: %v", err)
	}
	if planOffResp.Crosstab == nil || planOffResp.Crosstab.Matrix == nil {
		t.Fatal("plan-off: expected matrix payload")
	}

	elapsed := time.Since(start)

	assertMatrixDeepEqual(t, planOnResp.Crosstab.Matrix, planOffResp.Crosstab.Matrix)

	if !reflect.DeepEqual(planOnResp.Crosstab, planOffResp.Crosstab) {
		t.Errorf("Crosstab top-level differs between plan-on and plan-off:\n on: %#v\noff: %#v",
			planOnResp.Crosstab, planOffResp.Crosstab)
	}

	// Metadata equivalence — TotalRows and FilteredRows must match
	// exactly; CohortFile is set to the same absolute path on both
	// sides since processCrosstabWithoutPlan mirrors processCrosstab's
	// resp.Metadata.CohortFile = path assignment.
	if planOnResp.Metadata == nil || planOffResp.Metadata == nil {
		t.Fatalf("expected metadata on both sides: on=%v off=%v",
			planOnResp.Metadata, planOffResp.Metadata)
	}
	if planOnResp.Metadata.TotalRows != planOffResp.Metadata.TotalRows {
		t.Errorf("TotalRows differs: on=%d off=%d",
			planOnResp.Metadata.TotalRows, planOffResp.Metadata.TotalRows)
	}
	if planOnResp.Metadata.FilteredRows != planOffResp.Metadata.FilteredRows {
		t.Errorf("FilteredRows differs: on=%d off=%d",
			planOnResp.Metadata.FilteredRows, planOffResp.Metadata.FilteredRows)
	}

	// Wall-clock + matrix dimensions as a directional signal — not an
	// assertion. A regression here would surface as the test timing
	// out (60s) or the matrix being degenerate.
	m := planOnResp.Crosstab.Matrix
	cellCount := len(m.RowKeys) * len(m.ColumnKeys)
	t.Logf("crosstab populated %d rows x %d columns = %d cells; wall-clock %s",
		len(m.RowKeys), len(m.ColumnKeys), cellCount, elapsed)

	// Sanity: ensure the crosstab is non-degenerate. The synth cohort
	// fills every (brand, quarter, cardFeeling) tuple deterministically
	// so we expect ~brandCard rows × (~20 quarters × feelCard cells)
	// columns. The exact column count depends on which fiscal quarter
	// each waveDate falls in; we just assert non-trivial.
	if len(m.RowKeys) < 2 {
		t.Errorf("plan-on: too few row keys (%d), wanted multi-brand crosstab",
			len(m.RowKeys))
	}
	if len(m.ColumnKeys) < 2 {
		t.Errorf("plan-on: too few column keys (%d), wanted multi-(quarter, feeling) crosstab",
			len(m.ColumnKeys))
	}

	if testing.Short() {
		return
	}
	// Belt-and-braces: also enforce the under-5s wall-clock target
	// from the story. We give a 2x headroom in case CI is slow; the
	// real signal is the t.Logf above. Use a soft test bound rather
	// than a hard fail so noisy hardware doesn't false-alarm.
	if elapsed > 10*time.Second {
		t.Errorf("wall-clock %s exceeds 10s budget (story target <5s)", elapsed)
	}
}

// processCrosstabWithoutPlan mirrors service.processCrosstab end-to-end
// but clears the iterator's cached DecodePlan immediately after
// applyCrosstabProjection installs the keep filter. With plan==nil and
// project!=nil, streamingIterator.Next() falls through to the
// per-field projected decode (RecordReader.ReadRecordWithWideProjected).
// Every other step — defaults, label validation, materializeRecords,
// processing.NewProcessorWithExtensions, RunCrosstab,
// buildAndApplyLabels — is identical to processCrosstab so the two
// responses differ only in which decode entry point read the records.
//
// Shard-iter cohorts are not exercised by this test (single .pulse
// file); the type assertion to *streamingIterator below is therefore
// safe. Future shard-archive coverage would need a parallel hook
// against shardIter.
func processCrosstabWithoutPlan(ctx context.Context, s *Service, req *types.Request) (*types.Response, error) {
	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	s.applyDefaults(req, cohort.Schema())

	s.applyAutoLabels(&req.Labels, cohort.Schema(), collectOutputLabels(req), nil)
	if err := s.validateProcessLabels(req, cohort.Schema()); err != nil {
		return nil, err
	}

	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	s.applyCrosstabProjection(iter, req, cohort.Schema())

	// Disable the plan-driven path so the per-record hot loop routes
	// to ReadRecordWithWideProjected instead of ReadRecordWithWidePlan.
	// streamingIterator is the only iterator implementation the
	// single-file branch ever returns; if that changes, this assertion
	// will surface the regression via a test failure rather than a
	// silent bypass.
	si, ok := iter.(*streamingIterator)
	if !ok {
		return nil, fmt.Errorf("processCrosstabWithoutPlan: expected *streamingIterator, got %T", iter)
	}
	si.plan = nil
	si.planKey = ""

	records, err := materializeRecords(iter)
	if err != nil {
		return nil, err
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}

	proc := processing.NewProcessorWithExtensions(cohort.Schema(), s.extensions)
	resp, err := proc.RunCrosstab(ctx, req, records)
	if err != nil {
		return nil, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}

	if err := s.buildAndApplyLabels(req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// assertMatrixDeepEqual compares two MatrixPayloads using
// reflect.DeepEqual on every populated field. The crosstab pipeline is
// deterministic — sort-stable axis keys, summable AGG_SUM cells —
// so we can demand bit-exact equality rather than the eps-tolerance
// comparison the existing TestCrosstabProjection_BehavioralEquivalence
// tests use (those run before AGG_MEDIAN where ordering inside a tied
// median could legitimately drift across decode paths).
func assertMatrixDeepEqual(t *testing.T, a, b *types.MatrixPayload) {
	t.Helper()
	if a == nil || b == nil {
		t.Fatalf("nil matrix: a=%v b=%v", a == nil, b == nil)
	}
	if !reflect.DeepEqual(a.RowHeader, b.RowHeader) {
		t.Errorf("RowHeader differs:\n on: %#v\noff: %#v", a.RowHeader, b.RowHeader)
	}
	if !reflect.DeepEqual(a.ColumnHeader, b.ColumnHeader) {
		t.Errorf("ColumnHeader differs:\n on: %#v\noff: %#v", a.ColumnHeader, b.ColumnHeader)
	}
	if !reflect.DeepEqual(a.RowKeys, b.RowKeys) {
		t.Errorf("RowKeys differ:\n on: %v\noff: %v", a.RowKeys, b.RowKeys)
	}
	if !reflect.DeepEqual(a.ColumnKeys, b.ColumnKeys) {
		t.Errorf("ColumnKeys differ:\n on: %v\noff: %v", a.ColumnKeys, b.ColumnKeys)
	}
	if !reflect.DeepEqual(a.Cells, b.Cells) {
		// Report which cell first diverges so a failure is actionable
		// rather than a wall of text.
		reportFirstCellDiff(t, a, b)
	}
	if !reflect.DeepEqual(a.RowMargins, b.RowMargins) {
		t.Errorf("RowMargins differ:\n on: %v\noff: %v", a.RowMargins, b.RowMargins)
	}
	if !reflect.DeepEqual(a.ColumnMargins, b.ColumnMargins) {
		t.Errorf("ColumnMargins differ:\n on: %v\noff: %v", a.ColumnMargins, b.ColumnMargins)
	}
	if !reflect.DeepEqual(a.GrandTotal, b.GrandTotal) {
		t.Errorf("GrandTotal differs:\n on: %#v\noff: %#v", a.GrandTotal, b.GrandTotal)
	}
	if a.CellLabel != b.CellLabel {
		t.Errorf("CellLabel differs: on=%q off=%q", a.CellLabel, b.CellLabel)
	}
	if a.NormalizeApplied != b.NormalizeApplied {
		t.Errorf("NormalizeApplied differs: on=%q off=%q", a.NormalizeApplied, b.NormalizeApplied)
	}
}

// reportFirstCellDiff finds the first (row, col) where two matrices
// disagree and prints both values + the surrounding axis tuples so a
// drift is localizable to a specific bucket. Called only when
// reflect.DeepEqual already flagged a difference somewhere.
func reportFirstCellDiff(t *testing.T, a, b *types.MatrixPayload) {
	t.Helper()
	if len(a.Cells) != len(b.Cells) {
		t.Errorf("Cells row count differs: on=%d off=%d", len(a.Cells), len(b.Cells))
		return
	}
	for i := range a.Cells {
		if len(a.Cells[i]) != len(b.Cells[i]) {
			t.Errorf("Cells[%d] column count differs: on=%d off=%d",
				i, len(a.Cells[i]), len(b.Cells[i]))
			return
		}
		for j := range a.Cells[i] {
			if !reflect.DeepEqual(a.Cells[i][j], b.Cells[i][j]) {
				t.Errorf("Cells[%d][%d] differs: row=%v col=%v\n on: %#v\noff: %#v",
					i, j, a.RowKeys[i], a.ColumnKeys[j],
					a.Cells[i][j], b.Cells[i][j])
				return
			}
		}
	}
}

// buildHugeRequestCohort synthesises a (schema, packed payload) pair
// shaped to mirror the canonical vc/1.pulse cohort that drives
// tmp/huge-request.json: a categorical brand row field with
// brandCardinality cats, a date column field spanning ~5 years (so
// fiscal_quarter has multiple buckets), a categorical cardFeeling
// column field with feelCardinality cats, an f64 weight cell field,
// and (fieldCount - 4) decoy fields the crosstab never references.
//
// Deterministic: every value is a function of row index r and field
// index i, so re-running yields byte-identical output. No rand seeds,
// no time-of-day inputs.
func buildHugeRequestCohort(t *testing.T, fieldCount, rowCount, brandCardinality, feelCardinality int) (*encoding.Schema, []byte) {
	t.Helper()

	// --- Named-field dictionaries. ---
	brandDict := encoding.NewDictionary()
	for i := range brandCardinality {
		if _, err := brandDict.Add(fmt.Sprintf("brand_%02d", i)); err != nil {
			t.Fatalf("brand dict: %v", err)
		}
	}
	feelingDict := encoding.NewDictionary()
	for i := range feelCardinality {
		if _, err := feelingDict.Add(fmt.Sprintf("feel_%02d", i)); err != nil {
			t.Fatalf("feeling dict: %v", err)
		}
	}

	// Pad categorical dictionaries — one per pad categorical field so
	// the schema's dict-resolve cost is realistic. 12 entries each
	// keeps the parse cheap while still exercising every decode path.
	padCatDicts := make([]*encoding.Dictionary, 0)

	// --- Schema layout. Named fields first so they sit at predictable
	// offsets, then pad with mixed types up to fieldCount. ---
	fields := make([]encoding.Field, 0, fieldCount)
	byteOffset := 0
	add := func(f encoding.Field) {
		f.ByteOffset = byteOffset
		if f.Type.IsBitPacked() {
			byteOffset++
		} else {
			byteOffset += f.Type.ByteSize()
		}
		f.CsvColumnIdx = len(fields)
		fields = append(fields, f)
	}

	add(encoding.Field{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brandDict})
	add(encoding.Field{Name: "waveDate", Type: encoding.FieldTypeDate})
	// cardFeeling needs >8 cats → categorical_u8 caps at 256, so u8 is
	// fine for 30 cats; if a future caller bumps feelCardinality past
	// 256 they'll hit the same width-overflow CodedError the regular
	// import path would.
	add(encoding.Field{Name: "cardFeeling", Type: encoding.FieldTypeCategoricalU8, Dictionary: feelingDict})
	add(encoding.Field{Name: "weight", Type: encoding.FieldTypeF64})

	// Pad tail — deterministic mix of types so the projection has
	// heterogeneous bytes to skip past. Ratio mirrors vc/1.pulse:
	// ~70% categorical_u8, ~10% f64, ~10% u32, ~5% u16, ~5% f32.
	padIdx := 0
	for len(fields) < fieldCount {
		var f encoding.Field
		f.Name = fmt.Sprintf("pad_%03d", padIdx)
		switch padIdx % 20 {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13: // 70%
			dict := encoding.NewDictionary()
			for i := range 12 {
				if _, err := dict.Add(fmt.Sprintf("v%02d_%d", padIdx%50, i)); err != nil {
					t.Fatalf("pad cat dict: %v", err)
				}
			}
			padCatDicts = append(padCatDicts, dict)
			f.Type = encoding.FieldTypeCategoricalU8
			f.Dictionary = dict
		case 14, 15: // 10% f64
			f.Type = encoding.FieldTypeF64
		case 16, 17: // 10% u32
			f.Type = encoding.FieldTypeU32
		case 18: // 5% u16
			f.Type = encoding.FieldTypeU16
		case 19: // 5% f32
			f.Type = encoding.FieldTypeF32
		}
		add(f)
		padIdx++
	}

	schema := &encoding.Schema{Fields: fields}

	// --- Record payload. ---
	stride := schema.RecordByteSize()
	out := bytes.NewBuffer(make([]byte, 0, rowCount*stride))

	// Date range: 2020-01-01..2024-12-31 (~5 years) so the fiscal
	// quarter grouper has enough buckets to populate every (brand,
	// quarter, feeling) tuple.
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	startDays := int64(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Sub(epoch).Hours() / 24)
	const daySpan = 365 * 5

	for r := range rowCount {
		// brand: cycle deterministically over brandCardinality cats so
		// every brand row gets ~rowCount/brandCardinality records.
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64(r%brandCardinality)); err != nil {
			t.Fatalf("write brand: %v", err)
		}
		// waveDate: monotonic-with-noise across the date span.
		days := startDays + int64((r*7)%daySpan)
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeDate, uint64(days)); err != nil {
			t.Fatalf("write date: %v", err)
		}
		// cardFeeling: cycle over feelCardinality cats.
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64((r*3)%feelCardinality)); err != nil {
			t.Fatalf("write feeling: %v", err)
		}
		// weight: deterministic non-trivial f64 derived from r so the
		// per-cell sums are non-degenerate.
		w := float64((r*37)%1009) + 0.5
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, math.Float64bits(w)); err != nil {
			t.Fatalf("write weight: %v", err)
		}

		// Pad tail (fields 4..fieldCount-1).
		padCatIdx := 0
		for i := 4; i < len(fields); i++ {
			f := fields[i]
			switch f.Type {
			case encoding.FieldTypeCategoricalU8:
				dict := padCatDicts[padCatIdx]
				padCatIdx++
				size := len(dict.Values())
				if size == 0 {
					size = 1
				}
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64((r+i)%size)); err != nil {
					t.Fatalf("write pad cat: %v", err)
				}
			case encoding.FieldTypeF64:
				v := float64((r*i)%509) + 0.25
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, math.Float64bits(v)); err != nil {
					t.Fatalf("write pad f64: %v", err)
				}
			case encoding.FieldTypeU32:
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeU32, uint64((r*7+i)%1000000)); err != nil {
					t.Fatalf("write pad u32: %v", err)
				}
			case encoding.FieldTypeU16:
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeU16, uint64((r+i)%4096)); err != nil {
					t.Fatalf("write pad u16: %v", err)
				}
			case encoding.FieldTypeF32:
				v := float32((r*i)%509) + 0.5
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeF32, uint64(math.Float32bits(v))); err != nil {
					t.Fatalf("write pad f32: %v", err)
				}
			default:
				t.Fatalf("unexpected pad type %s", f.Type)
			}
		}
	}

	return schema, out.Bytes()
}
