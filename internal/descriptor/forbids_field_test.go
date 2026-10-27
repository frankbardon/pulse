package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// TestCapabilities_ForbidsFieldMatchesWindowRule: an operator declares
// ForbidsField exactly when the shared window rule refuses a field on
// its slot (windowparams.FieldForbidden — the rule predict and the
// runtime both apply, PULSE_WINDOW_INVALID), its AcceptsTypes is then
// the empty list (on the wire `"accepts_types": []`, never a type list
// and never null), and no non-window operator declares it. The manifest
// therefore names none of them as compatible with any field type.
func TestCapabilities_ForbidsFieldMatchesWindowRule(t *testing.T) {
	t.Parallel()

	forbidding := 0
	for kind, ops := range capabilityOperatorTables() {
		for _, op := range ops {
			want := kind == "window" && windowparams.FieldForbidden(types.WindowType(op.Name))
			if op.ForbidsField != want {
				t.Errorf("%s %s: ForbidsField %v, shared window rule %v", kind, op.Name, op.ForbidsField, want)
			}
			if !op.ForbidsField {
				continue
			}
			forbidding++
			if op.AcceptsTypes == nil || len(op.AcceptsTypes) != 0 {
				t.Errorf("%s %s forbids a field yet declares AcceptsTypes %#v", kind, op.Name, op.AcceptsTypes)
			}
			b, err := json.Marshal(op)
			if err != nil {
				t.Fatal(err)
			}
			if s := string(b); !strings.Contains(s, `"accepts_types":[]`) || !strings.Contains(s, `"forbids_field":true`) {
				t.Errorf("%s %s wire form %s", kind, op.Name, s)
			}
		}
	}
	if forbidding != 3 {
		t.Errorf("vacuous: %d operators forbid a field, want the 3 order-only windows", forbidding)
	}
	for _, ct := range BuildManifest().CohortTypes {
		for _, w := range ct.CompatibleWindows {
			if windowparams.FieldForbidden(types.WindowType(w)) {
				t.Errorf("cohort type %s lists order-only window %s as compatible", ct.Name, w)
			}
		}
	}
}
