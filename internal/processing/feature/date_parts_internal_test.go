package feature

import "testing"

// TestDecodeDateParts pins FEAT_DATE_FEATURES' epoch-day decode across
// the sign boundary.
func TestDecodeDateParts(t *testing.T) {
	for _, tc := range []struct {
		v               float64
		y, m, d, dow, q float64
	}{
		{0, 1970, 1, 1, 4, 1},
		{-1, 1969, 12, 31, 3, 4},
		{19787, 2024, 3, 5, 2, 1},
	} {
		y, m, d, dow, q := decodeDateParts(tc.v)
		if y != tc.y || m != tc.m || d != tc.d || dow != tc.dow || q != tc.q {
			t.Errorf("decodeDateParts(%v) = %v-%v-%v dow=%v q=%v, want %v-%v-%v dow=%v q=%v",
				tc.v, y, m, d, dow, q, tc.y, tc.m, tc.d, tc.dow, tc.q)
		}
	}
}
