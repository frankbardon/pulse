package pulse

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// readerColumn is one column of the all-types reader fixture: its type,
// its dictionary size (dictionary-bearing types only) and, per row, the
// EXACT value CohortReader must return (nil = null).
type readerColumn struct {
	name string
	typ  encoding.FieldType
	dict int
	want []any
}

func readerDict(prefix string, n int) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := d.Add(fmt.Sprintf("%s%03d", prefix, i)); err != nil {
			panic(err)
		}
	}
	return d
}

func readerLabels(prefix string, bits ...int) []string {
	out := []string{}
	for _, b := range bits {
		out = append(out, fmt.Sprintf("%s%03d", prefix, b))
	}
	return out
}

// bigDecimal is a decimal128 mantissa no float64 carries exactly.
func bigDecimal(t *testing.T, s string) encoding.Decimal128 {
	t.Helper()
	m, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad mantissa %q", s)
	}
	d, err := encoding.NewDecimal128FromBigInt(m)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const readerDecimalScale = 6

// readerColumns is the oracle: every one of the 20 field types, every
// set rung, u64 > 2^53, datetime beyond 2^53 seconds and before the
// epoch, a pre-1970 date, a decimal at scale whose mantissa exceeds
// float64 precision, an empty set (distinct from null) and nulls.
func readerColumns(t *testing.T) []readerColumn {
	const p53 = uint64(1) << 53
	return []readerColumn{
		{"u4", encoding.FieldTypeU4, 0, []any{uint64(15), uint64(0), nil, uint64(7)}},
		{"u8", encoding.FieldTypeU8, 0, []any{uint64(255), uint64(0), nil, uint64(1)}},
		{"u16", encoding.FieldTypeU16, 0, []any{uint64(65535), uint64(1), nil, uint64(2)}},
		{"u32", encoding.FieldTypeU32, 0, []any{uint64(math.MaxUint32), uint64(2), nil, uint64(3)}},
		{"u64", encoding.FieldTypeU64, 0, []any{uint64(math.MaxUint64), p53 + 1, nil, uint64(1)<<63 | 1}},
		{"f32", encoding.FieldTypeF32, 0, []any{math.Float32frombits(0x3F8CCCCD), math.Float32frombits(0x80000000), nil, float32(math.MaxFloat32)}},
		{"f64", encoding.FieldTypeF64, 0, []any{math.Pi, math.SmallestNonzeroFloat64, nil, -1e300}},
		{"date", encoding.FieldTypeDate, 0, []any{int32(-719162), int32(2932896), nil, int32(0)}},
		{"datetime", encoding.FieldTypeDateTime, 0, []any{int64(p53 + 1), int64(-62135596800), nil, int64(math.MinInt64)}},
		{"flag", encoding.FieldTypePackedBool, 0, []any{true, false, nil, true}},
		{"cat8", encoding.FieldTypeCategoricalU8, 4, []any{"cat8000", "cat8003", nil, "cat8001"}},
		{"cat16", encoding.FieldTypeCategoricalU16, 300, []any{"cat16299", "cat16000", nil, "cat16256"}},
		{"cat32", encoding.FieldTypeCategoricalU32, 3, []any{"cat32002", "cat32000", nil, "cat32001"}},
		{"dec", encoding.FieldTypeDecimal128, 0, []any{
			bigDecimal(t, "12345678901234567890123456789"),
			bigDecimal(t, "-99999999999999999999999999999999999999"),
			nil,
			bigDecimal(t, "1"),
		}},
		{"s8", encoding.FieldTypeSetU8, 8, []any{readerLabels("s8"), readerLabels("s8", 0, 7), nil, readerLabels("s8", 3)}},
		{"s16", encoding.FieldTypeSetU16, 16, []any{readerLabels("s16", 15), readerLabels("s16"), nil, readerLabels("s16", 0, 8)}},
		{"s32", encoding.FieldTypeSetU32, 32, []any{readerLabels("s32", 0, 31), readerLabels("s32", 16), nil, readerLabels("s32")}},
		{"s64", encoding.FieldTypeSetU64, 64, []any{readerLabels("s64", 0, 53, 63), readerLabels("s64", 1), nil, readerLabels("s64", 62)}},
		{"s128", encoding.FieldTypeSetU128, 128, []any{readerLabels("s128", 0, 64, 127), readerLabels("s128"), nil, readerLabels("s128", 63, 65)}},
		{"s256", encoding.FieldTypeSetU256, 256, []any{readerLabels("s256", 1, 128, 255), readerLabels("s256", 200), nil, readerLabels("s256")}},
	}
}

// writeReaderFixture writes the all-types fixture as an ungrouped
// single-file cohort straight from the public raw-byte primitives — an
// oracle independent of the reader under test. Every field is nullable
// and row 2 is all-null.
func writeReaderFixture(t *testing.T, cols []readerColumn) ([]byte, *encoding.Schema) {
	t.Helper()
	schema := &encoding.Schema{}
	off := 0
	for _, c := range cols {
		f := encoding.Field{Name: c.name, Type: c.typ, Nullable: true, ByteOffset: off}
		if c.dict > 0 {
			f.Dictionary = readerDict(c.name, c.dict)
		}
		if c.typ == encoding.FieldTypeDecimal128 {
			f.Scale = readerDecimalScale
		}
		schema.Fields = append(schema.Fields, f)
		if c.typ.IsBitPacked() {
			off++
		} else {
			off += c.typ.ByteSize()
		}
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	rows := len(cols[0].want)
	for r := 0; r < rows; r++ {
		bitmap := make([]byte, schema.BitmapByteSize())
		for fi, c := range cols {
			f := &schema.Fields[fi]
			v := c.want[r]
			if v == nil {
				encoding.BitmapSetNull(bitmap, fi)
			}
			if err := writeReaderCell(&buf, f, v); err != nil {
				t.Fatalf("row %d field %s: %v", r, c.name, err)
			}
		}
		if err := encoding.WriteBitmap(&buf, bitmap); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes(), schema
}

func writeReaderCell(buf *bytes.Buffer, f *encoding.Field, v any) error {
	maskOf := func(v any) encoding.SetMask {
		m := encoding.SetMask{}
		if v == nil {
			return m
		}
		for _, l := range v.([]string) {
			id, ok := f.Dictionary.IDFor(l)
			if !ok {
				panic("label not in dictionary: " + l)
			}
			m = m.WithBit(int(id))
		}
		return m
	}
	switch f.Type {
	case encoding.FieldTypeU4:
		n, _ := v.(uint64)
		return encoding.WriteNibble(buf, false, uint8(n))
	case encoding.FieldTypePackedBool:
		b, _ := v.(bool)
		return encoding.WriteBit(buf, 0, b)
	case encoding.FieldTypeDecimal128:
		d, ok := v.(encoding.Decimal128)
		if !ok {
			d = encoding.ZeroDecimal128()
		}
		return encoding.WriteDecimal128(buf, d)
	case encoding.FieldTypeSetU128, encoding.FieldTypeSetU256:
		return encoding.WriteSetMask(buf, f.Type, maskOf(v))
	}
	var w uint64
	switch x := v.(type) {
	case nil:
	case uint64:
		w = x
	case float32:
		w = uint64(math.Float32bits(x))
	case float64:
		w = math.Float64bits(x)
	case int32:
		w = uint64(uint32(x))
	case int64:
		w = uint64(x)
	case string:
		id, ok := f.Dictionary.IDFor(x)
		if !ok {
			panic("label not in dictionary: " + x)
		}
		w = uint64(id)
	case []string:
		w, _ = maskOf(x).Uint64()
	}
	return encoding.WriteFieldValue(buf, f.Type, w)
}

func readerTypeCoverage(t *testing.T, cols []readerColumn) {
	t.Helper()
	seen := map[encoding.FieldType]bool{}
	for _, c := range cols {
		seen[c.typ] = true
	}
	if len(seen) != 20 {
		t.Fatalf("fixture covers %d field types, want all 20", len(seen))
	}
}

func openReader(t *testing.T, p *Pulse, path string) *CohortReader {
	t.Helper()
	c, err := p.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatalf("Reader(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// assertRowEqual compares a row slot by slot: same Go type AND same
// value (a Decimal128 by Cmp, a float by bit pattern, everything else
// by reflect.DeepEqual — which tells an empty set from nil).
func assertRowEqual(t *testing.T, label string, schema *encoding.Schema, got, want CohortRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: row has %d slots, want %d", label, len(got), len(want))
	}
	for i := range want {
		name := schema.Fields[i].Name
		g, w := got[i], want[i]
		if reflect.TypeOf(g) != reflect.TypeOf(w) {
			t.Errorf("%s %s: type %T (%v), want %T (%v)", label, name, g, g, w, w)
			continue
		}
		switch wv := w.(type) {
		case encoding.Decimal128:
			if g.(encoding.Decimal128).Cmp(wv) != 0 {
				t.Errorf("%s %s: %s, want %s", label, name, g.(encoding.Decimal128).String(readerDecimalScale), wv.String(readerDecimalScale))
			}
		case float32:
			if math.Float32bits(g.(float32)) != math.Float32bits(wv) {
				t.Errorf("%s %s: %v, want %v", label, name, g, wv)
			}
		case float64:
			if math.Float64bits(g.(float64)) != math.Float64bits(wv) {
				t.Errorf("%s %s: %v, want %v", label, name, g, wv)
			}
		default:
			if !reflect.DeepEqual(g, w) {
				t.Errorf("%s %s: %#v, want %#v", label, name, g, w)
			}
		}
	}
}

func readerFixtureEngine(t *testing.T) (*Pulse, afero.Fs, []readerColumn, *encoding.Schema, []byte) {
	t.Helper()
	cols := readerColumns(t)
	readerTypeCoverage(t, cols)
	data, schema := writeReaderFixture(t, cols)
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "all.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys, cols, schema, data
}

func wantRow(cols []readerColumn, r int) CohortRow {
	row := make(CohortRow, len(cols))
	for i, c := range cols {
		row[i] = c.want[r]
	}
	return row
}

// TestCohortReader_ExactEveryType: RecordAt returns the exact stored
// value, with the canonical Go type, for all 20 field types and nulls.
func TestCohortReader_ExactEveryType(t *testing.T) {
	p, _, cols, _, _ := readerFixtureEngine(t)
	r := openReader(t, p, "all.pulse")
	if r.Len() != int64(len(cols[0].want)) {
		t.Fatalf("Len = %d, want %d", r.Len(), len(cols[0].want))
	}
	if len(r.Schema().Fields) != len(cols) {
		t.Fatalf("Schema has %d fields, want %d", len(r.Schema().Fields), len(cols))
	}
	for i := int64(0); i < r.Len(); i++ {
		got, err := r.RecordAt(i)
		if err != nil {
			t.Fatalf("RecordAt(%d): %v", i, err)
		}
		assertRowEqual(t, fmt.Sprintf("row %d", i), r.Schema(), got, wantRow(cols, int(i)))
	}

	// An empty set is a non-nil empty slice; a null set is nil.
	row0, _ := r.RecordAt(0)
	if s, ok := row0[14].([]string); !ok || s == nil || len(s) != 0 {
		t.Fatalf("empty set_u8 = %#v, want []string{}", row0[14])
	}
	row2, _ := r.RecordAt(2)
	for i, v := range row2 {
		if v != nil {
			t.Fatalf("all-null row slot %s = %#v, want nil", r.Schema().Fields[i].Name, v)
		}
	}
}

// TestCohortReader_RowsAreCallerOwned: mutating a returned row (and its
// set slice) never reaches a later read.
func TestCohortReader_RowsAreCallerOwned(t *testing.T) {
	p, _, cols, _, _ := readerFixtureEngine(t)
	r := openReader(t, p, "all.pulse")
	a, err := r.RecordAt(1)
	if err != nil {
		t.Fatal(err)
	}
	a[0] = "clobbered"
	a[14].([]string)[0] = "clobbered"
	b, err := r.RecordAt(1)
	if err != nil {
		t.Fatal(err)
	}
	assertRowEqual(t, "re-read row 1", r.Schema(), b, wantRow(cols, 1))
}

// TestCohortReader_GroupedMatchesUngroupedTwin: a grouped (0x02)
// single-file cohort reads identically to its ungrouped twin, both on
// the all-types fixture (a group spanning a decimal, a datetime, a
// categorical, a narrow and a wide set, nullable members included) and
// on the facade's 360-row grouped twin (indexed + constant groups).
func TestCohortReader_GroupedMatchesUngroupedTwin(t *testing.T) {
	t.Run("all-types", func(t *testing.T) {
		p, fsys, cols, _, data := readerFixtureEngine(t)
		var out bytes.Buffer
		gs, n, err := encx.DedupCohort(&out, bytes.NewReader(data), []encx.GroupSpec{
			{Kind: encoding.GroupKindIndexed, Members: []string{"cat16", "dec", "datetime", "s64", "s256"}},
			{Kind: encoding.GroupKindIndexed, Members: []string{"u4", "flag", "u64"}},
		})
		if err != nil {
			t.Fatalf("DedupCohort: %v", err)
		}
		if n != int64(len(cols[0].want)) || gs.RequiredFormatVersion() != encoding.FormatVersionV2 {
			t.Fatalf("dedup: %d rows, version %d", n, gs.RequiredFormatVersion())
		}
		if err := afero.WriteFile(fsys, "grouped.pulse", out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		flat := openReader(t, p, "all.pulse")
		grouped := openReader(t, p, "grouped.pulse")
		if !grouped.Schema().HasGroups() {
			t.Fatal("grouped fixture schema carries no groups")
		}
		compareReaders(t, flat, grouped)
		for i := int64(0); i < grouped.Len(); i++ {
			got, err := grouped.RecordAt(i)
			if err != nil {
				t.Fatal(err)
			}
			assertRowEqual(t, fmt.Sprintf("grouped row %d", i), grouped.Schema(), got, wantRow(cols, int(i)))
		}
	})
	t.Run("facade-twin", func(t *testing.T) {
		v1, v2 := groupedTwinFS(t)
		p1, err := New(Options{FS: v1})
		if err != nil {
			t.Fatal(err)
		}
		p2, err := New(Options{FS: v2})
		if err != nil {
			t.Fatal(err)
		}
		flat := openReader(t, p1, "cohort.pulse")
		grouped := openReader(t, p2, "cohort.pulse")
		if !grouped.Schema().HasGroups() || flat.Schema().HasGroups() {
			t.Fatal("twin fixture: want one flat and one grouped cohort")
		}
		if flat.Len() != 360 {
			t.Fatalf("flat Len = %d, want 360", flat.Len())
		}
		compareReaders(t, flat, grouped)
	})
}

func compareReaders(t *testing.T, flat, grouped *CohortReader) {
	t.Helper()
	if flat.Len() != grouped.Len() {
		t.Fatalf("Len flat %d, grouped %d", flat.Len(), grouped.Len())
	}
	for i := range flat.Schema().Fields {
		if flat.Schema().Fields[i].Name != grouped.Schema().Fields[i].Name {
			t.Fatalf("field %d: flat %q, grouped %q", i, flat.Schema().Fields[i].Name, grouped.Schema().Fields[i].Name)
		}
	}
	for i := int64(0); i < flat.Len(); i++ {
		a, err := flat.RecordAt(i)
		if err != nil {
			t.Fatalf("flat RecordAt(%d): %v", i, err)
		}
		b, err := grouped.RecordAt(i)
		if err != nil {
			t.Fatalf("grouped RecordAt(%d): %v", i, err)
		}
		assertRowEqual(t, fmt.Sprintf("record %d", i), flat.Schema(), b, a)
	}
}

// TestCohortReader_Errors: an out-of-range index and a read after Close
// are coded errors, never panics.
func TestCohortReader_Errors(t *testing.T) {
	p, _, _, _, _ := readerFixtureEngine(t)
	c, err := p.Open(context.Background(), "all.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int64{-1, r.Len(), r.Len() + 1, math.MaxInt64} {
		if _, err := r.RecordAt(i); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
			t.Errorf("RecordAt(%d) error = %v, want SERVICE_VALIDATION", i, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := r.RecordAt(0); !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Fatalf("RecordAt after Close error = %v, want SERVICE_RESOURCE", err)
	}
}

// TestCohortReader_TruncatedTailIgnored: a trailing partial record is
// not addressable (Len floors, as CountRecords does).
func TestCohortReader_TruncatedTailIgnored(t *testing.T) {
	p, fsys, cols, _, data := readerFixtureEngine(t)
	if err := afero.WriteFile(fsys, "trunc.pulse", append(append([]byte{}, data...), 0x01, 0x02), 0o644); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, p, "trunc.pulse")
	if r.Len() != int64(len(cols[0].want)) {
		t.Fatalf("Len = %d, want %d", r.Len(), len(cols[0].want))
	}
}

// TestCohortReader_ConcurrentRecordAt fans RecordAt across goroutines on
// an in-memory FS (mutex-serialised ReaderAt) and an on-disk DataDir
// (positional pread); run under -race.
func TestCohortReader_ConcurrentRecordAt(t *testing.T) {
	cols := readerColumns(t)
	data, _ := writeReaderFixture(t, cols)

	dir := t.TempDir()
	if err := afero.WriteFile(afero.NewOsFs(), filepath.Join(dir, "all.pulse"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "all.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string]Options{"memmap": {FS: mem}, "datadir": {DataDir: dir}} {
		t.Run(name, func(t *testing.T) {
			p, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			r := openReader(t, p, "all.pulse")
			var wg sync.WaitGroup
			errs := make(chan error, 16)
			for g := 0; g < 16; g++ {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					for k := 0; k < 50; k++ {
						i := int64((g + k) % len(cols[0].want))
						row, err := r.RecordAt(i)
						if err != nil {
							errs <- err
							return
						}
						if u, ok := row[4].(uint64); i != 2 && (!ok || u != cols[4].want[i]) {
							errs <- fmt.Errorf("record %d u64 = %#v", i, row[4])
							return
						}
					}
				}(g)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
		})
	}
}

// TestCohortRow_DistinctFromRecord: CohortRow is its own slice type, not
// Sample's name-keyed Record.
func TestCohortRow_DistinctFromRecord(t *testing.T) {
	if reflect.TypeOf(CohortRow{}).Kind() != reflect.Slice || reflect.TypeOf(Record{}).Kind() != reflect.Map {
		t.Fatal("CohortRow must be a slice and Record a map")
	}
	if reflect.TypeOf(CohortRow{}).PkgPath() != "github.com/frankbardon/pulse" ||
		reflect.TypeOf(CohortReader{}).PkgPath() != "github.com/frankbardon/pulse" {
		t.Fatal("CohortRow / CohortReader must be declared in the root package")
	}
}

// TestCohortReader_ArchiveAllTypes: a two-shard archive of the all-types
// fixture reads through the facade with a global index — Len is the sum
// over shards, record n+i is shard two's record i, every value exact —
// an anchor reads its one shard, and RecordAt fans across the shard
// boundary concurrently on an in-memory FS and an on-disk DataDir (run
// under -race).
func TestCohortReader_ArchiveAllTypes(t *testing.T) {
	cols := readerColumns(t)
	data, _ := writeReaderFixture(t, cols)
	n := len(cols[0].want)

	// Build the archive in memory, then mirror it onto disk.
	mem := afero.NewMemMapFs()
	for _, name := range []string{"a.pulse", "b.pulse"} {
		if err := afero.WriteFile(mem, name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pm, err := New(Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pm.CreateShardArchive(context.Background(), "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	archive, err := afero.ReadFile(mem, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := afero.WriteFile(afero.NewOsFs(), filepath.Join(dir, "arch.pulse"), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string]Options{"memmap": {FS: mem}, "datadir": {DataDir: dir}} {
		t.Run(name, func(t *testing.T) {
			p, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			r := openReader(t, p, "arch.pulse")
			if r.Len() != int64(2*n) {
				t.Fatalf("archive Len = %d, want %d", r.Len(), 2*n)
			}
			for i := int64(0); i < r.Len(); i++ {
				got, err := r.RecordAt(i)
				if err != nil {
					t.Fatalf("RecordAt(%d): %v", i, err)
				}
				assertRowEqual(t, fmt.Sprintf("archive row %d", i), r.Schema(), got, wantRow(cols, int(i)%n))
			}
			anchor := openReader(t, p, "arch.pulse#b.pulse")
			if anchor.Len() != int64(n) {
				t.Fatalf("anchor Len = %d, want %d", anchor.Len(), n)
			}
			for i := int64(0); i < anchor.Len(); i++ {
				got, err := anchor.RecordAt(i)
				if err != nil {
					t.Fatalf("anchor RecordAt(%d): %v", i, err)
				}
				assertRowEqual(t, fmt.Sprintf("anchor row %d", i), anchor.Schema(), got, wantRow(cols, int(i)))
			}

			var wg sync.WaitGroup
			errs := make(chan error, 16)
			for g := 0; g < 16; g++ {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					for k := 0; k < 60; k++ {
						i := int64((g + k) % (2 * n))
						row, err := r.RecordAt(i)
						if err != nil {
							errs <- err
							return
						}
						w := cols[4].want[int(i)%n]
						if u, _ := row[4].(uint64); w != nil && u != w {
							errs <- fmt.Errorf("record %d u64 = %#v, want %v", i, row[4], w)
							return
						}
					}
				}(g)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
		})
	}
}
