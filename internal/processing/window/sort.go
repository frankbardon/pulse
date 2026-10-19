package window

import (
	"math"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// sortCache memoizes sorted index slices for distinct (partitionBy, orderBy)
// tuples over a single Apply invocation. Key encoding is intentionally simple
// (\x00 separators between names) — it's only ever compared for equality and
// never persisted. orders is the run's grouper key orders (KeyOrders),
// fixed for the whole invocation, so it never enters the cache key.
type sortCache struct {
	rows   []map[string]any
	by     map[string][]int
	orders KeyOrders
}

func newSortCache(rows []map[string]any, orders KeyOrders) *sortCache {
	return &sortCache{
		rows:   rows,
		by:     make(map[string][]int),
		orders: orders,
	}
}

// get returns a sorted index slice over rows for (partitionBy, orderBy). The
// slice is owned by the cache; callers must not mutate it.
func (c *sortCache) get(partitionBy []string, orderBy []types.OrderKey) ([]int, error) {
	key := tupleKey(partitionBy, orderBy)
	if cached, ok := c.by[key]; ok {
		return cached, nil
	}
	idx := make([]int, len(c.rows))
	for i := range idx {
		idx[i] = i
	}
	sortIndices(c.rows, idx, partitionBy, orderBy, c.orders)
	c.by[key] = idx
	return idx, nil
}

// tupleKey hashes the (partitionBy, orderBy) tuple to a stable string key.
func tupleKey(partitionBy []string, orderBy []types.OrderKey) string {
	var sb strings.Builder
	sb.WriteString("p:")
	for i, p := range partitionBy {
		if i > 0 {
			sb.WriteByte(0)
		}
		sb.WriteString(p)
	}
	sb.WriteString("|o:")
	for i, o := range orderBy {
		if i > 0 {
			sb.WriteByte(0)
		}
		sb.WriteString(o.Field)
		if o.Desc {
			sb.WriteString(":d")
		} else {
			sb.WriteString(":a")
		}
	}
	return sb.String()
}

// KeyLess orders two string cells of one column ascending: true when a
// sorts strictly before b. It is a grouper's intrinsic key order — the
// window package cannot see groupers, so the engine hands the order in
// as data (KeyOrders). It must be a strict weak order whose ties are
// exactly equal strings, so a key it does not rank still orders
// byte-wise.
type KeyLess func(a, b string) bool

// KeyOrders maps a grouper key column (a grouped row's key cell) to its
// grouper's intrinsic key order: GROUP_DATE day_of_week Monday-first,
// GROUP_DATE_RANGES / GROUP_NUMERIC_RANGES labels in supplied order,
// the numeric bucket groupers by value. Every sort here — Request.Sort
// (SortKeyed), window ORDER BY (ApplyKeyed) and WIN_RANK / DENSE_RANK
// ties — compares a column it names through CompareKeyOrdered. Nil (or
// a column it does not name) is the plain CompareKey order.
type KeyOrders map[string]KeyLess

// Sort orders rows in place by keys. Stable sort; nulls last regardless of
// each key's Desc direction. Used by the processor for response-level sort
// (Request.Sort) and reusable by callers who want a top-level reorder.
//
// When keys is empty, Sort is a no-op.
func Sort(rows []map[string]any, keys []types.OrderKey) {
	SortKeyed(rows, keys, nil)
}

// SortKeyed is Sort under grouper key orders: a key whose column
// orders names compares two string cells by that column's KeyLess
// (CompareKeyOrdered). Nil orders is Sort.
func SortKeyed(rows []map[string]any, keys []types.OrderKey, orders KeyOrders) {
	if len(keys) == 0 || len(rows) == 0 {
		return
	}
	sort.SliceStable(rows, func(a, b int) bool {
		return rowsLess(rows[a], rows[b], keys, orders)
	})
}

// SortOrder is Sort without the reorder: it returns the permutation
// Sort applies to rows — out[j] is the index (into rows) of the row
// Sort places at position j — leaving rows untouched. Same comparator,
// same stability (ties keep their input order), nulls last in both
// directions. Nil when keys is empty (the identity order). The
// materialize path orders source indices with it, so its output rows
// follow exactly the order Request.Sort gives Process's row set. It
// takes no key orders: materialize refuses groups, so its rows carry no
// grouper key column.
func SortOrder(rows []map[string]any, keys []types.OrderKey) []int {
	if len(keys) == 0 {
		return nil
	}
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return rowsLess(rows[idx[a]], rows[idx[b]], keys, nil)
	})
	return idx
}

// rowsLess is Request.Sort's comparator: the first key whose cells
// differ decides (CompareKeyOrdered); equal on every key is not less.
func rowsLess(a, b map[string]any, keys []types.OrderKey, orders KeyOrders) bool {
	return CompareRows(a, b, keys, orders) < 0
}

// CompareRows orders two rows under keys: the first key whose cells
// differ decides, each through CompareKeyOrdered with its column's
// KeyLess (orders[key.Field], nil when orders does not name it).
// Negative when a sorts before b; 0 when equal on every key. It is the
// one row comparator of Request.Sort, window ORDER BY and post-test
// order_by.
func CompareRows(a, b map[string]any, keys []types.OrderKey, orders KeyOrders) int {
	for _, k := range keys {
		if cmp := CompareKeyOrdered(a[k.Field], b[k.Field], k.Desc, orders[k.Field]); cmp != 0 {
			return cmp
		}
	}
	return 0
}

// CompareKey orders two cells under one sort key: negative when a sorts
// before b. Desc reverses the order of NON-NULL values only — a null
// (nil or missing) trails in BOTH directions. Reversing compareCell
// wholesale instead flipped the null term too, so every Desc key put its
// nulls FIRST while the contract (window order_by, Request.Sort,
// post-test order_by) says nulls last regardless of direction. Every
// ordering arm in the engine routes through here.
func CompareKey(a, b any, desc bool) int {
	return CompareKeyOrdered(a, b, desc, nil)
}

// CompareKeyOrdered is CompareKey under a grouper key order: when less
// is non-nil and both cells are strings (grouper keys), they compare by
// less — reversed under desc — instead of byte-wise. A null, a
// non-string cell, or a nil less keeps CompareKey's order exactly, so a
// key the grouper does not rank (an ISO date) and every numeric column
// order as before.
func CompareKeyOrdered(a, b any, desc bool, less KeyLess) int {
	a, b = sortValue(a), sortValue(b)
	aNull, bNull := isNullCell(a), isNullCell(b)
	switch {
	case aNull && bNull:
		return 0
	case aNull:
		return 1
	case bNull:
		return -1
	}
	var cmp int
	as, aok := a.(string)
	bs, bok := b.(string)
	if less != nil && aok && bok {
		switch {
		case less(as, bs):
			cmp = -1
		case less(bs, as):
			cmp = 1
		}
	} else {
		cmp = compareCell(a, b)
	}
	if desc {
		return -cmp
	}
	return cmp
}

// sortIndices sorts idx by (partitionBy ASC, orderBy). Stable. Nulls last.
// An order key whose column orders names follows that grouper key order.
func sortIndices(rows []map[string]any, idx []int, partitionBy []string, orderBy []types.OrderKey, orders KeyOrders) {
	sort.SliceStable(idx, func(a, b int) bool {
		ra, rb := rows[idx[a]], rows[idx[b]]
		for _, p := range partitionBy {
			cmp := compareCell(ra[p], rb[p])
			if cmp != 0 {
				return cmp < 0
			}
		}
		return CompareRows(ra, rb, orderBy, orders) < 0
	})
}

// compareCell returns -1, 0, or +1 comparing two cell values in ASCENDING
// order, nulls (nil, missing) last. It is direction-free: a Desc key goes
// through CompareKey, which keeps nulls last while reversing the rest.
//
// Ordering per decoded value: numbers (every numeric field type, signed
// date / datetime epoch counts, packed_bool as 0/1) by float64 value;
// encoding.Decimal128 by Cmp — one column shares one scale, so comparing
// the unscaled mantissas is comparing the values; strings (categorical
// labels, bucket keys) byte-wise. Anything else (a set_* label slice)
// compares equal, which is why the shared orderability rule
// (internal/descriptor.IsOrderableType) refuses a set order_by key.
//
// A cell implementing SortValuer (a decimal aggregate result, a
// record-row decimal cell) is ordered by its SortValue.
func compareCell(a, b any) int {
	a, b = sortValue(a), sortValue(b)
	aNull := isNullCell(a)
	bNull := isNullCell(b)
	switch {
	case aNull && bNull:
		return 0
	case aNull:
		return 1 // a comes after b (nulls last)
	case bNull:
		return -1
	}

	if ad, ok := asDecimal(a); ok {
		if bd, ok := asDecimal(b); ok {
			return ad.Cmp(bd)
		}
	}
	if as, ok := a.(ScaledDecimal); ok {
		if bs, ok := b.(ScaledDecimal); ok && as.Scale == bs.Scale {
			return as.Value.Cmp(bs.Value)
		}
	}

	af, aOk := toFloat(a)
	bf, bOk := toFloat(b)
	if aOk && bOk {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}

	as, _ := a.(string)
	bs, _ := b.(string)
	return strings.Compare(as, bs)
}

// SortValuer is implemented by a cell value whose order is not its own Go
// value: compareCell orders it by SortValue() instead. A nil SortValue
// is a null.
type SortValuer interface {
	SortValue() any
}

// ScaledDecimal is a decimal128 cell carrying its scale, the SortValue
// of a decimal aggregate result or record-row decimal cell. Two of the
// same scale order exactly by Decimal128.Cmp; across scales, or against
// a plain number (an aggregate that fell back to f64), by Float64.
type ScaledDecimal struct {
	Value encoding.Decimal128
	Scale uint8
}

func sortValue(v any) any {
	if sv, ok := v.(SortValuer); ok {
		return sv.SortValue()
	}
	return v
}

// asDecimal unwraps a decimal128 cell (the Record stores it unboxed and
// AllValues hands it over by value; a pointer is accepted for callers
// that box it).
func asDecimal(v any) (encoding.Decimal128, bool) {
	switch d := v.(type) {
	case encoding.Decimal128:
		return d, true
	case *encoding.Decimal128:
		if d != nil {
			return *d, true
		}
	}
	return encoding.Decimal128{}, false
}

// isNullCell reports whether a cell value is null/missing for sort
// purposes. A NaN float is an undefined figure — null on the wire
// (types.MarshalFinite) — so it orders as a null: last in both
// directions. Compared as a number it tied with every value, which
// breaks the comparator's strict weak order (an aggregate no row
// answered, an expanded vector element no row answered).
func isNullCell(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case float64:
		return math.IsNaN(x)
	case float32:
		return math.IsNaN(float64(x))
	}
	return false
}

// toFloat coerces a cell value into float64 if numerically convertible.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case ScaledDecimal:
		return x.Value.Float64(x.Scale), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
