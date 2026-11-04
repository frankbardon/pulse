package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/paramread"
)

// TestFieldInputsFor_ScalarStandIn: the projection callback of an
// extension operator with an expression-capable param runs before the
// pass resolves it, so it is handed a stand-in number in place of an
// expression string — its decode sees the declared type and its field
// list is unchanged — while a number and every other key reach it as
// written.
func TestFieldInputsFor_ScalarStandIn(t *testing.T) {
	t.Parallel()

	var seen []string
	r := &ExtensionRegistry{
		FieldInputs: map[string]FieldInputsFunc{StreamabilityKey("aggregator", "AGG_X_EXT"): func(raw json.RawMessage) []string {
			seen = append(seen, string(raw))
			var p struct {
				Scale float64 `json:"scale"`
				Other string  `json:"other"`
			}
			if json.Unmarshal(raw, &p) != nil {
				return nil
			}
			return []string{p.Other}
		}},
		ScalarParams: map[string][]string{StreamabilityKey("aggregator", "AGG_X_EXT"): {"scale"}},
	}
	got, ok := r.FieldInputsFor("aggregator", "AGG_X_EXT", json.RawMessage(`{"scale":"params.k * 2","other":"f"}`))
	if !ok || len(got) != 1 || got[0] != "f" {
		t.Fatalf("FieldInputsFor = %v %v (callback saw %v), want [f]", got, ok, seen)
	}
	if seen[0] != `{"other":"f","scale":0}` {
		t.Fatalf("callback saw %s", seen[0])
	}
	in := `{"scale":2,"other":"f"}`
	if _, ok := r.FieldInputsFor("aggregator", "AGG_X_EXT", json.RawMessage(in)); !ok || seen[1] != in {
		t.Fatalf("a number param reached the callback as %s", seen[1])
	}
	if keys := r.ExtensionScalarParams(paramread.Aggregator, "AGG_X_EXT"); len(keys) != 1 || keys[0] != "scale" {
		t.Fatalf("ExtensionScalarParams = %v", keys)
	}
	hidden := r.WithHidden(func(n string) bool { return n == "AGG_X_EXT" })
	if keys := hidden.ExtensionScalarParams(paramread.Aggregator, "AGG_X_EXT"); keys != nil {
		t.Fatalf("a hidden operator's params = %v, want none", keys)
	}
}

// TestScalarParamsByOperator: the template validator's flagged-keys
// map is ScalarParams keyed by operator name alone, every category
// kept, a hidden operator absent, and each list a copy.
func TestScalarParamsByOperator(t *testing.T) {
	t.Parallel()

	var nilReg *ExtensionRegistry
	if got := nilReg.ScalarParamsByOperator(); got != nil {
		t.Fatalf("nil registry = %v, want nil", got)
	}
	r := &ExtensionRegistry{ScalarParams: map[string][]string{
		StreamabilityKey("aggregator", "AGG_X_EXT"): {"scale"},
		StreamabilityKey("window", "WIN_X_EXT"):     {"alpha", "scale"},
		StreamabilityKey("matrix", "MAT_X_HIDDEN"):  {"k"},
	}}
	r = r.WithHidden(func(name string) bool { return name == "MAT_X_HIDDEN" })
	got := r.ScalarParamsByOperator()
	if len(got) != 2 || len(got["AGG_X_EXT"]) != 1 || got["AGG_X_EXT"][0] != "scale" ||
		len(got["WIN_X_EXT"]) != 2 || got["WIN_X_EXT"][1] != "scale" {
		t.Fatalf("ScalarParamsByOperator = %v", got)
	}
	got["AGG_X_EXT"][0] = "mutated"
	if r.ExtensionScalarParams(paramread.Category("aggregator"), "AGG_X_EXT")[0] != "scale" {
		t.Fatal("the returned list aliases the registry's")
	}
}
