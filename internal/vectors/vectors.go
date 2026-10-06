// Package vectors is the one resolution of Request.Vectors: it turns
// each types.VectorSpec into its ordered member list against a schema,
// refusing malformed specs and unsupported member types with the
// PULSE_VECTOR_* codes.
//
// It is NO-EXECUTE: it reads only the schema, never a record, and
// imports nothing but the standard library, errors, encoding and types.
// Predict (internal/descriptor), the runtime (internal/service, via the
// descriptor's field-reference pass) and projection
// (internal/processing.NeededFields) all call it, so the three cannot
// disagree about what a vector contains.
//
// Resolution rules (the contract):
//
//   - Vector names are non-empty and unique per request.
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
//
// Checks run spec by spec in request order; within one spec in the
// order: name, duplicate name, fields/pattern shape, coerce, expansion
// (unknown literal, bad glob / regex, duplicate member), empty, member
// types, labels. The first failure is returned.
package vectors

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
}

// Resolve resolves every spec in specs against schema, in order. It
// returns the resolved vectors (index-aligned with specs) or the first
// refusal. A nil / empty specs resolves to nil, nil.
func Resolve(specs []types.VectorSpec, schema *encoding.Schema) ([]Resolved, *errors.CodedError) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]Resolved, 0, len(specs))
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
		r, err := ResolveSpec(at, spec, schema)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
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

	var members []string
	var err *errors.CodedError
	if hasFields {
		members, err = expandFields(at, spec, schema)
	} else {
		members, err = expandPattern(at, spec, schema)
	}
	if err != nil {
		return Resolved{}, err
	}
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

	for _, m := range members {
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
	}
	return Resolved{
		Name:    spec.Name,
		Members: members,
		Labels:  append([]string(nil), labels...),
		Coerce:  spec.Coerce,
	}, nil
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

// Referenced returns the vector names req's operator slots reference.
// No slot references a vector yet; the matrix slot (Request.Matrices)
// adds its references here, so the unreferenced-vector warning and
// every consumer read one answer.
func Referenced(req *types.Request) map[string]bool {
	return map[string]bool{}
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
		members = append(members, r.Members...)
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

// expandFields expands a Fields list: literals in place, globs in schema
// order at their position.
func expandFields(at string, spec types.VectorSpec, schema *encoding.Schema) ([]string, *errors.CodedError) {
	var out []string
	seen := map[string]bool{}
	add := func(name string) *errors.CodedError {
		if seen[name] {
			return duplicateMember(at, spec.Name, name)
		}
		seen[name] = true
		out = append(out, name)
		return nil
	}
	for j, entry := range spec.Fields {
		entryPath := at + ".fields[" + strconv.Itoa(j) + "]"
		if entry == "" {
			return nil, invalid(at, spec.Name, "empty_field_entry",
				"vector "+describe(at, spec.Name)+" has an empty entry at "+entryPath, nil)
		}
		if !isGlob(entry) {
			if schema == nil || schema.Field(entry) == nil {
				return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					"vector references unknown field: "+entry,
					map[string]any{"field": entry, "vector": spec.Name, "slot": entryPath})
			}
			if err := add(entry); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := path.Match(entry, ""); err != nil {
			return nil, invalid(at, spec.Name, "bad_glob",
				"vector "+describe(at, spec.Name)+" glob "+strconv.Quote(entry)+" is malformed",
				map[string]any{"value": entry})
		}
		if schema == nil {
			continue
		}
		for _, f := range schema.Fields {
			if ok, _ := path.Match(entry, f.Name); ok {
				if err := add(f.Name); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// expandPattern takes every schema field the regular expression matches,
// in schema order.
func expandPattern(at string, spec types.VectorSpec, schema *encoding.Schema) ([]string, *errors.CodedError) {
	re, err := regexp.Compile(spec.Pattern)
	if err != nil {
		return nil, invalid(at, spec.Name, "bad_pattern",
			"vector "+describe(at, spec.Name)+" pattern does not compile: "+err.Error(),
			map[string]any{"value": spec.Pattern})
	}
	if schema == nil {
		return nil, nil
	}
	var out []string
	for _, f := range schema.Fields {
		if re.MatchString(f.Name) {
			out = append(out, f.Name)
		}
	}
	return out, nil
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
