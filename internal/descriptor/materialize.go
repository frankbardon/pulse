package descriptor

import (
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// MaterializeColumn is one derived column a materialize run appends to
// the source schema: the column name the pipeline writes (the same
// label Process emits), its generated field description, and where the
// value is read — off the record (a feature or attribute output) or
// off the windowed row set (a window output).
type MaterializeColumn struct {
	Name        string
	Description string
	Window      bool
}

// materializeDescriptionMax is the field-description ceiling a cohort
// write enforces (PULSE_IMPORT_DESCRIPTION_TOO_LONG past it).
const materializeDescriptionMax = 1000

// MaterializeSlotRefusal is the materialize slot gate: a request slot
// that does not yield one value per source record is refused with
// PULSE_MATERIALIZE_SLOT_UNSUPPORTED, details.slot naming its JSON key,
// in Request field order. Accepted: cohort, label, features,
// filterers, attributes, windows, weight, time_zone and
// disable_components (which has nothing to gate). Schema-free.
func MaterializeSlotRefusal(req *types.Request) *errors.CodedError {
	if req == nil {
		return nil
	}
	slots := []struct {
		name string
		set  bool
	}{
		{"aggregations", len(req.Aggregations) > 0},
		{"groups", len(req.Groups) > 0},
		{"outputs", len(req.Outputs) > 0},
		{"sort", len(req.Sort) > 0},
		{"tests", len(req.Tests) > 0},
		{"post_tests", len(req.PostTests) > 0},
		{"regressions", len(req.Regressions) > 0},
		{"vectors", len(req.Vectors) > 0},
		{"matrices", len(req.Matrices) > 0},
		{"joins", len(req.Joins) > 0},
		{"labels", len(req.Labels) > 0},
		{"crosstab", req.Crosstab != nil},
		{"overlays", len(req.Overlays) > 0},
		{"multiplicity", req.Multiplicity != nil},
		{"return", req.Return != nil},
	}
	for _, s := range slots {
		if s.set {
			return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_SLOT_UNSUPPORTED,
				"materialize does not accept the "+s.name+" slot: only features, filterers, attributes, windows, weight and time_zone produce one value per source record",
				map[string]any{"slot": s.name})
		}
	}
	return nil
}

// MaterializeColumns names the derived columns a materialize run of req
// appends, in pipeline order — features, then attributes, then windows,
// declaration order within each, a multi-output feature contributing
// every output (featureOutputLabels) — each with a generated
// description naming the operator, its source, its params and source
// (the cohort path the run reads; only its base name is used).
//
// Refusals: PULSE_MATERIALIZE_NO_DERIVED when req carries no feature,
// attribute or window; PULSE_MATERIALIZE_OUTPUT_UNDECLARED for an
// embedder-registered feature (its outputs are known only after it
// runs) or a built-in one whose outputs cannot be named from its params
// and schema. A feature type that is neither built-in nor registered
// contributes nothing here: the run refuses it as an unknown operator,
// exactly as Process does. Reads only the schema.
func MaterializeColumns(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, source string) ([]MaterializeColumn, *errors.CodedError) {
	if req == nil || (len(req.Features) == 0 && len(req.Attributes) == 0 && len(req.Windows) == 0) {
		return nil, errors.NewCodedError(errors.PULSE_MATERIALIZE_NO_DERIVED,
			"materialize needs at least one feature, attribute or window: there is no derived column to append")
	}
	from := path.Base(source)
	var out []MaterializeColumn
	for i, feat := range req.Features {
		if feat == nil {
			continue
		}
		route := opRoute(inst, feat.Type)
		if !isKnownFeatureType(route) {
			if inst.Extensions().hasFeature(string(feat.Type)) {
				return nil, undeclaredFeature(i, feat, "it is an embedder-registered feature, whose output columns are known only after it runs")
			}
			continue
		}
		labels := featureOutputLabels(feat, schema)
		if len(labels) == 0 {
			return nil, undeclaredFeature(i, feat, "its params and source field yield no output column")
		}
		base := operatorPhrase(string(feat.Type), feat.Field, feat.Params, nil)
		for _, l := range labels {
			desc := base
			if len(labels) > 1 {
				desc += ", output " + l
			}
			out = append(out, MaterializeColumn{Name: l, Description: derivedDescription(desc, from)})
		}
	}
	for _, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		label := attr.Label
		if label == "" {
			label = attributeDefaultLabel(attr, inst)
		}
		field := attr.Field
		switch attr.Type {
		case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
			field = attr.Target
			if len(attr.Predictors) > 0 {
				field += " on " + strings.Join(attr.Predictors, ", ")
			}
		}
		out = append(out, MaterializeColumn{Name: label,
			Description: derivedDescription(operatorPhrase(string(attr.Type), field, attr.Params, nil), from)})
	}
	for _, w := range req.Windows {
		if w == nil {
			continue
		}
		out = append(out, MaterializeColumn{Name: windowLabel(w), Window: true,
			Description: derivedDescription(operatorPhrase(string(w.Type), w.Field, w.Params, windowClauses(w)), from)})
	}
	return out, nil
}

// windowClauses renders a window's partition, order and frame.
func windowClauses(w *types.Window) []string {
	var out []string
	if len(w.PartitionBy) > 0 {
		out = append(out, "partition "+strings.Join(w.PartitionBy, ", "))
	}
	if len(w.OrderBy) > 0 {
		keys := make([]string, len(w.OrderBy))
		for i, k := range w.OrderBy {
			keys[i] = k.Field
			if k.Desc {
				keys[i] += " desc"
			}
		}
		out = append(out, "order "+strings.Join(keys, ", "))
	}
	if f := w.Frame; f != nil {
		bound := func(n *int) string {
			if n == nil {
				return "unbounded"
			}
			return strconv.Itoa(*n)
		}
		out = append(out, "frame "+bound(f.Preceding)+" preceding, "+bound(f.Following)+" following")
	}
	return out
}

func undeclaredFeature(i int, feat *types.Feature, why string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_OUTPUT_UNDECLARED,
		"features["+strconv.Itoa(i)+"] ("+string(feat.Type)+"): the output columns cannot be named before the run — "+why,
		map[string]any{"feature_index": i, "feature": string(feat.Type)})
}

// operatorPhrase renders "<TYPE> of <field> (<k v>; …; <extra>…)":
// params in key order, a string as its text, anything else as compact
// JSON, then the extra clauses (a window's partition / order / frame).
func operatorPhrase(op, field string, params json.RawMessage, extra []string) string {
	var b strings.Builder
	b.WriteString(op)
	if field != "" {
		b.WriteString(" of ")
		b.WriteString(field)
	}
	var parts []string
	if len(params) > 0 {
		var m map[string]json.RawMessage
		if json.Unmarshal(params, &m) == nil {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts = append(parts, k+" "+paramLiteral(m[k]))
			}
		}
	}
	parts = append(parts, extra...)
	if len(parts) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString(")")
	}
	return b.String()
}

// paramLiteral renders one param value: a string unquoted, any other
// value as its compact JSON text.
func paramLiteral(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// derivedDescription appends the provenance suffix and caps the result
// at the cohort description ceiling, cutting on a rune boundary.
func derivedDescription(phrase, from string) string {
	suffix := " — derived from " + from
	if len(phrase)+len(suffix) <= materializeDescriptionMax {
		return phrase + suffix
	}
	const ellipsis = "…"
	cut := materializeDescriptionMax - len(suffix) - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(phrase[cut]) {
		cut--
	}
	return phrase[:cut] + ellipsis + suffix
}

// hasFeature reports whether name is an embedder-registered feature in
// the snapshot. Nil-safe.
func (s *ExtensionsSnapshot) hasFeature(name string) bool {
	if s == nil {
		return false
	}
	for _, m := range s.Features {
		if m.Name == name {
			return true
		}
	}
	return false
}
