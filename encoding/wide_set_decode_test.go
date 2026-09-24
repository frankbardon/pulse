package encoding

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// wideSetDecodeSchema is the fixture schema for the wide-rung decode
// tests: a wide set sandwiched between a bit-packed run, a categorical
// and plain scalars, with nullable fields on both sides of it. Anything
// that gets the 16- or 32-byte stride wrong shifts every field after the
// set, and `tail` is the canary that catches it.
func wideSetDecodeSchema() *Schema {
	return &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32},
		{Name: "nib", Type: FieldTypeU4, BitPosition: 0},
		{Name: "flag", Type: FieldTypePackedBool, BitPosition: 0},
		{Name: "cat", Type: FieldTypeCategoricalU16, Dictionary: buildCategoricalDict(300)},
		{Name: "tags128", Type: FieldTypeSetU128, Dictionary: buildSetDict(128), Nullable: true},
		{Name: "score", Type: FieldTypeF64, Nullable: true},
		{Name: "tags256", Type: FieldTypeSetU256, Dictionary: buildSetDict(256)},
		{Name: "tail", Type: FieldTypeU64},
	}}
}

// generateWideSetRecords synthesises n records for wideSetDecodeSchema
// with deterministic edge-case coverage on both wide rungs and a
// rotating null pattern that includes the nullable wide set. The logical
// values are returned alongside the encoded bytes so a test can assert
// against the GENERATOR rather than against another run of the decoder
// under test — a serial-vs-parallel comparison alone cannot catch a
// decoder bug both arms share.
func generateWideSetRecords(t *testing.T, schema *Schema, n int, seed int64) ([][]byte, []map[string]any) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	out := make([][]byte, 0, n)
	truth := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		vals := map[string]any{
			"id":      uint64(r.Uint32()),
			"nib":     uint8(r.Intn(16)),
			"flag":    r.Intn(2) == 1,
			"cat":     uint64(r.Intn(300)),
			"tags128": setMaskWide(r, i, 128),
			// Float BITS from a real float64 — a raw r.Uint64() would
			// occasionally be a NaN pattern, and NaN != NaN defeats the
			// map comparisons these tests rest on.
			"score":   math.Float64bits(r.NormFloat64() * 100),
			"tags256": setMaskWide(r, i+3, 256),
			"tail":    r.Uint64(),
		}
		var nulls map[string]bool
		switch i % 4 {
		case 1:
			// The nullable WIDE set is null: its 16 payload bytes are
			// still on the wire and must still be consumed, but the null
			// is signalled by the bitmap alone.
			nulls = map[string]bool{"tags128": true}
		case 2:
			nulls = map[string]bool{"score": true}
		case 3:
			nulls = map[string]bool{"tags128": true, "score": true}
		}
		out = append(out, encodeRecord(t, schema, vals, nulls))
		truth = append(truth, vals)
	}
	return out, truth
}

// assertMatchesTruth pins a decoded row against the values the generator
// wrote, for the fields whose on-wire position depends on the wide sets'
// strides. Without this the parallel tests would only compare one decoder
// run to another and would pass just as happily with a wrong width.
func assertMatchesTruth(t *testing.T, label string, got decodedRow, want map[string]any) {
	t.Helper()
	for _, name := range []string{"tags128", "tags256"} {
		if got.nulls[name] {
			continue
		}
		wantMask := want[name].(SetMask)
		gotMask, ok := got.wide[name].(SetMask)
		if !ok {
			t.Fatalf("%s: %s wide value is %T, want SetMask", label, name, got.wide[name])
		}
		if !gotMask.Equal(wantMask) {
			t.Fatalf("%s: %s decoded %v, generator wrote %v", label, name, gotMask.Words(), wantMask.Words())
		}
	}
	// `tail` is the canary: it sits after both wide sets, so a wrong
	// stride on either lands the decoder on the wrong bytes.
	if gotTail, wantTail := got.values["tail"], float64(want["tail"].(uint64)); gotTail != wantTail {
		t.Fatalf("%s: tail decoded %v, generator wrote %v", label, gotTail, wantTail)
	}
	if gotID, wantID := got.values["id"], float64(want["id"].(uint64)); gotID != wantID {
		t.Fatalf("%s: id decoded %v, generator wrote %v", label, gotID, wantID)
	}
}

// wideSetPayload assembles a complete single-file .pulse payload
// (header + schema + records) and reports where the record region
// starts, which is what RecordLocator and the segment-parallel decoder
// both need.
func wideSetPayload(t *testing.T, schema *Schema, records [][]byte) ([]byte, int) {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	prefix := buf.Len()
	for _, rec := range records {
		buf.Write(rec)
	}
	return buf.Bytes(), prefix
}

type decodedRow struct {
	values map[string]float64
	nulls  map[string]bool
	wide   map[string]any
}

// decodeOneFull decodes a single record with the reference map reader.
func decodeOneFull(t *testing.T, schema *Schema, raw []byte) decodedRow {
	t.Helper()
	rr := NewRecordReader(bytes.NewReader(raw), schema)
	row := decodedRow{
		values: map[string]float64{},
		nulls:  map[string]bool{},
		wide:   map[string]any{},
	}
	if err := rr.ReadRecordWithWide(row.values, row.nulls, row.wide); err != nil {
		t.Fatalf("ReadRecordWithWide: %v", err)
	}
	return row
}

// TestWideSetDecode_FourSurfacesAgree runs the same record through all
// four decode surfaces — the per-field map reader, the plan-driven map
// reader, the buffer-once reuse reader, and the plan-driven reuse
// reader — and asserts identical values / nulls / wide maps. Three of
// the four reached the wide rungs through code paths that previously
// raised ENCODING_TYPE_MISMATCH or ENCODING_INVALID "unknown field type
// 18"; nothing but an explicit cross-check keeps them in step as each
// gains its own arm.
func TestWideSetDecode_FourSurfacesAgree(t *testing.T) {
	schema := wideSetDecodeSchema()
	records, _ := generateWideSetRecords(t, schema, 200, 0x51DE5E70)
	all := allFieldNames(schema)
	keep := keepFromNames(all)
	plan, err := schema.BuildDecodePlan(all)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}

	for ri, raw := range records {
		want := decodeOneFull(t, schema, raw)

		// Surface 2: plan-driven map reader.
		rr2 := NewRecordReader(bytes.NewReader(raw), schema)
		got2 := decodedRow{values: map[string]float64{}, nulls: map[string]bool{}, wide: map[string]any{}}
		if err := rr2.ReadRecordWithWidePlan(got2.values, got2.nulls, got2.wide, keep, plan); err != nil {
			t.Fatalf("rec=%d ReadRecordWithWidePlan: %v", ri, err)
		}

		// Surface 3: buffer-once reuse reader.
		rr3 := NewRecordReader(bytes.NewReader(raw), schema)
		rec3 := newReusableTestRecord()
		if err := rr3.ReadRecordReused(rec3); err != nil {
			t.Fatalf("rec=%d ReadRecordReused: %v", ri, err)
		}

		// Surface 4: plan-driven reuse reader.
		rr4 := NewRecordReader(bytes.NewReader(raw), schema)
		rec4 := newReusableTestRecord()
		if err := rr4.ReadRecordReusedWithPlan(rec4, keep, plan); err != nil {
			t.Fatalf("rec=%d ReadRecordReusedWithPlan: %v", ri, err)
		}

		surfaces := []struct {
			name string
			row  decodedRow
		}{
			{"plan_map", got2},
			{"reused", decodedRow{rec3.values, rec3.nulls, rec3.wide}},
			{"reused_plan", decodedRow{rec4.values, rec4.nulls, rec4.wide}},
		}
		for _, s := range surfaces {
			if !reflect.DeepEqual(want.values, s.row.values) {
				t.Fatalf("rec=%d surface=%s values mismatch:\n  ref=%v\n  got=%v", ri, s.name, want.values, s.row.values)
			}
			if !reflect.DeepEqual(want.nulls, s.row.nulls) {
				t.Fatalf("rec=%d surface=%s nulls mismatch:\n  ref=%v\n  got=%v", ri, s.name, want.nulls, s.row.nulls)
			}
			if !reflect.DeepEqual(want.wide, s.row.wide) {
				t.Fatalf("rec=%d surface=%s wide mismatch:\n  ref=%v\n  got=%v", ri, s.name, want.wide, s.row.wide)
			}
		}
	}
}

// TestWideSetDecode_WideValueIsSetMask pins the in-memory hand-off type.
// A wide rung must reach the wide map as an encoding.SetMask, NOT the
// uint64 the narrow rungs use — a uint64 here would carry only the low
// 64 bits of a 128- or 256-bit selection and every consumer would read a
// plausible, wrong answer.
func TestWideSetDecode_WideValueIsSetMask(t *testing.T) {
	schema := &Schema{Fields: []Field{
		{Name: "narrow", Type: FieldTypeSetU64, Dictionary: buildSetDict(64)},
		{Name: "wide", Type: FieldTypeSetU128, Dictionary: buildSetDict(128)},
	}}
	want := SetMask{}.WithBit(3).WithBit(127)
	raw := encodeRecord(t, schema, map[string]any{
		"narrow": uint64(0b1010),
		"wide":   want,
	}, nil)

	row := decodeOneFull(t, schema, raw)
	if _, ok := row.wide["narrow"].(uint64); !ok {
		t.Errorf("narrow rung wide value is %T, want uint64", row.wide["narrow"])
	}
	got, ok := row.wide["wide"].(SetMask)
	if !ok {
		t.Fatalf("wide rung wide value is %T, want encoding.SetMask", row.wide["wide"])
	}
	if !got.Equal(want) {
		t.Fatalf("wide mask: got words %v, want words %v", got.Words(), want.Words())
	}
	if got.PopCount() != 2 || !got.Has(127) {
		t.Fatalf("bit 127 did not survive the decode: words=%v", got.Words())
	}
}

// TestWideSetDecode_LowWordIsByteIdenticalToSetU64 pins the normative
// word order from the decode side. A set_u128 whose selections all sit
// below bit 64 must lay its low 8 bytes down byte-for-byte as a set_u64
// holding the same selections, and both must echo the same float64 into
// the values map. Reverse the word order or the intra-word byte order
// and this is the assertion that fails rather than a cohort silently
// decoding as a different, plausible selection.
func TestWideSetDecode_LowWordIsByteIdenticalToSetU64(t *testing.T) {
	const low = uint64(0x0123456789ABCDEF)

	narrowSchema := &Schema{Fields: []Field{
		{Name: "tags", Type: FieldTypeSetU64, Dictionary: buildSetDict(64)},
	}}
	wideSchema := &Schema{Fields: []Field{
		{Name: "tags", Type: FieldTypeSetU128, Dictionary: buildSetDict(128)},
	}}

	narrowRaw := encodeRecord(t, narrowSchema, map[string]any{"tags": low}, nil)
	wideRaw := encodeRecord(t, wideSchema, map[string]any{"tags": SetMaskFromUint64(low)}, nil)

	if len(narrowRaw) != 8 || len(wideRaw) != 16 {
		t.Fatalf("unexpected strides: narrow=%d wide=%d", len(narrowRaw), len(wideRaw))
	}
	if !bytes.Equal(narrowRaw, wideRaw[:8]) {
		t.Fatalf("low 8 bytes diverge:\n  set_u64 =%x\n  set_u128=%x", narrowRaw, wideRaw[:8])
	}
	for i, b := range wideRaw[8:] {
		if b != 0 {
			t.Fatalf("high word byte %d is 0x%02x, want 0 for a low-only selection", i, b)
		}
	}

	narrowRow := decodeOneFull(t, narrowSchema, narrowRaw)
	wideRow := decodeOneFull(t, wideSchema, wideRaw)
	if narrowRow.values["tags"] != wideRow.values["tags"] {
		t.Fatalf("float echo diverges: set_u64=%v set_u128=%v",
			narrowRow.values["tags"], wideRow.values["tags"])
	}
	gotWide, ok := wideRow.wide["tags"].(SetMask)
	if !ok {
		t.Fatalf("wide value is %T, want SetMask", wideRow.wide["tags"])
	}
	gotLow, fits := gotWide.Uint64()
	if !fits || gotLow != low {
		t.Fatalf("low word: got 0x%016x (fits=%v), want 0x%016x", gotLow, fits, low)
	}
	if narrowRow.wide["tags"].(uint64) != gotLow {
		t.Fatalf("narrow mask 0x%016x != wide low word 0x%016x", narrowRow.wide["tags"], gotLow)
	}
}

// TestWideSetDecode_ProjectionIsOutputTransparent asserts the default-on
// projected decode contract over the wide rungs on both arms:
//
//   - a projection that RETAINS a wide set yields exactly the full
//     decode's entry for it (same SetMask, same float echo, same null
//     flag);
//   - a projection that DROPS it does not mis-stride anything after it —
//     every retained neighbour still matches the full decode.
//
// Divergence here is a bug, not a tradeoff: projection is output
// transparent by contract.
func TestWideSetDecode_ProjectionIsOutputTransparent(t *testing.T) {
	schema := wideSetDecodeSchema()
	records, _ := generateWideSetRecords(t, schema, 120, 0x9401EC7)
	all := allFieldNames(schema)

	subsets := []struct {
		name     string
		retained []string
	}{
		{"wide_only_128", []string{"tags128"}},
		{"wide_only_256", []string{"tags256"}},
		{"both_wide", []string{"tags128", "tags256"}},
		{"drop_both_wide", []string{"id", "nib", "flag", "cat", "score", "tail"}},
		{"drop_128_keep_256", []string{"id", "tags256", "tail"}},
		{"drop_256_keep_128", []string{"id", "tags128", "tail"}},
		{"tail_only", []string{"tail"}},
		{"full", all},
	}

	for _, s := range subsets {
		s := s
		t.Run(s.name, func(t *testing.T) {
			keep := keepFromNames(s.retained)
			plan, err := schema.BuildDecodePlan(s.retained)
			if err != nil {
				t.Fatalf("BuildDecodePlan: %v", err)
			}
			retainedSet := map[string]bool{}
			for _, n := range s.retained {
				retainedSet[n] = true
			}

			for ri, raw := range records {
				full := decodeOneFull(t, schema, raw)

				// Both projected readers: map-based and reuse-based.
				rrMap := NewRecordReader(bytes.NewReader(raw), schema)
				proj := decodedRow{values: map[string]float64{}, nulls: map[string]bool{}, wide: map[string]any{}}
				if err := rrMap.ReadRecordWithWidePlan(proj.values, proj.nulls, proj.wide, keep, plan); err != nil {
					t.Fatalf("rec=%d projected map decode: %v", ri, err)
				}
				rrReuse := NewRecordReader(bytes.NewReader(raw), schema)
				rec := newReusableTestRecord()
				if err := rrReuse.ReadRecordReusedWithPlan(rec, keep, plan); err != nil {
					t.Fatalf("rec=%d projected reuse decode: %v", ri, err)
				}

				for _, name := range all {
					if !retainedSet[name] {
						if _, ok := proj.values[name]; ok {
							t.Fatalf("rec=%d: dropped field %q leaked into the projected values map", ri, name)
						}
						continue
					}
					if proj.values[name] != full.values[name] {
						t.Fatalf("rec=%d field=%q projected value %v != full %v",
							ri, name, proj.values[name], full.values[name])
					}
					if proj.nulls[name] != full.nulls[name] {
						t.Fatalf("rec=%d field=%q projected null %v != full %v",
							ri, name, proj.nulls[name], full.nulls[name])
					}
					if !reflect.DeepEqual(proj.wide[name], full.wide[name]) {
						t.Fatalf("rec=%d field=%q projected wide %v != full %v",
							ri, name, proj.wide[name], full.wide[name])
					}
					if rec.values[name] != full.values[name] {
						t.Fatalf("rec=%d field=%q reuse-projected value %v != full %v",
							ri, name, rec.values[name], full.values[name])
					}
					if !reflect.DeepEqual(rec.wide[name], full.wide[name]) {
						t.Fatalf("rec=%d field=%q reuse-projected wide %v != full %v",
							ri, name, rec.wide[name], full.wide[name])
					}
				}
			}
		})
	}
}

// TestWideSetDecode_RecordAtRandomAccess asserts RecordLocator's offset
// math stays correct across a schema whose stride includes a 16- and a
// 32-byte set, and that a wide set read at an arbitrary index matches
// the sequential decode of that same index. The locator derives stride
// from Schema.RecordByteSize, so a wrong wide-set ByteSize would put
// every record past the first at the wrong offset.
func TestWideSetDecode_RecordAtRandomAccess(t *testing.T) {
	schema := wideSetDecodeSchema()
	records, _ := generateWideSetRecords(t, schema, 64, 0xA7A7A7)
	payload, _ := wideSetPayload(t, schema, records)

	loc, err := NewRecordLocator(bytes.NewReader(payload), schema)
	if err != nil {
		t.Fatalf("NewRecordLocator: %v", err)
	}
	if loc.TotalRecords != uint64(len(records)) {
		t.Fatalf("TotalRecords: got %d, want %d", loc.TotalRecords, len(records))
	}
	if want := schema.RecordByteSize(); loc.Stride != int64(want) {
		t.Fatalf("Stride: got %d, want %d", loc.Stride, want)
	}

	all := allFieldNames(schema)
	plan, err := schema.BuildDecodePlan(all)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}

	// Deliberately out of order, so a locator that only worked when read
	// sequentially would fail.
	order := []uint64{63, 0, 17, 62, 1, 40, 5, 63, 33}
	for _, i := range order {
		want := decodeOneFull(t, schema, records[i])

		got := decodedRow{values: map[string]float64{}, nulls: map[string]bool{}, wide: map[string]any{}}
		if err := loc.ReadRecordAt(bytes.NewReader(payload), i, got.values, got.nulls, got.wide, nil, nil); err != nil {
			t.Fatalf("ReadRecordAt(%d): %v", i, err)
		}
		if !reflect.DeepEqual(want.values, got.values) || !reflect.DeepEqual(want.wide, got.wide) || !reflect.DeepEqual(want.nulls, got.nulls) {
			t.Fatalf("ReadRecordAt(%d) mismatch:\n  want=%v/%v/%v\n  got =%v/%v/%v",
				i, want.values, want.nulls, want.wide, got.values, got.nulls, got.wide)
		}

		// Same index through the plan-driven (projected) arm.
		planned := decodedRow{values: map[string]float64{}, nulls: map[string]bool{}, wide: map[string]any{}}
		if err := loc.ReadRecordAt(bytes.NewReader(payload), i, planned.values, planned.nulls, planned.wide, keepFromNames(all), plan); err != nil {
			t.Fatalf("ReadRecordAt(%d) with plan: %v", i, err)
		}
		if !reflect.DeepEqual(want.wide, planned.wide) {
			t.Fatalf("ReadRecordAt(%d) plan wide mismatch:\n  want=%v\n  got =%v", i, want.wide, planned.wide)
		}
	}
}

// decodeRegionSegment decodes records [from, to) of the record region
// with its own RecordReader, exactly as one parallel-decode worker does.
func decodeRegionSegment(t *testing.T, schema *Schema, region []byte, stride, from, to int) []decodedRow {
	t.Helper()
	rr := NewRecordReader(bytes.NewReader(region[from*stride:to*stride]), schema)
	out := make([]decodedRow, 0, to-from)
	for i := from; i < to; i++ {
		rec := newReusableTestRecord()
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatalf("segment [%d,%d) record %d: %v", from, to, i, err)
		}
		row := decodedRow{values: map[string]float64{}, nulls: map[string]bool{}, wide: map[string]any{}}
		for k, v := range rec.values {
			row.values[k] = v
		}
		for k, v := range rec.nulls {
			row.nulls[k] = v
		}
		for k, v := range rec.wide {
			row.wide[k] = v
		}
		out = append(out, row)
	}
	return out
}

// TestWideSetDecode_ParallelSegmentsMatchSerial mirrors the geometry the
// buffered parallel decoder (Options.DecodeWorkers) uses: the record
// region is split into contiguous segments at record-stride multiples
// and one goroutine decodes each. A wide set must not shift a segment
// boundary — every worker's rows must equal the serial scan's rows at
// the same indices. A wrong wide-set stride would put worker 2 onto a
// mid-record byte and decode garbage that still parses.
func TestWideSetDecode_ParallelSegmentsMatchSerial(t *testing.T) {
	schema := wideSetDecodeSchema()
	records, truth := generateWideSetRecords(t, schema, 512, 0xDECA1F)
	payload, prefix := wideSetPayload(t, schema, records)
	region := payload[prefix:]
	stride := schema.RecordByteSize()

	if len(region)%stride != 0 {
		t.Fatalf("record region %d bytes is not a multiple of stride %d", len(region), stride)
	}

	// Serial reference: one reader over the whole region, itself pinned
	// against the generator so a shared decoder bug cannot hide behind a
	// serial-vs-parallel comparison.
	serial := decodeRegionSegment(t, schema, region, stride, 0, len(records))
	for i := range serial {
		assertMatchesTruth(t, fmt.Sprintf("serial row %d", i), serial[i], truth[i])
	}

	for _, workers := range []int{1, 2, 3, 7, 8} {
		workers := workers
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			per := (len(records) + workers - 1) / workers
			results := make([][]decodedRow, workers)
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				from := w * per
				to := from + per
				if from > len(records) {
					from = len(records)
				}
				if to > len(records) {
					to = len(records)
				}
				wg.Add(1)
				go func(w, from, to int) {
					defer wg.Done()
					results[w] = decodeRegionSegment(t, schema, region, stride, from, to)
				}(w, from, to)
			}
			wg.Wait()

			var merged []decodedRow
			for _, r := range results {
				merged = append(merged, r...)
			}
			if len(merged) != len(serial) {
				t.Fatalf("parallel produced %d rows, serial %d", len(merged), len(serial))
			}
			for i := range serial {
				assertMatchesTruth(t, fmt.Sprintf("parallel row %d", i), merged[i], truth[i])
				if !reflect.DeepEqual(serial[i].values, merged[i].values) {
					t.Fatalf("row %d values diverge:\n  serial  =%v\n  parallel=%v", i, serial[i].values, merged[i].values)
				}
				if !reflect.DeepEqual(serial[i].wide, merged[i].wide) {
					t.Fatalf("row %d wide diverges:\n  serial  =%v\n  parallel=%v", i, serial[i].wide, merged[i].wide)
				}
				if !reflect.DeepEqual(serial[i].nulls, merged[i].nulls) {
					t.Fatalf("row %d nulls diverge:\n  serial  =%v\n  parallel=%v", i, serial[i].nulls, merged[i].nulls)
				}
			}
		})
	}
}

// TestWideSetDecode_ParallelShardsMatchSerial mirrors the per-shard
// worker pool (Options.ShardWorkers): N standalone shard payloads over
// one canonical schema, each decoded start-to-finish by its own
// goroutine. The assertion is that a shard decoded concurrently produces
// exactly the rows it produces when decoded serially — a wide set is a
// value type (SetMask never allocates and no method aliases the
// receiver's array), so nothing here may be shared across workers.
func TestWideSetDecode_ParallelShardsMatchSerial(t *testing.T) {
	schema := wideSetDecodeSchema()
	const shardCount = 6
	shards := make([][]byte, shardCount)
	recordsPerShard := make([][][]byte, shardCount)
	truthPerShard := make([][]map[string]any, shardCount)
	for s := 0; s < shardCount; s++ {
		recs, shardTruth := generateWideSetRecords(t, schema, 80, int64(0x5AD00+s))
		truthPerShard[s] = shardTruth
		recordsPerShard[s] = recs
		payload, _ := wideSetPayload(t, schema, recs)
		shards[s] = payload
	}

	decodeShard := func(payload []byte) []decodedRow {
		r := bytes.NewReader(payload)
		if err := ReadHeader(r); err != nil {
			t.Errorf("ReadHeader: %v", err)
			return nil
		}
		shardSchema, err := ReadSchema(r)
		if err != nil {
			t.Errorf("ReadSchema: %v", err)
			return nil
		}
		region := payload[len(payload)-r.Len():]
		stride := shardSchema.RecordByteSize()
		return decodeRegionSegment(t, shardSchema, region, stride, 0, len(region)/stride)
	}

	serial := make([][]decodedRow, shardCount)
	for s := range shards {
		serial[s] = decodeShard(shards[s])
	}

	parallel := make([][]decodedRow, shardCount)
	var wg sync.WaitGroup
	for s := range shards {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			parallel[s] = decodeShard(shards[s])
		}(s)
	}
	wg.Wait()

	for s := range shards {
		if len(serial[s]) != len(recordsPerShard[s]) {
			t.Fatalf("shard %d serial decoded %d rows, wrote %d", s, len(serial[s]), len(recordsPerShard[s]))
		}
		if len(parallel[s]) != len(serial[s]) {
			t.Fatalf("shard %d parallel decoded %d rows, serial %d", s, len(parallel[s]), len(serial[s]))
		}
		for i := range serial[s] {
			assertMatchesTruth(t, fmt.Sprintf("shard %d serial row %d", s, i), serial[s][i], truthPerShard[s][i])
			assertMatchesTruth(t, fmt.Sprintf("shard %d parallel row %d", s, i), parallel[s][i], truthPerShard[s][i])
			if !reflect.DeepEqual(serial[s][i].values, parallel[s][i].values) {
				t.Fatalf("shard %d row %d values diverge:\n  serial  =%v\n  parallel=%v",
					s, i, serial[s][i].values, parallel[s][i].values)
			}
			if !reflect.DeepEqual(serial[s][i].wide, parallel[s][i].wide) {
				t.Fatalf("shard %d row %d wide diverges:\n  serial  =%v\n  parallel=%v",
					s, i, serial[s][i].wide, parallel[s][i].wide)
			}
			if !reflect.DeepEqual(serial[s][i].nulls, parallel[s][i].nulls) {
				t.Fatalf("shard %d row %d nulls diverge:\n  serial  =%v\n  parallel=%v",
					s, i, serial[s][i].nulls, parallel[s][i].nulls)
			}
		}
	}
}

// TestWideSetDecode_NullRidesBitmapAlone asserts the decode side of the
// no-in-band-sentinel rule for a nullable wide set: the 16 payload bytes
// are still present and still consumed on a null row (the next field
// must not shift), the null surfaces from the bitmap alone, and an
// all-zero mask on a NON-null row stays a valid empty selection rather
// than being reported as null.
func TestWideSetDecode_NullRidesBitmapAlone(t *testing.T) {
	schema := &Schema{Fields: []Field{
		{Name: "tags", Type: FieldTypeSetU128, Dictionary: buildSetDict(128), Nullable: true},
		{Name: "tail", Type: FieldTypeU64},
	}}
	const tail = uint64(0xFEEDFACECAFEBEEF)

	// Row A: null, but with non-zero mask bytes on the wire — proving the
	// null verdict comes from the bitmap and not from the payload.
	nullRaw := encodeRecord(t, schema, map[string]any{
		"tags": SetMask{}.WithBit(5).WithBit(120),
		"tail": tail,
	}, map[string]bool{"tags": true})
	// Row B: not null, all-zero mask — a valid "no selection".
	emptyRaw := encodeRecord(t, schema, map[string]any{
		"tags": SetMask{},
		"tail": tail,
	}, nil)

	if len(nullRaw) != 16+8+1 {
		t.Fatalf("stride: got %d, want %d (16 set + 8 u64 + 1 bitmap)", len(nullRaw), 25)
	}

	nullRow := decodeOneFull(t, schema, nullRaw)
	if !nullRow.nulls["tags"] {
		t.Error("null row: tags not marked null")
	}
	if _, ok := nullRow.wide["tags"]; ok {
		t.Errorf("null row: tags carries a wide value %v; a null field must carry none", nullRow.wide["tags"])
	}
	if nullRow.values["tail"] != float64(tail) {
		t.Errorf("null row: tail = %v, want %v — the null row's set payload was not consumed",
			nullRow.values["tail"], float64(tail))
	}

	emptyRow := decodeOneFull(t, schema, emptyRaw)
	if emptyRow.nulls["tags"] {
		t.Error("empty-mask row: an all-zero mask was reported as null")
	}
	m, ok := emptyRow.wide["tags"].(SetMask)
	if !ok {
		t.Fatalf("empty-mask row: wide value is %T, want SetMask", emptyRow.wide["tags"])
	}
	if !m.IsEmpty() {
		t.Errorf("empty-mask row: mask %v is not empty", m.Words())
	}
	if emptyRow.values["tail"] != float64(tail) {
		t.Errorf("empty-mask row: tail = %v, want %v", emptyRow.values["tail"], float64(tail))
	}
}

// TestWideSetDecode_PlanStrideAccounting asserts the decode plan sizes a
// dropped wide set correctly. BuildDecodePlan coalesces unprojected
// fields into SkipBytes by ByteSize(), so a wrong width here would skip
// the wrong number of bytes and mis-stride everything after it — the
// segment totals are asserted directly so the failure names the plan
// rather than a downstream value.
func TestWideSetDecode_PlanStrideAccounting(t *testing.T) {
	schema := &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32},
		{Name: "tags128", Type: FieldTypeSetU128, Dictionary: buildSetDict(128)},
		{Name: "tags256", Type: FieldTypeSetU256, Dictionary: buildSetDict(256)},
		{Name: "tail", Type: FieldTypeU64},
	}}
	if got, want := schema.RecordByteSize(), 4+16+32+8; got != want {
		t.Fatalf("stride: got %d, want %d", got, want)
	}

	plan, err := schema.BuildDecodePlan([]string{"id", "tail"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	var skipped int
	for _, seg := range plan.Segments {
		if s, ok := seg.(SkipBytes); ok {
			skipped += s.N
		}
	}
	if want := 16 + 32; skipped != want {
		t.Fatalf("plan skips %d bytes for the two dropped wide sets, want %d", skipped, want)
	}
}
