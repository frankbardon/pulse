package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Vector aggregators — AGG_VEC_MEAN, AGG_VEC_SUM (U27 E2-S2) and
// AGG_VEC_SD (U30 matrix-extensions-hardening E5-S1). Each
// reads a vector input (the slot's Fields after Processor.prepare's
// rewrite, vectors.AggregationSlotRewrite: the literal members, with
// the display labels in params.labels) and folds one figure PER
// MEMBER:
//
//	AGG_VEC_MEAN  element i = Σw·x_i / Σw over the rows whose member i
//	              answered (NaN — null on the wire — when none did)
//	AGG_VEC_SUM   element i = Σw·x_i over the same rows (0 when none did)
//	AGG_VEC_SD    element i = √(c·M2_i / (N*_i − 1)) over the same rows:
//	              M2_i = Σw·(x_i − mean_i)², N* = Σw (unweighted: n;
//	              frequency: Σw) or Kish n_eff = (Σw)²/Σw² (probability),
//	              c = N*/Σw — weighting.ScaledVariance, the one inference
//	              rule. NaN (null on the wire) when N* ≤ 1, never 0
//
// AGG_VEC_SD folds each row by the weighted Welford–West recurrence
// and combines two partials by Chan's rule (vecBlock.mergeMoments);
// the moment slices exist only on its blocks, so AGG_VEC_MEAN /
// AGG_VEC_SUM run the exact operation sequence they always have.
//
// Missing data is per ELEMENT: a null member skips that element of
// that row only; the other elements still count the row. A stored NaN
// is a value, not a null, and propagates into its element. On a
// weighted slot a row contributes only with a valid weight > 0 (the
// other aggregators' rule); element_n stays the RAW present count, as
// the floor's n does.
//
// Bit-exact merge (the linalg blocked-merge pattern, block_merge.go).
// A row folds into the fragment its GLOBAL merge position names
// (Record.MergeBlock: global ordinal / linalg.MergeBlockSize, shard,
// head) — never into "this worker's partial". A head fragment folds as
// rows arrive; a fragment that starts mid-block (a shard edge inside
// the block) buffers its rows, and Finalize replays them on top of the
// head in shard order, so each block's partial is the sequential fold
// of its rows in ordinal order. Finalize then combines the blocks in
// one fixed binary tree (vecMergeTree, linalg.MergeTree's shape) by
// block order. Partitions own disjoint fragments, so MergeOnline is a
// map union and the merge order of partitions cannot reach the bits:
// serial, every DecodeWorkers and every ShardWorkers count — and a
// shard archive versus its single-file twin, whatever the split —
// answer the same words. Products are written float64(w*x) so no FMA
// contraction differs by architecture. A record no source stamped (a
// joined or chain-stage row) folds into one trailing "loose" partial in
// arrival order; two partitions both holding loose rows cannot be
// ordered and refuse to merge (PROCESSING_INTERNAL), never guess.
//
// The scalar Aggregate / Finalize fallback is NaN: an array value has
// no single number, and every consumer that needs one refuses the slot
// (types.AggregationType.ArrayValued). The value rides Rich. The one
// scalar form is a crosstab slot's params.element: the prepared slot
// reads that member alone, Aggregate / Finalize return its figure and
// Rich returns none, so the cell is that element's mean / sum and its
// components the selected element's (one-entry arrays).

// vecBlock is one merge block's partial: per element Σw·x, Σw over
// contributing rows and the raw present count. A moment block
// (AGG_VEC_SD, newVecMomentBlock) also carries per element the running
// weighted mean, M2 = Σw·(x − mean)² and Σw²; on any other block the
// three are nil and never touched.
type vecBlock struct {
	sum []float64
	w   []float64
	n   []int

	mean []float64
	m2   []float64
	w2   []float64
}

func newVecBlock(p int) *vecBlock {
	return &vecBlock{sum: make([]float64, p), w: make([]float64, p), n: make([]int, p)}
}

// newVecMomentBlock is newVecBlock plus the per-element moments.
func newVecMomentBlock(p int) *vecBlock {
	b := newVecBlock(p)
	b.mean, b.m2, b.w2 = make([]float64, p), make([]float64, p), make([]float64, p)
	return b
}

func (b *vecBlock) clone() *vecBlock {
	c := &vecBlock{
		sum: append([]float64(nil), b.sum...),
		w:   append([]float64(nil), b.w...),
		n:   append([]int(nil), b.n...),
	}
	if b.m2 != nil {
		c.mean = append([]float64(nil), b.mean...)
		c.m2 = append([]float64(nil), b.m2...)
		c.w2 = append([]float64(nil), b.w2...)
	}
	return c
}

// add folds member i of one row: x answered, w the row weight,
// contrib whether the row carries weight (valid and > 0). A moment
// block then folds the weighted Welford–West step in the operation-
// order form (weighting.Welford.Add's: (w·δ)/Σw, (w·δ)·(x − mean')),
// every product written float64(a*b) so no FMA contraction differs by
// architecture.
func (b *vecBlock) add(i int, x, w float64, contrib bool) {
	b.n[i]++
	if !contrib {
		return
	}
	b.sum[i] += float64(w * x)
	b.w[i] += w
	if b.m2 != nil {
		b.w2[i] += float64(w * w)
		wd := float64(w * (x - b.mean[i]))
		b.mean[i] += wd / b.w[i]
		b.m2[i] += float64(wd * (x - b.mean[i]))
	}
}

// vecFrag is one merge fragment: blk on a head fragment (rows folded as
// they arrive); otherwise the buffered rows — per row p values in xs
// with their presence in pres, the weight in ws and whether it
// contributes in contrib — for root to replay in order.
type vecFrag struct {
	blk     *vecBlock
	xs      []float64
	pres    []bool
	ws      []float64
	contrib []bool
}

// replay folds the buffered rows into b in arrival order.
func (f *vecFrag) replay(b *vecBlock) {
	p := len(b.sum)
	for r, w := range f.ws {
		for i := 0; i < p; i++ {
			if f.pres[r*p+i] {
				b.add(i, f.xs[r*p+i], w, f.contrib[r])
			}
		}
	}
}

// merge adds o into b element by element (plain adds: no product, no
// contraction); a moment block first combines the moments
// (mergeMoments), which reads b's Σw before the add.
func (b *vecBlock) merge(o *vecBlock) {
	for i := range b.sum {
		if b.m2 != nil {
			b.mergeMoments(o, i)
		}
		b.sum[i] += o.sum[i]
		b.w[i] += o.w[i]
		b.n[i] += o.n[i]
	}
}

// mergeMoments folds element i of o's moments into b's by the
// Chan–Golub–LeVeque combine over weight totals (mergeWelfordWeighted's
// rule): mean' = mean_A + δ·w_B/W, M2' = M2_A + M2_B + δ²·w_A·w_B/W,
// δ = mean_B − mean_A, W = w_A + w_B; Σw² adds. Every product is written
// float64(a*b) (FMA-free, linalg's blocked-merge contract). b.w[i] is
// still w_A here: merge adds the weight totals after.
func (b *vecBlock) mergeMoments(o *vecBlock, i int) {
	b.w2[i] += o.w2[i]
	wA, wB := b.w[i], o.w[i]
	switch {
	case wB == 0:
	case wA == 0:
		b.mean[i], b.m2[i] = o.mean[i], o.m2[i]
	default:
		total := wA + wB
		delta := o.mean[i] - b.mean[i]
		b.mean[i] += float64(delta*wB) / total
		b.m2[i] += o.m2[i] + float64(float64(delta*delta)*float64(wA*wB))/total
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

// vecAggregator backs AGG_VEC_MEAN, AGG_VEC_SUM and AGG_VEC_SD.
type vecAggregator struct {
	typ types.AggregationType
	// moments: the slot folds the per-element Welford moments
	// (AGG_VEC_SD), so every block it opens is a moment block.
	moments bool
	members []string
	labels  []string
	// fill reads the members off a row (the shared fill: a native
	// vector's NaN element or null vector is a null member, like its
	// scalar twin's null column); xs / pres are its per-row scratch.
	fill memberFill
	xs   []float64
	pres []bool
	// weight is the applied slot weight; nil on an unweighted slot.
	weight *types.WeightSpec
	// expand is params.expand: the row carries one scalar column per
	// member (vectors.ExpandedColumn) instead of the array.
	expand bool
	// element is params.element: a crosstab slot reading one member
	// (members holds it alone). Aggregate / Finalize return that
	// element's figure and Rich defers to it, so the cell is a scalar.
	element bool

	blocks map[BlockKey]*vecFrag
	loose  *vecBlock // rows with no merge position, in arrival order
	cur    *vecFrag
	curKey BlockKey

	frozen     bool
	err        error
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
	resolved := vectors.Resolved{Members: append([]string(nil), agg.Fields...)}
	if schema != nil {
		r, rerr := vectors.ResolveSpec("aggregation.fields", types.VectorSpec{Fields: agg.Fields, Labels: p.Labels}, schema)
		if rerr != nil {
			return nil, rerr
		}
		resolved, p.Labels = r, r.Labels
	}
	members := resolved.Members
	labels := p.Labels
	if len(labels) != len(members) {
		labels = members
	}
	if p.Element != nil && (len(members) != 1 || labels[0] != *p.Element) {
		// An element slot reads its one member only after the
		// request-level narrowing (vectors.AggregationSlotRewrite), which
		// the universal floor (AggregationPresent) reads too. A wider
		// slot here would fold one member while the floor counted any —
		// an engine fault, refused loudly.
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			name+": params.element "+*p.Element+" was not narrowed to its member before the factory",
			map[string]any{"aggregation": name, "element": *p.Element, "members": append([]string(nil), members...)})
	}
	a := &vecAggregator{typ: agg.Type, moments: agg.Type == types.AGG_VEC_SD, members: members, labels: append([]string(nil), labels...), expand: p.Expand, element: p.Element != nil,
		fill: newMemberFill(resolved), xs: make([]float64, len(members)), pres: make([]bool, len(members))}
	if w := slotWeight(agg); w != nil {
		ew := effectiveWeight(w)
		a.weight = &ew
	}
	a.reset()
	return a, nil
}

// newBlock is an empty partial of this slot's shape.
func (a *vecAggregator) newBlock() *vecBlock {
	if a.moments {
		return newVecMomentBlock(len(a.members))
	}
	return newVecBlock(len(a.members))
}

func (a *vecAggregator) reset() {
	a.blocks = map[BlockKey]*vecFrag{}
	a.loose, a.cur = nil, nil
}

// fragFor returns the fragment r's merge position names, nil for an
// unpositioned record (it folds into loose).
func (a *vecAggregator) fragFor(r *Record) *vecFrag {
	key, ok := r.MergeBlock()
	if !ok {
		return nil
	}
	if a.cur != nil && key == a.curKey {
		return a.cur
	}
	f, exists := a.blocks[key]
	if !exists {
		f = &vecFrag{}
		if key.Head {
			f.blk = a.newBlock()
		}
		a.blocks[key] = f
	}
	a.cur, a.curKey = f, key
	return f
}

// fold adds one row: into its head fragment's (or the loose) partial
// now, or into a non-head fragment's buffer for root to replay.
func (a *vecAggregator) fold(r *Record) {
	w, contributes := 1.0, true
	if a.weight != nil {
		w, contributes = validRowWeight(r, a.weight)
	}
	f := a.fragFor(r)
	var b *vecBlock
	switch {
	case f == nil:
		if a.loose == nil {
			a.loose = a.newBlock()
		}
		b = a.loose
	case f.blk != nil:
		b = f.blk
	}
	a.fill.fill(r, a.xs, a.pres)
	if b != nil {
		for i, x := range a.xs {
			if a.pres[i] {
				b.add(i, x, w, contributes)
			}
		}
		return
	}
	f.xs = append(f.xs, a.xs...)
	f.pres = append(f.pres, a.pres...)
	f.ws = append(f.ws, w)
	f.contrib = append(f.contrib, contributes)
}

// root combines every partial in the one fixed shape: each block's
// partial is its head fragment (or an empty block) with the block's
// other fragments replayed in shard order; vecMergeTree runs over the
// block partials in block order, then the loose partial merges last.
func (a *vecAggregator) root() (*vecBlock, error) {
	keys := make([]BlockKey, 0, len(a.blocks))
	for k := range a.blocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	runs, err := blockRuns(keys)
	if err != nil {
		return nil, err
	}
	seq := make([]*vecBlock, 0, len(runs))
	for _, run := range runs {
		head := a.blocks[run[0]]
		if len(run) == 1 && head.blk != nil {
			seq = append(seq, head.blk)
			continue
		}
		b := a.newBlock()
		if head.blk != nil {
			b = head.blk.clone()
		}
		for _, k := range run {
			a.blocks[k].replay(b)
		}
		seq = append(seq, b)
	}
	var roots []*vecBlock
	if len(seq) > 0 {
		roots = append(roots, vecMergeTree(seq))
	}
	if a.loose != nil {
		roots = append(roots, a.loose)
	}
	if len(roots) == 0 {
		return a.newBlock(), nil
	}
	return vecMergeTree(roots), nil
}

// finish freezes the value and components from the live partials; a
// fragment-invariant break freezes the error instead.
func (a *vecAggregator) finish() {
	r, err := a.root()
	if err != nil {
		a.frozen, a.err, a.value, a.frozenComp = true, err, nil, nil
		return
	}
	a.err = nil
	val := make([]float64, len(a.members))
	basis := weighting.BasisOf(a.weight)
	for i := range val {
		switch a.typ {
		case types.AGG_VEC_MEAN:
			if r.w[i] == 0 {
				val[i] = math.NaN()
			} else {
				val[i] = r.sum[i] / r.w[i]
			}
		case types.AGG_VEC_SD:
			val[i] = vecSD(basis, r.m2[i], r.w[i], r.w2[i])
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
	if a.moments {
		comp["element_mean"] = append([]float64(nil), r.mean...)
		comp["element_m2"] = append([]float64(nil), r.m2...)
		if a.weight != nil {
			comp["element_sum_weights_sq"] = append([]float64(nil), r.w2...)
		}
	}
	a.frozenComp = comp
}

// vecSD is one element's sample standard deviation under basis:
// √(c·M2 / (N* − 1)) — weighting.ScaledVariance, the one inference
// rule (M2/(Σw − 1) unweighted / frequency, M2/(Σw − Σw²/Σw) under
// probability). N* ≤ 1 leaves no positive denominator: undefined, NaN
// (JSON null), never 0 — so is an element no row answered.
func vecSD(basis weighting.Basis, m2, sumW, sumWSq float64) float64 {
	nStar := basis.NStar(sumW, sumWSq)
	if !(nStar > 1) {
		return math.NaN()
	}
	return math.Sqrt(weighting.ScaledVariance(basis, m2, sumW, nStar))
}

func (a *vecAggregator) Aggregate(records []*Record, _ string) (float64, error) {
	a.reset()
	for _, r := range records {
		a.fold(r)
	}
	a.finish()
	a.reset()
	return a.scalar(), a.err
}

// scalar is the frozen value's one number: the selected element on an
// element slot, else NaN (an array has none).
func (a *vecAggregator) scalar() float64 {
	if a.element && a.err == nil && len(a.value) == 1 {
		return a.value[0]
	}
	return math.NaN()
}

func (a *vecAggregator) UpdateRow(r *Record, _ string) error {
	a.fold(r)
	return nil
}

func (a *vecAggregator) Finalize() (float64, error) {
	a.finish()
	a.reset()
	return a.scalar(), a.err
}

// Rich returns the per-member figures in member order ([]float64; a
// NaN element marshals as null). Before any Aggregate / Finalize it
// reports the empty state.
func (a *vecAggregator) Rich() (any, error) {
	if !a.frozen {
		a.finish()
	}
	if a.err != nil {
		return nil, a.err
	}
	if a.element {
		// No rich payload: the cell takes the scalar.
		return nil, nil
	}
	return append([]float64(nil), a.value...), nil
}

// Components returns the frozen per-element state: labels (the display
// label per member), element_n (rows whose member answered — raw, like
// the floor's n), element_sum (Σw·x) and, on a weighted slot,
// element_sum_weights (Σw over contributing rows; the mean's
// denominator). AGG_VEC_SD adds element_mean (the running weighted
// Welford mean), element_m2 (Σw·(x − mean)²) and, weighted,
// element_sum_weights_sq (Σw², Kish n_eff's denominator).
func (a *vecAggregator) Components() (map[string]any, error) {
	if !a.frozen {
		a.finish()
	}
	if a.err != nil {
		return nil, a.err
	}
	return a.frozenComp, nil
}

// ElementLabels is the display label per member, in member order — the
// keys a presentation of the array (expand) names its elements by.
func (a *vecAggregator) ElementLabels() []string { return append([]string(nil), a.labels...) }

// ExpandedElements reports, on a slot whose params set expand, the
// element labels and the per-member figures the row carries as one
// column each; ok is false on an array slot.
func (a *vecAggregator) ExpandedElements() (labels []string, values []float64, ok bool, err error) {
	if !a.expand {
		return nil, nil, false, nil
	}
	if !a.frozen {
		a.finish()
	}
	if a.err != nil {
		return nil, nil, false, a.err
	}
	return append([]string(nil), a.labels...), append([]float64(nil), a.value...), true, nil
}

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
	for k, frag := range b.blocks {
		if _, dup := a.blocks[k]; dup {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				string(a.typ)+": one merge block reached two partitions",
				map[string]any{"shard": k.Shard, "block": k.Block})
		}
		a.blocks[k] = frag
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
	_ elementExpander     = (*vecAggregator)(nil)
)
