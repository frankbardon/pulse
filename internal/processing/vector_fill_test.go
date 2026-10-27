package processing

import (
	"bytes"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

func fillSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
			{Name: "emb", Type: encoding.FieldTypeVecF64, Nullable: true},
			{Name: "raw", Type: encoding.FieldTypeVecF32},
		},
		Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 3}, {FieldIndex: 2, Dim: 2}},
	}
}

// fillOf resolves fields over fillSchema into a memberFill.
func fillOf(t *testing.T, fields ...string) memberFill {
	t.Helper()
	r, err := vectors.ResolveSpec("v", types.VectorSpec{Fields: fields}, fillSchema())
	if err != nil {
		t.Fatal(err)
	}
	return newMemberFill(r)
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] && !(math.IsNaN(a[i]) && math.IsNaN(b[i])) {
			return false
		}
	}
	return true
}

// TestMemberFill_NullAndNaN: a scalar reads NumericValue (a stored NaN
// is a value); a native element reads the stored element, a NaN element
// is a null member and a null vector makes every element null.
func TestMemberFill_NullAndNaN(t *testing.T) {
	t.Parallel()

	s := fillSchema()
	nan := math.NaN()
	f := fillOf(t, "raw[2]", "x", "emb")
	cases := []struct {
		name    string
		rec     *Record
		want    []float64
		present []bool
	}{
		{"all present",
			NewRecordWithWide(s, map[string]float64{"x": 1.5}, nil, map[string]any{"emb": []float64{1, 2, 3}, "raw": []float64{8, 9}}),
			[]float64{9, 1.5, 1, 2, 3}, []bool{true, true, true, true, true}},
		{"NaN element is a null member",
			NewRecordWithWide(s, map[string]float64{"x": 1.5}, nil, map[string]any{"emb": []float64{1, nan, 3}, "raw": []float64{8, nan}}),
			[]float64{nan, 1.5, 1, nan, 3}, []bool{false, true, true, false, true}},
		{"null vector nulls every element",
			NewRecordWithWide(s, map[string]float64{"x": 2}, map[string]bool{"emb": true}, map[string]any{"raw": []float64{8, 9}}),
			[]float64{9, 2, nan, nan, nan}, []bool{true, true, false, false, false}},
		{"stored scalar NaN stays a value, null scalar a null member",
			NewRecordWithWide(s, map[string]float64{"x": nan}, nil, map[string]any{"emb": []float64{1, 2, 3}, "raw": []float64{8, 9}}),
			[]float64{9, nan, 1, 2, 3}, []bool{true, true, true, true, true}},
		{"null scalar",
			NewRecordWithWide(s, nil, map[string]bool{"x": true}, map[string]any{"emb": []float64{1, 2, 3}, "raw": []float64{8, 9}}),
			[]float64{9, nan, 1, 2, 3}, []bool{true, false, true, true, true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dst, pres := make([]float64, f.Len()), make([]bool, f.Len())
			n := f.fill(c.rec, dst, pres)
			if !sameFloats(dst, c.want) {
				t.Errorf("values = %v, want %v", dst, c.want)
			}
			wantN := 0
			for i, p := range c.present {
				if pres[i] != p {
					t.Errorf("present[%d] = %v, want %v", i, pres[i], p)
				}
				if p {
					wantN++
				}
			}
			if n != wantN {
				t.Errorf("present count = %d, want %d", n, wantN)
			}
			// The name-only reader agrees member by member.
			for i, m := range []string{"raw[2]", "x", "emb[1]", "emb[2]", "emb[3]"} {
				x, ok := c.rec.MemberValue(m)
				if ok != c.present[i] || (ok && !sameFloats([]float64{x}, []float64{c.want[i]})) {
					t.Errorf("MemberValue(%s) = %v, %v; fill says %v, %v", m, x, ok, c.want[i], c.present[i])
				}
			}
		})
	}
}

// TestAggregationPresent_NativeElements: the floor's presence question
// reads element members — a row is present when any element answered,
// absent when the vector is null or every element is NaN.
func TestAggregationPresent_NativeElements(t *testing.T) {
	t.Parallel()

	s := fillSchema()
	nan := math.NaN()
	agg := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"emb[1]", "emb[3]"}}
	for _, c := range []struct {
		name string
		rec  *Record
		want bool
	}{
		{"one element answered", NewRecordWithWide(s, nil, nil, map[string]any{"emb": []float64{nan, 5, 1}}), true},
		{"only an unread element answered", NewRecordWithWide(s, nil, nil, map[string]any{"emb": []float64{nan, 5, nan}}), false},
		{"null vector", NewRecordWithWide(s, nil, map[string]bool{"emb": true}, nil), false},
	} {
		if got := AggregationPresent(c.rec, agg); got != c.want {
			t.Errorf("%s: AggregationPresent = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMemberFill_DecodedRecordAllocatesNothing: the fill reads a
// reused decoded record's vector storage in place.
func TestMemberFill_DecodedRecordAllocatesNothing(t *testing.T) {
	// serial: testing.AllocsPerRun counts process-wide allocations, which concurrent tests inflate

	s := fillSchema()
	var buf bytes.Buffer
	const rows = 32
	for i := 0; i < rows; i++ {
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(float64(i)))
		_ = encoding.WriteVector(&buf, encoding.FieldTypeVecF64, 3, []float64{float64(i), 1, 2})
		_ = encoding.WriteVector(&buf, encoding.FieldTypeVecF32, 2, []float64{4, 5})
		buf.WriteByte(0)
	}
	rr := encx.NewRecordReader(bytes.NewReader(buf.Bytes()), s)
	rec := NewReusableRecord(s)
	if err := rr.ReadRecordReused(rec); err != nil {
		t.Fatal(err)
	}
	f := fillOf(t, "emb", "x", "raw")
	dst, pres := make([]float64, f.Len()), make([]bool, f.Len())
	allocs := testing.AllocsPerRun(10, func() { f.fill(rec, dst, pres) })
	if allocs != 0 {
		t.Errorf("fill allocates %v per row, want 0", allocs)
	}
	if want := []float64{0, 1, 2, 0, 4, 5}; !sameFloats(dst, want) {
		t.Errorf("first row = %v, want %v", dst, want)
	}
}

// TestMemberFillOf_LiteralMembers: a slot's literal member list (the
// rewrite a vector-input factory receives) compiles to the same reads
// as the resolved vector — element members against the schema, a
// non-canonical "f[k]" spelling never an element — and, with no schema,
// still reads element members by name (the MemberValue fallback).
func TestMemberFillOf_LiteralMembers(t *testing.T) {
	t.Parallel()

	s := fillSchema()
	nan := math.NaN()
	rec := NewRecordWithWide(s, map[string]float64{"x": 1.5}, nil,
		map[string]any{"emb": []float64{1, nan, 3}, "raw": []float64{8, 9}})
	names := []string{"raw[2]", "x", "emb[1]", "emb[2]", "emb[3]", "emb[01]", "emb[+1]", "emb[4]"}
	want := []float64{9, 1.5, 1, nan, 3, nan, nan, nan}
	for _, schema := range []*encoding.Schema{s, nil} {
		f := memberFillOf(names, schema)
		dst := make([]float64, f.Len())
		f.fill(rec, dst, nil)
		if !sameFloats(dst, want) {
			t.Errorf("schema=%v: values = %v, want %v", schema != nil, dst, want)
		}
	}
	if f := memberFillOf(names, s); f.refs[2] != (vectors.MemberRef{Field: "emb", Element: 0}) || f.refs[5].Element >= 0 {
		t.Errorf("compiled refs = %+v, want emb[1] an element, emb[01] a scalar name", f.refs)
	}
	// complete: the listwise question — any null member or NaN is a drop.
	full := memberFillOf([]string{"x", "emb[1]", "raw[1]"}, s)
	if !full.complete(rec, make([]float64, 3)) {
		t.Error("complete = false on a row with every member present")
	}
	if memberFillOf([]string{"x", "emb[2]"}, s).complete(rec, make([]float64, 2)) {
		t.Error("complete = true on a row with a NaN element")
	}
}

// TestMemberValue_AllocatesNothing: the name-only reader parses an
// element member without allocating, at any element index.
func TestMemberValue_AllocatesNothing(t *testing.T) {
	// serial: testing.AllocsPerRun counts process-wide allocations, which concurrent tests inflate

	s := &encoding.Schema{
		Fields:  []encoding.Field{{Name: "big", Type: encoding.FieldTypeVecF64}},
		Vectors: []encoding.VectorField{{FieldIndex: 0, Dim: 200}},
	}
	v := make([]float64, 200)
	for i := range v {
		v[i] = float64(i)
	}
	rec := NewRecordWithWide(s, nil, nil, map[string]any{"big": v})
	var x float64
	var ok bool
	if allocs := testing.AllocsPerRun(10, func() { x, ok = rec.MemberValue("big[150]") }); allocs != 0 {
		t.Errorf("MemberValue allocates %v, want 0", allocs)
	}
	if !ok || x != 149 {
		t.Errorf("MemberValue(big[150]) = %v, %v, want 149, true", x, ok)
	}
}
