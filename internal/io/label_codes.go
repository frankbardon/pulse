package io

import (
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Which fields a value-label binding may name, shared by every export
// target. A categorical_* field is labelled through its dictionary text. An
// INTEGER-CODED field (u4 / u8 / u16 / u32) has no dictionary: its stored
// integers are the codes, and the bound table's keys are their canonical
// decimal spellings. Every other type — floats, u64, decimal128, date,
// datetime, packed_bool, set_* and vec_* — is refused
// PULSE_LABEL_FIELD_NOT_CATEGORICAL: its cell text is no stable key (a
// float's spelling, a zone-rendered datetime) or, for u64, the `.sav`
// writer could not hold it as an exact double, and a row export refuses
// what the `.sav` export refuses so one binding means one thing on every
// target.

// IntegerLabelCodeMax reports whether ft is an integer-coded type a
// value-label binding may name, and the largest value it stores. u64 is
// not one: its values past 2^53 have no exact SPSS (double) code.
func IntegerLabelCodeMax(ft encoding.FieldType) (uint64, bool) {
	switch ft {
	case encoding.FieldTypeU4:
		return 15, true
	case encoding.FieldTypeU8:
		return 255, true
	case encoding.FieldTypeU16:
		return 65535, true
	case encoding.FieldTypeU32:
		return 1<<32 - 1, true
	}
	return 0, false
}

// CheckIntegerLabelKeys refuses PULSE_LABEL_KEY_INVALID when a key of a
// binding on the integer-coded field (type ft, largest value max) is not
// the canonical decimal spelling of a value the field can store — text, a
// leading zero, a sign, a fraction or a value past max could never label a
// stored integer, and dropping it silently would hide the mistake. Keys are
// checked in sorted order so the refusal names the same key on every run.
// scope prefixes the message ("spss", "export").
func CheckIntegerLabelKeys(scope, field string, ft encoding.FieldType, max uint64, keys []string) error {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for _, k := range sorted {
		n, err := strconv.ParseUint(k, 10, 64)
		if err == nil && strconv.FormatUint(n, 10) == k && n <= max {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_KEY_INVALID,
			scope+": the value-label binding on "+strconv.Quote(field)+", a "+ft.String()+
				" field, has the key "+strconv.Quote(k)+", which is not the plain decimal spelling of a value from 0 to "+
				strconv.FormatUint(max, 10)+"; the field's stored integers are its codes, so the key could never label one",
			map[string]any{"field": field, "type": ft.String(), "key": k, "max": max, "option": "--labels"})
	}
	return nil
}

// checkRowLabelBindings validates an export's label bindings for a ROW
// writer (every target but a CohortWriter, which validates its own
// bindings — the `.sav` writer's BuildDictionary). The bound fields are
// those cohortValueLabels binds: the job's Labels, or every schema field
// the resolver Has. A categorical passes; an integer-coded field passes
// when its table's keys are values it stores (a table that cannot list
// its entries — a Lookup-only one — is looked up cell by cell, so it
// passes unchecked); any other type is PULSE_LABEL_FIELD_NOT_CATEGORICAL.
// A field the schema lacks is left as before (the binding labels nothing).
//
// It reads the schema and the bound tables only, never a record, so
// ExportJob.Run and ExportJob.Predict call it alike and raise one code.
func checkRowLabelBindings(schema *encoding.Schema, bindings []*types.LabelBinding, r LabelResolver) error {
	if r == nil || schema == nil {
		return nil
	}
	byName := make(map[string]int, len(schema.Fields))
	for i := range schema.Fields {
		byName[schema.Fields[i].Name] = i
	}
	var bound []string
	for _, b := range bindings {
		if b != nil {
			bound = append(bound, b.Field)
		}
	}
	if len(bound) == 0 {
		for i := range schema.Fields {
			bound = append(bound, schema.Fields[i].Name)
		}
	}
	for _, name := range bound {
		at, ok := byName[name]
		if !ok || !r.Has(name) {
			continue
		}
		ft := schema.Fields[at].Type
		if ft.IsCategorical() {
			continue
		}
		if max, coded := IntegerLabelCodeMax(ft); coded {
			e, ok := r.(labelEntrier)
			if !ok {
				continue
			}
			keys, ok := e.Entries(name)
			if !ok {
				continue
			}
			if err := CheckIntegerLabelKeys("export", name, ft, max, keys); err != nil {
				return err
			}
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL,
			"export: a label binding names "+strconv.Quote(name)+", a "+ft.String()+
				" field; labels bind to categorical_* fields, whose dictionary text is the key, "+
				"and to integer-coded u4 / u8 / u16 / u32 fields, whose stored integers' decimal spellings are the keys",
			map[string]any{"field": name, "type": ft.String(), "option": "--labels"})
	}
	return nil
}
