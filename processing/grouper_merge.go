package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// MergeableGrouper is the grouper-side sibling of MergeableAggregator:
// it folds another instance's LIVE COMPONENTS STATE (the per-bucket
// counters behind MetaGrouper.Components) into the receiver.
//
// The parallel reducers (per-shard and per-segment) build one grouper
// instance per partition and fold aggregator state at the bucket-key
// level. Without this fold there is no instance whose Components()
// describes the whole cohort, so a grouped parallel run could not emit
// Response.Components.Groupers at all while the serial arm did — the
// same request answering in two shapes depending on a worker count.
//
// Implemented by exactly the groupers types.GroupType.Mergeable()
// admits (CATEGORY, RANGE, DATE_RANGES, SET_VALUE, SET_PER_ELEMENT).
// `other` must be a fresh instance of the same concrete type built
// from the same *types.Group; a mismatch is PROCESSING_INTERNAL.
// Partials are folded in partition order, and every fold below is
// either a sum or the same first/last-write rule the serial tracker
// applies, so the merged Components() equals a serial instance's.
type MergeableGrouper interface {
	MergeGrouperState(other Grouper) error
}

func grouperMergeMismatch(want string, other Grouper) error {
	return errors.NewCodedError(errors.PROCESSING_INTERNAL,
		fmt.Sprintf("grouper merge: %s cannot fold a %T", want, other))
}

// MergeGrouperState implements MergeableGrouper: per-bucket counts sum.
func (g *categoryGrouper) MergeGrouperState(other Grouper) error {
	o, ok := other.(*categoryGrouper)
	if !ok {
		return grouperMergeMismatch("GROUP_CATEGORY", other)
	}
	if len(o.liveBuckets) > 0 && g.liveBuckets == nil {
		g.liveBuckets = make(map[string]int, len(o.liveBuckets))
	}
	for k, n := range o.liveBuckets {
		g.liveBuckets[k] += n
	}
	return nil
}

// MergeGrouperState implements MergeableGrouper: a bucket's [low, high)
// is a pure function of its key, counts sum, the observed range widens
// and the (currently always-zero) under/overflow tallies sum.
func (g *rangeGrouper) MergeGrouperState(other Grouper) error {
	o, ok := other.(*rangeGrouper)
	if !ok {
		return grouperMergeMismatch("GROUP_RANGE", other)
	}
	if len(o.liveBuckets) > 0 && g.liveBuckets == nil {
		g.liveBuckets = make(map[string]rangeBucketStat, len(o.liveBuckets))
	}
	for k, os := range o.liveBuckets {
		s, exists := g.liveBuckets[k]
		if !exists {
			s = rangeBucketStat{low: os.low, high: os.high}
		}
		s.count += os.count
		g.liveBuckets[k] = s
	}
	if o.liveRangeSet {
		if !g.liveRangeSet || o.rangeMin < g.rangeMin {
			g.rangeMin = o.rangeMin
		}
		if !g.liveRangeSet || o.rangeMax > g.rangeMax {
			g.rangeMax = o.rangeMax
		}
		g.liveRangeSet = true
	}
	g.underflowCount += o.underflowCount
	g.overflowCount += o.overflowCount
	return nil
}

// MergeGrouperState implements MergeableGrouper: per-range counts sum.
func (g *dateRangesGrouper) MergeGrouperState(other Grouper) error {
	o, ok := other.(*dateRangesGrouper)
	if !ok {
		return grouperMergeMismatch("GROUP_DATE_RANGES", other)
	}
	if len(o.liveBuckets) > 0 && g.liveBuckets == nil {
		g.liveBuckets = make(map[string]int, len(o.liveBuckets))
	}
	for k, n := range o.liveBuckets {
		g.liveBuckets[k] += n
	}
	return nil
}

// MergeGrouperState implements MergeableGrouper: counts and the
// empty-mask tally sum; the stored mask follows the serial tracker's
// LAST-write rule (trackSetValueRow overwrites it per observation), so
// the later partition's mask wins.
func (g *setValueGrouper) MergeGrouperState(other Grouper) error {
	o, ok := other.(*setValueGrouper)
	if !ok {
		return grouperMergeMismatch("GROUP_SET_VALUE", other)
	}
	if len(o.liveBuckets) > 0 && g.liveBuckets == nil {
		g.liveBuckets = make(map[string]setValueBucketStat, len(o.liveBuckets))
	}
	for k, os := range o.liveBuckets {
		s := g.liveBuckets[k]
		s.mask = os.mask
		s.count += os.count
		g.liveBuckets[k] = s
	}
	g.nEmptyMask += o.nEmptyMask
	return nil
}

// MergeGrouperState implements MergeableGrouper: counts sum; label and
// dict index follow the serial tracker's FIRST-write rule.
func (g *setPerElementGrouper) MergeGrouperState(other Grouper) error {
	o, ok := other.(*setPerElementGrouper)
	if !ok {
		return grouperMergeMismatch("GROUP_SET_PER_ELEMENT", other)
	}
	if len(o.liveBuckets) > 0 && g.liveBuckets == nil {
		g.liveBuckets = make(map[string]setPerElementBucketStat, len(o.liveBuckets))
	}
	for k, os := range o.liveBuckets {
		s, exists := g.liveBuckets[k]
		if !exists {
			s = setPerElementBucketStat{label: os.label, dictIndex: os.dictIndex}
		}
		s.count += os.count
		g.liveBuckets[k] = s
	}
	return nil
}

var (
	_ MergeableGrouper = (*categoryGrouper)(nil)
	_ MergeableGrouper = (*rangeGrouper)(nil)
	_ MergeableGrouper = (*dateRangesGrouper)(nil)
	_ MergeableGrouper = (*setValueGrouper)(nil)
	_ MergeableGrouper = (*setPerElementGrouper)(nil)
)
