package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Vector aggregators — AGG_VEC_MEAN and AGG_VEC_SUM (U27 E2-S2). Each
// reads a vector input (the slot's Fields after Processor.prepare's
// rewrite, vectors.AggregationSlotRewrite: the literal members, with
// the display labels in params.labels) and folds one figure PER
// MEMBER:
//
//	AGG_VEC_MEAN  element i = Σw·x_i / Σw over the rows whose member i
//	              answered (NaN — null on the wire — when none did)
//	AGG_VEC_SUM   element i = Σw·x_i over the same rows (0 when none did)
//
// Missing data is per ELEMENT: a null member skips that element of
// that row only; the other elements still count the row. A stored NaN
// is a value, not a null, and propagates into its element. On a
// weighted slot a row contributes only with a valid weight > 0 (the
// other aggregators' rule); element_n stays the RAW present count, as
// the floor's n does.
//
// Bit-exact merge (the linalg blocked-merge pattern). A row folds into
// the per-block partial its ABSOLUTE merge position names
// (Record.MergeBlock: shard, record / linalg.MergeBlockSize) — never
// into "this worker's partial" — and Finalize combines the blocks in
// one fixed binary tree (vecMergeTree, linalg.MergeTree's shape: within
// a shard by block order, then across shards). Partitions own disjoint
// blocks, so MergeOnline is a map union and the merge order of
// partitions cannot reach the bits: serial, every DecodeWorkers and
// every ShardWorkers count answer the same words. Products are written
// float64(w*x) so no FMA contraction differs by architecture. A record
// no source stamped (a joined or chain-stage row) folds into one
// trailing "loose" partial in arrival order; two partitions both
// holding loose rows cannot be ordered and refuse to merge
// (PROCESSING_INTERNAL), never guess.
//
// The scalar Aggregate / Finalize fallback is NaN: an array value has
// no single number, and every consumer that needs one refuses the slot
// (types.AggregationType.ArrayValued). The value rides Rich.

// vecBlock is one merge block's partial: per element Σw·x, Σw over
// contributing rows and the raw present count.
type vecBlock struct {
	sum []float64
	w   []float64
	n   []int
}

func newVecBlock(p int) *vecBlock {
	return &vecBlock{sum: make([]float64, p), w: make([]float64, p), n: make([]int, p)}
}

func (b *vecBlock) clone() *vecBlock {
	return &vecBlock{
		sum: append([]float64(nil), b.sum...),
		w:   append([]float64(nil), b.w...),
		n:   append([]int(nil), b.n...),
	}
}

// merge adds o into b element by element (plain adds: no product, no
// contraction).
func (b *vecBlock) merge(o *vecBlock) {
	for i := range b.sum {
		b.sum[i] += o.sum[i]
		b.w[i] += o.w[i]
		b.n[i] += o.n[i]
	}
}

// vecMergeTree folds blocks (in sequence order) level-wise pairwise by
// position — (0,1), (2,3), …, an odd tail carried up — exactly
// linalg.MergeTree's shape, so the root is a pure function of the
// sequence. No input is mutated.
func vecMergeTree(blocks []*vecBlock) *vecBlock {
	level := blocks
	owned := false
	for len(level) > 1 {
		next := make([]*vecBlock, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			left := level[i]
			if !owned {
				left = left.clone()
			}
			if i+1 < len(level) {
				left.merge(level[i+1])
			}
			next = append(next, left)
		}
		level = next
		owned = true
	}
	if !owned {
		return level[0].clone()
	}
	return level[0]
}

// vecAggregator backs AGG_VEC_MEAN and AGG_VEC_SUM.
type vecAggregator struct {
	typ     types.AggregationType
	members []string
	labels  []string
	// weight is the applied slot weight; nil on an unweighted slot.
	weight *types.WeightSpec

	blocks map[BlockKey]*vecBlock
	loose  *vecBlock // rows with no merge position, in arrival order
	cur    *vecBlock
	curKey BlockKey

	frozen     bool
	value      []float64
	frozenComp map[string]any
}

// newVecAggregator is the registry factory for both operators. The
// slot must be the prepared one: Fields set, Vector cleared. A slot
// still naming a Vector reached the factory without the request-level
// resolution (Processor.prepare / ResolveAggregationInputs) — an engine
// fault, refused loudly rather than read as an empty vector.
func newVecAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	name := string(agg.Type)
	if agg.Vector != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			name+": vector "+agg.Vector+" was not resolved to its members before the factory",
			map[string]any{"aggregation": name, "vector": agg.Vector})
	}
	if agg.Field != "" || len(agg.Fields) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			name+" reads its columns from vector or fields (exactly one), never field",
			map[string]any{"aggregation": name, "field": agg.Field})
	}
	p, cerr := vectors.DecodeVecAggParams("aggregation", agg.Type, agg.Params)
	if cerr != nil {
		return nil, cerr
	}
	members := append([]string(nil), agg.Fields...)
	if schema != nil {
		r, rerr := vectors.ResolveSpec("aggregation.fields", types.VectorSpec{Fields: agg.Fields, Labels: p.Labels}, schema)
		if rerr != nil {
			return nil, rerr
		}
		members, p.Labels = r.Members, r.Labels
	}
	labels := p.Labels
	if len(labels) != len(members) {
		labels = members
	}
	a := &vecAggregator{typ: agg.Type, members: members, labels: append([]string(nil), labels...)}
	if w := slotWeight(agg); w != nil {
		ew := effectiveWeight(w)
		a.weight = &ew
	}
	a.reset()
	return a, nil
}

func (a *vecAggregator) reset() {
	a.blocks = map[BlockKey]*vecBlock{}
	a.loose, a.cur = nil, nil
}

// blockFor returns the partial r's merge position names.
func (a *vecAggregator) blockFor(r *Record) *vecBlock {
	key, ok := r.MergeBlock()
	if !ok {
		if a.loose == nil {
			a.loose = newVecBlock(len(a.members))
		}
		return a.loose
	}
	if a.cur != nil && key == a.curKey {
		return a.cur
	}
	b, exists := a.blocks[key]
	if !exists {
		b = newVecBlock(len(a.members))
		a.blocks[key] = b
	}
	a.cur, a.curKey = b, key
	return b
}

// fold adds one row.
func (a *vecAggregator) fold(r *Record) {
	w, contributes := 1.0, true
	if a.weight != nil {
		w, contributes = validRowWeight(r, a.weight)
	}
	b := a.blockFor(r)
	for i, m := range a.members {
		x, ok := r.NumericValue(m)
		if !ok {
			continue
		}
		b.n[i]++
		if !contributes {
			continue
		}
		b.sum[i] += float64(w * x)
		b.w[i] += w
	}
}

// root combines every partial in the one fixed shape: within each
// shard vecMergeTree over its blocks in block order, then vecMergeTree
// over the shard roots in shard order, then the loose partial last.
func (a *vecAggregator) root() *vecBlock {
	p := len(a.members)
	keys := make([]BlockKey, 0, len(a.blocks))
	for k := range a.blocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	var roots []*vecBlock
	for start := 0; start < len(keys); {
		end := start
		seq := make([]*vecBlock, 0, 8)
		for end < len(keys) && keys[end].Shard == keys[start].Shard {
			seq = append(seq, a.blocks[keys[end]])
			end++
		}
		roots = append(roots, vecMergeTree(seq))
		start = end
	}
	if a.loose != nil {
		roots = append(roots, a.loose)
	}
	if len(roots) == 0 {
		return newVecBlock(p)
	}
	return vecMergeTree(roots)
}

// finish freezes the value and components from the live partials.
func (a *vecAggregator) finish() {
	r := a.root()
	val := make([]float64, len(a.members))
	for i := range val {
		switch a.typ {
		case types.AGG_VEC_MEAN:
			if r.w[i] == 0 {
				val[i] = math.NaN()
			} else {
				val[i] = r.sum[i] / r.w[i]
			}
		default:
			val[i] = r.sum[i]
		}
	}
	a.frozen, a.value = true, val
	comp := map[string]any{
		"labels":      append([]string(nil), a.labels...),
		"element_n":   append([]int(nil), r.n...),
		"element_sum": append([]float64(nil), r.sum...),
	}
	if a.weight != nil {
		comp["element_sum_weights"] = append([]float64(nil), r.w...)
	}
	a.frozenComp = comp
}

func (a *vecAggregator) Aggregate(records []*Record, _ string) (float64, error) {
	a.reset()
	for _, r := range records {
		a.fold(r)
	}
	a.finish()
	a.reset()
	return math.NaN(), nil
}

func (a *vecAggregator) UpdateRow(r *Record, _ string) error {
	a.fold(r)
	return nil
}

func (a *vecAggregator) Finalize() (float64, error) {
	a.finish()
	a.reset()
	return math.NaN(), nil
}

// Rich returns the per-member figures in member order ([]float64; a
// NaN element marshals as null). Before any Aggregate / Finalize it
// reports the empty state.
func (a *vecAggregator) Rich() (any, error) {
	if !a.frozen {
		a.finish()
	}
	return append([]float64(nil), a.value...), nil
}

// Components returns the frozen per-element state: labels (the display
// label per member), element_n (rows whose member answered — raw, like
// the floor's n), element_sum (Σw·x) and, on a weighted slot,
// element_sum_weights (Σw over contributing rows; the mean's
// denominator).
func (a *vecAggregator) Components() (map[string]any, error) {
	if !a.frozen {
		a.finish()
	}
	return a.frozenComp, nil
}

// ElementLabels is the display label per member, in member order — the
// keys a presentation of the array (expand) names its elements by.
func (a *vecAggregator) ElementLabels() []string { return append([]string(nil), a.labels...) }

// MergeOnline absorbs another partition's partials. Partitions cover
// disjoint merge blocks, so this is a map union and order-free; a
// block present on both sides, or loose (unpositioned) rows on both,
// means the split broke the invariant the bits rest on —
// PROCESSING_INTERNAL.
func (a *vecAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*vecAggregator)
	if !ok || b.typ != a.typ || len(b.members) != len(a.members) {
		return mergeTypeMismatch(string(a.typ))
	}
	for k, blk := range b.blocks {
		if _, dup := a.blocks[k]; dup {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				string(a.typ)+": one merge block reached two partitions",
				map[string]any{"shard": k.Shard, "block": k.Block})
		}
		a.blocks[k] = blk
	}
	if b.loose != nil {
		if a.loose != nil {
			return errors.NewCodedError(errors.PROCESSING_INTERNAL,
				string(a.typ)+": rows without a merge position reached two partitions; their order is undefined")
		}
		a.loose = b.loose
	}
	a.cur = nil
	b.reset()
	return nil
}

// Compile-time interface locks.
var (
	_ MetaAggregator      = (*vecAggregator)(nil)
	_ MergeableAggregator = (*vecAggregator)(nil)
	_ RichAggregator      = (*vecAggregator)(nil)
)
