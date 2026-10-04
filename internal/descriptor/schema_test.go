package descriptor

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/frankbardon/pulse/types"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// TestPayloadSchema_VersionMatchesEnvelope ensures the schema's stamped
// version tracks the live envelope format_version. Drift here means the
// published contract advertises a version the engine does not emit.
func TestPayloadSchema_VersionMatchesEnvelope(t *testing.T) {
	if got := descriptor.NewEnvelope(nil).FormatVersion; got != PayloadSchemaFormatVersion {
		t.Fatalf("PayloadSchemaFormatVersion %q != envelope format_version %q",
			PayloadSchemaFormatVersion, got)
	}
}

// TestPayloadSchema_EnumsMatchRegistry verifies every registry-backed
// enum def carries exactly the registry's current value set — the
// anti-drift hinge for operator/kind/regression additions.
func TestPayloadSchema_EnumsMatchRegistry(t *testing.T) {
	var doc struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(BuildPayloadSchema(), &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	cases := map[string][]string{
		"AggregationType": stringify(types.AllAggregationTypes()),
		"FiltererType":    stringify(types.AllFiltererTypes()),
		"GroupType":       stringify(types.AllGroupTypes()),
		"AttributeType":   stringify(types.AllAttributeTypes()),
		"WindowType":      stringify(types.AllWindowTypes()),
		"FeatureType":     stringify(types.AllFeatureTypes()),
		"TestType":        stringify(types.AllTestTypes()),
		"OverlayKind":     stringify(types.AllOverlayKinds()),
		"RegressionType":  stringify(types.AllRegressionTypes()),
	}
	for name, want := range cases {
		def, ok := doc.Defs[name]
		if !ok {
			t.Errorf("$defs.%s missing from schema", name)
			continue
		}
		if !equalStrings(def.Enum, want) {
			t.Errorf("$defs.%s enum drift:\n got:  %v\n want: %v", name, def.Enum, want)
		}
	}
}

// TestPayloadSchema_MetaValidates compiles the generated document with a
// strict JSON Schema engine, proving it is a well-formed draft-2020-12
// schema (no dangling $ref, no invalid keyword shapes).
func TestPayloadSchema_MetaValidates(t *testing.T) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(BuildPayloadSchema()))
	if err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if err := c.AddResource(payloadSchemaID, doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	if _, err := c.Compile(payloadSchemaID); err != nil {
		t.Fatalf("schema is not a valid draft-2020-12 schema: %v", err)
	}
}

// TestPayloadSchema_ValidatesRepresentativePayloads round-trips real
// payloads through the compiled schema: a request must validate against
// #/$defs/Request and a wrapped response against #/$defs/Envelope.
func TestPayloadSchema_ValidatesRepresentativePayloads(t *testing.T) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(BuildPayloadSchema()))
	if err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if err := c.AddResource(payloadSchemaID, doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}

	// A representative process Request exercising several slots, a strict
	// OverlayRef arm, and a discriminant enum.
	req := types.Request{
		Cohort: &types.Cohort{Filename: "sales.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "amount", Values: []string{"10", "100"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "amount", Label: "total"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
				Ref:   types.OverlayRef{}, // implicit-margin kind: empty Ref is valid
			},
		},
	}
	validateAgainst(t, c, "#/$defs/Request", req)

	// A representative Response exercising the strict OverlayPayload union.
	scalar := 1.42
	resp := types.Response{
		Metadata: &types.ResponseMetadata{TotalRows: 100, FilteredRows: 80},
		Overlays: []types.OverlayLayer{
			{
				Name:  "chisq",
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
				Payload: types.OverlayPayload{
					Shape:  types.OverlayShapeScalar,
					Scalar: &scalar,
				},
			},
		},
	}
	validateAgainst(t, c, "#/$defs/Response", resp)

	// The same Response wrapped in the universal output Envelope.
	validateAgainst(t, c, "#/$defs/Envelope", descriptor.NewEnvelope(resp))
}

// TestPayloadSchema_UndefinedFiguresValidate: a response whose figures
// are undefined (NaN / ±Inf in Go) marshals them as null
// (types.MarshalFinite), and that wire form validates — the result-side
// float slots are "number or null". Request floats stay "number": a
// null where a request carries a number is still refused.
func TestPayloadSchema_UndefinedFiguresValidate(t *testing.T) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(BuildPayloadSchema()))
	if err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if err := c.AddResource(payloadSchemaID, doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	nan, inf := math.NaN(), math.Inf(1)
	resp := types.Response{
		Data:  []map[string]any{{"r": nan}},
		Tests: []*types.TestResult{{Type: types.TEST_T, Statistic: nan, DF: nan, PValue: inf, Alpha: 0.05}},
		Overlays: []types.OverlayLayer{{
			Name: "i_prior", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup,
			Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{
				Entries: []types.SeriesEntry{{Key: types.AxisKey{"a"}, Summary: types.OverlaySummary{Statistic: &nan}}}}},
		}, {
			Name: "s", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix,
			Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &nan},
		}},
		Components: &types.ResponseComponents{Aggregations: []types.AggregationComponents{
			{Label: "r", SumWeights: &nan, Operator: map[string]any{"ratio": nan}}}},
	}
	validateAgainst(t, c, "#/$defs/Response", resp)
	validateAgainst(t, c, "#/$defs/Envelope", descriptor.NewEnvelope(resp))

	sch, err := c.Compile(payloadSchemaID + "#/$defs/Test")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(`{"type":"TEST_T","alpha":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if sch.Validate(inst) == nil {
		t.Error("a request Test accepted alpha: null; request floats must stay number")
	}
}

func validateAgainst(t *testing.T, c *jsonschema.Compiler, fragment string, payload any) {
	t.Helper()
	sch, err := c.Compile(payloadSchemaID + fragment)
	if err != nil {
		t.Fatalf("compile %s: %v", fragment, err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("payload failed %s validation: %v\npayload: %s", fragment, err, raw)
	}
}

// schemaDoc decodes a payload schema into a generic document.
func schemaDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return doc
}

// compileSchema proves raw is a valid draft 2020-12 document whose every
// $ref resolves.
func compileSchema(t *testing.T, raw []byte) {
	t.Helper()
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if err := c.AddResource(payloadSchemaID, doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	if _, err := c.Compile(payloadSchemaID); err != nil {
		t.Fatalf("schema is not a valid draft-2020-12 schema: %v", err)
	}
}

// TestPayloadSchemaForInstance_FullScopedEqualsBuildPayloadSchema: a
// scoped instance enabling every reached built-in (a profile-free
// pulse.New) and the nil instance both serve BuildPayloadSchema byte for
// byte, and the $comment carries the digest the manifest carries.
func TestPayloadSchemaForInstance_FullScopedEqualsBuildPayloadSchema(t *testing.T) {
	want := string(BuildPayloadSchema())
	full := NewInstanceSnapshot(nil, FeatureSet{Enabled: ReachedFeatureNames(buildinfo.Version())})
	if got := string(BuildPayloadSchemaForInstance(full)); got != want {
		t.Error("full scoped instance schema differs from BuildPayloadSchema")
	}
	if got, err := PayloadSchemaForInstance(nil); err != nil || string(got) != want {
		t.Errorf("nil instance schema differs from BuildPayloadSchema (err %v)", err)
	}
	doc := schemaDoc(t, []byte(want))
	if got, wantC := doc["$comment"], payloadSchemaDigestPrefix+BuildManifest().FeatureSetDigest; got != wantC {
		t.Errorf("$comment = %v, want %q", got, wantC)
	}
}

// TestPayloadSchemaForInstance_ScopedOmitsHidden: a narrow instance's
// schema lists only enabled enum values, drops the hidden Request slots
// and every def reachable only through them, drops hidden-capability
// roots, keeps Request / Response / Envelope, stays a valid draft
// 2020-12 document and carries the instance digest.
func TestPayloadSchemaForInstance_ScopedOmitsHidden(t *testing.T) {
	inst := NewInstanceSnapshot(nil, FeatureSet{Enabled: []string{
		featProcess, "AGG_COUNT", "AGG_SUM", "GROUP_CATEGORY", "TEST_T",
	}})
	raw := BuildPayloadSchemaForInstance(inst)
	compileSchema(t, raw)
	doc := schemaDoc(t, raw)

	if got, want := doc["$comment"], payloadSchemaDigestPrefix+inst.Digest(); got != want {
		t.Errorf("$comment = %v, want %q", got, want)
	}
	if doc["$id"] != payloadSchemaID {
		t.Errorf("$id = %v, want %q", doc["$id"], payloadSchemaID)
	}
	defs := doc["$defs"].(map[string]any)

	enum := func(name string) []any {
		def, ok := defs[name].(map[string]any)
		if !ok {
			t.Fatalf("$defs.%s missing", name)
		}
		e, _ := def["enum"].([]any)
		return e
	}
	for name, want := range map[string][]any{
		"AggregationType": {"AGG_COUNT", "AGG_SUM"},
		"GroupType":       {"GROUP_CATEGORY"},
		"TestType":        {"TEST_T"},
		"FiltererType":    {},
		"OverlayKind":     {},
		"RegressionType":  {},
	} {
		got := enum(name)
		if len(got) != len(want) {
			t.Errorf("$defs.%s enum = %v, want %v", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("$defs.%s enum = %v, want %v", name, got, want)
				break
			}
		}
	}

	props := defs["Request"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"crosstab", "joins", "overlays"} {
		if _, ok := props[k]; ok {
			t.Errorf("Request.%s present on an instance hiding it", k)
		}
	}
	if _, ok := props["aggregations"]; !ok {
		t.Error("Request.aggregations missing")
	}
	for _, name := range []string{
		"CrosstabSpec", "JoinSpec", "OverlaySpec", // reachable only through hidden slots
		"ComposedRequest", "ComposedResponse", "ChainRequest", "ChainResponse",
		"FacetRequest", "FacetResult", "SampleRequest", "LookupRequest", "LookupResult",
	} {
		if _, ok := defs[name]; ok {
			t.Errorf("$defs.%s present on an instance hiding it", name)
		}
	}
	for _, name := range []string{"Request", "Response", "Envelope"} {
		if _, ok := defs[name]; !ok {
			t.Errorf("$defs.%s missing", name)
		}
	}
	if desc := doc["description"].(string); bytes.Contains([]byte(desc), []byte("ComposedRequest")) {
		t.Errorf("root description names a hidden root: %q", desc)
	}
}

// TestPayloadSchemaForInstance_EnabledCapabilityKeepsRoot: enabling a
// capability brings its root (and, for a host listing an enabled
// overlay kind, the overlays slot) back.
func TestPayloadSchemaForInstance_EnabledCapabilityKeepsRoot(t *testing.T) {
	inst := NewInstanceSnapshot(nil, FeatureSet{Enabled: []string{
		featProcess, featCompose, featSample, featCrosstab, "AGG_COUNT", "OVERLAY_SHARE_OF_ROW",
	}})
	raw := BuildPayloadSchemaForInstance(inst)
	compileSchema(t, raw)
	defs := schemaDoc(t, raw)["$defs"].(map[string]any)
	for _, name := range []string{"ComposedRequest", "ComposedResponse", "SampleRequest", "CrosstabSpec", "OverlaySpec"} {
		if _, ok := defs[name]; !ok {
			t.Errorf("$defs.%s missing on an instance enabling it", name)
		}
	}
	for _, name := range []string{"ChainRequest", "FacetRequest", "LookupRequest", "JoinSpec"} {
		if _, ok := defs[name]; ok {
			t.Errorf("$defs.%s present on an instance hiding it", name)
		}
	}
	props := defs["Request"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"crosstab", "overlays"} {
		if _, ok := props[k]; !ok {
			t.Errorf("Request.%s missing on an instance enabling it", k)
		}
	}
}
