package processing

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
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
		g, err := factory(grp, p.schema)
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
}

// partitionComposite drives every grouper exactly once over the WHOLE
// record set (so a data-dependent grouper — GROUP_QUANTILE,
// GROUP_KMEANS — is fitted once, whatever its position, and a record's
// key equals its single-grouper key), then crosses the per-record key
// sets into composite buckets. lim is checked against the observed
// composite count as each new key appears.
func partitionComposite(groups []*types.Group, groupers []Grouper, records []*Record, lim limits.Limits) (*compositePartition, error) {
	n := len(groups)
	perGrouper := make([]map[string][]*Record, n)
	for i, grp := range groups {
		b, err := groupers[i].Group(records, grp.Field)
		if err != nil {
			return nil, err
		}
		perGrouper[i] = b
	}

	// Per-position, per-record key sets. Most groupers give one key per
	// record: first holds it, more any fan-out extras.
	idx := make(map[*Record]int, len(records))
	for i, r := range records {
		idx[r] = i
	}
	type position struct {
		first []string
		has   []bool
		more  map[int][]string
	}
	pos := make([]position, n)
	for i := range pos {
		pos[i] = position{first: make([]string, len(records)), has: make([]bool, len(records))}
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

// processGroupedComposite is the buffered grouped exit for two or more
// groupers: one Data row per OBSERVED composite key, in each grouper's
// native order outer → inner, every key column written from the tuple;
// one Components.Groupers entry per grouper slot, each from that
// grouper's own whole-set partition. The returned set carries the
// tuples behind each groups[] GroupKey.
func (p *Processor) processGroupedComposite(req *types.Request, records []*Record, matrices *GroupedMatrices) ([]map[string]any, []types.GrouperComponents, map[string][]types.AggregationComponents, []string, *CompositeKeySet, []*types.ResponseWarning, error) {
	groupers, err := p.newRequestGroupers(req.Groups)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	part, err := partitionComposite(req.Groups, groupers, records, p.limits)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	var warns []*types.ResponseWarning
	for _, g := range groupers {
		warns = append(warns, GrouperWarnings(g)...)
	}
	keys := OrderCompositeGroupKeys(part.Set.Keys(), part.Set, groupers)

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
		if err := matrices.foldRecords(key, recs); err != nil {
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
		entries = append(entries, entry)
	}
	return data, entries, bucketComps, keys, part.Set, warns, nil
}
