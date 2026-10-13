package vectors

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Native vector fields (vec_f32 / vec_f64, U29).
//
// A native vector field is a vector the cohort itself stores: its dim,
// kind, scale range and element labels are schema metadata (the
// VECTORS section, encoding.VectorField). The resolver makes it
// indistinguishable from a virtual vector downstream:
//
//   - Named directly. Every vector-name slot (a matrix spec's `vector`,
//     a test's `vector` / `vector2`, an attribute's, a grouper's or an
//     aggregation's `vector`) and every expression identifier accepts a
//     native field's name with no Request.Vectors entry: Resolve
//     appends one Resolved per native field AFTER the spec-backed ones
//     (spec order index alignment is kept), with Native set and kind,
//     scale range and labels taken from the schema.
//   - Never shadowed. A Request.Vectors entry named like a native field
//     is PULSE_VECTOR_NAME_COLLISION (details.native = true).
//   - Expanded inside a virtual vector. A `fields` entry, glob or
//     pattern match naming a native field expands IN PLACE to its
//     elements, in element order; this is how a request re-kinds or
//     re-scopes a native vector (the spec's own kind, scale and labels
//     apply; the schema's metadata is never mutated). An element
//     carries its schema label when the spec sets no labels.
//   - Element members. Element e (0-based) of native field f is the
//     member ElementMember(f, e) = "f[e+1]" — 1-based, the identity a
//     member list, a duplicate check and the predict echo use. A
//     `fields` entry spelled that way names that one element (a
//     re-scope), and a literal-member rewrite of a resolved vector
//     re-resolves to the same elements. The spelling is an element
//     reference only when f is a native vector field of the schema and
//     the index is canonical base-10 in 1..dim; otherwise the entry
//     keeps its glob meaning.
//   - Default element label. An element's label is its schema label,
//     else "f_<e+1>" — the column name an expanded export writes.
//   - Values. A NaN element is a null member; a null vector is every
//     member null (internal/processing's shared fill reads both).

// NativeVector marks a Resolved that IS one native vector field.
type NativeVector struct {
	// Field is the vec field's name (the vector's Name).
	Field string
	// FieldIndex is the field's LOGICAL position in Schema.Fields.
	FieldIndex int
	// Dim is the element count.
	Dim int
	// Type is vec_f32 or vec_f64.
	Type encoding.FieldType
	// HasLabels reports element labels declared in the schema (Labels
	// are then those, not the "f_<i>" defaults).
	HasLabels bool
}

// MemberRef locates one member's value on a record: scalar column Field
// (Element < 0), or element Element (0-based) of native vector field
// Field.
type MemberRef struct {
	Field   string
	Element int
}

// Native reports whether the member is an element of a native vector.
func (m MemberRef) Native() bool { return m.Element >= 0 }

// ElementMember is the member name of element e (0-based) of native
// vector field field: "field[e+1]".
func ElementMember(field string, e int) string {
	return field + "[" + strconv.Itoa(e+1) + "]"
}

// ElementLabel is the default display label of element e (0-based) of
// native vector field field when the schema declares none: "field_<e+1>".
func ElementLabel(field string, e int) string {
	return field + "_" + strconv.Itoa(e+1)
}

// ParseElementMember parses an element member name ("f[k]", k
// canonical base-10 in 1..dim of native vector field f of schema) into
// its field and 0-based element. ok is false for anything else.
func ParseElementMember(name string, schema *encoding.Schema) (field string, element int, ok bool) {
	if schema == nil || !schema.HasVectors() || !strings.HasSuffix(name, "]") {
		return "", 0, false
	}
	open := strings.LastIndexByte(name, '[')
	if open <= 0 {
		return "", 0, false
	}
	field, digits := name[:open], name[open+1:len(name)-1]
	k, err := strconv.Atoi(digits)
	if err != nil || k < 1 || strconv.Itoa(k) != digits {
		return "", 0, false
	}
	idx := fieldIndex(schema, field)
	if idx < 0 || !schema.Fields[idx].Type.IsVector() {
		return "", 0, false
	}
	if k > schema.VectorDim(idx) {
		return "", 0, false
	}
	return field, k - 1, true
}

// IsNativeVector reports whether name is a native vector field of
// schema.
func IsNativeVector(name string, schema *encoding.Schema) bool {
	if schema == nil || !schema.HasVectors() {
		return false
	}
	f := schema.Field(name)
	return f != nil && f.Type.IsVector()
}

// Natives returns one Resolved per native vector field of schema, in
// schema order (nil when it has none): Name = the field, Members its
// element members, Labels the schema labels (else the "f_<i>"
// defaults), Kind / scale range from the schema, Native set.
func Natives(schema *encoding.Schema) []Resolved {
	if schema == nil || !schema.HasVectors() {
		return nil
	}
	var out []Resolved
	for i := range schema.Fields {
		if !schema.Fields[i].Type.IsVector() {
			continue
		}
		if r, ok := nativeResolved(schema, i); ok {
			out = append(out, r)
		}
	}
	return out
}

// nativeResolved resolves logical field i of schema as a native vector.
func nativeResolved(schema *encoding.Schema, i int) (Resolved, bool) {
	vf, ok := schema.Vector(i)
	if !ok {
		return Resolved{}, false
	}
	f := &schema.Fields[i]
	r := Resolved{
		Name:    f.Name,
		Members: make([]string, vf.Dim),
		Labels:  make([]string, vf.Dim),
		Refs:    make([]MemberRef, vf.Dim),
		Kind:    types.VectorKind(vf.Kind.String()),
		Native: &NativeVector{Field: f.Name, FieldIndex: i, Dim: vf.Dim, Type: f.Type,
			HasLabels: len(vf.Labels) > 0},
	}
	for e := 0; e < vf.Dim; e++ {
		r.Members[e] = ElementMember(f.Name, e)
		r.Labels[e] = elementLabel(vf, f.Name, e)
		r.Refs[e] = MemberRef{Field: f.Name, Element: e}
	}
	if vf.HasScale {
		r.HasScale, r.ScaleMin, r.ScaleMax = true, vf.ScaleMin, vf.ScaleMax
	}
	return r, true
}

func elementLabel(vf encoding.VectorField, field string, e int) string {
	if e < len(vf.Labels) {
		return vf.Labels[e]
	}
	return ElementLabel(field, e)
}

// Ref returns member i's location: Refs[i] when the vector reads a
// native element anywhere, else the scalar column Members[i].
func (r Resolved) Ref(i int) MemberRef {
	if r.Refs != nil {
		return r.Refs[i]
	}
	return MemberRef{Field: r.Members[i], Element: -1}
}

// SourceFields returns the distinct schema fields r's members read, in
// first-use order: a scalar member's column, a native element's vector
// field. Projection decodes these (an element member names no column).
func (r Resolved) SourceFields() []string {
	if r.Refs == nil {
		return r.Members
	}
	return SourceFieldsOf(r.Refs)
}

// SourceFieldsOf is SourceFields over a member-location list.
func SourceFieldsOf(refs []MemberRef) []string {
	out := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, m := range refs {
		if !seen[m.Field] {
			seen[m.Field] = true
			out = append(out, m.Field)
		}
	}
	return out
}

// MemberSourceFields maps a resolved member-name list to the schema
// fields it reads (element members to their vector field), distinct, in
// first-use order. A name that is no element member is kept as is.
func MemberSourceFields(members []string, schema *encoding.Schema) []string {
	if schema == nil || !schema.HasVectors() {
		return members
	}
	refs := make([]MemberRef, len(members))
	for i, m := range members {
		refs[i] = MemberRef{Field: m, Element: -1}
		if f, e, ok := ParseElementMember(m, schema); ok {
			refs[i] = MemberRef{Field: f, Element: e}
		}
	}
	return SourceFieldsOf(refs)
}

// ReferencedNatives returns, in schema order, the native
// vector fields of schema req names directly in a vector slot (the
// slots Referenced reads). Projection decodes them; an expression's
// reference to a native field is an ordinary field identifier there.
func ReferencedNatives(req *types.Request, schema *encoding.Schema) []string {
	if req == nil || schema == nil || !schema.HasVectors() {
		return nil
	}
	refs := Referenced(req)
	var out []string
	for _, f := range schema.Fields {
		if f.Type.IsVector() && refs[f.Name] {
			out = append(out, f.Name)
		}
	}
	return out
}

// fieldIndex is name's logical position in schema, -1 when absent.
func fieldIndex(schema *encoding.Schema, name string) int {
	for i := range schema.Fields {
		if schema.Fields[i].Name == name {
			return i
		}
	}
	return -1
}
