package descriptor

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ValidateLabels walks a slice of LabelBindings against the resolved
// schema and the extension snapshot, surfacing per-binding failures
// as envelope errors. ValidateLabels never reads record data — it is
// safe to call from any no-execute predict path.
//
// Validation order is deterministic: bindings are inspected in input
// order; the first failure per binding is recorded but later bindings
// are still examined so callers see every problem in one pass.
//
// `extraFields` lists output-only field names that count as occupying
// the schema namespace for augment-mode collision detection (e.g.
// aggregation output labels, attribute labels). Pass nil when only
// raw schema fields matter.
//
// Returns the set of (output) field names occupied by augment-mode
// sibling columns so callers can fold them into downstream collision
// checks (group keys, sort keys against the augmented schema).
func ValidateLabels(
	env *Envelope,
	bindings []*types.LabelBinding,
	schema *encoding.Schema,
	snap *ExtensionsSnapshot,
	extraFields map[string]bool,
) (augmentNames map[string]bool) {
	augmentNames = map[string]bool{}
	if len(bindings) == 0 || schema == nil {
		return augmentNames
	}

	seen := make(map[string]int, len(bindings))
	for i, b := range bindings {
		if b == nil {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("labels[%d]: nil binding", i),
				map[string]any{"index": i})
			continue
		}
		if strings.TrimSpace(b.Field) == "" {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("labels[%d]: Field is required", i),
				map[string]any{"index": i})
			continue
		}
		if strings.TrimSpace(b.Table) == "" {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("labels[%d]: Table is required", i),
				map[string]any{"index": i, "field": b.Field})
			continue
		}
		mode := b.LabelModeOrDefault()
		if mode != types.LabelModeReplace && mode != types.LabelModeAugment {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("labels[%d]: Mode %q is invalid (expected %q or %q)", i, b.Mode, types.LabelModeReplace, types.LabelModeAugment),
				map[string]any{"index": i, "field": b.Field, "mode": string(b.Mode)})
			continue
		}

		if prior, ok := seen[b.Field]; ok {
			env.AddError(string(errors.PULSE_LABEL_DUPLICATE_BINDING),
				fmt.Sprintf("labels[%d]: field %q already bound at index %d", i, b.Field, prior),
				map[string]any{"index": i, "field": b.Field, "prior_index": prior})
			continue
		}
		seen[b.Field] = i

		field, ok := lookupField(schema, b.Field)
		if !ok {
			env.AddError(string(errors.PULSE_LABEL_FIELD_UNKNOWN),
				fmt.Sprintf("labels[%d]: field %q not in schema", i, b.Field),
				map[string]any{"index": i, "field": b.Field})
			continue
		}
		if !isCategoricalFieldType(field.Type) {
			env.AddError(string(errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL),
				fmt.Sprintf("labels[%d]: field %q is %s; labels require categorical_*", i, b.Field, field.Type),
				map[string]any{"index": i, "field": b.Field, "field_type": field.Type.String()})
			continue
		}
		if !labelTableRegistered(snap, b.Table) {
			env.AddError(string(errors.PULSE_LABEL_TABLE_UNKNOWN),
				fmt.Sprintf("labels[%d]: table %q not registered on Service", i, b.Table),
				map[string]any{"index": i, "field": b.Field, "table": b.Table})
			continue
		}

		if mode == types.LabelModeAugment {
			sibling := b.AugmentFieldName()
			if _, exists := lookupField(schema, sibling); exists {
				env.AddError(string(errors.PULSE_LABEL_FIELD_COLLISION),
					fmt.Sprintf("labels[%d]: augment sibling %q collides with existing schema field", i, sibling),
					map[string]any{"index": i, "field": b.Field, "sibling": sibling})
				continue
			}
			if extraFields[sibling] {
				env.AddError(string(errors.PULSE_LABEL_FIELD_COLLISION),
					fmt.Sprintf("labels[%d]: augment sibling %q collides with an output column", i, sibling),
					map[string]any{"index": i, "field": b.Field, "sibling": sibling})
				continue
			}
			if augmentNames[sibling] {
				env.AddError(string(errors.PULSE_LABEL_FIELD_COLLISION),
					fmt.Sprintf("labels[%d]: augment sibling %q already produced by an earlier binding", i, sibling),
					map[string]any{"index": i, "field": b.Field, "sibling": sibling})
				continue
			}
			augmentNames[sibling] = true
		}
	}
	return augmentNames
}

// lookupField returns the named field and a found-bool.
func lookupField(schema *encoding.Schema, name string) (*encoding.Field, bool) {
	for i := range schema.Fields {
		if schema.Fields[i].Name == name {
			return &schema.Fields[i], true
		}
	}
	return nil, false
}

// isCategoricalFieldType reports whether the given field type is one
// of the dictionary-backed categorical_* widths.
func isCategoricalFieldType(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		return true
	}
	return false
}

// labelTableRegistered reports whether the snapshot lists a label
// table by name. A nil snapshot resolves to "not registered" — the
// Service either has no extension state or the predict caller did not
// pass one. Built-in pulse ships no label tables, so absence is
// authoritative.
func labelTableRegistered(snap *ExtensionsSnapshot, name string) bool {
	if snap == nil {
		return false
	}
	for _, t := range snap.LabelTables {
		if t.Name == name {
			return true
		}
	}
	return false
}
