package sweep

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/template"
	"github.com/frankbardon/pulse/types"
)

// listOptimize is the Compose `optimize` list: its blocks fan out at the
// Compose level (ExpandOptimize), never inside a Request.
const listOptimize = "optimize"

// ReasonNameWithoutAxis: a fanned-out optimize block's `name` carries no
// axis placeholder, so every block it adds would share one name. Each
// expanded block needs its own name (results are keyed by it).
const ReasonNameWithoutAxis = "name_without_axis"

// ReasonMarkerPosition: a fanned-out optimize block carries a
// `{"$var": "axis"}` marker where no typed value of its own stands — a
// whole list (`variables`, `constraints`, `models`) or one entry of a
// list. A marker stands only for one whole value of the
// block or of one of its list entries — the positions the payload
// schema and the MCP pulse_compose schema admit — so the three never
// disagree on what runs.
const ReasonMarkerPosition = "marker_position"

// OptimizeExpandedCount is the number of optimize blocks c's for_each
// fan-outs add: FanOutCount summed over every block (plain blocks do
// not count), saturating at math.MaxInt. Structure is not checked.
func OptimizeExpandedCount(c *types.ComposedRequest) int {
	if c == nil {
		return 0
	}
	n := 0
	for i := range c.Optimize {
		n = addSaturating(n, FanOutCount(c.Optimize[i].ForEach))
	}
	return n
}

// OptimizeHasForEach reports whether any of c's optimize blocks carries a
// for_each.
func OptimizeHasForEach(c *types.ComposedRequest) bool {
	if c == nil {
		return false
	}
	for i := range c.Optimize {
		if c.Optimize[i].ForEach != nil {
			return true
		}
	}
	return false
}

// ExpandOptimize is the ONE Compose-level for_each expansion: every
// optimize block carrying a for_each is replaced in place by one block
// per axis combination. banner.ExpandCompose runs it right after the
// `base` merge, so serial Compose, ComposeParallel and predict judge the
// same blocks, and the optimize rules, the result() catalog and its
// pre-phase, and the MaxComposeSlots preflight all come after it.
//
// Order of checks per fanned-out block, in declared order: the
// structural axis rules; the reference rule (ExpandItems' — every axis
// named, every placeholder an axis, none inside an objective,
// constraint or bound expression, PULSE_FOR_EACH_EXPR_SPLICE); the
// `name` must carry an axis placeholder (ReasonNameWithoutAxis). Then
// preflight, when non-nil, with OptimizeExpandedCount before anything
// renders; then each block renders once per combination and decodes
// strictly into types.OptimizeSpec.
//
// Without a for_each it returns c itself and no fan-outs, and never
// calls preflight. With one it returns a shallow clone whose Optimize is
// the expanded list (plain blocks copied by value) and one FanOut per
// fanned-out block, its Path `optimize[i]` as written. c is never
// modified.
func ExpandOptimize(c *types.ComposedRequest, opts Options, preflight func(expanded int) error) (*types.ComposedRequest, []FanOut, error) {
	if !OptimizeHasForEach(c) {
		return c, nil, nil
	}
	pending := map[string]pendingItem{}
	var fanOuts []FanOut
	for i := range c.Optimize {
		spec := c.Optimize[i].ForEach
		if spec == nil {
			continue
		}
		path := itemPath(listOptimize, i)
		if ce := itemRules(path).validateAxes(spec.Axes, spec.Mode); ce != nil {
			return nil, nil, ce
		}
		body, err := itemBody(&c.Optimize[i])
		if err != nil {
			return nil, nil, itemInternalFault(path, err)
		}
		if ce := checkItemReferences(path, listOptimize, spec, body, opts); ce != nil {
			return nil, nil, ce
		}
		if ce := checkOptimizeName(path, spec, body); ce != nil {
			return nil, nil, ce
		}
		if ce := checkOptimizeMarkers(path, body); ce != nil {
			return nil, nil, ce
		}
		pending[path] = pendingItem{path: path, spec: spec, body: body}
		mode := spec.Mode
		if mode == "" {
			mode = types.SweepModeGrid
		}
		fanOuts = append(fanOuts, FanOut{Path: path, Axes: spec.Axes, Mode: mode, Count: countAxes(spec.Axes, spec.Mode)})
	}
	if preflight != nil {
		if err := preflight(OptimizeExpandedCount(c)); err != nil {
			return nil, nil, err
		}
	}
	blocks, ce := expandValues(listOptimize, c.Optimize, pending)
	if ce != nil {
		return nil, nil, ce
	}
	out := *c
	out.Optimize = blocks
	return &out, fanOuts, nil
}

// checkOptimizeName refuses a fanned-out block whose `name` names no
// axis: validated alone with no variable declared, a name carrying a
// placeholder fails on it, one without passes.
func checkOptimizeName(path string, spec *types.ForEach, body json.RawMessage) *errors.CodedError {
	var probe struct {
		Name json.RawMessage `json:"name"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return itemInternalFault(path, err)
	}
	name := probe.Name
	if len(name) == 0 {
		name = json.RawMessage(`""`)
	}
	if template.Validate(&template.Template{Target: template.TargetRequest, Body: wrap("name", name)}) != nil {
		return nil
	}
	axes := make([]any, len(spec.Axes))
	for i, ax := range spec.Axes {
		axes[i] = ax.Name
	}
	return forEachInvalid(path+".name", ReasonNameWithoutAxis,
		"the optimize block at "+path+" carries a for_each, but its `name` names no axis, so every block it adds "+
			"would share one name; put an axis placeholder in the name, e.g. `\"budget_{{"+spec.Axes[0].Name+"}}\"`",
		map[string]any{"axes": axes})
}

// optimizeSpecType is the block type whose fields fix where a marker
// may stand (checkOptimizeMarkers).
var optimizeSpecType = reflect.TypeFor[types.OptimizeSpec]()

var rawMessageType = reflect.TypeFor[json.RawMessage]()

// checkOptimizeMarkers refuses a `{"$var"}` marker a fanned-out block's
// body carries outside the positions the payload schema and the MCP
// pulse_compose schema admit (ReasonMarkerPosition): a marker is a
// whole value of a non-list field of the block, or of a non-list field
// of one entry of a list of objects (variables, constraints, models),
// never a whole list, a list entry, nor anything inside a typed value;
// a free-form value (a model's `inline` body) admits one anywhere.
// The rule is the schemas' (descriptor varMarkerStruct, the MCP
// binder's allowVarMarkers) read off the same Go type. The first fault
// in key order is reported; a key the type does not know is left to
// the strict decode.
func checkOptimizeMarkers(path string, body json.RawMessage) *errors.CodedError {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return itemInternalFault(path, err)
	}
	if at, what := markerFault(optimizeSpecType, obj, path); at != "" {
		return forEachInvalid(at, ReasonMarkerPosition,
			"the optimize block at "+path+" carries a {\"$var\"} marker at "+at+", "+what+"; a marker stands only for one "+
				"whole value of the block or of one entry of its variables, constraints or models — write the list "+
				"and its entries out and put the marker on a value inside an entry (e.g. a bound or an `expr`)",
			map[string]any{})
	}
	return nil
}

// markerFault walks obj, an object of struct type t at path, and
// returns the path of the first misplaced marker and what stands there
// ("" when none).
func markerFault(t reflect.Type, obj map[string]json.RawMessage, path string) (string, string) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		f, ok := jsonField(t, k)
		if !ok || (t == optimizeSpecType && k == "for_each") {
			continue
		}
		at := path + "." + k
		v := obj[k]
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft == rawMessageType {
			// Free-form JSON (a model's inline body): both schemas type
			// it as any object, so a marker anywhere in it is admitted.
			continue
		}
		if ft.Kind() != reflect.Slice {
			if isMarker(v) {
				continue
			}
			if inner := findMarker(v, at); inner != "" {
				return inner, "inside the value of `" + k + "`"
			}
			continue
		}
		if isMarker(v) {
			return at, "standing for the whole `" + k + "` list"
		}
		var items []json.RawMessage
		if json.Unmarshal(v, &items) != nil {
			if inner := findMarker(v, at); inner != "" {
				return inner, "inside the value of `" + k + "`"
			}
			continue
		}
		el := ft.Elem()
		if el.Kind() == reflect.Pointer {
			el = el.Elem()
		}
		for i, item := range items {
			ip := at + "[" + strconv.Itoa(i) + "]"
			if isMarker(item) {
				return ip, "standing for a whole `" + k + "` entry"
			}
			var sub map[string]json.RawMessage
			if el.Kind() == reflect.Struct && json.Unmarshal(item, &sub) == nil {
				if fp, what := markerFault(el, sub, ip); fp != "" {
					return fp, what
				}
				continue
			}
			if inner := findMarker(item, ip); inner != "" {
				return inner, "inside a `" + k + "` entry"
			}
		}
	}
	return "", ""
}

// isMarker reports whether v is a lone `{"$var": …}` object.
func isMarker(v json.RawMessage) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(v, &m) != nil || len(m) != 1 {
		return false
	}
	_, ok := m["$var"]
	return ok
}

// findMarker returns the path of the first marker anywhere inside v
// (v itself included), "" when none; object keys in sorted order.
func findMarker(v json.RawMessage, path string) string {
	if isMarker(v) {
		return path
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(v, &m) == nil {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if p := findMarker(m[k], path+"."+k); p != "" {
				return p
			}
		}
		return ""
	}
	var a []json.RawMessage
	if json.Unmarshal(v, &a) == nil {
		for i, e := range a {
			if p := findMarker(e, path+"["+strconv.Itoa(i)+"]"); p != "" {
				return p
			}
		}
	}
	return ""
}

// jsonField is t's exported field encoding/json names key (the tag
// name, else the Go name).
func jsonField(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if name == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// RenderItemCombinations renders a raw item object carrying a
// `for_each` into one object per axis combination (grid: first axis
// slowest; zip: lockstep) — the substitution ExpandItems and
// ExpandOptimize run, without their reference rules, strict decode or
// limits — for a reader of the request as written (the request reads
// walker), which reports what every combination reads. ok is false —
// read the item as written — when the item carries no for_each, its
// for_each is structurally invalid, it expands to more than max
// combinations, or a combination does not render.
func RenderItemCombinations(item map[string]any, max int) ([]map[string]any, bool) {
	rawFE, has := item["for_each"]
	if !has {
		return nil, false
	}
	feJSON, err := json.Marshal(rawFE)
	if err != nil {
		return nil, false
	}
	var fe types.ForEach
	if json.Unmarshal(feJSON, &fe) != nil {
		return nil, false
	}
	if itemRules("").validateAxes(fe.Axes, fe.Mode) != nil {
		return nil, false
	}
	if n := countAxes(fe.Axes, fe.Mode); n > max {
		return nil, false
	}
	rest := make(map[string]any, len(item))
	for k, v := range item {
		if k != "for_each" {
			rest[k] = v
		}
	}
	body, err := json.Marshal(rest)
	if err != nil {
		return nil, false
	}
	var out []map[string]any
	ok := true
	forEachCombination(fe.Axes, fe.Mode, func(_ int, values map[string]any) bool {
		raw, err := render(body, variablesFor(fe.Axes, values), values)
		if err != nil {
			ok = false
			return false
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var obj map[string]any
		if dec.Decode(&obj) != nil || obj == nil {
			ok = false
			return false
		}
		out = append(out, obj)
		return true
	})
	if !ok {
		return nil, false
	}
	return out, true
}
