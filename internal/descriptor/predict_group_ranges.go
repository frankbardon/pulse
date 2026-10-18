package descriptor

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// validateRangeOperatorParams reports what the runtime refuses about a
// range operator's source before it reads a record:
//
//   - GROUP_NUMERIC_RANGES: every numrange.Parse refusal (the one reading
//     the factory calls, so code and message are the runtime's own);
//   - all three range operators (GROUP_DATE_RANGES, FILTER_DATE_RANGES,
//     GROUP_NUMERIC_RANGES) naming a `table`: an unknown or hidden table
//     (PULSE_RANGE_TABLE_UNKNOWN) and a table of the other kind
//     (PULSE_RANGE_TABLE_KIND_MISMATCH), plus — for the numeric grouper —
//     an unmatched label colliding with one of the table's labels.
//
// Slots: Request.Groups, both crosstab axes, a MAT_DISTANCE
// params.between grouper, and Request.Filterers.
func validateRangeOperatorParams(env *descriptor.Envelope, req *types.Request, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	checkGroup := func(g *types.Group) {
		if g == nil {
			return
		}
		switch opRoute(inst, g.Type) {
		case types.GROUP_NUMERIC_RANGES:
			cfg, err := numrange.Parse(g.Params)
			if err == nil && cfg.Table != "" {
				err = numericTableRefusal(cfg, inst)
			}
			if err != nil {
				addCodedError(env, err)
			}
		case types.GROUP_DATE_RANGES:
			if err := dateTableRefusal(string(types.GROUP_DATE_RANGES), paramread.Grouper, g.Params, inst); err != nil {
				addCodedError(env, err)
			}
		}
	}
	for _, g := range req.Groups {
		checkGroup(g)
	}
	if ct := req.Crosstab; ct != nil {
		for _, g := range ct.Rows {
			checkGroup(g)
		}
		for _, g := range ct.Columns {
			checkGroup(g)
		}
	}
	for _, m := range req.Matrices {
		if opRoute(inst, m.Type) != types.MAT_DISTANCE {
			continue
		}
		if g, ok := vectors.SpecBetween(m); ok {
			checkGroup(&g)
		}
	}
	for _, f := range req.Filterers {
		if f == nil || opRoute(inst, f.Type) != types.FILTER_DATE_RANGES {
			continue
		}
		if err := dateTableRefusal(string(types.FILTER_DATE_RANGES), paramread.Filterer, f.Params, inst); err != nil {
			addCodedError(env, err)
		}
	}
}

// rangeTableMeta returns the instance's registered table name, or false
// when it is unknown or rides a hidden capability:range_tables (sized
// exactly like a never-registered one, so predict cannot leak it).
func rangeTableMeta(name string, inst *InstanceSnapshot) (descriptor.RangeTableMeta, bool) {
	ext := inst.Extensions()
	if ext == nil || inst.Hidden(featRangeTables) {
		return descriptor.RangeTableMeta{}, false
	}
	for _, t := range ext.RangeTables {
		if t.Name == name {
			return t, true
		}
	}
	return descriptor.RangeTableMeta{}, false
}

// unknownRangeTable is the runtime's PULSE_RANGE_TABLE_UNKNOWN, word for
// word (processing.lookupRangeTable).
func unknownRangeTable(operator, table string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_UNKNOWN,
		operator+": unknown range table "+strconv.Quote(table),
		map[string]any{"operator": operator, "table": table})
}

func numericTableRefusal(cfg numrange.Config, inst *InstanceSnapshot) error {
	meta, ok := rangeTableMeta(cfg.Table, inst)
	if !ok {
		return unknownRangeTable(numrange.Operator, cfg.Table)
	}
	if meta.Kind != daterange.KindNumeric {
		return daterange.KindMismatch(numrange.Operator, cfg.Table, daterange.KindNumeric, meta.Kind)
	}
	for _, label := range inst.Extensions().RangeTableLabels[cfg.Table] {
		if label == cfg.UnmatchedLabel {
			// The runtime's numrange.Config.Resolve refusal.
			c := cfg
			return c.Resolve([]numrange.Spec{{Label: label}})
		}
	}
	return nil
}

// dateTableRefusal reads only the date-range operators' `table` (their
// other refusals stay the runtime's) and refuses an unknown or numeric
// table. A params blob that does not decode, or that names an inline
// source as well, is left to the runtime.
func dateTableRefusal(operator string, cat paramread.Category, raw json.RawMessage, inst *InstanceSnapshot) error {
	if len(raw) == 0 {
		return nil
	}
	var p struct {
		Ranges []json.RawMessage `json:"ranges"`
		Table  string            `json:"table"`
	}
	if err := paramread.Decode(cat, operator, raw, &p); err != nil {
		return nil
	}
	table := strings.TrimSpace(p.Table)
	if table == "" || len(p.Ranges) > 0 {
		return nil
	}
	meta, ok := rangeTableMeta(table, inst)
	if !ok {
		return unknownRangeTable(operator, table)
	}
	if meta.Kind != daterange.KindDate {
		return daterange.KindMismatch(operator, table, daterange.KindDate, meta.Kind)
	}
	return nil
}
