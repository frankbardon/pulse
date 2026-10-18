package pulse

import "sort"

// RangeTableInfo is one registered range table as surfaced by
// RangeTables — the discovery companion for the GROUP_DATE_RANGES /
// FILTER_DATE_RANGES operators (a "date" table) and GROUP_NUMERIC_RANGES
// (a "numeric" table). It carries the table name, its kind, its labeled-
// range count, and the ordered ranges themselves — Ranges for a date
// table, NumericRanges for a numeric one — so a caller can turn a table
// name into the {label, start, end} tuples an operator resolves.
type RangeTableInfo struct {
	Name          string             `json:"name"`
	Kind          string             `json:"kind"`
	RangeCount    int                `json:"range_count"`
	Ranges        []DateRangeSpec    `json:"ranges,omitempty"`
	NumericRanges []NumericRangeSpec `json:"numeric_ranges,omitempty"`
}

// RangeTables returns the registered range tables in name order. Each
// entry carries the table's kind, range count and its ordered {label,
// start, end} ranges — the INPUT-direction discovery surface a caller consults
// before authoring a GROUP_DATE_RANGES / FILTER_DATE_RANGES (date table) or
// GROUP_NUMERIC_RANGES (numeric table) request that references the table
// by name. Returns nil when none are registered.
func (p *Pulse) RangeTables() []RangeTableInfo {
	reg := p.svc.Extensions()
	if reg == nil || len(reg.RangeTables) == 0 {
		return nil
	}
	out := make([]RangeTableInfo, 0, len(reg.RangeTables))
	for name, tbl := range reg.RangeTables {
		out = append(out, RangeTableInfo{
			Name:          name,
			Kind:          tbl.Kind(),
			RangeCount:    len(tbl.Ranges) + len(tbl.NumericRanges),
			Ranges:        fromEngineDateRanges(tbl.Ranges),
			NumericRanges: fromEngineNumericRanges(tbl.NumericRanges),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
