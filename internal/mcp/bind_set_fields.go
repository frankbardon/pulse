package mcp

import (
	"reflect"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fieldaccept"
)

// setOnlyField reports whether op's `field` reads a set field only —
// the AGG_SET_* / FILTER_SET_* / GROUP_SET_* operators of the
// fieldaccept table (setValue): it accepts a set and refuses a number
// and a categorical. An untabled operator (an extension) answers false
// and keeps the cohort-wide enum.
func setOnlyField(op string) bool {
	if _, ok := fieldaccept.Lookup(op, fieldaccept.SlotField); !ok {
		return false
	}
	return fieldaccept.Accepts(op, fieldaccept.SlotField, encoding.FieldTypeSetU8) &&
		!fieldaccept.Accepts(op, fieldaccept.SlotField, encoding.FieldTypeF64) &&
		!fieldaccept.Accepts(op, fieldaccept.SlotField, encoding.FieldTypeCategoricalU8)
}

// bindSetFieldBranch narrows an aggregation / filterer / grouper item's
// `field` to the cohort's set fields (c.Set, classifyFields' set
// bucket) when its `type` is one of opTypes' set-only operators
// (#749b): an `allOf` branch `if type ∈ {…} then field ∈ c.Set`, or
// `false` when the cohort has no set field (the run refuses every
// candidate). The item's own field enum stays the union (every other
// type reads it). No set-only operator visible, or no item: no change.
// Plain draft 2020-12 keywords, the attribute branches' shape
// (bindAttributeFieldTypes); relaxForEachItems relaxes the branch's
// `then` on a for_each slot like theirs.
func bindSetFieldBranch(item map[string]any, opTypes []string, c fieldClassification) {
	if item == nil {
		return
	}
	var setOps []string
	for _, op := range opTypes {
		if setOnlyField(op) {
			setOps = append(setOps, op)
		}
	}
	if len(setOps) == 0 {
		return
	}
	var field any = false
	if len(c.Set) > 0 {
		field = map[string]any{"type": "string", "enum": append([]string(nil), c.Set...)}
	}
	branch := map[string]any{
		"if": map[string]any{
			"properties": map[string]any{"type": map[string]any{"enum": setOps}},
			"required":   []string{"type"},
		},
		"then": map[string]any{"properties": map[string]any{"field": field}},
	}
	all, _ := item["allOf"].([]any)
	item["allOf"] = append(all, branch)
}

// bindSetFieldTypes applies bindSetFieldBranch to the request's
// aggregations, filterers and groups items and to the crosstab's axis
// groupers and cell aggregations.
func bindSetFieldTypes(props map[string]any, c fieldClassification, aggTypes, filterTypes, groupTypes []string) {
	arrayItem := func(m map[string]any, key string) map[string]any {
		arr, _ := m[key].(map[string]any)
		item, _ := arr["items"].(map[string]any)
		return item
	}
	bindSetFieldBranch(arrayItem(props, "aggregations"), aggTypes, c)
	bindSetFieldBranch(arrayItem(props, "filterers"), filterTypes, c)
	bindSetFieldBranch(arrayItem(props, "groups"), groupTypes, c)
	ct, _ := props["crosstab"].(map[string]any)
	ctp, _ := ct["properties"].(map[string]any)
	if ctp == nil {
		return
	}
	// rows and columns share one item object: bind it once.
	rows := arrayItem(ctp, "rows")
	bindSetFieldBranch(rows, groupTypes, c)
	if cols := arrayItem(ctp, "columns"); cols != nil && !sameMap(cols, rows) {
		bindSetFieldBranch(cols, groupTypes, c)
	}
	cell, _ := ctp["cell"].(map[string]any)
	bindSetFieldBranch(cell, aggTypes, c)
	bindSetFieldBranch(arrayItem(ctp, "cells"), aggTypes, c)
	bindSetFieldBranch(arrayItem(ctp, "margin_aggregations"), aggTypes, c)
}

// sameMap reports whether a and b are the same map object.
func sameMap(a, b map[string]any) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}
