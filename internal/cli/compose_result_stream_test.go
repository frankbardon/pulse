package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
)

// TestComposeResult_StreamOrder (library-gaps E5-S2): `pulse api
// compose --stream` writes a result() pre-phase slot's rows once, at
// its slot index — never in run order. The block reads slot 1
// ("weeks"), which runs before slot 0 ("first"); the stream is still
// index 0's row, index 1's row, then the {"optimizations":[...]} line,
// serial and --parallel alike.
func TestComposeResult_StreamOrder(t *testing.T) {
	// serial: t.Chdir moves the process working directory; t.Setenv mutates the process environment

	dir, _, at := optCLIFixture(t)
	var req pulse.ComposedRequest
	if err := json.Unmarshal(at(optfixture.PriceModel), &req); err != nil {
		t.Fatal(err)
	}
	first := &types.Request{Label: "first", Cohort: &types.Cohort{Filename: filepath.Join(dir, optfixture.CohortFile)},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "units", Label: "mean_units"}}}
	req.Requests = append([]*types.Request{first}, req.Requests...)
	req.Optimize[0].Variables[0].Upper = types.BoundExpr(`result("weeks", "n") / 8`)
	raw, err := json.Marshal(&req)
	if err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "result_stream.json")
	if err := os.WriteFile(reqPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"compose", "--stream", "-r", reqPath}, {"compose", "--stream", "--parallel", "3", "-r", reqPath}} {
		var buf bytes.Buffer
		if err := runAPICLI(t, &buf, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, buf.String())
		}
		var indices []int
		var last map[string]json.RawMessage
		for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
			var rec map[string]json.RawMessage
			if err := json.Unmarshal(line, &rec); err != nil {
				t.Fatalf("%v: line %s: %v", args, line, err)
			}
			if ix, ok := rec["index"]; ok {
				var i int
				if err := json.Unmarshal(ix, &i); err != nil {
					t.Fatal(err)
				}
				indices = append(indices, i)
			}
			last = rec
		}
		if !reflect.DeepEqual(indices, []int{0, 1}) {
			t.Fatalf("%v: row indices %v, want [0 1]\n%s", args, indices, buf.String())
		}
		if _, ok := last["optimizations"]; !ok {
			t.Fatalf("%v: stream does not end with the optimizations line:\n%s", args, buf.String())
		}
	}
}
