package pulse

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
)

// LookupTableInfo is one registered lookup table as surfaced by
// LookupTables — the discovery companion for the lookup() expression
// function. It carries what an author needs to write
// lookup("table", k1, k2, …) with on-axis keys — the table's name,
// description, declared key axes and whether every axis combination
// has a cell — plus its cell count, and NEVER a cell value.
//
// Keys lists the declared axes in lookup() argument order, each with
// its name and exactly one of the inline values or the range table
// whose labels are the axis's values; it is empty (never null on the
// wire) when the table declares none, and its keys are then free-form.
// CellCount is the number of cells of a static Rows table and is nil
// (absent on the wire) for a function-driven Lookup table, whose cells
// cannot be counted; HasRowsData tells the two apart.
type LookupTableInfo struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description,omitempty"`
	HasRowsData bool                        `json:"has_rows_data"`
	CellCount   *int                        `json:"cell_count,omitempty"`
	Keys        []descriptor.LookupAxisMeta `json:"keys"`
	Complete    bool                        `json:"complete"`
}

// LookupTables returns the registered lookup tables in name order — the
// INPUT-direction discovery surface a caller consults before authoring
// a lookup() call in an expression. It reports each table's metadata
// and cell count, never its cells. Returns nil when none are registered,
// including when the instance's feature profile hides
// capability:lookup_tables (pulse.New then registers none).
func (p *Pulse) LookupTables() []LookupTableInfo {
	snap := p.svc.ExtensionsSnapshot()
	if snap == nil || len(snap.LookupTables) == 0 {
		return nil
	}
	reg := p.svc.Extensions()
	out := make([]LookupTableInfo, 0, len(snap.LookupTables))
	for _, m := range snap.LookupTables {
		info := LookupTableInfo{
			Name:        m.Name,
			Description: m.Description,
			HasRowsData: m.HasRowsData,
			Keys:        make([]descriptor.LookupAxisMeta, 0, len(m.Keys)),
			Complete:    m.Complete,
		}
		for _, ax := range m.Keys {
			info.Keys = append(info.Keys, descriptor.LookupAxisMeta{
				Name:       ax.Name,
				Values:     append([]string(nil), ax.Values...),
				RangeTable: ax.RangeTable,
			})
		}
		if m.HasRowsData && reg != nil {
			n := len(reg.LookupTables[m.Name].Rows)
			info.CellCount = &n
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
