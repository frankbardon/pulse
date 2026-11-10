// Package orderedcat holds the ordered-categorical rule of the four
// order-statistic aggregators — AGG_MIN, AGG_MAX, AGG_MEDIAN and
// AGG_PERCENTILE — shared by the runtime constructors
// (internal/processing) and predict (internal/descriptor), so the two
// refuse the same request with the same code, message and details by
// construction (U66 E9-S2, #718; decision D32).
//
// The rule is opt-in. params.ordered true reads a categorical_* field's
// dictionary order as its order; params.levels lists the order itself,
// lowest first, by label (and implies ordered). Without either the slot
// is exactly what it was: a categorical field is the accepted-types
// table's PULSE_FIELD_TYPE_UNSUPPORTED (internal/fieldaccept), every
// other field its numeric figure.
//
// With either, the slot reads level RANKS: no interpolation; MIN / MAX
// the lowest / highest level a row carries; MEDIAN / PERCENTILE R's
// quantile(type = 1) over the ranks — the lowest level whose cumulative
// share reaches p (MEDIAN p = 0.5, PERCENTILE p = percentile / 100;
// p = 0 is the MIN level). Weighted (MEDIAN / PERCENTILE only — MIN and
// MAX are not weightable): the lowest level whose cumulative Σw reaches
// p·Σw. A row whose label params.levels does not list is dropped from
// the figure and counted (n_unlisted); a null row stays n_null. The
// figure is the chosen level's DICTIONARY INDEX — a code, exactly as
// AGG_MODE over a categorical — so every code-ness rule (the overlay
// categorical_code refusal, the chain forwarded_code refusal) applies
// to it (Requested is the slot-level predicate those rules read).
//
// It is a leaf: it imports only the public encoding / errors / types
// packages, internal/fieldaccept and internal/paramread, never
// internal/processing or internal/service, so internal/descriptor may
// import it without breaking TestPredictNoExecutionImports.
package orderedcat

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// The params keys.
const (
	ParamOrdered = "ordered"
	ParamLevels  = "levels"
)

// The refusal reasons (details.reason), each PROCESSING_CONFIG.
const (
	// ReasonNeedsCategorical: ordered / levels on a field that is not a
	// categorical_* schema field.
	ReasonNeedsCategorical = "ordered_needs_categorical"
	// ReasonUnknownLevel: a params.levels entry the field's dictionary
	// does not carry.
	ReasonUnknownLevel = "unknown_level"
	// ReasonDuplicateLevel: a params.levels entry listed twice.
	ReasonDuplicateLevel = "duplicate_level"
	// ReasonEmptyLevels: params.levels present but empty.
	ReasonEmptyLevels = "empty_levels"
	// ReasonOrderedFalse: params.levels beside an explicit
	// params.ordered false (levels implies ordered).
	ReasonOrderedFalse = "ordered_false_with_levels"
	// ReasonInvalidParams: params that do not decode (ordered not a
	// bool, levels not a list of strings).
	ReasonInvalidParams = "invalid_params"
)

// Params is the typed params decode both sides share.
type Params struct {
	// Ordered reads the dictionary order as the level order.
	Ordered *bool `json:"ordered"`
	// Levels is the level order, lowest first, by label; implies
	// ordered.
	Levels []string `json:"levels"`
}

// Applies reports whether op reads the ordered-categorical params.
func Applies(op types.AggregationType) bool {
	switch op {
	case types.AGG_MIN, types.AGG_MAX, types.AGG_MEDIAN, types.AGG_PERCENTILE:
		return true
	}
	return false
}

// Requested reports whether op's params ask for the ordered reading: op
// Applies and the params object carries a non-null levels, or an
// ordered other than false / null. A lenient look (paramread.Probe):
// params that are not an object are left to the other readers; an
// ordered / levels value of the wrong type still counts as requested,
// so Resolve refuses it rather than the slot silently reading numbers.
func Requested(op types.AggregationType, raw json.RawMessage) bool {
	if !Applies(op) || len(raw) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if paramread.Probe(raw, &m) != nil {
		return false
	}
	if v, ok := m[ParamLevels]; ok && string(v) != "null" {
		return true
	}
	if v, ok := m[ParamOrdered]; ok && string(v) != "null" && string(v) != "false" {
		return true
	}
	return false
}

// Spec is a resolved ordered slot: the rank of every dictionary code
// and the code of every rank.
type Spec struct {
	// Field is the categorical field the slot reads.
	Field string
	// Rank maps a dictionary code to its rank, -1 for a code
	// params.levels does not list.
	Rank []int
	// Code maps a rank to its dictionary code.
	Code []int
	// Levels are the level labels in rank order.
	Levels []string
	// field is the schema field (its dictionary), for the corrupt-index
	// check the runtime makes.
	field *encoding.Field
}

// SchemaField returns the categorical schema field the spec reads.
func (s *Spec) SchemaField() *encoding.Field { return s.field }

// Resolve decodes op's params and returns the ordered spec — nil when op
// does not apply, the params do not request the ordered reading, or
// schema is nil (a registry probe: not judged) or field is a vector
// (the field-reference walk's PULSE_VECTOR_FIELD_NOT_SCALAR owns it) —
// or the refusal the runtime constructor and predict both raise. A
// decimal128 field on an operator with no decimal arm (MEDIAN /
// PERCENTILE) is the accepted-types table's decimal refusal, first, as
// the runtime's decimal dispatch raises it before any constructor.
func Resolve(op types.AggregationType, field string, raw json.RawMessage, schema *encoding.Schema) (*Spec, *errors.CodedError) {
	if !Applies(op) {
		return nil, nil
	}
	name := string(op)
	paramread.Declare(paramread.Aggregator, name, (*Params)(nil))
	if !Requested(op, raw) || schema == nil {
		return nil, nil
	}
	f := schema.Field(field)
	if f != nil && f.Type.IsVector() {
		return nil, nil
	}
	if f != nil && f.Type.IsDecimal() && !fieldaccept.HasDecimalArm(name) {
		return nil, fieldaccept.Refusal(name, fieldaccept.SlotField, field, f.Type)
	}
	var p Params
	if err := paramread.DecodeOptional(paramread.Aggregator, name, raw, &p); err != nil {
		return nil, refusal(name, ReasonInvalidParams, name+": invalid params: "+err.Error(), field, nil)
	}
	if p.Levels != nil {
		if p.Ordered != nil && !*p.Ordered {
			return nil, refusal(name, ReasonOrderedFalse,
				name+": params.levels implies an ordered reading; drop params.ordered false", field, nil)
		}
		if len(p.Levels) == 0 {
			return nil, refusal(name, ReasonEmptyLevels,
				name+": params.levels is empty; list the levels lowest first, or set params.ordered true for the dictionary order", field, nil)
		}
	}
	if f == nil || !f.Type.IsCategorical() {
		ft := "derived"
		if f != nil {
			ft = f.Type.String()
		}
		return nil, refusal(name, ReasonNeedsCategorical,
			name+": params.ordered / params.levels order a categorical field's levels; "+field+" is "+ft+" (drop them for a numeric field)",
			field, map[string]any{"field_type": ft})
	}
	n := 0
	var labels []string
	if f.Dictionary != nil {
		n = f.Dictionary.Count()
		labels = f.Dictionary.Values()
	}
	s := &Spec{Field: field, Rank: make([]int, n), field: f}
	if p.Levels == nil {
		s.Code = make([]int, n)
		for i := range s.Rank {
			s.Rank[i], s.Code[i] = i, i
		}
		s.Levels = labels
		return s, nil
	}
	for i := range s.Rank {
		s.Rank[i] = -1
	}
	s.Code = make([]int, 0, len(p.Levels))
	s.Levels = make([]string, 0, len(p.Levels))
	for i, l := range p.Levels {
		var id uint32
		ok := false
		if f.Dictionary != nil {
			id, ok = f.Dictionary.IDFor(l)
		}
		if !ok {
			return nil, refusal(name, ReasonUnknownLevel,
				name+": params.levels["+strconv.Itoa(i)+"] "+strconv.Quote(l)+" is not a level of "+f.Type.String()+" field "+field+"; name a dictionary entry exactly (pulse inspect lists them)",
				field, map[string]any{"level": l, "index": i})
		}
		if s.Rank[id] >= 0 {
			return nil, refusal(name, ReasonDuplicateLevel,
				name+": params.levels lists "+strconv.Quote(l)+" twice; list each level once",
				field, map[string]any{"level": l, "index": i})
		}
		s.Rank[id] = len(s.Code)
		s.Code = append(s.Code, int(id))
		s.Levels = append(s.Levels, l)
	}
	return s, nil
}

// RankOf returns the rank of the dictionary code v, and whether the
// level is listed. v must be a valid code (the caller checks the
// dictionary bound first).
func (s *Spec) RankOf(v float64) (int, bool) {
	r := s.Rank[int(v)]
	return r, r >= 0
}

// rFuzz is R's type-1 fuzz, 4·.Machine$double.eps.
const rFuzz = 4 * 2.220446049250313e-16

// weightSnapTol is the relative distance within which a weighted
// cumulative Σw counts as reaching p·Σw: the knife edge the weighted
// quantiles snap away (weighting.md), so a share that is
// mathematically p·Σw cannot flip between platforms on the last ulp.
const weightSnapTol = 1e-9

// QuantileRank is R quantile(type = 1) over level ranks: given the
// per-rank totals (row counts; Σw when weighted) and p in [0, 1], the
// lowest rank whose cumulative total reaches p·W, W the grand total,
// among ranks with a positive total (so p = 0 is the lowest observed
// rank). -1 when W is not positive (no admitted row).
//
// Unweighted, the order statistic is R's own, literally: nppm = n·p,
// j = ⌊nppm + fuzz⌋, k = j + (nppm > j) — so an n·p a hair BELOW an
// integer stays on it, and one a hair above moves on (R:
// quantile(c(rep(1, 7), rep(2, 18)), .28, type = 1) is 2, 0.28·25 being
// 7.000000000000001) — and the rank is the first whose cumulative
// count reaches k. Weighted, a cumulative Σw within weightSnapTol
// relative of p·W counts as reaching it.
func QuantileRank(totals []float64, p float64, weighted bool) int {
	w := 0.0
	for _, t := range totals {
		w += t
	}
	if !(w > 0) {
		return -1
	}
	target := float64(p * w)
	reaches := func(cum float64) bool {
		return cum >= target || math.Abs(cum-target) <= weightSnapTol*math.Max(1, math.Abs(target))
	}
	if !weighted {
		j := math.Floor(target + rFuzz)
		k := j
		if target > j {
			k = j + 1
		}
		reaches = func(cum float64) bool { return cum >= k }
	}
	cum := 0.0
	last := -1
	for r, t := range totals {
		if !(t > 0) {
			continue
		}
		cum += t
		last = r
		if reaches(cum) {
			return r
		}
	}
	return last
}

func refusal(op, reason, msg, field string, extra map[string]any) *errors.CodedError {
	details := map[string]any{
		"aggregation": op,
		"reason":      reason,
		"field":       field,
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, msg, details)
}
