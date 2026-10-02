package window

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// sortCache memoizes sorted index slices for distinct (partitionBy, orderBy)
// tuples over a single Apply invocation. Key encoding is intentionally simple
// (\x00 separators between names) — it's only ever compared for equality and
// never persisted.
type sortCache struct {
	rows []map[string]any
	by   map[string][]int
}

func newSortCache(rows []map[string]any) *sortCache {
	return &sortCache{
		rows: rows,
		by:   make(map[string][]int),
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
	sortIndices(c.rows, idx, partitionBy, orderBy)
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

// Sort orders rows in place by keys. Stable sort; nulls last regardless of
// each key's Desc direction. Used by the processor for response-level sort
// (Request.Sort) and reusable by callers who want a top-level reorder.
//
// When keys is empty, Sort is a no-op.
func Sort(rows []map[string]any, keys []types.OrderKey) {
	if len(keys) == 0 || len(rows) == 0 {
		return
	}
	sort.SliceStable(rows, func(a, b int) bool {
		for _, k := range keys {
			if cmp := CompareKey(rows[a][k.Field], rows[b][k.Field], k.Desc); cmp != 0 {
				return cmp < 0
			}
		}
		return false
	})
}

// CompareKey orders two cells under one sort key: negative when a sorts
// before b. Desc reverses the order of NON-NULL values only — a null
// (nil or missing) trails in BOTH directions. Reversing compareCell
// wholesale instead flipped the null term too, so every Desc key put its
// nulls FIRST while the contract (window order_by, Request.Sort,
// post-test order_by) says nulls last regardless of direction. Every
// ordering arm in the engine routes through here.
func CompareKey(a, b any, desc bool) int {
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
	cmp := compareCell(a, b)
	if desc {
		return -cmp
	}
	return cmp
}

// sortIndices sorts idx by (partitionBy ASC, orderBy). Stable. Nulls last.
func sortIndices(rows []map[string]any, idx []int, partitionBy []string, orderBy []types.OrderKey) {
	sort.SliceStable(idx, func(a, b int) bool {
		ra, rb := rows[idx[a]], rows[idx[b]]
		for _, p := range partitionBy {
			cmp := compareCell(ra[p], rb[p])
			if cmp != 0 {
				return cmp < 0
			}
		}
		for _, o := range orderBy {
			if cmp := CompareKey(ra[o.Field], rb[o.Field], o.Desc); cmp != 0 {
				return cmp < 0
			}
		}
		return false
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

// isNullCell reports whether a cell value is null/missing for sort purposes.
func isNullCell(v any) bool {
	return v == nil
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
