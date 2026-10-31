package guide

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// The tools a materialise plan's steps run.
const (
	toolMaterialize = "pulse_materialize"
	toolProcess     = "pulse_process"
)

// materializeTrigger is one materializeSteps row: the label the derived
// column is written under, the model operator that reads it, and the
// model's wire key the column fills.
type materializeTrigger struct {
	column string
	model  string
	role   string // "predictors" or "target"
}

// steps returns rec's materialise plan, or nil when its operator is not
// a materializeSteps producer, when the instance hides a step's tool or
// model, or — bound mode — when the materialize draft does not pass
// materialize's own no-execute validation (its slot gate and derived
// column naming) over the cohort's schema.
func steps(inst *descx.InstanceSnapshot, rec descriptor.Recommendation, b *Bound) []descriptor.RecommendStep {
	trig, ok := materializeSteps[rec.Operator]
	if !ok || (rec.Category != catAttribute && rec.Category != catFeature) {
		return nil
	}
	for _, tool := range []string{toolMaterialize, toolProcess} {
		if bd, ok := descx.MCPToolBindingOf(tool); !ok || (bd.Feature != "" && inst.Hidden(bd.Feature)) {
			return nil
		}
	}
	if inst.Hidden(trig.model) {
		return nil
	}
	column := trig.column
	if b != nil {
		column = freeName(b.Schema, column)
	}
	inner, ok := labelled(rec.Request, column)
	if !ok {
		return nil
	}
	if b != nil && !materializes(inst, inner, b, column) {
		return nil
	}
	first, ok := encode(map[string]any{"request": inner, "out": placeholder("out")})
	if !ok {
		return nil
	}
	other, slot := "predictors", map[string]any{"type": trig.model}
	var reads any = column
	if trig.role == "target" {
		slot["predictors"] = []string{placeholder(other)}
	} else {
		other, reads = "target", []string{column}
		slot["target"] = placeholder(other)
	}
	slot[trig.role] = reads
	second, ok := encode(map[string]any{
		"cohort":      map[string]any{"filename": placeholder("out")},
		"regressions": []any{slot},
	})
	if !ok {
		return nil
	}
	return []descriptor.RecommendStep{
		{
			Tool:         toolMaterialize,
			Request:      first,
			Placeholders: sortedUnique(append(slices.Clone(rec.Placeholders), "out")),
			Why:          "Write the " + column + " column to a new cohort first, so the model and every later request read it as an ordinary field.",
		},
		{
			Tool:         toolProcess,
			Request:      second,
			Placeholders: sortedUnique([]string{"out", other}),
			Why:          "Then fit the model on the new cohort, reading " + column + " " + roleWhy(trig.role) + ".",
		},
	}
}

// roleWhy names the model role the materialised column fills.
func roleWhy(role string) string {
	if role == "target" {
		return "as the outcome"
	}
	return "as a predictor"
}

// freeName returns name, or name_2, name_3, … — the first that is not a
// field of schema, so the materialised column cannot collide with a
// source field.
func freeName(schema *encoding.Schema, name string) string {
	if schema == nil {
		return name
	}
	taken := func(n string) bool {
		return slices.ContainsFunc(schema.Fields, func(f encoding.Field) bool { return f.Name == n })
	}
	out := name
	for i := 2; taken(out); i++ {
		out = name + "_" + strconv.Itoa(i)
	}
	return out
}

// labelled returns the draft request with its one derived entry — an
// attribute or a feature, the only one across both slots — labelled
// column, so the model step can name the column it reads.
func labelled(request json.RawMessage, column string) (json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(request))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return nil, false
	}
	var entries []any
	for _, cat := range []string{catAttribute, catFeature} {
		if v, ok := body[slotKey[cat]]; ok {
			list, ok := v.([]any)
			if !ok {
				return nil, false
			}
			entries = append(entries, list...)
		}
	}
	if len(entries) != 1 {
		return nil, false
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		return nil, false
	}
	entry["label"] = column
	return encode(body)
}

// materializes reports whether the labelled draft passes materialize's
// no-execute validation over the bound cohort: its slot gate and its
// derived-column naming, which must yield the labelled column. The
// draft itself already passed predict.
func materializes(inst *descx.InstanceSnapshot, inner json.RawMessage, b *Bound, column string) bool {
	var req types.Request
	if err := json.Unmarshal(inner, &req); err != nil {
		return false
	}
	if descx.MaterializeSlotRefusal(&req) != nil {
		return false
	}
	source := ""
	if b.Cohort != nil {
		source = b.Cohort.Filename
	}
	cols, err := descx.MaterializeColumns(&req, b.Schema, inst, source)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(cols, func(c descx.MaterializeColumn) bool { return c.Name == column })
}

// encode marshals v as wire JSON without HTML escaping, as a draft
// request is written.
func encode(v any) (json.RawMessage, bool) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}
