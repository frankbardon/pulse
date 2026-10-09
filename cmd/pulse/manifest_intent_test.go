package main

import (
	"encoding/json"
	"testing"
)

// TestRootManifest_Intent: `pulse --json --intent X` prints the
// envelope-wrapped scoped manifest (scope set, fixed sections absent); an
// unknown intent is a coded error envelope.
func TestRootManifest_Intent(t *testing.T) {
	out, err := runApp(t, "--json", "--slim", "--intent", "compare_groups")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	var scope struct {
		Intent struct {
			ID string `json:"id"`
		} `json:"intent"`
	}
	if err := json.Unmarshal(env.Data["scope"], &scope); err != nil || scope.Intent.ID != "compare_groups" {
		t.Errorf("scope = %s", env.Data["scope"])
	}
	for _, k := range []string{"commands", "cohort_types", "mcp_tools", "error_codes"} {
		if _, ok := env.Data[k]; ok {
			t.Errorf("elided %s present", k)
		}
	}

	out, err = runApp(t, "--json", "--intent", "nope")
	if err != nil {
		t.Fatal(err)
	}
	var bad struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &bad); err != nil || len(bad.Errors) != 1 || bad.Errors[0].Code != "PULSE_RECOMMEND_INTENT_UNKNOWN" {
		t.Errorf("unknown intent envelope: %s", out)
	}
}
