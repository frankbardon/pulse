package exportoverlay

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	parrow "github.com/frankbardon/pulse/internal/io/arrow"
	pcsv "github.com/frankbardon/pulse/internal/io/csv"
	pexcel "github.com/frankbardon/pulse/internal/io/excel"
	pndjson "github.com/frankbardon/pulse/internal/io/ndjson"
	pparquet "github.com/frankbardon/pulse/internal/io/parquet"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// partsOverlayLayers is the three-shape fixture with named parts on the
// first (scalar) layer — a multi-output kind's layer.
func partsOverlayLayers() []*types.OverlayLayer {
	layers := helperOverlayLayers()
	layers[0].Payload.Parts = map[string]*types.OverlayPayload{
		"expected": {Shape: types.OverlayShapeScalar, Scalar: helperFloat(15)},
		"p": {Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{
			RowKeys: []types.AxisKey{{"a"}}, ColumnKeys: []types.AxisKey{{"dim_1"}},
			Cells: [][]types.MatrixCell{{{Value: 0.04, Present: true}}},
		}},
	}
	return layers
}

type overlayReader interface {
	ReadOverlays() ([]*types.OverlayLayer, error)
}

// TestExportJob_OverlayParts pins how each export format treats a
// layer's named parts:
//
//   - NDJSON / Arrow / Parquet write the layer as JSON, so parts ride
//     whole and read back unchanged; no warning.
//   - Excel writes the PRIMARY payload on the layer's sheet and drops
//     the parts with one PULSE_OVERLAY_EXPORT_PARTS_DROPPED warning
//     naming the layer and its parts.
//   - CSV drops every layer (warn-and-skip): the body stays the
//     overlay-free body and the one PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED
//     warning covers the parts too.
func TestExportJob_OverlayParts(t *testing.T) {
	type format struct {
		file     string
		writer   func(fs afero.Fs, path string) pio.Writer
		reader   func(fs afero.Fs, path string) overlayReader
		wantCode errors.Code // "" = no warning
		keeps    bool        // parts read back whole
	}
	formats := map[string]format{
		"ndjson": {file: "out.ndjson", keeps: true,
			writer: func(fs afero.Fs, p string) pio.Writer { return pndjson.NewWriter(fs, p) },
			reader: func(fs afero.Fs, p string) overlayReader { return pndjson.NewReader(fs, p) }},
		"arrow": {file: "out.arrow", keeps: true,
			writer: func(fs afero.Fs, p string) pio.Writer { return parrow.NewWriter(fs, p) },
			reader: func(fs afero.Fs, p string) overlayReader { return parrow.NewReader(fs, p) }},
		"parquet": {file: "out.parquet", keeps: true,
			writer: func(fs afero.Fs, p string) pio.Writer { return pparquet.NewWriter(fs, p) },
			reader: func(fs afero.Fs, p string) overlayReader { return pparquet.NewReader(fs, p) }},
		"excel": {file: "out.xlsx", wantCode: errors.PULSE_OVERLAY_EXPORT_PARTS_DROPPED,
			writer: func(fs afero.Fs, p string) pio.Writer { return pexcel.NewWriter(fs, p) },
			reader: func(fs afero.Fs, p string) overlayReader { return pexcel.NewReader(fs, p) }},
		"csv": {file: "out.csv", wantCode: errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED,
			writer: func(fs afero.Fs, p string) pio.Writer { return pcsv.NewWriter(fs, p) }},
	}
	run := func(t *testing.T, f format, layers []*types.OverlayLayer) (afero.Fs, *pio.ExportReport) {
		t.Helper()
		fs := importThreeFieldCohort(t)
		w := f.writer(fs, f.file)
		job := pio.NewExportJob("test.pulse", w)
		job.FS = fs
		job.Overlays = layers
		report, err := job.Run(context.Background())
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		return fs, report
	}
	for name, f := range formats {
		t.Run(name, func(t *testing.T) {
			// A parts-free slate reports exactly what it did before parts.
			_, plain := run(t, f, helperOverlayLayers())
			for _, w := range plain.OverlayWarnings {
				if w.Code == errors.PULSE_OVERLAY_EXPORT_PARTS_DROPPED {
					t.Errorf("parts-free export warned %s", w.Code)
				}
			}

			fs, report := run(t, f, partsOverlayLayers())
			var codes []errors.Code
			for _, w := range report.OverlayWarnings {
				codes = append(codes, w.Code)
			}
			if f.wantCode == "" && len(codes) != 0 || f.wantCode != "" && (len(codes) != 1 || codes[0] != f.wantCode) {
				t.Fatalf("warnings = %v, want [%s]", codes, f.wantCode)
			}
			if f.wantCode == errors.PULSE_OVERLAY_EXPORT_PARTS_DROPPED {
				d := report.OverlayWarnings[0].Details
				parts, _ := d["parts"].(map[string]any)
				if d["layer_count"] != 1 || !reflect.DeepEqual(d["layer_names"], []string{"chisq_total"}) ||
					!reflect.DeepEqual(parts["chisq_total"], []string{"expected", "p"}) {
					t.Errorf("details = %v", d)
				}
			}
			if f.reader == nil {
				// CSV: the body is the overlay-free body.
				body, _ := afero.ReadFile(fs, f.file)
				baseFs, _ := run(t, f, nil)
				base, _ := afero.ReadFile(baseFs, f.file)
				if !bytes.Equal(body, base) {
					t.Errorf("CSV body moved:\n got %q\nwant %q", body, base)
				}
				return
			}
			got, err := f.reader(fs, f.file).ReadOverlays()
			if err != nil {
				t.Fatalf("ReadOverlays: %v", err)
			}
			// The primary payloads read back on every embedding format.
			if err := pio.CompareOverlayLayers(got, helperOverlayLayers()); err != nil {
				t.Fatalf("primary payloads: %v", err)
			}
			want := partsOverlayLayers()[0].Payload.Parts
			if !f.keeps {
				want = nil
			}
			gb, _ := json.Marshal(got[0].Payload.Parts)
			wb, _ := json.Marshal(want)
			if !bytes.Equal(gb, wb) {
				t.Errorf("parts read back = %s, want %s", gb, wb)
			}
		})
	}
}
