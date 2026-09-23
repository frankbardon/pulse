package encoding_test

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pfs "github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Fixtures
//
// Every fixture cohort has the same three-field shape so a widen's effect on
// the fields AROUND the set column is directly observable:
//
//	id     u32   — the prefix field, offset must not move
//	picks  set_*  — the field being widened
//	score  f64   — the suffix field, offset MUST move by the width delta
//
// plus an optional trailing null bitmap driven by `score` being nullable.
// ---------------------------------------------------------------------------

type widenRow struct {
	id       uint32
	mask     encoding.SetMask
	score    float64
	nullScor bool
}

// widenMaskOf builds a SetMask from explicit bit indices. Deliberately NOT
// reusing maskOf from shard_rewrite_wide_set_test.go's fixture set so the two
// files stay independently readable.
func widenMaskOf(bits ...int) encoding.SetMask {
	var m encoding.SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	return m
}

func widenDict(t *testing.T, n int) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := d.Add(fmt.Sprintf("entry-%03d", i)); err != nil {
			t.Fatalf("dict add %d: %v", i, err)
		}
	}
	return d
}

func widenSchema(t *testing.T, setType encoding.FieldType, nullable bool, dictEntries int) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "picks", Type: setType, ByteOffset: 4, CsvColumnIdx: 1,
				Dictionary: widenDict(t, dictEntries), Description: "multi-select"},
			{Name: "score", Type: encoding.FieldTypeF64, Nullable: nullable,
				ByteOffset: 4 + setType.ByteSize(), CsvColumnIdx: 2},
		},
	}
}

// buildWidenCohort emits a complete single-file .pulse cohort.
func buildWidenCohort(t *testing.T, schema *encoding.Schema, rows []widenRow) []byte {
	t.Helper()
	setType := schema.Fields[1].Type
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	bmSize := schema.BitmapByteSize()
	for _, r := range rows {
		var id [4]byte
		binary.LittleEndian.PutUint32(id[:], r.id)
		buf.Write(id[:])
		if setType.IsWideSet() {
			if err := encoding.WriteSetMask(&buf, setType, r.mask); err != nil {
				t.Fatalf("WriteSetMask: %v", err)
			}
		} else {
			low, ok := r.mask.Uint64()
			if !ok {
				t.Fatalf("fixture mask does not fit a narrow rung")
			}
			if err := encoding.WriteFieldValue(&buf, setType, low); err != nil {
				t.Fatalf("WriteFieldValue: %v", err)
			}
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(r.score)); err != nil {
			t.Fatalf("WriteFieldValue(score): %v", err)
		}
		if bmSize > 0 {
			bm := make([]byte, bmSize)
			if r.nullScor {
				encoding.BitmapSetNull(bm, 2)
			}
			if err := encoding.WriteBitmap(&buf, bm); err != nil {
				t.Fatalf("WriteBitmap: %v", err)
			}
		}
	}
	return buf.Bytes()
}

// readWidenCohort decodes a cohort back into its schema and rows using the
// stride the schema itself declares.
func readWidenCohort(t *testing.T, b []byte) (*encoding.Schema, []widenRow) {
	t.Helper()
	r := bytes.NewReader(b)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	setType := schema.Fields[1].Type
	stride := schema.RecordByteSize()
	tail := b[len(b)-r.Len():]
	if len(tail)%stride != 0 {
		t.Fatalf("payload %d bytes is not a multiple of stride %d", len(tail), stride)
	}
	setOff := 4
	scoreOff := setOff + setType.ByteSize()
	bmSize := schema.BitmapByteSize()
	out := make([]widenRow, 0, len(tail)/stride)
	for i := 0; i < len(tail); i += stride {
		rec := tail[i : i+stride]
		var m encoding.SetMask
		if setType.IsWideSet() {
			m, err = encoding.SetMaskFromBytes(setType, rec[setOff:scoreOff])
			if err != nil {
				t.Fatalf("SetMaskFromBytes: %v", err)
			}
		} else {
			v, err := encoding.ReadFieldValue(bytes.NewReader(rec[setOff:scoreOff]), setType)
			if err != nil {
				t.Fatalf("ReadFieldValue(set): %v", err)
			}
			m = encoding.SetMaskFromUint64(v)
		}
		row := widenRow{
			id:    binary.LittleEndian.Uint32(rec[0:4]),
			mask:  m,
			score: math.Float64frombits(binary.LittleEndian.Uint64(rec[scoreOff : scoreOff+8])),
		}
		if bmSize > 0 {
			row.nullScor = encoding.BitmapIsNull(rec[stride-bmSize:stride], 2)
		}
		out = append(out, row)
	}
	return schema, out
}

func assertWidenRowsEqual(t *testing.T, want, got []widenRow) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("record count: want %d, got %d", len(want), len(got))
	}
	for i := range want {
		if want[i].id != got[i].id {
			t.Errorf("record %d id: want %d, got %d", i, want[i].id, got[i].id)
		}
		if !want[i].mask.Equal(got[i].mask) {
			t.Errorf("record %d mask: want %v, got %v", i, want[i].mask.Words(), got[i].mask.Words())
		}
		if math.Float64bits(want[i].score) != math.Float64bits(got[i].score) {
			t.Errorf("record %d score: want %v, got %v", i, want[i].score, got[i].score)
		}
		if want[i].nullScor != got[i].nullScor {
			t.Errorf("record %d null: want %v, got %v", i, want[i].nullScor, got[i].nullScor)
		}
	}
}

// ---------------------------------------------------------------------------
// Re-stride behaviour
// ---------------------------------------------------------------------------

func TestWidenSetFieldBytes_RestridesEveryRung(t *testing.T) {
	cases := []struct {
		name     string
		from, to encoding.FieldType
		dictN    int
		rows     []widenRow
		nullable bool
	}{
		{
			// Narrow -> narrow. Bit 7 is the highest bit set_u8 can carry;
			// a rewrite that dropped the top of the source byte fails here.
			name: "set_u8 to set_u16",
			from: encoding.FieldTypeSetU8, to: encoding.FieldTypeSetU16, dictN: 8,
			rows: []widenRow{
				{id: 1, mask: widenMaskOf(0, 7), score: 1.5},
				{id: 2, mask: widenMaskOf(), score: -2.25},
			},
		},
		{
			// Narrow -> wide across the 64-bit word boundary. Bit 63 is the
			// source's highest; a low-word-only copy still passes without it,
			// which is exactly the vacuity E1-S3 hit on this arithmetic.
			name: "set_u64 to set_u128",
			from: encoding.FieldTypeSetU64, to: encoding.FieldTypeSetU128, dictN: 64,
			rows: []widenRow{
				{id: 10, mask: widenMaskOf(0, 40, 63), score: 3.5},
				{id: 11, mask: widenMaskOf(63), score: 0},
				{id: 12, mask: widenMaskOf(), score: math.Inf(1)},
			},
		},
		{
			// Wide -> wide. Source bits 100 and 127 live in words[1]; a
			// SetMaskFromUint64 shortcut on the read side loses both.
			name: "set_u128 to set_u256",
			from: encoding.FieldTypeSetU128, to: encoding.FieldTypeSetU256, dictN: 128,
			rows: []widenRow{
				{id: 20, mask: widenMaskOf(1, 64, 100, 127), score: 9.5},
				{id: 21, mask: widenMaskOf(127), score: -1},
			},
		},
		{
			name: "set_u32 to set_u256 skipping rungs",
			from: encoding.FieldTypeSetU32, to: encoding.FieldTypeSetU256, dictN: 32,
			rows: []widenRow{
				{id: 30, mask: widenMaskOf(0, 31), score: 42},
			},
		},
		{
			name: "set_u64 to set_u128 with a null bitmap",
			from: encoding.FieldTypeSetU64, to: encoding.FieldTypeSetU128, dictN: 64,
			rows: []widenRow{
				{id: 40, mask: widenMaskOf(63), score: 0, nullScor: true},
				{id: 41, mask: widenMaskOf(2, 62), score: 7.25},
				{id: 42, mask: widenMaskOf(), score: 0, nullScor: true},
			},
			nullable: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srcSchema := widenSchema(t, tc.from, tc.nullable, tc.dictN)
			src := buildWidenCohort(t, srcSchema, tc.rows)
			srcCopy := append([]byte(nil), src...)

			out, rep, err := encoding.WidenSetFieldBytes(src, "picks", tc.to)
			if err != nil {
				t.Fatalf("WidenSetFieldBytes: %v", err)
			}
			if !bytes.Equal(src, srcCopy) {
				t.Fatalf("input bytes were mutated in place")
			}

			gotSchema, gotRows := readWidenCohort(t, out)

			if gotSchema.Fields[1].Type != tc.to {
				t.Fatalf("widened field type: want %s, got %s", tc.to, gotSchema.Fields[1].Type)
			}
			if gotSchema.Fields[0].Type != encoding.FieldTypeU32 || gotSchema.Fields[2].Type != encoding.FieldTypeF64 {
				t.Fatalf("neighbour field types changed: %v", gotSchema.Fields)
			}
			// Offsets: the prefix is pinned, the suffix moves by the delta.
			if gotSchema.Fields[0].ByteOffset != 0 {
				t.Errorf("id offset: want 0, got %d", gotSchema.Fields[0].ByteOffset)
			}
			if gotSchema.Fields[1].ByteOffset != 4 {
				t.Errorf("picks offset: want 4, got %d", gotSchema.Fields[1].ByteOffset)
			}
			wantScoreOff := 4 + tc.to.ByteSize()
			if gotSchema.Fields[2].ByteOffset != wantScoreOff {
				t.Errorf("score offset: want %d, got %d", wantScoreOff, gotSchema.Fields[2].ByteOffset)
			}
			// Nullability and the bitmap survive.
			if gotSchema.Fields[2].Nullable != tc.nullable {
				t.Errorf("score nullable: want %v, got %v", tc.nullable, gotSchema.Fields[2].Nullable)
			}
			if gotSchema.HasBitmap() != tc.nullable {
				t.Errorf("HasBitmap: want %v, got %v", tc.nullable, gotSchema.HasBitmap())
			}
			wantStride := 4 + tc.to.ByteSize() + 8 + srcSchema.BitmapByteSize()
			if gotSchema.RecordByteSize() != wantStride {
				t.Errorf("stride: want %d, got %d", wantStride, gotSchema.RecordByteSize())
			}
			// Description and dictionary ride along untouched.
			if gotSchema.Fields[1].Description != "multi-select" {
				t.Errorf("description lost: %q", gotSchema.Fields[1].Description)
			}
			gotDict, ok := gotSchema.SetField("picks")
			if !ok {
				t.Fatalf("widened field lost its dictionary")
			}
			srcDict := srcSchema.Fields[1].Dictionary
			if gotDict.Count() != srcDict.Count() {
				t.Fatalf("dictionary size: want %d, got %d", srcDict.Count(), gotDict.Count())
			}
			for i := 0; i < srcDict.Count(); i++ {
				if gotDict.Resolve(uint32(i)) != srcDict.Resolve(uint32(i)) {
					t.Errorf("dictionary entry %d: want %q, got %q",
						i, srcDict.Resolve(uint32(i)), gotDict.Resolve(uint32(i)))
				}
			}

			assertWidenRowsEqual(t, tc.rows, gotRows)

			if rep == nil {
				t.Fatalf("nil report")
			}
			if rep.Field != "picks" || rep.From != tc.from || rep.To != tc.to {
				t.Errorf("report identity: %+v", rep)
			}
			if rep.Records != int64(len(tc.rows)) {
				t.Errorf("report records: want %d, got %d", len(tc.rows), rep.Records)
			}
			if rep.StrideBefore != srcSchema.RecordByteSize() || rep.StrideAfter != wantStride {
				t.Errorf("report stride: want %d->%d, got %d->%d",
					srcSchema.RecordByteSize(), wantStride, rep.StrideBefore, rep.StrideAfter)
			}
		})
	}
}

// TestWidenSetFieldBytes_NeighbourBytesAreByteIdentical checks the criterion
// directly on the wire rather than through a decoder: the bytes of every
// non-widened field are lifted out of both payloads and compared.
func TestWidenSetFieldBytes_NeighbourBytesAreByteIdentical(t *testing.T) {
	rows := []widenRow{
		{id: 0xDEADBEEF, mask: widenMaskOf(0, 63), score: 1.25, nullScor: true},
		{id: 0x01020304, mask: widenMaskOf(17), score: -1e300},
		{id: 7, mask: widenMaskOf(), score: math.NaN()},
	}
	srcSchema := widenSchema(t, encoding.FieldTypeSetU64, true, 64)
	src := buildWidenCohort(t, srcSchema, rows)

	out, _, err := encoding.WidenSetFieldBytes(src, "picks", encoding.FieldTypeSetU256)
	if err != nil {
		t.Fatalf("WidenSetFieldBytes: %v", err)
	}

	srcStride := srcSchema.RecordByteSize() // 4 + 8 + 8 + 1
	dstStride := 4 + 32 + 8 + 1
	srcPayload := src[len(src)-srcStride*len(rows):]
	dstPayload := out[len(out)-dstStride*len(rows):]

	for i := range rows {
		sr := srcPayload[i*srcStride : (i+1)*srcStride]
		dr := dstPayload[i*dstStride : (i+1)*dstStride]
		// id
		if !bytes.Equal(sr[0:4], dr[0:4]) {
			t.Errorf("record %d: id bytes differ: %x vs %x", i, sr[0:4], dr[0:4])
		}
		// score + trailing bitmap: one contiguous suffix on both sides.
		if !bytes.Equal(sr[12:srcStride], dr[36:dstStride]) {
			t.Errorf("record %d: suffix bytes differ: %x vs %x", i, sr[12:srcStride], dr[36:dstStride])
		}
		// The widened slot's low 8 bytes are the old payload verbatim and
		// everything above it is zero — the append-zero-bytes property.
		if !bytes.Equal(sr[4:12], dr[4:12]) {
			t.Errorf("record %d: set low word differs: %x vs %x", i, sr[4:12], dr[4:12])
		}
		for j := 12; j < 36; j++ {
			if dr[j] != 0 {
				t.Errorf("record %d: byte %d of the widened slot is %#x, want 0", i, j, dr[j])
			}
		}
	}
}

func TestWidenSetFieldBytes_ZeroRecords(t *testing.T) {
	srcSchema := widenSchema(t, encoding.FieldTypeSetU64, false, 64)
	src := buildWidenCohort(t, srcSchema, nil)

	out, rep, err := encoding.WidenSetFieldBytes(src, "picks", encoding.FieldTypeSetU128)
	if err != nil {
		t.Fatalf("WidenSetFieldBytes: %v", err)
	}
	if rep.Records != 0 {
		t.Errorf("records: want 0, got %d", rep.Records)
	}
	schema, rowsOut := readWidenCohort(t, out)
	if len(rowsOut) != 0 {
		t.Errorf("rows: want 0, got %d", len(rowsOut))
	}
	if schema.Fields[1].Type != encoding.FieldTypeSetU128 {
		t.Errorf("type not widened on an empty cohort: %s", schema.Fields[1].Type)
	}
}

func TestWidenSetFieldBytes_TruncatedTailRefused(t *testing.T) {
	srcSchema := widenSchema(t, encoding.FieldTypeSetU64, false, 64)
	src := buildWidenCohort(t, srcSchema, []widenRow{{id: 1, mask: widenMaskOf(63), score: 1}})
	truncated := src[:len(src)-3]

	_, _, err := encoding.WidenSetFieldBytes(truncated, "picks", encoding.FieldTypeSetU128)
	if err == nil {
		t.Fatalf("want an error on a truncated tail")
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("want ENCODING_INVALID, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

func TestWidenSetFieldBytes_Refusals(t *testing.T) {
	cases := []struct {
		name     string
		from     encoding.FieldType
		field    string
		to       encoding.FieldType
		wantCode errors.Code
		wantMsg  string
	}{
		{name: "non-set source field", from: encoding.FieldTypeSetU64, field: "id",
			to: encoding.FieldTypeSetU128, wantCode: errors.ENCODING_TYPE_MISMATCH, wantMsg: "not a set type"},
		{name: "non-set target", from: encoding.FieldTypeSetU64, field: "picks",
			to: encoding.FieldTypeU64, wantCode: errors.ENCODING_TYPE_MISMATCH, wantMsg: "not a set type"},
		{name: "equal width target", from: encoding.FieldTypeSetU64, field: "picks",
			to: encoding.FieldTypeSetU64, wantCode: errors.ENCODING_TYPE_MISMATCH, wantMsg: "not wider"},
		{name: "narrower target", from: encoding.FieldTypeSetU64, field: "picks",
			to: encoding.FieldTypeSetU8, wantCode: errors.ENCODING_TYPE_MISMATCH, wantMsg: "not wider"},
		{name: "past the widest rung", from: encoding.FieldTypeSetU256, field: "picks",
			to: encoding.FieldTypeSetU256, wantCode: errors.ENCODING_TYPE_MISMATCH, wantMsg: "widest set rung"},
		{name: "unknown field", from: encoding.FieldTypeSetU64, field: "nope",
			to: encoding.FieldTypeSetU128, wantCode: errors.ENCODING_INVALID, wantMsg: "no field named"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dictN := 8
			if tc.from.MaxSetEntries() < 8 {
				dictN = int(tc.from.MaxSetEntries())
			}
			schema := widenSchema(t, tc.from, false, dictN)
			src := buildWidenCohort(t, schema, []widenRow{{id: 1, mask: widenMaskOf(1), score: 1}})

			_, _, err := encoding.WidenSetFieldBytes(src, tc.field, tc.to)
			if err == nil {
				t.Fatalf("want a refusal")
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("want a coded error, got %v", err)
			}
			if ce.Code != tc.wantCode {
				t.Errorf("code: want %s, got %s", tc.wantCode, ce.Code)
			}
			if !strings.Contains(ce.Error(), tc.wantMsg) {
				t.Errorf("message %q does not mention %q", ce.Error(), tc.wantMsg)
			}
		})
	}
}

func TestCheckSetWiden(t *testing.T) {
	if err := encoding.CheckSetWiden(encoding.FieldTypeSetU8, encoding.FieldTypeSetU256); err != nil {
		t.Errorf("set_u8 -> set_u256 should be allowed: %v", err)
	}
	if err := encoding.CheckSetWiden(encoding.FieldTypeSetU128, encoding.FieldTypeSetU256); err != nil {
		t.Errorf("set_u128 -> set_u256 should be allowed: %v", err)
	}
	if err := encoding.CheckSetWiden(encoding.FieldTypeSetU256, encoding.FieldTypeSetU256); err == nil {
		t.Errorf("set_u256 is the widest rung and must refuse")
	}
	if err := encoding.CheckSetWiden(encoding.FieldTypeCategoricalU8, encoding.FieldTypeSetU16); err == nil {
		t.Errorf("a categorical source must refuse")
	}
}

// ---------------------------------------------------------------------------
// Atomic file path
// ---------------------------------------------------------------------------

// widenOpFs records the ORDER of Sync and Rename so the temp-fsync-rename
// sequence is asserted rather than assumed, and optionally fails writes after
// a byte budget so a mid-write failure can be injected.
type widenOpFs struct {
	afero.Fs
	ops    *[]string
	budget *int // nil = unlimited
}

type widenOpFile struct {
	afero.File
	parent *widenOpFs
}

func (f *widenOpFile) Write(p []byte) (int, error) {
	if f.parent.budget == nil {
		return f.File.Write(p)
	}
	if *f.parent.budget <= 0 {
		return 0, fmt.Errorf("injected write failure")
	}
	if len(p) > *f.parent.budget {
		n, _ := f.File.Write(p[:*f.parent.budget])
		*f.parent.budget = 0
		return n, fmt.Errorf("injected write failure")
	}
	*f.parent.budget -= len(p)
	return f.File.Write(p)
}

func (f *widenOpFile) Sync() error {
	*f.parent.ops = append(*f.parent.ops, "sync:"+f.File.Name())
	return f.File.Sync()
}

func (fs *widenOpFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if flag&os.O_CREATE != 0 {
		return &widenOpFile{File: f, parent: fs}, nil
	}
	return f, nil
}

func (fs *widenOpFs) Rename(oldname, newname string) error {
	*fs.ops = append(*fs.ops, "rename:"+oldname+"->"+newname)
	return fs.Fs.Rename(oldname, newname)
}

func widenDirEntries(t *testing.T, fsys afero.Fs, dir string) []string {
	t.Helper()
	infos, err := afero.ReadDir(fsys, dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	names := make([]string, 0, len(infos))
	for _, fi := range infos {
		names = append(names, fi.Name())
	}
	return names
}

func TestWidenSetFieldFile_AtomicSyncThenRename(t *testing.T) {
	rows := []widenRow{
		{id: 1, mask: widenMaskOf(0, 63), score: 1.5, nullScor: true},
		{id: 2, mask: widenMaskOf(31), score: 2.5},
	}
	srcSchema := widenSchema(t, encoding.FieldTypeSetU64, true, 64)
	src := buildWidenCohort(t, srcSchema, rows)

	base := pfs.NewMemMap().Fs()
	var ops []string
	fsys := &widenOpFs{Fs: base, ops: &ops}
	path := filepath.Join("/data", "cohort.pulse")
	if err := afero.WriteFile(fsys, path, src, 0o644); err != nil {
		t.Fatalf("seed cohort: %v", err)
	}

	rep, err := encoding.WidenSetFieldFile(fsys, path, "picks", encoding.FieldTypeSetU128)
	if err != nil {
		t.Fatalf("WidenSetFieldFile: %v", err)
	}
	if rep.Records != int64(len(rows)) {
		t.Errorf("records: want %d, got %d", len(rows), rep.Records)
	}

	// The file path result must equal the pure bytes result exactly.
	wantBytes, _, err := encoding.WidenSetFieldBytes(src, "picks", encoding.FieldTypeSetU128)
	if err != nil {
		t.Fatalf("WidenSetFieldBytes: %v", err)
	}
	gotBytes, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatalf("read widened cohort: %v", err)
	}
	if !bytes.Equal(wantBytes, gotBytes) {
		t.Fatalf("file arm and bytes arm disagree (%d vs %d bytes)", len(wantBytes), len(gotBytes))
	}

	// Exactly one file in the directory: the temp was renamed, not left behind.
	if names := widenDirEntries(t, fsys, "/data"); len(names) != 1 || names[0] != "cohort.pulse" {
		t.Errorf("directory should hold only the cohort, got %v", names)
	}

	// Sync happens, and it happens BEFORE the rename.
	syncIdx, renameIdx := -1, -1
	for i, op := range ops {
		if strings.HasPrefix(op, "sync:") && syncIdx < 0 {
			syncIdx = i
		}
		if strings.HasPrefix(op, "rename:") && renameIdx < 0 {
			renameIdx = i
		}
	}
	if syncIdx < 0 {
		t.Errorf("temp file was never fsynced; ops=%v", ops)
	}
	if renameIdx < 0 {
		t.Fatalf("no rename happened; ops=%v", ops)
	}
	if syncIdx > renameIdx {
		t.Errorf("fsync must precede rename; ops=%v", ops)
	}
}

func TestWidenSetFieldFile_MidWriteFailureLeavesOriginalByteIdentical(t *testing.T) {
	rows := make([]widenRow, 0, 200)
	for i := 0; i < 200; i++ {
		rows = append(rows, widenRow{id: uint32(i), mask: widenMaskOf(i%64, 63), score: float64(i)})
	}
	srcSchema := widenSchema(t, encoding.FieldTypeSetU64, true, 64)
	src := buildWidenCohort(t, srcSchema, rows)

	base := pfs.NewMemMap().Fs()
	path := filepath.Join("/data", "cohort.pulse")
	// Seed through the BARE fs: the injecting wrapper exists to break the
	// widen, not the fixture.
	if err := afero.WriteFile(base, path, src, 0o644); err != nil {
		t.Fatalf("seed cohort: %v", err)
	}
	var ops []string
	budget := 64 // enough for the header, nowhere near the payload
	fsys := &widenOpFs{Fs: base, ops: &ops, budget: &budget}

	if _, err := encoding.WidenSetFieldFile(fsys, path, "picks", encoding.FieldTypeSetU128); err == nil {
		t.Fatalf("want an error from the injected write failure")
	}
	// The budget was fully consumed, so bytes really did land in the temp
	// file before the failure — this is a MID-write abort, not a failure
	// that happened before anything was written.
	if budget != 0 {
		t.Fatalf("injection never reached a partial write (budget left %d)", budget)
	}

	got, err := afero.ReadFile(base, path)
	if err != nil {
		t.Fatalf("read cohort after failure: %v", err)
	}
	if !bytes.Equal(src, got) {
		t.Fatalf("original cohort was modified by a failed widen (%d vs %d bytes)", len(src), len(got))
	}
	if names := widenDirEntries(t, fsys, "/data"); len(names) != 1 || names[0] != "cohort.pulse" {
		t.Errorf("failed widen left a temp file behind: %v", names)
	}
	for _, op := range ops {
		if strings.HasPrefix(op, "rename:") {
			t.Errorf("a failed widen must not rename anything; ops=%v", ops)
		}
	}
}

func TestWidenSetFieldFile_RefusalLeavesFileUntouched(t *testing.T) {
	srcSchema := widenSchema(t, encoding.FieldTypeSetU64, false, 64)
	src := buildWidenCohort(t, srcSchema, []widenRow{{id: 1, mask: widenMaskOf(63), score: 1}})

	fsys := pfs.NewMemMap().Fs()
	path := "/data/cohort.pulse"
	if err := afero.WriteFile(fsys, path, src, 0o644); err != nil {
		t.Fatalf("seed cohort: %v", err)
	}

	if _, err := encoding.WidenSetFieldFile(fsys, path, "picks", encoding.FieldTypeSetU8); err == nil {
		t.Fatalf("want a refusal for a narrower target")
	}
	got, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatalf("read cohort: %v", err)
	}
	if !bytes.Equal(src, got) {
		t.Fatalf("a refused widen modified the cohort")
	}
	if names := widenDirEntries(t, fsys, "/data"); len(names) != 1 {
		t.Errorf("a refused widen left files behind: %v", names)
	}
}

func TestWidenSetFieldFile_MissingCohort(t *testing.T) {
	fsys := pfs.NewMemMap().Fs()
	_, err := encoding.WidenSetFieldFile(fsys, "/data/nope.pulse", "picks", encoding.FieldTypeSetU128)
	if err == nil {
		t.Fatalf("want an error for a missing cohort")
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a coded error, got %v", err)
	}
}

// TestWidenSetFieldBytes_BitPackedNeighboursKeepTheirByte pins the one
// offset rule a plain "sum of ByteSize()" walk gets wrong: u4 and
// packed_bool report ByteSize()==0 yet occupy one whole byte each on the
// wire. A widen that summed ByteSize() would place every field after a
// bit-packed neighbour one byte too early on BOTH sides and produce a
// cohort that still opens.
func TestWidenSetFieldBytes_BitPackedNeighboursKeepTheirByte(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "flag", Type: encoding.FieldTypePackedBool, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "picks", Type: encoding.FieldTypeSetU8, ByteOffset: 1, CsvColumnIdx: 1,
				Dictionary: widenDict(t, 8)},
			{Name: "nib", Type: encoding.FieldTypeU4, ByteOffset: 2, CsvColumnIdx: 2},
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 3, CsvColumnIdx: 3},
		},
	}
	if got := schema.RecordByteSize(); got != 7 {
		t.Fatalf("fixture stride: want 7, got %d", got)
	}
	records := [][]byte{
		{0x01, 0x81, 0x0D, 0xEF, 0xBE, 0xAD, 0xDE}, // picks bits 0 and 7
		{0x00, 0x00, 0x07, 0x04, 0x03, 0x02, 0x01},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, rec := range records {
		buf.Write(rec)
	}
	src := buf.Bytes()

	out, rep, err := encoding.WidenSetFieldBytes(src, "picks", encoding.FieldTypeSetU64)
	if err != nil {
		t.Fatalf("WidenSetFieldBytes: %v", err)
	}
	if rep.StrideBefore != 7 || rep.StrideAfter != 14 {
		t.Fatalf("stride: want 7->14, got %d->%d", rep.StrideBefore, rep.StrideAfter)
	}

	r := bytes.NewReader(out)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	got, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	wantOffsets := []int{0, 1, 9, 10}
	for i, want := range wantOffsets {
		if got.Fields[i].ByteOffset != want {
			t.Errorf("field %q offset: want %d, got %d", got.Fields[i].Name, want, got.Fields[i].ByteOffset)
		}
	}
	if got.RecordByteSize() != 14 {
		t.Fatalf("widened stride: want 14, got %d", got.RecordByteSize())
	}

	tail := out[len(out)-r.Len():]
	for i, srcRec := range records {
		dstRec := tail[i*14 : (i+1)*14]
		if dstRec[0] != srcRec[0] {
			t.Errorf("record %d: packed_bool byte moved: %#x vs %#x", i, srcRec[0], dstRec[0])
		}
		if dstRec[1] != srcRec[1] {
			t.Errorf("record %d: set low byte: want %#x, got %#x", i, srcRec[1], dstRec[1])
		}
		for j := 2; j < 9; j++ {
			if dstRec[j] != 0 {
				t.Errorf("record %d: widened slot byte %d is %#x, want 0", i, j, dstRec[j])
			}
		}
		if dstRec[9] != srcRec[2] {
			t.Errorf("record %d: u4 byte: want %#x, got %#x", i, srcRec[2], dstRec[9])
		}
		if !bytes.Equal(dstRec[10:14], srcRec[3:7]) {
			t.Errorf("record %d: id bytes: want %x, got %x", i, srcRec[3:7], dstRec[10:14])
		}
	}
}
