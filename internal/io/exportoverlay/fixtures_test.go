package exportoverlay

import (
	"context"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// helperFloat returns a pointer to f. Mirrors the per-format adapter
// test fixtures so the integration-level claims share the same authoring
// shape.
func helperFloat(f float64) *float64 { return &f }

func helperInt(i int) *int { return &i }

// helperOverlayLayers returns a small fixture set covering all three
// OverlayPayload shapes (scalar / series / matrix). The same fixture is
// reused across the Arrow / Parquet / Excel / NDJSON integration tests
// so the round-trip claims compare against the same baseline.
func helperOverlayLayers() []*types.OverlayLayer {
	scalarVal := 12.34
	return []*types.OverlayLayer{
		{
			Name:  "chisq_total",
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
			Ref:   types.OverlayRef{},
			Payload: types.OverlayPayload{
				Shape:  types.OverlayShapeScalar,
				Scalar: &scalarVal,
			},
			Summary: &types.OverlaySummary{
				Statistic: helperFloat(12.34),
				PValue:    helperFloat(0.00021),
			},
		},
		{
			Name:  "index_vs_total",
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
			Ref:   types.OverlayRef{},
			Payload: types.OverlayPayload{
				Shape: types.OverlayShapeSeries,
				Series: &types.SeriesPayload{
					Entries: []types.SeriesEntry{
						{
							Key:     types.AxisKey{"NORTH"},
							Summary: types.OverlaySummary{Statistic: helperFloat(125.0)},
						},
						{
							Key:     types.AxisKey{"SOUTH"},
							Summary: types.OverlaySummary{Statistic: helperFloat(80.0)},
						},
					},
				},
			},
			Summary: &types.OverlaySummary{
				Min:   helperFloat(80.0),
				Max:   helperFloat(125.0),
				Count: helperInt(2),
			},
		},
		{
			Name:  "index_vs_margin",
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: "row"},
			},
			Payload: types.OverlayPayload{
				Shape: types.OverlayShapeMatrix,
				Matrix: &types.MatrixPayload{
					RowKeys:    []types.AxisKey{{"NORTH"}, {"SOUTH"}},
					ColumnKeys: []types.AxisKey{{"WEB"}, {"STORE"}},
					Cells: [][]types.MatrixCell{
						{
							{Value: 95.2, Present: true},
							{Value: 105.8, Present: true},
						},
						{
							{Value: 88.4, Present: true},
							{Value: 112.1, Present: true},
						},
					},
				},
			},
		},
	}
}

// importThreeFieldCohort writes a small three-field cohort and returns
// the in-mem filesystem holding "test.pulse". Used by every per-format
// integration test so the source byte layout is identical across the
// suite.
//
// The reader is a thin string-tabular mock — categorical fields ride on
// "name" / "country" so the export emits dictionary-resolved strings.
func importThreeFieldCohort(t *testing.T) afero.Fs {
	t.Helper()
	rows := [][]string{
		{"10", "alice", "US"},
		{"20", "bob", "GB"},
		{"30", "carol", "US"},
	}
	reader := newMockReader([]string{"age", "name", "country"}, rows)
	fs := afero.NewMemMapFs()

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("import: %v", err)
	}
	return fs
}

// mockReader is a tiny in-memory pio.Reader / pio.ResetReader the
// integration tests use to seed a .pulse cohort without touching disk.
// Mirrors the test helper in io/import_test.go but is reproduced here
// because that helper is unexported.
type mockReader struct {
	header  []string
	rows    [][]string
	pos     int
	started bool
}

func newMockReader(header []string, rows [][]string) *mockReader {
	return &mockReader{header: header, rows: rows}
}

func (r *mockReader) ReadHeader() ([]string, error) {
	r.started = true
	return r.header, nil
}

func (r *mockReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for r.pos < len(r.rows) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := fn(r.rows[r.pos]); err != nil {
			return err
		}
		r.pos++
	}
	return nil
}

func (r *mockReader) Close() error { return nil }

func (r *mockReader) Reset() error {
	r.pos = 0
	r.started = false
	return nil
}
