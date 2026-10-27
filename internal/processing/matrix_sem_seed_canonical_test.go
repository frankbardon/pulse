package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestSEMSeedSpec_ParamsKeyOrder: a MAT_SEM bootstrap's hashed wire form
// is the canonical params (rng_seed dropped) — nested key order and
// whitespace never move it, a value change does.
func TestSEMSeedSpec_ParamsKeyOrder(t *testing.T) {
	t.Parallel()

	spec := func(params string) types.MatrixSpec {
		return types.MatrixSpec{Type: types.MAT_SEM, Params: json.RawMessage(params)}
	}
	a := string(semSeedSpec(spec(`{"model":{"f":{"x":1,"y":[2,1]}},"rng_seed":5}`)))
	b := string(semSeedSpec(spec(`{"rng_seed":0, "model":{"f":{"y":[2,1],"x":1}}}`)))
	if a != b {
		t.Fatalf("nested key order moved the hashed form:\n%s\n%s", a, b)
	}
	if string(semSeedSpec(spec(`{"model":{"f":{"x":2,"y":[2,1]}}}`))) == a {
		t.Fatal("a params value change did not move the hashed form")
	}
}
