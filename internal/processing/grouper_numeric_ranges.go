package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/types"
)

// numericRangesGrouper buckets each numeric row by a validated set of
// labeled half-open ranges ([start, end); internal/numrange). The bucket
// key is the matching range's label; rows outside every range land in the
// unmatched bucket. The range→label lookup is row-local, so the grouper
// is streamable and mergeable (and a weight never changes a key).
type numericRangesGrouper struct {
	field string
	cfg   numrange.Config

	// rank maps each supplied label to its author-order index, the
	// unmatched label to len(order): the keyOrderer rank. Built from the
	// configuration (inline ranges, or the resolved table) — never from
	// records — so a fresh instance orders as the partitioning one did.
	rank map[string]float64

	// resolveErr holds a deferred table-resolution error (unknown table,
	// a date table, or the unmatched-label collision), surfaced at KeyFor.
	resolveErr error

	// liveBuckets mirrors the post-Group / post-KeyForRow per-bucket
	// counts for Components(). KeyFor itself never touches it.
	liveBuckets map[string]int
}

func newNumericRangesGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	// Numeric fields only: the accepted-types table refuses a date,
	// categorical, packed_bool, decimal or set field (set: its own
	// message), exactly as predict reports it.
	if err := rejectSetFieldForNumericGrouper(grp, schema); err != nil {
		return nil, err
	}
	cfg, err := numrange.Parse(grp.Params)
	if err != nil {
		return nil, err
	}
	g := &numericRangesGrouper{field: grp.Field, cfg: cfg}
	if cfg.Set != nil {
		g.buildRank()
	}
	return g, nil
}

func (g *numericRangesGrouper) buildRank() {
	g.rank = make(map[string]float64, len(g.cfg.Order)+1)
	for i, label := range g.cfg.Order {
		g.rank[label] = float64(i)
	}
	g.rank[g.cfg.UnmatchedLabel] = float64(len(g.cfg.Order))
}

// SetExtensions implements ExtensionAware: a table-source grouper
// resolves its numeric table against the live registry. Failures are
// stashed and surfaced at KeyFor.
func (g *numericRangesGrouper) SetExtensions(r *ExtensionRegistry) {
	if g.cfg.Table == "" || g.cfg.Set != nil || g.resolveErr != nil {
		return
	}
	specs, err := resolveNumericRangeSpecs(g.cfg.Table, r)
	if err == nil {
		err = g.cfg.Resolve(specs)
	}
	if err != nil {
		g.resolveErr = err
		return
	}
	g.buildRank()
}

// KeyFor implements StreamableGrouper.KeyFor: the label of the range
// holding the value, else the unmatched label; a null value is
// ErrGrouperKeyNull.
func (g *numericRangesGrouper) KeyFor(r *Record) (string, error) {
	if g.resolveErr != nil {
		return "", g.resolveErr
	}
	if g.cfg.Set == nil {
		return "", errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"GROUP_NUMERIC_RANGES: range table not resolved (SetExtensions was not called for a table-backed grouper)")
	}
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	if label, matched := g.cfg.Set.Match(v); matched {
		return label, nil
	}
	return g.cfg.UnmatchedLabel, nil
}

// KeyForRow is the StreamingGrouper adapter: KeyFor plus the live count.
func (g *numericRangesGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	key, ok, err := keyForOrSkip(g, r)
	if err != nil || !ok {
		return key, ok, err
	}
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]int)
	}
	g.liveBuckets[key]++
	return key, ok, nil
}

func (g *numericRangesGrouper) Group(records []*Record, _ string) (map[string][]*Record, error) {
	groups, err := streamableGroup(g, records)
	if err != nil {
		return nil, err
	}
	g.liveBuckets = make(map[string]int, len(groups))
	for k, v := range groups {
		g.liveBuckets[k] = len(v)
	}
	return groups, nil
}

// keyRank implements keyOrderer: a supplied label ranks by its position
// in the ranges (author order), the unmatched label last; any other key
// is unranked. Configuration-only, so every arm emits supplied order.
func (g *numericRangesGrouper) keyRank(key string) (float64, bool) {
	r, ok := g.rank[key]
	return r, ok
}

// Components implements MetaGrouper: {n_ranges, unmatched_label,
// buckets: [{key, label, count}]} — every configured range in supplied
// order (zero counts included), then the unmatched bucket when observed.
func (g *numericRangesGrouper) Components() (map[string]any, error) {
	buckets := make([]map[string]any, 0, len(g.cfg.Order)+1)
	for _, label := range g.cfg.Order {
		buckets = append(buckets, map[string]any{"key": label, "label": label, "count": g.liveBuckets[label]})
	}
	if n := g.liveBuckets[g.cfg.UnmatchedLabel]; n > 0 {
		buckets = append(buckets, map[string]any{"key": g.cfg.UnmatchedLabel, "label": g.cfg.UnmatchedLabel, "count": n})
	}
	return map[string]any{
		"n_ranges":        len(g.cfg.Order),
		"unmatched_label": g.cfg.UnmatchedLabel,
		"buckets":         buckets,
	}, nil
}

// MergeGrouperState implements MergeableGrouper: per-range counts sum.
func (g *numericRangesGrouper) MergeGrouperState(other Grouper) error {
	o, ok := other.(*numericRangesGrouper)
	if !ok {
		return grouperMergeMismatch(numrange.Operator, other)
	}
	if len(o.liveBuckets) > 0 && g.liveBuckets == nil {
		g.liveBuckets = make(map[string]int, len(o.liveBuckets))
	}
	for k, n := range o.liveBuckets {
		g.liveBuckets[k] += n
	}
	return nil
}

var (
	_ MetaGrouper       = (*numericRangesGrouper)(nil)
	_ StreamableGrouper = (*numericRangesGrouper)(nil)
	_ StreamingGrouper  = (*numericRangesGrouper)(nil)
	_ ExtensionAware    = (*numericRangesGrouper)(nil)
	_ MergeableGrouper  = (*numericRangesGrouper)(nil)
)
