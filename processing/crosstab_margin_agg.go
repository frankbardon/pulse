package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// This file is the BUFFERED path's half of auxiliary margin-only
// aggregations (types.CrosstabSpec.MarginAggregations). The fused half
// lives in crosstab_fused.go (rowMarginAux / colMarginAux /
// grandMarginAux + updateAuxMargins).
//
// THE TWO HALVES MUST AGREE, AND NOTHING IN Response SAYS WHICH RAN.
// service/crosstab.go picks fused or buffered on request SHAPE — a
// non-mergeable cell aggregator, a GROUP_QUANTILE axis, a feature, a
// FILTER_EXPRESSION. None of those has anything to do with sample size,
// so an auxiliary implemented on one arm only produces a figure that
// appears, vanishes or changes for reasons a caller cannot see and
// cannot ask about. TestCrosstab_BufferedAuxMarginMatchesFused drives
// one request down both arms; keep it green.
//
// The realistic buffered caller is not hypothetical: AGG_WELFORD and
// AGG_MEDIAN are non-mergeable, so they force this path by
// construction, and two BERA stat-test families are built on
// AGG_WELFORD.

// crosstabAuxMargins carries the buffered path's auxiliary margin
// figures — the counterpart of the fused state's rowMarginAux /
// colMarginAux / grandMarginAux triple.
//
// The two shapes differ deliberately. The fused walk holds LIVE online
// accumulators because it sees each record once and can never look
// again; the buffered path already has every record in hand, so it
// aggregates each slot in one shot and holds the FINISHED figure. What
// must match is the numbers, not the plumbing.
//
// Every slice is indexed in DECLARATION ORDER — position i is
// spec.MarginAggregations[i], and Labels[i] is its effective label.
// Margin components are keyed by label, so a Labels slice that drifted
// out of step with the figures would mislabel each figure rather than
// drop one: silent, and pinned by
// TestCrosstab_BufferedAuxMarginLabelsTrackDeclarationOrder.
//
// Rows / Cols are keyed by the same composite axis keys
// CrosstabAxisPartition.Keys carries, so a consumer addresses an
// auxiliary slot with the key it already used for the cell aggregator's
// own margin. A slot the spec did not ask for stays nil; the whole
// carrier is nil when no auxiliary is declared.
type crosstabAuxMargins struct {
	Labels []string
	Rows   map[string][]auxMarginFigure
	Cols   map[string][]auxMarginFigure
	Grand  []auxMarginFigure
}

// auxMarginFigure is one auxiliary aggregation's finished figure in one
// margin slot: the aggregator's own output plus the universal-floor
// counters over the records ADMITTED to that slot.
//
// Present is false when the slot admitted no record at all, which is the
// buffered mirror of the fused path's nil accumulator — the slot exists
// (some record resolved that axis key) but nothing reached it. It is
// deliberately not conflated with a zero Value: an aggregator over an
// empty set has no defined output, and inventing one would put a
// fabricated base beside real cells.
//
// N / NNull tile the ADMITTED record count for this auxiliary's own
// Field, the same split buildCellComponentMap merges under {n, n_null}.
type auxMarginFigure struct {
	Value   any
	Present bool
	N       int
	NNull   int
}

// computeAuxMargins evaluates every declared auxiliary margin
// aggregation into the row / column / grand margin slots the spec asks
// for. Returns nil when the spec declares no auxiliary — the slot costs
// an undeclared request nothing, not even a map header.
//
// THE ADMISSION RULE. An auxiliary observes the SAME record admission as
// the cell aggregator: a record contributes only if it contributed to a
// CELL. Three exclusions follow, identical to the fused path's:
//
//   - a record whose CELL FIELD IS NULL — it occupies a cell slot and is
//     counted in that cell's n_null, but contributes no value, so it is
//     no part of the base an auxiliary reports;
//   - a record whose ROW AXIS KEY did not resolve — including one a
//     grouper Include excluded, which on this path means the record is
//     absent from every bucket of rowPart (KeyFor returns
//     ErrGrouperKeyNull for an excluded key and streamableGroup skips
//     it);
//   - a record whose COLUMN AXIS KEY did not resolve, by the same rule.
//
// THIS IS DELIBERATELY NOT HOW THE CELL AGGREGATOR'S OWN MARGINS BEHAVE,
// and the loops above in RunCrosstab are left exactly as they were: they
// iterate rowPart.Records[rkey] / colPart.Records[ckey] / filtered
// whole, so a column margin counts every filter-passing record routed to
// that column whatever happened on the other axis. Reusing that here
// would make the figure cohort-wide and metric-agnostic — a "sample
// size" describing every respondent in the cohort rather than the
// respondents who could contribute to a cell beside it. Do not align the
// two.
//
// GETTING IT WRONG IS COMPLETELY SILENT. Every number still renders, the
// base is merely wrong, and nothing throws or warns.
//
// WHY MEMBERSHIP SETS RATHER THAN THE CELL BUCKETS. The obvious
// implementation — walk the (rkey, ckey) buckets RunCrosstab already
// builds, since a bucket is by definition admitted on both axes — is
// wrong on exactly one shape and right everywhere else: it would fold a
// record into a ROW auxiliary once per COLUMN it landed in, M times
// under a column fan-out, where the fused walk folds it once per
// distinct row key. GROUP_SET_PER_ELEMENT is precisely the axis this
// slot exists to serve. So the two axes' membership is resolved ONCE
// into pointer sets and each margin slot is then walked on its own axis,
// exactly as the existing margin loops are.
func (p *Processor) computeAuxMargins(spec *types.CrosstabSpec, filtered []*Record,
	rowPart, colPart *CrosstabAxisPartition) (*crosstabAuxMargins, error) {

	if !spec.HasMarginAggregations() {
		return nil, nil
	}

	// Resolve every auxiliary against the registry BEFORE deciding
	// whether any slot is observed. NewFusedCrosstabState resolves
	// unconditionally too, so an auxiliary naming an operator no registry
	// knows is refused on both arms rather than on whichever one the
	// request happened to dispatch to. Refused rather than dropped, for
	// E2-S1's stated reason: silently skipping it returns a margin with
	// the requested figure missing and nothing saying so.
	for _, aux := range spec.MarginAggregations {
		if _, ok := p.exts.LookupAggregator(aux.Type); ok {
			continue
		}
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown crosstab margin aggregation type: %s", aux.Type),
			map[string]any{"aggregation": string(aux.Type), "slot": "margin_aggregations"})
	}

	out := &crosstabAuxMargins{Labels: spec.MarginAggregationLabels()}

	needRow := spec.NeedsRowMargin()
	needCol := spec.NeedsColumnMargin()
	needGrand := spec.NeedsGrandMargin()
	if !needRow && !needCol && !needGrand {
		// Structurally legal — predict warns
		// (PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED) rather than refusing —
		// and there is genuinely nowhere for a figure to land. Mirrors
		// the fused path allocating no slot in the same case.
		return out, nil
	}

	// Axis membership, resolved once. A record is a member of an axis
	// when it resolved at least one key on it; the partition's own
	// buckets ARE that answer, since a record whose key was null (or
	// Include-excluded) reached no bucket. Built only for the axis a
	// requested slot actually cross-checks against: a row slot walks the
	// row axis and needs to know about columns, and vice versa.
	var rowMember, colMember map[*Record]struct{}
	if needCol || needGrand {
		rowMember = axisMembership(rowPart)
	}
	if needRow || needGrand {
		colMember = axisMembership(colPart)
	}

	cellField := ""
	if spec.Cell != nil {
		cellField = spec.Cell.Field
	}

	if needRow {
		out.Rows = make(map[string][]auxMarginFigure, len(rowPart.Keys))
		for _, rkey := range rowPart.Keys {
			bucket := rowPart.Records[rkey]
			if len(bucket) == 0 {
				continue
			}
			figures, err := p.auxFiguresFor(spec, admitRecords(bucket, colMember, nil, cellField))
			if err != nil {
				return nil, err
			}
			out.Rows[rkey] = figures
		}
	}

	if needCol {
		out.Cols = make(map[string][]auxMarginFigure, len(colPart.Keys))
		for _, ckey := range colPart.Keys {
			bucket := colPart.Records[ckey]
			if len(bucket) == 0 {
				continue
			}
			figures, err := p.auxFiguresFor(spec, admitRecords(bucket, rowMember, nil, cellField))
			if err != nil {
				return nil, err
			}
			out.Cols[ckey] = figures
		}
	}

	if needGrand {
		figures, err := p.auxFiguresFor(spec, admitRecords(filtered, rowMember, colMember, cellField))
		if err != nil {
			return nil, err
		}
		out.Grand = figures
	}

	return out, nil
}

// axisMembership collects every record that resolved at least one key on
// an axis. Identity is the pointer: RunCrosstab partitions the SAME
// *Record values it was handed, so a record is the same pointer in both
// partitions and in `filtered`.
func axisMembership(part *CrosstabAxisPartition) map[*Record]struct{} {
	if part == nil {
		return nil
	}
	// Sized against the largest bucket rather than the total, which is
	// only a hint: a fan-out axis holds a record in several buckets, so
	// summing them over-allocates by the fan factor.
	capacity := 0
	for _, recs := range part.Records {
		if len(recs) > capacity {
			capacity = len(recs)
		}
	}
	out := make(map[*Record]struct{}, capacity)
	for _, recs := range part.Records {
		for _, r := range recs {
			out[r] = struct{}{}
		}
	}
	return out
}

// admitRecords narrows a margin slot's routed bucket to the records that
// reached a CELL: present in each supplied membership set (a nil set is
// "this axis is not cross-checked here", which is how a row slot skips
// re-testing the row axis it is already walking) and carrying a non-null
// cell field.
//
// The null probe is Record.NumericValue against the cell field, which is
// the SAME EXPRESSION FusedCrosstabState.Update reads cellValuePresent
// from. That is deliberate rather than incidental: writing an equivalent
// probe here — Record.IsNull, say — would be a second definition of
// "the cell had a value", and the two would diverge the day one field
// type stops agreeing with the other.
//
// Returns the bucket ITSELF when every record is admitted, which is the
// common case on a crosstab with no Include and no nullable cell field,
// so the usual request pays no copy at all.
func admitRecords(bucket []*Record, memberA, memberB map[*Record]struct{}, cellField string) []*Record {
	admitted := func(r *Record) bool {
		if memberA != nil {
			if _, ok := memberA[r]; !ok {
				return false
			}
		}
		if memberB != nil {
			if _, ok := memberB[r]; !ok {
				return false
			}
		}
		_, ok := r.NumericValue(cellField)
		return ok
	}

	rejected := -1
	for i, r := range bucket {
		if !admitted(r) {
			rejected = i
			break
		}
	}
	if rejected < 0 {
		return bucket
	}

	out := make([]*Record, rejected, len(bucket)-1)
	copy(out, bucket[:rejected])
	for _, r := range bucket[rejected+1:] {
		if admitted(r) {
			out = append(out, r)
		}
	}
	return out
}

// auxFiguresFor evaluates every declared auxiliary over one slot's
// ADMITTED records, in declaration order.
//
// The admitted set is computed once per SLOT and shared across the
// auxiliaries in it — admission is a property of the record and the cell
// field, not of which auxiliary is reading it.
//
// Each auxiliary rides runCellAggregation, the same helper the cell and
// its own margins use, so an auxiliary inherits the decimal128 dispatch
// and the {n, n_null} floor for free. Handing it the admitted subset
// rather than the routed bucket is what makes that floor the ADMITTED
// count, matching what the fused path accumulates record by record.
//
// An empty admitted set yields a figure with Present false and no
// aggregation run at all — the mirror of the fused path's accumulator
// that was never constructed, and the reason an aggregator is never
// asked for its value over zero records.
func (p *Processor) auxFiguresFor(spec *types.CrosstabSpec, admitted []*Record) ([]auxMarginFigure, error) {
	figures := make([]auxMarginFigure, len(spec.MarginAggregations))
	if len(admitted) == 0 {
		return figures, nil
	}
	for i, aux := range spec.MarginAggregations {
		val, _, n, nNull, err := p.runCellAggregation(aux, admitted)
		if err != nil {
			return nil, err
		}
		figures[i] = auxMarginFigure{
			Value:   val.value,
			Present: val.present,
			N:       n,
			NNull:   nNull,
		}
	}
	return figures, nil
}
