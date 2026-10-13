package io

import (
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/iocore"
)

// The export's field-to-column mapping stops being 1:1 at a vector field.
//
// A vec_f32 / vec_f64 field is ONE cell in the row the export loop reads,
// and what it becomes in the target depends on the target: a writer with a
// native list cell (ListAwareWriter — NDJSON, JSON array, Arrow, Parquet)
// keeps it as one column holding a []float64; every other writer gets it
// EXPANDED into dim scalar columns named `<field>_<label>` (`<field>_1..N`
// without labels) — the shape the sibling-column import folds back.
//
// The expansion runs AFTER applyExportLabels, on that function's output, so
// projection and `_label` augment keep their existing meaning: both work on
// fields, and the vector's columns land where its single cell landed.

// exportLayout is the column plan of one export.
type exportLayout struct {
	// columns is the header the writer receives: plan's names.
	columns []string
	// plan is the explicit per-column mapping — name, kind, source field
	// and the field that types it — one entry per header column. Typed
	// writers (ColumnAwareWriter) type from it and ExportJob.Predict
	// reports its names, so the two can never disagree about which field
	// a column carries.
	plan []iocore.ExportColumn
	// cellVec maps each cell of applyExportLabels' output to the schema
	// field it carries when that field is a vector to EXPAND, -1
	// otherwise. Nil when nothing expands.
	cellVec []int
	// lists are the native list columns, for a ListAwareWriter. Nil when
	// the cohort has no projected vector or the writer expands.
	lists []ListColumn
}

// planExportColumns lays out an export's header. includeMask and
// insertAfter are the projection and augment plans, replaceFields the
// fields a label REPLACE binding rewrites (typed as text: their cells
// become labels); expand selects the scalar expansion over native list
// columns.
//
// An expanded column whose name another column already has is refused with
// PULSE_EXPORT_COLUMN_COLLISION: a writer keyed by column name (NDJSON
// object keys, an Arrow schema) would drop one silently, and a positional
// one would hand a reader two columns it cannot tell apart.
func planExportColumns(schema *encoding.Schema, includeMask, insertAfter []bool, replaceFields map[int]bool, expand bool) (*exportLayout, error) {
	l := &exportLayout{}
	// owner records, per emitted column name, the vector it expands
	// ("" for an ordinary column) and the field it comes from.
	type owner struct{ vector, field string }
	seen := map[string]owner{}
	var collision error
	add := func(name string, o owner, col iocore.ExportColumn) {
		col.Name = name
		l.plan = append(l.plan, col)
		prev, dup := seen[name]
		if !dup {
			seen[name] = o
		} else if collision == nil && (o.vector != "" || prev.vector != "") {
			// Only a collision the expansion CAUSES is refused here: two
			// ordinary columns sharing a name is the label-augment
			// collision, validated where the bindings are.
			vec, other := o.vector, prev
			if vec == "" {
				vec, other = prev.vector, o
			}
			with := "field " + strconv.Quote(other.field)
			if other.vector != "" {
				with = "an element column of vector " + strconv.Quote(other.vector)
			}
			collision = errors.NewCodedErrorWithDetails(errors.PULSE_EXPORT_COLUMN_COLLISION,
				fmt.Sprintf("export: expanding vector %q into one column per element emits column %q, which collides with %s", vec, name, with),
				map[string]any{"column": name, "vector": vec, "collides_with": with})
		}
		l.columns = append(l.columns, name)
	}

	var cellVec []int
	for i, f := range schema.Fields {
		if includeMask != nil && !includeMask[i] {
			continue
		}
		field := &schema.Fields[i]
		switch {
		case f.Type.IsVector() && expand:
			meta, _ := schema.Vector(i)
			cellVec = append(cellVec, i)
			for e := 0; e < meta.Dim; e++ {
				name := VectorElementColumn(f.Name, meta.Labels, e)
				elem := vectorElementField(name, f.Type)
				add(name, owner{vector: f.Name, field: f.Name},
					iocore.ExportColumn{Kind: iocore.ExportColumnElement, Source: f.Name, Element: e, Field: &elem})
			}
		case f.Type.IsVector():
			meta, _ := schema.Vector(i)
			l.lists = append(l.lists, ListColumn{
				Index:    len(l.columns),
				Name:     f.Name,
				Type:     f.Type,
				Dim:      meta.Dim,
				Labels:   meta.Labels,
				Nullable: f.Nullable,
			})
			cellVec = append(cellVec, -1)
			add(f.Name, owner{field: f.Name},
				iocore.ExportColumn{Kind: iocore.ExportColumnList, Source: f.Name, Element: -1, Field: field})
		default:
			cellVec = append(cellVec, -1)
			typing := field
			if replaceFields[i] && !f.Type.IsDecimal() {
				// A replace binding rewrites the cell to its label
				// text (a decimal reaches a typed writer as a
				// Decimal128, never as text, so it is never replaced).
				typing = nil
			}
			add(f.Name, owner{field: f.Name},
				iocore.ExportColumn{Kind: iocore.ExportColumnField, Source: f.Name, Element: -1, Field: typing})
		}
		if insertAfter[i] {
			cellVec = append(cellVec, -1)
			add(f.Name+"_label", owner{field: f.Name},
				iocore.ExportColumn{Kind: iocore.ExportColumnLabel, Source: f.Name, Element: -1})
		}
	}
	if collision != nil {
		return nil, collision
	}
	for _, at := range cellVec {
		if at >= 0 {
			l.cellVec = cellVec
			break
		}
	}
	return l, nil
}

// VectorElementColumn is the expanded column name of element e (0-based)
// of the vector field `field`: `<field>_<label>` when the vector carries
// labels, `<field>_<e+1>` otherwise.
func VectorElementColumn(field string, labels []string, e int) string {
	if e < len(labels) {
		return field + "_" + labels[e]
	}
	return field + "_" + strconv.Itoa(e+1)
}

// expandRow rewrites one applyExportLabels output row into the expanded
// row, into dst (reused across rows). A vector cell becomes its elements'
// text — nil for a NaN element and for every element of a null vector.
func (l *exportLayout) expandRow(row []any, schema *encoding.Schema, dst []any) []any {
	dst = dst[:0]
	for c, v := range row {
		at := -1
		if c < len(l.cellVec) {
			at = l.cellVec[c]
		}
		if at < 0 {
			dst = append(dst, v)
			continue
		}
		f32 := schema.Fields[at].Type == encoding.FieldTypeVecF32
		dim := schema.VectorDim(at)
		vec, _ := v.([]float64)
		for e := 0; e < dim; e++ {
			if vec == nil || e >= len(vec) || math.IsNaN(vec[e]) {
				dst = append(dst, nil)
				continue
			}
			dst = append(dst, formatVectorElement(vec[e], f32))
		}
	}
	return dst
}

// formatVectorElement renders one element exactly as the scalar f32 / f64
// export renders a value of the same type, so an expanded column re-imports
// to the bits it was stored as.
func formatVectorElement(v float64, f32 bool) string {
	if f32 {
		return strconv.FormatFloat(float64(float32(v)), 'f', -1, 32)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// expandedSchema is schema with each vector field replaced, in place, by
// its element columns as nullable scalar f32 / f64 fields — the schema a
// SchemaAwareWriter that does not take list cells is handed, so its
// per-column typing stays aligned with the expanded row.
func expandedSchema(schema *encoding.Schema) *encoding.Schema {
	if !schema.HasVectors() {
		return schema
	}
	out := &encoding.Schema{}
	for i, f := range schema.Fields {
		if !f.Type.IsVector() {
			out.Fields = append(out.Fields, f)
			continue
		}
		meta, _ := schema.Vector(i)
		for e := 0; e < meta.Dim; e++ {
			out.Fields = append(out.Fields, vectorElementField(VectorElementColumn(f.Name, meta.Labels, e), f.Type))
		}
	}
	return out
}

// vectorElementField is the scalar field one expanded element column of a
// vector of type vec is typed as: nullable (a NaN element and a null
// vector are both nil) f32 for vec_f32, f64 for vec_f64.
func vectorElementField(name string, vec encoding.FieldType) encoding.Field {
	elem := encoding.FieldTypeF64
	if vec == encoding.FieldTypeVecF32 {
		elem = encoding.FieldTypeF32
	}
	return encoding.Field{Name: name, Type: elem, Nullable: true}
}

// vectorCellReader decodes vector fields off the export's record stream
// into per-field buffers reused across rows.
type vectorCellReader struct {
	raw  [][]byte
	vals [][]float64
}

func newVectorCellReader(schema *encoding.Schema) *vectorCellReader {
	vr := &vectorCellReader{
		raw:  make([][]byte, len(schema.Fields)),
		vals: make([][]float64, len(schema.Fields)),
	}
	for i, f := range schema.Fields {
		if f.Type.IsVector() {
			vr.raw[i] = make([]byte, schema.FieldWidth(i))
			vr.vals[i] = make([]float64, 0, schema.VectorDim(i))
		}
	}
	return vr
}

// read decodes field i's vector from r. The returned slice is reused on
// the next call for the same field.
func (vr *vectorCellReader) read(r io.Reader, schema *encoding.Schema, i int) ([]float64, error) {
	buf := vr.raw[i]
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	v, err := encoding.VectorFromBytes(buf, schema.Fields[i].Type, schema.VectorDim(i), vr.vals[i])
	if err != nil {
		return nil, err
	}
	vr.vals[i] = v
	return v, nil
}
