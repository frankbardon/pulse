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

// returnPresetPaths is the preset table — the ONE definition of each
// preset, listed (expanded against the instance) in the manifest's
// return_presets block. A nil list selects the whole Response (every
// visible top-level key). A listed path a hidden feature owns is
// dropped silently at expansion, never refused. Every listed path must
// resolve against the full payload schema (TestReturnPathsMatchSchema),
// so a field rename cannot rot a preset.
//
// A preset is an INCLUDE allowlist: "X minus Y" is spelled as X's
// remaining keys, never as an exclude, so a caller's include on top of a
// preset can always add Y back (an exclude would win over it). Nested
// `*.warnings` need no entry — they are KEEP paths, emitted wherever
// their parent object is.
var returnPresetPaths = map[types.ReturnPreset][]string{
	types.ReturnPresetFull: nil,
	// standard: the primary result of every slot plus the figures most
	// callers read beside it; never components.
	types.ReturnPresetStandard: {
		"data", "warnings", "metadata", "crosstab",
		// matrices[*] minus auxiliary
		"matrices[*].name", "matrices[*].type", "matrices[*].group_key", "matrices[*].group_header",
		"matrices[*].primary", "matrices[*].vectors", "matrices[*].scalars",
		// tests headline + df / alpha / variant / multiplicity + effect sizes
		"tests[*].label", "tests[*].type", "tests[*].variant", "tests[*].statistic", "tests[*].df",
		"tests[*].p_value", "tests[*].alpha", "tests[*].reject_null", "tests[*].p_adjusted",
		"tests[*].significant_adjusted", "tests[*].multiplicity", "tests[*].details.effect_size",
		"post_tests[*].label", "post_tests[*].type", "post_tests[*].variant", "post_tests[*].statistic", "post_tests[*].df",
		"post_tests[*].p_value", "post_tests[*].alpha", "post_tests[*].reject_null", "post_tests[*].p_adjusted",
		"post_tests[*].significant_adjusted", "post_tests[*].multiplicity", "post_tests[*].details.effect_size",
		// regressions[*] minus credible_intervals and selection
		"regressions[*].name", "regressions[*].type", "regressions[*].family", "regressions[*].link",
		"regressions[*].penalty", "regressions[*].alpha", "regressions[*].l1_ratio", "regressions[*].prior",
		"regressions[*].resample", "regressions[*].criterion", "regressions[*].coefficients",
		"regressions[*].std_errors", "regressions[*].p_values", "regressions[*].r2", "regressions[*].adj_r2",
		"regressions[*].deviance", "regressions[*].null_deviance", "regressions[*].pseudo_r2",
		"regressions[*].n_obs", "regressions[*].sum_weights", "regressions[*].n_eff",
		"regressions[*].residual_std_err", "regressions[*].converged_iters", "regressions[*].selected_features",
		"overlays",
	},
	// minimal: the primary result of every slot; no metadata, no
	// components, no overlay payload.
	types.ReturnPresetMinimal: {
		"data", "warnings",
		"crosstab.shape", "crosstab.matrix",
		"matrices[*].name", "matrices[*].type", "matrices[*].group_key", "matrices[*].primary",
		"tests[*].label", "tests[*].type", "tests[*].statistic", "tests[*].p_value",
		"tests[*].reject_null", "tests[*].p_adjusted", "tests[*].significant_adjusted",
		"post_tests[*].label", "post_tests[*].type", "post_tests[*].statistic", "post_tests[*].p_value",
		"post_tests[*].reject_null", "post_tests[*].p_adjusted", "post_tests[*].significant_adjusted",
		"regressions[*].name", "regressions[*].type", "regressions[*].coefficients", "regressions[*].p_values",
		"overlays[*].name", "overlays[*].kind", "overlays[*].ref", "overlays[*].summary",
	},
}

// ReturnPresetsFor lists every preset expanded against inst — the
// manifest's return_presets block — in types.AllReturnPresets order.
// Paths are the canonical spellings of the preset's selection on this
// instance (a hidden feature's paths are absent); nested `*.warnings`
// keep paths are not listed. nil inst is the full registry.
func ReturnPresetsFor(inst *InstanceSnapshot) []descriptor.ReturnPresetMeta {
	rootKeys := returnVisibleKeys(returnRoot, inst)
	out := make([]descriptor.ReturnPresetMeta, 0, len(returnPresetPaths))
	for _, p := range types.AllReturnPresets() {
		paths := expandReturnPreset(p, returnRoot, inst, rootKeys)
		strs := make([]string, len(paths))
		for i, path := range paths {
			strs[i] = path.String()
		}
		out = append(out, descriptor.ReturnPresetMeta{Name: string(p), Paths: strs})
	}
	return out
}

// returnWarningsKey is the JSON key retained unless explicitly excluded.
const returnWarningsKey = "warnings"

// returnRoot is the Go type a Request's `return` paths root at.
var returnRoot = reflect.TypeFor[types.Response]()

// ResolveReturn resolves the request's EFFECTIVE `return` block
// (EffectiveReturn) into the canonical selection plan. Nil (and no
// error) when no layer supplies a block. inst hides the paths its
// hidden features own and carries the instance default; nil hides
// nothing and has no default. It never mutates req.
func ResolveReturn(req *types.Request, inst *InstanceSnapshot) (*returnplan.Plan, error) {
	ret := EffectiveReturn(req, inst)
	if ret == nil {
		return nil, nil
	}
	plan, err := resolveReturnBlock(ret, returnRoot, inst)
	if err != nil {
		return nil, err
	}
	if plan.Precision > 0 {
		plan.Exact = returnExactPaths(req)
	}
	return plan, nil
}

// returnComponentsKey is Response.Components — the slot the
// DisableComponents shorthand excludes.
const returnComponentsKey = "components"

// EffectiveReturn is the `return` block a request resolves through —
// the precedence both predict and the runtime apply:
//
//  1. the request's own block, when set, REPLACES every instance layer;
//  2. else the instance default (inst.DefaultReturn: Options.DefaultReturn,
//     else the feature profile's `return`);
//  3. else none (nil) — the library default `full`, identity.
//
// The DisableComponents shorthands then merge as `exclude: ["components"]`
// (exclude wins over any include) exactly when the components compute
// gate is off for this request (EffectiveDisableComponents): a request
// `disable_components: true` always; the engine Options.DisableComponents
// only through the instance-default layer (a request block replaces it,
// and the gate then reopens); an explicit `false` adds nothing and never
// re-includes what a block excludes or a preset omits. With no block on
// any layer the shorthands add nothing either — DisableComponents stays
// the compute gate alone, so its wire form is byte-identical to the
// pre-`return` baseline (no `returned` marker). The result is a fresh
// copy; req and inst are never mutated.
func EffectiveReturn(req *types.Request, inst *InstanceSnapshot) *types.Return {
	var ret *types.Return
	switch {
	case req != nil && req.Return != nil:
		ret = cloneReturn(req.Return)
	default:
		ret = inst.DefaultReturn()
	}
	if ret == nil {
		return nil
	}
	if EffectiveDisableComponents(req, inst.Behaviour().DisableComponents) &&
		slices.Contains(returnVisibleKeys(returnRoot, inst), returnComponentsKey) &&
		!slices.Contains(ret.Exclude, returnComponentsKey) {
		ret.Exclude = append(ret.Exclude, returnComponentsKey)
	}
	return ret
}

// EffectiveDisableComponents is the Response.Components COMPUTE gate for
// req on an engine whose Options.DisableComponents is engine — the one
// rule the runtime (Service.effectiveDisableComponents) and predict
// (PredictOptions.componentsDisabled) share. A request's explicit
// disable_components wins; else a request `return` block replaces the
// instance-default layer the engine switch folds into, so the gate is
// open; else the engine switch. Without a request block this is the
// pre-`return` rule unchanged.
func EffectiveDisableComponents(req *types.Request, engine bool) bool {
	if req != nil && req.DisableComponents != nil {
		return *req.DisableComponents
	}
	if req != nil && req.Return != nil {
		return false
	}
	return engine
}

// ValidateDefaultReturn checks an instance `return` default (from
// Options.DefaultReturn or a feature profile) against inst: the same
// preset / precision / path-syntax / path-existence rules a request
// block meets, returning the resolver's own coded error. Nil r is
// valid. A `data[*].<column>` path is not judged here (no request, no
// schema): an instance default names columns openly and the runtime
// warns PULSE_RETURN_PATH_UNMATCHED when one matches nothing.
func ValidateDefaultReturn(r *types.Return, inst *InstanceSnapshot) error {
	if r == nil {
		return nil
	}
	_, err := resolveReturnBlock(r, returnRoot, inst)
	return err
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
	if err := refuseReturnedMarker("include", include, ret.Include); err != nil {
		return nil, err
	}
	if err := refuseReturnedMarker("exclude", exclude, ret.Exclude); err != nil {
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
	// The caller's includes first: canonicalization keeps the first of
	// two equal spellings, so a caller's Open include survives a
	// preset's identical (never-warning) path.
	all := append(include, base...)
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

// returnMarkerKey is Response.Returned: stamped on every shaped
// response, so it is outside the path space — never a root key, never
// selectable or excludable.
const returnMarkerKey = "returned"

// refuseReturnedMarker refuses a path naming the `returned` marker as
// PULSE_RETURN_INVALID.
func refuseReturnedMarker(key string, paths []returnplan.Path, raw []string) error {
	for i, p := range paths {
		if s0 := p.Segments[0]; s0.Glob || s0.Index || s0.Name != returnMarkerKey {
			continue
		}
		reason := "the returned marker is stamped on every shaped response and cannot be selected or excluded"
		return errors.NewCodedErrorWithDetails(errors.PULSE_RETURN_INVALID,
			"return "+key+"["+strconv.Itoa(i)+"] "+strconv.Quote(raw[i])+": "+reason,
			map[string]any{"key": key, "index": i, "value": raw[i], "reason": reason})
	}
	return nil
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
		if skip || slices.Contains(hidden, name) || (t == returnRoot && name == returnMarkerKey) {
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
		// A preset path through an open map (tests[*].details.effect_size)
		// is the preset's own, not the caller's: it never raises
		// PULSE_RETURN_PATH_UNMATCHED when the response has no such key.
		p.Open = false
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
