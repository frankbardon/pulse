package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// matrix_grouped.go is Request.Matrices on a grouped request: one set
// of matrix slots per group bucket. A bucket's slots are minted on the
// key's first sighting — exactly when its aggregator bucket is — so the
// matrix buckets are the grouped Response.Data rows, and each slot holds
// per-merge-block co-moments for the blocks that bucket's rows touched
// only (BlockCoMoments allocates a block on its first row). Finalize
// runs each bucket's own fixed merge tree, so a bucket's matrix is a
// function of that bucket's rows alone, whatever path or worker count
// delivered them.
//
// Bucket keys come from the grouper the grouped path executes — the
// same GroupKeyer / Grouper.Group partition and the same
// orderKeysByInclude order the Data rows use. The engine executes
// Request.Groups[0] only today (multi-entry Groups is U35's), and the
// matrices follow whatever the Data rows do.

// GroupedMatrices is a grouped run's matrix state: the resolved specs
// and one slot set per bucket key. A nil *GroupedMatrices is a request
// without matrices (or without groups): every method is a no-op.
type GroupedMatrices struct {
	set     *matrixPlanSet
	header  types.AxisHeader
	buckets map[string][]*matrixSlot
}

// BuildGroupedMatrices returns empty per-bucket matrix state for a
// grouped req (nil when req carries no matrices or no groups). req
// must be the STAMPED request, as for BuildMatrixSlots.
func BuildGroupedMatrices(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) (*GroupedMatrices, error) {
	if req == nil || len(req.Groups) == 0 || req.Groups[0] == nil {
		return nil, nil
	}
	set, err := resolveMatrixPlans(req, schema, exts)
	if err != nil || set == nil {
		return nil, err
	}
	grp := req.Groups[0]
	return &GroupedMatrices{
		set:     set,
		header:  types.AxisHeader{Fields: []string{grp.Field}, Types: []string{string(grp.Type)}},
		buckets: make(map[string][]*matrixSlot),
	}, nil
}

// bucket returns key's slots, minting zero-state ones on first sight.
func (g *GroupedMatrices) bucket(key string) ([]*matrixSlot, error) {
	slots, ok := g.buckets[key]
	if ok {
		return slots, nil
	}
	slots, err := g.set.fresh()
	if err != nil {
		return nil, err
	}
	g.buckets[key] = slots
	return slots, nil
}

// UpdateRow folds one filter-passing, merge-position-stamped record
// into bucket key's slots.
func (g *GroupedMatrices) UpdateRow(key string, r *Record) error {
	if g == nil {
		return nil
	}
	slots, err := g.bucket(key)
	if err != nil {
		return err
	}
	for _, s := range slots {
		if err := s.UpdateRow(r, ""); err != nil {
			return err
		}
	}
	return nil
}

// foldRecords folds a buffered bucket's records, in record order, into
// bucket key's slots.
func (g *GroupedMatrices) foldRecords(key string, records []*Record) error {
	if g == nil {
		return nil
	}
	slots, err := g.bucket(key)
	if err != nil {
		return err
	}
	return foldMatrixRecords(slots, records)
}

// Merge absorbs o's buckets: a key only o saw moves over whole; a key
// both saw merges slot by slot (MergeBlockMerger — the disjoint block
// union). o must not be used afterwards.
func (g *GroupedMatrices) Merge(o *GroupedMatrices) error {
	if g == nil || o == nil {
		if (g == nil) != (o == nil) {
			return errors.NewCodedError(errors.PROCESSING_INTERNAL,
				"grouped matrix merge: one partition carries matrix state and the other none")
		}
		return nil
	}
	for key, src := range o.buckets {
		dst, ok := g.buckets[key]
		if !ok {
			g.buckets[key] = src
			continue
		}
		if err := mergeSlotSets(dst, src); err != nil {
			return err
		}
	}
	o.buckets = map[string][]*matrixSlot{}
	return nil
}

// mergeSlotSets folds src's slots into dst's, slot by slot.
func mergeSlotSets(dst, src []*matrixSlot) error {
	if len(dst) != len(src) {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"matrix merge: partitions differ in slot count",
			map[string]any{"slots": len(dst), "other_slots": len(src)})
	}
	for i, s := range dst {
		if err := MergeBlockMerger(s, src[i]); err != nil {
			return err
		}
		s.dropped += src[i].dropped
		s.allNull += src[i].allNull
	}
	return nil
}

// finalize renders the per-bucket results over keys — the grouped
// emission order of the Data rows — spec-major, then bucket. Each
// result (and Components entry) carries its bucket's GroupKey and the
// executed grouper's GroupHeader. A key with no matrix state (no row
// reached its slots) renders from empty slots, so it is still emitted,
// as the thin bucket it is.
func (g *GroupedMatrices) finalize(keys []string, withComponents bool) ([]types.MatrixResult, []types.MatrixComponents, error) {
	if g == nil {
		return nil, nil, nil
	}
	sets := make([][]*matrixSlot, len(keys))
	for k, key := range keys {
		slots, err := g.bucket(key)
		if err != nil {
			return nil, nil, err
		}
		sets[k] = slots
	}
	out := make([]types.MatrixResult, 0, len(g.set.plans)*len(keys))
	var comps []types.MatrixComponents
	for i := range g.set.plans {
		for k, key := range keys {
			res, c, err := sets[k][i].result(withComponents)
			if err != nil {
				return nil, nil, err
			}
			res.GroupKey = types.AxisKey{key}
			res.GroupHeader = &types.AxisHeader{
				Fields: append([]string(nil), g.header.Fields...),
				Types:  append([]string(nil), g.header.Types...),
			}
			out = append(out, res)
			if c != nil {
				c.GroupKey = types.AxisKey{key}
				comps = append(comps, *c)
			}
		}
	}
	return out, comps, nil
}
