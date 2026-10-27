package descriptor

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

const inspectVecLabels = 150

// vectorInspectFixture writes a 0x02 cohort of rows records: id (u32),
// emb (vec_f64, dim inspectVecLabels, kind scale, scale 1..5, one label
// per element) and plain (vec_f32, dim 2, no metadata, no description).
func vectorInspectFixture(t *testing.T, rows int) ([]byte, *encoding.Schema) {
	t.Helper()
	labels := make([]string, inspectVecLabels)
	for i := range labels {
		labels[i] = "item_" + strconv.Itoa(i+1)
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, Description: "Respondent identifier"},
			{Name: "emb", ByteOffset: 4, Type: encoding.FieldTypeVecF64, Description: "Battery of agreement items"},
			{Name: "plain", ByteOffset: 4 + 8*inspectVecLabels, Type: encoding.FieldTypeVecF32},
		},
		Vectors: []encoding.VectorField{
			{FieldIndex: 1, Dim: inspectVecLabels, Kind: encoding.VectorKindScale, HasScale: true, ScaleMin: 1, ScaleMax: 5, Labels: labels},
			{FieldIndex: 2, Dim: 2},
		},
	}
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, schema); err != nil {
		t.Fatalf("WritePreamble: %v", err)
	}
	emb := make([]float64, inspectVecLabels)
	for r := 0; r < rows; r++ {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, uint64(r)); err != nil {
			t.Fatal(err)
		}
		for k := range emb {
			emb[k] = float64(1 + (r+k)%5)
		}
		if err := encoding.WriteVector(&buf, encoding.FieldTypeVecF64, inspectVecLabels, emb); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteVector(&buf, encoding.FieldTypeVecF32, 2, []float64{float64(r), 0.5}); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes(), schema
}

func inspectVector(t *testing.T, data []byte, opts *descriptor.InspectOptions) *descriptor.InspectResult {
	t.Helper()
	env := inspectFromBytes(data, opts)
	if len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Fatalf("inspect errors %+v warnings %+v", env.Errors, env.Warnings)
	}
	return env.Data.(*descriptor.InspectResult)
}

// TestInspect_VectorFieldMetadata (FR-17): a vector field reports its
// dim, kind, scale range and element labels, the labels truncated
// exactly like a dictionary (DefaultDictionaryLimit, DictionaryLimit,
// FullDict); a vector without metadata reports its dim alone and a
// synthesized description naming it a vector; a scalar field carries no
// vector block.
func TestInspect_VectorFieldMetadata(t *testing.T) {
	t.Parallel()

	data, _ := vectorInspectFixture(t, 5)
	res := inspectVector(t, data, nil)
	if res.RecordCount != 5 {
		t.Errorf("record count %d, want 5", res.RecordCount)
	}
	id, emb, plain := res.Fields[0], res.Fields[1], res.Fields[2]
	if id.Vector != nil {
		t.Errorf("scalar field carries a vector block: %+v", id.Vector)
	}
	v := emb.Vector
	if v == nil || v.Dim != inspectVecLabels || v.Kind != "scale" || v.ScaleMin == nil || *v.ScaleMin != 1 || v.ScaleMax == nil || *v.ScaleMax != 5 {
		t.Fatalf("emb vector = %+v, want dim %d kind scale scale 1..5", v, inspectVecLabels)
	}
	if l := v.Labels; l == nil || !l.Truncated || l.TotalEntries != inspectVecLabels || len(l.Values) != DefaultDictionaryLimit || l.Values[0] != "item_1" {
		t.Errorf("default labels = %+v, want the first %d of %d, truncated", l, DefaultDictionaryLimit, inspectVecLabels)
	}
	if emb.Description != "Battery of agreement items" || emb.DescriptionSource != "schema" {
		t.Errorf("emb description %q (%s)", emb.Description, emb.DescriptionSource)
	}

	full := inspectVector(t, data, &descriptor.InspectOptions{FullDict: true}).Fields[1].Vector.Labels
	if full.Truncated || len(full.Values) != inspectVecLabels || full.Values[inspectVecLabels-1] != "item_150" {
		t.Errorf("FullDict labels = %d values, truncated %v", len(full.Values), full.Truncated)
	}
	ten := inspectVector(t, data, &descriptor.InspectOptions{DictionaryLimit: 10}).Fields[1].Vector.Labels
	if !ten.Truncated || len(ten.Values) != 10 {
		t.Errorf("DictionaryLimit 10 labels = %d values, truncated %v", len(ten.Values), ten.Truncated)
	}

	if p := plain.Vector; p == nil || p.Dim != 2 || p.Kind != "" || p.ScaleMin != nil || p.ScaleMax != nil || p.Labels != nil {
		t.Errorf("plain vector = %+v, want dim 2 and nothing else", p)
	}
	if plain.Description != "Vector field: plain (2 elements)" || plain.DescriptionSource != "synthesized" {
		t.Errorf("plain description %q (%s)", plain.Description, plain.DescriptionSource)
	}
	wire := mustMarshal(t, plain)
	for _, absent := range []string{`"kind"`, `"scale_min"`, `"scale_max"`, `"labels"`, `"dictionary"`} {
		if strings.Contains(wire, absent) {
			t.Errorf("plain field wire carries %s: %s", absent, wire)
		}
	}
	var idWire map[string]any
	if err := json.Unmarshal([]byte(mustMarshal(t, id)), &idWire); err != nil {
		t.Fatal(err)
	}
	if _, ok := idWire["vector"]; ok {
		t.Errorf("scalar field wire carries vector: %v", idWire)
	}
}

// TestInspect_VectorFieldReadsNoRecord: the vector block is schema
// metadata — garbling every record byte changes nothing.
func TestInspect_VectorFieldReadsNoRecord(t *testing.T) {
	t.Parallel()

	data, schema := vectorInspectFixture(t, 7)
	clean := mustMarshal(t, inspectFromBytes(data, nil))
	payload := 7 * schema.RecordByteSize()
	garbled := append([]byte(nil), data...)
	for i := len(garbled) - payload; i < len(garbled); i++ {
		garbled[i] = 0xFF
	}
	if got := mustMarshal(t, inspectFromBytes(garbled, nil)); got != clean {
		t.Fatalf("inspect read a record:\n clean:   %s\n garbled: %s", clean, got)
	}
}
