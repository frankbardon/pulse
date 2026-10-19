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
// Used by RANK / DENSE_RANK to detect ties: a tie is exactly a CompareRows
// tie under the run's key orders — the comparator that sorted the
// partition — so a rank never disagrees with the order it numbers.
func equalsOnKeys(a, b map[string]any, keys []types.OrderKey, orders KeyOrders) bool {
	return CompareRows(a, b, keys, orders) == 0
}
