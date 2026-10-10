package sweep

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/template"
	"github.com/frankbardon/pulse/types"
)

// The reason values expansion adds to PULSE_SWEEP_INVALID.
const (
	// ReasonAxisUnreferenced: an axis is named by no placeholder in the
	// request body, the authored label pattern or the overlays — every
	// slot it adds would run the same request.
	ReasonAxisUnreferenced = "axis_unreferenced"
	// ReasonPlaceholderUnknown: a `{{name}}` token, `{"$var": "name"}`
	// marker or `$when` guard names no axis.
	ReasonPlaceholderUnknown = "placeholder_unknown"
	// ReasonPlaceholderInvalid: placeholder syntax the substitution
	// grammar refuses — an unterminated or empty `{{}}`, `{{` inside an
	// object key, a `$var` marker whose name is not a string, a `$when`
	// guard on the request root.
	ReasonPlaceholderInvalid = "placeholder_invalid"
	// ReasonRequestDecode: a substituted request body does not decode
	// strictly into a Request — an unknown key (named under
	// `unknown_field`) or a value of the wrong JSON type.
	ReasonRequestDecode = "request_decode"
	// ReasonRequestLabelSet: the request body sets `label`; a sweep
	// slot's label comes from the sweep `label` pattern.
	ReasonRequestLabelSet = "request_label_set"
	// ReasonOverlaysDecode: a substituted overlays array does not decode
	// into compose overlay specs.
	ReasonOverlaysDecode = "overlays_decode"
	// ReasonLabelEmpty: the label pattern rendered to the empty string
	// for a slot.
	ReasonLabelEmpty = "label_empty"
)

// Expansion is a ComposedRequest with its sweep expanded: the effective
// request every Compose surface runs, predicts and label-checks.
type Expansion struct {
	// Composed is the effective request. Without a sweep it is the input
	// pointer itself, untouched. With one it is a shallow clone whose
	// Requests are the explicit slots (the caller's pointers, in order)
	// followed by one slot per sweep combination in expansion order,
	// whose Overlays are the explicit specs followed by each sweep
	// slot's substituted `sweep.overlays` in expansion order, and whose
	// Sweep is nil — the effective request is sweep-free, so running or
	// echoing it never expands twice.
	Composed *types.ComposedRequest

	// Explicit is the number of explicit (hand-written) slots; the sweep
	// slots are Composed.Requests[Explicit:].
	Explicit int

	// Labels are the sweep slots' labels in expansion order (nil without
	// a sweep).
	Labels []string
}

// Count returns how many slots a structurally valid spec expands to:
// the product of the axis lengths under grid, the common axis length
// under zip, 0 for a nil spec or one without axes. A product past
// math.MaxInt saturates there, so a limit check on it never wraps.
func Count(spec *types.SweepSpec) int {
	if spec == nil || len(spec.Axes) == 0 {
		return 0
	}
	if spec.Mode == types.SweepModeZip {
		return len(spec.Axes[0].Values)
	}
	n := 1
	for _, ax := range spec.Axes {
		k := len(ax.Values)
		if k == 0 {
			return 0
		}
		if n > math.MaxInt/k {
			n = math.MaxInt
			continue
		}
		n *= k
	}
	return n
}

// Expand turns c into its effective request — the ONE expansion every
// Compose surface consumes (Compose and ComposeParallel in the runtime,
// PredictCompose, the overlay resolve, the EchoRequest echo), so their slot
// lists and label namespaces cannot drift.
//
// Order of checks: Validate; then the reference rule (every axis is
// named by a placeholder in the request body, the AUTHORED label pattern
// or the overlays — the default label does not count — and every
// placeholder names an axis); then preflight, when non-nil, with the
// total slot count (explicit + expanded) BEFORE anything is rendered,
// so an oversized product is refused without being built; then one
// substitution per combination through internal/template — `{{axis}}`
// renders the value's text exactly as written, `{"$var": "axis"}`
// splices it type-preserving — and a STRICT decode of each body into a
// Request. Faults are PULSE_SWEEP_INVALID (a preflight error is
// returned unchanged).
//
// Without a sweep Expand calls preflight with len(c.Requests) and
// returns c itself. Label collisions are NOT checked here: each
// collision site checks the effective slot list it is handed, explicit
// and sweep slots in one namespace.
func Expand(c *types.ComposedRequest, preflight func(slots int) error) (*Expansion, error) {
	if c == nil {
		return &Expansion{}, nil
	}
	spec := c.Sweep
	if spec == nil {
		if preflight != nil {
			if err := preflight(len(c.Requests)); err != nil {
				return nil, err
			}
		}
		return &Expansion{Composed: c, Explicit: len(c.Requests)}, nil
	}
	if ce := Validate(spec); ce != nil {
		return nil, ce
	}
	if ce := checkReferences(spec); ce != nil {
		return nil, ce
	}
	n := Count(spec)
	if preflight != nil {
		total := len(c.Requests)
		if n > math.MaxInt-total {
			total = math.MaxInt
		} else {
			total += n
		}
		if err := preflight(total); err != nil {
			return nil, err
		}
	}

	labelPattern := spec.Label
	if labelPattern == "" {
		labelPattern = defaultLabelPattern(spec.Axes)
	}
	labelBody, err := json.Marshal(map[string]string{"label": labelPattern})
	if err != nil {
		return nil, internalFault("sweep.label", err)
	}
	var overlaysBody json.RawMessage
	if ov := bytes.TrimSpace(spec.Overlays); len(ov) > 0 && !bytes.Equal(ov, []byte("null")) {
		overlaysBody = wrap("overlays", ov)
	}

	requests := make([]*types.Request, 0, len(c.Requests)+n)
	requests = append(requests, c.Requests...)
	overlays := append([]types.ComposeOverlaySpec(nil), c.Overlays...)
	labels := make([]string, 0, n)

	var ce *errors.CodedError
	forEachCombination(spec, func(slot int, values map[string]any) bool {
		vars := variablesFor(spec.Axes, values)
		req, fault := renderRequest(spec, vars, values, slot)
		if fault != nil {
			ce = fault
			return false
		}
		label, fault := renderLabel(labelBody, spec, vars, values, slot)
		if fault != nil {
			ce = fault
			return false
		}
		req.Label = label
		if overlaysBody != nil {
			specs, fault := renderOverlays(overlaysBody, vars, values, slot, label)
			if fault != nil {
				ce = fault
				return false
			}
			overlays = append(overlays, specs...)
		}
		requests = append(requests, req)
		labels = append(labels, label)
		return true
	})
	if ce != nil {
		return nil, ce
	}

	out := *c
	out.Requests = requests
	out.Overlays = overlays
	if len(overlays) == 0 {
		out.Overlays = c.Overlays
	}
	out.Sweep = nil
	return &Expansion{Composed: &out, Explicit: len(c.Requests), Labels: labels}, nil
}

// forEachCombination walks the spec's combinations in expansion order —
// grid row-major with the FIRST axis slowest (an odometer whose last
// axis turns fastest), zip in lockstep — handing each one's 0-based
// sweep-slot ordinal and axis → value map to visit until it returns
// false.
func forEachCombination(spec *types.SweepSpec, visit func(slot int, values map[string]any) bool) {
	axes := spec.Axes
	if spec.Mode == types.SweepModeZip {
		for i := range axes[0].Values {
			values := make(map[string]any, len(axes))
			for _, ax := range axes {
				values[ax.Name] = ax.Values[i]
			}
			if !visit(i, values) {
				return
			}
		}
		return
	}
	idx := make([]int, len(axes))
	for slot := 0; ; slot++ {
		values := make(map[string]any, len(axes))
		for a, ax := range axes {
			values[ax.Name] = ax.Values[idx[a]]
		}
		if !visit(slot, values) {
			return
		}
		a := len(axes) - 1
		for ; a >= 0; a-- {
			idx[a]++
			if idx[a] < len(axes[a].Values) {
				break
			}
			idx[a] = 0
		}
		if a < 0 {
			return
		}
	}
}

// defaultLabelPattern is the label pattern a sweep without `label`
// takes: `<axis>={{<axis>}}` per axis in declared order, joined by `_`.
func defaultLabelPattern(axes []types.SweepAxis) string {
	parts := make([]string, len(axes))
	for i, ax := range axes {
		parts[i] = ax.Name + "={{" + ax.Name + "}}"
	}
	return strings.Join(parts, "_")
}

// variablesFor declares one template variable per axis, typed from the
// value it takes in this slot (an axis may mix kinds, so the type is
// per slot): boolean, string, or number for every numeric kind.
func variablesFor(axes []types.SweepAxis, values map[string]any) []*template.Variable {
	vars := make([]*template.Variable, len(axes))
	for i, ax := range axes {
		vars[i] = &template.Variable{Name: ax.Name, Type: varType(values[ax.Name])}
	}
	return vars
}

func varType(v any) template.VarType {
	switch v.(type) {
	case bool:
		return template.VarBoolean
	case string:
		return template.VarString
	default:
		return template.VarNumber
	}
}

// render substitutes values into body through an in-memory template.
func render(body json.RawMessage, vars []*template.Variable, values map[string]any) (json.RawMessage, error) {
	t := &template.Template{Target: template.TargetRequest, Variables: vars, Body: body}
	return template.RenderJSON(t, values)
}

// renderRequest renders the request body for one combination and
// decodes it strictly.
func renderRequest(spec *types.SweepSpec, vars []*template.Variable, values map[string]any, slot int) (*types.Request, *errors.CodedError) {
	raw, err := render(spec.Request, vars, values)
	if err != nil {
		return nil, templateFault("sweep.request", "request", err, values, slot)
	}
	req := new(types.Request)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		details := slotDetails(values, slot)
		msg := "sweep slot " + strconv.Itoa(slot) + " " + describeValues(spec.Axes, values) +
			": the substituted request body does not decode into a Request: " + err.Error()
		if field, ok := unknownField(err); ok {
			details["unknown_field"] = field
			msg = "sweep slot " + strconv.Itoa(slot) + " " + describeValues(spec.Axes, values) +
				": the substituted request body carries the unknown field " + strconv.Quote(field) +
				". A sweep body is decoded strictly, so a misspelled key fails here rather than silently " +
				"dropping a slot; correct or remove it"
		}
		ce := invalidWith("sweep.request", ReasonRequestDecode, msg, details)
		ce.Cause = err
		return nil, ce
	}
	if req.Label != "" {
		return nil, invalidWith("sweep.request.label", ReasonRequestLabelSet,
			"sweep request body sets `label`; a sweep slot is labelled by the sweep `label` pattern "+
				"(default `<axis>=<value>` joined by `_`) — move the label into sweep.label",
			map[string]any{})
	}
	return req, nil
}

// renderLabel renders the label pattern for one combination.
func renderLabel(labelBody json.RawMessage, spec *types.SweepSpec, vars []*template.Variable, values map[string]any, slot int) (string, *errors.CodedError) {
	raw, err := render(labelBody, vars, values)
	if err != nil {
		return "", templateFault("sweep.label", "label", err, values, slot)
	}
	var out struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", internalFault("sweep.label", err)
	}
	if out.Label == "" {
		return "", invalidWith("sweep.label", ReasonLabelEmpty,
			"sweep label pattern renders to an empty label for slot "+strconv.Itoa(slot)+" "+
				describeValues(spec.Axes, values)+"; every sweep slot needs a non-empty label",
			slotDetails(values, slot))
	}
	return out.Label, nil
}

// renderOverlays renders the sweep overlays for one combination.
func renderOverlays(body json.RawMessage, vars []*template.Variable, values map[string]any, slot int, label string) ([]types.ComposeOverlaySpec, *errors.CodedError) {
	raw, err := render(body, vars, values)
	if err != nil {
		return nil, templateFault("sweep.overlays", "overlays", err, values, slot)
	}
	var out struct {
		Overlays []types.ComposeOverlaySpec `json:"overlays"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		details := slotDetails(values, slot)
		details["label"] = label
		ce := invalidWith("sweep.overlays", ReasonOverlaysDecode,
			"sweep overlays for slot "+strconv.Quote(label)+" do not decode into compose overlay specs: "+err.Error(),
			details)
		ce.Cause = err
		return nil, ce
	}
	return out.Overlays, nil
}

// checkReferences enforces the reference rule over one in-memory
// template whose body carries the request, the authored label and the
// overlays side by side, so the placeholder grammar applied is exactly
// the substitution's: every placeholder must name an axis (validation
// with every axis declared), and every axis must be named somewhere
// (validation with that axis alone undeclared must fail on it).
func checkReferences(spec *types.SweepSpec) *errors.CodedError {
	body := referenceBody(spec)
	all := make([]*template.Variable, len(spec.Axes))
	for i, ax := range spec.Axes {
		all[i] = &template.Variable{Name: ax.Name, Type: template.VarString}
	}
	if err := template.Validate(&template.Template{Target: template.TargetRequest, Variables: all, Body: body}); err != nil {
		return referenceFault(err)
	}
	for i, ax := range spec.Axes {
		without := make([]*template.Variable, 0, len(all)-1)
		without = append(without, all[:i]...)
		without = append(without, all[i+1:]...)
		if template.Validate(&template.Template{Target: template.TargetRequest, Variables: without, Body: body}) == nil {
			at := axisPath(i)
			return invalid(at+".name", ax.Name, ReasonAxisUnreferenced,
				"sweep axis "+describe(at, ax.Name)+" is referenced nowhere — no `{{"+ax.Name+"}}` or "+
					"`{\"$var\": \""+ax.Name+"\"}` in the request, the label pattern or the overlays — so "+
					"every slot it adds would run the same request; reference it or drop the axis",
				nil)
		}
	}
	return nil
}

// referenceBody is the scan document: {"request", "label"?, "overlays"?}.
func referenceBody(spec *types.SweepSpec) json.RawMessage {
	var b bytes.Buffer
	b.WriteString(`{"request":`)
	b.Write(bytes.TrimSpace(spec.Request))
	if spec.Label != "" {
		lbl, _ := json.Marshal(spec.Label)
		b.WriteString(`,"label":`)
		b.Write(lbl)
	}
	if ov := bytes.TrimSpace(spec.Overlays); len(ov) > 0 && !bytes.Equal(ov, []byte("null")) {
		b.WriteString(`,"overlays":`)
		b.Write(ov)
	}
	b.WriteString("}")
	return b.Bytes()
}

// referenceFault maps a template validation fault on the scan document
// to PULSE_SWEEP_INVALID: one naming a variable is a placeholder naming
// no axis; any other is placeholder syntax the grammar refuses.
func referenceFault(err error) *errors.CodedError {
	var tce *errors.CodedError
	if !stderrors.As(err, &tce) {
		return internalFault("sweep", err)
	}
	path, _ := tce.Details["path"].(string)
	field := scanPath(path)
	name, _ := tce.Details[errors.DetailVariable].(string)
	var ce *errors.CodedError
	if name != "" {
		ce = invalidWith(field, ReasonPlaceholderUnknown,
			"sweep placeholder "+strconv.Quote(name)+" at "+field+" names no axis; declare an axis "+
				strconv.Quote(name)+" or correct the placeholder",
			map[string]any{"placeholder": name})
	} else {
		ce = invalidWith(field, ReasonPlaceholderInvalid,
			"sweep placeholder syntax at "+field+" is not valid: "+tce.Message, map[string]any{})
	}
	ce.Cause = err
	return ce
}

// scanPath maps a reference-scan body path (`body.request.x`) to its
// sweep-rooted path (`sweep.request.x`).
func scanPath(path string) string {
	if rest, ok := strings.CutPrefix(path, "body"); ok && rest != "" {
		return "sweep" + rest
	}
	return "sweep"
}

// templateFault maps a per-slot render fault. The reference scan has
// already passed, so it is placeholder syntax the render walk alone
// refuses (a `$when` guard on the request root).
func templateFault(field, part string, err error, values map[string]any, slot int) *errors.CodedError {
	msg := err.Error()
	var tce *errors.CodedError
	if stderrors.As(err, &tce) {
		msg = tce.Message
		if path, ok := tce.Details["path"].(string); ok {
			if rest, ok := strings.CutPrefix(path, "body"); ok {
				field += rest
				if part == "label" || part == "overlays" {
					field = "sweep" + rest
				}
			}
		}
	}
	ce := invalidWith(field, ReasonPlaceholderInvalid,
		"sweep "+part+" for slot "+strconv.Itoa(slot)+" does not substitute: "+msg, slotDetails(values, slot))
	ce.Cause = err
	return ce
}

func internalFault(field string, err error) *errors.CodedError {
	ce := invalidWith(field, ReasonPlaceholderInvalid, "sweep "+field+" could not be expanded: "+err.Error(), map[string]any{})
	ce.Cause = err
	return ce
}

func invalidWith(field, reason, msg string, details map[string]any) *errors.CodedError {
	details["field"] = field
	details["reason"] = reason
	return errors.NewCodedErrorWithDetails(errors.PULSE_SWEEP_INVALID, msg, details)
}

// slotDetails names the faulting sweep slot: its 0-based ordinal and
// its axis values.
func slotDetails(values map[string]any, slot int) map[string]any {
	vals := make(map[string]any, len(values))
	for k, v := range values {
		vals[k] = v
	}
	return map[string]any{"slot": slot, "values": vals}
}

// describeValues renders a slot's axis values for error prose, in axis
// order: (tv=10, search=x).
func describeValues(axes []types.SweepAxis, values map[string]any) string {
	parts := make([]string, len(axes))
	for i, ax := range axes {
		b, _ := json.Marshal(values[ax.Name])
		parts[i] = ax.Name + "=" + string(b)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func wrap(key string, raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteString(`{"`)
	b.WriteString(key)
	b.WriteString(`":`)
	b.Write(raw)
	b.WriteString("}")
	return b.Bytes()
}

// unknownField extracts the key from encoding/json's
// DisallowUnknownFields fault.
func unknownField(err error) (string, bool) {
	const prefix = "json: unknown field "
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	quoted := strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	if name, uerr := strconv.Unquote(quoted); uerr == nil {
		return name, true
	}
	return quoted, true
}
