package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type exampleHit struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Intents  []string `json:"intents"`
}

type examplesSearchEnvelope struct {
	Data   []exampleHit `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

func decodeExamplesSearch(t *testing.T, text string) examplesSearchEnvelope {
	t.Helper()
	var env examplesSearchEnvelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decoding envelope: %v\n%s", err, text)
	}
	return env
}

// TestCliExamplesSearch: `pulse examples search` is a thin adapter over
// pulse.ExamplesSearchWith — --intent filters, the query may be given as
// arguments, a misspelt word reaches the fuzzy tier, the synonym tier
// answers an alias, and an unknown intent is its own coded envelope.
func TestCliExamplesSearch(t *testing.T) {
	text, err := runApp(t, "examples", "search", "--intent", "compare_groups", "--json", "regon")
	if err != nil {
		t.Fatalf("intent + fuzzy: %v\n%s", err, text)
	}
	env := decodeExamplesSearch(t, text)
	if len(env.Data) == 0 {
		t.Fatalf(`--intent compare_groups "regon" found nothing: %s`, text)
	}
	for _, s := range env.Data {
		if !slices.Contains(s.Intents, "compare_groups") {
			t.Errorf("%s lacks compare_groups", s.Name)
		}
	}
	if !slices.ContainsFunc(env.Data, func(s exampleHit) bool {
		return strings.Contains(s.Name, "region")
	}) {
		t.Errorf(`"regon" found no region-named example: %s`, text)
	}

	// The facade's synonym tier: "chisq.test" is an alias, not a literal hit.
	text, err = runApp(t, "examples", "search", "--query", "chisq.test", "--json")
	if err != nil {
		t.Fatalf("alias: %v\n%s", err, text)
	}
	if env := decodeExamplesSearch(t, text); len(env.Data) == 0 {
		t.Errorf(`alias "chisq.test" found nothing: %s`, text)
	}

	text, err = runApp(t, "examples", "search", "--intent", "nope", "--json")
	if err == nil {
		t.Fatalf("unknown intent: no error\n%s", text)
	}
	if env := decodeExamplesSearch(t, text); len(env.Errors) != 1 || env.Errors[0].Code != "PULSE_RECOMMEND_INTENT_UNKNOWN" {
		t.Errorf("unknown intent envelope = %s", text)
	}

	text, err = runApp(t, "examples", "search", "--query", "count", "--json", "mean")
	if err == nil {
		t.Fatalf("--query beside arguments: no error\n%s", text)
	}
	if env := decodeExamplesSearch(t, text); len(env.Errors) != 1 || env.Errors[0].Code != "CLI_INPUT" {
		t.Errorf("--query beside arguments envelope = %s", text)
	}
}
