package descriptor_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/buildinfo"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

func TestInspectGolden(t *testing.T) {
	dict := goldenDictionary(t, "red", "green", "blue")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Age of the participant in years"},
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, Description: "Primary color category", Dictionary: dict},
		},
	}
	data := goldenPulseFile(t, schema)

	env := descx.Inspect(bytes.NewReader(data), nil)
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "inspect.json", out)
}

// TestInspectGroupedGolden pins the full grouped inspect envelope.
func TestInspectGroupedGolden(t *testing.T) {
	data := goldenGroupedCohort(t, 12, 3)
	out, err := json.MarshalIndent(descx.Inspect(bytes.NewReader(data), nil), "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}
	compareGolden(t, "inspect_grouped.json", out)
}

func TestRootManifestGolden(t *testing.T) {
	// pulse_version is a build fact; pin it so the golden is deterministic
	// regardless of ldflags, module version or VCS metadata.
	defer buildinfo.SetForTest("v0.0.0-test")()
	m := descx.BuildManifest()
	env := descriptor.NewEnvelope(m)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "manifest.json", data)
}

func TestPredictGolden_SimpleRequest(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Age of the participant in years"},
		},
	}
	data := goldenPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	env := descx.Predict(bytes.NewReader(data), req, nil)
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "predict_simple.json", out)
}

func TestPredictGolden_ComposedRequest(t *testing.T) {
	dict := goldenDictionary(t, "A", "B", "C")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: dict},
		},
	}
	data := goldenPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
			{Type: types.AGG_SUM, Field: "grade", Label: "sum_grade"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "grade"},
		},
	}

	env := descx.Predict(bytes.NewReader(data), req, nil)
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "predict_composed.json", out)
}

// TestPayloadSchemaGolden pins the generated payload contract. Any change
// to a payload struct field, a registry enum value, or a strict-union
// shape changes this output; regenerate with:
//
//	go test ./descriptor/ -run TestPayloadSchemaGolden -update
func TestPayloadSchemaGolden(t *testing.T) {
	// The root $comment carries the profile-free feature_set_digest,
	// whose reached-feature set depends on the build version: pin it.
	defer buildinfo.SetForTest("v0.0.0-test")()
	compareGolden(t, "payload-schema.json", descx.BuildPayloadSchema())
}

// The fixtures below duplicate the internal/descriptor test helpers the
// golden cases used before the descriptor split: the golden tests live in
// the public package (so `go test ./descriptor/ -run 'Test.*Golden'
// -update` keeps regenerating descriptor/testdata) as an external test
// package, which cannot reach the twin's unexported helpers.

func goldenPulseFile(t *testing.T, schema *encoding.Schema) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	return buf.Bytes()
}

func goldenDictionary(t *testing.T, values ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("Dictionary.Add(%q): %v", v, err)
		}
	}
	return d
}

// goldenGroupedCohort builds the synthetic 0x02 cohort inspect_grouped.json
// pins: a parent block (order_id key, region categorical, order_total)
// that changes every `fanout` rows in an indexed group keyed on order_id,
// a per-row qty, and a constant tenant column in a constant group.
func goldenGroupedCohort(t *testing.T, rows, fanout int) []byte {
	t.Helper()
	regions := []string{"north", "south", "east", "west"}
	flat := &encoding.Schema{Fields: []encoding.Field{
		{Name: "order_id", Type: encoding.FieldTypeU32, Description: "Synthetic parent key"},
		{Name: "region", ByteOffset: 4, Type: encoding.FieldTypeCategoricalU8, Description: "Synthetic parent region", Dictionary: goldenDictionary(t, regions...)},
		{Name: "order_total", ByteOffset: 5, Type: encoding.FieldTypeU64, Description: "Synthetic parent total"},
		{Name: "qty", ByteOffset: 13, Type: encoding.FieldTypeU16, Description: "Synthetic per-row quantity"},
		{Name: "tenant", ByteOffset: 15, Type: encoding.FieldTypeU8, Description: "Synthetic constant tenant"},
	}}
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, flat); err != nil {
		t.Fatalf("WritePreamble: %v", err)
	}
	for r := 0; r < rows; r++ {
		p := uint64(r / fanout)
		vals := []uint64{p, p % uint64(len(regions)), p * 100, uint64(r), 7}
		for i, f := range flat.Fields {
			if err := encoding.WriteFieldValue(&buf, f.Type, vals[i]); err != nil {
				t.Fatalf("WriteFieldValue(%s): %v", f.Name, err)
			}
		}
	}
	var out bytes.Buffer
	_, n, err := encx.DedupCohort(&out, bytes.NewReader(buf.Bytes()), []encx.GroupSpec{
		{Kind: encoding.GroupKindIndexed, Members: []string{"order_id", "region", "order_total"}, Key: []string{"order_id"}},
		{Kind: encoding.GroupKindConstant, Members: []string{"tenant"}},
	})
	if err != nil {
		t.Fatalf("DedupCohort: %v", err)
	}
	if n != int64(rows) {
		t.Fatalf("DedupCohort wrote %d rows, want %d", n, rows)
	}
	return out.Bytes()
}
