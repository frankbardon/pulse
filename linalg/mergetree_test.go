package linalg_test

import (
	"math/rand/v2"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// blocksFromSegments folds rows into one CoMoment per MergeBlockSize
// block keyed by ABSOLUTE record index, the way a block-keyed reducer
// does: each segment [start, end) is walked independently, and the row
// at absolute index r lands in block r / MergeBlockSize. The returned
// sequence is ordered by block index.
func blocksFromSegments(t *testing.T, p int, mode linalg.CoMomentMode, rows []wrow, cuts []int) []*linalg.CoMoment {
	t.Helper()
	nBlocks := (len(rows) + linalg.MergeBlockSize - 1) / linalg.MergeBlockSize
	blocks := make([]*linalg.CoMoment, nBlocks)
	bounds := append(append([]int{0}, cuts...), len(rows))
	for s := 0; s+1 < len(bounds); s++ {
		for r := bounds[s]; r < bounds[s+1]; r++ {
			b := r / linalg.MergeBlockSize
			if blocks[b] == nil {
				blocks[b] = mustCoMoment(t, p, mode)
			}
			blocks[b].Add(rows[r].x, rows[r].w)
		}
	}
	return blocks
}

// TestMergeTreeGroupingInvariantBitwise: the merged result is a pure
// function of the block sequence — segments [0..3][4..9] and [0..6][7..9]
// (in blocks), a single serial segment, and a many-way split all give the
// same bits, including a ragged final block.
func TestMergeTreeGroupingInvariantBitwise(t *testing.T) {
	const blocks = 10
	B := linalg.MergeBlockSize
	r := rand.New(rand.NewPCG(11, 13))
	for _, mode := range bothModes {
		p := 3
		rows := randomRows(r, (blocks-1)*B+123, p)
		groupings := map[string][]int{
			"serial":        nil,
			"[0..3][4..9]":  {4 * B},
			"[0..6][7..9]":  {7 * B},
			"one per block": {B, 2 * B, 3 * B, 4 * B, 5 * B, 6 * B, 7 * B, 8 * B, 9 * B},
			"three uneven":  {2 * B, 3 * B},
		}
		var want []uint64
		for name, cuts := range groupings {
			got, err := linalg.MergeTree(blocksFromSegments(t, p, mode, rows, cuts))
			if err != nil {
				t.Fatalf("%s %s: %v", mode, name, err)
			}
			bits := coMomentBits(got)
			if want == nil {
				want = bits
				continue
			}
			if !sameWords(bits, want) {
				t.Fatalf("%s %s: bits differ from another grouping of the same block sequence", mode, name)
			}
		}
	}
}

// TestMergeTreeFixedShape pins the tree: level-wise pairwise by block
// index, an odd tail carried up unmerged. Five blocks are
// ((b0·b1)·(b2·b3))·b4 exactly, which a left fold b0·b1·b2·b3·b4 is not.
func TestMergeTreeFixedShape(t *testing.T) {
	r := rand.New(rand.NewPCG(17, 19))
	for _, mode := range bothModes {
		p := 3
		var bs []*linalg.CoMoment
		for i := 0; i < 5; i++ {
			bs = append(bs, feed(t, p, mode, randomRows(r, 50+r.IntN(200), p)))
		}
		b01 := bs[0].Clone()
		_ = b01.Merge(bs[1])
		b23 := bs[2].Clone()
		_ = b23.Merge(bs[3])
		_ = b01.Merge(b23)
		_ = b01.Merge(bs[4])

		got, err := linalg.MergeTree(bs)
		if err != nil {
			t.Fatal(err)
		}
		if !sameWords(coMomentBits(got), coMomentBits(b01)) {
			t.Fatalf("%s: MergeTree is not ((b0·b1)·(b2·b3))·b4", mode)
		}

		fold := bs[0].Clone()
		for _, b := range bs[1:] {
			_ = fold.Merge(b)
		}
		if sameWords(coMomentBits(fold), coMomentBits(got)) {
			t.Fatalf("%s: fixture too weak — a left fold matches the tree bit for bit", mode)
		}
		assertCoMomentClose(t, mode.String(), got, fold)
	}
}

// TestMergeTreeDoesNotMutateInputs: every block is read, never written,
// and a single block comes back as an independent copy.
func TestMergeTreeDoesNotMutateInputs(t *testing.T) {
	r := rand.New(rand.NewPCG(23, 29))
	for _, mode := range bothModes {
		var bs []*linalg.CoMoment
		var before [][]uint64
		for i := 0; i < 4; i++ {
			b := feed(t, 2, mode, randomRows(r, 100, 2))
			bs = append(bs, b)
			before = append(before, coMomentBits(b))
		}
		if _, err := linalg.MergeTree(bs); err != nil {
			t.Fatal(err)
		}
		for i, b := range bs {
			if !sameWords(coMomentBits(b), before[i]) {
				t.Fatalf("%s: block %d mutated by MergeTree", mode, i)
			}
		}

		one, err := linalg.MergeTree(bs[:1])
		if err != nil {
			t.Fatal(err)
		}
		if one == bs[0] {
			t.Fatalf("%s: single-block MergeTree aliases its input", mode)
		}
		if !sameWords(coMomentBits(one), before[0]) {
			t.Fatalf("%s: single-block MergeTree changed the figures", mode)
		}
		one.Add([]float64{1, 2}, 1)
		if !sameWords(coMomentBits(bs[0]), before[0]) {
			t.Fatalf("%s: single-block result shares state with its input", mode)
		}
	}
}

// TestMergeTreeErrors: an empty sequence, a nil block and a shape or
// mode mismatch are PULSE_MATRIX_SHAPE_MISMATCH.
func TestMergeTreeErrors(t *testing.T) {
	a := mustCoMoment(t, 2, linalg.Listwise)
	cases := map[string][]*linalg.CoMoment{
		"empty":         nil,
		"nil block":     {a, nil},
		"dimension":     {a, mustCoMoment(t, 3, linalg.Listwise)},
		"mode":          {a, mustCoMoment(t, 2, linalg.Pairwise)},
		"deep mismatch": {a, a, a, mustCoMoment(t, 1, linalg.Listwise), a},
	}
	for name, bs := range cases {
		got, err := linalg.MergeTree(bs)
		if err == nil {
			t.Fatalf("%s: want error, got %v", name, got)
		}
		if code := codeOf(t, err); code != perr.PULSE_MATRIX_SHAPE_MISMATCH {
			t.Fatalf("%s: code = %s", name, code)
		}
	}
}

func TestMergeBlockSize(t *testing.T) {
	if linalg.MergeBlockSize != 4096 {
		t.Fatalf("MergeBlockSize = %d, want 4096 (the blocked-merge contract)", linalg.MergeBlockSize)
	}
}
