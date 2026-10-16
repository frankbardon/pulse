package processing

import "github.com/frankbardon/pulse/types"

// Normalise-denominator floors for the weighted pairwise proportion
// overlays (U47 #324; .claude/reference/weighting.md, Overlays).
//
// On a normalised crosstab a cell's value is Σw_cell / Σw_base, where the
// base is the denominator normalize divided it by: the row margin
// (normalize row), the column margin (column), the grand margin (total),
// the depth-truncated prefix margin (normalize_level) or the cross-axis
// slab (normalize_within). A weighted proportion test pairs that p̂ with
// the SAME base's N* — Σw under kind frequency, Kish n_eff under kind
// probability — never the cell's own N*, which is a sliver of the base
// and drives every p-value far too high.
//
// An unweighted host follows the same rule on counts: the base's n (its
// present rows), so a frequency-weighted host still equals the
// physically expanded cohort and a unity weight equals no weight.
//
// The denominators exist on both crosstab arms whatever the display
// flags say, but only DISPLAYED leaf margins reach Response.Components,
// and the partial-depth / cross-axis ones never do. So each arm hands
// the overlay host this processing-internal carrier beside the
// response: per host cell, the n and weighted floor of the base that
// cell was divided by. It never reaches the wire (Response.Components
// is unchanged, byte-identical) and is built only under a normalize
// mode on a run that folds overlays.
//
// Every base is the ADMITTED one (U47 #325): its records reached a
// cell, exactly as the denominator the cell value was divided by.

// crosstabBaseFloors is one host's per-cell base floors.
type crosstabBaseFloors struct {
	// floors holds each distinct base's floor map: `n` (its present
	// rows — the cell slot's presence rule, as a margin's floor counts
	// them) plus, on a weighted cell slot, sum_weights,
	// n_weight_invalid and, under kind probability, n_eff (the
	// WeightFloor.stampMap keys), so floorLeg reads it like a margin's
	// components.
	floors []map[string]any
	// cell[r][c] indexes floors in MatrixPayload RowKeys × ColumnKeys
	// order; -1 when the cell had no denominator.
	cell [][]int
}

// at returns cell (r, c)'s base floor and that base's identity (its
// floors index, shared by every cell divided by the same base).
func (b *crosstabBaseFloors) at(r, c int) (map[string]any, int, bool) {
	if b == nil || r < 0 || r >= len(b.cell) || c < 0 || c >= len(b.cell[r]) {
		return nil, 0, false
	}
	id := b.cell[r][c]
	if id < 0 {
		return nil, 0, false
	}
	return b.floors[id], id, true
}

// baseTally is one denominator's floor: its present-row count and its
// weighted floor (inert on an unweighted cell slot).
type baseTally struct {
	n int
	w WeightFloor
}

// observe folds one admitted record into the tally (the fused walk;
// present is the record's cell-slot presence, probed once per record).
func (t *baseTally) observe(r *Record, cellAgg *types.Aggregation, present bool) {
	if present {
		t.n++
	}
	t.w.ObserveSlot(r, cellAgg)
}

// baseFloorSource is what each crosstab arm collected for the builder:
// the floors of its normalise denominators keyed exactly as its
// normalisation pass looks them up. Both arms fill it from the same
// admitted records in the same order, so the floors agree bit for bit.
type baseFloorSource struct {
	mode types.CrosstabNormalize

	// rows / cols are the row / column denominators (normalize row /
	// column, no normalize_within), keyed by the leaf composite key or —
	// under normalize_level — by the truncated prefix key, mapped from
	// the leaf through leafRowToPartial / leafColToPartial (nil: the
	// leaf IS the key).
	rows             map[string]baseTally
	leafRowToPartial map[string]string
	cols             map[string]baseTally
	leafColToPartial map[string]string

	// grand is the normalize total denominator.
	grand   baseTally
	grandOK bool

	// cross is the normalize_within denominator, keyed by the
	// (row prefix, column prefix) pair the leaf keys map to.
	crossActive bool
	cross       map[crosstabCellKey]baseTally
	crossRow    map[string]string
	crossCol    map[string]string
}

// build lays the denominators out per host cell, in the payload's
// rowKeys × colKeys order. nil for normalize none (the cell is its own
// base and the host reads the cell floor).
func (s *baseFloorSource) build(rowKeys, colKeys []string) *crosstabBaseFloors {
	if s == nil || s.mode == "" || s.mode == types.CrosstabNormalizeNone {
		return nil
	}
	out := &crosstabBaseFloors{cell: make([][]int, len(rowKeys))}
	ids := map[string]int{}
	intern := func(id string, f baseTally) int {
		if i, ok := ids[id]; ok {
			return i
		}
		m := map[string]any{"n": f.n}
		f.w.stampMap(m)
		ids[id] = len(out.floors)
		out.floors = append(out.floors, m)
		return ids[id]
	}
	mapped := func(m map[string]string, k string) string {
		if m == nil {
			return k
		}
		return m[k]
	}
	for r, rk := range rowKeys {
		out.cell[r] = make([]int, len(colKeys))
		for c, ck := range colKeys {
			id := -1
			switch {
			case s.crossActive:
				key := crosstabCellKey{row: s.crossRow[rk], col: s.crossCol[ck]}
				if f, ok := s.cross[key]; ok {
					id = intern("x\x00"+key.row+"\x01"+key.col, f)
				}
			case s.mode == types.CrosstabNormalizeRow:
				k := mapped(s.leafRowToPartial, rk)
				if f, ok := s.rows[k]; ok {
					id = intern("r\x00"+k, f)
				}
			case s.mode == types.CrosstabNormalizeColumn:
				k := mapped(s.leafColToPartial, ck)
				if f, ok := s.cols[k]; ok {
					id = intern("c\x00"+k, f)
				}
			case s.mode == types.CrosstabNormalizeTotal:
				if s.grandOK {
					id = intern("g", s.grand)
				}
			}
			out.cell[r][c] = id
		}
	}
	return out
}

// bucketBaseTally tallies one denominator bucket's floor the way
// runCellAggregation tallies a margin's: every record under the cell
// slot's presence rule, in bucket order.
func bucketBaseTally(cellAgg *types.Aggregation, bucket []*Record) baseTally {
	t := baseTally{w: NewWeightFloor(cellAgg)}
	for _, r := range bucket {
		if AggregationPresent(r, cellAgg) {
			t.n++
		}
		t.w.ObserveSlot(r, cellAgg)
	}
	return t
}
