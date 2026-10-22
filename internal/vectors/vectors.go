// Package vectors is the one resolution of Request.Vectors: it turns
// each types.VectorSpec into its ordered member list against a schema,
// refusing malformed specs and unsupported member types with the
// PULSE_VECTOR_* codes.
//
// It is NO-EXECUTE: it reads only the schema, never a record, and
// imports nothing but the standard library, errors, encoding, types,
// the parse-only internal/exprident (an expression's vector references)
// and the shared formula environment's no-execute leaf internal/exprenv
// (the built-in names and their coded refusals).
// Predict (internal/descriptor), the runtime (internal/service, via the
// descriptor's field-reference pass) and projection
// (internal/processing.NeededFields) all call it, so the three cannot
// disagree about what a vector contains.
//
// Resolution rules (the contract):
//
//   - Vector names are non-empty, unique per request and never a
//     schema field's name (an expression binds a named vector as an
//     identifier beside the fields: PULSE_VECTOR_NAME_COLLISION;
//     details.native when the field is a native vector field).
//   - A native vector field (vec_f32 / vec_f64) is a vector with no
//     spec: Resolve appends one Resolved per native field after the
//     spec-backed ones, and a fields entry / glob / pattern naming one
//     expands to its element members ("f[k]", 1-based) — see native.go.
//   - Exactly one of Fields and Pattern is set.
//   - A Fields entry without a glob metacharacter (`*`, `?`, `[`) is a
//     literal member and keeps its position (caller order is axis order);
//     a glob entry expands in place to every matching schema field, in
//     schema order (path.Match semantics, whole-name). Pattern is a Go
//     regular expression (unanchored) whose matches are taken in schema
//     order.
//   - A member repeated after expansion is refused; so is an empty
//     resolution.
//   - Members are integer (u4/u8/u16/u32/u64) or float (f32/f64) fields;
//     packed_bool needs Coerce "binary"; categorical, set_*, date,
//     datetime and decimal128 are refused naming the field.
//   - Labels, when set, carry one entry per resolved member.
//   - Kind, when set, is one of types.AllVectorKinds (unknown_kind);
//     scale_min / scale_max are set only under kind "scale"
//     (scale_bounds_without_scale_kind), together (scale_bounds_partial)
//     and finite with min < max (scale_bounds_invalid).
//   - Standardize, when set, is one of types.AllVectorStandardizes
//     (unknown_standardize). Which consumers may read a standardized
//     vector is judged after resolution (StandardizeRefusal, from
//     ResolveMatricesWith): a listwise co-moment MAT_* slot only.
//
// Checks run spec by spec in request order; within one spec in the
// order: name, duplicate name, field-name collision, fields/pattern
// shape, coerce, kind and scale bounds, standardize, expansion (unknown literal, bad glob / regex, duplicate
// member), empty, member types, labels. The first failure is returned.
package vectors

import (
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/types"
)

// Resolved is one vector after resolution.
type Resolved struct {
	// Name is the vector's request-scoped name.
	Name string
	// Members are the member field names in axis order.
	Members []string
	// Labels are the display labels, one per member: the spec's own, or
	// the member names when the spec sets none.
	Labels []string
	// Coerce is the spec's coercion ("" when none).
	Coerce types.VectorCoerce
	// Kind is the spec's declared kind ("" when none).
	Kind types.VectorKind
	// HasScale reports declared scale bounds (kind "scale" only):
	// ScaleMin < ScaleMax, both finite.
	HasScale           bool
	ScaleMin, ScaleMax float64
	// Standardize is the spec's member rescaling ("" when none): a
	// listwise co-moment matrix standardizes its merged state
	// (processing.standardizeCoMoment).
	Standardize types.VectorStandardize
	// Native is set when the vector IS one native vector field named
	// directly (no Request.Vectors entry): its metadata is the schema's.
	Native *NativeVector
	// Refs locates each member (index-aligned with Members) when any
	// member is an element of a native vector field; nil when every
	// member is a scalar column (Ref reads Members then).
	Refs []MemberRef
}

// Resolve resolves every spec in specs against schema, in order. It
// returns the resolved vectors — index-aligned with specs, followed by
// the schema's native vector fields (Natives), so every by-name lookup
// (Find) accepts a native field named directly — or the first refusal.
// A nil / empty specs over a vector-free schema resolves to nil, nil.
func Resolve(specs []types.VectorSpec, schema *encoding.Schema) ([]Resolved, *errors.CodedError) {
	natives := Natives(schema)
	if len(specs) == 0 {
		return natives, nil
	}
	out := make([]Resolved, 0, len(specs)+len(natives))
	seen := make(map[string]int, len(specs))
	for i := range specs {
		spec := specs[i]
		at := slotPath(i)
		if spec.Name == "" {
			return nil, invalid(at, spec.Name, "empty_name", "vector "+at+" has no name", nil)
		}
		if first, dup := seen[spec.Name]; dup {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_DUPLICATE,
				"vector name "+strconv.Quote(spec.Name)+" is defined twice (vectors["+strconv.Itoa(first)+"] and "+at+")",
				map[string]any{"name": spec.Name, "indices": []int{first, i}})
		}
		seen[spec.Name] = i
		if IsNativeVector(spec.Name, schema) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_NAME_COLLISION,
				"vector name "+strconv.Quote(spec.Name)+" ("+at+") is a native vector field of the cohort; name the field directly in the slot (no vectors entry), or list it in a differently named vector's fields to re-kind it",
				map[string]any{"vector": at, "name": spec.Name, "field": spec.Name, "native": true})
		}
		if schema != nil && schema.Field(spec.Name) != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_NAME_COLLISION,
				"vector name "+strconv.Quote(spec.Name)+" ("+at+") is also a schema field's name; an expression could not tell the two apart",
				map[string]any{"vector": at, "name": spec.Name, "field": spec.Name})
		}
		r, err := ResolveSpec(at, spec, schema)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return append(out, natives...), nil
}

// ResolveSpec resolves one spec against schema. at is the spec's path in
// the request ("vectors[0]", or an operator slot's inline-fields path)
// and lands in every refusal's details under "vector". Duplicate NAMES
// are Resolve's concern, not this function's.
func ResolveSpec(at string, spec types.VectorSpec, schema *encoding.Schema) (Resolved, *errors.CodedError) {
	hasFields, hasPattern := len(spec.Fields) > 0, spec.Pattern != ""
	switch {
	case hasFields && hasPattern:
		return Resolved{}, invalid(at, spec.Name, "fields_and_pattern",
			"vector "+describe(at, spec.Name)+" sets both fields and pattern; set exactly one", nil)
	case !hasFields && !hasPattern:
		return Resolved{}, invalid(at, spec.Name, "no_fields_or_pattern",
			"vector "+describe(at, spec.Name)+" sets neither fields nor pattern; set exactly one", nil)
	}
	if spec.Coerce != "" && !knownCoerce(spec.Coerce) {
		return Resolved{}, invalid(at, spec.Name, "unknown_coerce",
			"vector "+describe(at, spec.Name)+" has unknown coerce "+strconv.Quote(string(spec.Coerce)),
			map[string]any{"value": string(spec.Coerce), "valid": coerceNames()})
	}
	if err := checkKind(at, spec); err != nil {
		return Resolved{}, err
	}
	if spec.Standardize != "" && !slices.Contains(types.AllVectorStandardizes(), spec.Standardize) {
		return Resolved{}, invalid(at, spec.Name, "unknown_standardize",
			"vector "+describe(at, spec.Name)+" has unknown standardize "+strconv.Quote(string(spec.Standardize)),
			map[string]any{"value": string(spec.Standardize), "valid": standardizeNames()})
	}

	var x expansion
	var err *errors.CodedError
	if hasFields {
		x, err = expandFields(at, spec, schema)
	} else {
		x, err = expandPattern(at, spec, schema)
	}
	if err != nil {
		return Resolved{}, err
	}
	members := x.members
	if len(members) == 0 {
		details := map[string]any{"vector": at, "name": spec.Name}
		if hasFields {
			details["fields"] = append([]string(nil), spec.Fields...)
		} else {
			details["pattern"] = spec.Pattern
		}
		return Resolved{}, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_EMPTY,
			"vector "+describe(at, spec.Name)+" matches no field", details)
	}

	for i, m := range members {
		if x.refs != nil && x.refs[i].Native() {
			continue // a native element is a float: always a member
		}
		f := schema.Field(m)
		if f == nil {
			continue // unreachable: members come from the schema
		}
		if !MemberTypeAllowed(f.Type, spec.Coerce) {
			msg := "vector " + describe(at, spec.Name) + " member " + strconv.Quote(m) +
				" has type " + f.Type.String() + ", which a vector cannot carry"
			if f.Type == encoding.FieldTypePackedBool {
				msg += " without coerce \"binary\""
			}
			return Resolved{}, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_MEMBER_TYPE, msg,
				map[string]any{"vector": at, "name": spec.Name, "field": m, "field_type": f.Type.String()})
		}
	}

	labels := spec.Labels
	if len(labels) > 0 && len(labels) != len(members) {
		return Resolved{}, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_LABELS_MISMATCH,
			"vector "+describe(at, spec.Name)+" has "+strconv.Itoa(len(labels))+" labels for "+
				strconv.Itoa(len(members))+" members",
			map[string]any{"vector": at, "name": spec.Name, "labels": len(labels), "members": len(members), "resolved": members})
	}
	if len(labels) == 0 {
		labels = members
		if x.refs != nil {
			labels = x.defaultLabels(schema)
		}
	}
	r := Resolved{
		Name:    spec.Name,
		Members: members,
		Labels:  append([]string(nil), labels...),
		Coerce:  spec.Coerce,
		Kind:    spec.Kind,
		Refs:    x.refs,

		Standardize: spec.Standardize,
	}
	if spec.ScaleMin != nil {
		r.HasScale, r.ScaleMin, r.ScaleMax = true, *spec.ScaleMin, *spec.ScaleMax
	}
	return r, nil
}

// checkKind judges a spec's kind and scale bounds: a known kind, bounds
// only under kind "scale", both or neither, finite, min < max — each a
// PULSE_VECTOR_INVALID naming its reason.
func checkKind(at string, spec types.VectorSpec) *errors.CodedError {
	if spec.Kind != "" && !slices.Contains(types.AllVectorKinds(), spec.Kind) {
		return invalid(at, spec.Name, "unknown_kind",
			"vector "+describe(at, spec.Name)+" has unknown kind "+strconv.Quote(string(spec.Kind)),
			map[string]any{"value": string(spec.Kind), "valid": kindNames()})
	}
	lo, hi := spec.ScaleMin, spec.ScaleMax
	if lo == nil && hi == nil {
		return nil
	}
	if spec.Kind != types.VectorKindScale {
		return invalid(at, spec.Name, "scale_bounds_without_scale_kind",
			"vector "+describe(at, spec.Name)+" sets scale_min / scale_max, which only a kind \"scale\" vector declares",
			map[string]any{"kind": string(spec.Kind)})
	}
	if lo == nil || hi == nil {
		return invalid(at, spec.Name, "scale_bounds_partial",
			"vector "+describe(at, spec.Name)+" sets one of scale_min / scale_max; set both or neither", nil)
	}
	if !finite(*lo) || !finite(*hi) || !(*lo < *hi) {
		return invalid(at, spec.Name, "scale_bounds_invalid",
			"vector "+describe(at, spec.Name)+" scale_min must be finite and below a finite scale_max",
			map[string]any{"scale_min": *lo, "scale_max": *hi})
	}
	return nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func kindNames() []string {
	all := types.AllVectorKinds()
	out := make([]string, len(all))
	for i, k := range all {
		out[i] = string(k)
	}
	return out
}

// MemberTypeAllowed reports whether a field of type ft may be a vector
// member under coerce: integer and float types always; packed_bool only
// under VectorCoerceBinary; nothing else.
func MemberTypeAllowed(ft encoding.FieldType, coerce types.VectorCoerce) bool {
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16,
		encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64:
		return true
	case encoding.FieldTypePackedBool:
		return coerce == types.VectorCoerceBinary
	}
	return false
}

// Find returns the resolved vector named name.
func Find(resolved []Resolved, name string) (Resolved, bool) {
	for _, r := range resolved {
		if r.Name == name {
			return r, true
		}
	}
	return Resolved{}, false
}

// Referenced returns the vector names req's operator slots reference:
// every Request.Matrices spec's `vector`, every tier-1 test's
// `vector` / `vector2`, every attribute's `vector`, every grouper
// slot's `vector` (groups and both crosstab axes), every aggregation
// slot's `vector` (aggregations, the crosstab cell and its margin
// aggregations), and every
// Request.Vectors name an expression reads as a free identifier — an
// ATTR_FORMULA expression, a FILTER_EXPRESSION filterer's expression
// (Request.Filterers and a vector attribute's params.fit_on) and so a
// total() argument inside either (internal/exprident.Free, the walk the
// expression program binds by). It is the one reference set, so the
// unreferenced-vector warning and every consumer read one answer.
func Referenced(req *types.Request) map[string]bool {
	refs := map[string]bool{}
	if req == nil {
		return refs
	}
	for _, m := range req.Matrices {
		if m.Vector != "" {
			refs[m.Vector] = true
		}
		if g, ok := SpecBetween(m); ok && g.Vector != "" {
			refs[g.Vector] = true
		}
	}
	for _, t := range req.Tests {
		if t == nil {
			continue
		}
		for _, v := range []string{t.Vector, t.Vector2} {
			if v != "" {
				refs[v] = true
			}
		}
	}
	for _, a := range req.Attributes {
		if a != nil && a.Vector != "" {
			refs[a.Vector] = true
		}
	}
	for _, s := range GroupSlots(req) {
		if s.Group.Vector != "" {
			refs[s.Group.Vector] = true
		}
	}
	anyAggregationSlot(req, func(a *types.Aggregation) bool {
		if a.Vector != "" {
			refs[a.Vector] = true
		}
		return false
	})
	exprReferenced(req, refs)
	return refs
}

// exprReferenced adds to refs every Request.Vectors name an expression
// slot of req reads as a free identifier. A name that is no vector (a
// field, an attribute label) is not added; an unparsable expression
// adds nothing (its compile refuses the request).
func exprReferenced(req *types.Request, refs map[string]bool) {
	if len(req.Vectors) == 0 {
		return
	}
	names := make(map[string]bool, len(req.Vectors))
	for _, v := range req.Vectors {
		names[v.Name] = true
	}
	scan := func(src string) {
		if src == "" {
			return
		}
		idents, _, _ := exprident.Free(src)
		for _, id := range idents {
			if names[id] {
				refs[id] = true
			}
		}
	}
	filters := func(fs []*types.Filterer) {
		for _, f := range fs {
			if f != nil && f.Type == types.FILTER_EXPRESSION {
				scan(f.Expression)
			}
		}
	}
	filters(req.Filterers)
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		if a.Type == types.ATTR_FORMULA {
			scan(a.Expression)
		}
		filters(AttributeFitOn(a.Type, a))
	}
}

// Unreferenced returns, in request order, the names of req's vectors no
// operator slot references (each is a PULSE_VECTOR_UNREFERENCED
// warning).
func Unreferenced(req *types.Request) []string {
	if req == nil || len(req.Vectors) == 0 {
		return nil
	}
	refs := Referenced(req)
	var out []string
	for _, v := range req.Vectors {
		if !refs[v.Name] {
			out = append(out, v.Name)
		}
	}
	return out
}

// UnreferencedWarning builds the PULSE_VECTOR_UNREFERENCED warning for
// vector name.
func UnreferencedWarning(name string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_UNREFERENCED,
		"vector "+strconv.Quote(name)+" is defined but no operator references it",
		map[string]any{"name": name})
}

// Members returns the union of every member req's vectors resolve to
// against schema, in vector then member order, and whether every vector
// resolved. Projection calls it: a vector that fails to resolve is
// refused before any record is read, so ok == false only tells the
// caller to decode wide rather than guess.
func Members(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	if req == nil || len(req.Vectors) == 0 {
		return nil, true
	}
	ok = true
	for i, spec := range req.Vectors {
		r, err := ResolveSpec(slotPath(i), spec, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.SourceFields()...)
	}
	return members, ok
}

// Normalize returns req with every vector rewritten to its RESOLVED
// form — `fields` = the member list, `pattern` cleared, everything else
// kept — so Hash() on the result is a function of the resolved members:
// a pattern and the equivalent explicit field list hash identically.
// The request is shallow-copied (the caller's request never changes);
// req itself is returned when it has no vectors or a vector fails to
// resolve (that request is refused anyway). Request.Hash() alone is
// schema-free and hashes vectors as written; hash the normalized request
// wherever a schema is in hand and resolved-member identity is wanted.
func Normalize(req *types.Request, schema *encoding.Schema) *types.Request {
	if req == nil || len(req.Vectors) == 0 {
		return req
	}
	resolved, err := Resolve(req.Vectors, schema)
	if err != nil {
		return req
	}
	clone := *req
	clone.Vectors = make([]types.VectorSpec, len(req.Vectors))
	for i, spec := range req.Vectors {
		spec.Fields = append([]string(nil), resolved[i].Members...)
		spec.Pattern = ""
		if len(spec.Labels) > 0 {
			spec.Labels = append([]string(nil), spec.Labels...)
		}
		clone.Vectors[i] = spec
	}
	return &clone
}

// expansion is a spec's resolved member list: the member names and,
// when any member is a native vector element, every member's location
// (nil otherwise — the vector-free shape, byte-identical).
type expansion struct {
	at, name string // the spec's path and name, for a duplicate refusal
	members  []string
	refs     []MemberRef
	seen     map[string]bool
}

// add appends one member, refusing a repeat.
func (x *expansion) add(name string, ref MemberRef) *errors.CodedError {
	if x.seen == nil {
		x.seen = map[string]bool{}
	}
	if x.seen[name] {
		return duplicateMember(x.at, x.name, name)
	}
	x.seen[name] = true
	if ref.Native() && x.refs == nil {
		x.refs = make([]MemberRef, len(x.members), len(x.members)+1)
		for i, m := range x.members {
			x.refs[i] = MemberRef{Field: m, Element: -1}
		}
	}
	x.members = append(x.members, name)
	if x.refs != nil {
		x.refs = append(x.refs, ref)
	}
	return nil
}

// addField appends schema field f: a scalar column as one member, a
// native vector field as its elements in element order.
func (x *expansion) addField(schema *encoding.Schema, f *encoding.Field) *errors.CodedError {
	if !f.Type.IsVector() {
		return x.add(f.Name, MemberRef{Field: f.Name, Element: -1})
	}
	dim := schema.VectorDim(fieldIndex(schema, f.Name))
	for e := 0; e < dim; e++ {
		if err := x.add(ElementMember(f.Name, e), MemberRef{Field: f.Name, Element: e}); err != nil {
			return err
		}
	}
	return nil
}

// defaultLabels is one label per member when the spec sets none: a
// scalar member's name, a native element's schema label (else
// ElementLabel).
func (x *expansion) defaultLabels(schema *encoding.Schema) []string {
	out := make([]string, len(x.members))
	for i, ref := range x.refs {
		if !ref.Native() {
			out[i] = x.members[i]
			continue
		}
		vf, _ := schema.Vector(fieldIndex(schema, ref.Field))
		out[i] = elementLabel(vf, ref.Field, ref.Element)
	}
	return out
}

// expandFields expands a Fields list: literals in place, globs in schema
// order at their position, a native vector field to its elements, an
// element member ("f[k]") to that one element.
func expandFields(at string, spec types.VectorSpec, schema *encoding.Schema) (expansion, *errors.CodedError) {
	x := expansion{at: at, name: spec.Name}
	for j, entry := range spec.Fields {
		entryPath := at + ".fields[" + strconv.Itoa(j) + "]"
		if entry == "" {
			return expansion{}, invalid(at, spec.Name, "empty_field_entry",
				"vector "+describe(at, spec.Name)+" has an empty entry at "+entryPath, nil)
		}
		if field, e, ok := ParseElementMember(entry, schema); ok {
			if err := x.add(entry, MemberRef{Field: field, Element: e}); err != nil {
				return expansion{}, err
			}
			continue
		}
		// An entry naming a schema field exactly is that field, glob
		// metacharacters and all: a later chain stage's input carries
		// expanded element columns ("m[b1]"), and the runtime's slot
		// rewrite hands the resolved member names back as entries.
		if !isGlob(entry) || (schema != nil && schema.Field(entry) != nil) {
			if schema == nil || schema.Field(entry) == nil {
				return expansion{}, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					"vector references unknown field: "+entry,
					map[string]any{"field": entry, "vector": spec.Name, "slot": entryPath})
			}
			if err := x.addField(schema, schema.Field(entry)); err != nil {
				return expansion{}, err
			}
			continue
		}
		if _, err := path.Match(entry, ""); err != nil {
			return expansion{}, invalid(at, spec.Name, "bad_glob",
				"vector "+describe(at, spec.Name)+" glob "+strconv.Quote(entry)+" is malformed",
				map[string]any{"value": entry})
		}
		if schema == nil {
			continue
		}
		for i := range schema.Fields {
			if ok, _ := path.Match(entry, schema.Fields[i].Name); ok {
				if err := x.addField(schema, &schema.Fields[i]); err != nil {
					return expansion{}, err
				}
			}
		}
	}
	return x, nil
}

// expandPattern takes every schema field the regular expression matches,
// in schema order (a native vector field as its elements).
func expandPattern(at string, spec types.VectorSpec, schema *encoding.Schema) (expansion, *errors.CodedError) {
	re, err := regexp.Compile(spec.Pattern)
	if err != nil {
		return expansion{}, invalid(at, spec.Name, "bad_pattern",
			"vector "+describe(at, spec.Name)+" pattern does not compile: "+err.Error(),
			map[string]any{"value": spec.Pattern})
	}
	x := expansion{at: at, name: spec.Name}
	if schema == nil {
		return x, nil
	}
	for i := range schema.Fields {
		if re.MatchString(schema.Fields[i].Name) {
			if err := x.addField(schema, &schema.Fields[i]); err != nil {
				return expansion{}, err
			}
		}
	}
	return x, nil
}

func isGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

func knownCoerce(c types.VectorCoerce) bool {
	for _, k := range types.AllVectorCoerces() {
		if k == c {
			return true
		}
	}
	return false
}

func standardizeNames() []string {
	all := types.AllVectorStandardizes()
	out := make([]string, len(all))
	for i, s := range all {
		out[i] = string(s)
	}
	return out
}

func coerceNames() []string {
	all := types.AllVectorCoerces()
	out := make([]string, len(all))
	for i, c := range all {
		out[i] = string(c)
	}
	return out
}

func slotPath(i int) string { return "vectors[" + strconv.Itoa(i) + "]" }

func describe(at, name string) string {
	if name == "" {
		return at
	}
	return strconv.Quote(name) + " (" + at + ")"
}

func duplicateMember(at, name, field string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_DUPLICATE,
		"vector "+describe(at, name)+" lists member "+strconv.Quote(field)+" more than once after expansion",
		map[string]any{"vector": at, "name": name, "field": field})
}

func invalid(at, name, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"vector": at, "name": name, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_INVALID, msg, details)
}

// isExtension reports whether a slot's routed type name is a registered
// extension operator whose vector input the resolvers judge: not a
// built-in of its category, not routed away (""), and known to ext (nil:
// no extension is registered).
func isExtension(ext func(string) bool, name string, builtin bool) bool {
	return ext != nil && name != "" && !builtin && ext(name)
}
