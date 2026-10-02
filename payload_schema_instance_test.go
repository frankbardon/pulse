package pulse

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// TestPayloadSchema_DefaultIsBuildPayloadSchema: a profile-free
// instance serves the full-registry schema byte for byte, and its
// $comment names the instance's FeatureSetDigest.
func TestPayloadSchema_DefaultIsBuildPayloadSchema(t *testing.T) {
	p, err := New(Options{FS: parityFS(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := p.PayloadSchema()
	if err != nil {
		t.Fatalf("PayloadSchema: %v", err)
	}
	if want := descx.BuildPayloadSchema(); !bytes.Equal(got, want) {
		t.Error("default instance PayloadSchema differs from BuildPayloadSchema")
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if want := "feature_set_digest: " + p.FeatureSetDigest(); doc["$comment"] != want {
		t.Errorf("$comment = %v, want %q", doc["$comment"], want)
	}
}

// collectRefs gathers every "$ref" target in v.
func collectRefs(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, ok := e.(string); ok && k == "$ref" {
				out[s] = true
				continue
			}
			collectRefs(e, out)
		}
	case []any:
		for _, e := range x {
			collectRefs(e, out)
		}
	}
}

// TestPayloadSchema_FixturesNameNoHiddenFeature: for every fixture
// profile the instance schema is a valid draft 2020-12 document with no
// dangling $ref and no orphan def, names no hidden operator anywhere,
// omits the root of every hidden capability, keeps Request / Response /
// Envelope, and carries the instance digest.
func TestPayloadSchema_FixturesNameNoHiddenFeature(t *testing.T) {
	roots := map[string][]string{
		"capability:compose":       {"ComposedRequest", "ComposedResponse"},
		"capability:process_chain": {"ChainRequest", "ChainResponse"},
		"capability:facet":         {"FacetRequest", "FacetResult"},
		"capability:sample":        {"SampleRequest"},
		"capability:lookup":        {"LookupRequest", "LookupResult"},
	}
	for _, name := range featureSetFixtures {
		t.Run(name, func(t *testing.T) {
			p := newFixturePulse(t, name, Options{})
			raw, err := p.PayloadSchema()
			if err != nil {
				t.Fatalf("PayloadSchema: %v", err)
			}

			c := jsonschema.NewCompiler()
			jdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			const id = "https://frankbardon.github.io/pulse/payload-schema.json"
			if err := c.AddResource(id, jdoc); err != nil {
				t.Fatalf("add resource: %v", err)
			}
			if _, err := c.Compile(id); err != nil {
				t.Fatalf("not a valid draft 2020-12 schema: %v", err)
			}

			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			if want := "feature_set_digest: " + p.FeatureSetDigest(); doc["$comment"] != want {
				t.Errorf("$comment = %v, want %q", doc["$comment"], want)
			}
			defs := doc["$defs"].(map[string]any)
			refs := map[string]bool{}
			collectRefs(doc, refs)
			for ref := range refs {
				if _, ok := defs[strings.TrimPrefix(ref, "#/$defs/")]; !ok {
					t.Errorf("dangling $ref %q", ref)
				}
			}
			for def := range defs {
				if !refs["#/$defs/"+def] {
					t.Errorf("orphan def %q (reachable from no entry point)", def)
				}
			}

			inst := p.svc.InstanceSnapshot()
			text := string(raw)
			for _, h := range inst.HiddenNames() {
				if !strings.Contains(h, ":") && containsToken(text, h) {
					t.Errorf("schema names hidden feature %q", h)
				}
			}
			for capName, defNames := range roots {
				for _, d := range defNames {
					if _, ok := defs[d]; ok == inst.Hidden(capName) {
						t.Errorf("$defs.%s present=%v with %s hidden=%v", d, ok, capName, inst.Hidden(capName))
					}
				}
			}
			props := defs["Request"].(map[string]any)["properties"].(map[string]any)
			for _, k := range descx.HiddenSlotKeys(&types.Request{}, inst) {
				if _, ok := props[k]; ok {
					t.Errorf("Request.%s is a property on an instance hiding it", k)
				}
			}
			for _, k := range descx.VisibleSlotKeys(&types.Request{}, inst) {
				if _, ok := props[k]; !ok {
					t.Errorf("Request.%s missing on an instance offering it", k)
				}
			}
			for _, d := range []string{"Request", "Response", "Envelope"} {
				if _, ok := defs[d]; !ok {
					t.Errorf("$defs.%s missing", d)
				}
			}
		})
	}
}
