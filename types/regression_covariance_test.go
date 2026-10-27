package types

import (
	"encoding/json"
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

// TestAutoHACLags pins the rule ⌊4·(n/100)^(2/9)⌋ at a few sizes.
func TestAutoHACLags(t *testing.T) {
	t.Parallel()

	for n, want := range map[int]int{0: 0, 1: 1, 48: 3, 60: 3, 100: 4, 1000: 6, 10000: 11} {
		if got := AutoHACLags(n); got != want {
			t.Errorf("AutoHACLags(%d) = %d, want %d", n, got, want)
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
