package service

import (
	"github.com/frankbardon/pulse/encoding"
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
func (s *Service) applyAutoLabels(labels *[]*types.LabelBinding, schema *encoding.Schema, occupied, restrictTo map[string]bool) {
	if len(s.autoLabels) == 0 || schema == nil || labels == nil {
		return
	}

	bound := make(map[string]bool, len(*labels))
	for _, b := range *labels {
		if b != nil {
			bound[b.Field] = true
		}
	}

	for _, def := range s.autoLabels {
		if def == nil || bound[def.Field] {
			continue
		}
		if restrictTo != nil && !restrictTo[def.Field] {
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

// labelTableRegistered reports whether a label table is registered on
// the runtime extension registry.
func (s *Service) labelTableRegistered(name string) bool {
	if s.extensions == nil || s.extensions.LabelTables == nil {
		return false
	}
	_, ok := s.extensions.LabelTables[name]
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
