// Overlay column-family sidecar for the Parquet adapter.
//
// Per research/export-embedding-shape.md § 4 the Parquet adapter rides
// the SAME Arrow LIST<STRUCT> schema as the Arrow IPC adapter. Parquet's
// `pqarrow.FileWriter` consumes an Arrow Schema as its native shape, so
// reusing the Arrow schema verbatim halves the marshal surface (§ 4.4
// "Why Parquet shares the Arrow schema").
//
// This file carries the Parquet-side glue — the Writer-side overlay
// embedding (SetOverlays + appendOverlayCell) and the Reader-side
// overlay extraction (ReadOverlays) — wired against the shared helpers
// exported from io/arrow (OverlaysFieldName / OverlaysArrowField /
// AppendOverlayLayerStruct / ReadOverlaysFromArray).
package parquet

import (
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"

	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/frankbardon/pulse/types"
)

// SetOverlays records the Response.Overlays layers the export pipeline
// wants the writer to embed in the Parquet file. The layers ride as a
// top-level "overlays" LIST<STRUCT> column whose populated value lands
// in the first row of the first record-batch (and therefore the first
// row-group, per research/export-embedding-shape.md § 4.2) and whose
// subsequent rows / row-groups carry an empty list.
//
// nil or empty layers leaves the Parquet output byte-identical to a
// pre-overlay export (the "overlays" field is OMITTED from the schema
// rather than emitted with empty lists). Implements
// pio.OverlayAwareWriter.
//
// Must be called BEFORE WriteHeader so the lazily-built Arrow schema
// includes the overlay field. Calling after the writer has been
// initialised has no effect on the schema.
func (w *Writer) SetOverlays(layers []*types.OverlayLayer) {
	w.overlays = layers
}

// appendOverlayCell appends one cell to the trailing overlays
// LIST<STRUCT> column. No-op when the writer has no overlays
// configured (the schema does not include the overlays field).
//
// The first appended cell (row 0 of the first record-batch) carries
// the populated layer list; every subsequent cell carries an empty
// list per § 4.2. The flag flips on the first emission so subsequent
// rows (including subsequent record-batches and row-groups) see
// overlaysEmitted=true and append empty lists.
func (w *Writer) appendOverlayCell() error {
	if len(w.overlays) == 0 {
		return nil
	}
	overlayFieldIdx := len(w.columns)
	listBldr, ok := w.bldr.Field(overlayFieldIdx).(*array.ListBuilder)
	if !ok {
		return fmt.Errorf("parquet.Writer: overlays field is not a list builder (got %T)", w.bldr.Field(overlayFieldIdx))
	}
	listBldr.Append(true)
	structBldr := listBldr.ValueBuilder().(*array.StructBuilder)

	if w.overlaysEmitted {
		// Empty list — no struct elements appended.
		return nil
	}

	for _, layer := range w.overlays {
		if err := parrow.AppendOverlayLayerStruct(structBldr, layer); err != nil {
			return err
		}
	}
	w.overlaysEmitted = true
	return nil
}

// findOverlayFieldIndex returns the column index of the top-level
// overlays sidecar field, or -1 when the schema does not carry one.
func findOverlayFieldIndex(sc *arrow.Schema) int {
	for c := 0; c < sc.NumFields(); c++ {
		if sc.Field(c).Name == parrow.OverlaysFieldName {
			return c
		}
	}
	return -1
}

// ReadOverlays extracts the embedded overlay layers from the top-level
// "overlays" sidecar column emitted by a SetOverlays-enabled Writer.
// Returns nil when the Parquet file carries no overlays sidecar (the
// "overlays" field is absent from the schema) or when every list
// element is empty.
//
// Per research/export-embedding-shape.md § 4.2 the writer emits the
// populated list in row 0 of the first record-batch / row-group; the
// reader walks the materialised Arrow table column for the first non-
// empty list across all chunks and stops on the first hit.
func (r *Reader) ReadOverlays() ([]*types.OverlayLayer, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	overlayFieldIdx := findOverlayFieldIndex(r.arrowSc)
	if overlayFieldIdx < 0 {
		return nil, nil
	}
	col := r.tbl.Column(overlayFieldIdx)
	for _, chunk := range col.Data().Chunks() {
		layers, err := parrow.ReadOverlaysFromArray(chunk)
		if err != nil {
			return nil, err
		}
		if layers != nil {
			return layers, nil
		}
	}
	return nil, nil
}
