package pulse_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	pfs "github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// cohortFuzzContract caps one read's total heap allocation at 256 MiB:
// far above what an honest cohort or archive of at most
// fuzzcontract.DefaultMaxInput bytes needs to open and read whole, far
// below what a decoder sizing a buffer from a length the input merely
// declares (a record count, a dictionary size, an extension block
// length, a shard count, a zip entry size) would take.
var cohortFuzzContract = fuzzcontract.Contract{MaxAlloc: 256 << 20}

// cohortFuzzSeq names each fuzzed input's file uniquely, so no cached
// view of an earlier input can answer for a later one.
var cohortFuzzSeq atomic.Int64

// cohortFuzzPulse is the hermetic instance a cohort fuzz target reads
// through: an in-memory fs, the engine capped by fuzzcontract.Limits.
func cohortFuzzPulse(tb testing.TB) (*pulse.Pulse, afero.Fs) {
	tb.Helper()
	mem := pfs.NewMemMap().Fs()
	p, err := pulse.New(pulse.Options{FS: mem, Limits: fuzzcontract.Limits()})
	if err != nil {
		tb.Fatal(err)
	}
	return p, mem
}

// cohortFuzzSchemas are the seed schemas: between them every field-type
// family, nullable and not, both format versions — 0x01 (ungrouped,
// vector-free) and 0x02 (a parent group, vectors) — and the wide sets.
func cohortFuzzSchemas() map[string]encoding.Schema {
	f := func(name string, ft encoding.FieldType, nullable bool) encoding.Field {
		return encoding.Field{Name: name, Type: ft, Nullable: nullable, Description: "fuzz seed field " + name}
	}
	dec := f("amount", encoding.FieldTypeDecimal128, true)
	dec.Precision, dec.Scale = 12, 2
	return map[string]encoding.Schema{
		"v1-scalars": {Fields: []encoding.Field{
			f("a8", encoding.FieldTypeU8, true),
			f("a16", encoding.FieldTypeU16, false),
			f("a32", encoding.FieldTypeU32, false),
			f("a64", encoding.FieldTypeU64, true),
			f("nib", encoding.FieldTypeU4, true),
			f("flag", encoding.FieldTypePackedBool, true),
			f("r32", encoding.FieldTypeF32, false),
			f("r64", encoding.FieldTypeF64, true),
			f("day", encoding.FieldTypeDate, true),
			f("at", encoding.FieldTypeDateTime, false),
		}},
		"v1-dictionaries": {Fields: []encoding.Field{
			f("region", encoding.FieldTypeCategoricalU8, true),
			f("city", encoding.FieldTypeCategoricalU16, false),
			f("tags", encoding.FieldTypeSetU8, true),
			f("wide", encoding.FieldTypeSetU128, true),
			f("wider", encoding.FieldTypeSetU256, false),
			dec,
		}},
		"v1-not-nullable": {Fields: []encoding.Field{
			f("id", encoding.FieldTypeU32, false),
			f("kind", encoding.FieldTypeCategoricalU8, false),
		}},
		"v2-vectors": {
			Fields: []encoding.Field{
				f("id", encoding.FieldTypeU16, false),
				f("emb", encoding.FieldTypeVecF32, true),
				f("pos", encoding.FieldTypeVecF64, false),
			},
			Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 3}, {FieldIndex: 2, Dim: 2}},
		},
		"v2-groups": {Fields: []encoding.Field{
			f("store", encoding.FieldTypeCategoricalU8, false),
			f("store_region", encoding.FieldTypeCategoricalU8, true),
			f("store_size", encoding.FieldTypeU32, false),
			f("sales", encoding.FieldTypeF64, true),
		}},
	}
}

// cohortFuzzRows returns n rows for the named seed schema; every third
// row carries nulls in the nullable fields.
func cohortFuzzRows(name string, n int) []pulse.CohortRow {
	rows := make([]pulse.CohortRow, 0, n)
	for i := 0; i < n; i++ {
		null := i%3 == 2
		pick := func(v any) any {
			if null {
				return nil
			}
			return v
		}
		var row pulse.CohortRow
		switch name {
		case "v1-scalars":
			row = pulse.CohortRow{pick(uint64(i)), uint64(i * 300), uint64(i * 70000), pick(uint64(1) << 40), pick(uint64(i % 16)), pick(i%2 == 0),
				float32(i) / 2, pick(float64(i) * 1.25), pick(int32(19000 + i)), int64(1_700_000_000 + i)}
		case "v1-dictionaries":
			row = pulse.CohortRow{pick([]string{"north", "south", "east"}[i%3]), []string{"oslo", "rome"}[i%2],
				pick([]string{"x", fmt.Sprintf("t%d", i)}), pick([]string{"w0", "w1"}), []string{}, pick(encoding.NewDecimal128FromInt(int64(i * 1234)))}
		case "v1-not-nullable":
			row = pulse.CohortRow{uint64(i), []string{"a", "b"}[i%2]}
		case "v2-vectors":
			row = pulse.CohortRow{uint64(i), pick([]float32{float32(i), 0.5, -1}), []float64{float64(i), 2}}
		case "v2-groups":
			s := i % 2
			row = pulse.CohortRow{[]string{"s1", "s2"}[s], []string{"west", "east"}[s], uint64(100 + s), pick(float64(i) * 3)}
		}
		rows = append(rows, row)
	}
	return rows
}

// cohortFuzzGroups declares the v2-groups seed's parent group.
func cohortFuzzGroups(name string) []pio.GroupDecl {
	if name != "v2-groups" {
		return nil
	}
	return []pio.GroupDecl{{Key: []string{"store"}, Members: []string{"store_region", "store_size"}}}
}

// buildFuzzCohort writes one seed cohort (or, with shardRows > 0, a
// shard archive split every shardRows rows) and returns its bytes.
func buildFuzzCohort(tb testing.TB, name string, rows, shardRows int) []byte {
	tb.Helper()
	p, mem := cohortFuzzPulse(tb)
	opts := pulse.CohortBuilderOptions{Groups: cohortFuzzGroups(name), RatioFloor: 1}
	if shardRows > 0 {
		opts.Shards = &pulse.ShardSplit{MaxRecords: shardRows}
	}
	ctx := context.Background()
	b, err := p.NewCohortBuilder(ctx, "seed.pulse", cohortFuzzSchemas()[name], opts)
	if err != nil {
		tb.Fatalf("%s: %v", name, err)
	}
	for _, r := range cohortFuzzRows(name, rows) {
		if err := b.Append(r); err != nil {
			tb.Fatalf("%s: append: %v", name, err)
		}
	}
	if _, err := b.Close(); err != nil {
		tb.Fatalf("%s: close: %v", name, err)
	}
	body, err := afero.ReadFile(mem, "seed.pulse")
	if err != nil {
		tb.Fatal(err)
	}
	return body
}

// cohortFuzzTruncations returns body cut at the header, mid-schema,
// mid-record and one byte short.
func cohortFuzzTruncations(body []byte) [][]byte {
	var out [][]byte
	for _, cut := range []int{encoding.HeaderSize, len(body) / 3, len(body) / 2, len(body) - 1} {
		if cut > 0 && cut < len(body) {
			out = append(out, body[:cut])
		}
	}
	return out
}

// cohortFuzzSeeds are the FuzzCohortRead seeds: every seed schema as a
// single-file cohort (with rows and empty), their truncations, and the
// degenerate inputs.
func cohortFuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	seeds := [][]byte{nil, []byte("PULSE\x00\x00\x00"), []byte("PULSE\x00\x00\x00\x01"), []byte("PULSE\x00\x00\x00\x02")}
	for _, name := range []string{"v1-scalars", "v1-dictionaries", "v1-not-nullable", "v2-vectors", "v2-groups"} {
		body := buildFuzzCohort(tb, name, 6, 0)
		seeds = append(seeds, body, buildFuzzCohort(tb, name, 0, 0))
		seeds = append(seeds, cohortFuzzTruncations(body)...)
	}
	return seeds
}

// readCohortFuzz is the embedder's read of one untrusted cohort path:
// Inspect and CountRecords (header-only), Open, then every record
// through Cohort.Reader, then a counting Process through the engine's
// record iterator. For an archive it also lists and verifies the shards
// and reads the first one through its anchor.
func readCohortFuzz(p *pulse.Pulse, mem afero.Fs, in []byte) error {
	ctx := context.Background()
	path := fmt.Sprintf("in-%d.pulse", cohortFuzzSeq.Add(1))
	if err := afero.WriteFile(mem, path, in, 0o644); err != nil {
		return err
	}
	defer mem.Remove(path) //nolint:errcheck
	if _, err := p.Inspect(ctx, path); err != nil {
		return err
	}
	if _, err := p.CountRecords(ctx, path); err != nil {
		return err
	}
	c, err := p.Open(ctx, path)
	if err != nil {
		return err
	}
	if err := readAllRecords(c); err != nil {
		return err
	}
	if fields := c.Schema().Fields; len(fields) > 0 {
		if _, err := p.Process(ctx, &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{{Type: "AGG_COUNT", Field: fields[0].Name}},
		}); err != nil {
			return err
		}
	}
	shards := c.Shards()
	if len(shards) == 0 {
		return nil
	}
	if _, err := p.ListShards(ctx, path); err != nil {
		return err
	}
	if _, err := p.VerifyShardArchive(ctx, path); err != nil {
		return err
	}
	anchored, err := p.Open(ctx, path+"#"+shards[0].Filename)
	if err != nil {
		return err
	}
	return readAllRecords(anchored)
}

// readAllRecords decodes every record of c through its reader.
func readAllRecords(c *pulse.Cohort) error {
	r, err := c.Reader()
	if err != nil {
		return err
	}
	defer r.Close() //nolint:errcheck
	for i := int64(0); i < r.Len(); i++ {
		if _, err := r.RecordAt(i); err != nil {
			return err
		}
	}
	return r.Close()
}

// FuzzCohortRead holds the single-file `.pulse` read path an embedder
// uses — header → schema (0x01 and 0x02: parent groups, vectors, sets
// and wide sets, decimal, the nullable bitmap) → dictionaries → every
// record, through Inspect, CountRecords, pulse.Open + Cohort.Reader and
// a counting Process — to the fuzz contract on a hermetic fs: no panic,
// a bounded run with a heap cap that fails a declared-length allocation
// bomb, every error a registered *errors.CodedError.
func FuzzCohortRead(f *testing.F) {
	for _, s := range cohortFuzzSeeds(f) {
		f.Add(s)
	}
	p, mem := cohortFuzzPulse(f)
	f.Fuzz(func(t *testing.T, in []byte) {
		cohortFuzzContract.Run(t, in, func(in []byte) error { return readCohortFuzz(p, mem, in) })
	})
}

// archiveFuzzSeeds are the FuzzShardArchiveOpen seeds: every seed
// schema as a shard archive split into 2-row shards (and an empty
// one-shard archive), an archive whose shards' categorical and set
// dictionaries diverge (each a prefix of the union), their truncations,
// and the degenerate zip inputs.
func archiveFuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	seeds := [][]byte{[]byte("PK\x03\x04"), []byte("PK\x05\x06" + string(make([]byte, 18)))}
	for _, name := range []string{"v1-scalars", "v1-dictionaries", "v1-not-nullable", "v2-vectors", "v2-groups"} {
		body := buildFuzzCohort(tb, name, 5, 2)
		seeds = append(seeds, body, buildFuzzCohort(tb, name, 0, 1))
		seeds = append(seeds, cohortFuzzTruncations(body)...)
	}
	return append(seeds, divergentFuzzArchive(tb))
}

// divergentFuzzArchive builds an archive of two separately written
// shards whose dictionaries differ — the canonical schema is their
// union-merge, the second shard's dictionaries are re-encoded onto it,
// and the set field's 11 members widen it past set_u8.
func divergentFuzzArchive(tb testing.TB) []byte {
	tb.Helper()
	p, mem := cohortFuzzPulse(tb)
	ctx := context.Background()
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "kind", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "fuzz seed field kind"},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true, Description: "fuzz seed field tags"},
	}}
	write := func(path, prefix string, n int) {
		b, err := p.NewCohortBuilder(ctx, path, schema, pulse.CohortBuilderOptions{})
		if err != nil {
			tb.Fatal(err)
		}
		for i := 0; i < n; i++ {
			tags := make([]string, 0, n)
			for j := 0; j <= i; j++ {
				tags = append(tags, fmt.Sprintf("%s%d", prefix, j))
			}
			if err := b.Append(pulse.CohortRow{fmt.Sprintf("k%d", i), tags}); err != nil {
				tb.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			tb.Fatal(err)
		}
	}
	write("a.pulse", "t", 5)
	write("b.pulse", "u", 6)
	if _, err := p.CreateShardArchive(ctx, "seed.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		tb.Fatal(err)
	}
	body, err := afero.ReadFile(mem, "seed.pulse")
	if err != nil {
		tb.Fatal(err)
	}
	return body
}

// FuzzShardArchiveOpen holds the shard-archive open path an embedder
// uses — the zip dispatch at pulse.Open (PK magic), the central
// directory, the reserved `_schema.pulse` entry and its SHRD trailer,
// every shard entry, the canonical-schema record read through
// Cohort.Reader and a counting Process, ListShards, VerifyShardArchive
// and an anchored shard open — to the fuzz contract on a hermetic fs,
// under the same heap cap as FuzzCohortRead.
func FuzzShardArchiveOpen(f *testing.F) {
	for _, s := range archiveFuzzSeeds(f) {
		f.Add(s)
	}
	p, mem := cohortFuzzPulse(f)
	f.Fuzz(func(t *testing.T, in []byte) {
		cohortFuzzContract.Run(t, in, func(in []byte) error { return readCohortFuzz(p, mem, in) })
	})
}

// TestCohortFuzzSeedsRead pins that the fuzz targets are not vacuous:
// every VALID seed reads clean end to end — every record decoded, the
// archive arm reached for archives — and the seeds span both format
// versions, a parent group, vectors and the wide sets.
func TestCohortFuzzSeedsRead(t *testing.T) {
	t.Parallel()
	p, mem := cohortFuzzPulse(t)
	ctx := context.Background()
	sawGroups, sawVectors, sawWide := false, false, false
	check := func(name string, body []byte, archive bool, want int64) {
		if err := readCohortFuzz(p, mem, body); err != nil {
			t.Fatalf("%s: valid seed refused: %v", name, err)
		}
		path := "check-" + name + ".pulse"
		if err := afero.WriteFile(mem, path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := p.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(c.Shards()) > 0; got != archive {
			t.Fatalf("%s: archive = %v, want %v", name, got, archive)
		}
		r, err := c.Reader()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close() //nolint:errcheck
		if r.Len() != want {
			t.Fatalf("%s: %d records, want %d", name, r.Len(), want)
		}
		s := r.Schema()
		sawGroups = sawGroups || s.HasGroups()
		sawVectors = sawVectors || s.HasVectors()
		for _, f := range s.Fields {
			sawWide = sawWide || f.Type.IsWideSet()
		}
	}
	for _, name := range []string{"v1-scalars", "v1-dictionaries", "v1-not-nullable", "v2-vectors", "v2-groups"} {
		body := buildFuzzCohort(t, name, 6, 0)
		wantVersion := byte(encoding.FormatVersionV1)
		if name[:2] == "v2" {
			wantVersion = encoding.FormatVersionV2
		}
		if body[encoding.HeaderSize-1] != wantVersion {
			t.Fatalf("%s: format version %#x, want %#x", name, body[encoding.HeaderSize-1], wantVersion)
		}
		check(name, body, false, 6)
		check(name+"-archive", buildFuzzCohort(t, name, 5, 2), true, 5)
	}
	check("divergent", divergentFuzzArchive(t), true, 11)
	if c, err := p.Open(ctx, "check-divergent.pulse"); err != nil || c.Field("tags").Type != encoding.FieldTypeSetU16 {
		t.Fatalf("divergent archive did not widen its set field: %v", err)
	}
	if !sawGroups || !sawVectors || !sawWide {
		t.Fatalf("seeds miss a variant: groups=%v vectors=%v wide sets=%v", sawGroups, sawVectors, sawWide)
	}
}
