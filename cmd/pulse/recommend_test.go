package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// recommendEnvelope is the slice of a `pulse recommend --json` envelope
// the CLI test reads.
type recommendEnvelope struct {
	Data *struct {
		Intent          string `json:"intent"`
		Bound           bool   `json:"bound"`
		Recommendations []struct {
			Operator string          `json:"operator"`
			Bound    bool            `json:"bound"`
			Request  json.RawMessage `json:"request"`
		} `json:"recommendations"`
		RoutesTo []struct {
			Use string `json:"use"`
		} `json:"routes_to"`
	} `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

func decodeRecommend(t *testing.T, text string) recommendEnvelope {
	t.Helper()
	var env recommendEnvelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decoding envelope: %v\n%s", err, text)
	}
	return env
}

// TestCliRecommend: `pulse recommend` is a thin adapter over
// pulse.Recommend — unbound skeletons without --cohort, drafts bound to
// the cohort's own fields with it (a --field hint lands in a draft), a
// tooling route for a non-analytic intent, and every refusal under its
// own code in errors[0].
func TestCliRecommend(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "c.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeGroupCSV(t, dir), "--output", cohort); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}

	text, err := runApp(t, "recommend", "--intent", "compare_groups", "--json")
	if err != nil {
		t.Fatalf("unbound: %v\n%s", err, text)
	}
	env := decodeRecommend(t, text)
	if env.Data == nil || env.Data.Bound || env.Data.Intent != "compare_groups" || len(env.Data.Recommendations) == 0 {
		t.Fatalf("unbound envelope = %s", text)
	}
	var draft any
	if err := json.Unmarshal(env.Data.Recommendations[0].Request, &draft); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(draft), "<field>") {
		t.Errorf("unbound draft carries no placeholder: %s", env.Data.Recommendations[0].Request)
	}

	text, err = runApp(t, "recommend", "--intent", "describe", "--cohort", cohort, "--field", "qty", "--limit", "3", "--json")
	if err != nil {
		t.Fatalf("bound: %v\n%s", err, text)
	}
	env = decodeRecommend(t, text)
	if env.Data == nil || !env.Data.Bound || len(env.Data.Recommendations) == 0 || len(env.Data.Recommendations) > 3 {
		t.Fatalf("bound envelope = %s", text)
	}
	if !strings.Contains(string(env.Data.Recommendations[0].Request), `"qty"`) {
		t.Errorf("top bound draft ignores the --field hint: %s", env.Data.Recommendations[0].Request)
	}

	text, err = runApp(t, "recommend", "--intent", "simulate", "--json")
	if err != nil {
		t.Fatalf("routed: %v\n%s", err, text)
	}
	if env = decodeRecommend(t, text); env.Data == nil || len(env.Data.Recommendations) != 0 || len(env.Data.RoutesTo) == 0 {
		t.Fatalf("simulate envelope = %s", text)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--intent", "no_such_intent"}, "PULSE_RECOMMEND_INTENT_UNKNOWN"},
		{[]string{"--intent", "describe", "--cohort", cohort, "--field", "nope"}, "SERVICE_VALIDATION"},
		{[]string{"--intent", "describe", "--cohort", filepath.Join(dir, "missing.pulse")}, "DATA_FILE"},
		{nil, "CLI_INPUT"},
	} {
		text, _ := runApp(t, append(append([]string{"recommend"}, tc.args...), "--json")...)
		if env := decodeRecommend(t, text); len(env.Errors) == 0 || env.Errors[0].Code != tc.want {
			t.Errorf("recommend %v: errors = %+v, want %s", tc.args, env.Errors, tc.want)
		}
	}

	text, err = runApp(t, "recommend", "--intent", "describe")
	if err != nil {
		t.Fatalf("text: %v\n%s", err, text)
	}
	if !strings.Contains(text, "Intent describe (unbound)") || !strings.Contains(text, "request: {") {
		t.Errorf("text output = %s", text)
	}
}
