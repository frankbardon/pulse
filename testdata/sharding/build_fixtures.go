//go:build ignore

// build_fixtures.go regenerates the canonical Pulse shard archives
// committed under testdata/sharding/ for use as snapshot inputs by the
// non-skippable CI gates. Run it from the repo root:
//
//	go run ./testdata/sharding/build_fixtures.go
//
// The resulting archives are stable byte-for-byte across runs because
// Pulse writes uncompressed entries (Method 0) with deterministic
// dictionary/header bytes and we set every zip entry's modtime to a
// fixed epoch below. Re-run only when the canonical schema or shard
// contents themselves change.
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/frankbardon/pulse/encoding"
)

// stableModtime is the deterministic modtime stamped on every zip
// entry so the archive bytes are reproducible. Picking 2020-01-01 UTC
// avoids any local-time-zone drift.
var stableModtime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	outDir := filepath.Join("testdata", "sharding")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail("MkdirAll: %v", err)
	}

	// Schema used by every fixture: u32 id, f64 score, categorical
	// region. The categorical width is u8 so dict-growth fits inside
	// 256 entries.
	schema := func() *encoding.Schema {
		dict := encoding.NewDictionary()
		for _, v := range []string{"US", "CA"} {
			if _, err := dict.Add(v); err != nil {
				fail("seed dict: %v", err)
			}
		}
		return &encoding.Schema{
			Fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0,
					CsvColumnIdx: 0, Description: "Record id"},
				{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4,
					CsvColumnIdx: 1, Description: "Numeric score"},
				{Name: "region", Type: encoding.FieldTypeCategoricalU8,
					ByteOffset: 12, CsvColumnIdx: 2, Description: "Region code",
					Dictionary: dict},
			},
		}
	}

	// --- two_shards.pulse: 2 shards, 3 records each, shared dict ---
	{
		s := schema()
		writeArchive(outDir, "two_shards.pulse", s, []shard{
			{
				Name: "20190101.pulse",
				Records: []record{
					{id: 1, score: 10.0, region: "US"},
					{id: 2, score: 20.0, region: "CA"},
					{id: 3, score: 30.0, region: "US"},
				},
			},
			{
				Name: "20190201.pulse",
				Records: []record{
					{id: 4, score: 40.0, region: "US"},
					{id: 5, score: 50.0, region: "CA"},
					{id: 6, score: 60.0, region: "US"},
				},
			},
		}, sharedDictOnly)
	}

	// --- dict_growth.pulse: shard 2 extends region dict with "MX" ---
	{
		s := schema()
		writeArchive(outDir, "dict_growth.pulse", s, []shard{
			{
				Name: "20190101.pulse",
				Records: []record{
					{id: 1, score: 10.0, region: "US"},
					{id: 2, score: 20.0, region: "CA"},
				},
				// Shard 1 dict is just the seed: [US, CA].
				dictExtra: nil,
			},
			{
				Name: "20190201.pulse",
				Records: []record{
					{id: 3, score: 30.0, region: "US"},
					{id: 4, score: 40.0, region: "MX"},
				},
				// Shard 2 extends the dict with MX.
				dictExtra: []string{"MX"},
			},
		}, perShardDict)
	}

	// --- three_shards.pulse: 3 shards, 2 records each, shared dict ---
	{
		s := schema()
		writeArchive(outDir, "three_shards.pulse", s, []shard{
			{
				Name: "20190101.pulse",
				Records: []record{
					{id: 1, score: 10.0, region: "US"},
					{id: 2, score: 20.0, region: "CA"},
				},
			},
			{
				Name: "20190201.pulse",
				Records: []record{
					{id: 3, score: 30.0, region: "US"},
					{id: 4, score: 40.0, region: "CA"},
				},
			},
			{
				Name: "20190301.pulse",
				Records: []record{
					{id: 5, score: 50.0, region: "US"},
					{id: 6, score: 60.0, region: "CA"},
				},
			},
		}, sharedDictOnly)
	}

	fmt.Println("Wrote testdata/sharding/{two_shards,dict_growth,three_shards}.pulse")
}

type record struct {
	id     uint32
	score  float64
	region string
}

type shard struct {
	Name      string
	Records   []record
	dictExtra []string // when non-nil, this shard's dict = canonical seed + dictExtra
}

type dictMode int

const (
	sharedDictOnly dictMode = iota // every shard uses the canonical seed dict
	perShardDict                   // each shard carries its own dict (= seed + dictExtra)
)

// writeArchive composes a Pulse shard archive on disk: a _schema.pulse
// entry carrying the canonical schema doc plus one entry per shard,
// each a complete single-file .pulse. Method 0 entries with a fixed
// modtime keep the byte output reproducible across runs.
func writeArchive(outDir, filename string, canonical *encoding.Schema, shards []shard, mode dictMode) {
	// Build canonical schema with the *extended* dictionary (so the
	// dict_growth fixture's _schema.pulse reflects [US, CA, MX]).
	canonicalSchema := cloneSchema(canonical)
	if mode == perShardDict {
		extendDict(canonicalSchema, gatherDictExtras(shards))
	}

	var total uint64
	for _, s := range shards {
		total += uint64(len(s.Records))
	}
	var doc bytes.Buffer
	if err := encoding.WriteSchemaDoc(&doc, canonicalSchema, total, uint16(len(shards))); err != nil {
		fail("WriteSchemaDoc(%s): %v", filename, err)
	}

	var arch bytes.Buffer
	zw := zip.NewWriter(&arch)
	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     name,
			Method:   zip.Store,
			Modified: stableModtime,
		})
		if err != nil {
			fail("zip CreateHeader(%s/%s): %v", filename, name, err)
		}
		if _, err := w.Write(payload); err != nil {
			fail("zip write(%s/%s): %v", filename, name, err)
		}
	}
	write(encoding.ReservedSchemaName, doc.Bytes())
	for _, s := range shards {
		write(s.Name, writeSingleShard(canonical, s, mode))
	}
	if err := zw.Close(); err != nil {
		fail("zip Close(%s): %v", filename, err)
	}

	outPath := filepath.Join(outDir, filename)
	if err := os.WriteFile(outPath, arch.Bytes(), 0o644); err != nil {
		fail("WriteFile(%s): %v", outPath, err)
	}
	fmt.Printf("  %s (%d bytes)\n", outPath, len(arch.Bytes()))
}

// writeSingleShard returns the bytes of a complete single-file .pulse
// cohort for one shard. The shard's own categorical dictionary equals
// the canonical seed under sharedDictOnly mode, or seed+dictExtra under
// perShardDict mode. Records are encoded according to the shard's dict.
func writeSingleShard(canonical *encoding.Schema, s shard, mode dictMode) []byte {
	shardSchema := cloneSchema(canonical)
	if mode == perShardDict {
		extendDict(shardSchema, s.dictExtra)
	}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		fail("WriteHeader(%s): %v", s.Name, err)
	}
	if err := encoding.WriteSchema(&buf, shardSchema); err != nil {
		fail("WriteSchema(%s): %v", s.Name, err)
	}
	for _, r := range s.Records {
		// id (u32).
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, uint64(r.id)); err != nil {
			fail("WriteFieldValue id: %v", err)
		}
		// score (f64) — bit pattern.
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(r.score)); err != nil {
			fail("WriteFieldValue score: %v", err)
		}
		// region (categorical_u8).
		idx, ok := shardSchema.Fields[2].Dictionary.IDFor(r.region)
		if !ok {
			fail("region %q not in shard %q dict", r.region, s.Name)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, uint64(idx)); err != nil {
			fail("WriteFieldValue region: %v", err)
		}
	}
	return buf.Bytes()
}

// gatherDictExtras returns the union of dictExtra entries across the
// shards in insertion order (no dedup; callers pass already-dedup'd
// values for fixtures).
func gatherDictExtras(shards []shard) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range shards {
		for _, v := range s.dictExtra {
			if !seen[v] {
				out = append(out, v)
				seen[v] = true
			}
		}
	}
	return out
}

// cloneSchema returns a deep-enough copy of s that mutating the clone's
// per-field Dictionary doesn't affect the original.
func cloneSchema(s *encoding.Schema) *encoding.Schema {
	fields := make([]encoding.Field, len(s.Fields))
	copy(fields, s.Fields)
	for i := range fields {
		if fields[i].Dictionary != nil {
			cloneDict := encoding.NewDictionary()
			for _, v := range fields[i].Dictionary.Values() {
				if _, err := cloneDict.Add(v); err != nil {
					fail("clone dict add(%q): %v", v, err)
				}
			}
			fields[i].Dictionary = cloneDict
		}
	}
	return &encoding.Schema{Fields: fields}
}

func extendDict(s *encoding.Schema, extras []string) {
	if len(extras) == 0 {
		return
	}
	for fi := range s.Fields {
		if !s.Fields[fi].Type.IsCategorical() {
			continue
		}
		if s.Fields[fi].Dictionary == nil {
			s.Fields[fi].Dictionary = encoding.NewDictionary()
		}
		for _, v := range extras {
			if _, err := s.Fields[fi].Dictionary.Add(v); err != nil {
				fail("extend dict add(%q): %v", v, err)
			}
		}
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "build_fixtures: "+format+"\n", args...)
	os.Exit(1)
}
