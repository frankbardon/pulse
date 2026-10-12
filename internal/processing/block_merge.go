package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// block_merge.go is the engine side of the blocked merge: a reducer
// whose result must be BIT-identical for every worker count and every
// shard split opts in by implementing BlockMerger, and folds each row
// into the partial of the block the row's GLOBAL record ordinal names,
// never into "this worker's partial". Because the blocks, the order of
// the rows inside each block and the fixed tree that combines them are
// all functions of the ordinals alone, serial, any DecodeWorkers, any
// ShardWorkers count and any shard split of the same rows (a shard
// archive versus its single-file twin) produce the same bits.
//
// The global ordinal of a shard-archive row is its shard's start (the
// record counts of the shards before it, in archive order) plus its
// in-shard index. A shard boundary therefore can fall INSIDE a block:
// the block's rows then arrive as one FRAGMENT per shard, possibly on
// different workers. The fragment of the shard that holds the block's
// first ordinal (the head, BlockKey.Head) folds its rows as they arrive
// — exactly the prefix of the block's sequential fold; every other
// fragment (it starts mid-block, so it can be at most the first block
// of its shard and at most MergeBlockSize rows) buffers its rows, and
// the finalize replays them, in shard order, on top of the head. So a
// block's partial is always the one sequential fold of its rows in
// ordinal order, wherever the shard edges fell.
//
// Positions are stamped by the engine's record sources: the serial
// single-file iterator and the parallel-decode workers (shard 0, start
// 0; segments are cut at linalg.MergeBlockSize multiples), the serial
// shard-archive iterator and the per-shard reducer (the shard's archive
// index and start). A record no source stamped — one built by a join, a
// chain stage or a test harness — has no position, and a BlockMerger
// refuses it rather than guess.

// BlockKey addresses one merge fragment: Block is the GLOBAL record
// ordinal divided by linalg.MergeBlockSize, Shard the shard's index in
// archive order (0 for a single-file cohort) and Head whether that
// shard holds the block's first ordinal (always true on a single file).
// Fragments order by Block, then Shard — ordinal order.
type BlockKey struct {
	Block int
	Shard int
	Head  bool
}

func (k BlockKey) less(o BlockKey) bool {
	if k.Block != o.Block {
		return k.Block < o.Block
	}
	return k.Shard < o.Shard
}

const (
	posSetFlag  uint8 = 1 << iota // the record carries a merge position
	posHeadFlag                   // its shard holds the block's first ordinal
)

// SetMergePosition stamps the record's merge-block position: shard is
// the shard's archive index (0 for a single-file cohort), shardStart
// the shard's first GLOBAL record ordinal (the record counts of the
// shards before it; 0 for a single file) and record the row's absolute
// record index within that shard. A position the record cannot hold
// (negative, a shard past 65535 or a block past 2^32-1) leaves it
// unpositioned, which a BlockMerger refuses.
func (r *Record) SetMergePosition(shard, shardStart, record int) {
	global := shardStart + record
	block := global / linalg.MergeBlockSize
	if shard < 0 || shardStart < 0 || record < 0 || global < 0 || shard > math.MaxUint16 || block > math.MaxUint32 {
		r.posFlags = 0
		return
	}
	r.posFlags = posSetFlag
	if shardStart <= block*linalg.MergeBlockSize {
		r.posFlags |= posHeadFlag
	}
	r.posShard = uint16(shard)
	r.posBlock = uint32(block)
}

// MergeBlock returns the fragment SetMergePosition stamped, and false
// when the record was never stamped.
func (r *Record) MergeBlock() (BlockKey, bool) {
	if r.posFlags&posSetFlag == 0 {
		return BlockKey{}, false
	}
	return BlockKey{Block: int(r.posBlock), Shard: int(r.posShard), Head: r.posFlags&posHeadFlag != 0}, true
}

// blockRuns groups keys (sorted by less) into one run per Block and
// checks the fragment invariant: at most one head per block, and it is
// the block's first fragment (the lowest shard). Anything else means a
// source stamped inconsistent positions — PROCESSING_INTERNAL.
func blockRuns(keys []BlockKey) ([][]BlockKey, error) {
	var runs [][]BlockKey
	for start := 0; start < len(keys); {
		end := start + 1
		for end < len(keys) && keys[end].Block == keys[start].Block {
			end++
		}
		run := keys[start:end]
		for i, k := range run {
			if k.Head && i > 0 {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
					"block merge: a block's head fragment is not its first; record positions are inconsistent",
					map[string]any{"block": k.Block, "shard": k.Shard})
			}
		}
		runs = append(runs, run)
		start = end
	}
	return runs, nil
}

// BlockMerger is the opt-in for a reducer whose running state is a set
// of per-block linalg.CoMoment partials (BlockCoMoments). The parallel
// reducers (service mergeShardPartials, both the per-segment and the
// per-shard arm) fold a BlockMerger by absorbing the other partition's
// blocks — never through MergeableAggregator.MergeOnline — so the merge
// order of partitions cannot reach the bits. The reducer's Finalize
// combines its blocks through BlockCoMoments.Tree, the one fixed tree.
//
// A BlockMerger must still be declared mergeable (built-in Mergeable()
// or the extension Mergeable flag) for the parallel arms to engage;
// every other reducer keeps the MergeOnline path unchanged.
type BlockMerger interface {
	OnlineAggregator
	// BlockMoments returns the reducer's per-block state. The engine
	// absorbs another partition's state into it; it must be the same
	// value across calls.
	BlockMoments() *BlockCoMoments
}

// MergeBlockMerger folds src's per-block state into dst. src must be a
// BlockMerger too; its blocks move into dst (src must not be used
// afterwards).
func MergeBlockMerger(dst BlockMerger, src OnlineAggregator) error {
	other, ok := src.(BlockMerger)
	if !ok {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("block merge: partition state %T is not a BlockMerger", src))
	}
	return dst.BlockMoments().Absorb(other.BlockMoments())
}

// BlockCoMoments holds one fragment per (block, shard) that saw at
// least one row: a head fragment's linalg.CoMoment, or a non-head
// fragment's buffered rows. A block no row reached (empty, or wholly
// filtered out) has no entry — absence is itself a function of the
// data, so it does not disturb worker invariance.
type BlockCoMoments struct {
	p      int
	mode   linalg.CoMomentMode
	blocks map[BlockKey]*comFrag

	// cur caches the last fragment written, so a run of rows in one
	// fragment skips the map lookup.
	cur    *comFrag
	curKey BlockKey
}

// comFrag is one fragment: acc on a head fragment (rows folded as they
// arrive), the buffered rows (xs, p values per row, and ws) otherwise.
type comFrag struct {
	acc *linalg.CoMoment
	xs  []float64
	ws  []float64
}

// NewBlockCoMoments returns empty per-block state over p variables. An
// invalid p or mode is PULSE_MATRIX_SHAPE_MISMATCH (linalg.NewCoMoment).
func NewBlockCoMoments(p int, mode linalg.CoMomentMode) (*BlockCoMoments, error) {
	if _, err := linalg.NewCoMoment(p, mode); err != nil {
		return nil, err
	}
	return &BlockCoMoments{p: p, mode: mode, blocks: make(map[BlockKey]*comFrag)}, nil
}

// Add folds one row (x, w — linalg.CoMoment.Add semantics) into the
// fragment rec's merge position names. An unpositioned record is
// PROCESSING_INTERNAL: folding it anywhere would make the result depend
// on how the rows were split.
func (b *BlockCoMoments) Add(rec *Record, x []float64, w float64) error {
	key, ok := rec.MergeBlock()
	if !ok {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"block merge: record carries no merge-block position (its source does not stamp SetMergePosition)")
	}
	return b.AddAt(key, x, w)
}

// AddAt folds one row into the fragment key names. Add is AddAt at the
// record's stamped position; a caller that must reproduce a matrix
// slot's bits on rows of its own (TEST_BARTLETT_SPHERICITY) keys them
// the same way. A head fragment folds x now; any other fragment keeps a
// copy for the finalize to replay.
func (b *BlockCoMoments) AddAt(key BlockKey, x []float64, w float64) error {
	if b.cur == nil || key != b.curKey {
		frag, exists := b.blocks[key]
		if !exists {
			frag = &comFrag{}
			if key.Head {
				var err error
				if frag.acc, err = linalg.NewCoMoment(b.p, b.mode); err != nil {
					return err
				}
			}
			b.blocks[key] = frag
		}
		b.cur, b.curKey = frag, key
	}
	if b.cur.acc != nil {
		b.cur.acc.Add(x, w)
		return nil
	}
	if len(x) != b.p {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SHAPE_MISMATCH,
			"block merge: row length differs from the dimension",
			map[string]any{"p": b.p, "row": len(x)})
	}
	b.cur.xs = append(b.cur.xs, x...)
	b.cur.ws = append(b.cur.ws, w)
	return nil
}

// Absorb moves o's fragments into b. A shard is never split across
// partitions and parallel-decode segments are cut at block boundaries,
// so a key present on both sides means one fragment reached two
// partitions — the invariant the bits rest on — and is
// PROCESSING_INTERNAL. A shape or mode mismatch is
// PULSE_MATRIX_SHAPE_MISMATCH.
func (b *BlockCoMoments) Absorb(o *BlockCoMoments) error {
	if o == nil || o == b {
		return nil
	}
	if o.p != b.p || o.mode != b.mode {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SHAPE_MISMATCH,
			"block merge: partitions differ in dimension or mode",
			map[string]any{"p": b.p, "other_p": o.p, "mode": b.mode.String(), "other_mode": o.mode.String()})
	}
	for k, frag := range o.blocks {
		if _, dup := b.blocks[k]; dup {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"block merge: one merge block reached two partitions",
				map[string]any{"shard": k.Shard, "block": k.Block})
		}
		b.blocks[k] = frag
	}
	o.blocks = map[BlockKey]*comFrag{}
	o.cur = nil
	return nil
}

// Len returns the number of fragments holding rows (one per block on a
// single-file cohort).
func (b *BlockCoMoments) Len() int { return len(b.blocks) }

// Tree combines the fragments in the one fixed shape. Each block's
// partial is its head fragment's accumulator (or an empty one) with the
// block's other fragments replayed on top in shard order — the
// sequential fold of the block's rows in ordinal order; then
// linalg.MergeTree runs over the block partials in block order. The
// result is a function of the rows' global ordinals alone, so a shard
// archive equals its single-file twin bit for bit, whatever the split.
// No fragment is mutated. With no blocks the result is an empty
// accumulator.
func (b *BlockCoMoments) Tree() (*linalg.CoMoment, error) {
	if len(b.blocks) == 0 {
		return linalg.NewCoMoment(b.p, b.mode)
	}
	keys := make([]BlockKey, 0, len(b.blocks))
	for k := range b.blocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	runs, err := blockRuns(keys)
	if err != nil {
		return nil, err
	}
	seq := make([]*linalg.CoMoment, 0, len(runs))
	for _, run := range runs {
		head := b.blocks[run[0]]
		if len(run) == 1 && head.acc != nil {
			seq = append(seq, head.acc)
			continue
		}
		var acc *linalg.CoMoment
		if head.acc != nil {
			acc = head.acc.Clone()
		} else if acc, err = linalg.NewCoMoment(b.p, b.mode); err != nil {
			return nil, err
		}
		for _, k := range run {
			frag := b.blocks[k]
			for i, w := range frag.ws {
				acc.Add(frag.xs[i*b.p:(i+1)*b.p], w)
			}
		}
		seq = append(seq, acc)
	}
	return linalg.MergeTree(seq)
}
