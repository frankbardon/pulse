package vectors

import (
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/types"
)

// post_window.go is the post-window attribute phase's one dependency
// classification (transformed-predictors E2-S1), read by the
// field-reference walk (predict and the runtime's entry check alike)
// and by the engine's ordering (processRecords, DeriveRecords), so the
// two can never disagree on which slots move.
//
// Attributes run before windows. An attribute that reads a window label
// — directly, or through the label of an earlier post-window attribute —
// is WINDOW-DEPENDENT. A window-dependent ROW-LOCAL attribute
// (PostWindowEligible: ATTR_FORMULA without total(), ATTR_REG_PREDICT)
// runs in the post-window phase when the windows run over the record
// rows (WindowsOverRecords): after the windows, over the filtered
// records the window labels it reads were copied onto, in declared
// order. Any other window-dependent attribute — a non-row-local type,
// or any type outside that shape — is PULSE_ATTRIBUTE_WINDOW_ORDER
// (AttributeWindowOrderRefusal). An attribute reading no window label
// stays where it always ran: a request with none moves nothing.

// PostWindow is a request's post-window attribute plan. The zero value
// moves nothing.
type PostWindow struct {
	// Post[i] reports whether req.Attributes[i] runs after the windows.
	// Nil when no slot does.
	Post []bool
	// Dep[i] is the first window-derived name req.Attributes[i] reads —
	// a window label or an earlier post-window attribute's label — ""
	// when it reads none. Nil when no slot reads one.
	Dep []string
	// WindowReads are the window labels the post-window attributes read,
	// in first-read order without repeats: the labels the engine copies
	// onto the records before the phase runs.
	WindowReads []string
	// Labels are the post-window attributes' output labels, in declared
	// order.
	Labels []string
}

// Any reports whether any attribute runs after the windows.
func (pw PostWindow) Any() bool { return len(pw.Labels) > 0 }

// IsPost reports whether req.Attributes[i] runs after the windows.
func (pw PostWindow) IsPost(i int) bool { return i < len(pw.Post) && pw.Post[i] }

// DepOf is the window-derived name req.Attributes[i] reads ("" none).
func (pw PostWindow) DepOf(i int) string {
	if i < len(pw.Dep) {
		return pw.Dep[i]
	}
	return ""
}

// Refusal returns the PULSE_ATTRIBUTE_WINDOW_ORDER refusal of the first
// window-dependent attribute that does not run after the windows, nil
// when every window-dependent slot moves.
func (pw PostWindow) Refusal(req *types.Request) *errors.CodedError {
	for i, dep := range pw.Dep {
		if dep == "" || pw.IsPost(i) {
			continue
		}
		return AttributeWindowOrderRefusal(req, req.Attributes[i], dep)
	}
	return nil
}

// PostWindowEligible reports whether an attribute of type route (the
// instance's route of attr.Type) may run after the windows: a row-local
// ATTR_FORMULA (no total(), which folds the whole filtered set first)
// or ATTR_REG_PREDICT.
func PostWindowEligible(route types.AttributeType, attr *types.Attribute) bool {
	switch route {
	case types.ATTR_REG_PREDICT:
		return true
	case types.ATTR_FORMULA:
		return attr != nil && !totalcall.Contains(attr.Expression)
	}
	return false
}

// PostWindowAttributes classifies req's attributes (route: the
// instance's view of a type, nil the identity; cat: the bound models an
// ATTR_REG_PREDICT or params.fit slot reads its terms from, nil none).
// A window label equal to a schema field or to a named vector of req is
// no window-derived name (the field wins and the walk refuses the
// shadowing window; the vector collision is
// PULSE_VECTOR_NAME_COLLISION's). Allocation-free —
// the zero plan — when req carries no window or no attribute.
func PostWindowAttributes(req *types.Request, schema *encoding.Schema, route func(types.AttributeType) types.AttributeType, cat model.Catalog) PostWindow {
	var pw PostWindow
	if req == nil || len(req.Windows) == 0 || len(req.Attributes) == 0 {
		return pw
	}
	derived := make(map[string]bool, len(req.Windows))
	windows := make(map[string]bool, len(req.Windows))
	for _, w := range req.Windows {
		if w == nil {
			continue
		}
		l := WindowLabel(w)
		if (schema != nil && schema.Field(l) != nil) || vectorNamed(req, l) {
			continue
		}
		derived[l], windows[l] = true, true
	}
	if len(derived) == 0 {
		return pw
	}
	overRecords := WindowsOverRecords(req)
	seenRead := map[string]bool{}
	for i, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		rt := routeOf(route, attr.Type)
		reads := attributeReads(attr, rt, cat)
		dep := ""
		for _, n := range reads {
			if derived[n] {
				dep = n
				break
			}
		}
		if dep == "" {
			continue
		}
		if pw.Dep == nil {
			pw.Dep = make([]string, len(req.Attributes))
		}
		pw.Dep[i] = dep
		if !overRecords || !PostWindowEligible(rt, attr) {
			continue
		}
		if pw.Post == nil {
			pw.Post = make([]bool, len(req.Attributes))
		}
		pw.Post[i] = true
		for _, n := range reads {
			if windows[n] && !seenRead[n] {
				seenRead[n] = true
				pw.WindowReads = append(pw.WindowReads, n)
			}
		}
		label := attr.Label
		if label == "" {
			label = DefaultAttributeLabel(attr)
		}
		pw.Labels = append(pw.Labels, label)
		derived[label] = true
	}
	return pw
}

// attributeReads lists the names attr (of type route) reads off a
// record, in a fixed order: an ATTR_FORMULA its field then its
// expression's free identifiers (exprident.Free); an ATTR_REG_PREDICT —
// or a params.fit ATTR_REG_FITTED / _RESIDUAL / _LEVERAGE — the bound
// model's terms (and a residual's target) when cat binds it; any other
// slot AttributeInputNames, sorted.
func attributeReads(attr *types.Attribute, route types.AttributeType, cat model.Catalog) []string {
	switch {
	case route == types.ATTR_FORMULA:
		var out []string
		if attr.Field != "" {
			out = append(out, attr.Field)
		}
		refs, _, _ := exprident.Free(attr.Expression)
		return append(out, refs...)
	case route == types.ATTR_REG_PREDICT:
		p, err := model.DecodeScoreParams(attr.Params)
		if err != nil {
			return nil
		}
		return boundTerms(cat, p.Fit, false)
	case model.FitReferenced(route, attr):
		name, _ := model.FitReference(route, attr)
		return boundTerms(cat, name, route == types.ATTR_REG_RESIDUAL)
	}
	names, _ := AttributeInputNames(attr)
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// boundTerms is the bound fit name's terms (plus its target when
// target), nil when cat does not bind it.
func boundTerms(cat model.Catalog, name string, target bool) []string {
	if cat == nil || name == "" {
		return nil
	}
	sig, ok := cat.Signature(name)
	if !ok {
		return nil
	}
	out := append([]string(nil), sig.Terms...)
	if target && sig.Target != "" {
		out = append(out, sig.Target)
	}
	return out
}

// vectorNamed reports whether name is a named vector of req.
func vectorNamed(req *types.Request, name string) bool {
	for _, v := range req.Vectors {
		if v.Name == name {
			return true
		}
	}
	return false
}

// routeOf applies route (nil: the identity).
func routeOf(route func(types.AttributeType) types.AttributeType, t types.AttributeType) types.AttributeType {
	if route == nil {
		return t
	}
	return route(t)
}

// AttributeWindowOrderRefusal is PULSE_ATTRIBUTE_WINDOW_ORDER for attr
// reading field, a window label of req or the label of
// a post-window attribute: the slot cannot run after the windows —
// it is not row-local, or the windows run over grouped / aggregated
// output rows (or beside a crosstab), where no record carries a window
// value. details {attribute, field}.
func AttributeWindowOrderRefusal(req *types.Request, attr *types.Attribute, field string) *errors.CodedError {
	var why string
	if !WindowsOverRecords(req) {
		why = "the windows run over the grouped or aggregated output rows, where no record carries a window value"
	} else {
		why = "only a row-local expression formula (no total()) or bound-model score runs after the windows"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_ATTRIBUTE_WINDOW_ORDER,
		"attribute "+string(attr.Type)+" reads window output "+field+", but "+why+"; materialize the window first",
		map[string]any{"attribute": string(attr.Type), "field": field})
}
