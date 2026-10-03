package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The tests in this file pin that the schema-bind-on-inspect enums and
// the strict request decode see only what the instance offers: a
// hidden operator / overlay kind / label table / request slot is
// advertised and refused exactly as a never-registered one.

// instanceFor builds a Pulse over an empty in-memory fs with fp (nil:
// profile-free) and ext, and returns it with its instance snapshot.
func instanceFor(t *testing.T, fp *pulse.FeatureProfile, ext pulse.Extensions) (*pulse.Pulse, *descx.InstanceSnapshot) {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fp, Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, facadebridge.InstanceSnapshot(p)
}

// fixtureProfile loads one of the private feature-profile fixtures.
func fixtureProfile(t *testing.T, name string) *pulse.FeatureProfile {
	t.Helper()
	data, err := os.ReadFile("../../descriptor/testdata/profiles/" + name + ".json")
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	fp, err := pulse.ParseFeatureProfile(data)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return fp
}

// labelExt registers one label table so the labels slot is exercised.
var labelExt = pulse.Extensions{LabelTables: map[string]pulse.LabelTable{
	"regions": {Description: "Region names", Rows: map[string]string{"alpha": "Alpha"}},
}}

// collectEnums gathers every string in every "enum" array of a JSON
// Schema document.
func collectEnums(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			for k, child := range n {
				if k == "enum" {
					if arr, ok := child.([]any); ok {
						for _, e := range arr {
							if s, ok := e.(string); ok {
								out = append(out, s)
							}
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(doc)
	return out
}

// TestBindForInstance_EnumsCarryOnlyEnabledNames: over every fixture,
// every enum value of every bound tool schema is a cohort field, a
// fixed literal, or a name the instance enables — never a hidden one.
// The enabled operators of the fixture do appear (non-vacuous).
func TestBindForInstance_EnumsCarryOnlyEnabledNames(t *testing.T) {
	schema := makeBindSchema()
	literals := map[string]bool{}
	for _, f := range schema.Fields {
		literals[f.Name] = true
	}
	for _, l := range []string{"none", "row", "column", "total", "matrix", "long", "replace", "augment"} {
		literals[l] = true
	}
	for _, tc := range []struct {
		fixture string
		want    []string // enabled names that must surface somewhere
	}{
		{fixture: "empty"},
		{fixture: "minimal", want: []string{"AGG_COUNT", "AGG_SUM", "GROUP_CATEGORY", "GROUP_RANGE"}},
		{fixture: "survey-crosstab", want: []string{"AGG_WELFORD", "FILTER_RANGE", "TEST_WELCH", "OVERLAY_SHARE_OF_ROW", "OVERLAY_PAIRWISE_WELCH_T"}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			_, inst := instanceFor(t, fixtureProfile(t, tc.fixture), labelExt)
			if !inst.Scoped() {
				t.Fatal("fixture instance is not scoped")
			}
			bound, err := BindForInstance(schema, inst)
			if err != nil {
				t.Fatalf("BindForInstance: %v", err)
			}
			seen := map[string]bool{}
			for tool, raw := range bound {
				for _, v := range collectEnums(t, raw) {
					seen[v] = true
					if literals[v] {
						continue
					}
					if !inst.Enabled(v) {
						t.Errorf("%s: enum value %q is not enabled on the instance", tool, v)
					}
				}
				for _, h := range inst.HiddenNames() {
					if bytes.Contains(raw, []byte(`"`+h+`"`)) {
						t.Errorf("%s: hidden name %q appears in the bound schema", tool, h)
					}
				}
			}
			for _, w := range tc.want {
				if !seen[w] {
					t.Errorf("enabled %q missing from every bound enum", w)
				}
			}
		})
	}
}

// TestBindForInstance_MinimalAggregatorEnum pins the exact filtered
// enum, and that an extension operator the profile keeps is merged in.
func TestBindForInstance_MinimalAggregatorEnum(t *testing.T) {
	_, inst := instanceFor(t, fixtureProfile(t, "minimal"), pulse.Extensions{})
	bound, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	req := decodeBoundRequest(t, bound[toolmeta.ToolProcess])
	if got, want := enumOf(req, "aggregations", "type"), []string{"AGG_COUNT", "AGG_SUM"}; !slices.Equal(got, want) {
		t.Errorf("aggregations.type enum = %v, want %v", got, want)
	}
	if got := enumOf(req, "filterers", "type"); got != nil {
		t.Errorf("no filterer enabled: filterers.type must carry no enum, got %v", got)
	}
}

// TestBindForInstance_HiddenSlotsDropped: a request slot the instance
// hides is absent from every bound root; an enabled one stays.
func TestBindForInstance_HiddenSlotsDropped(t *testing.T) {
	schema := makeBindSchema()
	props := func(raw json.RawMessage) map[string]any {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc["properties"].(map[string]any)
	}

	_, minimal := instanceFor(t, fixtureProfile(t, "minimal"), pulse.Extensions{})
	bound, err := BindForInstance(schema, minimal)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"crosstab", "overlays"} {
		if _, ok := props(bound[toolmeta.ToolProcess])[k]; ok {
			t.Errorf("minimal: pulse_process schema still offers hidden slot %q", k)
		}
	}
	for _, tool := range []string{toolmeta.ToolCompose, toolmeta.ToolProcessChain, toolmeta.ToolFacetSchema} {
		if _, ok := props(bound[tool])["overlays"]; ok {
			t.Errorf("minimal: %s schema still offers hidden overlays", tool)
		}
	}

	_, survey := instanceFor(t, fixtureProfile(t, "survey-crosstab"), pulse.Extensions{})
	bound, err = BindForInstance(schema, survey)
	if err != nil {
		t.Fatal(err)
	}
	p := props(bound[toolmeta.ToolProcess])
	if _, ok := p["crosstab"]; !ok {
		t.Error("survey-crosstab: crosstab slot must stay")
	}
	if _, ok := p["overlays"]; !ok {
		t.Error("survey-crosstab: overlays slot must stay (crosstab host has enabled kinds)")
	}
}

// TestBindForInstance_LabelsFollowCapability: with capability:labels
// hidden, the bound labels slot is byte-identical to an instance that
// registered no label table; enabled, it lists the table.
func TestBindForInstance_LabelsFollowCapability(t *testing.T) {
	schema := makeBindSchema()
	features := []string{"capability:process", "AGG_COUNT"}

	_, withTable := instanceFor(t, &pulse.FeatureProfile{Features: features}, labelExt)
	_, noTable := instanceFor(t, &pulse.FeatureProfile{Features: features}, pulse.Extensions{})
	a, err := BindForInstance(schema, withTable)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BindForInstance(schema, noTable)
	if err != nil {
		t.Fatal(err)
	}
	for tool := range b {
		if !bytes.Equal(a[tool], b[tool]) {
			t.Errorf("%s: hidden label table changes the bound schema\nwith:    %s\nwithout: %s", tool, a[tool], b[tool])
		}
	}

	_, enabled := instanceFor(t, &pulse.FeatureProfile{Features: append(features, "capability:labels")}, labelExt)
	c, err := BindForInstance(schema, enabled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(c[toolmeta.ToolProcess], []byte(`"regions"`)) {
		t.Error("capability:labels enabled: the bound labels enum must list the table")
	}
}

// TestBindForInstance_ProfileFreeByteIdentical: a profile-free Pulse
// binds byte-identically to the unscoped binder over its extensions.
func TestBindForInstance_ProfileFreeByteIdentical(t *testing.T) {
	schema := makeBindSchema()
	_, inst := instanceFor(t, nil, labelExt)
	got, err := BindForInstance(schema, inst)
	if err != nil {
		t.Fatal(err)
	}
	want, err := BindWithExtensions(schema, inst.Extensions())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("tool count %d, want %d", len(got), len(want))
	}
	for tool := range want {
		if !bytes.Equal(got[tool], want[tool]) {
			t.Errorf("%s: profile-free bound schema drifted", tool)
		}
	}
	// And the unscoped binder still advertises every built-in.
	req := decodeBoundRequest(t, want[toolmeta.ToolProcess])
	if got := enumOf(req, "aggregations", "type"); len(got) != len(types.AllAggregationTypes()) {
		t.Errorf("profile-free aggregations enum has %d names, want %d", len(got), len(types.AllAggregationTypes()))
	}
}

// codedOf unwraps a *perr.CodedError.
func codedOf(t *testing.T, err error) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("not a coded error: %v", err)
	}
	return ce
}

// sameRefusal compares two refusals on code, message and details.
func sameRefusal(t *testing.T, label string, got, want *perr.CodedError) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
	if got.Code != want.Code || got.Message != want.Message || !reflect.DeepEqual(got.Details, want.Details) {
		t.Errorf("%s: MCP refusal differs from the library's\ngot:  %s %v\nwant: %s %v", label, got.Message, got.Details, want.Message, want.Details)
	}
}

// TestCheckUnknownKeys_HiddenSlotRefusedLikeLibrary: a hidden slot sent
// over MCP is refused exactly as the library's request-slot gate refuses
// it, at the root and nested in compose / chain.
func TestCheckUnknownKeys_HiddenSlotRefusedLikeLibrary(t *testing.T) {
	_, inst := instanceFor(t, fixtureProfile(t, "minimal"), pulse.Extensions{})
	crosstab := &types.CrosstabSpec{}

	sameRefusal(t, "request",
		checkUnknownRequestKeys([]byte(`{"crosstab":{}}`), inst),
		codedOf(t, descx.SlotRefusal(&types.Request{Crosstab: crosstab}, inst)))

	sameRefusal(t, "compose nested",
		checkUnknownKeysComposed([]byte(`{"requests":[{},{"crosstab":{}}]}`), inst),
		codedOf(t, descx.SlotRefusal(&types.ComposedRequest{Requests: []*types.Request{{}, {Crosstab: crosstab}}}, inst)))

	sameRefusal(t, "compose root first",
		checkUnknownKeysComposed([]byte(`{"overlays":[{}],"requests":[{"crosstab":{}}]}`), inst),
		codedOf(t, descx.SlotRefusal(&types.ComposedRequest{
			Overlays: []types.ComposeOverlaySpec{{}},
			Requests: []*types.Request{{Crosstab: crosstab}},
		}, inst)))

	sameRefusal(t, "chain nested",
		checkUnknownKeysChain([]byte(`{"stages":[{"request":{"crosstab":{}}}]}`), inst),
		codedOf(t, descx.SlotRefusal(&types.ChainRequest{Stages: []*types.ChainStage{{Request: &types.Request{Crosstab: crosstab}}}}, inst)))

	sameRefusal(t, "chain root first",
		checkUnknownKeysChain([]byte(`{"overlays":[{}],"stages":[{"request":{"crosstab":{}}}]}`), inst),
		codedOf(t, descx.SlotRefusal(&types.ChainRequest{
			Overlays: []*types.ChainOverlaySpec{{}},
			Stages:   []*types.ChainStage{{Request: &types.Request{Crosstab: crosstab}}},
		}, inst)))
}

// TestCheckUnknownKeys_HiddenSlotNeverSuggested: a misspelling near a
// hidden slot neither suggests it nor lists it in valid_keys — the
// refusal is the one an instance without that slot would give.
func TestCheckUnknownKeys_HiddenSlotNeverSuggested(t *testing.T) {
	_, inst := instanceFor(t, fixtureProfile(t, "minimal"), pulse.Extensions{})
	ce := checkUnknownRequestKeys([]byte(`{"crosstabb":{}}`), inst)
	if ce == nil {
		t.Fatal("expected an unknown-field refusal")
	}
	want := descx.UnknownFieldError([]string{"crosstabb"}, descx.VisibleSlotKeys(&types.Request{}, inst))
	sameRefusal(t, "typo", ce, want)
	valid, _ := ce.Details["valid_keys"].([]string)
	for _, hidden := range []string{"crosstab", "joins", "overlays"} {
		if slices.Contains(valid, hidden) {
			t.Errorf("valid_keys lists hidden slot %q", hidden)
		}
		if strings.Contains(ce.Message, `"`+hidden+`"`) {
			t.Errorf("message names hidden slot %q: %s", hidden, ce.Message)
		}
	}
	if s, _ := ce.Details["suggestions"].(map[string]any); s["crosstabb"] != nil {
		t.Errorf("suggestion points at a hidden slot: %v", s)
	}

	// Profile-free: the same key is a valid slot, and the typo suggests it.
	_, free := instanceFor(t, nil, pulse.Extensions{})
	if ce := checkUnknownRequestKeys([]byte(`{"crosstab":{}}`), free); ce != nil {
		t.Errorf("profile-free: crosstab refused: %v", ce)
	}
	sameRefusal(t, "profile-free typo",
		checkUnknownRequestKeys([]byte(`{"crosstabb":{}}`), free),
		checkUnknownRequestKeys([]byte(`{"crosstabb":{}}`), nil))
}

// TestTools_InvokeScopesStrictDecodeToInstance: the tool catalog's
// Invoke reads the calling instance, so pulse_process on a profiled
// Pulse refuses a misspelt key against the instance's visible slots
// (an unscoped decode would list and suggest the hidden crosstab).
func TestTools_InvokeScopesStrictDecodeToInstance(t *testing.T) {
	p, inst := instanceFor(t, fixtureProfile(t, "minimal"), pulse.Extensions{})
	var invoke InvokeFunc
	for _, d := range Tools(Config{}) {
		if d.Name == toolmeta.ToolProcess {
			invoke = d.Invoke
		}
	}
	if invoke == nil {
		t.Fatal("pulse_process not in the catalog")
	}
	_, err := invoke(context.Background(), p, json.RawMessage(`{"cohort":{"filename":"x.pulse"},"crosstabb":{}}`))
	ce := codedOf(t, err)
	if ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("code = %s, want PULSE_REQUEST_UNKNOWN_FIELD", ce.Code)
	}
	want := descx.VisibleSlotKeys(&types.Request{}, inst)
	sort.Strings(want)
	if got, _ := ce.Details["valid_keys"].([]string); !slices.Equal(got, want) {
		t.Errorf("valid_keys = %v, want the instance's visible keys %v", got, want)
	}
}
