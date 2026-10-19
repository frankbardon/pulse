package processing

import (
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// groupKeyOrders maps each grouper key column of groups (the row column
// vectors.GroupKeyField names) to that grouper's intrinsic key order
// (keyOrderFilter: GROUP_DATE day_of_week Monday-first,
// GROUP_DATE_RANGES / GROUP_NUMERIC_RANGES supplied order, the numeric
// bucket groupers by value). It is the one source of the grouper key
// order every row sort of a grouped run follows — Request.Sort, window
// ORDER BY (WIN_RANK / WIN_DENSE_RANK ties included) and post-test
// order_by (TEST_TREND). The rank derives from the grouper's
// configuration alone, so a fresh instance orders as the one that
// partitioned. Nil when no grouper carries a key order.
func (p *Processor) groupKeyOrders(groups []*types.Group) map[string]*includeFilter {
	return keyOrdersOf(groups, func(i int, grp *types.Group) Grouper {
		factory, ok := p.exts.LookupGrouper(grp.Type)
		if !ok {
			return nil
		}
		g, err := factory(grp, p.grouperSchema(grp))
		if err != nil {
			return nil
		}
		ApplyGrouperExtensions(g, p.exts)
		return g
	})
}

// keyOrdersOf is groupKeyOrders over the grouper grouperOf returns for
// groups[i] (nil contributes nothing: the partition pass already ran
// it, so a grouper that does not construct is defensive). A column two
// groupers share is ambiguous and dropped (it stays byte-wise).
func keyOrdersOf(groups []*types.Group, grouperOf func(i int, grp *types.Group) Grouper) map[string]*includeFilter {
	if len(groups) == 0 {
		return nil
	}
	var orders map[string]*includeFilter
	seen := make(map[string]bool, len(groups))
	for i, grp := range groups {
		if grp == nil {
			continue
		}
		col := vectors.GroupKeyField(grp.Type, grp)
		if seen[col] {
			delete(orders, col)
			continue
		}
		seen[col] = true
		if f := keyOrderFilter(grouperOf(i, grp)); f != nil {
			if orders == nil {
				orders = make(map[string]*includeFilter, len(groups))
			}
			orders[col] = f
		}
	}
	return orders
}

// windowKeyOrders is orders as the window package's comparator data
// (window.KeyOrders, one KeyLess per column): the window package cannot
// see groupers, so the order crosses as each filter's less. Nil stays
// nil — the plain CompareKey order, byte-identically.
func windowKeyOrders(orders map[string]*includeFilter) window.KeyOrders {
	if len(orders) == 0 {
		return nil
	}
	out := make(window.KeyOrders, len(orders))
	for col, f := range orders {
		out[col] = f.less
	}
	return out
}
