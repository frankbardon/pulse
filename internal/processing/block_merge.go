package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// block_merge.go is the engine side of the blocked merge: a reducer
// whose result must be BIT-identical for every worker count opts in by
// implementing BlockMerger, and folds each row into the partial of the
// block the row's ABSOLUTE position names, never into "this worker's
// partial". Because a block's rows always arrive in record order on one
// goroutine (parallel-decode segments are cut at linalg.MergeBlockSize
// multiples; a shard is never split), the set of block partials is a
// pure function of the data — and so is the fixed tree that combines
// them. Serial, any DecodeWorkers and any ShardWorkers count therefore
// produce the same bits.
//
// Positions are stamped by the engine's record sources: the serial
// single-file iterator (shard 0), the serial shard-archive iterator and
// the per-shard reducer (the shard's index in archive order, record
// index restarting at 0 per shard), and the parallel-decode workers
// (segment start + offset). A record no source stamped — one built by
// a join, a chain stage or a test harness — has no position, and a
// BlockMerger refuses it rather than guess.

// BlockKey addresses one merge block: Shard is the shard's index in
// archive order (0 for a single-file cohort) and Block the shard-local
// ABSOLUTE record index divided by linalg.MergeBlockSize.
type BlockKey struct {
	Shard int
	Block int
}

func (k BlockKey) less(o BlockKey) bool {
	if k.Shard != o.Shard {
		return k.Shard < o.Shard
	}
	return k.Block < o.Block
}

// SetMergePosition stamps the record's merge-block position: shard is
// the shard's archive index (0 for a single-file cohort), record the
// row's absolute record index within that shard. A position the record
// cannot hold (negative, a shard past 65535 or a block past 2^32-1)
// leaves it unpositioned, which a BlockMerger refuses.
func (r *Record) SetMergePosition(shard, record int) {
	block := record / linalg.MergeBlockSize
	if shard < 0 || record < 0 || shard > math.MaxUint16 || block > math.MaxUint32 {
		r.posSet = false
		return
	}
	r.posSet = true
	r.posShard = uint16(shard)
	r.posBlock = uint32(block)
}

// MergeBlock returns the block SetMergePosition stamped, and false when
// the record was never stamped.
func (r *Record) MergeBlock() (BlockKey, bool) {
	if !r.posSet {
		return BlockKey{}, false
	}
	return BlockKey{Shard: int(r.posShard), Block: int(r.posBlock)}, true
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

// BlockCoMoments holds one linalg.CoMoment per merge block that saw at
// least one row. A block no row reached (empty, or wholly filtered out)
// has no entry — absence is itself a function of the data, so it does
// not disturb worker invariance.
type BlockCoMoments struct {
	p      int
	mode   linalg.CoMomentMode
	blocks map[BlockKey]*linalg.CoMoment

	// cur caches the last block written, so a run of rows in one block
	// skips the map lookup.
	cur    *linalg.CoMoment
	curKey BlockKey
}

// NewBlockCoMoments returns empty per-block state over p variables. An
// invalid p or mode is PULSE_MATRIX_SHAPE_MISMATCH (linalg.NewCoMoment).
func NewBlockCoMoments(p int, mode linalg.CoMomentMode) (*BlockCoMoments, error) {
	if _, err := linalg.NewCoMoment(p, mode); err != nil {
		return nil, err
	}
	return &BlockCoMoments{p: p, mode: mode, blocks: make(map[BlockKey]*linalg.CoMoment)}, nil
}

// Add folds one row (x, w — linalg.CoMoment.Add semantics) into the
// block rec's merge position names. An unpositioned record is
// PROCESSING_INTERNAL: folding it anywhere would make the result depend
// on how the rows were split.
func (b *BlockCoMoments) Add(rec *Record, x []float64, w float64) error {
	key, ok := rec.MergeBlock()
	if !ok {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"block merge: record carries no merge-block position (its source does not stamp SetMergePosition)")
	}
	if b.cur == nil || key != b.curKey {
		acc, exists := b.blocks[key]
		if !exists {
			var err error
			if acc, err = linalg.NewCoMoment(b.p, b.mode); err != nil {
				return err
			}
			b.blocks[key] = acc
		}
		b.cur, b.curKey = acc, key
	}
	b.cur.Add(x, w)
	return nil
}

// Absorb moves o's blocks into b. Partitions cover disjoint record
// ranges cut at block boundaries, so a key present on both sides means
// a block was split across partitions — the invariant the bits rest on
// — and is PROCESSING_INTERNAL. A shape or mode mismatch is
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
	for k, acc := range o.blocks {
		if _, dup := b.blocks[k]; dup {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"block merge: one merge block reached two partitions",
				map[string]any{"shard": k.Shard, "block": k.Block})
		}
		b.blocks[k] = acc
	}
	o.blocks = map[BlockKey]*linalg.CoMoment{}
	o.cur = nil
	return nil
}

// Len returns the number of blocks holding rows.
func (b *BlockCoMoments) Len() int { return len(b.blocks) }

// Tree combines the blocks in the one fixed shape: within each shard,
// linalg.MergeTree over that shard's blocks in block order; then
// linalg.MergeTree over the per-shard roots in shard order. A shard's
// subtree is therefore a function of that shard's rows alone, and a
// one-shard archive reproduces its single-file twin bit for bit; a
// multi-shard archive and the single file holding the same rows cut
// blocks at different places and may differ in the last bits. No block
// is mutated. With no blocks the result is an empty accumulator.
func (b *BlockCoMoments) Tree() (*linalg.CoMoment, error) {
	if len(b.blocks) == 0 {
		return linalg.NewCoMoment(b.p, b.mode)
	}
	keys := make([]BlockKey, 0, len(b.blocks))
	for k := range b.blocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })

	var roots []*linalg.CoMoment
	for start := 0; start < len(keys); {
		end := start
		seq := make([]*linalg.CoMoment, 0, 8)
		for end < len(keys) && keys[end].Shard == keys[start].Shard {
			seq = append(seq, b.blocks[keys[end]])
			end++
		}
		root, err := linalg.MergeTree(seq)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
		start = end
	}
	return linalg.MergeTree(roots)
}
