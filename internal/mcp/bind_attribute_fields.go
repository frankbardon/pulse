package mcp

import (
	"slices"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fieldaccept"
)

// attributeFieldDescription is the bound attributes[].field description:
// the enum is the union over the instance's attribute types, and each
// type narrows it to the fields it accepts.
const attributeFieldDescription = "Source field. The field types it may name follow the attribute type (the manifest operator's accepts_types): numeric for the standardising attributes, a categorical or packed_bool state for ATTR_PREVIOUS_STATE, a categorical or unsigned integer for ATTR_CODE_IN, a date or datetime for ATTR_DATE_PART, a set for ATTR_SET_*."

// attributeBoundSlots are the attribute item keys an accepted-types
// rule judges, in schema order.
var attributeBoundSlots = []fieldaccept.Slot{fieldaccept.SlotField, fieldaccept.SlotSubject, fieldaccept.SlotOrder}

// attributeSlotAdmits is the bound-schema predicate for an attribute
// type's slot — the rule predict and the runtime apply. A tabled
// built-in slot (internal/fieldaccept) admits what the table accepts,
// never a vector (the field-reference walk's refusal). An untabled
// `field` admits an extension's declared Accepts when it declares any,
// else the numeric class every attribute bound before per-type rules
// (it reads no other type: an expression, a regression's target, a
// vector's members). An untabled subject_field / order_field is not
// read: any field.
func attributeSlotAdmits(op string, slot fieldaccept.Slot, accepts map[string][]string) func(encoding.FieldType) bool {
	if _, ok := fieldaccept.Lookup(op, slot); ok {
		return func(ft encoding.FieldType) bool { return !ft.IsVector() && fieldaccept.Accepts(op, slot, ft) }
	}
	if slot != fieldaccept.SlotField {
		return func(encoding.FieldType) bool { return true }
	}
	if names := accepts[op]; len(names) > 0 {
		return func(ft encoding.FieldType) bool { return slices.Contains(names, ft.String()) }
	}
	return isNumericType
}

// bindAttributeFieldTypes binds the attributes[] item's field,
// subject_field and order_field to the types each attribute type
// accepts. Each property's enum is the union over attrTypes (schema
// order), and an `allOf` of `if type ∈ {…} then {narrower enum}`
// branches narrows it per type: types sharing one admitted field set
// share a branch, and a type admitting the whole union needs none. A
// type admitting no field binds the slot `false`. Plain draft 2020-12
// keywords; relaxForEachItems relaxes each branch's `then` like the
// base properties.
func bindAttributeFieldTypes(props map[string]any, c fieldClassification, attrTypes []string, inst *descx.InstanceSnapshot) {
	arr, _ := props["attributes"].(map[string]any)
	item, _ := arr["items"].(map[string]any)
	ip, _ := item["properties"].(map[string]any)
	if ip == nil {
		return
	}
	accepts := map[string][]string{}
	if snap := inst.Extensions(); snap != nil {
		for _, m := range snap.Attributes {
			accepts[m.Name] = m.Accepts
		}
	}
	admitted := func(op string, slot fieldaccept.Slot) []string {
		ok := attributeSlotAdmits(op, slot, accepts)
		var out []string
		for _, name := range c.AllFields {
			if ft, known := c.Types[name]; known && ok(ft) {
				out = append(out, name)
			}
		}
		return out
	}

	perType := make(map[string]map[fieldaccept.Slot][]string, len(attrTypes))
	union := map[fieldaccept.Slot][]string{}
	for _, op := range attrTypes {
		perType[op] = map[fieldaccept.Slot][]string{}
		for _, slot := range attributeBoundSlots {
			names := admitted(op, slot)
			perType[op][slot] = names
			union[slot] = appendUnique(union[slot], names, "")
		}
	}
	for _, slot := range attributeBoundSlots {
		// The union in schema order (appendUnique keeps first-seen).
		var ordered []string
		for _, name := range c.AllFields {
			if slices.Contains(union[slot], name) {
				ordered = append(ordered, name)
			}
		}
		union[slot] = ordered
		if p, ok := ip[string(slot)].(map[string]any); ok {
			desc, _ := p["description"].(string)
			ip[string(slot)] = enumStringField(ordered, desc)
		}
	}

	type branch struct {
		types []string
		then  map[string]any
	}
	var branches []*branch
	byKey := map[string]*branch{}
	for _, op := range attrTypes {
		then := map[string]any{}
		var key []string
		for _, slot := range attributeBoundSlots {
			names := perType[op][slot]
			if slices.Equal(names, union[slot]) {
				continue
			}
			key = append(key, string(slot)+"="+strings.Join(names, "\x00"))
			if len(names) == 0 {
				then[string(slot)] = false
				continue
			}
			then[string(slot)] = map[string]any{"type": "string", "enum": names}
		}
		if len(then) == 0 {
			continue
		}
		k := strings.Join(key, "\x01")
		if b, ok := byKey[k]; ok {
			b.types = append(b.types, op)
			continue
		}
		b := &branch{types: []string{op}, then: then}
		byKey[k] = b
		branches = append(branches, b)
	}
	if len(branches) == 0 {
		return
	}
	all := make([]any, len(branches))
	for i, b := range branches {
		all[i] = map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"type": map[string]any{"enum": b.types}},
				"required":   []string{"type"},
			},
			"then": map[string]any{"properties": b.then},
		}
	}
	item["allOf"] = all
}
