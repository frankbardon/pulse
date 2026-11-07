package prevstate

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// inherit.go is the extension half of the dictionary-inheritance hook
// (U67 E3-S2): an extension attribute registration declaring
// InheritsDictionary emits, per record, a CODE of its source field's
// dictionary (a categorical index, or 0 / 1 for a packed_bool source),
// and its output column reads as that field everywhere
// ATTR_PREVIOUS_STATE's does (KeySchema's consumers). Predict reads
// the declarations off its extensions snapshot, the runtime off its
// registry — the same map, so both build the same view.

// Inherits maps an extension attribute type to the source of the
// dictionary its output inherits: "" for the slot's Field, else the
// name of the string param whose value names the source field. Nil (or
// a type absent) declares none — the built-in view.
type Inherits map[types.AttributeType]string

// SourceName is how a declaration names its source in a refusal and in
// the manifest: "field", or "params.<name>".
func SourceName(param string) string {
	if param == "" {
		return "field"
	}
	return "params." + param
}

// sourceField is the field name attr's declared source names (its
// Field, or the string value of the declared param; "" when unset or
// not a string) and whether attr's type declares one at all.
func sourceField(attr *types.Attribute, inh Inherits) (name, param string, declared bool) {
	if attr == nil || inh == nil {
		return "", "", false
	}
	param, declared = inh[attr.Type]
	if !declared {
		return "", "", false
	}
	if param == "" {
		return attr.Field, param, true
	}
	if len(attr.Params) == 0 {
		return "", param, true
	}
	var m map[string]any
	if paramread.Probe(attr.Params, &m) != nil {
		return "", param, true
	}
	s, _ := m[param].(string)
	return s, param, true
}

// coded reports whether ft carries codes an output may inherit: a
// categorical_* (dictionary indices) or packed_bool (0 / 1) field.
func coded(ft encoding.FieldType) bool {
	return ft.IsCategorical() || ft == encoding.FieldTypePackedBool
}

// InheritedSource returns the cohort field the output of attr inherits
// its dictionary from, or the slot's refusal; (nil, nil) when attr's
// type declares no inherited dictionary (inh). The source must be set
// (PROCESSING_CONFIG reason "source_unset"), name a cohort field — the
// dictionary is a stored column's, never another slot's output
// (PROCESSING_CONFIG "source_not_cohort_field") — of a coded type
// (PULSE_FIELD_TYPE_UNSUPPORTED "source_not_coded"). Details carry
// attribute, source ("field" / "params.<name>") and reason, plus field
// (and field_type) once a name is known. The field-reference walk
// (predict and the runtime entry check) and the runtime adapter (before
// the factory) both call it. A nil schema judges presence only.
func InheritedSource(attr *types.Attribute, schema *encoding.Schema, inh Inherits) (*encoding.Field, *errors.CodedError) {
	name, param, declared := sourceField(attr, inh)
	if !declared {
		return nil, nil
	}
	op, src := string(attr.Type), SourceName(param)
	if name == "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			op+" inherits its output dictionary from its "+src+", which the slot does not set to a field name",
			map[string]any{"attribute": op, "source": src, "reason": "source_unset"})
	}
	if schema == nil {
		return nil, nil
	}
	f := schema.Field(name)
	if f == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			op+" inherits its output dictionary from "+src+" "+name+", which is not a cohort field (the dictionary is a stored column's, never another slot's output)",
			map[string]any{"attribute": op, "source": src, "field": name, "reason": "source_not_cohort_field"})
	}
	if !coded(f.Type) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_FIELD_TYPE_UNSUPPORTED,
			op+" inherits its output dictionary from "+src+" "+name+", a "+f.Type.String()+" field; the source must be a categorical_* or packed_bool field",
			map[string]any{"attribute": op, "source": src, "field": name, "field_type": f.Type.String(), "reason": "source_not_coded"})
	}
	return f, nil
}

// SourceOf is the field whose dictionary attr's output inherits: an
// ATTR_PREVIOUS_STATE slot's state Field, an extension slot's accepted
// source (InheritedSource over inh; under a nil schema the declared
// name, unjudged), "" for every other slot or a refused source.
func SourceOf(attr *types.Attribute, schema *encoding.Schema, inh Inherits) string {
	if attr == nil {
		return ""
	}
	if attr.Type == types.ATTR_PREVIOUS_STATE {
		return attr.Field
	}
	name, _, declared := sourceField(attr, inh)
	if !declared || schema == nil {
		return name
	}
	if f, _ := InheritedSource(attr, schema, inh); f != nil {
		return f.Name
	}
	return ""
}

// CodeCount is the number of codes source f carries: its dictionary's
// entry count for a categorical, 2 for a packed_bool, 0 otherwise.
func CodeCount(f *encoding.Field) int {
	switch {
	case f == nil:
		return 0
	case f.Type == encoding.FieldTypePackedBool:
		return 2
	case f.Type.IsCategorical() && f.Dictionary != nil:
		return f.Dictionary.Count()
	}
	return 0
}

// CodeFault reports whether v is no code of an n-entry dictionary: not
// an integer in [0, n). NaN — a null output — is never a fault.
func CodeFault(v float64, n int) bool {
	if math.IsNaN(v) {
		return false
	}
	return !(v >= 0 && v < float64(n) && v == math.Trunc(v))
}
