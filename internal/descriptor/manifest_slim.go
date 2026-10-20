package descriptor

import (
	"reflect"

	"github.com/frankbardon/pulse/descriptor"
)

// slimBlankedFields names the prose fields SlimManifest blanks wherever
// they occur in the manifest tree: every Description, plus the operator
// hint notes StreamableHint and EmitsTypeNote.
var slimBlankedFields = map[string]bool{
	"Description":    true,
	"StreamableHint": true,
	"EmitsTypeNote":  true,
}

// SlimManifest returns a copy of m with every prose Description field
// blanked — in every section, at every depth: component operators and
// their params, component_schema keys, components_schemas, tests,
// post-tests, regressions (params, modifiers, hints), matrices, synth
// distributions, overlays, commands, operations, MCP tools, skills,
// extensions and the capability blocks — plus the StreamableHint and
// EmitsTypeNote operator notes. Structural metadata (names, params,
// types, tiers, accept-type lists, streamability flags,
// cross-references, fixups) is preserved.
//
// The slim variant is intended for size-sensitive MCP/CLI clients that
// would rather pay a few extra discovery round-trips than transit the
// full descriptive payload at session start. The default manifest
// surface remains the full one; --slim is opt-in.
//
// The walk is by reflection over the manifest's types, so a section
// added later is blanked without a new case here
// (TestSlimManifest_BlanksEveryDescriptionField fills every reachable
// field to prove it). SlimManifest never mutates the input: every
// slice, map, pointer and interface on the path is copied, never
// aliased.
func SlimManifest(m *descriptor.Manifest) *descriptor.Manifest {
	if m == nil {
		return nil
	}
	out := slimCopy(reflect.ValueOf(*m)).Interface().(descriptor.Manifest)
	return &out
}

// slimCopy deep-copies v, blanking every slimBlankedFields string field
// of every struct it reaches. Nil slices, maps and pointers stay nil, so
// the wire form is otherwise byte-identical.
func slimCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		p := reflect.New(v.Type().Elem())
		p.Elem().Set(slimCopy(v.Elem()))
		return p
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(slimCopy(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			if f.Type.Kind() == reflect.String && slimBlankedFields[f.Name] {
				out.Field(i).SetString("")
				continue
			}
			out.Field(i).Set(slimCopy(v.Field(i)))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(slimCopy(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(slimCopy(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), slimCopy(iter.Value()))
		}
		return out
	default:
		return v
	}
}
