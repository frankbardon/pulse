package encoding

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// fullDecodeRecordCount is the record count a FULL decode derives from
// one standalone cohort: header + ReadSchema (dictionaries and group
// entries allocated), then the remaining bytes over the stride — what
// PeekShardRecordCount computed before it read the schema layout-only.
func fullDecodeRecordCount(t *testing.T, payload []byte) (count int64, consumed int, stride int) {
	t.Helper()
	r := bytes.NewReader(payload)
	s, _, err := ReadPreamble(r)
	if err != nil {
		t.Fatalf("ReadPreamble: %v", err)
	}
	consumed = len(payload) - r.Len()
	stride = s.RecordByteSize()
	if stride == 0 {
		return 0, consumed, 0
	}
	return int64(r.Len() / stride), consumed, stride
}

// storeArchive packs payloads as uncompressed entries s00.pulse, ... in
// order (no canonical entry: PeekShardRecordCount reads one entry).
func storeArchive(t testing.TB, payloads [][]byte) *Archive {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, p := range payloads {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("s%02d.pulse", i), Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	arch, err := OpenArchive(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return arch
}

// dictHeavyCohort: a u32 id, a categorical_u32 whose dictionary holds
// entries ~40-byte labels, a set_u8 and a nullable f64 — rows records.
func dictHeavyCohort(t testing.TB, entries, rows int) []byte {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := range entries {
		if _, err := dict.Add(fmt.Sprintf("label-%07d-abcdefghijklmnopqrstuvwxyz", i)); err != nil {
			t.Fatal(err)
		}
	}
	tags := encoding.NewDictionary()
	for _, l := range []string{"a", "b", "c"} {
		if _, err := tags.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU32, Dictionary: dict},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: tags},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
	}}
	LayoutFields(s)
	var b bytes.Buffer
	if err := WritePreamble(&b, s); err != nil {
		t.Fatal(err)
	}
	row := make([]byte, s.RecordByteSize())
	for i := range rows {
		binary.LittleEndian.PutUint32(row[0:], uint32(i))
		binary.LittleEndian.PutUint32(row[4:], uint32(i%entries))
		row[8] = byte(i % 8)
		binary.LittleEndian.PutUint64(row[9:], math.Float64bits(float64(i)))
		row[17] = byte(i % 2) // x null on odd rows
		b.Write(row)
	}
	return b.Bytes()
}

// groupedHeavyCohort: a 0x02 cohort whose one indexed parent group holds
// rows distinct entries (every row its own parent tuple), so its entry
// table is rows x 16 bytes.
func groupedHeavyCohort(t testing.TB, rows int) []byte {
	t.Helper()
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "pid", Type: encoding.FieldTypeU64},
		{Name: "pv", Type: encoding.FieldTypeF64},
		{Name: "v", Type: encoding.FieldTypeU16},
	}}
	LayoutFields(s)
	var flat bytes.Buffer
	if err := WritePreamble(&flat, s); err != nil {
		t.Fatal(err)
	}
	row := make([]byte, s.RecordByteSize())
	for i := range rows {
		binary.LittleEndian.PutUint64(row[0:], uint64(i))
		binary.LittleEndian.PutUint64(row[8:], math.Float64bits(float64(i)/3))
		binary.LittleEndian.PutUint16(row[16:], uint16(i))
		flat.Write(row)
	}
	var out bytes.Buffer
	if _, _, err := DedupCohort(&out, bytes.NewReader(flat.Bytes()), []GroupSpec{{Kind: encoding.GroupKindIndexed, Members: []string{"pid", "pv"}}}); err != nil {
		t.Fatalf("DedupCohort: %v", err)
	}
	return out.Bytes()
}

// TestPeekShardRecordCount_LayoutMatchesFullDecode (E8-S4):
// PeekShardRecordCount reads the schema block layout-only, and its count
// is the one a full decode derives — on every shape whose stride the
// layout reader must reconstruct without its dictionaries: categorical
// and set dictionaries, wide sets (set_u128 / set_u256), decimals and
// bit-packed runs, 0x02 parent groups (indexed and constant, nullable
// members, a member-only bitmap), 0x02 vectors (alone and inside a
// constant group), a truncated tail, the committed format fixtures and
// every shard of the committed archives. The layout schema also stops
// at the same byte as ReadSchema and reports the same stride, with no
// dictionary or entry table attached.
func TestPeekShardRecordCount_LayoutMatchesFullDecode(t *testing.T) {
	type shard struct {
		name    string
		payload []byte
	}
	var shards []shard
	for _, fx := range groupFixtures(t) {
		flat := flatCohort(t, fx.schema, fx.rows)
		grouped, _ := groupedTwin(t, flat, fx.specs)
		shards = append(shards, shard{fx.name + "/flat", flat}, shard{fx.name + "/grouped", grouped})
	}
	vec := vecCohortFile(t, vecCohortSchema())
	gv := groupedVecSchema()
	var gvb bytes.Buffer
	if err := WritePreamble(&gvb, gv); err != nil {
		t.Fatal(err)
	}
	gvb.Write([]byte{5, 0, 6, 0})
	dict := dictHeavyCohort(t, 5000, 300)
	shards = append(shards,
		shard{"vectors", vec},
		shard{"vector in a constant group", gvb.Bytes()},
		shard{"dictionary heavy", dict},
		shard{"dictionary heavy, truncated tail", dict[:len(dict)-3]},
		shard{"grouped heavy", groupedHeavyCohort(t, 2000)},
	)
	for _, f := range []string{"format_v1.pulse", "format_v2_grouped.pulse"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "encoding", "testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		shards = append(shards, shard{f, b})
	}
	committed, err := filepath.Glob(filepath.Join("..", "..", "testdata", "sharding", "*.pulse"))
	if err != nil || len(committed) == 0 {
		t.Fatalf("committed archives: %v %v", committed, err)
	}
	for _, path := range committed {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		arch, err := OpenArchive(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range arch.Entries() {
			if e.Name == ReservedSchemaName {
				continue
			}
			rc, err := arch.Open(e.Name)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			shards = append(shards, shard{filepath.Base(path) + "#" + e.Name, b})
		}
	}

	payloads := make([][]byte, len(shards))
	for i, s := range shards {
		payloads[i] = s.payload
	}
	arch := storeArchive(t, payloads)
	sawGroups, sawVectors, sawDict := false, false, false
	for i, s := range shards {
		want, consumed, stride := fullDecodeRecordCount(t, s.payload)
		got, err := arch.PeekShardRecordCount(fmt.Sprintf("s%02d.pulse", i))
		if err != nil {
			t.Fatalf("%s: PeekShardRecordCount: %v", s.name, err)
		}
		if got != want {
			t.Errorf("%s: PeekShardRecordCount = %d, a full decode counts %d", s.name, got, want)
		}

		r := bytes.NewReader(s.payload)
		v, err := encoding.ReadHeader(r)
		if err != nil {
			t.Fatal(err)
		}
		layout, err := readSchemaLayout(r, v)
		if err != nil {
			t.Fatalf("%s: readSchemaLayout: %v", s.name, err)
		}
		if at := len(s.payload) - r.Len(); at != consumed {
			t.Errorf("%s: layout read stops at byte %d, ReadSchema at %d", s.name, at, consumed)
		}
		if layout.RecordByteSize() != stride {
			t.Errorf("%s: layout stride %d, full stride %d", s.name, layout.RecordByteSize(), stride)
		}
		for _, f := range layout.Fields {
			if f.Dictionary != nil {
				t.Errorf("%s: field %q carries a dictionary after a layout-only read", s.name, f.Name)
			}
			sawDict = sawDict || f.Type.HasDictionary()
		}
		for g := range layout.Groups {
			if layout.Groups[g].Entries != nil {
				t.Errorf("%s: group %d carries entries after a layout-only read", s.name, g)
			}
		}
		sawGroups = sawGroups || layout.HasGroups()
		sawVectors = sawVectors || layout.HasVectors()
	}
	if !sawGroups || !sawVectors || !sawDict {
		t.Fatalf("fixture coverage: groups %v, vectors %v, dictionaries %v", sawGroups, sawVectors, sawDict)
	}
}

// TestPeekShardRecordCount_LayoutRefusesWhatReadSchemaRefuses: the
// layout-only reader keeps ReadSchema's refusals over the bytes it
// skips — a dictionary or group entry table cut short, and a constant
// group declaring other than one entry — as PULSE_SHARD_HEADER_INVALID.
func TestPeekShardRecordCount_LayoutRefusesWhatReadSchemaRefuses(t *testing.T) {
	dict := dictHeavyCohort(t, 50, 0)
	grouped := groupedHeavyCohort(t, 40)
	gv := groupedVecSchema()
	var gvb bytes.Buffer
	if err := WritePreamble(&gvb, gv); err != nil {
		t.Fatal(err)
	}
	constant := gvb.Bytes()
	// The constant group over the vec_f64 member: GROUPS ends with its
	// u32 entry_count (1) and the one 24-byte entry. Declare two entries
	// and carry two.
	pre, secs, _ := splitExtension(t, constant, gv)
	body := secs[0].body
	countAt := len(body) - 24 - 4
	if secs[0].tag != 1 || binary.LittleEndian.Uint32(body[countAt:]) != 1 {
		t.Fatal("constant group entry count not found")
	}
	entry := body[countAt+4:]
	twoBody := binary.LittleEndian.AppendUint32(bytes.Clone(body[:countAt]), 2)
	twoBody = append(append(twoBody, entry...), entry...)
	twoEntries := joinExtension(pre, []extSection{{tag: secs[0].tag, flags: secs[0].flags, body: twoBody}, secs[1]}, nil)

	cases := map[string][]byte{
		"dictionary cut short":      dict[:len(dict)-20],
		"group entries cut short":   grouped[:encoding.HeaderSize+60],
		"constant group, 2 entries": twoEntries,
	}
	for name, payload := range cases {
		r := bytes.NewReader(payload)
		v, err := encoding.ReadHeader(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := encoding.ReadSchema(bytes.NewReader(payload[encoding.HeaderSize:]), v); err == nil {
			t.Fatalf("%s: ReadSchema accepts the fixture; the case is vacuous", name)
		}
		if _, err := readSchemaLayout(r, v); err == nil {
			t.Errorf("%s: readSchemaLayout accepts what ReadSchema refuses", name)
		}
		arch := storeArchive(t, [][]byte{payload})
		if _, err := arch.PeekShardRecordCount("s00.pulse"); err == nil {
			t.Errorf("%s: PeekShardRecordCount accepts what ReadSchema refuses", name)
		}
	}
}

// TestPeekShardRecordCount_SkipsDictionaries (E8-S4): counting a shard's
// records allocates a few KB whatever its dictionaries and group
// entries hold — a full decode of the same shards allocates megabytes.
// Service.Open peeks every shard this way, so a dictionary-heavy
// archive no longer pays a whole decode per shard just to be opened
// (the memory model's schema-decode term dropped from 2 x shards + 1 to
// shards + 1 with it).
func TestPeekShardRecordCount_SkipsDictionaries(t *testing.T) {
	dict := dictHeavyCohort(t, 100_000, 64)
	grouped := groupedHeavyCohort(t, 50_000)
	arch := storeArchive(t, [][]byte{dict, grouped})
	allocated := func(f func()) uint64 {
		f() // warm: pools, zip directory caches
		var before, after runtime.MemStats
		const runs = 5
		runtime.GC()
		runtime.ReadMemStats(&before)
		for range runs {
			f()
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / runs
	}
	for i, name := range []string{"100,000-entry dictionary", "50,000-entry parent group"} {
		entry := fmt.Sprintf("s%02d.pulse", i)
		payload := [][]byte{dict, grouped}[i]
		peek := allocated(func() {
			if _, err := arch.PeekShardRecordCount(entry); err != nil {
				t.Fatal(err)
			}
		})
		full := allocated(func() {
			if _, _, err := ReadPreamble(bytes.NewReader(payload)); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("%s: peek allocates %d bytes, a full decode %d", name, peek, full)
		if full < 512<<10 {
			t.Fatalf("%s: a full decode allocates only %d bytes; the fixture is too small to pin the drop", name, full)
		}
		if peek > 64<<10 {
			t.Errorf("%s: PeekShardRecordCount allocates %d bytes (a full decode: %d), want <= 64 KiB — it must not decode the dictionaries", name, peek, full)
		}
	}
}
