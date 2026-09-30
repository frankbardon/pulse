package service

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// Join-shape synthetic cohort: the committed schema + seed behind the
// positional-Record gates (join_shape_gate_test.go) and benchmarks
// (join_shape_bench_test.go).
//
// The SHAPE mirrors a denormalised parent/child join — the cohort shape
// positional Record storage was built for — without a single name,
// dictionary value or cell value from any real cohort: 95 fields, 66 of
// them a parent block and 29 a child block; every parent fans out to 12
// or 13 child rows (alternating, mean 12.5x); the parent block is held
// constant across a parent's rows (the structural rule a join produces);
// field types are the measured mix — categorical_u8 / categorical_u16,
// packed_bool, u64 and set_* rungs up to set_u128, with nullable fields
// in both blocks.
//
// Everything is a pure function of (joinShapeSeed, parent count), so the
// small committed fixture under testdata/join_shape/ can be checked
// byte-for-byte against the generator, and the large bench cohort is
// generated only inside -bench runs, never by a unit test.

const (
	// joinShapeSeed seeds the PCG stream every value is drawn from.
	joinShapeSeed uint64 = 0x9e3779b97f4a7c15

	joinShapeParentFields = 66
	joinShapeChildFields  = 29

	// joinShapeFixtureParents sizes the committed correctness fixture:
	// 32 parents x 12.5 = 400 rows.
	joinShapeFixtureParents = 32

	joinShapeFixturePath = "testdata/join_shape/join_shape.pulse"
)

// joinShapeFanout is the child-row count of parent p: 12 and 13
// alternating, mean 12.5x.
func joinShapeFanout(p int) int { return 12 + p%2 }

// joinShapeRows is the row count generated for n parents.
func joinShapeRows(parents int) int {
	n := 0
	for p := range parents {
		n += joinShapeFanout(p)
	}
	return n
}

// joinShapeKeep4 retains four fields — the shape of a typical projected
// buffered request: two group keys, a filter field and a cell field,
// spanning both blocks.
func joinShapeKeep4(name string) bool {
	switch name {
	case "p_cat8_00", "p_cat16_00", "c_cat8_00", "c_u64_00":
		return true
	}
	return false
}

func joinShapeDict(prefix string, n int) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for i := range n {
		if _, err := d.Add(fmt.Sprintf("%s_v%02d", prefix, i)); err != nil {
			panic(err)
		}
	}
	return d
}

// joinShapeSchema returns the committed synthetic schema: parent block
// first (key, categoricals, flags, u64s, sets), then the child block.
func joinShapeSchema() *encoding.Schema {
	var fields []encoding.Field
	off := 0
	add := func(f encoding.Field) {
		f.ByteOffset = off
		if f.Type.IsBitPacked() {
			off++
		} else {
			off += f.Type.ByteSize()
		}
		f.CsvColumnIdx = len(fields)
		fields = append(fields, f)
	}
	cat := func(name string, ft encoding.FieldType, card int, nullable bool) {
		add(encoding.Field{Name: name, Type: ft, Dictionary: joinShapeDict(name, card), Nullable: nullable})
	}

	// Parent block: 1 + 28 + 4 + 18 + 10 + 5 = 66.
	add(encoding.Field{Name: "p_key", Type: encoding.FieldTypeU32})
	for i := range 28 {
		cat(fmt.Sprintf("p_cat8_%02d", i), encoding.FieldTypeCategoricalU8, 2+(i*5)%23, i%7 == 3)
	}
	for i := range 4 {
		cat(fmt.Sprintf("p_cat16_%02d", i), encoding.FieldTypeCategoricalU16, 300+i*40, i == 1)
	}
	for i := range 18 {
		add(encoding.Field{Name: fmt.Sprintf("p_flag_%02d", i), Type: encoding.FieldTypePackedBool})
	}
	for i := range 10 {
		add(encoding.Field{Name: fmt.Sprintf("p_u64_%02d", i), Type: encoding.FieldTypeU64, Nullable: i == 4})
	}
	cat("p_set_00", encoding.FieldTypeSetU8, 6, false)
	cat("p_set_01", encoding.FieldTypeSetU8, 8, true)
	cat("p_set_02", encoding.FieldTypeSetU16, 12, false)
	cat("p_set_03", encoding.FieldTypeSetU64, 40, false)
	cat("p_set_04", encoding.FieldTypeSetU128, 100, false)

	// Child block: 12 + 1 + 8 + 5 + 3 = 29.
	for i := range 12 {
		cat(fmt.Sprintf("c_cat8_%02d", i), encoding.FieldTypeCategoricalU8, 3+(i*7)%19, i%4 == 2)
	}
	cat("c_cat16_00", encoding.FieldTypeCategoricalU16, 500, false)
	for i := range 8 {
		add(encoding.Field{Name: fmt.Sprintf("c_flag_%02d", i), Type: encoding.FieldTypePackedBool})
	}
	for i := range 5 {
		add(encoding.Field{Name: fmt.Sprintf("c_u64_%02d", i), Type: encoding.FieldTypeU64, Nullable: i == 1})
	}
	cat("c_set_00", encoding.FieldTypeSetU8, 5, false)
	cat("c_set_01", encoding.FieldTypeSetU16, 14, true)
	cat("c_set_02", encoding.FieldTypeSetU32, 24, false)

	return &encoding.Schema{Fields: fields}
}

// joinShapeCell is one drawn field value: the raw on-wire value (or a
// wide set mask) plus its null flag.
type joinShapeCell struct {
	raw  uint64
	mask encoding.SetMask
	null bool
}

// drawJoinShapeCell draws field f's value. The key is supplied by the
// caller; every other value is a function of the stream only.
func drawJoinShapeCell(rng *rand.Rand, f *encoding.Field) joinShapeCell {
	var c joinShapeCell
	if f.Nullable && rng.IntN(10) == 0 {
		c.null = true
		return c
	}
	switch {
	case f.Type.IsCategorical():
		c.raw = uint64(rng.IntN(f.Dictionary.Count()))
	case f.Type == encoding.FieldTypePackedBool:
		c.raw = uint64(rng.IntN(2))
	case f.Type == encoding.FieldTypeU64:
		c.raw = rng.Uint64N(1 << 40)
	case f.Type.IsWideSet():
		n := f.Dictionary.Count()
		for range 1 + rng.IntN(4) {
			c.mask = c.mask.WithBit(rng.IntN(n))
		}
	case f.Type.IsSet():
		n := f.Dictionary.Count()
		for range 1 + rng.IntN(3) {
			c.raw |= 1 << rng.IntN(n)
		}
	default:
		panic(fmt.Sprintf("join-shape: no draw for %s", f.Type))
	}
	return c
}

func writeJoinShapeCell(buf *bytes.Buffer, f *encoding.Field, c joinShapeCell) error {
	switch {
	case f.Type.IsBitPacked():
		return buf.WriteByte(byte(c.raw))
	case f.Type.IsWideSet():
		return encoding.WriteSetMask(buf, f.Type, c.mask)
	default:
		return encoding.WriteFieldValue(buf, f.Type, c.raw)
	}
}

// buildJoinShapeCohort renders a complete single-file cohort (header +
// schema + payload) for the given parent count, deterministically from
// joinShapeSeed.
func buildJoinShapeCohort(parents int) (*encoding.Schema, []byte, error) {
	schema := joinShapeSchema()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		return nil, nil, err
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		return nil, nil, err
	}
	rng := rand.New(rand.NewPCG(joinShapeSeed, uint64(joinShapeParentFields)<<32|uint64(joinShapeChildFields)))
	parent := make([]joinShapeCell, joinShapeParentFields)
	child := make([]joinShapeCell, joinShapeChildFields)
	bitmap := make([]byte, schema.BitmapByteSize())
	for p := range parents {
		parent[0] = joinShapeCell{raw: uint64(p)}
		for i := 1; i < joinShapeParentFields; i++ {
			parent[i] = drawJoinShapeCell(rng, &schema.Fields[i])
		}
		for range joinShapeFanout(p) {
			for i := range joinShapeChildFields {
				child[i] = drawJoinShapeCell(rng, &schema.Fields[joinShapeParentFields+i])
			}
			clear(bitmap)
			for i := range schema.Fields {
				f := &schema.Fields[i]
				c := child[max(i-joinShapeParentFields, 0)]
				if i < joinShapeParentFields {
					c = parent[i]
				}
				if c.null {
					encoding.BitmapSetNull(bitmap, i)
				}
				if err := writeJoinShapeCell(&buf, f, c); err != nil {
					return nil, nil, fmt.Errorf("field %s: %w", f.Name, err)
				}
			}
			if len(bitmap) > 0 {
				if err := encoding.WriteBitmap(&buf, bitmap); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	return schema, buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// Pre-change baseline: the map-backed record.
//
// The positional Record replaced a record that held three name-keyed
// maps. The gates measure the positional record AGAINST that design,
// reproduced here in-test so the baseline cannot drift with the code
// under test:
//
//   - legacyBufferedRecord is the former Record struct field for field;
//     drainLegacyBuffered fills it exactly as the former buffered
//     streamingIterator branch did (per-row maps sized to the field or
//     retained count, decoded by the name-keyed map decoder, which still
//     ships for the non-reuse consumers).
//   - legacyReuseRecord is the former reuse record: name-keyed maps kept
//     across rows, driven through encoding.ReusableRecord (the decoder's
//     name-keyed shim), so every write hashes the field name.
// ---------------------------------------------------------------------------

type legacyBufferedRecord struct {
	schema *encoding.Schema
	values map[string]float64
	nulls  map[string]bool
	wide   map[string]any
	_      map[string]any // the former allValuesCache slot: keeps the struct its pre-change size
}

type legacyReuseRecord struct {
	values map[string]float64
	nulls  map[string]bool
	wide   map[string]any
}

func newLegacyReuseRecord(schema *encoding.Schema) *legacyReuseRecord {
	return &legacyReuseRecord{
		values: make(map[string]float64, len(schema.Fields)),
		nulls:  make(map[string]bool),
		wide:   make(map[string]any),
	}
}

func (r *legacyReuseRecord) SetNumeric(name string, v float64) { r.values[name] = v }
func (r *legacyReuseRecord) SetNullField(name string)          { r.nulls[name] = true }
func (r *legacyReuseRecord) SetWideField(name string, v any)   { r.wide[name] = v }
func (r *legacyReuseRecord) ClearForRow() {
	if len(r.nulls) > 0 {
		clear(r.nulls)
	}
	if len(r.wide) > 0 {
		clear(r.wide)
	}
}

// openLegacyReader positions a map-path RecordReader at the first record
// of the cohort at path, sourcing the bytes exactly as
// streamingIterator.initFromFile does — an off-heap mmap when the fs
// resolves to a real file, afero.ReadFile otherwise — so neither arm is
// billed for file bytes the other does not hold. The returned func
// releases the mapping.
func openLegacyReader(fsys afero.Fs, path string, schema *encoding.Schema) (*encoding.RecordReader, func(), error) {
	var data []byte
	release := func() {}
	if real, ok := resolveRealPath(fsys, path); ok {
		if m, cleanup, err := mmapFileBytes(real); err == nil {
			data, release = m, func() { _ = cleanup() }
		}
	}
	if data == nil {
		var err error
		if data, err = afero.ReadFile(fsys, path); err != nil {
			return nil, release, err
		}
	}
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, release, err
	}
	if _, err := encoding.ReadSchema(r); err != nil {
		return nil, release, err
	}
	return encoding.NewRecordReader(r, schema), release, nil
}

// drainLegacyBuffered materialises up to limit records of the cohort
// the way the pre-change buffered path did. keep/keepN select the
// projected (plan) arm; nil keep is the full decode.
//
// Every drain/scan helper below takes the same limit: the maximum row
// count to decode, which also sizes the output slice. Decoding one row
// versus all rows is how the gates separate per-row cost from the fixed
// per-open cost (schema and dictionary parse).
func drainLegacyBuffered(fsys afero.Fs, path string, schema *encoding.Schema, keep encoding.FieldFilter, keepN, limit int) ([]*legacyBufferedRecord, error) {
	rr, release, err := openLegacyReader(fsys, path, schema)
	defer release()
	if err != nil {
		return nil, err
	}
	var plan *encoding.DecodePlan
	mapHint := len(schema.Fields)
	if keep != nil {
		if plan, err = schema.BuildDecodePlan(retainedFromFilter(schema, keep)); err != nil {
			return nil, err
		}
		mapHint = keepN
	}
	out := make([]*legacyBufferedRecord, 0, limit)
	for len(out) < limit {
		values := make(map[string]float64, mapHint)
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		if plan != nil {
			err = rr.ReadRecordWithWidePlan(values, nulls, wide, keep, plan)
		} else {
			err = rr.ReadRecordWithWide(values, nulls, wide)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, &legacyBufferedRecord{schema: schema, values: values, nulls: nulls, wide: wide})
	}
	return out, nil
}

// scanLegacyReuse scans up to limit rows through the pre-change reuse
// record and returns the row count.
func scanLegacyReuse(fsys afero.Fs, path string, schema *encoding.Schema, limit int) (int, error) {
	rr, release, err := openLegacyReader(fsys, path, schema)
	defer release()
	if err != nil {
		return 0, err
	}
	rec := newLegacyReuseRecord(schema)
	n := 0
	for n < limit {
		err := rr.ReadRecordReused(rec)
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// drainPositionalBuffered materialises up to limit records through the
// production buffered path (a fresh positional Record per row).
func drainPositionalBuffered(fsys afero.Fs, path string, schema *encoding.Schema, keep encoding.FieldFilter, keepN, limit int) ([]*processing.Record, error) {
	it := newStreamingIterator(fsys, path, schema)
	defer it.Close()
	if keep != nil {
		it.SetProjection(keep, keepN)
	}
	out := make([]*processing.Record, 0, limit)
	for len(out) < limit && it.Next() {
		out = append(out, it.Record())
	}
	return out, it.Err()
}

// scanPositionalReuse scans up to limit rows through the production
// reuse path (one positional Record reused across rows).
func scanPositionalReuse(fsys afero.Fs, path string, schema *encoding.Schema, limit int) (int, error) {
	it := newStreamingIterator(fsys, path, schema)
	defer it.Close()
	it.SetReuse(true)
	n := 0
	for n < limit && it.Next() {
		n++
	}
	return n, it.Err()
}
