package pulse

import "sort"

// RangeTableInfo is one registered range table as surfaced by
// RangeTables — the discovery companion for the GROUP_DATE_RANGES /
// FILTER_DATE_RANGES operators. It carries the table name, its labeled-
// range count, and the ordered ranges themselves so a caller can turn a
// table name into the {label, start, end} tuples an operator resolves.
type RangeTableInfo struct {
	Name       string          `json:"name"`
	RangeCount int             `json:"range_count"`
	Ranges     []DateRangeSpec `json:"ranges"`
}

// RangeTables returns the registered range tables in name order. Each
// entry carries the table's range count and its ordered {label, start,
// end} ranges — the INPUT-direction discovery surface a caller consults
// before authoring a GROUP_DATE_RANGES / FILTER_DATE_RANGES request that
// references the table by name. Returns nil when none are registered.
func (p *Pulse) RangeTables() []RangeTableInfo {
	reg := p.svc.Extensions()
	if reg == nil || len(reg.RangeTables) == 0 {
		return nil
	}
	out := make([]RangeTableInfo, 0, len(reg.RangeTables))
	for name, tbl := range reg.RangeTables {
		ranges := make([]DateRangeSpec, len(tbl.Ranges))
		copy(ranges, tbl.Ranges)
		out = append(out, RangeTableInfo{
			Name:       name,
			RangeCount: len(tbl.Ranges),
			Ranges:     ranges,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
