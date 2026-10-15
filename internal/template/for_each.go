package template

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// forEachKey is the request-item key that fans a window, feature or
// attribute item out over parameter axes (types.ForEach). The item's own
// expansion — run after the template renders, by the request surfaces —
// resolves `{{axis}}` and `{"$var": "axis"}` against that item's axes, so
// the template walk must leave them alone.
//
// The rule is structural, read off the JSON tree, never off the request
// types: an object carrying a `for_each` object whose `axes` array lists
// `{"name": …}` entries scopes those names over every OTHER key of the
// object. Inside that scope a `{{name}}` token or a `{"$var": "name"}`
// marker naming one of the axes passes through untouched; everything else
// — template variables beside the axis placeholders, and the whole
// `for_each` value itself (so a template variable can supply an axis's
// `values` list) — substitutes as anywhere else in the body. Keeping the
// rule here keeps the package inside its import ceiling
// (TestTemplatePackage_ImportBoundary): the expansion package imports this
// one, never the reverse.
//
// A Compose `sweep` (types.SweepSpec) follows the same rule one level
// down: an object carrying a `sweep` object whose `axes` array lists
// `{"name": …}` entries scopes those names over the sweep's `request`,
// `overlays` and `label` — the parts the sweep's own expansion
// substitutes per slot. Every other sweep key (`axes`, `mode`, `rank`)
// renders in the enclosing scope, so a template variable can supply an
// axis's `values` exactly as it can a for_each axis's.
const (
	forEachKey  = "for_each"
	forEachAxes = "axes"
	forEachName = "name"

	sweepKey = "sweep"
)

// sweepScopedKeys are the keys of a `sweep` object its axes scope over.
var sweepScopedKeys = map[string]struct{}{"request": {}, "overlays": {}, "label": {}}

// sweepScoped reports whether key, a key of a `sweep` object, walks in
// that sweep's axis scope.
func sweepScoped(key string) bool {
	_, ok := sweepScopedKeys[key]
	return ok
}

// axisScope is the set of for_each axis names in force at a body node.
// The zero value (nil) is the empty scope: outside every for_each item.
type axisScope map[string]struct{}

// has reports whether name is an axis of an enclosing for_each item.
func (s axisScope) has(name string) bool {
	_, ok := s[name]
	return ok
}

// with returns the scope extended by axes. A node without a for_each
// keeps its parent's scope unchanged (no allocation).
func (s axisScope) with(axes []itemAxis) axisScope {
	if len(axes) == 0 {
		return s
	}
	out := make(axisScope, len(s)+len(axes))
	for k := range s {
		out[k] = struct{}{}
	}
	for _, a := range axes {
		out[a.name] = struct{}{}
	}
	return out
}

// itemAxis is one literal axis name a for_each or sweep declares, the
// declaring key (forEachKey or sweepKey), and the body path of its
// `name` key.
type itemAxis struct {
	name, owner, path string
}

// itemAxes reads the axis names obj's `for_each` declares. An object
// without a `for_each` object, or whose `axes` is not an array, declares
// none — its shape is the request decode's to judge, not the template's.
//
// An axis name must be LITERAL: it decides which placeholders the walk
// leaves alone, so it has to be known at declaration time. A name
// written as template syntax — a `{{token}}` string or a `$var` marker —
// is a template author error, PULSE_TEMPLATE_INVALID. A name of any other
// non-string kind is skipped and left to the strict decode.
func itemAxes(t *Template, obj map[string]any, path string) ([]itemAxis, error) {
	return declaredAxes(t, obj, path, forEachKey)
}

// sweepAxes reads the axis names obj's `sweep` declares, under the same
// rules as itemAxes. They scope over the sweep object's sweepScopedKeys
// only, never over obj's other keys.
func sweepAxes(t *Template, obj map[string]any, path string) ([]itemAxis, error) {
	return declaredAxes(t, obj, path, sweepKey)
}

// declaredAxes reads the literal axis names under obj[owner].axes.
func declaredAxes(t *Template, obj map[string]any, path, owner string) ([]itemAxis, error) {
	holder, ok := obj[owner].(map[string]any)
	if !ok {
		return nil, nil
	}
	list, ok := holder[forEachAxes].([]any)
	if !ok {
		return nil, nil
	}
	var out []itemAxis
	for i, el := range list {
		ax, ok := el.(map[string]any)
		if !ok {
			continue
		}
		at := childPath(indexPath(childPath(childPath(path, owner), forEachAxes), i), forEachName)
		switch name := ax[forEachName].(type) {
		case string:
			if strings.Contains(name, interpOpen) {
				return nil, nonLiteralAxis(t, at, owner)
			}
			out = append(out, itemAxis{name: strings.TrimSpace(name), owner: owner, path: at})
		case map[string]any:
			if isLoneMarkerKey(name) {
				return nil, nonLiteralAxis(t, at, owner)
			}
		}
	}
	return out, nil
}

// nonLiteralAxis is the fault for an axis name written as template syntax.
func nonLiteralAxis(t *Template, path, owner string) error {
	return invalidAt(t, path, "", "template body at "+path+" spells a "+owner+"."+forEachAxes+
		" name with template syntax; axis names must be literal, because the renderer leaves the "+
		"`{{axis}}` and {\""+markerKey+"\": \"axis\"} placeholders for the "+owner+" expansion and must know them "+
		"before any variable resolves — write the name literally (a template variable may still supply the axis's `values`)")
}

// axisCollision is the fault for a declared template variable sharing a
// name with an axis of a for_each item or a sweep in the body: inside its
// scope the placeholder would be ambiguous — template variable or axis — so the
// declaration is refused rather than one reading silently winning.
func axisCollision(t *Template, a itemAxis) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_AXIS_COLLISION,
		"template body at "+a.path+" declares the "+a.owner+" axis "+strconv.Quote(a.name)+
			", which is also a declared template variable; inside its scope `{{"+a.name+"}}` and {\""+markerKey+
			"\": \""+a.name+"\"} would be ambiguous — rename the axis or the variable",
		renderDetails(t, a.name, a.path))
}
