//go:build heavy

package regression_test

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing/regression"
	_ "github.com/frankbardon/pulse/internal/regadapt"
	"github.com/frankbardon/pulse/types"
)

// TestRegressionOutputsHoldAtRuntime holds the descriptor's per-type
// applicability table (which every regression Interpretation is
// validated against) to what the engines really emit on the plain fit
// path: every listed key appears in at least one probe of the type, and
// no probe emits a fit-output key another type owns but this row omits
// (no r2 on REG_GLM, no p_values on REG_BAYES_LINEAR, pseudo_r2 on
// REG_GLM only). Echo fields (type, family, penalty, ...) are ignored.
func TestRegressionOutputsHoldAtRuntime(t *testing.T) {
	universe := map[string]bool{}
	for _, r := range types.AllRegressionTypes() {
		for _, k := range descx.RegressionOutputKeys(string(r)) {
			universe[k] = true
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64}, {Name: "b", Type: encoding.FieldTypeF64},
		{Name: "x1", Type: encoding.FieldTypeF64}, {Name: "x2", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeF64}, {Name: "c", Type: encoding.FieldTypeF64},
		{Name: "t", Type: encoding.FieldTypeF64}, {Name: "m", Type: encoding.FieldTypeF64},
	}}
	rows := probeRows()
	for _, r := range types.AllRegressionTypes() {
		specs, ok := regressionProbes[r]
		if !ok {
			t.Errorf("%s has no runtime probe; add one to regressionProbes", r)
			continue
		}
		want := descx.RegressionOutputKeys(string(r))
		emitted := map[string]bool{}
		for i := range specs {
			spec := specs[i]
			res, err := regression.FitBuffered([]*types.RegressionSpec{&spec}, schema, rows)
			if err != nil {
				t.Fatalf("%s probe %d: %v", r, i, err)
			}
			raw, err := json.Marshal(res[0])
			if err != nil {
				t.Fatalf("%s probe %d: marshal: %v", r, i, err)
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(raw, &keys); err != nil {
				t.Fatal(err)
			}
			for k := range keys {
				if !universe[k] {
					continue // an echo of the spec, not a fit output
				}
				emitted[k] = true
				if !slices.Contains(want, k) {
					t.Errorf("%s probe %d emits %q, which its applicability row %v omits", r, i, k, want)
				}
			}
		}
		var missing []string
		for _, k := range want {
			if !emitted[k] {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: applicability row lists %s, but no probe emits them", r, strings.Join(missing, ", "))
		}
	}
}
