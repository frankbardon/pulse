package sweep

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/internal/template"
	"github.com/frankbardon/pulse/types"
)

// The reason values item expansion adds to PULSE_FOR_EACH_INVALID, beside
// the structural axis reasons (ReasonAxesEmpty … ReasonZipLengthMismatch)
// and the placeholder reasons (ReasonAxisUnreferenced,
// ReasonPlaceholderUnknown, ReasonPlaceholderInvalid) it shares with the
// compose sweep.
const (
	// ReasonItemDecode: a substituted item does not decode into its slot
	// type — a value of the wrong JSON type. An unknown key is
	// PULSE_REQUEST_UNKNOWN_FIELD (details.path from the item).
	ReasonItemDecode = "item_decode"
	// ReasonNestedInSweep: a compose sweep body carries an item with a
	// `for_each`. A sweep body is substituted once per slot; a second
	// fan-out inside it is refused rather than expanded twice.
	ReasonNestedInSweep = "nested_in_sweep"
)

// The fanned-out item lists, in pipeline order (features run first,
// then attributes, then windows) — the order expansion checks, counts
// and reports them.
const (
	listFeatures   = "features"
	listAttributes = "attributes"
	listWindows    = "windows"
)

// FanOut describes one for_each item: where it sits, its axes and the
// number of items it expands to.
type FanOut struct {
	// Path is the item's request-rooted path, e.g. `windows[0]`.
	Path string
	// Axes are the item's axes in declared order.
	Axes []types.SweepAxis
	// Mode is the resolved mode: grid (the default) or zip.
	Mode types.SweepMode
	// Count is the number of items the for_each expands to.
	Count int
}

// itemRules is the for_each axis rule set rooted at one item.
func itemRules(path string) axisRules {
	return axisRules{root: path + ".for_each", noun: "for_each", code: errors.PULSE_FOR_EACH_INVALID}
}

// DerivedCount is the number of window, feature and attribute items req
// carries once every for_each is expanded: one per plain item, the axis
// product (grid) or common length (zip) per for_each item. It saturates
// at math.MaxInt. Structure is not checked; ExpandItems checks it before
// it calls its preflight with this figure.
func DerivedCount(req *types.Request) int {
	if req == nil {
		return 0
	}
	n := 0
	for _, f := range req.Features {
		n = addSaturating(n, itemCount(forEachOf(f)))
	}
	for _, a := range req.Attributes {
		n = addSaturating(n, itemCount(forEachOf(a)))
	}
	for _, w := range req.Windows {
		n = addSaturating(n, itemCount(forEachOf(w)))
	}
	return n
}

func addSaturating(n, k int) int {
	if k > math.MaxInt-n {
		return math.MaxInt
	}
	return n + k
}

func itemCount(fe *types.ForEach) int {
	if fe == nil {
		return 1
	}
	return countAxes(fe.Axes, fe.Mode)
}

// HasForEach reports whether any window, feature or attribute item of
// req carries a for_each.
func HasForEach(req *types.Request) bool {
	return forEachPath(req) != ""
}

// forEachPath is the path of the first item carrying a for_each, in
// pipeline order, or "".
func forEachPath(req *types.Request) string {
	if req == nil {
		return ""
	}
	for i, f := range req.Features {
		if forEachOf(f) != nil {
			return itemPath(listFeatures, i)
		}
	}
	for i, a := range req.Attributes {
		if forEachOf(a) != nil {
			return itemPath(listAttributes, i)
		}
	}
	for i, w := range req.Windows {
		if forEachOf(w) != nil {
			return itemPath(listWindows, i)
		}
	}
	return ""
}

func forEachOf(item any) *types.ForEach {
	switch v := item.(type) {
	case *types.Feature:
		if v != nil {
			return v.ForEach
		}
	case *types.Attribute:
		if v != nil {
			return v.ForEach
		}
	case *types.Window:
		if v != nil {
			return v.ForEach
		}
	}
	return nil
}

func itemPath(list string, i int) string { return list + "[" + strconv.Itoa(i) + "]" }

// pendingItem is one for_each item that passed the structural and
// reference checks, waiting to render.
type pendingItem struct {
	path string
	spec *types.ForEach
	body json.RawMessage
}

// ExpandItems is the ONE for_each expansion every surface runs —
// Process (and through it every Compose slot), ProcessChain stages,
// Materialize, and predict — so the item lists they judge and run
// cannot drift.
//
// Order of checks: every for_each item, in pipeline order (features,
// attributes, windows) and declared order within a list, is checked
// structurally (the compose sweep's axis rules) and by the reference
// rule (every axis named by a placeholder in the item, every
// placeholder naming an axis, none inside an expression slot —
// PULSE_FOR_EACH_EXPR_SPLICE); then preflight, when non-nil, with
// DerivedCount — plain items plus expanded ones — BEFORE anything is
// rendered, so an oversized product is refused without being built;
// then each for_each item renders once per combination (grid: first axis
// slowest; zip: lockstep) through internal/template — `{{axis}}`
// renders the value's text exactly as written, `{"$var": "axis"}`
// splices it type-preserving — and each rendered item is decoded
// STRICTLY into its slot type. Faults are PULSE_FOR_EACH_INVALID (a
// preflight error is returned unchanged).
//
// Without a for_each ExpandItems calls preflight and returns req itself
// and no fan-outs. With one it returns a shallow clone whose Features,
// Attributes and Windows are the expanded lists — each for_each item
// replaced in place by its rendered items, plain items kept (the
// caller's pointers) — and one FanOut per for_each item. req is never
// modified.
func ExpandItems(req *types.Request, preflight func(derived int) error) (*types.Request, []FanOut, error) {
	if req == nil {
		return nil, nil, nil
	}
	var pending []pendingItem
	var fanOuts []FanOut
	check := func(list string, i int, item any) *errors.CodedError {
		spec := forEachOf(item)
		if spec == nil {
			return nil
		}
		path := itemPath(list, i)
		if ce := itemRules(path).validateAxes(spec.Axes, spec.Mode); ce != nil {
			return ce
		}
		body, err := itemBody(item)
		if err != nil {
			return itemInternalFault(path, err)
		}
		if ce := checkItemReferences(path, list, spec, body); ce != nil {
			return ce
		}
		pending = append(pending, pendingItem{path: path, spec: spec, body: body})
		mode := spec.Mode
		if mode == "" {
			mode = types.SweepModeGrid
		}
		fanOuts = append(fanOuts, FanOut{Path: path, Axes: spec.Axes, Mode: mode, Count: countAxes(spec.Axes, spec.Mode)})
		return nil
	}
	for i, f := range req.Features {
		if ce := check(listFeatures, i, f); ce != nil {
			return nil, nil, ce
		}
	}
	for i, a := range req.Attributes {
		if ce := check(listAttributes, i, a); ce != nil {
			return nil, nil, ce
		}
	}
	for i, w := range req.Windows {
		if ce := check(listWindows, i, w); ce != nil {
			return nil, nil, ce
		}
	}
	if preflight != nil {
		if err := preflight(DerivedCount(req)); err != nil {
			return nil, nil, err
		}
	}
	if len(pending) == 0 {
		return req, nil, nil
	}

	bodies := make(map[string]pendingItem, len(pending))
	for _, p := range pending {
		bodies[p.path] = p
	}
	out := *req
	var err *errors.CodedError
	if out.Features, err = expandList(listFeatures, req.Features, bodies); err != nil {
		return nil, nil, err
	}
	if out.Attributes, err = expandList(listAttributes, req.Attributes, bodies); err != nil {
		return nil, nil, err
	}
	if out.Windows, err = expandList(listWindows, req.Windows, bodies); err != nil {
		return nil, nil, err
	}
	return &out, fanOuts, nil
}

// expandList rebuilds one item list: plain items kept, each pending
// for_each item replaced by its rendered items. A list without a
// for_each item comes back as the input slice.
func expandList[T any](list string, items []*T, pending map[string]pendingItem) ([]*T, *errors.CodedError) {
	found := false
	for i := range items {
		if _, ok := pending[itemPath(list, i)]; ok {
			found = true
			break
		}
	}
	if !found {
		return items, nil
	}
	out := make([]*T, 0, len(items))
	for i, item := range items {
		p, ok := pending[itemPath(list, i)]
		if !ok {
			out = append(out, item)
			continue
		}
		var ce *errors.CodedError
		forEachCombination(p.spec.Axes, p.spec.Mode, func(idx int, values map[string]any) bool {
			rendered, fault := renderItem[T](p, values, idx)
			if fault != nil {
				ce = fault
				return false
			}
			out = append(out, rendered)
			return true
		})
		if ce != nil {
			return nil, ce
		}
	}
	return out, nil
}

// renderItem renders one combination of a for_each item and decodes it
// strictly into a fresh T.
func renderItem[T any](p pendingItem, values map[string]any, idx int) (*T, *errors.CodedError) {
	vars := variablesFor(p.spec.Axes, values)
	raw, err := render(p.body, vars, values)
	if err != nil {
		return nil, itemTemplateFault(p.path, err, p.spec.Axes, values, idx)
	}
	prefix := "for_each on " + p.path + " combination " + strconv.Itoa(idx) + " " + describeValues(p.spec.Axes, values) + ": "
	// The shared strict decoder: an unknown key is
	// PULSE_REQUEST_UNKNOWN_FIELD, its details.path rooted at the item
	// (windows[3].bogus), the combination in details.index / values.
	if u := strictjson.Check(raw, reflect.TypeFor[T](), strictjson.Options{}); u != nil {
		objectPath := p.path
		if u.FullPath != "" {
			objectPath += "." + u.FullPath
		}
		ce := strictjson.UnknownFieldError(objectPath, u.Keys, u.Valid)
		for k, v := range combinationDetails(values, idx) {
			ce.Details[k] = v
		}
		ce.Message = prefix + ce.Message
		return nil, ce
	}
	item := new(T)
	if err := strictjson.DecodeInto(raw, item, strictjson.Options{}); err != nil {
		details := combinationDetails(values, idx)
		msg := prefix + "the substituted item does not decode: " + err.Error()
		ce := forEachInvalid(p.path, ReasonItemDecode, msg, details)
		ce.Cause = err
		return nil, ce
	}
	return item, nil
}

// itemBody is the item's JSON with its for_each removed: the template
// each combination renders.
func itemBody(item any) (json.RawMessage, error) {
	switch v := item.(type) {
	case *types.Feature:
		c := *v
		c.ForEach = nil
		return json.Marshal(&c)
	case *types.Attribute:
		c := *v
		c.ForEach = nil
		return json.Marshal(&c)
	case *types.Window:
		c := *v
		c.ForEach = nil
		return json.Marshal(&c)
	}
	return nil, stderrors.New("unsupported item type")
}

// checkItemReferences is the reference rule over one item body: every
// placeholder names an axis (validation with every axis declared), and
// every axis is named somewhere (validation with that axis alone
// undeclared must fail on it). A placeholder inside an expression slot
// is PULSE_FOR_EACH_EXPR_SPLICE, checked first: that scan reads the item
// nested under its list key ({"<list>": [item]}), so the
// expression-slot list (internal/exprslots) matches the key paths it
// would from a request root — a feature's params.fit_on, not an
// attribute's. Its other faults are left to the item-rooted scan.
func checkItemReferences(path, list string, spec *types.ForEach, body json.RawMessage) *errors.CodedError {
	all := make([]*template.Variable, len(spec.Axes))
	for i, ax := range spec.Axes {
		all[i] = &template.Variable{Name: ax.Name, Type: template.VarString}
	}
	nested := wrap(list, append(append([]byte("["), body...), ']'))
	if err := template.Validate(&template.Template{Target: template.TargetRequest, Variables: all, Body: nested}); err != nil {
		var tce *errors.CodedError
		if stderrors.As(err, &tce) && tce.Code == errors.PULSE_TEMPLATE_EXPR_SPLICE {
			return itemReferenceFault(path, list, err)
		}
	}
	if err := template.Validate(&template.Template{Target: template.TargetRequest, Variables: all, Body: body}); err != nil {
		return itemReferenceFault(path, list, err)
	}
	rules := itemRules(path)
	for i, ax := range spec.Axes {
		without := make([]*template.Variable, 0, len(all)-1)
		without = append(without, all[:i]...)
		without = append(without, all[i+1:]...)
		if template.Validate(&template.Template{Target: template.TargetRequest, Variables: without, Body: body}) == nil {
			at := rules.axisPath(i)
			return rules.invalid(at+".name", ax.Name, ReasonAxisUnreferenced,
				"for_each axis "+describe(at, ax.Name)+" is referenced nowhere in the item — no `{{"+ax.Name+"}}` or "+
					"`{\"$var\": \""+ax.Name+"\"}` — so every item it adds would be identical; reference it or drop the axis",
				nil)
		}
	}
	return nil
}

// itemReferenceFault maps a template validation fault on an item body
// to PULSE_FOR_EACH_INVALID.
func itemReferenceFault(path, list string, err error) *errors.CodedError {
	var tce *errors.CodedError
	if !stderrors.As(err, &tce) {
		return itemInternalFault(path, err)
	}
	bodyPath, _ := tce.Details["path"].(string)
	// The expression scan nests the item at <list>[0].
	if rest, ok := strings.CutPrefix(bodyPath, "body."+list+"[0]"); ok {
		bodyPath = "body" + rest
	}
	field := itemFieldPath(path, bodyPath)
	name, _ := tce.Details[errors.DetailVariable].(string)
	var ce *errors.CodedError
	if tce.Code == errors.PULSE_TEMPLATE_EXPR_SPLICE {
		slot, _ := tce.Details["slot"].(string)
		ce = exprSplice(errors.PULSE_FOR_EACH_EXPR_SPLICE, "for_each", field, name, slot,
			"vary the value with a Compose sweep instead (its request carries `\"params\": {"+strconv.Quote(name)+
				": {\"$var\": "+strconv.Quote(name)+"}}` and the expression reads `params."+name+"`), or, for an "+
				"expression inside params, supply it whole with a `{\"$var\": "+strconv.Quote(name)+"}` axis marker "+
				"whose values are the complete expressions")
	} else if name != "" {
		ce = forEachInvalid(field, ReasonPlaceholderUnknown,
			"for_each placeholder "+strconv.Quote(name)+" at "+field+" names no axis; declare an axis "+
				strconv.Quote(name)+" in "+path+".for_each or correct the placeholder",
			map[string]any{"placeholder": name})
	} else {
		ce = forEachInvalid(field, ReasonPlaceholderInvalid,
			"for_each placeholder syntax at "+field+" is not valid: "+tce.Message, map[string]any{})
	}
	ce.Cause = err
	return ce
}

// itemTemplateFault maps a per-combination render fault: placeholder
// syntax only the render walk refuses.
func itemTemplateFault(path string, err error, axes []types.SweepAxis, values map[string]any, idx int) *errors.CodedError {
	msg := err.Error()
	field := path
	var tce *errors.CodedError
	if stderrors.As(err, &tce) {
		msg = tce.Message
		if bp, ok := tce.Details["path"].(string); ok {
			field = itemFieldPath(path, bp)
		}
	}
	ce := forEachInvalid(field, ReasonPlaceholderInvalid,
		"for_each on "+path+" combination "+strconv.Itoa(idx)+" "+describeValues(axes, values)+
			" does not substitute: "+msg, combinationDetails(values, idx))
	ce.Cause = err
	return ce
}

// itemFieldPath maps a template body path (`body.params.decay`) to the
// request-rooted item path (`windows[0].params.decay`).
func itemFieldPath(path, bodyPath string) string {
	if rest, ok := strings.CutPrefix(bodyPath, "body"); ok {
		return path + rest
	}
	return path
}

func itemInternalFault(path string, err error) *errors.CodedError {
	ce := forEachInvalid(path, ReasonPlaceholderInvalid, "for_each on "+path+" could not be expanded: "+err.Error(), map[string]any{})
	ce.Cause = err
	return ce
}

func forEachInvalid(field, reason, msg string, details map[string]any) *errors.CodedError {
	details["field"] = field
	details["reason"] = reason
	return errors.NewCodedErrorWithDetails(errors.PULSE_FOR_EACH_INVALID, msg, details)
}

// combinationDetails names the faulting combination: its 0-based
// ordinal and its axis values.
func combinationDetails(values map[string]any, idx int) map[string]any {
	vals := make(map[string]any, len(values))
	for k, v := range values {
		vals[k] = v
	}
	return map[string]any{"index": idx, "values": vals}
}

// nestedForEachRefusal refuses a compose sweep body carrying an item
// with a `for_each` (reason nested_in_sweep), read off the raw body so
// it fires before the sweep's own placeholder scan would trip on the
// item's axis names.
func nestedForEachRefusal(body json.RawMessage) *errors.CodedError {
	var probe struct {
		Features   []map[string]json.RawMessage `json:"features"`
		Attributes []map[string]json.RawMessage `json:"attributes"`
		Windows    []map[string]json.RawMessage `json:"windows"`
	}
	if json.Unmarshal(body, &probe) != nil {
		// Left to the strict body decode, which names the fault.
		return nil
	}
	for _, l := range []struct {
		name  string
		items []map[string]json.RawMessage
	}{{listFeatures, probe.Features}, {listAttributes, probe.Attributes}, {listWindows, probe.Windows}} {
		for i, item := range l.items {
			if _, ok := item["for_each"]; ok {
				path := itemPath(l.name, i)
				return forEachInvalid("sweep.request."+path+".for_each", ReasonNestedInSweep,
					"the compose sweep body item "+path+" carries a for_each; a sweep body is substituted once per slot "+
						"and cannot fan an item out a second time — add the item's axes to the sweep, or move the item "+
						"to an explicit request",
					map[string]any{})
			}
		}
	}
	return nil
}
