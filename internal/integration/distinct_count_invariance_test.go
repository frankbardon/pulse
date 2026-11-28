package integration_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// distinct_count_invariance_test.go is the AGG_DISTINCT_COUNT family
// (#723: the inline value set promoted to a map past distinctInlineMax)
// on the shared worker-invariance harness. Over an f64 column whose
// per-group value pools straddle the inline threshold (1 to 40 distinct
// values a fine group, hundreds a coarse one) and carry +0 beside -0, a
// quiet NaN, a sign-set NaN and +Inf, the grouped count and its
// Components are the same bits under
//
//   - archive (fast tier): the streaming single file ≡ the forced
//     buffered arm ≡ an unevenly split 3-shard archive serial and under
//     ShardWorkers 2/3/8 and DecodeWorkers 7;
//   - decode (heavy tier, distinct_count_invariance_heavy_test.go):
//     DecodeWorkers 2/3/7/8 over a file above the parallel-decode
//     threshold ≡ serial;
//
// and the reference equals the map[float64]struct{} identity the set
// replaced (±0 one value, every NaN row one more), group by group.

const (
	distinctInvFine     = 61
	distinctInvCoarse   = 3
	distinctInvRows     = 3_000
	distinctInvBigRows  = limits.ParallelDecodeRecordThreshold + 4096
	distinctInvPoolSpan = 40
)

func distinctInvSchema() *encoding.Schema {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeU32},
		{Name: "h", Type: encoding.FieldTypeU32},
		{Name: "v", Type: encoding.FieldTypeF64, Nullable: true},
	}}
	off := 0
	for i := range s.Fields {
		s.Fields[i].ByteOffset = off
		s.Fields[i].CsvColumnIdx = i
		off += s.Fields[i].Type.ByteSize()
	}
	return s
}

// distinctInvValue is row r's v (ok=false: null). Fine group g draws
// from a pool of 1 + g%40 values: +0, -0, NaN, +Inf, a sign-set NaN,
// then distinct finite values.
func distinctInvValue(r int) (v float64, ok bool) {
	if r%17 == 5 {
		return 0, false
	}
	g := r % distinctInvFine
	switch idx := (r / distinctInvFine) % (1 + g%distinctInvPoolSpan); idx {
	case 0:
		return 0, true
	case 1:
		return math.Copysign(0, -1), true
	case 2:
		return math.NaN(), true
	case 3:
		return math.Inf(1), true
	case 4:
		return math.Float64frombits(0xfff8000000000000), true
	default:
		return float64(idx)*0.37 + float64(g), true
	}
}

// writeDistinctInvRows writes rows [from, to) as a cohort at path.
func writeDistinctInvRows(t testing.TB, fsys afero.Fs, path string, from, to int) {
	t.Helper()
	s := distinctInvSchema()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	stride, bm := s.RecordByteSize(), s.BitmapByteSize()
	for r := from; r < to; r++ {
		rec := make([]byte, stride)
		le := func(off int, x uint64, n int) {
			for i := range n {
				rec[off+i] = byte(x >> (8 * i))
			}
		}
		le(s.Fields[0].ByteOffset, uint64(r%distinctInvFine), 4)
		le(s.Fields[1].ByteOffset, uint64(r%distinctInvFine%distinctInvCoarse), 4)
		if v, ok := distinctInvValue(r); ok {
			le(s.Fields[2].ByteOffset, math.Float64bits(v), 8)
		} else {
			encoding.BitmapSetNull(rec[stride-bm:], 2)
		}
		buf = append(buf, rec...)
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// distinctInvOracle is the map identity's count per group key value.
func distinctInvOracle(rows int, key func(r int) int) map[int]int {
	sets := map[int]map[float64]struct{}{}
	for r := range rows {
		v, ok := distinctInvValue(r)
		if !ok {
			continue
		}
		k := key(r)
		if sets[k] == nil {
			sets[k] = map[float64]struct{}{}
		}
		sets[k][v] = struct{}{}
	}
	out := map[int]int{}
	for k, s := range sets {
		out[k] = len(s)
	}
	return out
}

// newDistinctInvFixture writes the twin and its uneven 3-shard archive
// in memory; heavy adds the decode-threshold file on disk.
func newDistinctInvFixture(t *testing.T, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	mem := afero.NewMemMapFs()
	writeDistinctInvRows(t, mem, "twin.pulse", 0, distinctInvRows)
	cuts := []int{0, distinctInvRows / 7, distinctInvRows * 3 / 5, distinctInvRows}
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeDistinctInvRows(t, mem, path, cuts[i], cuts[i+1])
	}
	p, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	out := map[invariance.Layout]invCohort{
		invariance.File:    {mem, "twin.pulse"},
		invariance.Archive: {mem, "archive.pulse"},
	}
	if heavy {
		dir := t.TempDir()
		writeDistinctInvRows(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), 0, distinctInvBigRows)
		out[invariance.BigFile] = invCohort{afero.NewBasePathFs(afero.NewOsFs(), dir), "large.pulse"}
	}
	return out
}

// distinctInvAnswer is the arm-invariant document.
type distinctInvAnswer struct {
	Data []map[string]any
	Aggs []types.AggregationComponents
}

// distinctInvFamilies: one family per grouping (fine pools straddle
// the threshold; coarse ones are all promoted).
func distinctInvFamilies(cohorts map[invariance.Layout]invCohort, groups []invariance.Group) []invariance.Family[distinctInvAnswer] {
	shapes := []struct {
		name, field string
		key         func(r int) int
	}{
		{"fine", "g", func(r int) int { return r % distinctInvFine }},
		{"coarse", "h", func(r int) int { return r % distinctInvFine % distinctInvCoarse }},
	}
	var out []invariance.Family[distinctInvAnswer]
	for _, sh := range shapes {
		out = append(out, invariance.Family[distinctInvAnswer]{
			Name:   sh.name,
			Groups: groups,
			Run: func(t *testing.T, k invariance.Knobs) invariance.Result[distinctInvAnswer] {
				co, ok := cohorts[k.Layout]
				if !ok {
					t.Fatalf("layout %s not built in this tier", k.Layout)
				}
				p, arm := invEngine(t, pulse.Options{}, co, k)
				req := &types.Request{
					Cohort:       &types.Cohort{Filename: co.path},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: sh.field}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "v", Label: "d"}},
				}
				if k.Mode == invariance.Buffered {
					req.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "g", Label: "g_pct"}}
				}
				resp := gcProcess(t, p, req)
				if resp.Components == nil {
					t.Fatalf("%s: no Components", k)
				}
				return invariance.Result[distinctInvAnswer]{Answer: distinctInvAnswer{Data: resp.Data, Aggs: resp.Components.Aggregations}, Arm: arm()}
			},
			Check: func(t *testing.T, k invariance.Knobs, ref distinctInvAnswer) {
				rows := distinctInvRows
				if k.Layout == invariance.BigFile {
					rows = distinctInvBigRows
				}
				want := distinctInvOracle(rows, sh.key)
				if len(ref.Data) != len(want) {
					t.Fatalf("%d groups, oracle %d", len(ref.Data), len(want))
				}
				var inline, promoted int
				for _, row := range ref.Data {
					d, ok := row["d"].(float64)
					if !ok {
						t.Fatalf("row %v: d is %T", row, row["d"])
					}
					key, err := strconv.Atoi(fmt.Sprint(row[sh.field]))
					if err != nil {
						t.Fatalf("row %v: group key %v", row, row[sh.field])
					}
					n, ok := want[key]
					if !ok || d != float64(n) {
						t.Fatalf("group %s=%d: %v distinct, map identity %d", sh.field, key, d, n)
					}
					if n <= 16 {
						inline++
					} else {
						promoted++
					}
				}
				if sh.name == "fine" && (inline == 0 || promoted == 0) {
					t.Fatalf("fine groups: %d inline, %d promoted: the fixture does not straddle the threshold", inline, promoted)
				}
			},
		})
	}
	return out
}

// TestDistinctCountInvariance_Archive is the fast-tier half: the archive
// group and the buffered arm against the streaming single file.
func TestDistinctCountInvariance_Archive(t *testing.T) {
	t.Parallel()

	archive := invariance.ArchiveGroup()
	archive.Ref.Arm = observe.ArmStreaming
	archive.Cells = append(archive.Cells, invariance.BufferedCell())
	for _, f := range distinctInvFamilies(newDistinctInvFixture(t, false), []invariance.Group{archive}) {
		invariance.Run(t, f)
	}
}
