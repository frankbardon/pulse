package processing

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/groupfit"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Composite group keys: the shared core behind a plain Request.Groups
// list with two or more groupers (U54). Every grouped arm — buffered
// processGrouped, the streaming keyer and the parallel / shard reducers
// — keys a record through CompositeKeySet.Expand, orders the observed
// keys with OrderCompositeGroupKeys and writes the key columns with
// WriteGroupKeyColumns, so the arms cannot drift.
//
// Rules (crosstab's composite-key engine, reused):
//   - The bucket key is the per-grouper keys joined with
//     crosstabAxisKeySep ("\x00"); the per-grouper tuple is carried
//     beside it and the key string is NEVER parsed back.
//   - Only observed combinations exist; a record whose key set is empty
//     at ANY position (null key, include rejection, empty fan-out) joins
//     no composite bucket — yet the caller still drives every grouper
//     once for that record (the crosstab ONCE-PER-RECORD rule), so each
//     grouper's own Components see every record.
//   - A multi-key grouper (GROUP_SET_PER_ELEMENT, extension fan-out)
//     contributes the per-record cartesian product of each position's
//     key set.
//   - Keys order by each grouper's native order (includeFilterOf: the
//     emission order, else the include list, else alphabetical), outer
//     to inner.
//
// A single-grouper request never enters this file: its fast path keeps
// the plain bucket key (byte-identical output, no new allocations).

// compositeEntry is one observed composite key and its carried tuple.
type compositeEntry struct {
	key   string
	tuple types.AxisKey
}

// CompositeKeySet interns the observed composite keys of a
// multi-grouper run together with their key tuples. Not safe for
// concurrent use; a parallel reducer keeps one per partial and merges
// with Merge.
type CompositeKeySet struct {
	width   int
	entries map[string]*compositeEntry
	buf     []byte
	odo     []int
}

// NewCompositeKeySet returns an empty set for width groupers.
func NewCompositeKeySet(width int) *CompositeKeySet {
	return &CompositeKeySet{width: width, entries: map[string]*compositeEntry{}, odo: make([]int, width)}
}

// Len is the number of observed composite keys (what max_groups counts).
func (c *CompositeKeySet) Len() int { return len(c.entries) }

// Tuple returns key's per-grouper tuple (nil for a key never observed).
func (c *CompositeKeySet) Tuple(key string) types.AxisKey {
	if e, ok := c.entries[key]; ok {
		return e.tuple
	}
	return nil
}

// Keys returns every observed composite key, in no particular order.
func (c *CompositeKeySet) Keys() []string {
	out := make([]string, 0, len(c.entries))
	for k := range c.entries {
		out = append(out, k)
	}
	return out
}

// Merge absorbs o's observed keys (a parallel / shard partial's).
func (c *CompositeKeySet) Merge(o *CompositeKeySet) {
	if o == nil {
		return
	}
	for k, e := range o.entries {
		if _, ok := c.entries[k]; !ok {
			c.entries[k] = e
		}
	}
}

// Expand calls emit once per composite key of one record: the
// cartesian product of keySets (keySets[i] is grouper i's keys for the
// record, outer → inner). Nothing is emitted when any position's set is
// empty. A key seen for the first time is interned with its tuple;
// emit receives the interned string, so a repeat combination allocates
// nothing. len(keySets) must equal the set's width.
func (c *CompositeKeySet) Expand(keySets [][]string, emit func(key string) error) error {
	if len(keySets) != c.width {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"composite group key: key sets do not match the grouper count",
			map[string]any{"groupers": c.width, "key_sets": len(keySets)})
	}
	for _, ks := range keySets {
		if len(ks) == 0 {
			return nil
		}
	}
	odo := c.odo
	for i := range odo {
		odo[i] = 0
	}
	for {
		c.buf = c.buf[:0]
		for i, ks := range keySets {
			if i > 0 {
				c.buf = append(c.buf, crosstabAxisKeySep...)
			}
			c.buf = append(c.buf, ks[odo[i]]...)
		}
		e, ok := c.entries[string(c.buf)]
		if !ok {
			tuple := make(types.AxisKey, len(keySets))
			for i, ks := range keySets {
				tuple[i] = ks[odo[i]]
				// A key holding the separator makes the composite
				// string ambiguous ("a\x00b"+"c" = "a"+"b\x00c").
				// Checking at intern time suffices: every interned key
				// is clean, so it carries exactly width-1 separators
				// and a dirty candidate (more) can never hit the map.
				if k := ks[odo[i]]; strings.Contains(k, crosstabAxisKeySep) {
					return errors.NewCodedErrorWithDetails(errors.PROCESSING_GROUP,
						"group["+strconv.Itoa(i)+"] key contains a NUL byte (\\x00), the composite group-key separator; a grouper key must not contain it when two or more groupers are combined",
						map[string]any{"group_index": i, "key": k, "reason": "key_contains_separator"})
				}
			}
			e = &compositeEntry{key: string(c.buf), tuple: tuple}
			c.entries[e.key] = e
		}
		if err := emit(e.key); err != nil {
			return err
		}
		// Advance the odometer, innermost position fastest.
		p := len(odo) - 1
		for ; p >= 0; p-- {
			odo[p]++
			if odo[p] < len(keySets[p]) {
				break
			}
			odo[p] = 0
		}
		if p < 0 {
			return nil
		}
	}
}

// OrderCompositeGroupKeys orders keys (all observed by set) by each
// grouper's native key order, outer → inner — the order the
// single-grouper exit gives each position on its own.
func OrderCompositeGroupKeys(keys []string, set *CompositeKeySet, groupers []Grouper) []string {
	filters := make([]*includeFilter, len(groupers))
	for i, g := range groupers {
		filters[i] = includeFilterOf(g)
	}
	tupleOf := make(map[string]types.AxisKey, len(keys))
	for _, k := range keys {
		tupleOf[k] = set.Tuple(k)
	}
	return orderCompositeKeys(keys, tupleOf, filters)
}

// WriteGroupKeyColumns writes each grouper's key column of a Data row
// from tuple (never from the composite string): column
// vectors.GroupKeyField of groups[i] ← tuple[i].
func WriteGroupKeyColumns(row map[string]any, groups []*types.Group, tuple types.AxisKey) {
	for i, g := range groups {
		if i < len(tuple) {
			row[vectors.GroupKeyField(g.Type, g)] = tuple[i]
		}
	}
}

// newRequestGroupers builds one grouper instance per Request.Groups
// slot, extension hooks applied.
func (p *Processor) newRequestGroupers(groups []*types.Group) ([]Grouper, error) {
	out := make([]Grouper, len(groups))
	for i, grp := range groups {
		factory, ok := p.exts.LookupGrouper(grp.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown group type: %s", grp.Type))
		}
		g, err := factory(grp, p.grouperSchema(grp))
		if err != nil {
			return nil, err
		}
		ApplyGrouperExtensions(g, p.exts)
		out[i] = g
	}
	return out, nil
}

// compositePartition is a buffered multi-grouper partition of the
// filtered records.
type compositePartition struct {
	// Set holds the observed composite keys and their tuples.
	Set *CompositeKeySet
	// Buckets maps composite key → its records, in record order.
	Buckets map[string][]*Record
	// PerGrouper is grouper i's own partition of the whole set — the
	// source of its Components floor.
	PerGrouper []map[string][]*Record
	// RefitWarnings are the per-outer-bucket refits' warnings of every
	// params.fit "within" position (refitWarnings: one per refit, each
	// naming its outer segment as details.group_key), in walk order —
	// a degenerate refit's PULSE_SLOT_FAILED among them.
	RefitWarnings []*types.ResponseWarning
	// RefitBuckets maps a "within" position to its refits' Components
	// entries (GrouperComponents.Buckets), in the outer segments' Data
	// order; nil unless the partition was asked to collect them.
	RefitBuckets map[int][]types.GrouperBucketComponents
}

// withinRefit is how partitionComposite runs a params.fit "within"
// position: refit mints a fresh grouper for position i, strict makes a
// degenerate refit (slotfail.RefitDegenerate) fail the run instead of
// failing its bucket alone, and collect builds each refit's
// GrouperComponents.Buckets entry.
type withinRefit struct {
	refit   func(i int) (Grouper, error)
	strict  bool
	collect bool
}

// degenerateRefit is the one handling of a "within" refit that refused
// one outer bucket's records on their data (slotfail.RefitDegenerate):
// under strict the run fails with the refit's own code; otherwise the
// bucket's records join no bucket of the slot, the slot's Buckets entry
// carries the error, and the run warns PULSE_SLOT_FAILED (slot_kind
// "groups", index = the composite position — a crosstab counts rows
// then columns — label = the slot's key column, group_key = the outer
// segment). ok is false when err is not degenerate (the caller returns
// it as the fatal error it is).
func degenerateRefit(err error, grp *types.Group, pos int, outer types.AxisKey, strict bool) (*types.ResponseWarning, *types.SlotError, bool, error) {
	se, ok := slotfail.RefitDegenerate(err)
	if !ok {
		return nil, nil, false, nil
	}
	f := slotfail.Failure{Kind: slotfail.KindGroups, Index: pos, Label: vectors.GroupKeyField(grp.Type, grp), GroupKey: outer, Err: se}
	if strict {
		return nil, nil, true, slotfail.StrictError(f)
	}
	return slotfail.Warning(f), se, true, nil
}

// refitBucketEntry is one successful "within" refit as its
// GrouperComponents.Buckets entry: the outer segment plus the floor and
// operator keys buildGrouperComponents gives the same grouper over the
// segment's records.
func refitBucketEntry(g Grouper, grp *types.Group, outer types.AxisKey, part map[string][]*Record, n int) (types.GrouperBucketComponents, error) {
	c, err := buildGrouperComponents(g, grp, part, n)
	if err != nil {
		return types.GrouperBucketComponents{}, err
	}
	return types.GrouperBucketComponents{GroupKey: outer, TotalN: c.TotalN, NNull: c.NNull, Operator: c.Operator}, nil
}

// orderRefitBuckets orders one "within" position's Buckets entries by
// their outer segments in Data order: each outer position's native key
// order (includeFilterOf the whole-set grouper), outer → inner — the
// order OrderCompositeGroupKeys gives the rows. entries is not mutated.
func orderRefitBuckets(entries []types.GrouperBucketComponents, outer []Grouper) []types.GrouperBucketComponents {
	filters := make([]*includeFilter, len(outer))
	for i, g := range outer {
		filters[i] = includeFilterOf(g)
	}
	keys := make([]string, len(entries))
	tupleOf := make(map[string]types.AxisKey, len(entries))
	for i, e := range entries {
		keys[i] = strconv.Itoa(i)
		tupleOf[keys[i]] = e.GroupKey
	}
	keys = sortCompositeKeys(keys, tupleOf, filters)
	out := make([]types.GrouperBucketComponents, len(entries))
	for i, k := range keys {
		j, _ := strconv.Atoi(k)
		out[i] = entries[j]
	}
	return out
}

// partitionComposite drives every grouper exactly once over the WHOLE
// record set (so a data-dependent grouper — GROUP_QUANTILE,
// GROUP_KMEANS — is fitted once under params.fit "global", the default,
// whatever its position, and a record's key equals its single-grouper
// key), then crosses the per-record key sets into composite buckets.
// lim is checked against the observed composite count as each new key
// appears.
//
// A position after the first whose grouper asks for params.fit
// "within" (internal/groupfit) is refitted inside each outer-prefix
// bucket instead — a fresh instance from refit per bucket — through
// partitionCompositeWithin; its whole-set fit still sources the slot's
// Components, its unstamped warnings and key order, and every refit's
// warnings join RefitWarnings stamped with their outer segment.
// "within" on groups[0] has no outer segment and is the whole-set fit.
// A degenerate refit fails its bucket alone (degenerateRefit); with
// within.collect each refit's figures land in RefitBuckets.
func partitionComposite(groups []*types.Group, groupers []Grouper, records []*Record, lim limits.Limits, within withinRefit) (*compositePartition, error) {
	n := len(groups)
	perGrouper := make([]map[string][]*Record, n)
	hasWithin := false
	for i, grp := range groups {
		b, err := groupers[i].Group(records, grp.Field)
		if err != nil {
			return nil, err
		}
		perGrouper[i] = b
		if i > 0 && groupfit.IsWithin(grp) {
			hasWithin = true
		}
	}

	// Per-position, per-record key sets. Most groupers give one key per
	// record: first holds it, more any fan-out extras.
	idx := make(map[*Record]int, len(records))
	for i, r := range records {
		idx[r] = i
	}
	pos := make([]compositePosition, n)
	for i := range pos {
		pos[i] = compositePosition{first: make([]string, len(records)), has: make([]bool, len(records))}
		for key, recs := range perGrouper[i] {
			for _, r := range recs {
				ri, ok := idx[r]
				if !ok {
					continue
				}
				if !pos[i].has[ri] {
					pos[i].first[ri], pos[i].has[ri] = key, true
					continue
				}
				if pos[i].more == nil {
					pos[i].more = map[int][]string{}
				}
				pos[i].more[ri] = append(pos[i].more[ri], key)
			}
		}
	}

	if hasWithin {
		return partitionCompositeWithin(groups, groupers, records, lim, within, pos, perGrouper)
	}

	set := NewCompositeKeySet(n)
	out := &compositePartition{Set: set, Buckets: map[string][]*Record{}, PerGrouper: perGrouper}
	keySets := make([][]string, n)
	scratch := make([][1]string, n)
	for ri, r := range records {
		for i := range pos {
			if !pos[i].has[ri] {
				keySets[i] = nil
				continue
			}
			if extra, ok := pos[i].more[ri]; ok {
				ks := make([]string, 0, 1+len(extra))
				keySets[i] = append(append(ks, pos[i].first[ri]), extra...)
				continue
			}
			scratch[i][0] = pos[i].first[ri]
			keySets[i] = scratch[i][:]
		}
		err := set.Expand(keySets, func(key string) error {
			recs, seen := out.Buckets[key]
			if !seen {
				if err := limits.Check(lim, limits.MaxGroups, int64(len(out.Buckets))+1); err != nil {
					return err
				}
			}
			out.Buckets[key] = append(recs, r)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// compositePosition is one grouper position's per-record key set over
// the whole record set (indexed by record position): first holds the
// first key, more any fan-out extras.
type compositePosition struct {
	first []string
	has   []bool
	more  map[int][]string
}

// keys returns record ri's keys at this position (nil = none).
func (c *compositePosition) keys(ri int) []string {
	if !c.has[ri] {
		return nil
	}
	if extra, ok := c.more[ri]; ok {
		return append([]string{c.first[ri]}, extra...)
	}
	return []string{c.first[ri]}
}

// partitionCompositeWithin is partitionComposite when some position
// after the first refits within its outer segment: it walks the
// positions outer → inner over prefix buckets (each in record order),
// keying a "global" position from its whole-set key set (pos) and a
// "within" position from a fresh fit (refit) over that prefix bucket's
// records only, so the inner key depends on the outer tuple. The
// leaves become the composite buckets — same key strings, tuples, NUL
// guard and max_groups check as the cross-product path.
//
// Each refit's figures become its GrouperComponents.Buckets entry
// (within.collect); a degenerate refit fails its bucket alone
// (degenerateRefit): an error entry, no key for its records.
func partitionCompositeWithin(groups []*types.Group, groupers []Grouper, records []*Record, lim limits.Limits, within withinRefit, pos []compositePosition, perGrouper []map[string][]*Record) (*compositePartition, error) {
	idx := make(map[*Record]int, len(records))
	for i, r := range records {
		idx[r] = i
	}
	type prefix struct {
		tuple []string
		recs  []*Record
	}
	level := []prefix{{recs: records}}
	var refitWarns []*types.ResponseWarning
	var buckets map[int][]types.GrouperBucketComponents
	for i, grp := range groups {
		var next []prefix
		for _, b := range level {
			var keysOf func(r *Record) []string
			if i > 0 && groupfit.IsWithin(grp) {
				g, err := within.refit(i)
				if err != nil {
					return nil, err
				}
				outer := stringsAxisKey(b.tuple)
				m, err := g.Group(b.recs, grp.Field)
				if err != nil {
					w, se, ok, fatal := degenerateRefit(err, grp, i, outer, within.strict)
					if !ok {
						return nil, err
					}
					if fatal != nil {
						return nil, fatal
					}
					refitWarns = append(refitWarns, w)
					if within.collect {
						if buckets == nil {
							buckets = map[int][]types.GrouperBucketComponents{}
						}
						buckets[i] = append(buckets[i], types.GrouperBucketComponents{GroupKey: outer, NNull: len(b.recs), Error: se})
					}
					continue // its records join no bucket of the slot
				}
				refitWarns = append(refitWarns, refitWarnings(g, outer)...)
				if within.collect {
					e, err := refitBucketEntry(g, grp, outer, m, len(b.recs))
					if err != nil {
						return nil, err
					}
					if buckets == nil {
						buckets = map[int][]types.GrouperBucketComponents{}
					}
					buckets[i] = append(buckets[i], e)
				}
				local := make(map[*Record][]string, len(b.recs))
				for k, recs := range m {
					for _, r := range recs {
						local[r] = append(local[r], k)
					}
				}
				keysOf = func(r *Record) []string { return local[r] }
			} else {
				keysOf = func(r *Record) []string { return pos[i].keys(idx[r]) }
			}
			child := map[string]int{}
			for _, r := range b.recs {
				for _, k := range keysOf(r) {
					ci, ok := child[k]
					if !ok {
						ci = len(next)
						child[k] = ci
						tuple := make([]string, len(b.tuple)+1)
						copy(tuple, b.tuple)
						tuple[len(b.tuple)] = k
						next = append(next, prefix{tuple: tuple})
					}
					next[ci].recs = append(next[ci].recs, r)
				}
			}
		}
		level = next
	}

	set := NewCompositeKeySet(len(groups))
	for i, entries := range buckets {
		buckets[i] = orderRefitBuckets(entries, groupers[:i])
	}
	out := &compositePartition{Set: set, Buckets: map[string][]*Record{}, PerGrouper: perGrouper, RefitWarnings: refitWarns, RefitBuckets: buckets}
	keySets := make([][]string, len(groups))
	for _, leaf := range level {
		for i, k := range leaf.tuple {
			keySets[i] = []string{k}
		}
		err := set.Expand(keySets, func(key string) error {
			if _, seen := out.Buckets[key]; !seen {
				if err := limits.Check(lim, limits.MaxGroups, int64(len(out.Buckets))+1); err != nil {
					return err
				}
			}
			out.Buckets[key] = leaf.recs
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// processGroupedComposite is the buffered grouped exit for two or more
// groupers: one Data row per OBSERVED composite key, in each grouper's
// native order outer → inner, every key column written from the tuple;
// one Components.Groupers entry per grouper slot, each from that
// grouper's own whole-set partition. The returned set carries the
// tuples behind each groups[] GroupKey.
func (p *Processor) processGroupedComposite(req *types.Request, records []*Record, folds bucketFolds) ([]map[string]any, []types.GrouperComponents, map[string][]types.AggregationComponents, []string, *CompositeKeySet, []*types.ResponseWarning, error) {
	groupers, err := p.newRequestGroupers(req.Groups)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	part, err := partitionComposite(req.Groups, groupers, records, p.limits, withinRefit{
		refit: func(i int) (Grouper, error) {
			gs, err := p.newRequestGroupers(req.Groups[i : i+1])
			if err != nil {
				return nil, err
			}
			return gs[0], nil
		},
		strict:  p.strictWeights,
		collect: p.compute.Groupers,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	var warns []*types.ResponseWarning
	for _, g := range groupers {
		warns = append(warns, GrouperWarnings(g)...)
	}
	warns = append(warns, part.RefitWarnings...)
	keys := OrderCompositeGroupKeys(part.Set.Keys(), part.Set, groupers)
	if err := folds.tests.admit(len(keys)); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	collect := p.compute.Groups && len(req.Aggregations) > 0
	var bucketComps map[string][]types.AggregationComponents
	if collect {
		bucketComps = make(map[string][]types.AggregationComponents, len(keys))
	}
	data := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		recs := part.Buckets[key]
		row, comps, err := p.aggregateWithComponents(req.Aggregations, recs, collect)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		if collect {
			bucketComps[key] = comps
		}
		if row == nil {
			row = make(map[string]any, len(req.Groups))
		}
		WriteGroupKeyColumns(row, req.Groups, part.Set.Tuple(key))
		data = append(data, row)
		if err := folds.foldRecords(p, key, part.Set.Tuple(key), recs); err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
	}

	if !p.compute.Groupers {
		return data, nil, bucketComps, keys, part.Set, warns, nil
	}
	entries := make([]types.GrouperComponents, 0, len(groupers))
	for i, g := range groupers {
		entry, err := buildGrouperComponents(g, req.Groups[i], part.PerGrouper[i], len(records))
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		// A "within" position's per-refit figures (the slot-level floor
		// and operator keys above stay the whole-set fit's).
		entry.Buckets = part.RefitBuckets[i]
		entries = append(entries, entry)
	}
	return data, entries, bucketComps, keys, part.Set, warns, nil
}
