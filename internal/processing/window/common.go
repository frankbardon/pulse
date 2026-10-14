package window

import (
	"github.com/frankbardon/pulse/types"
)

// cellFloat reads a numeric cell as (value, ok). Null/missing/non-numeric
// values — and a NaN, an undefined figure that is null on the wire —
// return (0, false), so every value-bearing window skips it like a null.
func cellFloat(row map[string]any, field string) (float64, bool) {
	v, present := row[field]
	if !present || isNullCell(v) {
		return 0, false
	}
	return toFloat(v)
}

// equalsOnKeys reports whether two rows have equal values on every order key.
// Used by RANK / DENSE_RANK to detect ties.
func equalsOnKeys(a, b map[string]any, keys []types.OrderKey) bool {
	for _, k := range keys {
		if compareCell(a[k.Field], b[k.Field]) != 0 {
			return false
		}
	}
	return true
}
