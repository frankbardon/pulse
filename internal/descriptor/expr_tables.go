package descriptor

import (
	"github.com/frankbardon/pulse/internal/tablecall"
)

// ExprTables is the instance's named tables as the static lookup() /
// range_label() literal-call check (internal/tablecall) reads them, off
// the extensions snapshot alone: lookup() is the built-in when snap
// registers a LookupTable, a table's axes are its declared values (a
// range-table axis that table's labels, RangeTableLabels), and a range
// table resolves unless capability:range_tables is hidden on inst —
// exactly the runtime registry's answers (processing.ExtensionRegistry.
// TablesCatalog), so the field-reference walk, the overlay gate and the
// optimize compile judge the same call alike on both sides. A nil snap
// registers no table.
func ExprTables(snap *ExtensionsSnapshot, inst *InstanceSnapshot) tablecall.Catalog {
	return snapTables{snap: snap, inst: inst}
}

type snapTables struct {
	snap *ExtensionsSnapshot
	inst *InstanceSnapshot
}

func (t snapTables) HasLookup() bool { return t.snap != nil && len(t.snap.LookupTables) > 0 }

func (t snapTables) LookupAxes(name string) ([][]string, bool) {
	if t.snap == nil {
		return nil, false
	}
	for _, lt := range t.snap.LookupTables {
		if lt.Name != name {
			continue
		}
		if len(lt.Keys) == 0 {
			return nil, true
		}
		axes := make([][]string, len(lt.Keys))
		for i, ax := range lt.Keys {
			if ax.RangeTable == "" {
				axes[i] = ax.Values
				continue
			}
			if labels, ok := t.snap.RangeTableLabels[ax.RangeTable]; ok {
				axes[i] = labels
			}
		}
		return axes, true
	}
	return nil, false
}

func (t snapTables) RangeKind(name string) (string, bool) {
	if t.snap == nil || t.inst.Hidden(featRangeTables) {
		return "", false
	}
	for _, rt := range t.snap.RangeTables {
		if rt.Name == name {
			return rt.Kind, true
		}
	}
	return "", false
}
