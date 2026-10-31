package types

import (
	"encoding/json"
	"math"
	"testing"
)

// TestCovarianceLags_JSON: lags decodes "auto" or an integer, null and
// absence read as auto, anything else is refused; it re-encodes as it
// came and an absent lag is omitted.
func TestCovarianceLags_JSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		auto    bool
		n       int
		wantOut string
	}{
		{`{"type":"hac","order_by":"week","lags":"auto"}`, true, 0, `{"type":"hac","order_by":"week","lags":"auto"}`},
		{`{"type":"hac","order_by":"week","lags":3}`, false, 3, `{"type":"hac","order_by":"week","lags":3}`},
		{`{"type":"hac","order_by":"week","lags":0}`, false, 0, `{"type":"hac","order_by":"week","lags":0}`},
		{`{"type":"hac","order_by":"week"}`, true, 0, `{"type":"hac","order_by":"week"}`},
		{`{"type":"hac","order_by":"week","lags":null}`, true, 0, `{"type":"hac","order_by":"week"}`},
	}
	for _, c := range cases {
		var cov RegressionCovariance
		if err := json.Unmarshal([]byte(c.in), &cov); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if cov.Lags.IsAuto() != c.auto {
			t.Errorf("%s: IsAuto = %v", c.in, cov.Lags.IsAuto())
		}
		if n, ok := cov.Lags.Value(); ok != !c.auto || n != c.n {
			t.Errorf("%s: Value = (%d, %v)", c.in, n, ok)
		}
		out, err := json.Marshal(cov)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != c.wantOut {
			t.Errorf("%s: re-encoded %s, want %s", c.in, out, c.wantOut)
		}
	}
	for _, bad := range []string{`"fixed"`, `2.5`, `true`, `[1]`} {
		var l CovarianceLags
		if err := json.Unmarshal([]byte(bad), &l); err == nil {
			t.Errorf("lags %s decoded, want an error", bad)
		}
	}
	if b, _ := json.Marshal(CovarianceLags{}); string(b) != `"auto"` {
		t.Errorf("absent lag marshalled directly = %s, want \"auto\"", b)
	}
}

// TestAutoHACLags pins the rule ⌊4·(n/100)^(2/9)⌋ at a few sizes,
// including the exact boundary n = 1968300 (n/100 = 3^9, lag 36) where
// a floating-point power gives 35, and its neighbours.
func TestAutoHACLags(t *testing.T) {
	t.Parallel()

	for n, want := range map[int]int{
		-5: 0, 0: 0, 1: 1, 2: 1, 48: 3, 60: 3, 99: 3, 100: 4, 101: 4,
		1000: 6, 10000: 11, 51199: 15, 51200: 16,
		1968299: 35, 1968300: 36, 1968301: 36,
		26214399: 63, 26214400: 64,
		1_000_000_000_000: 667, math.MaxInt64: 23552,
	} {
		if got := AutoHACLags(n); got != want {
			t.Errorf("AutoHACLags(%d) = %d, want %d", n, got, want)
		}
	}
}

// TestAutoHACLags_ExactBoundaries: 4·(n/100)^(2/9) is an exact integer
// at n = 100·m^9 (lag 4·m²); the lag steps there and not one row
// earlier, for every such n an int64 holds (m ≤ 76).
func TestAutoHACLags_ExactBoundaries(t *testing.T) {
	t.Parallel()

	for m := int64(1); m <= 76; m++ {
		n := int64(100)
		for range 9 {
			n *= m
		}
		want := int(4 * m * m)
		if got := AutoHACLags(int(n)); got != want {
			t.Errorf("AutoHACLags(100·%d^9 = %d) = %d, want %d", m, n, got, want)
		}
		if got := AutoHACLags(int(n - 1)); got != want-1 {
			t.Errorf("AutoHACLags(100·%d^9 − 1 = %d) = %d, want %d", m, n-1, got, want-1)
		}
	}
}

// TestRegressionCovariance_StreamableAndRobust: absent / empty /
// classical stream and are not robust; every other type is robust and
// forces the buffered path.
func TestRegressionCovariance_StreamableAndRobust(t *testing.T) {
	t.Parallel()

	base := RegressionSpec{Type: REG_OLS, Target: "y", Predictors: []string{"x"}}
	if !base.Streamable() || base.Covariance.Robust() || base.Covariance.EffectiveType() != CovarianceClassical {
		t.Fatal("absent covariance must stream and be classical")
	}
	for _, typ := range append([]CovarianceType{""}, AllCovarianceTypes()...) {
		s := base
		s.Covariance = &RegressionCovariance{Type: typ}
		robust := typ != "" && typ != CovarianceClassical
		if s.Covariance.Robust() != robust || s.Streamable() == robust {
			t.Errorf("%q: Robust %v, Streamable %v", typ, s.Covariance.Robust(), s.Streamable())
		}
	}
}
