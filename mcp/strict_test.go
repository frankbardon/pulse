package mcp

import (
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

func TestJsonObjectKeys_RequestSlots(t *testing.T) {
	keys := requestSlotKeys
	for _, want := range []string{"cohort", "groups", "aggregations", "filterers", "windows", "tests", "post_tests"} {
		if !slices.Contains(keys, want) {
			t.Errorf("requestSlotKeys missing %q; got %v", want, keys)
		}
	}
	// The manifest catalog field names must NOT be request slots — that
	// confusion is exactly what this guard protects against.
	for _, bad := range []string{"groupers", "aggregators"} {
		if slices.Contains(keys, bad) {
			t.Errorf("requestSlotKeys unexpectedly contains catalog name %q", bad)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"groupers", "groups", 2},
		{"aggregators", "aggregations", 2},
		{"groups", "groups", 0},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckUnknownRequestKeys_GroupersSuggestsGroups(t *testing.T) {
	body := []byte(`{"cohort":{"filename":"x.pulse"},"groupers":[{"type":"GROUP_CATEGORY","field":"region"}]}`)
	ce := checkUnknownRequestKeys(body)
	if ce == nil {
		t.Fatal("expected error for unknown key 'groupers', got nil")
	}
	if ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Errorf("code = %s, want PULSE_REQUEST_UNKNOWN_FIELD", ce.Code)
	}
	if !strings.Contains(ce.Message, `"groups"`) {
		t.Errorf("message should suggest 'groups': %s", ce.Message)
	}
	sugg, _ := ce.Details["suggestions"].(map[string]any)
	if sugg["groupers"] != "groups" {
		t.Errorf("suggestions[groupers] = %v, want groups", sugg["groupers"])
	}
}

func TestCheckUnknownRequestKeys_AggregatorsSuggestsAggregations(t *testing.T) {
	body := []byte(`{"aggregators":[{"type":"AGG_SUM","field":"amount"}]}`)
	ce := checkUnknownRequestKeys(body)
	if ce == nil {
		t.Fatal("expected error for unknown key 'aggregators', got nil")
	}
	sugg, _ := ce.Details["suggestions"].(map[string]any)
	if sugg["aggregators"] != "aggregations" {
		t.Errorf("suggestions[aggregators] = %v, want aggregations", sugg["aggregators"])
	}
}

func TestCheckUnknownRequestKeys_ValidPasses(t *testing.T) {
	body := []byte(`{"cohort":{"filename":"x.pulse"},"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"amount"}]}`)
	if ce := checkUnknownRequestKeys(body); ce != nil {
		t.Fatalf("valid request flagged: %s", ce.Message)
	}
}

func TestCheckUnknownRequestKeys_UnrelatedKeyNoSuggestion(t *testing.T) {
	body := []byte(`{"xyzzy":1}`)
	ce := checkUnknownRequestKeys(body)
	if ce == nil {
		t.Fatal("expected error for unknown key 'xyzzy', got nil")
	}
	sugg, _ := ce.Details["suggestions"].(map[string]any)
	if _, ok := sugg["xyzzy"]; ok {
		t.Errorf("unrelated key should carry no suggestion; got %v", sugg["xyzzy"])
	}
}

func TestCheckUnknownRequestKeys_NonObjectIsNil(t *testing.T) {
	if ce := checkUnknownRequestKeys([]byte(`[1,2,3]`)); ce != nil {
		t.Errorf("array body should defer to typed decoder, got %s", ce.Code)
	}
	if ce := checkUnknownRequestKeys(nil); ce != nil {
		t.Errorf("empty body should be nil, got %s", ce.Code)
	}
}

func TestCheckUnknownKeysComposed_TagsIndex(t *testing.T) {
	body := []byte(`{"requests":[{"cohort":{"filename":"x.pulse"}},{"groupers":[]}]}`)
	ce := checkUnknownKeysComposed(body)
	if ce == nil {
		t.Fatal("expected error for unknown key in composed request[1], got nil")
	}
	if idx, _ := ce.Details["request_index"].(int); idx != 1 {
		t.Errorf("request_index = %v, want 1", ce.Details["request_index"])
	}
}

func TestCheckUnknownKeysChain_TagsStage(t *testing.T) {
	body := []byte(`{"cohort":{"filename":"x.pulse"},"stages":[{"name":"s0","request":{"groupers":[]}}]}`)
	ce := checkUnknownKeysChain(body)
	if ce == nil {
		t.Fatal("expected error for unknown key in chain stage 0, got nil")
	}
	if idx, _ := ce.Details["stage_index"].(int); idx != 0 {
		t.Errorf("stage_index = %v, want 0", ce.Details["stage_index"])
	}
	if ce.Details["stage_name"] != "s0" {
		t.Errorf("stage_name = %v, want s0", ce.Details["stage_name"])
	}
}

// TestStrictRequestDecode_RejectsUnknownKey proves the decode wrapper used by
// Invoke surfaces the coded error verbatim before the typed unmarshal.
func TestStrictRequestDecode_RejectsUnknownKey(t *testing.T) {
	_, err := strictRequestDecode([]byte(`{"groupers":[]}`))
	if err == nil {
		t.Fatal("expected unknown-key error")
	}
	ce, ok := err.(*perr.CodedError)
	if !ok || ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("err = %v, want PULSE_REQUEST_UNKNOWN_FIELD coded error", err)
	}
}
