package integration_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// u64_exact_key_test.go pins #427: a u64 key read by ATTR_*_COMPARE's
// key paths ({table, key_field} and the centroid `by`) and by
// ATTR_CODE_IN is the EXACT stored word, never its float64 echo. Two
// distinct u64 values above 2^53 that round to one float64 (2^53 and
// 2^53+1; MaxUint64 and MaxUint64-1) stay distinct keys, and a value
// above MaxInt64 never goes through an out-of-range float→uint
// conversion. Covered on streaming, buffered, parallel decode and a
// shard archive.

const (
	u64Lo     = uint64(1) << 53 // float64-exact
	u64LoNext = u64Lo + 1       // rounds to u64Lo
	u64Hi     = math.MaxUint64  // rounds to 2^64
	u64HiPrev = u64Hi - 1       // rounds to 2^64
	u64Small  = uint64(7)
)

var u64Keys = []uint64{u64Lo, u64LoNext, u64Hi, u64HiPrev, u64Small}

func u64KeySchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU64, ByteOffset: 0},
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 16},
	}}
}

// u64KeyRow is row r: its key and its (a, b) values. a depends on the
// key so two keys merged into one centroid move that centroid.
func u64KeyRow(r int) (k uint64, a, b float64) {
	ki := r % len(u64Keys)
	return u64Keys[ki], float64(ki*10) + float64(r%3), float64(r%11) - 5
}

func writeU64KeyCohort(t *testing.T, fsys afero.Fs, path string, from, to int) {
	t.Helper()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, u64KeySchema()); err != nil {
		t.Fatal(err)
	}
	rec := make([]byte, 24)
	for r := from; r < to; r++ {
		k, a, b := u64KeyRow(r)
		binary.LittleEndian.PutUint64(rec[0:], k)
		binary.LittleEndian.PutUint64(rec[8:], math.Float64bits(a))
		binary.LittleEndian.PutUint64(rec[16:], math.Float64bits(b))
		buf = append(buf, rec...)
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

type u64Mode struct {
	p    *pulse.Pulse
	path string
	rows int
}

func u64KeyModes(t *testing.T, ext pulse.Extensions) map[string]u64Mode {
	t.Helper()
	out := map[string]u64Mode{}
	fsys := afero.NewMemMapFs()
	writeU64KeyCohort(t, fsys, "c.pulse", 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	out["streaming"] = u64Mode{p, "c.pulse", paritySmallRows}
	out["buffered"] = out["streaming"]

	dir := t.TempDir()
	writeU64KeyCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), 0, parityLargeRows)
	lp, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 4, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	out["parallel_decode"] = u64Mode{lp, "large.pulse", parityLargeRows}

	sfs := afero.NewMemMapFs()
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeU64KeyCohort(t, sfs, path, i*40, (i+1)*40)
	}
	sp, err := pulse.New(pulse.Options{FS: sfs, ShardWorkers: 3, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	out["shard_archive"] = u64Mode{sp, "archive.pulse", 120}
	return out
}

func u64KeyRequest(m u64Mode, name string, attrs []*types.Attribute, aggs []*types.Aggregation) *types.Request {
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: m.path},
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"a", "b"}, Labels: []string{"a", "b"}}},
		Attributes:   attrs,
		Aggregations: aggs,
	}
	if name == "buffered" {
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "a", Label: "pct"})
	}
	return req
}

// TestU64ExactKey_KeyFieldReference: the table profiles 2^53 and
// MaxUint64 only, so the rows keyed 2^53+1, MaxUint64-1 and 7 must miss.
func TestU64ExactKey_KeyFieldReference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	profiles := map[string][]float64{
		strconv.FormatUint(u64Lo, 10): {1, 2},
		strconv.FormatUint(u64Hi, 10): {-3, 4},
	}
	ext := pulse.Extensions{ReferenceVectors: map[string]types.ReferenceVectorTable{
		"byk": {Labels: []string{"a", "b"}, Profiles: profiles},
	}}
	for name, m := range u64KeyModes(t, ext) {
		t.Run(name, func(t *testing.T) {
			req := u64KeyRequest(m, name, []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "d",
				Params: json.RawMessage(`{"metric":"manhattan","reference":{"table":"byk","key_field":"k"}}`)}},
				[]*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "sum_d"}})
			resp, err := m.p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			wantSum, missing := 0.0, 0
			for r := 0; r < m.rows; r++ {
				k, a, b := u64KeyRow(r)
				prof, ok := profiles[strconv.FormatUint(k, 10)]
				if !ok {
					missing++
					continue
				}
				wantSum += math.Abs(a-prof[0]) + math.Abs(b-prof[1])
			}
			if got, ok := vexprFloat(resp.Data[0]["sum_d"]); !ok || !vexprClose(got, wantSum) {
				t.Errorf("sum_d = %v, want %v", resp.Data[0]["sum_d"], wantSum)
			}
			var gotMissing any
			for _, e := range resp.Components.Attributes {
				if e.Label == "d" {
					gotMissing = e.Operator["n_reference_missing"]
				}
			}
			if gotMissing != missing {
				t.Errorf("n_reference_missing = %v, want %d (2^53+1, MaxUint64-1 and 7 have no profile)", gotMissing, missing)
			}
		})
	}
}

// TestU64ExactKey_CentroidBy: one centroid per EXACT key; 2^53 / 2^53+1
// and MaxUint64 / MaxUint64-1 are four centroids, not two.
func TestU64ExactKey_CentroidBy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for name, m := range u64KeyModes(t, pulse.Extensions{}) {
		t.Run(name, func(t *testing.T) {
			req := u64KeyRequest(m, name, []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "d",
				Params: json.RawMessage(`{"metric":"manhattan","reference":{"centroid":{"by":"k"}}}`)}},
				[]*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "sum_d"}})
			resp, err := m.p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			type acc struct{ a, b, n float64 }
			groups := map[uint64]*acc{}
			for r := 0; r < m.rows; r++ {
				k, a, b := u64KeyRow(r)
				g := groups[k]
				if g == nil {
					g = &acc{}
					groups[k] = g
				}
				g.a += a
				g.b += b
				g.n++
			}
			wantSum := 0.0
			for r := 0; r < m.rows; r++ {
				k, a, b := u64KeyRow(r)
				g := groups[k]
				wantSum += math.Abs(a-g.a/g.n) + math.Abs(b-g.b/g.n)
			}
			if got, ok := vexprFloat(resp.Data[0]["sum_d"]); !ok || !vexprClose(got, wantSum) {
				t.Errorf("sum_d = %v, want %v (one centroid per exact u64 key)", resp.Data[0]["sum_d"], wantSum)
			}
		})
	}
}

// TestU64ExactKey_CodeIn: a code above 2^53 matches its own value only.
func TestU64ExactKey_CodeIn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for name, m := range u64KeyModes(t, pulse.Extensions{}) {
		t.Run(name, func(t *testing.T) {
			req := u64KeyRequest(m, name, []*types.Attribute{
				{Type: types.ATTR_CODE_IN, Field: "k", Label: "lo_next",
					Params: json.RawMessage(`{"codes":["` + strconv.FormatUint(u64LoNext, 10) + `"]}`)},
				{Type: types.ATTR_CODE_IN, Field: "k", Label: "hi_prev",
					Params: json.RawMessage(`{"codes":[` + strconv.FormatUint(u64HiPrev, 10) + `]}`)},
				{Type: types.ATTR_CODE_IN, Field: "k", Label: "hi",
					Params: json.RawMessage(`{"codes":["` + strconv.FormatUint(u64Hi, 10) + `"]}`)},
			}, []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "lo_next", Label: "n_lo_next"},
				{Type: types.AGG_SUM, Field: "hi_prev", Label: "n_hi_prev"},
				{Type: types.AGG_SUM, Field: "hi", Label: "n_hi"},
			})
			req.Vectors = nil
			resp, err := m.p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			want := map[uint64]int{}
			for r := 0; r < m.rows; r++ {
				k, _, _ := u64KeyRow(r)
				want[k]++
			}
			for label, k := range map[string]uint64{"n_lo_next": u64LoNext, "n_hi_prev": u64HiPrev, "n_hi": u64Hi} {
				if got, ok := vexprFloat(resp.Data[0][label]); !ok || got != float64(want[k]) {
					t.Errorf("%s = %v, want %d (rows holding exactly %d)", label, resp.Data[0][label], want[k], k)
				}
			}
		})
	}
}

// TestU64ExactKey_JoinRightSide: a u64 on the join's buffered right side
// keeps its exact word through the build copy, so ATTR_CODE_IN on the
// joined column still tells 2^53+1 from 2^53.
func TestU64ExactKey_JoinRightSide(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	write := func(path string, s *encoding.Schema, row func(r int) []byte, n int) {
		t.Helper()
		var buf []byte
		w := &byteSink{b: &buf}
		if err := encoding.WriteHeader(w); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteSchema(w, s); err != nil {
			t.Fatal(err)
		}
		for r := 0; r < n; r++ {
			buf = append(buf, row(r)...)
		}
		if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const n = 40
	write("l.pulse", &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
	}}, func(r int) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(r)) }, n)
	write("r.pulse", &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "k", Type: encoding.FieldTypeU64, ByteOffset: 4},
	}}, func(r int) []byte {
		k, _, _ := u64KeyRow(r)
		return binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint32(nil, uint32(r)), k)
	}, n)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "l.pulse"},
		Joins:  []*types.JoinSpec{{Right: "r.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}, As: "r_"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_CODE_IN, Field: "r_k", Label: "hit",
			Params: json.RawMessage(`{"codes":["` + strconv.FormatUint(u64LoNext, 10) + `"]}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "hit", Label: "n"}},
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want := 0
	for r := 0; r < n; r++ {
		if k, _, _ := u64KeyRow(r); k == u64LoNext {
			want++
		}
	}
	if got, ok := vexprFloat(resp.Data[0]["n"]); !ok || got != float64(want) {
		t.Errorf("joined code_in hits = %v, want %d", resp.Data[0]["n"], want)
	}
}

// TestU64ExactKey_JoinOnKey pins #458: the join ON key of a u64 column
// is its EXACT stored word, so 2^53 / 2^53+1 and MaxUint64 /
// MaxUint64-1 (each pair one float64 echo) join to their own right row
// only. Every left-side iterator kind feeds joinKeyOf the exact word:
// the single-file streaming scan, the serial shard-archive scan, and an
// anchored shard. The right table holds one row per key with weight
// 10^i, so a collision adds a neighbour's power of ten to the sum.
func TestU64ExactKey_JoinOnKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeU64KeyCohort(t, fsys, "l.pulse", 0, 120)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeU64KeyCohort(t, fsys, path, i*40, (i+1)*40)
	}
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU64, ByteOffset: 0},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}); err != nil {
		t.Fatal(err)
	}
	weight := map[uint64]float64{}
	for i, k := range u64Keys {
		weight[k] = math.Pow(10, float64(i))
		buf = binary.LittleEndian.AppendUint64(buf, k)
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(weight[k]))
	}
	if err := afero.WriteFile(fsys, "r.pulse", buf, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(ctx, "la.pulse", shards); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		cohort   string
		from, to int
	}{
		{"streaming", "l.pulse", 0, 120},
		{"shard_archive", "la.pulse", 0, 120},
		{"anchored_shard", "la.pulse#s1.pulse", 40, 80},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: c.cohort},
				Joins:        []*types.JoinSpec{{Right: "r.pulse", On: []types.OnPair{{LeftField: "k", RightField: "k"}}, As: "r_"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "r_w", Label: "s"}, {Type: types.AGG_COUNT, Field: "k", Label: "n"}},
			}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			want := 0.0
			for r := c.from; r < c.to; r++ {
				k, _, _ := u64KeyRow(r)
				want += weight[k]
			}
			if got, ok := vexprFloat(resp.Data[0]["s"]); !ok || got != want {
				t.Errorf("joined weight sum = %v, want %v (a collision adds a neighbour's weight)", resp.Data[0]["s"], want)
			}
			if got, ok := vexprFloat(resp.Data[0]["n"]); !ok || got != float64(c.to-c.from) {
				t.Errorf("joined rows = %v, want %d (one right row per left row)", resp.Data[0]["n"], c.to-c.from)
			}
		})
	}
}
