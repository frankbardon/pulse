package linalg

// MergeBlockSize is the blocked-merge block length in records. A
// block-keyed reducer folds the row at ABSOLUTE record index r into
// block r / MergeBlockSize — never into "this worker's partial" — so the
// block sequence, and therefore MergeTree's result, does not depend on
// how the records were split across workers. The engine cuts its
// parallel-decode segments at multiples of MergeBlockSize so no block
// straddles two workers. The value is part of the bit contract: changing
// it changes merged bits.
const MergeBlockSize = 4096

// MergeTree folds a sequence of per-block accumulators, ordered by block
// index, in a FIXED binary tree and returns the root; no input is
// mutated, and a single block comes back as an independent copy.
//
// The tree is level-wise pairwise by position: blocks (0,1), (2,3), …
// merge left.Merge(right); an odd tail is carried up to the next level
// unmerged; repeat until one remains. Five blocks are therefore
// ((b0·b1)·(b2·b3))·b4. The shape is a pure function of the sequence
// length, and Merge is an FMA-free reference kernel, so the result is a
// pure function of the block sequence — bit-identical for any grouping
// of the same blocks into worker segments, on every architecture.
//
// Every slot must hold an accumulator (an empty one is fine — it is a
// valid block that saw no rows). An empty sequence, a nil block, or
// blocks differing in P or Mode is PULSE_MATRIX_SHAPE_MISMATCH.
func MergeTree(blocks []*CoMoment) (*CoMoment, error) {
	if len(blocks) == 0 {
		return nil, shapeError("merge tree needs at least one block", map[string]any{"blocks": 0})
	}
	for i, b := range blocks {
		if b == nil {
			return nil, shapeError("nil co-moment block in merge tree", map[string]any{"block": i})
		}
		if b.p != blocks[0].p || b.mode != blocks[0].mode {
			return nil, shapeError("co-moment blocks differ in dimension or mode",
				map[string]any{"block": i, "p": blocks[0].p, "other_p": b.p,
					"mode": blocks[0].mode.String(), "other_mode": b.mode.String()})
		}
	}
	// Level 0 reads the caller's blocks; every node built above it is
	// owned here, so only a level-0 left child is cloned before Merge
	// mutates it.
	level := blocks
	owned := false
	for len(level) > 1 {
		next := make([]*CoMoment, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			left := level[i]
			if !owned {
				left = left.Clone()
			}
			if i+1 < len(level) {
				if err := left.Merge(level[i+1]); err != nil {
					return nil, err
				}
			}
			next = append(next, left)
		}
		level = next
		owned = true
	}
	if !owned {
		return level[0].Clone(), nil
	}
	return level[0], nil
}
