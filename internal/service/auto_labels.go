package service

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/types"
)

// applyAutoLabels appends the Service's configured default LabelBindings
// to labels, in place, for the subset that fits the given schema. A
// default is injected only when all of the following hold:
//
//   - the caller has not already bound the same field;
//   - the field exists in the schema and is categorical_*;
//   - the referenced table is registered on the extension registry;
//   - (augment mode) the "<field>_label" sibling does not collide with
//     an existing schema field or a name in occupied.
//
// Any default that fails a check is skipped silently — auto-injection
// must never turn an otherwise-valid request into a validation error.
// occupied carries the request's output column names (aggregation /
// group / window / feature labels) so an augment sibling cannot shadow
// one; pass nil when there are no such columns. restrictTo, when
// non-nil, limits injection to the named fields (used by Facet to bind
// only the fields actually being faceted); pass nil to allow any
// categorical schema field.
//
// attrs are the request's attribute slots: an ATTR_PREVIOUS_STATE
// output column reads as its state field (prevstate.KeySchema), so a
// default on the state field also binds each such column (one copy per
// label, autoLabelDefaults) — else a previous-state × state crosstab
// shows display labels on one axis and raw codes on the other. A label
// that names a real schema field gets no copy. Pass nil when the
// request carries no attributes.
func (s *Service) applyAutoLabels(labels *[]*types.LabelBinding, schema *encoding.Schema, occupied, restrictTo map[string]bool, attrs []*types.Attribute) {
	if len(s.autoLabels) == 0 || schema == nil || labels == nil {
		return
	}
	defaults := s.autoLabels
	// derivedFrom maps a previous-state output label to its state
	// field: restrictTo is judged on the state field.
	var derivedFrom map[string]string
	if derived := prevStateAutoLabels(s.autoLabels, attrs); len(derived) > 0 {
		derivedFrom = map[string]string{}
		for _, attr := range attrs {
			if attr != nil && attr.Type == types.ATTR_PREVIOUS_STATE {
				if _, real := schemaFieldByName(schema, prevstate.Label(attr)); !real {
					derivedFrom[prevstate.Label(attr)] = attr.Field
				}
			}
		}
		defaults = append(append([]*types.LabelBinding(nil), s.autoLabels...), derived...)
		schema = prevstate.KeySchema(attrs, schema)
	}

	bound := make(map[string]bool, len(*labels))
	for _, b := range *labels {
		if b != nil {
			bound[b.Field] = true
		}
	}

	for i, def := range defaults {
		if def == nil || bound[def.Field] {
			continue
		}
		origin := def.Field
		if i >= len(s.autoLabels) {
			src, ok := derivedFrom[def.Field]
			if !ok {
				continue // the label names a real schema field
			}
			origin = src
		}
		if restrictTo != nil && !restrictTo[origin] {
			continue
		}
		field, ok := schemaFieldByName(schema, def.Field)
		if !ok || !field.Type.IsCategorical() {
			continue
		}
		if !s.labelTableRegistered(def.Table) {
			continue
		}
		mode := def.LabelModeOrDefault()
		if mode != types.LabelModeReplace && mode != types.LabelModeAugment {
			continue
		}
		if mode == types.LabelModeAugment {
			sibling := def.AugmentFieldName()
			if _, exists := schemaFieldByName(schema, sibling); exists {
				continue
			}
			if occupied[sibling] {
				continue
			}
		}
		cp := *def
		*labels = append(*labels, &cp)
		bound[def.Field] = true
	}
}

// autoLabelDefaults is the instance's default bindings plus, for each
// ATTR_PREVIOUS_STATE slot of attrs, a copy of every default on its
// state field rebound to the slot's output label — the set
// applyAutoLabels injects from, and the set whose bindings never raise
// PULSE_LABEL_NOT_APPLIED.
func (s *Service) autoLabelDefaults(attrs []*types.Attribute) []*types.LabelBinding {
	derived := prevStateAutoLabels(s.autoLabels, attrs)
	if len(derived) == 0 {
		return s.autoLabels
	}
	return append(append([]*types.LabelBinding(nil), s.autoLabels...), derived...)
}

// prevStateAutoLabels copies each default bound to an
// ATTR_PREVIOUS_STATE slot's state field onto the slot's output label,
// in slot order; nil when no slot's state field has a default.
func prevStateAutoLabels(defaults []*types.LabelBinding, attrs []*types.Attribute) []*types.LabelBinding {
	var out []*types.LabelBinding
	for _, attr := range attrs {
		if attr == nil || attr.Type != types.ATTR_PREVIOUS_STATE || attr.Field == "" {
			continue
		}
		label := prevstate.Label(attr)
		for _, def := range defaults {
			if def != nil && def.Field == attr.Field && label != attr.Field {
				cp := *def
				cp.Field = label
				out = append(out, &cp)
			}
		}
	}
	return out
}

// labelTableRegistered reports whether a label table is registered on
// the runtime extension registry and offered by the instance (a hidden
// capability:labels offers none).
func (s *Service) labelTableRegistered(name string) bool {
	_, ok := s.extensions.LookupLabelTable(name)
	return ok
}

// schemaFieldByName returns the named field and a found-bool.
func schemaFieldByName(schema *encoding.Schema, name string) (*encoding.Field, bool) {
	if schema == nil {
		return nil, false
	}
	for i := range schema.Fields {
		if schema.Fields[i].Name == name {
			return &schema.Fields[i], true
		}
	}
	return nil, false
}
