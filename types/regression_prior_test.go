package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestPriorPrecision_JSON: prior_precision decodes a number or an array
// of numbers, null and absence read as absent, a scalar 0 is absent; it
// re-encodes as it came (the scalar form byte-identical to the float64
// field it replaced) and anything else — a null entry included — is
// refused.
func TestPriorPrecision_JSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		vector  []float64
		scalar  float64
		zero    bool
		wantOut string
	}{
		{`{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":2.5}`, nil, 2.5, false, `{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":2.5}`},
		{`{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":0}`, nil, 0, true, `{"type":"REG_BAYES_LINEAR","target":"y"}`},
		{`{"type":"REG_BAYES_LINEAR","target":"y"}`, nil, 0, true, `{"type":"REG_BAYES_LINEAR","target":"y"}`},
		{`{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":null}`, nil, 0, true, `{"type":"REG_BAYES_LINEAR","target":"y"}`},
		{`{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":[0,40,0.001]}`, []float64{0, 40, 0.001}, 0, false, `{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":[0,40,0.001]}`},
		{`{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":[]}`, []float64{}, 0, false, `{"type":"REG_BAYES_LINEAR","target":"y","prior_precision":[]}`},
	}
	for _, c := range cases {
		var s RegressionSpec
		if err := json.Unmarshal([]byte(c.in), &s); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		p := s.PriorPrecision
		if p.IsZero() != c.zero || p.IsVector() != (c.vector != nil) || p.Scalar() != c.scalar || !reflect.DeepEqual(p.Vector(), c.vector) {
			t.Errorf("%s: zero %v vector %v %v scalar %v", c.in, p.IsZero(), p.IsVector(), p.Vector(), p.Scalar())
		}
		out, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != c.wantOut {
			t.Errorf("%s: re-encoded %s, want %s", c.in, out, c.wantOut)
		}
	}
	for _, bad := range []string{`"1"`, `true`, `{}`, `[1,null]`, `[[1]]`} {
		var p PriorPrecision
		if err := json.Unmarshal([]byte(bad), &p); err == nil {
			t.Errorf("prior_precision %s decoded, want an error", bad)
		}
	}
	// The constructors copy; Vector returns a copy.
	v := []float64{1, 2}
	p := PrecisionVector(v)
	v[0] = 9
	got := p.Vector()
	got[1] = 9
	if !reflect.DeepEqual(p.Vector(), []float64{1, 2}) {
		t.Errorf("PrecisionVector aliased its input: %v", p.Vector())
	}
	if PrecisionScalar(3).Scalar() != 3 || PrecisionVector(nil).Scalar() != 0 || PrecisionVector(nil).IsZero() {
		t.Error("constructor states")
	}
}
