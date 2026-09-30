package service

import (
	"bytes"
	"flag"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// Positional-Record regression gates on the committed join-shape
// fixture (join_shape_fixture_test.go has the schema, seed and the
// in-test map-backed baseline).
//
// Every gate is a RATIO of the production positional path to the
// map-backed design it replaced, measured in the same process on the
// same bytes, so it holds on any architecture and any runner speed. The
// quantities are deterministic or near-deterministic — allocation counts
// (testing.AllocsPerRun) and live heap after a forced GC — never wall
// clock; the throughput and peak-heap ratios live in
// join_shape_bench_test.go, which only a -bench run executes.
//
// What the gates lock in: a later change that reintroduces per-row maps,
// per-field boxing or per-record layout allocation on the buffered or
// reuse decode path moves one of these ratios past its threshold.

var updateJoinShapeFixture = flag.Bool("update", false, "rewrite testdata/join_shape/join_shape.pulse from the generator")

// loadJoinShapeFixture reads the committed fixture into a hermetic
// in-memory filesystem.
func loadJoinShapeFixture(t testing.TB) (afero.Fs, string, *encoding.Schema, int) {
	t.Helper()
	data, err := afero.ReadFile(afero.NewOsFs(), joinShapeFixturePath)
	if err != nil {
		t.Fatalf("read fixture (regenerate with go test ./service/ -run TestJoinShapeFixture_MatchesGenerator -update): %v", err)
	}
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "join_shape.pulse", data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	count, trailing, ok := schema.RecordCountForPayload(int64(r.Len()))
	if !ok || trailing != 0 {
		t.Fatalf("fixture payload is not a whole number of records (trailing %d)", trailing)
	}
	return cfg.Fs(), "join_shape.pulse", schema, int(count)
}

// TestJoinShapeFixture_MatchesGenerator pins the committed fixture to
// the committed schema + seed and asserts the structural shape the
// gates depend on.
func TestJoinShapeFixture_MatchesGenerator(t *testing.T) {
	_, want, err := buildJoinShapeCohort(joinShapeFixtureParents)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if *updateJoinShapeFixture {
		if err := afero.WriteFile(afero.NewOsFs(), joinShapeFixturePath, want, 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	got, err := afero.ReadFile(afero.NewOsFs(), joinShapeFixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("committed fixture (%d bytes) differs from the generator (%d bytes); regenerate with -update if the schema or seed changed deliberately", len(got), len(want))
	}

	fsys, path, schema, rows := loadJoinShapeFixture(t)
	if n := len(schema.Fields); n != joinShapeParentFields+joinShapeChildFields {
		t.Fatalf("fields = %d, want %d", n, joinShapeParentFields+joinShapeChildFields)
	}
	types := map[encoding.FieldType]int{}
	var nParent, nChild, nNullable int
	for i, f := range schema.Fields {
		types[f.Type]++
		if f.Nullable {
			nNullable++
		}
		switch {
		case strings.HasPrefix(f.Name, "p_") && i < joinShapeParentFields:
			nParent++
		case strings.HasPrefix(f.Name, "c_") && i >= joinShapeParentFields:
			nChild++
		default:
			t.Fatalf("field %d %q is outside its block", i, f.Name)
		}
	}
	if nParent != joinShapeParentFields || nChild != joinShapeChildFields {
		t.Fatalf("block split = %d/%d, want %d/%d", nParent, nChild, joinShapeParentFields, joinShapeChildFields)
	}
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypePackedBool,
		encoding.FieldTypeU64, encoding.FieldTypeSetU8, encoding.FieldTypeSetU128,
	} {
		if types[ft] == 0 {
			t.Fatalf("type mix lacks %s", ft)
		}
	}
	if nNullable == 0 || !schema.HasBitmap() {
		t.Fatalf("fixture carries no nullable field")
	}
	if want := joinShapeRows(joinShapeFixtureParents); rows != want {
		t.Fatalf("rows = %d, want %d", rows, want)
	}

	// Fanout 12-13 and the structural rule: the parent block is constant
	// within a key.
	recs, err := drainLegacyBuffered(fsys, path, schema, nil, 0, rows)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	fanout := map[float64]int{}
	first := map[float64]*legacyBufferedRecord{}
	var nulls int
	for _, r := range recs {
		key := r.values["p_key"]
		fanout[key]++
		nulls += len(r.nulls)
		f, ok := first[key]
		if !ok {
			first[key] = r
			continue
		}
		for i := 1; i < joinShapeParentFields; i++ {
			n := schema.Fields[i].Name
			if f.values[n] != r.values[n] || f.nulls[n] != r.nulls[n] || !reflect.DeepEqual(f.wide[n], r.wide[n]) {
				t.Fatalf("parent field %s varies within key %v", n, key)
			}
		}
	}
	if len(fanout) != joinShapeFixtureParents {
		t.Fatalf("distinct keys = %d, want %d", len(fanout), joinShapeFixtureParents)
	}
	for k, n := range fanout {
		if n < 12 || n > 13 {
			t.Fatalf("key %v fans out to %d rows, want 12-13", k, n)
		}
	}
	if nulls == 0 {
		t.Fatalf("fixture carries no null cell")
	}
}

// TestJoinShapeFixture_PositionalMatchesMapDecode is the correctness
// half: every record the positional buffered path materialises reads
// back, through every public accessor, exactly as the map-backed record
// decoded from the same bytes does — full decode and projected.
func TestJoinShapeFixture_PositionalMatchesMapDecode(t *testing.T) {
	fsys, path, schema, rows := loadJoinShapeFixture(t)
	for _, tc := range []struct {
		name  string
		keep  encoding.FieldFilter
		keepN int
	}{
		{name: "full"},
		{name: "projected4", keep: joinShapeKeep4, keepN: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := drainPositionalBuffered(fsys, path, schema, tc.keep, tc.keepN, rows)
			if err != nil {
				t.Fatalf("positional: %v", err)
			}
			want, err := drainLegacyBuffered(fsys, path, schema, tc.keep, tc.keepN, rows)
			if err != nil {
				t.Fatalf("map: %v", err)
			}
			if len(got) != rows || len(want) != rows {
				t.Fatalf("rows positional=%d map=%d, want %d", len(got), len(want), rows)
			}
			for i := range got {
				g := got[i]
				w := processing.NewRecordWithWide(schema, want[i].values, want[i].nulls, want[i].wide)
				for _, f := range schema.Fields {
					n := f.Name
					gv, gok := g.NumericValue(n)
					wv, wok := w.NumericValue(n)
					if gv != wv || gok != wok {
						t.Fatalf("row %d NumericValue(%s) = (%v,%v), map (%v,%v)", i, n, gv, gok, wv, wok)
					}
					if g.IsNull(n) != w.IsNull(n) {
						t.Fatalf("row %d IsNull(%s) = %v, map %v", i, n, g.IsNull(n), w.IsNull(n))
					}
					gs, gsok := g.StringValue(n)
					ws, wsok := w.StringValue(n)
					if gs != ws || gsok != wsok {
						t.Fatalf("row %d StringValue(%s) = (%q,%v), map (%q,%v)", i, n, gs, gsok, ws, wsok)
					}
					gm, gmok := g.SetMaskValue(n)
					wm, wmok := w.SetMaskValue(n)
					if gm != wm || gmok != wmok {
						t.Fatalf("row %d SetMaskValue(%s) differs", i, n)
					}
				}
				if !reflect.DeepEqual(g.AllValues(), w.AllValues()) {
					t.Fatalf("row %d AllValues differs:\n got  %v\n want %v", i, g.AllValues(), w.AllValues())
				}
			}
		})
	}
}

// retainedPerRecord returns the live heap held per materialised record:
// HeapAlloc after a forced GC with the records reachable, minus the
// same before materialising, over the record count. passes repeats the
// fixture so the figure is taken over thousands of records, and the
// minimum of three attempts discards background-allocation noise.
func retainedPerRecord(t *testing.T, passes int, materialize func() (any, int, error)) float64 {
	t.Helper()
	best := -1.0
	for range 3 {
		held := make([]any, 0, passes)
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		n := 0
		for range passes {
			recs, k, err := materialize()
			if err != nil {
				t.Fatalf("materialize: %v", err)
			}
			held = append(held, recs)
			n += k
		}
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		per := (float64(after.HeapAlloc) - float64(before.HeapAlloc)) / float64(n)
		runtime.KeepAlive(held)
		if best < 0 || per < best {
			best = per
		}
	}
	return best
}

// TestJoinShapeFixture_BufferedRetainedRatio gates the resident size of
// a buffered record — the figure that decides whether a cohort fits in
// memory — as a ratio against the map-backed record.
func TestJoinShapeFixture_BufferedRetainedRatio(t *testing.T) {
	fsys, path, schema, rows := loadJoinShapeFixture(t)
	const passes = 16 // 16 x 400 = 6,400 records per attempt
	for _, tc := range []struct {
		name  string
		keep  encoding.FieldFilter
		keepN int
		// maxRatio is positional / map retained B/record, with ~40%
		// headroom over the measured figure (arm64, go1.26).
		maxRatio float64
	}{
		// MEASURED full: positional 1026 B, map 4198 B, ratio 0.244.
		{name: "full", maxRatio: 0.35},
		// MEASURED projected4: positional 149 B, map 409 B, ratio 0.364.
		{name: "projected4", keep: joinShapeKeep4, keepN: 4, maxRatio: 0.50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pos := retainedPerRecord(t, passes, func() (any, int, error) {
				recs, err := drainPositionalBuffered(fsys, path, schema, tc.keep, tc.keepN, rows)
				return recs, len(recs), err
			})
			legacy := retainedPerRecord(t, passes, func() (any, int, error) {
				recs, err := drainLegacyBuffered(fsys, path, schema, tc.keep, tc.keepN, rows)
				return recs, len(recs), err
			})
			ratio := pos / legacy
			t.Logf("retained B/record: positional=%.0f map=%.0f ratio=%.3f (max %.2f)", pos, legacy, ratio, tc.maxRatio)
			if !(ratio <= tc.maxRatio) {
				t.Fatalf("buffered record retains %.0f B vs %.0f B map-backed: ratio %.3f exceeds %.2f — per-record storage regressed toward the map design", pos, legacy, ratio, tc.maxRatio)
			}
		})
	}
}

// TestJoinShapeFixture_DecodeAllocsRatio gates per-row allocation on
// both decode arms as a ratio against the map-backed design. Allocation
// counts are deterministic, so this is the CI-stable proxy for decode
// throughput: per-row allocation is what the positional record removed.
//
// The per-row figure is MARGINAL — allocations decoding every row minus
// allocations decoding one, over rows-1 — so the fixed per-open cost
// (the schema and dictionary parse, which dominates a 400-row fixture)
// cancels out instead of diluting both arms toward a ratio of 1.
func TestJoinShapeFixture_DecodeAllocsRatio(t *testing.T) {
	fsys, path, schema, rows := loadJoinShapeFixture(t)
	allocs := func(fn func(limit int) (int, error), limit int) float64 {
		var err error
		n := 0
		a := testing.AllocsPerRun(5, func() {
			n, err = fn(limit)
		})
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if n != limit {
			t.Fatalf("decoded %d rows, want %d", n, limit)
		}
		return a
	}
	perRow := func(fn func(limit int) (int, error)) float64 {
		return (allocs(fn, rows) - allocs(fn, 1)) / float64(rows-1)
	}
	buffered := func(keep encoding.FieldFilter, keepN int, legacy bool) func(int) (int, error) {
		return func(limit int) (int, error) {
			if legacy {
				recs, err := drainLegacyBuffered(fsys, path, schema, keep, keepN, limit)
				return len(recs), err
			}
			recs, err := drainPositionalBuffered(fsys, path, schema, keep, keepN, limit)
			return len(recs), err
		}
	}
	for _, tc := range []struct {
		name       string
		positional func(limit int) (int, error)
		legacy     func(limit int) (int, error)
		maxRatio   float64
	}{
		// MEASURED (arm64, go1.26): positional 3 (record, values, presence
		// planes), map 108.5, ratio 0.028. 0.05 admits at most 5 allocs/row.
		{name: "buffered-full", positional: buffered(nil, 0, false), legacy: buffered(nil, 0, true), maxRatio: 0.05},
		// MEASURED: positional 3, map 9, ratio 0.333. 0.40 fails a 4th
		// per-row allocation.
		{name: "buffered-projected4", positional: buffered(joinShapeKeep4, 4, false), legacy: buffered(joinShapeKeep4, 4, true), maxRatio: 0.40},
		// MEASURED: positional 0 (sets ride typed storage, no boxing), map
		// 3.83 (boxed set masks), ratio 0. 0.10 fails ~1 alloc per 3 rows.
		{
			name:       "reuse-full",
			positional: func(limit int) (int, error) { return scanPositionalReuse(fsys, path, schema, limit) },
			legacy:     func(limit int) (int, error) { return scanLegacyReuse(fsys, path, schema, limit) },
			maxRatio:   0.1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pos := perRow(tc.positional)
			legacy := perRow(tc.legacy)
			ratio := pos / legacy
			t.Logf("marginal allocs/row: positional=%.3f map=%.3f ratio=%.3f (max %.2f)", pos, legacy, ratio, tc.maxRatio)
			if !(ratio <= tc.maxRatio) {
				t.Fatalf("%s: %.3f allocs/row vs %.3f map-backed: ratio %.3f exceeds %.2f", tc.name, pos, legacy, ratio, tc.maxRatio)
			}
		})
	}
}
