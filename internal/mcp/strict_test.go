package mcp

import (
	stderrors "errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
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
	// The location key is the service's (descx.RefusalAt): an MCP client
	// sees the same details a library caller does.
	if idx, ok := ce.Details["request"].(int); !ok || idx != 1 {
		t.Errorf("request = %v, want 1", ce.Details["request"])
	}
	assertDetailKeys(t, ce.Details, "unknown_keys", "suggestions", "valid_keys", "request")
}

func TestCheckUnknownKeysChain_TagsStage(t *testing.T) {
	body := []byte(`{"cohort":{"filename":"x.pulse"},"stages":[{"name":"s0","request":{"groupers":[]}}]}`)
	ce := checkUnknownKeysChain(body)
	if ce == nil {
		t.Fatal("expected error for unknown key in chain stage 0, got nil")
	}
	if idx, ok := ce.Details["stage"].(int); !ok || idx != 0 {
		t.Errorf("stage = %v, want 0", ce.Details["stage"])
	}
	assertDetailKeys(t, ce.Details, "unknown_keys", "suggestions", "valid_keys", "stage")
}

// assertDetailKeys pins the exact detail key set: the located
// PULSE_REQUEST_UNKNOWN_FIELD shape descx.SlotRefusal returns to a
// library caller (no MCP-only request_index / stage_index / stage_name).
func assertDetailKeys(t *testing.T, details map[string]any, want ...string) {
	t.Helper()
	got := make([]string, 0, len(details))
	for k := range details {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("detail keys = %v, want %v", got, want)
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

// TestCheckUnknownRequestKeys_SharesSlotGateShape: the MCP strict
// decoder and the instance-scoped request-slot gate build the SAME
// PULSE_REQUEST_UNKNOWN_FIELD error — only the candidate list differs
// (this package's requestSlotKeys stays the full, unscoped list until
// U06 scopes MCP registration).
func TestCheckUnknownRequestKeys_SharesSlotGateShape(t *testing.T) {
	ce := checkUnknownRequestKeys([]byte(`{"crosstabz":{},"groupers":[]}`))
	if ce == nil {
		t.Fatal("expected an unknown-field error")
	}
	want := descx.UnknownFieldError([]string{"crosstabz", "groupers"}, requestSlotKeys)
	if ce.Code != want.Code || ce.Message != want.Message || !reflect.DeepEqual(ce.Details, want.Details) {
		t.Errorf("strict decode diverges from the shared shape\ngot:  %s %v\nwant: %s %v", ce.Message, ce.Details, want.Message, want.Details)
	}

	// The slot gate's refusal of a hidden crosstab is that shape over
	// the instance's visible keys.
	inst := descx.NewInstanceSnapshot(nil, descx.FeatureSet{Enabled: []string{"capability:process"}})
	gate := descx.SlotRefusal(&types.Request{Crosstab: &types.CrosstabSpec{}}, inst)
	var gce *perr.CodedError
	if !stderrors.As(gate, &gce) {
		t.Fatalf("slot gate: %v", gate)
	}
	if gce.Code != ce.Code {
		t.Errorf("codes differ: %s vs %s", gce.Code, ce.Code)
	}
	for k := range ce.Details {
		if _, ok := gce.Details[k]; !ok {
			t.Errorf("slot gate details lack %q", k)
		}
	}
	if len(gce.Details) != len(ce.Details) {
		t.Errorf("detail keys differ: %v vs %v", gce.Details, ce.Details)
	}
	if !strings.HasPrefix(gce.Message, `request contains unrecognized top-level key(s): "crosstab"`) {
		t.Errorf("slot gate message: %s", gce.Message)
	}
}
