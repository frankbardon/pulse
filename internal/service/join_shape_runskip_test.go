package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// Run-skip on the join-shape fixture: the reuse decoders skip rewriting
// fields whose on-wire bytes repeat the previous row (encoding
// RunSkipRecord). The fixture is SORTED by its parent key — the parent
// block repeats across each parent's 12–13 child rows — and a SCATTERED
// copy permutes its rows, so the same bytes exercise both the paying and
// the non-paying case.
//
// Every test here compares the production reuse iterator against a
// baseline that decodes the same bytes with run-skip OFF (noSkipRecord),
// row by row through every public accessor. That equality is the
// load-bearing claim: skipping is invisible in the output.

// noSkipRecord forwards the reuse-decoder contracts to a positional
// Record but deliberately does not implement encoding.RunSkipRecord, so
// the decoder clears and fully repopulates it every row — the
// pre-run-skip behaviour, on the same storage.
type noSkipRecord struct{ r *processing.Record }

func (n noSkipRecord) SetNumeric(name string, v float64)      { n.r.SetNumeric(name, v) }
func (n noSkipRecord) SetNullField(name string)               { n.r.SetNullField(name) }
func (n noSkipRecord) SetWideField(name string, v any)        { n.r.SetWideField(name, v) }
func (n noSkipRecord) SetNumericAt(i int, v float64)          { n.r.SetNumericAt(i, v) }
func (n noSkipRecord) SetNullFieldAt(i int)                   { n.r.SetNullFieldAt(i) }
func (n noSkipRecord) SetWideFieldAt(i int, v any)            { n.r.SetWideFieldAt(i, v) }
func (n noSkipRecord) SetNarrowSetAt(i int, m uint64)         { n.r.SetNarrowSetAt(i, m) }
func (n noSkipRecord) SetWideSetAt(i int, m encoding.SetMask) { n.r.SetWideSetAt(i, m) }
func (n noSkipRecord) ClearForRow()                           { n.r.ClearForRow() }
func (n noSkipRecord) record() *processing.Record             { return n.r }
func newNoSkipRecord(schema *encoding.Schema) noSkipRecord {
	return noSkipRecord{processing.NewReusableRecord(schema)}
}
func (n noSkipRecord) read(rr *encx.RecordReader, keep encx.FieldFilter, plan *encx.DecodePlan) error {
	return rr.ReadRecordReusedWithPlan(n, keep, plan)
}

var _ encx.TypedSetRecord = noSkipRecord{}

// scatterJoinShapeCohort returns data with its payload rows permuted
// (deterministically), header and schema untouched.
func scatterJoinShapeCohort(data []byte, schema *encoding.Schema, rows int) []byte {
	stride := schema.RecordByteSize()
	prefix := len(data) - rows*stride
	out := append([]byte(nil), data[:prefix]...)
	perm := rand.New(rand.NewPCG(joinShapeSeed, 0x5CA7)).Perm(rows)
	for _, p := range perm {
		out = append(out, data[prefix+p*stride:prefix+(p+1)*stride]...)
	}
	return out
}

// joinShapeContinuation is the fraction of adjacent row pairs whose
// field bytes (and null bit) are identical, over all fields — the
// run-skip hit rate.
func joinShapeContinuation(data []byte, schema *encoding.Schema, rows int) float64 {
	stride := schema.RecordByteSize()
	payload := data[len(data)-rows*stride:]
	bm := schema.BitmapByteSize()
	same, total := 0, 0
	for k := 1; k < rows; k++ {
		cur, prev := payload[k*stride:(k+1)*stride], payload[(k-1)*stride:k*stride]
		c := 0
		for i := range schema.Fields {
			w := 1
			if !schema.Fields[i].Type.IsBitPacked() {
				w = schema.Fields[i].Type.ByteSize()
			}
			eq := bytes.Equal(cur[c:c+w], prev[c:c+w])
			if schema.Fields[i].Nullable {
				eq = eq && encoding.BitmapIsNull(cur[stride-bm:], i) == encoding.BitmapIsNull(prev[stride-bm:], i)
			}
			if eq {
				same++
			}
			total++
			c += w
		}
	}
	return float64(same) / float64(total)
}

func assertSameRecord(t *testing.T, label string, schema *encoding.Schema, g, w *processing.Record) {
	t.Helper()
	for _, f := range schema.Fields {
		n := f.Name
		gv, gok := g.NumericValue(n)
		wv, wok := w.NumericValue(n)
		if gv != wv || gok != wok {
			t.Fatalf("%s NumericValue(%s) = (%v,%v), want (%v,%v)", label, n, gv, gok, wv, wok)
		}
		if g.IsNull(n) != w.IsNull(n) {
			t.Fatalf("%s IsNull(%s) = %v, want %v", label, n, g.IsNull(n), w.IsNull(n))
		}
		gwv, gwok := g.WideValue(n)
		wwv, wwok := w.WideValue(n)
		if gwok != wwok || !reflect.DeepEqual(gwv, wwv) {
			t.Fatalf("%s WideValue(%s) = (%v,%v), want (%v,%v)", label, n, gwv, gwok, wwv, wwok)
		}
		gm, gmok := g.SetMaskValue(n)
		wm, wmok := w.SetMaskValue(n)
		if gm != wm || gmok != wmok {
			t.Fatalf("%s SetMaskValue(%s) differs", label, n)
		}
		gs, gsok := g.StringValue(n)
		ws, wsok := w.StringValue(n)
		if gs != ws || gsok != wsok {
			t.Fatalf("%s StringValue(%s) = (%q,%v), want (%q,%v)", label, n, gs, gsok, ws, wsok)
		}
	}
	if !reflect.DeepEqual(g.AllValues(), w.AllValues()) {
		t.Fatalf("%s AllValues differs:\n got  %v\n want %v", label, g.AllValues(), w.AllValues())
	}
}

// joinShapeOrders writes the sorted fixture and its scattered copy into
// one hermetic filesystem.
func joinShapeOrders(t *testing.T) (afero.Fs, *encoding.Schema, int, map[string]string) {
	t.Helper()
	fsys, path, schema, rows := loadJoinShapeFixture(t)
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "scattered.pulse", scatterJoinShapeCohort(data, schema, rows), 0o644); err != nil {
		t.Fatal(err)
	}
	return fsys, schema, rows, map[string]string{"sorted": path, "scattered": "scattered.pulse"}
}

// baselinePlan builds the plan the iterator installs for keep.
func baselinePlan(t *testing.T, schema *encoding.Schema, keep encx.FieldFilter) *encx.DecodePlan {
	t.Helper()
	if keep == nil {
		return nil
	}
	plan, err := encx.BuildDecodePlan(schema, retainedFromFilter(schema, keep))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// TestJoinShapeRunSkip_MatchesFullRepopulate: sorted and scattered, full
// and projected (plan path), two passes through the iterator's Reset —
// the reuse record equals a full repopulate on every row.
func TestJoinShapeRunSkip_MatchesFullRepopulate(t *testing.T) {
	fsys, schema, rows, paths := joinShapeOrders(t)
	for _, order := range []string{"sorted", "scattered"} {
		for _, tc := range []struct {
			name string
			keep encx.FieldFilter
			n    int
		}{{name: "full"}, {name: "projected4", keep: joinShapeKeep4, n: 4}} {
			t.Run(order+"/"+tc.name, func(t *testing.T) {
				path := paths[order]
				plan := baselinePlan(t, schema, tc.keep)
				it := newStreamingIterator(fsys, path, schema)
				defer it.Close()
				it.SetReuse(true)
				if tc.keep != nil {
					it.SetProjection(tc.keep, tc.n)
				}
				for pass := range 2 {
					rr, release, err := openLegacyReader(fsys, path, schema)
					if err != nil {
						t.Fatal(err)
					}
					base := newNoSkipRecord(schema)
					n := 0
					for it.Next() {
						if err := base.read(rr, tc.keep, plan); err != nil {
							t.Fatalf("baseline row %d: %v", n, err)
						}
						assertSameRecord(t, fmt.Sprintf("pass %d row %d", pass, n), schema, it.Record(), base.record())
						n++
					}
					release()
					if it.Err() != nil || n != rows {
						t.Fatalf("pass %d: %d rows, err %v; want %d", pass, n, it.Err(), rows)
					}
					it.Reset()
				}
			})
		}
	}
}

// TestJoinShapeRunSkip_ShardBoundaries: the shard iterator keeps one
// reuse record across shards and opens a new reader per shard. Three
// cut sets:
//
//   - sorted/mid-run: shards cut INSIDE a parent's run, so the last row
//     of one shard and the first of the next share their parent block;
//   - sorted/parent-boundary: shards cut exactly where the parent key
//     changes, so shard N+1's first row differs from shard N's last in
//     the parent block as well (asserted on the bytes, so a fixture
//     change that moves the parent boundaries fails loudly);
//   - scattered: the permuted copy, where adjacent rows share little.
//
// Every case must equal a full repopulate of the unsplit cohort, row by
// row, over two passes through Reset.
func TestJoinShapeRunSkip_ShardBoundaries(t *testing.T) {
	fsys, schema, rows, paths := joinShapeOrders(t)
	stride := schema.RecordByteSize()
	keyOf := func(payload []byte, row int) []byte {
		f := schema.Fields[0] // p_key
		return payload[row*stride+f.ByteOffset : row*stride+f.ByteOffset+f.Type.ByteSize()]
	}
	for _, cc := range []struct {
		name  string
		order string
		cuts  []int
		// parentChanges: every interior cut must fall where the parent
		// key changes; otherwise every interior cut must fall inside a
		// parent's run.
		parentChanges bool
	}{
		{name: "sorted/mid-run", order: "sorted", cuts: []int{0, 5, 131, 257, rows}},
		// Parents 0 and 1 hold 12 and 13 rows; parents 0-15 hold 200.
		{name: "sorted/parent-boundary", order: "sorted", cuts: []int{0, 12, 25, 200, rows}, parentChanges: true},
		{name: "scattered", order: "scattered", cuts: []int{0, 5, 131, 250, rows}, parentChanges: true},
	} {
		data, err := afero.ReadFile(fsys, paths[cc.order])
		if err != nil {
			t.Fatal(err)
		}
		prefix := data[:len(data)-rows*stride]
		payload := data[len(prefix):]
		for _, c := range cc.cuts[1 : len(cc.cuts)-1] {
			last, first := payload[(c-1)*stride:c*stride], payload[c*stride:(c+1)*stride]
			if bytes.Equal(last, first) {
				t.Fatalf("%s: rows %d and %d are identical; the boundary tests nothing", cc.name, c-1, c)
			}
			if changed := !bytes.Equal(keyOf(payload, c-1), keyOf(payload, c)); changed != cc.parentChanges {
				t.Fatalf("%s: cut %d parent-key change = %v, want %v", cc.name, c, changed, cc.parentChanges)
			}
		}

		var doc bytes.Buffer
		if err := encx.WriteSchemaDoc(&doc, schema, uint64(rows), uint16(len(cc.cuts)-1)); err != nil {
			t.Fatal(err)
		}
		var arch bytes.Buffer
		zw := zip.NewWriter(&arch)
		put := func(name string, b []byte) {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(b); err != nil {
				t.Fatal(err)
			}
		}
		put(encx.ReservedSchemaName, doc.Bytes())
		var entries []ShardEntry
		for s := range len(cc.cuts) - 1 {
			name := fmt.Sprintf("s%d.pulse", s)
			shard := append(append([]byte(nil), prefix...), payload[cc.cuts[s]*stride:cc.cuts[s+1]*stride]...)
			put(name, shard)
			entries = append(entries, ShardEntry{Filename: name, RecordCount: int64(cc.cuts[s+1] - cc.cuts[s])})
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "arch.pulse", arch.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			name string
			keep encx.FieldFilter
			n    int
		}{{name: "full"}, {name: "projected4", keep: joinShapeKeep4, n: 4}} {
			t.Run(cc.name+"/"+tc.name, func(t *testing.T) {
				plan := baselinePlan(t, schema, tc.keep)
				it := newShardIter(cfg.Fs(), "arch.pulse", schema, entries)
				defer it.Close()
				it.SetReuse(true)
				if tc.keep != nil {
					it.SetProjection(tc.keep, tc.n)
				}
				for pass := range 2 {
					rr, release, err := openLegacyReader(fsys, paths[cc.order], schema)
					if err != nil {
						t.Fatal(err)
					}
					base := newNoSkipRecord(schema)
					n := 0
					for it.Next() {
						if err := base.read(rr, tc.keep, plan); err != nil {
							t.Fatal(err)
						}
						assertSameRecord(t, fmt.Sprintf("pass %d row %d", pass, n), schema, it.Record(), base.record())
						n++
					}
					release()
					if it.Err() != nil || n != rows {
						t.Fatalf("pass %d: %d rows, err %v; want %d", pass, n, it.Err(), rows)
					}
					it.Reset()
				}
			})
		}
	}
}

// TestJoinShapeRunSkip_ContinuationShape pins the fixture property the
// optimisation (and E2's measurements) depend on: sorted adjacent rows
// repeat most field bytes, the scattered copy few.
func TestJoinShapeRunSkip_ContinuationShape(t *testing.T) {
	fsys, path, schema, rows := loadJoinShapeFixture(t)
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	sorted := joinShapeContinuation(data, schema, rows)
	scattered := joinShapeContinuation(scatterJoinShapeCohort(data, schema, rows), schema, rows)
	t.Logf("field continuation: sorted %.3f, scattered %.3f", sorted, scattered)
	// MEASURED (400-row committed fixture): sorted 0.710, scattered 0.208.
	if sorted < 0.65 || scattered > 0.30 {
		t.Fatalf("fixture continuation sorted %.3f / scattered %.3f: expected a sorted parent block and a scattered copy", sorted, scattered)
	}
}
