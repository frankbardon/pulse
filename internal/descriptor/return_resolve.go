package descriptor

import (
	"reflect"
	"slices"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// return_resolve.go is the shared, PURE resolver of the `return`
// response-shaping block: predict and the runtime call the same
// functions, so a path predict refuses is a path the runtime refuses
// with the same code, message and details.
//
//   - ResolveReturn is schema-free (no cohort): it reads the block,
//     refuses a bad preset / precision / path syntax
//     (PULSE_RETURN_INVALID), resolves every include / exclude path
//     against the instance's Response type by reflection — the same
//     json-name and HiddenSlotKeys rules structSchema publishes, so a
//     hidden feature's path is PULSE_RETURN_PATH_UNKNOWN exactly like a
//     nonexistent one — and returns the canonical returnplan.Plan.
//   - ReturnColumnRefusal needs the schema: it judges every
//     `data[*].<column>` against the columns the (defaults-resolved)
//     request produces.
//
// Below a map key or an open (`any`) value the structural walk ends and
// any key is accepted (the path is marked Open); the runtime warns
// PULSE_RETURN_PATH_UNMATCHED when such an include matched nothing.

// returnPresetPaths is the preset table. A nil list selects the whole
// Response (every visible top-level key). A listed path a hidden
// feature owns is dropped silently at expansion, never refused.
//
// E2-S3 (response-shaping-core) fills `standard` and `minimal`; until
// then they expand like `full`.
var returnPresetPaths = map[types.ReturnPreset][]string{
	types.ReturnPresetFull:     nil,
	types.ReturnPresetStandard: nil,
	types.ReturnPresetMinimal:  nil,
}

// returnWarningsKey is the JSON key retained unless explicitly excluded.
const returnWarningsKey = "warnings"

// returnRoot is the Go type a Request's `return` paths root at.
var returnRoot = reflect.TypeFor[types.Response]()

// ResolveReturn resolves req.Return into the canonical selection plan.
// Nil (and no error) when the request carries no block. inst hides the
// paths its hidden features own; nil hides nothing. It never mutates
// req.
func ResolveReturn(req *types.Request, inst *InstanceSnapshot) (*returnplan.Plan, error) {
	if req == nil || req.Return == nil {
		return nil, nil
	}
	return resolveReturnBlock(req.Return, returnRoot, inst)
}

func resolveReturnBlock(ret *types.Return, root reflect.Type, inst *InstanceSnapshot) (*returnplan.Plan, error) {
	if ret.Precision < 0 || ret.Precision > 17 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RETURN_INVALID,
			"return precision "+strconv.Itoa(ret.Precision)+" is outside 1–17 significant digits",
			map[string]any{"key": "precision", "value": ret.Precision, "reason": "precision must be 1–17 (0 means unlimited)"})
	}
	if ret.Preset != "" {
		if _, ok := returnPresetPaths[ret.Preset]; !ok {
			valid := make([]string, 0, len(returnPresetPaths))
			for _, p := range types.AllReturnPresets() {
				valid = append(valid, string(p))
			}
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RETURN_INVALID,
				"return preset "+strconv.Quote(string(ret.Preset))+" is not one of "+joinQuoted(valid),
				map[string]any{"key": "preset", "value": string(ret.Preset), "reason": "unknown preset", "valid": valid})
		}
	}
	include, err := parseReturnPaths("include", ret.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := parseReturnPaths("exclude", ret.Exclude)
	if err != nil {
		return nil, err
	}
	for i := range include {
		if err := resolveReturnPath(root, &include[i], inst, "include", i, ret.Include[i]); err != nil {
			return nil, err
		}
	}
	for i := range exclude {
		if err := resolveReturnPath(root, &exclude[i], inst, "exclude", i, ret.Exclude[i]); err != nil {
			return nil, err
		}
	}

	rootKeys := returnVisibleKeys(root, inst)
	var base []returnplan.Path
	preset := string(ret.Preset)
	switch {
	case ret.Preset != "":
		base = expandReturnPreset(ret.Preset, root, inst, rootKeys)
	case len(ret.Include) > 0:
		// Include-only is an allowlist: empty base.
		preset = returnplan.PresetCustom
	default:
		base = rootKeyPaths(rootKeys)
		preset = string(types.ReturnPresetFull)
		if len(ret.Exclude) > 0 {
			preset = returnplan.PresetCustom
		}
	}

	// Warnings are retained unless explicitly excluded: the top-level
	// slot joins the selection, every nested one is kept wherever its
	// parent object is emitted.
	keep := returnWarningPaths(root, inst)
	var nested []returnplan.Path
	for _, k := range keep {
		if len(k.Segments) == 1 {
			base = append(base, k)
		} else {
			nested = append(nested, k)
		}
	}
	all := append(base, include...)
	return returnplan.New(preset, all, exclude, nested, ret.Precision, rootKeys), nil
}

// parseReturnPaths parses one path list, refusing the first malformed
// entry as PULSE_RETURN_INVALID.
func parseReturnPaths(key string, raw []string) ([]returnplan.Path, error) {
	out := make([]returnplan.Path, len(raw))
	for i, s := range raw {
		p, err := returnplan.Parse(s)
		if err != nil {
			reason := err.Error()
			if se, ok := err.(*returnplan.SyntaxError); ok {
				reason = se.Reason
			}
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RETURN_INVALID,
				"return "+key+"["+strconv.Itoa(i)+"] "+strconv.Quote(s)+" is malformed: "+reason,
				map[string]any{"key": key, "index": i, "value": s, "reason": reason})
		}
		out[i] = p
	}
	return out, nil
}

// returnField is one visible JSON property of a struct type.
type returnField struct {
	name string
	typ  reflect.Type
}

// returnStructFields lists t's visible JSON properties in declaration
// order: json names, `-` and unexported fields skipped, untagged
// embedded structs flattened, and the keys inst hides (HiddenSlotKeys)
// dropped — the same rules structSchema publishes.
func returnStructFields(t reflect.Type, inst *InstanceSnapshot) []returnField {
	hidden := HiddenSlotKeys(reflect.New(t).Interface(), inst)
	var out []returnField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, skip := jsonFieldName(f)
		if skip || slices.Contains(hidden, name) {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && name == f.Name {
			out = append(out, returnStructFields(f.Type, inst)...)
			continue
		}
		out = append(out, returnField{name: name, typ: f.Type})
	}
	return out
}

// returnVisibleKeys is the visible top-level keys of the root object.
func returnVisibleKeys(root reflect.Type, inst *InstanceSnapshot) []string {
	fields := returnStructFields(root, inst)
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.name
	}
	return keys
}

func rootKeyPaths(keys []string) []returnplan.Path {
	out := make([]returnplan.Path, len(keys))
	for i, k := range keys {
		out[i] = returnplan.Path{Segments: []returnplan.Segment{returnplan.Key(k)}}
	}
	return out
}

// expandReturnPreset expands a preset against the instance: a nil list
// is every visible top-level key; a listed path that does not resolve
// (a hidden feature owns it) is dropped silently.
func expandReturnPreset(preset types.ReturnPreset, root reflect.Type, inst *InstanceSnapshot, rootKeys []string) []returnplan.Path {
	list := returnPresetPaths[preset]
	if list == nil {
		return rootKeyPaths(rootKeys)
	}
	out := make([]returnplan.Path, 0, len(list))
	for _, s := range list {
		p, err := returnplan.Parse(s)
		if err != nil {
			continue
		}
		if resolveReturnPath(root, &p, inst, "preset", 0, s) != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// isOpenReturnType reports whether t's keys and shape are unknowable
// statically: an interface (`any`) or raw JSON.
func isOpenReturnType(t reflect.Type) bool {
	return t.Kind() == reflect.Interface || t == rawMessageType
}

// resolveReturnPath walks p over the Go type graph from root, marking
// p.Open when it steps through a map key or an open value. A step the
// type cannot take is PULSE_RETURN_PATH_UNKNOWN.
func resolveReturnPath(root reflect.Type, p *returnplan.Path, inst *InstanceSnapshot, key string, index int, raw string) error {
	t := root
	for i, seg := range p.Segments {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if isOpenReturnType(t) {
			p.Open = true
			return nil
		}
		fail := func(reason string, valid []string) error {
			details := map[string]any{"key": key, "index": index, "path": raw, "at": seg.String(), "reason": reason}
			if valid != nil {
				details["valid"] = valid
				if !seg.Index && !seg.Glob {
					if near := NearestKey(seg.Name, valid); near != "" {
						details["suggestion"] = near
					}
				}
			}
			prefix := returnplan.Path{Segments: p.Segments[:i]}.String()
			where := "the response"
			if prefix != "" {
				where = strconv.Quote(prefix)
			}
			return errors.NewCodedErrorWithDetails(errors.PULSE_RETURN_PATH_UNKNOWN,
				"return "+key+"["+strconv.Itoa(index)+"] "+strconv.Quote(raw)+": "+strconv.Quote(seg.String())+" is not in "+where+" ("+reason+")",
				details)
		}
		switch t.Kind() {
		case reflect.Struct:
			fields := returnStructFields(t, inst)
			names := make([]string, len(fields))
			for j, f := range fields {
				names[j] = f.name
			}
			sorted := append([]string(nil), names...)
			sort.Strings(sorted)
			if seg.Index {
				return fail("an object takes a key, not [*]", sorted)
			}
			if seg.Glob {
				return fail("a * glob matches map keys only, not a fixed object key", sorted)
			}
			j := slices.Index(names, seg.Name)
			if j < 0 {
				return fail("no such key", sorted)
			}
			t = fields[j].typ
		case reflect.Slice, reflect.Array:
			if t.Elem() == byteType {
				return fail("a leaf value has no children", nil)
			}
			if !seg.Index {
				return fail("an array takes [*], not a key", nil)
			}
			t = t.Elem()
		case reflect.Map:
			if seg.Index {
				return fail("a map takes a key, not [*]", nil)
			}
			p.Open = true
			t = t.Elem()
		default:
			return fail("a leaf value has no children", nil)
		}
	}
	return nil
}

// returnWarningPaths lists every visible `warnings` slot reachable from
// root through fixed object keys and arrays (never through a map or an
// open value), in canonical order.
func returnWarningPaths(root reflect.Type, inst *InstanceSnapshot) []returnplan.Path {
	var out []returnplan.Path
	onStack := map[reflect.Type]bool{}
	var walk func(t reflect.Type, prefix []returnplan.Segment)
	walk = func(t reflect.Type, prefix []returnplan.Segment) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			if t.Kind() == reflect.Pointer {
				t = t.Elem()
				continue
			}
			if t.Elem() == byteType {
				return
			}
			prefix = append(prefix, returnplan.Elem())
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || onStack[t] {
			return
		}
		onStack[t] = true
		defer delete(onStack, t)
		for _, f := range returnStructFields(t, inst) {
			path := append(append([]returnplan.Segment(nil), prefix...), returnplan.Key(f.name))
			if f.name == returnWarningsKey {
				out = append(out, returnplan.Path{Segments: path})
				continue
			}
			walk(f.typ, path)
		}
	}
	walk(root, nil)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// ReturnColumnRefusal judges every `data[*].<column>` include / exclude
// path of req.Return against the columns req produces over schema (the
// schema the request executes over — the joined one for a join).
// Call it on the defaults-resolved request: a defaulted aggregation's
// label carries its inferred type. Nil when every named column is
// produced, the request has no block, or the column set is open (a
// join, a crosstab, an unknown feature type). A `*` glob over data
// columns is never refused.
func ReturnColumnRefusal(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) error {
	if req == nil || req.Return == nil || schema == nil {
		return nil
	}
	cols, open := returnOutputColumns(req, schema, inst)
	if open {
		return nil
	}
	check := func(key string, raw []string) error {
		for i, s := range raw {
			p, err := returnplan.Parse(s)
			if err != nil || len(p.Segments) < 3 {
				continue
			}
			s0, s1, s2 := p.Segments[0], p.Segments[1], p.Segments[2]
			if s0.Name != "data" || s0.Glob || !s1.Index || s2.Index || s2.Glob {
				continue
			}
			if cols[s2.Name] {
				continue
			}
			valid := make([]string, 0, len(cols))
			for c := range cols {
				valid = append(valid, c)
			}
			sort.Strings(valid)
			details := map[string]any{"key": key, "index": i, "path": s, "at": s2.Name,
				"reason": "column not produced by this request", "valid": valid}
			if near := NearestKey(s2.Name, valid); near != "" {
				details["suggestion"] = near
			}
			return errors.NewCodedErrorWithDetails(errors.PULSE_RETURN_PATH_UNKNOWN,
				"return "+key+"["+strconv.Itoa(i)+"] "+strconv.Quote(s)+": data column "+strconv.Quote(s2.Name)+" is not produced by this request",
				details)
		}
		return nil
	}
	if err := check("include", req.Return.Include); err != nil {
		return err
	}
	return check("exclude", req.Return.Exclude)
}

// returnOutputColumns is the set of Response.Data column names req can
// produce over schema, and whether that set is open (unknowable
// statically). It follows the field-reference walk's pipeline (features
// → attributes → grouped output → windows) plus the label-binding
// `<field>_label` siblings. On an ungrouped run it keeps every schema
// field — the superset errs toward accepting a column, never refusing a
// real one.
func returnOutputColumns(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) (map[string]bool, bool) {
	if req.Crosstab != nil || len(req.Joins) > 0 {
		return nil, true
	}
	cols := make(map[string]bool, len(schema.Fields))
	for i := range schema.Fields {
		cols[schema.Fields[i].Name] = true
	}
	for _, feat := range req.Features {
		if feat == nil {
			continue
		}
		if !isKnownFeatureType(opRoute(inst, feat.Type)) {
			return nil, true
		}
		for _, label := range featureOutputLabels(feat, schema) {
			cols[label] = true
		}
	}
	projectAttributeOutputs(req, cols, inst)
	if len(req.Groups) > 0 || len(req.Aggregations) > 0 {
		cols = make(map[string]bool, len(req.Groups)+len(req.Aggregations)+len(req.Windows))
		for _, grp := range req.Groups {
			if grp != nil {
				cols[grp.Field] = true
			}
		}
		for _, agg := range req.Aggregations {
			if agg == nil {
				continue
			}
			label := agg.Label
			if label == "" {
				label = string(agg.Type) + "_" + agg.Field
			}
			cols[label] = true
		}
	}
	for _, win := range req.Windows {
		if win != nil {
			cols[windowLabel(win)] = true
		}
	}
	for _, lb := range req.Labels {
		if lb != nil && lb.Field != "" {
			cols[lb.Field+"_label"] = true
		}
	}
	return cols, false
}

// joinQuoted renders names as `"a", "b" or "c"`.
func joinQuoted(names []string) string {
	out := ""
	for i, n := range names {
		switch {
		case i == 0:
		case i == len(names)-1:
			out += " or "
		default:
			out += ", "
		}
		out += strconv.Quote(n)
	}
	return out
}

// returnPlanDescriptor renders a plan for PredictResult.Return; nil for
// a nil plan.
func returnPlanDescriptor(p *returnplan.Plan) *descriptor.ReturnPlan {
	if p == nil {
		return nil
	}
	return &descriptor.ReturnPlan{
		Preset:    p.Preset,
		Include:   returnplan.Strings(p.Include),
		Exclude:   returnplan.Strings(p.Exclude),
		Keep:      returnplan.Strings(p.Keep),
		Precision: p.Precision,
		Identity:  p.Identity(),
		Digest:    p.Digest,
	}
}
