package processing

import (
	"math"
	"math/rand/v2"
	"testing"
	"unsafe"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

func blockTestSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
}

// The merge position packs into the padding after groupIdxOK; growing
// the Record moves every buffered record into the next size class.
func TestRecordMergePosition_FitsPadding(t *testing.T) {
	if got := unsafe.Sizeof(Record{}); got != 96 {
		t.Fatalf("unsafe.Sizeof(Record{}) = %d, want 96 — the merge position must ride the padding", got)
	}
}

func TestRecordMergePosition(t *testing.T) {
	B := linalg.MergeBlockSize
	cases := []struct {
		name                 string
		shard, start, record int
		want                 BlockKey
		ok                   bool
	}{
		{"first row", 0, 0, 0, BlockKey{Block: 0, Shard: 0, Head: true}, true},
		{"last row of block 0", 0, 0, B - 1, BlockKey{Block: 0, Shard: 0, Head: true}, true},
		{"first row of block 1", 0, 0, B, BlockKey{Block: 1, Shard: 0, Head: true}, true},
		{"shard and block", 7, 0, 5*B + 3, BlockKey{Block: 5, Shard: 7, Head: true}, true},
		{"global ordinal", 3, 2*B + 10, B - 11, BlockKey{Block: 2, Shard: 3, Head: false}, true},
		{"shard start on the block edge", 3, 2 * B, 0, BlockKey{Block: 2, Shard: 3, Head: true}, true},
		{"next block of a mid-block shard", 3, 2*B + 10, B - 10, BlockKey{Block: 3, Shard: 3, Head: true}, true},
		{"max shard", math.MaxUint16, 0, 0, BlockKey{Block: 0, Shard: math.MaxUint16, Head: true}, true},
		{"shard overflow", math.MaxUint16 + 1, 0, 0, BlockKey{}, false},
		{"negative record", 0, 0, -1, BlockKey{}, false},
		{"negative start", 1, -1, 0, BlockKey{}, false},
		{"negative shard", -1, 0, 0, BlockKey{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReusableRecord(blockTestSchema())
			if _, ok := r.MergeBlock(); ok {
				t.Fatal("a fresh record reports a merge position")
			}
			r.SetMergePosition(2, 0, 9) // a stale position must not survive a bad stamp
			r.SetMergePosition(tc.shard, tc.start, tc.record)
			got, ok := r.MergeBlock()
			if ok != tc.ok || got != tc.want {
				t.Fatalf("MergeBlock() = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

type posRow struct {
	shard, start, record int
	x                    []float64
	w                    float64
}

func blockRows(r *rand.Rand, shards []int, p int) []posRow {
	var out []posRow
	start := 0
	for s, n := range shards {
		for i := 0; i < n; i++ {
			x := make([]float64, p)
			for j := range x {
				x[j] = r.NormFloat64()*3 + float64(j)
				if r.IntN(9) == 0 {
					x[j] = math.NaN()
				}
			}
			out = append(out, posRow{shard: s, start: start, record: i, x: x, w: 0.25 + r.Float64()})
		}
		start += n
	}
	return out
}

func coMomentWords(c *linalg.CoMoment) []uint64 {
	out := []uint64{uint64(c.N()), uint64(c.NWeightInvalid()), math.Float64bits(c.W())}
	m := c.Mean()
	cov := c.Cov(0)
	for i := 0; i < c.P(); i++ {
		out = append(out, math.Float64bits(m.At(i)))
		for j := i; j < c.P(); j++ {
			out = append(out, uint64(c.PairN(i, j)), math.Float64bits(c.PairW(i, j)),
				math.Float64bits(c.PairW2(i, j)), math.Float64bits(cov.At(i, j)))
		}
	}
	return out
}

func equalWords(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// foldPartitioned splits rows at the cut points (indexes into rows),
// folds each partition into its own BlockCoMoments, then absorbs the
// partitions in the given order.
func foldPartitioned(t *testing.T, rows []posRow, p int, mode linalg.CoMomentMode, cuts []int, order []int) *linalg.CoMoment {
	t.Helper()
	bounds := append(append([]int{0}, cuts...), len(rows))
	parts := make([]*BlockCoMoments, len(bounds)-1)
	for i := range parts {
		b, err := NewBlockCoMoments(p, mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows[bounds[i]:bounds[i+1]] {
			rec := NewReusableRecord(blockTestSchema())
			rec.SetMergePosition(row.shard, row.start, row.record)
			if err := b.Add(rec, row.x, row.w); err != nil {
				t.Fatal(err)
			}
		}
		parts[i] = b
	}
	merged := parts[order[0]]
	for _, i := range order[1:] {
		if err := merged.Absorb(parts[i]); err != nil {
			t.Fatal(err)
		}
	}
	root, err := merged.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// Absorb order and partition boundaries (at global block multiples or
// shard edges) never reach the bits, and Tree is the single file's
// tree: one sequential fold per GLOBAL block in ordinal order — shard
// edges fall inside blocks here — then MergeTree over the blocks.
// Falsified by keying blocks per shard (ignoring the shard start).
func TestBlockCoMoments_PartitionAndOrderInvariant(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 12))
	const p = 3
	B := linalg.MergeBlockSize
	shards := []int{3*B + 17, B, 5, 1, 0, 2 * B}
	rows := blockRows(r, shards, p)
	shardStart := []int{0, 3*B + 17, 4*B + 17, 4*B + 22, 4*B + 23, 4*B + 23}

	for _, mode := range []linalg.CoMomentMode{linalg.Listwise, linalg.Pairwise} {
		want := foldPartitioned(t, rows, p, mode, nil, []int{0})

		// The single-file reference: Add every row of a global block in
		// ordinal order, then MergeTree over the blocks.
		var blocks []*linalg.CoMoment
		for b := 0; b*B < len(rows); b++ {
			acc, _ := linalg.NewCoMoment(p, mode)
			for i := b * B; i < len(rows) && i < (b+1)*B; i++ {
				acc.Add(rows[i].x, rows[i].w)
			}
			blocks = append(blocks, acc)
		}
		ref, err := linalg.MergeTree(blocks)
		if err != nil {
			t.Fatal(err)
		}
		if !equalWords(coMomentWords(want), coMomentWords(ref)) {
			t.Fatalf("%v: Tree is not the single file's global-block MergeTree", mode)
		}

		splits := []struct {
			cuts, order []int
		}{
			{[]int{B, 2 * B, 3 * B}, []int{3, 1, 0, 2}},
			{[]int{shardStart[1], shardStart[2], shardStart[3], shardStart[5]}, []int{2, 0, 4, 3, 1}},
			{[]int{2 * B, 5 * B}, []int{2, 1, 0}},
		}
		for _, sp := range splits {
			got := foldPartitioned(t, rows, p, mode, sp.cuts, sp.order)
			if !equalWords(coMomentWords(got), coMomentWords(want)) {
				t.Fatalf("%v: cuts %v absorbed in order %v changed the bits", mode, sp.cuts, sp.order)
			}
		}
	}
}

// A block's head fragment must be its first: a head behind a non-head
// fragment means inconsistent stamps, refused PROCESSING_INTERNAL.
func TestBlockCoMoments_HeadNotFirstRefused(t *testing.T) {
	b, _ := NewBlockCoMoments(1, linalg.Listwise)
	if err := b.AddAt(BlockKey{Block: 0, Shard: 0, Head: false}, []float64{1}, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAt(BlockKey{Block: 0, Shard: 1, Head: true}, []float64{2}, 1); err != nil {
		t.Fatal(err)
	}
	_, err := b.Tree()
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("head behind a fragment: err = %v, want PROCESSING_INTERNAL", err)
	}
	if err := b.AddAt(BlockKey{Block: 1, Shard: 0}, []float64{1, 2}, 1); err == nil {
		t.Fatal("a buffered row of the wrong length was accepted")
	}
}

func TestBlockCoMoments_Refusals(t *testing.T) {
	b, err := NewBlockCoMoments(2, linalg.Listwise)
	if err != nil {
		t.Fatal(err)
	}
	rec := NewReusableRecord(blockTestSchema())
	err = b.Add(rec, []float64{1, 2}, 1)
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("unpositioned row: err = %v, want PROCESSING_INTERNAL", err)
	}

	rec.SetMergePosition(0, 0, 10)
	if err := b.Add(rec, []float64{1, 2}, 1); err != nil {
		t.Fatal(err)
	}
	split, _ := NewBlockCoMoments(2, linalg.Listwise)
	rec.SetMergePosition(0, 0, 20) // same block 0 on another partition
	if err := split.Add(rec, []float64{3, 4}, 1); err != nil {
		t.Fatal(err)
	}
	err = b.Absorb(split)
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("split block: err = %v, want PROCESSING_INTERNAL", err)
	}

	other, _ := NewBlockCoMoments(3, linalg.Listwise)
	err = b.Absorb(other)
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatalf("shape mismatch: err = %v, want PULSE_MATRIX_SHAPE_MISMATCH", err)
	}
	if _, err := NewBlockCoMoments(-1, linalg.Listwise); err == nil {
		t.Fatal("negative p accepted")
	}
	if err := MergeBlockMerger(nil, nil); err == nil {
		t.Fatal("non-BlockMerger partition accepted")
	}
}

func TestBlockCoMoments_EmptyTree(t *testing.T) {
	b, _ := NewBlockCoMoments(2, linalg.Pairwise)
	root, err := b.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if root.N() != 0 || root.P() != 2 || root.Mode() != linalg.Pairwise || b.Len() != 0 {
		t.Fatalf("empty tree: N=%d P=%d mode=%v len=%d", root.N(), root.P(), root.Mode(), b.Len())
	}
}
