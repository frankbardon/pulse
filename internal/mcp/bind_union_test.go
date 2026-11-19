package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// unionFeatures is a profile with every root carrying the union slots,
// without capability:union.
var unionFeatures = []string{
	"capability:process", "capability:compose", "capability:process_chain", "capability:facet",
	"capability:sample", "capability:lookup", "AGG_SUM", "AGG_COUNT", "GROUP_CATEGORY",
}

func unionInstance(t *testing.T, with bool) (*pulse.Pulse, *descx.InstanceSnapshot) {
	t.Helper()
	return instanceFor(t, unionProfile(with), pulse.Extensions{})
}

func unionProfile(with bool) *pulse.FeatureProfile {
	features := append([]string(nil), unionFeatures...)
	if with {
		features = append(features, descx.FeatureUnion)
	}
	return &pulse.FeatureProfile{Features: features}
}

// unionSampleFS holds u.pulse: one u32 field, three records.
func unionSampleFS(t *testing.T) afero.Fs {
	t.Helper()
	sch := &encoding.Schema{Fields: []encoding.Field{{Name: "n", Type: encoding.FieldTypeU32}}}
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, sch); err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint64{1, 2, 3} {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, v); err != nil {
			t.Fatal(err)
		}
	}
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "u.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return afs
}

// TestBindForInstance_UnionSlots: every bound tool whose root reads a
// union (process, predict, compose slots, chain, facet_schema, sample)
// advertises `union` / `union_source_field` with capability:union, and
// no bound schema carries either key without it.
func TestBindForInstance_UnionSlots(t *testing.T) {
	t.Parallel()

	schema := makeBindSchema()
	_, on := unionInstance(t, true)
	_, off := unionInstance(t, false)
	enabled, err := BindForInstance(schema, on)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := BindForInstance(schema, off)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{toolmeta.ToolProcess, toolmeta.ToolPredict, toolmeta.ToolCompose,
		toolmeta.ToolProcessChain, toolmeta.ToolFacetSchema, toolmeta.ToolSample} {
		for _, key := range []string{`"union":`, `"union_source_field":`} {
			if !bytes.Contains(enabled[tool], []byte(key)) {
				t.Errorf("%s: union enabled, bound schema lacks %s", tool, key)
			}
		}
	}
	for tool, body := range hidden {
		if bytes.Contains(body, []byte(`"union":`)) || bytes.Contains(body, []byte(`"union_source_field":`)) {
			t.Errorf("%s: union hidden, bound schema still carries a union slot", tool)
		}
	}
}

// TestTools_SampleUnion: pulse_sample samples a union in place of path
// (rows in member order, each carrying its label in the source field),
// and an instance hiding capability:union refuses the key as an unknown
// argument whose valid_keys name no union slot.
func TestTools_SampleUnion(t *testing.T) {
	t.Parallel()

	afs := unionSampleFS(t)
	p, err := pulse.New(pulse.Options{FS: afs, FeatureProfile: unionProfile(true)})
	if err != nil {
		t.Fatal(err)
	}
	off, err := pulse.New(pulse.Options{FS: afs, FeatureProfile: unionProfile(false)})
	if err != nil {
		t.Fatal(err)
	}
	invoke := map[string]InvokeFunc{}
	for _, d := range Tools(Config{}) {
		invoke[d.Name] = d.Invoke
	}
	body := `{"union":[{"cohort":{"filename":"u.pulse"},"label":"real"},{"cohort":{"filename":"u.pulse"},"label":"twin"}],"union_source_field":"side","count":1000}`
	out, err := invoke[toolmeta.ToolSample](context.Background(), p, json.RawMessage(body))
	if err != nil {
		t.Fatalf("union sample: %v", err)
	}
	rows := out.(SampleOut).Rows
	if len(rows) < 2 || rows[0]["side"] != "real" || rows[len(rows)-1]["side"] != "twin" {
		t.Fatalf("rows = %v, want real rows then twin rows tagged by side", rows)
	}
	// A path beside a union is the cohort-beside-union refusal.
	_, err = invoke[toolmeta.ToolSample](context.Background(), p,
		json.RawMessage(`{"path":"u.pulse","union":[{"cohort":{"filename":"u.pulse"},"label":"a"}]}`))
	if ce := codedOf(t, err); ce.Code != perr.PULSE_UNION_COHORT_CONFLICT {
		t.Errorf("path + union: %s, want PULSE_UNION_COHORT_CONFLICT", ce.Code)
	}

	_, err = invoke[toolmeta.ToolSample](context.Background(), off, json.RawMessage(body))
	ce := codedOf(t, err)
	if ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("hidden: %s (%s), want PULSE_REQUEST_UNKNOWN_FIELD", ce.Code, ce.Message)
	}
	// Refused at decode, over the tool's own visible arguments.
	if valid, _ := ce.Details["valid_keys"].([]string); !slices.Equal(valid, []string{"count", "path"}) && !slices.Equal(valid, []string{"path", "count"}) {
		t.Errorf("hidden: valid_keys = %v, want pulse_sample's visible arguments [path count]", valid)
	}
	// The same request through the library is refused by the slot gate.
	_, lerr := off.SampleWithRequest(context.Background(), &pulse.SampleRequest{
		Union: []types.UnionMember{{Cohort: &types.Cohort{Filename: "u.pulse"}, Label: "a"}}, N: 1})
	if lce := codedOf(t, lerr); lce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Errorf("library: %s, want PULSE_REQUEST_UNKNOWN_FIELD", lce.Code)
	}
}
