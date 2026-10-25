package strictjson

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestOptimize_StrictDecodeNested: a misspelled key inside an optimize
// block's variable or constraint is refused at its path, and a bound
// decodes as a number or a params reference without a key walk.
func TestOptimize_StrictDecodeNested(t *testing.T) {
	for _, c := range []struct{ body, path string }{
		{`{"requests":[],"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":0,"uper":1}],"objective":"x"}]}`, "optimize[0].variables[0]"},
		{`{"requests":[],"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"x","constraints":[{"exp":"x","type":"eq"}]}]}`, "optimize[0].constraints[0]"},
	} {
		_, err := Decode[types.ComposedRequest]([]byte(c.body), Options{})
		if err == nil || !strings.Contains(err.Error(), c.path) {
			t.Errorf("%s: got %v, want a refusal at %s", c.body, err, c.path)
		}
	}
	got, err := Decode[types.ComposedRequest]([]byte(`{"requests":[],"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":"params.lo","upper":2.5}],"objective":"x"}]}`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := got.Optimize[0].Variables[0]
	if v.Lower.Param() != "lo" || v.Upper.Value() != 2.5 {
		t.Fatalf("decoded %+v", v)
	}
}
