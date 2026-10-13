package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/frankbardon/pulse/errors"
)

// Vector fields (vec_f32, vec_f64).
//
// A vector field stores a fixed-length numeric vector per row: dim
// elements, each a little-endian IEEE-754 float of the type's element
// width (VectorElemSize), packed contiguously. Its on-wire width is
// VectorElemSize × dim, so the dim is part of the schema, not the type
// byte: it lives in the VECTORS section of the 0x02 schema extension
// block (tag 2, always REQUIRED), together with the field's optional
// kind, scale range and element labels. Every vec field must be
// described there exactly once, which is why any cohort carrying a
// vector is written at format 0x02 (Schema.RequiredFormatVersion) and a
// binary that predates vectors refuses it loudly — at the unknown type
// byte, or at the unknown REQUIRED section.
//
// Null: a null vector is a zeroed full stride plus its null-bitmap bit;
// there is no in-band sentinel. A NaN element is a value the format
// carries verbatim (the engine reads it as a missing member).

// MinVectorDim and MaxVectorDim bound a vector field's dim, inclusive.
const (
	MinVectorDim = 2
	MaxVectorDim = 1024
)

// VectorKind is what a vector's values mean — the same vocabulary as a
// request's virtual-vector kind (measure, scale, composition, binary).
// VectorKindNone is "no kind declared"; it is the zero value and is
// never written as a kind byte (the has_kind flag is clear instead).
type VectorKind uint8

// The vector kinds. The numeric value IS the on-wire kind byte, so the
// order is frozen; append only.
const (
	VectorKindNone        VectorKind = 0
	VectorKindMeasure     VectorKind = 1
	VectorKindScale       VectorKind = 2
	VectorKindComposition VectorKind = 3
	VectorKindBinary      VectorKind = 4

	vectorKindCount VectorKind = 5 // sentinel: one past the last kind
)

// String returns the kind's name ("measure", "scale", "composition",
// "binary"), "" for VectorKindNone and "unknown(N)" for an unregistered
// byte.
func (k VectorKind) String() string {
	switch k {
	case VectorKindNone:
		return ""
	case VectorKindMeasure:
		return "measure"
	case VectorKindScale:
		return "scale"
	case VectorKindComposition:
		return "composition"
	case VectorKindBinary:
		return "binary"
	}
	return fmt.Sprintf("unknown(%d)", uint8(k))
}

// ParseVectorKind inverts VectorKind.String for the four declared
// kinds; ok is false for anything else (the empty string included).
func ParseVectorKind(name string) (VectorKind, bool) {
	for k := VectorKindMeasure; k < vectorKindCount; k++ {
		if k.String() == name {
			return k, true
		}
	}
	return VectorKindNone, false
}

// IsKnown reports whether k is VectorKindNone or a declared kind.
func (k VectorKind) IsKnown() bool { return k < vectorKindCount }

// VectorField is the schema metadata of one vec_f32 / vec_f64 field:
// its dim and its optional kind, scale range and element labels.
type VectorField struct {
	// FieldIndex is the field's LOGICAL position in Schema.Fields.
	FieldIndex int
	// Dim is the element count, MinVectorDim..MaxVectorDim.
	Dim int
	// Kind is what the values mean; VectorKindNone when undeclared.
	Kind VectorKind
	// ScaleMin and ScaleMax are the declared value range, meaningful
	// only when HasScale; ScaleMin < ScaleMax, both finite.
	ScaleMin float64
	ScaleMax float64
	HasScale bool
	// Labels names each element, in element order: nil (no labels) or
	// exactly Dim entries.
	Labels []string
}

// HasVectors reports whether the schema declares vector-field metadata
// (a VECTORS section). Every vec field must be covered, so a valid
// schema has vectors exactly when it has a vec field.
func (s *Schema) HasVectors() bool { return len(s.Vectors) > 0 }

// Vector returns the metadata of logical field i when it is a described
// vector field. The Labels slice is shared with the schema — do not
// mutate it.
func (s *Schema) Vector(i int) (VectorField, bool) {
	for k := range s.Vectors {
		if s.Vectors[k].FieldIndex == i {
			return s.Vectors[k], true
		}
	}
	return VectorField{}, false
}

// VectorDim returns logical field i's dim, or 0 when it is not a
// described vector field.
func (s *Schema) VectorDim(i int) int {
	v, ok := s.Vector(i)
	if !ok {
		return 0
	}
	return v.Dim
}

// ValidateVectors checks Schema.Vectors against Schema.Fields: each
// entry names an in-range vec field, no field twice, dim in
// MinVectorDim..MaxVectorDim, a known kind, a finite scale range with
// ScaleMin < ScaleMax when HasScale, nil or exactly Dim labels (each
// valid UTF-8, at most 65,535 bytes), and every vec field covered.
// Every fault is ENCODING_INVALID with details naming the field. Both
// the writer and the reader run it, so neither can produce or accept a
// vec field whose width is unknown.
func (s *Schema) ValidateVectors() error {
	covered := make(map[int]int, len(s.Vectors))
	for k := range s.Vectors {
		v := &s.Vectors[k]
		if v.FieldIndex < 0 || v.FieldIndex >= len(s.Fields) {
			return vectorErr("names no field", map[string]any{"field_index": v.FieldIndex, "field_count": len(s.Fields)})
		}
		f := &s.Fields[v.FieldIndex]
		if !f.Type.IsVector() {
			return vectorErr("describes a field that is not a vector type",
				map[string]any{"field_index": v.FieldIndex, "field": f.Name, "type": f.Type.String()})
		}
		if prev, dup := covered[v.FieldIndex]; dup {
			return vectorErr("field is described twice",
				map[string]any{"field_index": v.FieldIndex, "field": f.Name, "first_entry": prev, "entry": k})
		}
		covered[v.FieldIndex] = k
		if err := v.validate(f.Name); err != nil {
			return err
		}
	}
	for i := range s.Fields {
		if !s.Fields[i].Type.IsVector() {
			continue
		}
		if _, ok := covered[i]; !ok {
			return vectorErr("vector field has no VECTORS entry (its dim is unknown)",
				map[string]any{"field_index": i, "field": s.Fields[i].Name, "type": s.Fields[i].Type.String()})
		}
	}
	return nil
}

func (v *VectorField) validate(name string) error {
	at := func(extra map[string]any) map[string]any {
		extra["field_index"] = v.FieldIndex
		extra["field"] = name
		return extra
	}
	if v.Dim < MinVectorDim || v.Dim > MaxVectorDim {
		return vectorErr(fmt.Sprintf("dim must be %d..%d", MinVectorDim, MaxVectorDim),
			at(map[string]any{"dim": v.Dim, "min_dim": MinVectorDim, "max_dim": MaxVectorDim}))
	}
	if !v.Kind.IsKnown() {
		return vectorErr("unknown vector kind", at(map[string]any{"kind": uint8(v.Kind)}))
	}
	if v.HasScale {
		if math.IsNaN(v.ScaleMin) || math.IsInf(v.ScaleMin, 0) || math.IsNaN(v.ScaleMax) || math.IsInf(v.ScaleMax, 0) {
			return vectorErr("scale range must be finite", at(map[string]any{"scale_min": fmt.Sprint(v.ScaleMin), "scale_max": fmt.Sprint(v.ScaleMax)}))
		}
		if v.ScaleMin >= v.ScaleMax {
			return vectorErr("scale_min must be below scale_max", at(map[string]any{"scale_min": v.ScaleMin, "scale_max": v.ScaleMax}))
		}
	}
	if len(v.Labels) != 0 && len(v.Labels) != v.Dim {
		return vectorErr("label count must equal dim", at(map[string]any{"labels": len(v.Labels), "dim": v.Dim}))
	}
	for e, l := range v.Labels {
		if len(l) > math.MaxUint16 {
			return vectorErr("element label exceeds 65535 bytes", at(map[string]any{"element": e, "bytes": len(l)}))
		}
		if !utf8.ValidString(l) {
			return vectorErr("element label is not valid UTF-8", at(map[string]any{"element": e}))
		}
	}
	return nil
}

func vectorErr(msg string, details map[string]any) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID, "vector field: "+msg, details)
}

// The VECTORS section (tag 2, always REQUIRED) of the 0x02 schema
// extension block:
//
//	u16 vector_count                >= 1
//	per vector field, field_index strictly ascending:
//	  u16 field_index               LOGICAL position of a vec_f32 / vec_f64 field
//	  u16 dim                       MinVectorDim..MaxVectorDim
//	  u8  flags                     bit 0 labels, bit 1 has_kind,
//	                                bit 2 has_scale_range; bits 3-7 reserved, 0
//	  [u8 kind]                     iff has_kind: a VectorKind byte, 1..4
//	  [f64 scale_min, f64 scale_max] iff has_scale_range (IEEE-754 LE)
//	  [dim × (u16 length + UTF-8 label)] iff labels
const (
	sectionTagVectors uint16 = 2

	vectorFlagLabels   uint8 = 1 << 0
	vectorFlagKind     uint8 = 1 << 1
	vectorFlagScale    uint8 = 1 << 2
	vectorFlagsDefined       = vectorFlagLabels | vectorFlagKind | vectorFlagScale
)

// writeVectorsSection writes the VECTORS section body, entries sorted by
// field index so the bytes are a function of the metadata, not of the
// order a caller listed it in. s must have passed ValidateVectors.
func writeVectorsSection(b *bytes.Buffer, s *Schema) {
	le := binary.LittleEndian
	vs := slices.Clone(s.Vectors)
	slices.SortFunc(vs, func(a, c VectorField) int { return a.FieldIndex - c.FieldIndex })
	b.Write(le.AppendUint16(nil, uint16(len(vs))))
	for _, v := range vs {
		b.Write(le.AppendUint16(nil, uint16(v.FieldIndex)))
		b.Write(le.AppendUint16(nil, uint16(v.Dim)))
		var fl uint8
		if len(v.Labels) > 0 {
			fl |= vectorFlagLabels
		}
		if v.Kind != VectorKindNone {
			fl |= vectorFlagKind
		}
		if v.HasScale {
			fl |= vectorFlagScale
		}
		b.WriteByte(fl)
		if fl&vectorFlagKind != 0 {
			b.WriteByte(byte(v.Kind))
		}
		if fl&vectorFlagScale != 0 {
			b.Write(le.AppendUint64(nil, math.Float64bits(v.ScaleMin)))
			b.Write(le.AppendUint64(nil, math.Float64bits(v.ScaleMax)))
		}
		for _, l := range v.Labels {
			b.Write(le.AppendUint16(nil, uint16(len(l))))
			b.WriteString(l)
		}
	}
}

// readVectorsSection parses the VECTORS section into s.Vectors. Shape
// faults the wire itself can carry (reserved flag bits, an unknown or
// zero kind byte, an out-of-range dim, entries out of order) are refused
// here; coverage against s.Fields is ValidateVectors, which the caller
// runs once every section is read.
func readVectorsSection(x *extReader, s *Schema) error {
	n, err := x.u16("vector count")
	if err != nil {
		return err
	}
	if n == 0 {
		return vectorErr("VECTORS section declares no vector", nil)
	}
	vs := make([]VectorField, n)
	for k := range vs {
		fi, err := x.u16("vector field index")
		if err != nil {
			return err
		}
		dim, err := x.u16("vector dim")
		if err != nil {
			return err
		}
		fl, err := x.u8("vector flags")
		if err != nil {
			return err
		}
		v := &vs[k]
		v.FieldIndex, v.Dim = int(fi), int(dim)
		if k > 0 {
			switch prev := vs[k-1].FieldIndex; {
			case v.FieldIndex == prev:
				return vectorErr("field is described twice", map[string]any{"field_index": v.FieldIndex, "entry": k})
			case v.FieldIndex < prev:
				return vectorErr("entries are not in ascending field order", map[string]any{"field_index": v.FieldIndex, "previous": prev, "entry": k})
			}
		}
		if fl&^vectorFlagsDefined != 0 {
			return vectorErr("entry sets reserved flag bits", map[string]any{"field_index": v.FieldIndex, "flags": fl})
		}
		if v.Dim < MinVectorDim || v.Dim > MaxVectorDim {
			return vectorErr(fmt.Sprintf("dim must be %d..%d", MinVectorDim, MaxVectorDim),
				map[string]any{"field_index": v.FieldIndex, "dim": v.Dim, "min_dim": MinVectorDim, "max_dim": MaxVectorDim})
		}
		if fl&vectorFlagKind != 0 {
			kb, err := x.u8("vector kind")
			if err != nil {
				return err
			}
			v.Kind = VectorKind(kb)
			if v.Kind == VectorKindNone || !v.Kind.IsKnown() {
				return vectorErr("unknown vector kind", map[string]any{"field_index": v.FieldIndex, "kind": kb})
			}
		}
		if fl&vectorFlagScale != 0 {
			lo, err := x.u64("vector scale_min")
			if err != nil {
				return err
			}
			hi, err := x.u64("vector scale_max")
			if err != nil {
				return err
			}
			v.HasScale, v.ScaleMin, v.ScaleMax = true, math.Float64frombits(lo), math.Float64frombits(hi)
		}
		if fl&vectorFlagLabels != 0 {
			v.Labels = make([]string, v.Dim)
			for e := range v.Labels {
				ln, err := x.u16("vector label length")
				if err != nil {
					return err
				}
				lb, err := x.bytesN(uint64(ln), "vector label")
				if err != nil {
					return err
				}
				v.Labels[e] = string(lb)
			}
		}
	}
	s.Vectors = vs
	return nil
}

// vectorWidth returns a vector's on-wire stride, refusing a non-vector
// type and a dim outside MinVectorDim..MaxVectorDim.
func vectorWidth(ft FieldType, dim int) (int, error) {
	if !ft.IsVector() {
		return 0, errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("vector API requires vec_f32 or vec_f64, got %s", ft),
			map[string]any{"type": ft.String()})
	}
	if dim < MinVectorDim || dim > MaxVectorDim {
		return 0, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("vector dim must be %d..%d", MinVectorDim, MaxVectorDim),
			map[string]any{"type": ft.String(), "dim": dim})
	}
	return ft.VectorElemSize() * dim, nil
}

// PutVector writes v into dst as the on-wire payload of a vector field
// of type ft and the given dim: exactly VectorElemSize × dim bytes from
// dst[0], little-endian, nothing past them touched. A nil v writes the
// NULL placeholder — a zeroed full stride (the null itself rides the
// record's bitmap bit). A non-vector ft is ENCODING_TYPE_MISMATCH; a dim
// out of range, len(v) other than dim, a dst shorter than the stride, or
// a finite value a vec_f32 element cannot hold (it would silently become
// ±Inf) is ENCODING_INVALID, and dst is left untouched. NaN and ±Inf are
// stored verbatim.
func PutVector(dst []byte, ft FieldType, dim int, v []float64) error {
	width, err := vectorWidth(ft, dim)
	if err != nil {
		return err
	}
	if len(dst) < width {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("destination buffer too small for %s[%d]", ft, dim),
			map[string]any{"type": ft.String(), "dim": dim, "want_bytes": width, "got_bytes": len(dst)})
	}
	if v == nil {
		clear(dst[:width])
		return nil
	}
	if len(v) != dim {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("vector has %d elements, field dim is %d", len(v), dim),
			map[string]any{"type": ft.String(), "dim": dim, "elements": len(v)})
	}
	le := binary.LittleEndian
	if ft == FieldTypeVecF32 {
		for e, x := range v {
			if !math.IsInf(x, 0) && !math.IsNaN(x) && math.Abs(x) > math.MaxFloat32 {
				return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
					"vector element overflows vec_f32",
					map[string]any{"type": ft.String(), "element": e, "value": x})
			}
		}
		for e, x := range v {
			le.PutUint32(dst[e*4:], math.Float32bits(float32(x)))
		}
		return nil
	}
	for e, x := range v {
		le.PutUint64(dst[e*8:], math.Float64bits(x))
	}
	return nil
}

// VectorFromBytes decodes the on-wire payload of a vector field of type
// ft and the given dim from the front of src (anything past the stride
// is ignored), appending the dim elements to dst[:0] and returning it —
// pass a reused dst to decode without allocating. vec_f32 elements widen
// to float64 exactly. Errors are those of PutVector, plus a src shorter
// than the stride.
func VectorFromBytes(src []byte, ft FieldType, dim int, dst []float64) ([]float64, error) {
	width, err := vectorWidth(ft, dim)
	if err != nil {
		return nil, err
	}
	if len(src) < width {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("source buffer too small for %s[%d]", ft, dim),
			map[string]any{"type": ft.String(), "dim": dim, "want_bytes": width, "got_bytes": len(src)})
	}
	out := slices.Grow(dst[:0], dim)
	le := binary.LittleEndian
	if ft == FieldTypeVecF32 {
		for e := 0; e < dim; e++ {
			out = append(out, float64(math.Float32frombits(le.Uint32(src[e*4:]))))
		}
		return out, nil
	}
	for e := 0; e < dim; e++ {
		out = append(out, math.Float64frombits(le.Uint64(src[e*8:])))
	}
	return out, nil
}

// WriteVector writes a vector field's payload to the record stream — the
// streaming mirror of PutVector, and the API the uint64 WriteFieldValue
// points at when it refuses a vector type. The dim is explicit (it is
// schema metadata, Schema.VectorDim); a nil v writes the null
// placeholder.
func WriteVector(w io.Writer, ft FieldType, dim int, v []float64) error {
	width, err := vectorWidth(ft, dim)
	if err != nil {
		return err
	}
	buf := make([]byte, width)
	if err := PutVector(buf, ft, dim, v); err != nil {
		return err
	}
	if _, err := w.Write(buf); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing vector")
	}
	return nil
}

// ReadVector reads a vector field's payload from the record stream into
// dst[:0] (see VectorFromBytes). Null state rides the per-record null
// bitmap, so a null vector reads as its zeroed placeholder; the caller
// consults the bitmap.
func ReadVector(r io.Reader, ft FieldType, dim int, dst []float64) ([]float64, error) {
	width, err := vectorWidth(ft, dim)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, width)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "reading vector")
	}
	return VectorFromBytes(buf, ft, dim, dst)
}
