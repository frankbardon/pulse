package encoding

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// groupFixture is one synthetic parent/child cohort: a flat (0x01)
// schema, its rows, and the group specs that dedup it.
type groupFixture struct {
	name   string
	schema *Schema
	rows   [][]byte
	specs  []GroupSpec
	// subsets are retained sets for the plan paths.
	subsets [][]string
}

// fieldIdx resolves names to logical positions.
func fieldIdx(t *testing.T, s *Schema, names ...string) []int {
	t.Helper()
	var out []int
	for _, n := range names {
		f := -1
		for i := range s.Fields {
			if s.Fields[i].Name == n {
				f = i
			}
		}
		if f < 0 {
			t.Fatalf("fixture names unknown field %q", n)
		}
		out = append(out, f)
	}
	return out
}

// parentChildRows builds n rows with real parent structure: every row
// starts as a random donor record, then each group's member fields
// (bytes AND null bit) are overwritten from that group's parent record
// — parent = row / fanout, so members repeat across a parent's children
// exactly as a denormalised join repeats its parent block. A constant
// group's members come from records[0] on every row.
func parentChildRows(t *testing.T, s *Schema, records [][]byte, specs []GroupSpec, fanouts []int, n int, seed int64) [][]byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	spans := fieldSpans(s)
	bm := s.BitmapByteSize()
	body := s.RecordByteSize() - bm
	rows := make([][]byte, n)
	for r := 0; r < n; r++ {
		row := append([]byte(nil), records[rng.Intn(len(records))]...)
		for g, sp := range specs {
			src := records[0]
			if sp.Kind == GroupKindIndexed {
				src = records[((r/fanouts[g])*7+g*3)%len(records)]
			}
			for _, fi := range fieldIdx(t, s, sp.Members...) {
				copy(row[spans[fi][0]:spans[fi][1]], src[spans[fi][0]:spans[fi][1]])
				if bm > 0 {
					row[body+fi/8] &^= 1 << uint(fi%8)
					if BitmapIsNull(src[body:], fi) {
						BitmapSetNull(row[body:], fi)
					}
				}
			}
		}
		rows[r] = row
	}
	return rows
}

func groupFixtures(t *testing.T) []groupFixture {
	t.Helper()
	big := bigMixedSchema()
	bigRecs := generateBigSchemaRecords(t, big, 0x6A0B16)
	bigSpecs := []GroupSpec{
		// Parent block: straddles the bit-packed runs (u4_hi without
		// u4_lo, pb_0 without pb_1), carries nullable decimal and a
		// nullable wide set.
		{Kind: GroupKindIndexed, Members: []string{"u16_a", "u8_a", "u4_hi", "pb_0", "u32_a", "cat_u16", "amount", "tags_u128", "tags_u256"}},
		// An independent second parent level.
		{Kind: GroupKindIndexed, Members: []string{"date_a", "cat_u8", "f64_b"}},
		// A global constant, one nullable member.
		{Kind: GroupKindConstant, Members: []string{"u8_c", "tags_u64"}},
	}
	ws := wideSetDecodeSchema()
	wsRecs, _ := generateWideSetRecords(t, ws, 64, 0x6A0B5E7)
	// Every nullable field is a member: the physical row has no bitmap.
	wsSpecs := []GroupSpec{{Kind: GroupKindIndexed, Members: []string{"cat", "tags128", "score"}}}
	return []groupFixture{
		{
			name: "big_mixed", schema: big, specs: bigSpecs,
			rows: parentChildRows(t, big, bigRecs, bigSpecs, []int{13, 5, 1}, 400, 0x6A0B1),
			subsets: [][]string{
				{"u8_a"}, {"u4_hi"}, {"u4_lo", "pb_1"}, {"amount", "u8_b"}, {"u16_c"},
				{"tags_u128", "tags_u64", "u8_c"}, {"date_a", "u32_b"}, {"cat_u16", "f64_b", "u64_b"},
			},
		},
		{
			name: "wide_set_all_nullable_grouped", schema: ws, specs: wsSpecs,
			rows:    parentChildRows(t, ws, wsRecs, wsSpecs, []int{7}, 200, 0x6A0B2),
			subsets: [][]string{{"score"}, {"tail"}, {"tags128", "id"}, {"flag"}},
		},
	}
}

// flatCohort is header + schema + rows at 0x01.
func flatCohort(t *testing.T, s *Schema, rows [][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := WritePreamble(&b, s); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		b.Write(r)
	}
	return b.Bytes()
}

// groupedTwin dedups the flat cohort and returns the 0x02 bytes and
// schema as read back from them.
func groupedTwin(t *testing.T, flat []byte, specs []GroupSpec) ([]byte, *Schema) {
	t.Helper()
	var out bytes.Buffer
	if _, _, err := DedupCohort(&out, bytes.NewReader(flat), specs); err != nil {
		t.Fatalf("DedupCohort: %v", err)
	}
	r := bytes.NewReader(out.Bytes())
	s, v, err := ReadPreamble(r)
	if err != nil {
		t.Fatalf("ReadPreamble(grouped): %v", err)
	}
	if v != FormatVersionV2 {
		t.Fatalf("grouped cohort version = 0x%02x, want 0x02", v)
	}
	return out.Bytes(), s
}

// recordRegion returns the bytes after the preamble.
func recordRegion(t *testing.T, data []byte) (*Schema, []byte) {
	t.Helper()
	r := bytes.NewReader(data)
	s, _, err := ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	return s, data[len(data)-r.Len():]
}

// TestGroupedCohort_LogicalStreamIsTheFlatRecordRegion is the structural
// half of "indistinguishable from the undeduped equivalent": the logical
// stream of the 0x02 cohort is, byte for byte, the 0x01 twin's record
// region — so every decoder that runs over it is running over exactly
// the bytes it runs over for 0x01.
func TestGroupedCohort_LogicalStreamIsTheFlatRecordRegion(t *testing.T) {
	for _, fx := range groupFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			flat := flatCohort(t, fx.schema, fx.rows)
			grouped, gs := groupedTwin(t, flat, fx.specs)
			_, flatRegion := recordRegion(t, flat)
			_, physRegion := recordRegion(t, grouped)

			lr, logical, err := NewLogicalStream(bytes.NewReader(physRegion), gs)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(logical.Fields, fx.schema.Fields) {
				t.Fatal("logical schema fields differ from the flat schema")
			}
			got, err := io.ReadAll(lr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, flatRegion) {
				t.Fatalf("logical stream (%d bytes) differs from the flat record region (%d bytes)", len(got), len(flatRegion))
			}
			if len(physRegion) >= len(flatRegion) {
				t.Fatalf("grouped payload %d bytes is not smaller than flat %d", len(physRegion), len(flatRegion))
			}
		})
	}
}

// TestGroupedCohort_EveryDecodePathMatchesFlat drives the map decoder,
// the reuse decoder (index-keyed), the plan decoders under several
// retained sets, the run-skip decoder and the O(1) record locator over
// the 0x02 cohort, and compares each record with the same path over the
// 0x01 twin.
func TestGroupedCohort_EveryDecodePathMatchesFlat(t *testing.T) {
	setBackoff(t, 32, 0)
	for _, fx := range groupFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			flat := flatCohort(t, fx.schema, fx.rows)
			grouped, gs := groupedTwin(t, flat, fx.specs)
			fs, flatRegion := recordRegion(t, flat)
			_, physRegion := recordRegion(t, grouped)

			type decodeFn func(rr *RecordReader, s *Schema) (any, error)
			mapDecode := func(keep FieldFilter, plan func(*Schema) *DecodePlan) decodeFn {
				return func(rr *RecordReader, s *Schema) (any, error) {
					v, n, w := map[string]float64{}, map[string]bool{}, map[string]any{}
					err := rr.ReadRecordWithWidePlan(v, n, w, keep, plan(s))
					return []any{v, n, w}, err
				}
			}
			reuseDecode := func(keep FieldFilter, plan func(*Schema) *DecodePlan) decodeFn {
				return func(rr *RecordReader, s *Schema) (any, error) {
					rec := &dualRecord{indexedTestRecord: newIndexedTestRecord(s)}
					err := rr.ReadRecordReusedWithPlan(rec, keep, plan(s))
					return []any{rec.values, rec.nulls, rec.wide}, err
				}
			}
			noPlan := func(*Schema) *DecodePlan { return nil }
			paths := map[string]decodeFn{
				"map":   mapDecode(nil, noPlan),
				"reuse": reuseDecode(nil, noPlan),
			}
			for i, retained := range fx.subsets {
				retained := retained
				plan := func(s *Schema) *DecodePlan {
					p, err := s.BuildDecodePlan(retained)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				paths[fmt.Sprintf("map_plan%d", i)] = mapDecode(keepFromNames(retained), plan)
				paths[fmt.Sprintf("reuse_plan%d", i)] = reuseDecode(keepFromNames(retained), plan)
			}
			for name, fn := range paths {
				rf := NewRecordReader(bytes.NewReader(flatRegion), fs)
				rg := NewRecordReader(bytes.NewReader(physRegion), gs)
				for k := 0; ; k++ {
					want, werr := fn(rf, fs)
					got, gerr := fn(rg, gs)
					if werr != gerr {
						t.Fatalf("%s row %d: err flat=%v grouped=%v", name, k, werr, gerr)
					}
					if werr == io.EOF {
						if k != len(fx.rows) {
							t.Fatalf("%s: %d rows, want %d", name, k, len(fx.rows))
						}
						break
					}
					if werr != nil {
						t.Fatalf("%s row %d: %v", name, k, werr)
					}
					if !reflect.DeepEqual(want, got) {
						t.Fatalf("%s row %d differs:\n flat   =%v\n grouped=%v", name, k, want, got)
					}
				}
			}

			// Run-skip: one record through the whole stream, compared row
			// by row with a fresh full decode of the flat row.
			rec := newRunSkipTestRecord(gs)
			rg := NewRecordReader(bytes.NewReader(physRegion), gs)
			for k, row := range fx.rows {
				if err := rg.ReadRecordReused(rec); err != nil {
					t.Fatalf("run-skip row %d: %v", k, err)
				}
				assertSameState(t, fmt.Sprintf("run-skip row %d", k), rec, referenceDecode(t, fs, row, nil, nil))
			}
			if rec.keptRows != len(fx.rows)-1 {
				t.Fatalf("run-skip kept %d rows, want %d", rec.keptRows, len(fx.rows)-1)
			}

			// Record locator: first, middle, last.
			floc, err := NewRecordLocator(bytes.NewReader(flat), fs)
			if err != nil {
				t.Fatal(err)
			}
			gloc, err := NewRecordLocator(bytes.NewReader(grouped), gs)
			if err != nil {
				t.Fatal(err)
			}
			if gloc.TotalRecords != uint64(len(fx.rows)) || gloc.Stride != int64(gs.RecordByteSize()) {
				t.Fatalf("grouped locator total/stride = %d/%d, want %d/%d", gloc.TotalRecords, gloc.Stride, len(fx.rows), gs.RecordByteSize())
			}
			for _, i := range []uint64{0, uint64(len(fx.rows) / 2), uint64(len(fx.rows) - 1)} {
				var maps [2][]any
				for j, pair := range []struct {
					loc  *RecordLocator
					data []byte
				}{{floc, flat}, {gloc, grouped}} {
					v, n, w := map[string]float64{}, map[string]bool{}, map[string]any{}
					if err := pair.loc.ReadRecordAt(bytes.NewReader(pair.data), i, v, n, w, nil, nil); err != nil {
						t.Fatalf("ReadRecordAt(%d): %v", i, err)
					}
					maps[j] = []any{v, n, w}
				}
				if !reflect.DeepEqual(maps[0], maps[1]) {
					t.Fatalf("ReadRecordAt(%d) differs:\n flat   =%v\n grouped=%v", i, maps[0], maps[1])
				}
			}
		})
	}
}

// TestGroupedSchema_StrideBitmapAndCount pins the physical geometry:
// the reduced stride, the narrowed bitmap (absent when every nullable
// field is a member), and the count derived from file length.
func TestGroupedSchema_StrideBitmapAndCount(t *testing.T) {
	for _, fx := range groupFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			flat := flatCohort(t, fx.schema, fx.rows)
			grouped, gs := groupedTwin(t, flat, fx.specs)
			_, phys := recordRegion(t, grouped)

			members := map[int]bool{}
			indexed := 0
			for _, g := range gs.Groups {
				if g.Kind == GroupKindIndexed {
					indexed++
				}
				for _, m := range g.Members {
					members[m.Field] = true
				}
			}
			wantBody, rowFields, rowNullable := indexed*4, 0, false
			for i, f := range gs.Fields {
				if members[i] {
					continue
				}
				rowFields++
				wantBody += onWireWidth(f.Type)
				rowNullable = rowNullable || f.Nullable
			}
			wantBM := 0
			if rowNullable {
				wantBM = (rowFields + 7) / 8
			}
			if gs.HasBitmap() != rowNullable || gs.BitmapByteSize() != wantBM {
				t.Fatalf("HasBitmap/BitmapByteSize = %v/%d, want %v/%d", gs.HasBitmap(), gs.BitmapByteSize(), rowNullable, wantBM)
			}
			if gs.RecordByteSize() != wantBody+wantBM {
				t.Fatalf("RecordByteSize = %d, want %d", gs.RecordByteSize(), wantBody+wantBM)
			}
			if gs.Logical().RecordByteSize() != fx.schema.RecordByteSize() {
				t.Fatalf("Logical().RecordByteSize = %d, want the flat stride %d", gs.Logical().RecordByteSize(), fx.schema.RecordByteSize())
			}
			n, trailing, ok := gs.RecordCountForPayload(int64(len(phys)))
			if !ok || n != int64(len(fx.rows)) || trailing != 0 {
				t.Fatalf("RecordCountForPayload = %d/%d/%v, want %d/0/true", n, trailing, ok, len(fx.rows))
			}
			n, trailing, _ = gs.RecordCountForPayload(int64(len(phys) - 1))
			if n != int64(len(fx.rows)-1) || trailing != int64(gs.RecordByteSize()-1) {
				t.Fatalf("truncated tail: count/trailing = %d/%d", n, trailing)
			}
		})
	}
	// The all-nullable-members fixture is the one that loses its bitmap.
	fx := groupFixtures(t)[1]
	_, gs := groupedTwin(t, flatCohort(t, fx.schema, fx.rows), fx.specs)
	if !fx.schema.HasBitmap() || gs.HasBitmap() {
		t.Fatalf("flat HasBitmap=%v grouped HasBitmap=%v, want true/false", fx.schema.HasBitmap(), gs.HasBitmap())
	}
}

// TestGroupDescriptor_RoundTrip writes a grouped schema and reads every
// descriptor field back: kinds, members, key flags, entries, entry
// widths and counts, and each member's own Dictionary untouched.
func TestGroupDescriptor_RoundTrip(t *testing.T) {
	fx := groupFixtures(t)[0]
	flat := flatCohort(t, fx.schema, fx.rows)
	_, gs := groupedTwin(t, flat, fx.specs)

	// Mark key members on a copy, then round-trip it.
	src := &Schema{Fields: gs.Fields, Groups: append([]Group(nil), gs.Groups...)}
	src.Groups[0].Members = append([]GroupMember(nil), src.Groups[0].Members...)
	src.Groups[0].Members[1].Key = false
	var b bytes.Buffer
	if err := WritePreamble(&b, src); err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(b.Bytes())
	got, v, err := ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	if v != FormatVersionV2 || r.Len() != 0 {
		t.Fatalf("version 0x%02x, %d bytes left", v, r.Len())
	}
	if !reflect.DeepEqual(got.Groups, src.Groups) {
		t.Fatalf("groups differ after round trip:\n got=%+v\nwant=%+v", got.Groups, src.Groups)
	}
	schemasEqual(t, got, fx.schema)
	for g := range got.Groups {
		if got.GroupEntryCount(g) != src.GroupEntryCount(g) || got.GroupEntryWidth(g) != src.GroupEntryWidth(g) {
			t.Fatalf("group %d count/width differ", g)
		}
	}
	if got.GroupEntryCount(0) != len(fx.rows)/13+1 {
		t.Fatalf("group 0 entries = %d, want %d (one per parent)", got.GroupEntryCount(0), len(fx.rows)/13+1)
	}
	if got.GroupEntryCount(2) != 1 || got.Groups[2].Kind != GroupKindConstant || got.GroupIndexOffset(2) != -1 {
		t.Fatalf("constant group: count=%d kind=%d idxOff=%d", got.GroupEntryCount(2), got.Groups[2].Kind, got.GroupIndexOffset(2))
	}
}

// TestGroupDescriptor_WireLayout pins the extension bytes of a minimal
// grouped schema, field by field.
func TestGroupDescriptor_WireLayout(t *testing.T) {
	s := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU16, Nullable: true},
			{Name: "c", Type: FieldTypeU8},
		},
		Groups: []Group{{
			Kind:    GroupKindIndexed,
			Members: []GroupMember{{Field: 0, Key: true}, {Field: 1}},
			// entry = a(1) b(2) + member null bitmap(1): two entries,
			// the second with b null.
			Entries: []byte{7, 0x34, 0x12, 0, 9, 0, 0, 0b10},
		}},
	}
	var descs bytes.Buffer
	if err := writeFieldDescriptors(&descs, s); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := WritePreamble(&b, s); err != nil {
		t.Fatal(err)
	}
	ext := b.Bytes()[HeaderSize+descs.Len():]
	want := []byte{
		42, 0, 0, 0, 0, 0, 0, 0, // u64 extension_length
		1, 0, // u16 section_count
		1, 0, // u16 tag GROUPS
		1,                       // flags REQUIRED
		29, 0, 0, 0, 0, 0, 0, 0, // u64 section_length
		1, 0, // u16 group_count
		0, 4, 0, // kind indexed, index width 4, flags
		2, 0, // member_count
		0, 0, 1, // field 0, key
		1, 0, 0, // field 1
		4, 0, 0, 0, // entry_width
		2, 0, 0, 0, // entry_count
		7, 0x34, 0x12, 0, 9, 0, 0, 0b10,
	}
	if !bytes.Equal(ext, want) {
		t.Fatalf("extension bytes:\n got=%v\nwant=%v", ext, want)
	}
	if s.RecordByteSize() != 4+1 || s.HasBitmap() {
		t.Fatalf("stride %d hasBitmap %v, want 5/false (u32 index + c, no row nullable)", s.RecordByteSize(), s.HasBitmap())
	}
	// Decode a row pointing at entry 1: b is null via the entry.
	rows := []byte{1, 0, 0, 0, 42}
	rr := NewRecordReader(bytes.NewReader(rows), s)
	v, n, w := map[string]float64{}, map[string]bool{}, map[string]any{}
	if err := rr.ReadRecordWithWide(v, n, w); err != nil {
		t.Fatal(err)
	}
	if v["a"] != 9 || !n["b"] || v["c"] != 42 {
		t.Fatalf("decoded %v nulls %v", v, n)
	}
	if e, ok := rr.GroupIndex(0); !ok || e != 1 {
		t.Fatalf("GroupIndex = %d/%v, want 1/true", e, ok)
	}
}

func extensionAt(t *testing.T, s *Schema) (prefix []byte) {
	t.Helper()
	var d bytes.Buffer
	if err := writePreambleVersion(&d, &Schema{Fields: s.Fields}, FormatVersionV2); err != nil {
		t.Fatal(err)
	}
	return d.Bytes()[:d.Len()-10] // drop the empty extension (u64 len + u16 count)
}

func withExtension(prefix []byte, payload []byte) []byte {
	out := append([]byte(nil), prefix...)
	out = append(out, byte(len(payload)), 0, 0, 0, 0, 0, 0, 0)
	return append(out, payload...)
}

// TestGroupDescriptor_ForwardCompat: an unknown REQUIRED section is
// refused; an unknown optional one is skipped; reserved bits and
// malformed groups are refused.
func TestGroupDescriptor_ForwardCompat(t *testing.T) {
	s := &Schema{Fields: []Field{{Name: "a", Type: FieldTypeU8}, {Name: "b", Type: FieldTypeU8}}}
	prefix := extensionAt(t, s)
	groups := func(g ...byte) []byte {
		sec := append([]byte{1, 0}, g...)
		return append([]byte{1, 0, 1, 0, 1, byte(len(sec)), 0, 0, 0, 0, 0, 0, 0}, sec...)
	}
	good := []byte{0, 4, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 5}
	cases := []struct {
		name    string
		payload []byte
		ok      bool
	}{
		{"good", groups(good...), true},
		{"unknown_optional_section_skipped", []byte{1, 0, 9, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3}, true},
		{"unknown_required_section", []byte{1, 0, 9, 0, 1, 3, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3}, false},
		{"reserved_section_flag", []byte{1, 0, 9, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0}, false},
		{"duplicate_section", append(append([]byte{2, 0}, groups(good...)[2:]...), groups(good...)[2:]...), false},
		{"trailing_bytes", append(groups(good...), 0), false},
		{"unknown_kind", groups(2, 4, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 5), false},
		{"index_width_2", groups(0, 2, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 5), false},
		{"reserved_group_flag", groups(0, 4, 1, 1, 0, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 5), false},
		{"reserved_member_flag", groups(0, 4, 0, 1, 0, 0, 0, 2, 1, 0, 0, 0, 1, 0, 0, 0, 5), false},
		{"member_out_of_range", groups(0, 4, 0, 1, 0, 7, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 5), false},
		{"entry_width_mismatch", groups(0, 4, 0, 1, 0, 0, 0, 0, 2, 0, 0, 0, 1, 0, 0, 0, 5, 5), false},
		{"entries_truncated", groups(0, 4, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 3, 0, 0, 0, 5), false},
		{"constant_two_entries", groups(1, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 5, 6), false},
		{"no_groups", []byte{1, 0, 1, 0, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := withExtension(prefix, c.payload)
			got, _, err := ReadPreamble(bytes.NewReader(data))
			if c.ok {
				if err != nil {
					t.Fatalf("err = %v, want accepted", err)
				}
				if c.name == "good" && (len(got.Groups) != 1 || got.GroupEntryCount(0) != 1) {
					t.Fatalf("groups = %+v", got.Groups)
				}
				return
			}
			if !errors.HasCode(err, errors.ENCODING_INVALID) {
				t.Fatalf("err = %v, want ENCODING_INVALID", err)
			}
		})
	}
}

// TestGroupShape_Rejected: schema-level validation on write.
func TestGroupShape_Rejected(t *testing.T) {
	f := []Field{{Name: "a", Type: FieldTypeU8}, {Name: "b", Type: FieldTypeU8}}
	cases := map[string][]Group{
		"field_in_two_groups":  {{Members: []GroupMember{{Field: 0}}, Entries: []byte{1}}, {Members: []GroupMember{{Field: 0}}, Entries: []byte{1}}},
		"members_not_sorted":   {{Members: []GroupMember{{Field: 1}, {Field: 0}}, Entries: []byte{1, 2}}},
		"no_members":           {{Entries: nil}},
		"partial_entry":        {{Members: []GroupMember{{Field: 0}, {Field: 1}}, Entries: []byte{1}}},
		"zero_stride_constant": {{Kind: GroupKindConstant, Members: []GroupMember{{Field: 0}, {Field: 1}}, Entries: []byte{1, 2}}},
	}
	for name, groups := range cases {
		t.Run(name, func(t *testing.T) {
			err := WritePreamble(io.Discard, &Schema{Fields: f, Groups: groups})
			if !errors.HasCode(err, errors.ENCODING_INVALID) {
				t.Fatalf("err = %v, want ENCODING_INVALID", err)
			}
		})
	}
}

// TestGroupedDecode_IndexOutOfRange: a row pointing past its group's
// dictionary is ENCODING_INVALID on every decode path, never a panic or
// a silent zero.
func TestGroupedDecode_IndexOutOfRange(t *testing.T) {
	s := &Schema{
		Fields: []Field{{Name: "a", Type: FieldTypeU8}, {Name: "c", Type: FieldTypeU8}},
		Groups: []Group{{Members: []GroupMember{{Field: 0}}, Entries: []byte{7}}},
	}
	row := []byte{1, 0, 0, 0, 3}
	rr := NewRecordReader(bytes.NewReader(row), s)
	err := rr.ReadRecordWithWide(map[string]float64{}, map[string]bool{}, map[string]any{})
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("map path err = %v", err)
	}
	rr = NewRecordReader(bytes.NewReader(row), s)
	err = rr.ReadRecordReused(&dualRecord{indexedTestRecord: newIndexedTestRecord(s)})
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("reuse path err = %v", err)
	}
}

// TestGroupEncoder_Violations: a keyed group whose non-key member
// disagrees, a constant group that changes, and index-space exhaustion
// are coded errors naming the field and row.
func TestGroupEncoder_Violations(t *testing.T) {
	flat := &Schema{Fields: []Field{{Name: "k", Type: FieldTypeU8}, {Name: "p", Type: FieldTypeU8}, {Name: "c", Type: FieldTypeU8}}}
	rows := [][]byte{{1, 10, 0}, {2, 20, 0}, {1, 11, 0}}

	enc, err := NewGroupEncoder(flat, []GroupSpec{{Members: []string{"k", "p"}, Key: []string{"k"}}})
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for i, r := range rows {
		out, err = enc.EncodeRow(out, r)
		if i < 2 && err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID || ce.Details["field"] != "p" || ce.Details["row"] != int64(2) {
		t.Fatalf("key violation err = %v (details %v), want field p row 2", err, ce)
	}

	enc, _ = NewGroupEncoder(flat, []GroupSpec{{Kind: GroupKindConstant, Members: []string{"c", "p"}}})
	out = nil
	for _, r := range [][]byte{{1, 10, 0}, {2, 10, 0}, {3, 10, 5}} {
		out, err = enc.EncodeRow(out, r)
	}
	if !stderrors.As(err, &ce) || ce.Details["field"] != "c" || ce.Details["row"] != int64(2) {
		t.Fatalf("constant violation err = %v, want field c row 2", err)
	}

	old := MaxGroupEntries
	MaxGroupEntries = 2
	t.Cleanup(func() { MaxGroupEntries = old })
	enc, _ = NewGroupEncoder(flat, []GroupSpec{{Members: []string{"k"}}})
	out = nil
	for _, r := range [][]byte{{1, 0, 0}, {2, 0, 0}, {1, 0, 0}, {3, 0, 0}} {
		if out, err = enc.EncodeRow(out, r); err != nil {
			break
		}
	}
	if !stderrors.As(err, &ce) || ce.Details["max_entries"] != uint64(2) {
		t.Fatalf("overflow err = %v, want max_entries 2", err)
	}

	for name, specs := range map[string][]GroupSpec{
		"unknown_field":  {{Members: []string{"zz"}}},
		"two_groups":     {{Members: []string{"k"}}, {Members: []string{"k", "p"}}},
		"key_not_member": {{Members: []string{"k"}, Key: []string{"p"}}},
	} {
		if _, err := NewGroupEncoder(flat, specs); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

// TestGroupEntryAccessors: the per-entry surface a precompute pass uses
// — DecodeGroupEntry writes exactly the members' values/nulls, and the
// reader reports the row's entry index.
func TestGroupEntryAccessors(t *testing.T) {
	fx := groupFixtures(t)[0]
	flat := flatCohort(t, fx.schema, fx.rows)
	grouped, gs := groupedTwin(t, flat, fx.specs)
	_, phys := recordRegion(t, grouped)
	rr := NewRecordReader(bytes.NewReader(phys), gs)
	for k := 0; k < 40; k++ {
		row := &dualRecord{indexedTestRecord: newIndexedTestRecord(gs)}
		if err := rr.ReadRecordReused(row); err != nil {
			t.Fatal(err)
		}
		for g := range gs.Groups {
			e, ok := rr.GroupIndex(g)
			if !ok {
				t.Fatalf("row %d group %d: no index", k, g)
			}
			ent := &dualRecord{indexedTestRecord: newIndexedTestRecord(gs)}
			if err := gs.DecodeGroupEntry(g, int(e), ent); err != nil {
				t.Fatal(err)
			}
			for _, m := range gs.Groups[g].Members {
				name := gs.Fields[m.Field].Name
				if row.values[name] != ent.values[name] || row.nulls[name] != ent.nulls[name] ||
					!reflect.DeepEqual(row.wide[name], ent.wide[name]) {
					t.Fatalf("row %d member %s: row=%v/%v entry=%v/%v", k, name, row.values[name], row.nulls[name], ent.values[name], ent.nulls[name])
				}
			}
			if len(ent.values) != len(gs.Groups[g].Members) {
				t.Fatalf("DecodeGroupEntry wrote %d fields, want %d members", len(ent.values), len(gs.Groups[g].Members))
			}
		}
		if g, k2, ok := gs.FieldGroup(fieldIdx(t, gs, "amount")[0]); !ok || g != 0 || gs.Groups[g].Members[k2].Field != fieldIdx(t, gs, "amount")[0] {
			t.Fatalf("FieldGroup(amount) = %d/%d/%v", g, k2, ok)
		}
	}
	if _, _, ok := gs.FieldGroup(fieldIdx(t, gs, "u8_b")[0]); ok {
		t.Fatal("u8_b is a row field, FieldGroup said member")
	}
}

// TestRefuseGroups_ByteLevelRewriters: paths that rewrite physical row
// bytes by logical offsets refuse a grouped cohort loudly.
func TestRefuseGroups_ByteLevelRewriters(t *testing.T) {
	fx := groupFixtures(t)[0]
	grouped, gs := groupedTwin(t, flatCohort(t, fx.schema, fx.rows), fx.specs)
	if _, _, err := WidenSetFieldBytes(grouped, "tags_u8", FieldTypeSetU16); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("widen err = %v", err)
	}
	if _, err := WidenSchemaSetField(gs, "tags_u8", FieldTypeSetU16); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("schema widen err = %v", err)
	}
	if _, err := RewriteShardCategoricals(grouped, gs, nil); !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
		t.Fatalf("shard rewrite err = %v", err)
	}
	var doc bytes.Buffer
	if err := WritePreamble(&doc, gs); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSchemaDoc(bytes.NewReader(doc.Bytes())); !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
		t.Fatalf("schema doc err = %v", err)
	}
}

// BenchmarkGroupedDecode compares full reuse decode of a 0x01 cohort
// with its grouped 0x02 twin (direct physical-row decode), plus the
// logical-stream expansion alone.
func BenchmarkGroupedDecode(b *testing.B) {
	t := &testing.T{}
	fx := groupFixtures(t)[0]
	rows := fx.rows
	for len(rows) < 20000 {
		rows = append(rows, fx.rows...)
	}
	// Re-derive parent structure over the longer run so dedup holds.
	flat := flatCohort(t, fx.schema, rows)
	grouped, gs := groupedTwin(t, flat, fx.specs)
	fs, flatRegion := recordRegion(t, flat)
	_, phys := recordRegion(t, grouped)
	b.Logf("flat stride %d, grouped stride %d, dict bytes %d/%d/%d (entry widths %d/%d/%d)",
		fs.RecordByteSize(), gs.RecordByteSize(),
		len(gs.Groups[0].Entries), len(gs.Groups[1].Entries), len(gs.Groups[2].Entries),
		gs.GroupEntryWidth(0), gs.GroupEntryWidth(1), gs.GroupEntryWidth(2))
	run := func(b *testing.B, s *Schema, region []byte, runSkip bool) {
		b.SetBytes(int64(len(region)))
		for i := 0; i < b.N; i++ {
			rr := NewRecordReader(bytes.NewReader(region), s)
			var rec ReusableRecord = &dualRecord{indexedTestRecord: newIndexedTestRecord(s)}
			if runSkip {
				rec = newRunSkipTestRecord(s)
			}
			for {
				if err := rr.ReadRecordReused(rec); err != nil {
					if err == io.EOF {
						break
					}
					b.Fatal(err)
				}
			}
		}
	}
	b.Run("v2_expand_stream", func(b *testing.B) {
		b.SetBytes(int64(len(flatRegion)))
		for i := 0; i < b.N; i++ {
			lr, _, err := NewLogicalStream(bytes.NewReader(phys), gs)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, lr); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("v1", func(b *testing.B) { run(b, fs, flatRegion, false) })
	b.Run("v2", func(b *testing.B) { run(b, gs, phys, false) })
	b.Run("v1_runskip", func(b *testing.B) { run(b, fs, flatRegion, true) })
	b.Run("v2_runskip", func(b *testing.B) { run(b, gs, phys, true) })
}
