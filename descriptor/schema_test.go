package descriptor

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// TestPayloadSchemaGolden pins the generated payload contract. Any change
// to a payload struct field, a registry enum value, or a strict-union
// shape changes this output; regenerate with:
//
//	go test ./descriptor/ -run TestPayloadSchemaGolden -update
func TestPayloadSchemaGolden(t *testing.T) {
	compareGolden(t, "payload-schema.json", BuildPayloadSchema())
}

// TestPayloadSchema_VersionMatchesEnvelope ensures the schema's stamped
// version tracks the live envelope format_version. Drift here means the
// published contract advertises a version the engine does not emit.
func TestPayloadSchema_VersionMatchesEnvelope(t *testing.T) {
	if got := NewEnvelope(nil).FormatVersion; got != PayloadSchemaFormatVersion {
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
	validateAgainst(t, c, "#/$defs/Envelope", NewEnvelope(resp))
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
