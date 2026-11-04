package pulse

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/afero"
)

// payload_schema_for_each_optimize_test.go pins the payload schema's
// `{"$var": "axis"}` marker in a fanned-out Compose optimize block
// (library-gaps E7-S3): the instance payload schema admits the marker
// exactly where the runtime keeps a block as written — any scalar of a
// block that carries a `for_each` — refuses it in a block without one,
// and drops it with the for_each on an instance hiding
// capability:for_each.

// composeSchemaValidator compiles p's instance payload schema and
// returns a validator for its ComposedRequest def.
func composeSchemaValidator(t *testing.T, p *Pulse) func(string) error {
	t.Helper()
	raw, err := p.PayloadSchema()
	if err != nil {
		t.Fatalf("PayloadSchema: %v", err)
	}
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	const id = "https://frankbardon.github.io/pulse/payload-schema.json"
	if err := c.AddResource(id, doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	sch, err := c.Compile(id + "#/$defs/ComposedRequest")
	if err != nil {
		t.Fatalf("compile ComposedRequest: %v", err)
	}
	return func(body string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		return sch.Validate(inst)
	}
}

// TestPayloadSchema_ForEachOptimizeMarkers: the E7-S2 marker-form
// request (a bound, a constraint expr) and its hand-written twin both
// validate; every other scalar kind (starts, objective, the whole
// name) takes a marker too; a block without for_each carrying one is
// rejected; and an instance hiding capability:for_each binds neither
// the for_each nor the marker.
func TestPayloadSchema_ForEachOptimizeMarkers(t *testing.T) {
	t.Parallel()

	validate := composeSchemaValidator(t, optPulse(t, Options{}))
	if err := validate(forEachOptBody); err != nil {
		t.Fatalf("the marker-form request was rejected: %v", err)
	}
	if err := validate(forEachOptTwin); err != nil {
		t.Fatalf("the hand-written twin was rejected: %v", err)
	}
	more := strings.Replace(forEachOptBody, `"starts": 4`, `"starts": {"$var": "cap"}, "objective_x": 1`, 1)
	if validate(more) == nil {
		t.Fatal("an unknown key in a fanned-out block validated")
	}
	more = strings.Replace(forEachOptBody, `"starts": 4`, `"starts": {"$var": "cap"}, "rng_seed": {"$var": "cap"}, "tolerance": {"$var": "cap"}`, 1)
	if err := validate(more); err != nil {
		t.Fatalf("a marker in starts / rng_seed / tolerance was rejected: %v", err)
	}
	if validate(strings.Replace(forEachOptBody, `{"$var": "cap"}`, `{"$var": "cap", "x": 1}`, 1)) == nil {
		t.Fatal("a marker object with an extra key validated")
	}

	// No for_each: the marker is the decode error the runtime raises.
	noFE := forEachOptBody[:strings.Index(forEachOptBody, `,
    "for_each"`)] + "\n  }]\n}"
	var c ComposedRequest
	if json.Unmarshal([]byte(noFE), &c) == nil {
		t.Fatal("fixture: the runtime decoded a marker block without for_each")
	}
	if validate(noFE) == nil {
		t.Fatal("a marker in a block without for_each validated")
	}
	scalarOnly := strings.Replace(strings.Replace(noFE, `{"$var": "cap"}`, `60`, 1), `{"$var": "con"}`, `{"$var": "x"}`, 1)
	if validate(scalarOnly) == nil {
		t.Fatal("a constraint-expr marker in a block without for_each validated")
	}

	hidden, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{
		Features: []string{"capability:compose", "capability:compose_optimize", "capability:params", "AGG_COUNT"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hidden.PayloadSchema()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"$var"`)) {
		t.Fatal("an instance hiding capability:for_each still declares the $var marker")
	}
	if composeSchemaValidator(t, hidden)(forEachOptBody) == nil {
		t.Fatal("a marker block validated on an instance that hides for_each")
	}
}
