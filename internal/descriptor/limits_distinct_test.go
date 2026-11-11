package descriptor

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// distinctSchema is a schema of every field type the DIST trailer
// tracks plus an untracked u8.
func distinctSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "big", Type: encoding.FieldTypeU32},
		{Name: "huge", Type: encoding.FieldTypeU64},
		{Name: "num", Type: encoding.FieldTypeF64},
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "day", Type: encoding.FieldTypeDate},
		{Name: "small", Type: encoding.FieldTypeU8},
		{Name: "absent", Type: encoding.FieldTypeU32},
		{Name: "over", Type: encoding.FieldTypeU32},
		{Name: "optout", Type: encoding.FieldTypeU32},
		{Name: "retyped", Type: encoding.FieldTypeU32},
	}}
}

// exact is a DistinctExact entry.
func exact(n uint64) encx.DistinctCount {
	return encx.DistinctCount{Status: encx.DistinctExact, Count: n}
}

// twoShardDistinct records, over two shards s0 / s1: big 30 + 20,
// huge 7 + 8, num 40 + 2, ts 11 + 13, day 3 + 4; `absent` is unknown on
// s1, `over` over the counting limit on s0, `optout` opted out on both,
// and `retyped` recorded as a u64 (the schema's field is a u32).
func twoShardDistinct() *encx.ArchiveDistinct {
	f := func(name string, ft encoding.FieldType) encx.DistinctField {
		return encx.DistinctField{Name: name, Type: ft}
	}
	opt := encx.DistinctCount{Status: encx.DistinctOptedOut}
	return &encx.ArchiveDistinct{
		Fields: []encx.DistinctField{
			f("big", encoding.FieldTypeU32), f("huge", encoding.FieldTypeU64), f("num", encoding.FieldTypeF64),
			f("ts", encoding.FieldTypeDateTime), f("day", encoding.FieldTypeDate), f("absent", encoding.FieldTypeU32),
			f("over", encoding.FieldTypeU32), f("optout", encoding.FieldTypeU32), f("retyped", encoding.FieldTypeU64),
		},
		Shards: []encx.ShardDistinct{
			{Name: "s0.pulse", Counts: []encx.DistinctCount{exact(30), exact(7), exact(40), exact(11), exact(3), exact(5),
				{Status: encx.DistinctOverLimit, Count: 1 << 22}, opt, exact(9)}},
			{Name: "s1.pulse", Counts: []encx.DistinctCount{exact(20), exact(8), exact(2), exact(13), exact(4), {},
				exact(6), opt, exact(9)}},
		},
	}
}

// TestEstimateMemory_DistinctBound (U73 #500): with a shard archive's
// recorded distinct counts (LimitInputs.Distinct) a value-keyed grouper
// on a tracked field is charged the SUM of the shards' exact counts —
// below its value width or the records — and a multi-grouper run the
// product of the per-field bounds, capped by the records. Every
// fallback keeps today's bound: an untracked field, a shard whose entry
// is absent, over the counting limit or opted out, a field recorded
// under another type, a GROUP_DATE under a per-row zone field, a
// grouper whose key is not one field's stored value, and no counts.
func TestEstimateMemory_DistinctBound(t *testing.T) {
	t.Parallel()

	schema := distinctSchema()
	const records = 100_000
	// The bucket bound the memory estimate charges (EstimateMemory's
	// buckets, crosstab axes and per-shard partials all read it); the
	// estimate's wiring end to end is internal/integration
	// TestLimits_DistinctBoundCoversEveryKey.
	buckets := func(in LimitInputs, req *types.Request) int64 {
		return memoryGroupBound(req.Groups, schema, nil, in.Records, in.keyBounds(req))
	}
	g := func(typ types.GroupType, field string) *types.Group {
		return &types.Group{Type: typ, Field: field, Interval: 10}
	}
	withDist := memIn(records)
	withDist.Distinct = twoShardDistinct()
	cases := []struct {
		name    string
		in      LimitInputs
		records int64
		req     *types.Request
		want    int64
	}{
		{"category over u32", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "big")}}, 50},
		{"category over u32, no counts", memIn(records), records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "big")}}, records},
		{"category over u32, fewer records", withDist, 10, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "big")}}, 10},
		{"category over u64", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "huge")}}, 15},
		{"rounded over f64", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_ROUNDED, "num")}}, 42},
		{"range over f64", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_RANGE, "num")}}, 42},
		{"date over datetime", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_DATE, "ts")}}, 24},
		{"date over date", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_DATE, "day")}}, 7},
		{"date over datetime, slot zone field", withDist, records, &types.Request{Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "zone"}}}, records},
		{"date over datetime, request zone field", withDist, records, &types.Request{TimeZoneField: "zone", Groups: []*types.Group{g(types.GROUP_DATE, "ts")}}, records},
		{"date over datetime, slot zone beats request zone field", withDist, records, &types.Request{TimeZoneField: "zone", Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZone: "UTC"}}}, 24},
		{"untracked u8 keeps its width", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "small")}}, 256},
		{"a shard's entry absent", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "absent")}}, records},
		{"a shard over the counting limit", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "over")}}, records},
		{"opted out", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "optout")}}, records},
		{"recorded under another type", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "retyped")}}, records},
		{"quantile keeps its bucket count", withDist, records, &types.Request{Groups: []*types.Group{{Type: types.GROUP_QUANTILE, Field: "big", Params: []byte(`{"n":4}`)}}}, 4},
		// 50 x 15 = 750 composite keys; 50 x 42 x 24 caps at the records.
		{"u32 x u64", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "big"), g(types.GROUP_CATEGORY, "huge")}}, 750},
		{"u32 x f64 x datetime, capped", withDist, 1_000, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "big"), g(types.GROUP_RANGE, "num"), g(types.GROUP_DATE, "ts")}}, 1_000},
		{"u32 x untracked u8", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "big"), g(types.GROUP_CATEGORY, "small")}}, 50 * 256},
		{"u32 x absent", withDist, records, &types.Request{Groups: []*types.Group{g(types.GROUP_CATEGORY, "big"), g(types.GROUP_CATEGORY, "absent")}}, records},
	}
	for _, c := range cases {
		in := c.in
		in.Records = c.records
		if got := buckets(in, c.req); got != c.want {
			t.Errorf("%s: charged %d buckets, want %d", c.name, got, c.want)
		}
	}

	// A join's processor reads joined rows the left archive's counts do
	// not describe: no key bound.
	join := withDist
	join.Join = true
	if kb := join.keyBounds(&types.Request{}); kb.dist != nil {
		t.Error("a join keeps the recorded counts as a key bound")
	}
	if kb := withDist.keyBounds(&types.Request{}); kb.dist == nil {
		t.Error("a plain scan drops the recorded counts")
	}
}

// distinctArchiveBytes writes a stored zip archive: the `_schema.pulse`
// doc (header + schema + SHRD for shardCount shards, then tail) and an
// empty-payload shard per name in entries.
func distinctArchiveBytes(t *testing.T, schema *encoding.Schema, shardCount int, dist *encx.ArchiveDistinct, tail []byte, entries ...string) *encx.Archive {
	t.Helper()
	var doc bytes.Buffer
	if err := encx.WriteSchemaDocDistinct(&doc, schema, 0, uint16(shardCount), dist); err != nil {
		t.Fatal(err)
	}
	doc.Write(tail)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	for _, name := range entries {
		write(name, writeShardPayload(t, schema, 0))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	arch, err := encx.OpenArchive(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return arch
}

// TestArchiveDistinctOf_Fallbacks (U73 #500): the one reader predict and
// the runtime pre-flight share returns the recorded counts only when the
// DIST trailer is well-formed and names exactly the archive's shards;
// otherwise nil — the estimate falls back to today's bound and predict
// never fails over an advisory stat: no trailer, a newer trailer
// version, a truncated (malformed) trailer, a trailer naming a shard the
// archive lacks, and an archive carrying a shard the trailer does not
// count (added outside Pulse — its keys would go uncounted).
func TestArchiveDistinctOf_Fallbacks(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "big", Type: encoding.FieldTypeU32}}}
	dist := &encx.ArchiveDistinct{
		Fields: []encx.DistinctField{{Name: "big", Type: encoding.FieldTypeU32}},
		Shards: []encx.ShardDistinct{{Name: "s0.pulse", Counts: []encx.DistinctCount{exact(3)}}, {Name: "s1.pulse", Counts: []encx.DistinctCount{exact(4)}}},
	}
	renamed := &encx.ArchiveDistinct{Fields: dist.Fields,
		Shards: []encx.ShardDistinct{dist.Shards[0], {Name: "gone.pulse", Counts: dist.Shards[1].Counts}}}
	repeated := &encx.ArchiveDistinct{Fields: dist.Fields,
		Shards: []encx.ShardDistinct{dist.Shards[0], dist.Shards[0]}}
	// A DIST header announcing a body longer than the bytes that follow.
	truncated := append([]byte("DIST"), encx.DistinctTrailerVersion)
	truncated = binary.LittleEndian.AppendUint64(truncated, 1<<20)
	newer := append([]byte("DIST"), encx.DistinctTrailerVersion+1)
	newer = binary.LittleEndian.AppendUint64(newer, 0)

	if got := ArchiveDistinctOf(distinctArchiveBytes(t, schema, 2, dist, nil, "s0.pulse", "s1.pulse")); got == nil {
		t.Fatal("a well-formed trailer naming every shard read as none")
	} else if n, ok := got.FieldBound("big"); !ok || n != 7 {
		t.Fatalf("FieldBound(big) = %d, %v; want 7, true", n, ok)
	}
	for _, c := range []struct {
		name string
		arch *encx.Archive
	}{
		{"no trailer", distinctArchiveBytes(t, schema, 2, nil, nil, "s0.pulse", "s1.pulse")},
		{"newer trailer version", distinctArchiveBytes(t, schema, 2, nil, newer, "s0.pulse", "s1.pulse")},
		{"malformed trailer", distinctArchiveBytes(t, schema, 2, nil, truncated, "s0.pulse", "s1.pulse")},
		{"trailer names a shard the archive lacks", distinctArchiveBytes(t, schema, 2, renamed, nil, "s0.pulse", "s1.pulse")},
		{"trailer repeats a shard", distinctArchiveBytes(t, schema, 2, repeated, nil, "s0.pulse", "s1.pulse")},
		{"archive carries an uncounted shard", distinctArchiveBytes(t, schema, 2, dist, nil, "s0.pulse", "s1.pulse", "extra.pulse")},
	} {
		if got := ArchiveDistinctOf(c.arch); got != nil {
			t.Errorf("%s: read counts %+v, want none", c.name, got)
		}
	}
}
