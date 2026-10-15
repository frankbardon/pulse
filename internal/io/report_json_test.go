package io

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
)

var snakeKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// TestReports_JSONTagsAreSnakeCase: every exported field of every report
// type the import / export / convert `--json` leaves put under `data`
// (and a library json.Marshal renders) carries an explicit snake_case
// json tag — the managed-import Result's casing — so no Go field name
// ever reaches the wire. A new report field without a tag fails here.
func TestReports_JSONTagsAreSnakeCase(t *testing.T) {
	for _, v := range []any{
		ImportReport{}, ExportReport{}, ConvertReport{}, PredictReport{},
		LabelWarning{}, InferenceWarning{}, GroupReport{}, ImportProjection{},
		GroupDetection{}, GroupCandidate{},
	} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "" {
				t.Errorf("%s.%s has no json tag", rt.Name(), f.Name)
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if name != "-" && !snakeKey.MatchString(name) {
				t.Errorf("%s.%s json key %q is not snake_case", rt.Name(), f.Name, name)
			}
		}
	}
}

// TestImportReport_JSONShape: a populated report marshals with the
// snake_case keys the managed-import Result already uses, the schema's
// types as names, and each row error flattened to {row, code, message,
// details} — never an empty object for an uncoded error.
func TestImportReport_JSONShape(t *testing.T) {
	warn := perrors.NewCodedErrorWithDetails(perrors.PULSE_IMPORT_WIDTH_PROMOTED, "widened", map[string]any{"field": "a"})
	rep := &ImportReport{
		RowsImported: 3,
		Schema:       &encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeF64}}},
		RowErrors: []RowError{
			{Row: 2, Err: perrors.NewCodedErrorWithDetails(perrors.PULSE_IMPORT_ROW_ERROR, "bad cell", map[string]any{"column": "a"})},
			{Row: 4, Err: stderrors.New("plain failure")},
		},
		PromotedFields:  []string{"a"},
		WidthWarnings:   []*perrors.CodedError{warn},
		ZoneWarnings:    []*perrors.CodedError{warn},
		SourceWarnings:  []*perrors.CodedError{warn},
		ElidedConstants: []string{"a"},
		Groups:          []GroupReport{{Label: "g"}},
		GroupWarnings:   []*perrors.CodedError{warn},
		DroppedWarnings: []*perrors.CodedError{warn},
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rows_imported", "schema", "row_errors", "promoted_fields", "width_warnings",
		"zone_warnings", "source_warnings", "elided_constants", "groups", "group_warnings", "dropped_warnings"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in %s", k, raw)
		}
	}
	if got["rows_imported"] != float64(3) {
		t.Errorf("rows_imported = %v, want 3", got["rows_imported"])
	}
	fields := got["schema"].(map[string]any)["fields"].([]any)
	if typ := fields[0].(map[string]any)["type"]; typ != "f64" {
		t.Errorf("schema.fields[0].type = %#v, want \"f64\"", typ)
	}
	rowErrs := got["row_errors"].([]any)
	first := rowErrs[0].(map[string]any)
	if first["row"] != float64(2) || first["code"] != "PULSE_IMPORT_ROW_ERROR" || first["message"] != "bad cell" ||
		first["details"].(map[string]any)["column"] != "a" {
		t.Errorf("row_errors[0] = %v, want {row 2, code, message, details}", first)
	}
	second := rowErrs[1].(map[string]any)
	if second["row"] != float64(4) || second["message"] != "plain failure" {
		t.Errorf("row_errors[1] = %v, want {row 4, message}", second)
	}
	if _, ok := second["code"]; ok {
		t.Errorf("uncoded row error carries a code: %v", second)
	}

	// A clean report omits every empty warning list.
	clean, _ := json.Marshal(&ImportReport{RowsImported: 1})
	var cm map[string]any
	_ = json.Unmarshal(clean, &cm)
	for _, k := range []string{"width_warnings", "zone_warnings", "group_warnings", "dropped_warnings", "groups", "elided_constants"} {
		if _, ok := cm[k]; ok {
			t.Errorf("clean report carries %q: %s", k, clean)
		}
	}
}
