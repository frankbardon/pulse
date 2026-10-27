package encoding

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestFieldTypeVector_Predicates(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		ft   FieldType
		b    byte
		name string
		elem int
	}{
		{FieldTypeVecF32, 20, "vec_f32", 4},
		{FieldTypeVecF64, 21, "vec_f64", 8},
	} {
		if byte(c.ft) != c.b {
			t.Errorf("%s type byte = %d, want %d", c.name, byte(c.ft), c.b)
		}
		if c.ft.String() != c.name {
			t.Errorf("String() = %q, want %q", c.ft.String(), c.name)
		}
		if got, ok := ParseFieldType(c.name); !ok || got != c.ft {
			t.Errorf("ParseFieldType(%q) = %v, %v", c.name, got, ok)
		}
		if !c.ft.IsKnown() || !c.ft.IsVector() {
			t.Errorf("%s: IsKnown/IsVector false", c.name)
		}
		if c.ft.IsNumeric() || c.ft.IsNumericForAnalytics() {
			t.Errorf("%s: a vector is not a scalar number", c.name)
		}
		if c.ft.IsBitPacked() || c.ft.HasDictionary() || c.ft.IsSet() || c.ft.IsDecimal() {
			t.Errorf("%s: unexpected scalar-family predicate", c.name)
		}
		if c.ft.VectorElemSize() != c.elem {
			t.Errorf("%s VectorElemSize = %d, want %d", c.name, c.ft.VectorElemSize(), c.elem)
		}
		if c.ft.ByteSize() != 0 {
			t.Errorf("%s ByteSize = %d, want 0 (width is per field)", c.name, c.ft.ByteSize())
		}
	}
	for ft := FieldType(0); ft < fieldTypeCount; ft++ {
		if ft.IsVector() != (ft == FieldTypeVecF32 || ft == FieldTypeVecF64) {
			t.Errorf("IsVector(%s) wrong", ft)
		}
		if !ft.IsVector() && ft.VectorElemSize() != 0 {
			t.Errorf("VectorElemSize(%s) = %d, want 0", ft, ft.VectorElemSize())
		}
	}
}

func TestVectorKind_Names(t *testing.T) {
	t.Parallel()

	want := map[VectorKind]string{VectorKindMeasure: "measure", VectorKindScale: "scale", VectorKindComposition: "composition", VectorKindBinary: "binary"}
	for k, n := range want {
		if k.String() != n {
			t.Errorf("%d.String() = %q, want %q", k, k.String(), n)
		}
		if got, ok := ParseVectorKind(n); !ok || got != k {
			t.Errorf("ParseVectorKind(%q) = %v, %v", n, got, ok)
		}
	}
	if _, ok := ParseVectorKind(""); ok {
		t.Error(`ParseVectorKind("") accepted`)
	}
	if VectorKindNone.String() != "" || VectorKind(9).IsKnown() || !VectorKindNone.IsKnown() {
		t.Error("VectorKindNone / unknown kind predicates wrong")
	}
}

// vecSchema is a mixed schema: scalars around an f32 and an f64 vector,
// the f64 one nullable and fully described.
func vecSchema() *Schema {
	s := &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU16},
		{Name: "emb", Type: FieldTypeVecF32},
		{Name: "flag", Type: FieldTypePackedBool},
		{Name: "rating", Type: FieldTypeVecF64, Nullable: true, Description: "brand rating"},
		{Name: "tail", Type: FieldTypeF64},
	}, Vectors: []VectorField{
		{FieldIndex: 3, Dim: 3, Kind: VectorKindScale, HasScale: true, ScaleMin: 1, ScaleMax: 7, Labels: []string{"a", "b", "ç"}},
		{FieldIndex: 1, Dim: 4},
	}}
	return s
}

func TestSchema_VectorWidthAndVersion(t *testing.T) {
	t.Parallel()

	s := vecSchema()
	wantW := []int{2, 16, 1, 24, 8}
	for i, w := range wantW {
		if got := s.FieldWidth(i); got != w {
			t.Errorf("FieldWidth(%d) = %d, want %d", i, got, w)
		}
	}
	// 51 bytes of fields + ceil(5/8) = 1 bitmap byte.
	if got := s.RecordByteSize(); got != 52 {
		t.Errorf("RecordByteSize = %d, want 52", got)
	}
	if !s.HasVectors() || s.RequiredFormatVersion() != FormatVersionV2 {
		t.Errorf("HasVectors/RequiredFormatVersion = %v/0x%02x, want true/0x02", s.HasVectors(), s.RequiredFormatVersion())
	}
	if v, ok := s.Vector(3); !ok || v.Dim != 3 || v.Kind != VectorKindScale {
		t.Errorf("Vector(3) = %+v, %v", v, ok)
	}
	if _, ok := s.Vector(0); ok || s.VectorDim(0) != 0 {
		t.Error("Vector(0) on a scalar field reported a vector")
	}
	// The logical view of a grouped schema keeps the dims.
	g := vecSchema()
	g.Groups = []Group{{Kind: GroupKindConstant, Members: []GroupMember{{Field: 1}}, Entries: make([]byte, 16)}}
	if got := g.Logical().FieldWidth(1); got != 16 {
		t.Errorf("Logical().FieldWidth(vec) = %d, want 16", got)
	}
	if got := g.GroupEntryWidth(0); got != 16 {
		t.Errorf("GroupEntryWidth over a vec member = %d, want 16", got)
	}
}

func TestSchema_VectorsRoundTrip(t *testing.T) {
	t.Parallel()

	s := vecSchema()
	var b bytes.Buffer
	if err := writeSchemaVersion(&b, s, FormatVersionV2); err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(b.Bytes())
	got, err := ReadSchema(r, FormatVersionV2)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if r.Len() != 0 {
		t.Fatalf("%d schema bytes left unread", r.Len())
	}
	// The wire is canonical: entries come back in field order.
	want := []VectorField{s.Vectors[1], s.Vectors[0]}
	if !reflect.DeepEqual(got.Vectors, want) {
		t.Fatalf("Vectors = %+v, want %+v", got.Vectors, want)
	}
	if got.RecordByteSize() != s.RecordByteSize() {
		t.Fatalf("stride %d after round trip, want %d", got.RecordByteSize(), s.RecordByteSize())
	}
	// Listing order never changes the bytes.
	s2 := vecSchema()
	s2.Vectors[0], s2.Vectors[1] = s2.Vectors[1], s2.Vectors[0]
	var b2 bytes.Buffer
	if err := writeSchemaVersion(&b2, s2, FormatVersionV2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), b2.Bytes()) {
		t.Fatal("VECTORS bytes depend on Schema.Vectors listing order")
	}
}

// TestVectorsSection_Wire pins the section layout byte-for-byte for one
// entry carrying every optional part.
func TestVectorsSection_Wire(t *testing.T) {
	t.Parallel()

	s := &Schema{Fields: []Field{{Name: "v", Type: FieldTypeVecF64}}, Vectors: []VectorField{
		{FieldIndex: 0, Dim: 2, Kind: VectorKindComposition, HasScale: true, ScaleMin: 0, ScaleMax: 1, Labels: []string{"x", "yz"}},
	}}
	var b bytes.Buffer
	writeVectorsSection(&b, s)
	le := binary.LittleEndian
	want := []byte{1, 0, 0, 0, 2, 0, 0b111, 3}
	want = le.AppendUint64(want, math.Float64bits(0))
	want = le.AppendUint64(want, math.Float64bits(1))
	want = append(want, 1, 0, 'x', 2, 0, 'y', 'z')
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("section = %v, want %v", b.Bytes(), want)
	}
	// No optional part → flags 0, nothing after dim.
	s.Vectors[0] = VectorField{FieldIndex: 0, Dim: 2}
	b.Reset()
	writeVectorsSection(&b, s)
	if !bytes.Equal(b.Bytes(), []byte{1, 0, 0, 0, 2, 0, 0}) {
		t.Fatalf("bare section = %v", b.Bytes())
	}
}

func wantInvalid(t *testing.T, err error, msgPart string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("err = %v, want ENCODING_INVALID", err)
	}
	if !strings.Contains(ce.Message, msgPart) {
		t.Fatalf("message %q does not mention %q", ce.Message, msgPart)
	}
	if len(ce.Details) == 0 && msgPart != "declares no vector" {
		t.Fatalf("%q carries no details", ce.Message)
	}
}

// TestValidateVectors_Refusals: every in-memory fault is refused by the
// writer before a byte is written.
func TestValidateVectors_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mut  func(s *Schema)
		msg  string
	}{
		{"dim below 2", func(s *Schema) { s.Vectors[1].Dim = 1 }, "dim must be"},
		{"dim above 1024", func(s *Schema) { s.Vectors[1].Dim = 1025 }, "dim must be"},
		{"field index out of range", func(s *Schema) { s.Vectors[1].FieldIndex = 9 }, "names no field"},
		{"negative field index", func(s *Schema) { s.Vectors[1].FieldIndex = -1 }, "names no field"},
		{"not a vec field", func(s *Schema) { s.Vectors[1].FieldIndex = 0 }, "not a vector type"},
		{"covered twice", func(s *Schema) { s.Vectors[1].FieldIndex = 3 }, "described twice"},
		{"uncovered", func(s *Schema) { s.Vectors = s.Vectors[:1] }, "no VECTORS entry"},
		{"no vectors at all", func(s *Schema) { s.Vectors = nil }, "no VECTORS entry"},
		{"unknown kind", func(s *Schema) { s.Vectors[0].Kind = 7 }, "unknown vector kind"},
		{"label count", func(s *Schema) { s.Vectors[0].Labels = []string{"a", "b"} }, "label count"},
		{"scale min == max", func(s *Schema) { s.Vectors[0].ScaleMin = 7 }, "scale_min must be below"},
		{"scale min > max", func(s *Schema) { s.Vectors[0].ScaleMin = 8 }, "scale_min must be below"},
		{"scale NaN", func(s *Schema) { s.Vectors[0].ScaleMax = math.NaN() }, "finite"},
		{"label not utf8", func(s *Schema) { s.Vectors[0].Labels[1] = "\xff" }, "UTF-8"},
		{"label too long", func(s *Schema) { s.Vectors[0].Labels[1] = strings.Repeat("x", 70000) }, "65535"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := vecSchema()
			c.mut(s)
			for _, v := range []byte{FormatVersionV1, FormatVersionV2} {
				var b bytes.Buffer
				err := writeSchemaVersion(&b, s, v)
				wantInvalid(t, err, c.msg)
				if b.Len() != 0 {
					t.Fatalf("version 0x%02x: %d bytes written before the refusal", v, b.Len())
				}
			}
		})
	}
	if err := vecSchema().ValidateVectors(); err != nil {
		t.Fatalf("valid schema refused: %v", err)
	}
}

// vecSchemaBlock assembles a 0x02 schema block over s's descriptors with
// one section carrying body under tag.
func vecSchemaBlock(t *testing.T, s *Schema, tag uint16, flags uint8, body []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writeFieldDescriptors(&b, s); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	payload := le.AppendUint16(nil, 1)
	payload = le.AppendUint16(payload, tag)
	payload = append(payload, flags)
	payload = le.AppendUint64(payload, uint64(len(body)))
	payload = append(payload, body...)
	b.Write(le.AppendUint64(nil, uint64(len(payload))))
	b.Write(payload)
	return b.Bytes()
}

// TestVectorsSection_ReadRefusals: every fault a hand-made VECTORS
// section can carry is ENCODING_INVALID at read.
func TestVectorsSection_ReadRefusals(t *testing.T) {
	t.Parallel()

	two := &Schema{Fields: []Field{
		{Name: "a", Type: FieldTypeVecF64},
		{Name: "b", Type: FieldTypeVecF32},
		{Name: "c", Type: FieldTypeU8},
	}}
	entry := func(fi, dim uint16, fl uint8, rest ...byte) []byte {
		e := binary.LittleEndian.AppendUint16(nil, fi)
		e = binary.LittleEndian.AppendUint16(e, dim)
		return append(append(e, fl), rest...)
	}
	sec := func(entries ...[]byte) []byte {
		out := binary.LittleEndian.AppendUint16(nil, uint16(len(entries)))
		for _, e := range entries {
			out = append(out, e...)
		}
		return out
	}
	ok0, ok1 := entry(0, 2, 0), entry(1, 3, 0)
	scale := func(lo, hi float64) []byte {
		b := binary.LittleEndian.AppendUint64(nil, math.Float64bits(lo))
		return binary.LittleEndian.AppendUint64(b, math.Float64bits(hi))
	}
	cases := []struct {
		name string
		body []byte
		msg  string
	}{
		{"zero entries", sec(), "declares no vector"},
		{"dim 1", sec(entry(0, 1, 0), ok1), "dim must be"},
		{"dim 1025", sec(entry(0, 1025, 0), ok1), "dim must be"},
		{"duplicate index", sec(ok0, entry(0, 2, 0)), "described twice"},
		{"descending", sec(ok1, ok0), "ascending"},
		{"out of range index", sec(ok0, ok1, entry(7, 2, 0)), "names no field"},
		{"index not a vec field", sec(ok0, ok1, entry(2, 2, 0)), "not a vector type"},
		{"vec field uncovered", sec(ok0), "no VECTORS entry"},
		{"reserved flag bit", sec(entry(0, 2, 0b1000), ok1), "reserved flag bits"},
		{"kind byte 0", sec(entry(0, 2, vectorFlagKind, 0), ok1), "unknown vector kind"},
		{"kind byte 5", sec(entry(0, 2, vectorFlagKind, 5), ok1), "unknown vector kind"},
		{"scale min == max", sec(entry(0, 2, vectorFlagScale, scale(3, 3)...), ok1), "scale_min must be below"},
		{"scale inf", sec(entry(0, 2, vectorFlagScale, scale(math.Inf(-1), 3)...), ok1), "finite"},
		{"label count short", sec(entry(0, 2, vectorFlagLabels, 1, 0, 'x'), ok1), "vector label length"},
		{"truncated entry", sec(entry(0, 2, vectorFlagKind)), "vector kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block := vecSchemaBlock(t, two, sectionTagVectors, sectionFlagRequired, c.body)
			_, err := ReadSchema(bytes.NewReader(block), FormatVersionV2)
			if c.msg == "vector label length" || c.msg == "vector kind" {
				if !errors.HasCode(err, errors.ENCODING_INVALID) {
					t.Fatalf("err = %v, want ENCODING_INVALID", err)
				}
				return
			}
			wantInvalid(t, err, c.msg)
		})
	}
	// The happy path over the same helper reads.
	good := sec(entry(0, 2, vectorFlagLabels|vectorFlagKind, append([]byte{1}, 1, 0, 'x', 1, 0, 'y')...), ok1)
	s, err := ReadSchema(bytes.NewReader(vecSchemaBlock(t, two, sectionTagVectors, sectionFlagRequired, good)), FormatVersionV2)
	if err != nil {
		t.Fatalf("valid hand-made section refused: %v", err)
	}
	if v, _ := s.Vector(0); v.Kind != VectorKindMeasure || !reflect.DeepEqual(v.Labels, []string{"x", "y"}) {
		t.Fatalf("Vector(0) = %+v", v)
	}
}

// TestReadSchema_V1VecFieldRefused: a 0x01 block has no VECTORS
// section, so a vec field in it has no width and is refused.
func TestReadSchema_V1VecFieldRefused(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	s := &Schema{Fields: []Field{{Name: "v", Type: FieldTypeVecF64}}}
	if err := writeFieldDescriptors(&b, s); err != nil {
		t.Fatal(err)
	}
	_, err := ReadSchema(bytes.NewReader(b.Bytes()), FormatVersionV1)
	wantInvalid(t, err, "no VECTORS entry")
}

func TestVectorValueAPI_RoundTrip(t *testing.T) {
	t.Parallel()

	v := []float64{1.5, math.NaN(), -0.25, math.Inf(1)}
	for _, ft := range []FieldType{FieldTypeVecF32, FieldTypeVecF64} {
		var b bytes.Buffer
		if err := WriteVector(&b, ft, 4, v); err != nil {
			t.Fatalf("%s WriteVector: %v", ft, err)
		}
		if b.Len() != 4*ft.VectorElemSize() {
			t.Fatalf("%s wrote %d bytes, want %d", ft, b.Len(), 4*ft.VectorElemSize())
		}
		dst := make([]float64, 0, 8)
		got, err := ReadVector(bytes.NewReader(b.Bytes()), ft, 4, dst)
		if err != nil {
			t.Fatalf("%s ReadVector: %v", ft, err)
		}
		if &got[:1][0] != &dst[:1][0] {
			t.Errorf("%s ReadVector did not reuse dst", ft)
		}
		if got[0] != 1.5 || !math.IsNaN(got[1]) || got[2] != -0.25 || !math.IsInf(got[3], 1) {
			t.Errorf("%s round trip = %v", ft, got)
		}
	}
	// Hard-coded little-endian bytes: vec_f32 element 1.5 = 0x3FC00000.
	buf := make([]byte, 8)
	if err := PutVector(buf, FieldTypeVecF32, 2, []float64{1.5, 0}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, []byte{0, 0, 0xC0, 0x3F, 0, 0, 0, 0}) {
		t.Fatalf("vec_f32 bytes = %v", buf)
	}
	// vec_f32 widens exactly.
	got, _ := VectorFromBytes([]byte{0xCD, 0xCC, 0xCC, 0x3D, 0, 0, 0x80, 0x3F}, FieldTypeVecF32, 2, nil)
	if got[0] != float64(float32(0.1)) || got[1] != 1 {
		t.Fatalf("widened = %v", got)
	}
}

// TestVectorValueAPI_NullPlaceholder: a nil vector writes a zeroed full
// stride (the null rides the bitmap), overwriting whatever was there.
func TestVectorValueAPI_NullPlaceholder(t *testing.T) {
	t.Parallel()

	buf := bytes.Repeat([]byte{0xAB}, 3*8+1)
	if err := PutVector(buf, FieldTypeVecF64, 3, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:24], make([]byte, 24)) || buf[24] != 0xAB {
		t.Fatalf("null placeholder = %v", buf)
	}
	var b bytes.Buffer
	if err := WriteVector(&b, FieldTypeVecF32, 2, nil); err != nil || !bytes.Equal(b.Bytes(), make([]byte, 8)) {
		t.Fatalf("WriteVector(nil) = %v, %v", b.Bytes(), err)
	}
}

func TestVectorValueAPI_Refusals(t *testing.T) {
	t.Parallel()

	mismatch := func(err error) bool { return errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) }
	invalid := func(err error) bool { return errors.HasCode(err, errors.ENCODING_INVALID) }
	buf := make([]byte, 64)
	cases := []struct {
		name string
		err  error
		ok   func(error) bool
	}{
		{"put non-vector", PutVector(buf, FieldTypeF64, 2, []float64{1, 2}), mismatch},
		{"put dim 1", PutVector(buf, FieldTypeVecF64, 1, []float64{1}), invalid},
		{"put len != dim", PutVector(buf, FieldTypeVecF64, 3, []float64{1, 2}), invalid},
		{"put short dst", PutVector(buf[:7], FieldTypeVecF32, 2, []float64{1, 2}), invalid},
		{"put f32 overflow", PutVector(buf, FieldTypeVecF32, 2, []float64{1, 1e300}), invalid},
		{"from short src", func() error { _, err := VectorFromBytes(buf[:15], FieldTypeVecF64, 2, nil); return err }(), invalid},
		{"read non-vector", func() error { _, err := ReadVector(bytes.NewReader(buf), FieldTypeSetU256, 2, nil); return err }(), mismatch},
		{"read short", func() error { _, err := ReadVector(bytes.NewReader(buf[:3]), FieldTypeVecF32, 2, nil); return err }(), func(err error) bool { return errors.HasCode(err, errors.ENCODING_IO) }},
	}
	for _, c := range cases {
		if !c.ok(c.err) {
			t.Errorf("%s: err = %v", c.name, c.err)
		}
	}
	// PutVector leaves dst untouched on refusal.
	dst := bytes.Repeat([]byte{7}, 8)
	_ = PutVector(dst, FieldTypeVecF32, 2, []float64{1, 1e300})
	if !bytes.Equal(dst, bytes.Repeat([]byte{7}, 8)) {
		t.Fatal("refused PutVector touched dst")
	}
}

// TestFieldValue_VectorRejected: the uint64 scalar API refuses both
// vector types through explicit arms that point at the vector API.
func TestFieldValue_VectorRejected(t *testing.T) {
	t.Parallel()

	for _, ft := range []FieldType{FieldTypeVecF32, FieldTypeVecF64} {
		var b bytes.Buffer
		werr := WriteFieldValue(&b, ft, 1)
		_, rerr := ReadFieldValue(bytes.NewReader(make([]byte, 64)), ft)
		for _, err := range []error{werr, rerr} {
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_TYPE_MISMATCH || !strings.Contains(ce.Message, "vector API") {
				t.Errorf("%s: err = %v, want ENCODING_TYPE_MISMATCH naming the vector API", ft, err)
			}
		}
		if b.Len() != 0 {
			t.Errorf("%s: WriteFieldValue wrote %d bytes", ft, b.Len())
		}
	}
}
