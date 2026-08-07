package template_test

import (
	"encoding/json"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
)

// validBody is a minimal non-empty JSON object body. Declaration
// validation never renders it, so its contents only need to be a
// well-formed object.
const validBody = `{"cohort":{"filename":"sales.pulse"}}`

// tmpl builds a baseline-valid template and applies the given mutation, so
// every reject case in the table differs from the accepted baseline in
// exactly one way.
func tmpl(mutate func(*template.Template)) *template.Template {
	t := &template.Template{
		Name:        "finance/revenue",
		Description: "Revenue by region",
		Target:      template.TargetRequest,
		Variables: []*template.Variable{
			{Name: "metric", Type: template.VarField, Required: true},
		},
		Body: json.RawMessage(validBody),
	}
	if mutate != nil {
		mutate(t)
	}
	return t
}

// only replaces the baseline's variable list with a single declaration.
func only(v *template.Variable) func(*template.Template) {
	return func(t *template.Template) { t.Variables = []*template.Variable{v} }
}

// TestValidate_Matrix is the declaration-validation contract: one row per
// accepted shape and per rejected shape, each naming the code it must
// raise. wantCode empty means the declaration must validate.
func TestValidate_Matrix(t *testing.T) {
	tests := []struct {
		name     string
		tmpl     *template.Template
		wantCode perr.Code
		wantVar  string // expected DetailVariable, when variable-scoped
	}{
		// ---- accepted ----
		{
			name: "baseline valid",
			tmpl: tmpl(nil),
		},
		{
			name: "no variables at all",
			tmpl: tmpl(func(t *template.Template) { t.Variables = nil }),
		},
		{
			name: "no name (the store derives it)",
			tmpl: tmpl(func(t *template.Template) { t.Name = "" }),
		},
		{
			name: "no description",
			tmpl: tmpl(func(t *template.Template) { t.Description = "" }),
		},
		{
			name: "every target spelling: composed",
			tmpl: tmpl(func(t *template.Template) { t.Target = template.TargetComposed }),
		},
		{
			name: "every target spelling: chain",
			tmpl: tmpl(func(t *template.Template) { t.Target = template.TargetChain }),
		},
		{
			name: "every target spelling: facet",
			tmpl: tmpl(func(t *template.Template) { t.Target = template.TargetFacet }),
		},
		{
			name: "every target spelling: sample",
			tmpl: tmpl(func(t *template.Template) { t.Target = template.TargetSample }),
		},
		{
			name: "enum with values",
			tmpl: tmpl(only(&template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold", "silver"},
			})),
		},
		{
			name: "list of scalars",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
			})),
		},
		{
			name: "list of dates",
			tmpl: tmpl(only(&template.Variable{
				Name: "days", Type: template.VarList, Items: template.VarDate,
			})),
		},
		{
			name: "integer default with no fractional part",
			tmpl: tmpl(only(&template.Variable{
				Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`10`),
			})),
		},
		{
			name: "integer default written as 1.0",
			tmpl: tmpl(only(&template.Variable{
				Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`1.0`),
			})),
		},
		{
			name: "integer default beyond int64 range but integral",
			tmpl: tmpl(only(&template.Variable{
				Name: "big", Type: template.VarInteger, Default: json.RawMessage(`12345678901234567890`),
			})),
		},
		{
			name: "number default fractional",
			tmpl: tmpl(only(&template.Variable{
				Name: "rate", Type: template.VarNumber, Default: json.RawMessage(`0.15`),
			})),
		},
		{
			name: "number default integral",
			tmpl: tmpl(only(&template.Variable{
				Name: "rate", Type: template.VarNumber, Default: json.RawMessage(`3`),
			})),
		},
		{
			name: "boolean default false",
			tmpl: tmpl(only(&template.Variable{
				Name: "verbose", Type: template.VarBoolean, Default: json.RawMessage(`false`),
			})),
		},
		{
			name: "date default is a string",
			tmpl: tmpl(only(&template.Variable{
				Name: "since", Type: template.VarDate, Default: json.RawMessage(`"2024-01-01"`),
			})),
		},
		{
			name: "period default naming a range table",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod, Default: json.RawMessage(`{"table":"fiscal"}`),
			})),
		},
		{
			name: "period default carrying an inline range set",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod,
				Default: json.RawMessage(`{"ranges":[{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}]}`),
			})),
		},
		{
			name: "period default with open boundaries",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod,
				Default: json.RawMessage(`{"ranges":[{"label":"early","end":"2024-03-31"},{"label":"late","start":"2024-04-01"}]}`),
			})),
		},
		{
			name: "enum default that is a declared member",
			tmpl: tmpl(only(&template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold", "silver"},
				Default: json.RawMessage(`"silver"`),
			})),
		},
		{
			name: "list default whose elements match items",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
				Default: json.RawMessage(`["north","south"]`),
			})),
		},
		{
			name: "empty list default",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
				Default: json.RawMessage(`[]`),
			})),
		},
		{
			name: "explicit null default is no default",
			tmpl: tmpl(only(&template.Variable{
				Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`null`),
			})),
		},

		// ---- rejected: target ----
		{
			name:     "absent target",
			tmpl:     tmpl(func(t *template.Template) { t.Target = "" }),
			wantCode: perr.PULSE_TEMPLATE_TARGET_UNKNOWN,
		},
		{
			name:     "unrecognized target",
			tmpl:     tmpl(func(t *template.Template) { t.Target = "process" }),
			wantCode: perr.PULSE_TEMPLATE_TARGET_UNKNOWN,
		},
		{
			name:     "target in Go type spelling rather than wire spelling",
			tmpl:     tmpl(func(t *template.Template) { t.Target = "ComposedRequest" }),
			wantCode: perr.PULSE_TEMPLATE_TARGET_UNKNOWN,
		},

		// ---- rejected: body ----
		{
			name:     "absent body",
			tmpl:     tmpl(func(t *template.Template) { t.Body = nil }),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "blank body",
			tmpl:     tmpl(func(t *template.Template) { t.Body = json.RawMessage("   ") }),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "null body",
			tmpl:     tmpl(func(t *template.Template) { t.Body = json.RawMessage(`null`) }),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "empty object body",
			tmpl:     tmpl(func(t *template.Template) { t.Body = json.RawMessage(`{}`) }),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "body is an array",
			tmpl:     tmpl(func(t *template.Template) { t.Body = json.RawMessage(`[{"a":1}]`) }),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "body is a scalar",
			tmpl:     tmpl(func(t *template.Template) { t.Body = json.RawMessage(`"sales.pulse"`) }),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "body is malformed JSON",
			tmpl:     tmpl(func(t *template.Template) { t.Body = json.RawMessage(`{"cohort":`) }),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},

		// ---- rejected: variable identity ----
		{
			name: "duplicate variable names",
			tmpl: tmpl(func(t *template.Template) {
				t.Variables = []*template.Variable{
					{Name: "metric", Type: template.VarField},
					{Name: "metric", Type: template.VarString},
				}
			}),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "metric",
		},
		{
			name:     "empty variable name",
			tmpl:     tmpl(only(&template.Variable{Name: "", Type: template.VarString})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "",
		},
		{
			name: "null variable declaration",
			tmpl: tmpl(func(t *template.Template) {
				t.Variables = []*template.Variable{nil}
			}),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},

		// ---- rejected: variable type ----
		{
			name:     "absent variable type",
			tmpl:     tmpl(only(&template.Variable{Name: "metric"})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "metric",
		},
		{
			name:     "unrecognized variable type",
			tmpl:     tmpl(only(&template.Variable{Name: "metric", Type: "float"})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "metric",
		},

		// ---- rejected: enum ----
		{
			name:     "enum with no values",
			tmpl:     tmpl(only(&template.Variable{Name: "tier", Type: template.VarEnum})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "tier",
		},
		{
			name: "enum with empty values slice",
			tmpl: tmpl(only(&template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{},
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "tier",
		},

		// ---- rejected: list ----
		{
			name:     "list with no items",
			tmpl:     tmpl(only(&template.Variable{Name: "segments", Type: template.VarList})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "segments",
		},
		{
			name: "list of lists",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarList,
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "segments",
		},
		{
			name: "list of enum is non-scalar",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarEnum,
				Values: []string{"gold"},
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "segments",
		},
		{
			name: "list of period is non-scalar",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarPeriod,
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "segments",
		},
		{
			name: "list with unrecognized items type",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: "float",
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "segments",
		},

		// ---- rejected: default contradicts its own declared type ----
		{
			name: "string default on an integer variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`"10"`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "bucket",
		},
		{
			name: "fractional default on an integer variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`1.5`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "bucket",
		},
		{
			name: "string default on a number variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "rate", Type: template.VarNumber, Default: json.RawMessage(`"0.15"`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "rate",
		},
		{
			name: "quoted default on a boolean variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "verbose", Type: template.VarBoolean, Default: json.RawMessage(`"true"`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "verbose",
		},
		{
			name: "number default on a string variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "label", Type: template.VarString, Default: json.RawMessage(`7`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "label",
		},
		{
			name: "number default on a field variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "metric", Type: template.VarField, Default: json.RawMessage(`7`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "metric",
		},
		{
			name: "number default on a date variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "since", Type: template.VarDate, Default: json.RawMessage(`20240101`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "since",
		},
		{
			name: "number default on an enum variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold"},
				Default: json.RawMessage(`1`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "tier",
		},
		{
			name: "array default on a period variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod, Default: json.RawMessage(`[]`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "window",
		},
		{
			name: "object default on a list variable",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
				Default: json.RawMessage(`{"a":1}`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "segments",
		},
		{
			name: "list default element contradicts items type",
			tmpl: tmpl(only(&template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
				Default: json.RawMessage(`["north",7]`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "segments",
		},
		{
			name: "list default element is fractional under integer items",
			tmpl: tmpl(only(&template.Variable{
				Name: "buckets", Type: template.VarList, Items: template.VarInteger,
				Default: json.RawMessage(`[1, 2.5]`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "buckets",
		},
		{
			name: "malformed default JSON",
			tmpl: tmpl(only(&template.Variable{
				Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`{`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "bucket",
		},

		// ---- rejected: default fails its declared type's VALUE rule ----
		// Default checking is semantic, not merely JSON-kind deep, and it
		// runs here at declaration time so a template carrying an
		// unparseable date (or a non-member enum default, or a malformed
		// period) fails when it is registered rather than lying in wait
		// until someone renders it. Every one of these is a template
		// AUTHOR fault, so the code is PULSE_TEMPLATE_INVALID and never a
		// caller-facing PULSE_TEMPLATE_VAR_* code.
		{
			name: "enum default is not a declared member",
			tmpl: tmpl(only(&template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold", "silver"},
				Default: json.RawMessage(`"bronze"`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "tier",
		},
		{
			name: "enum default differs from a member only by case",
			tmpl: tmpl(only(&template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold"},
				Default: json.RawMessage(`"Gold"`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "tier",
		},
		{
			name: "date default does not parse",
			tmpl: tmpl(only(&template.Variable{
				Name: "since", Type: template.VarDate, Default: json.RawMessage(`"01/01/2024"`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "since",
		},
		{
			name: "date default names a day that does not exist",
			tmpl: tmpl(only(&template.Variable{
				Name: "since", Type: template.VarDate, Default: json.RawMessage(`"2024-02-30"`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "since",
		},
		{
			name: "list-of-dates default has an unparseable element",
			tmpl: tmpl(only(&template.Variable{
				Name: "days", Type: template.VarList, Items: template.VarDate,
				Default: json.RawMessage(`["2024-01-01","yesterday"]`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "days",
		},
		{
			name: "period default carries both ranges and table",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod,
				Default: json.RawMessage(`{"table":"fiscal","ranges":[{"label":"Q1","start":"2024-01-01"}]}`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "window",
		},
		{
			name: "period default carries neither ranges nor table",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod, Default: json.RawMessage(`{}`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "window",
		},
		{
			name: "period default range has an unparseable boundary",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod,
				Default: json.RawMessage(`{"ranges":[{"label":"Q1","start":"2024-1-1"}]}`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "window",
		},
		{
			name: "period default range has a misspelled boundary key",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod,
				Default: json.RawMessage(`{"ranges":[{"label":"Q1","startt":"2024-01-01"}]}`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "window",
		},
		{
			name: "period default range has no label",
			tmpl: tmpl(only(&template.Variable{
				Name: "window", Type: template.VarPeriod,
				Default: json.RawMessage(`{"ranges":[{"start":"2024-01-01","end":"2024-03-31"}]}`),
			})),
			wantCode: perr.PULSE_TEMPLATE_INVALID,
			wantVar:  "window",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := template.Validate(tt.tmpl)

			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("Validate() = nil, want %s", tt.wantCode)
			}
			if !perr.HasCode(err, tt.wantCode) {
				t.Fatalf("Validate() = %v, want code %s", err, tt.wantCode)
			}

			ce := codedError(t, err)
			if got, ok := ce.Details[perr.DetailTemplate]; !ok {
				t.Errorf("error details missing %q key", perr.DetailTemplate)
			} else if got != tt.tmpl.Name {
				t.Errorf("details[%q] = %v, want %q", perr.DetailTemplate, got, tt.tmpl.Name)
			}
			if tt.wantVar != "" || hasVariableDetail(ce) {
				got, ok := ce.Details[perr.DetailVariable]
				if !ok {
					t.Fatalf("error details missing %q key", perr.DetailVariable)
				}
				if got != tt.wantVar {
					t.Errorf("details[%q] = %v, want %q", perr.DetailVariable, got, tt.wantVar)
				}
			}
			if strings.TrimSpace(ce.Message) == "" {
				t.Error("coded error carries an empty message")
			}
		})
	}
}

// codedError extracts the *CodedError from an error chain.
func codedError(t *testing.T, err error) *perr.CodedError {
	t.Helper()
	ce, ok := err.(*perr.CodedError)
	if !ok {
		t.Fatalf("error %v is not a *errors.CodedError", err)
	}
	return ce
}

// hasVariableDetail reports whether the error is variable-scoped.
func hasVariableDetail(ce *perr.CodedError) bool {
	_, ok := ce.Details[perr.DetailVariable]
	return ok
}

// TestValidate_RequiredWithDefaultIsLegal is the explicit anti-regression
// for the easiest thing to get wrong here: required plus a default is NOT
// a contradiction. The default resolves the variable, so a required
// variable carrying one can never go missing at render.
func TestValidate_RequiredWithDefaultIsLegal(t *testing.T) {
	cases := []*template.Variable{
		{Name: "label", Type: template.VarString, Required: true, Default: json.RawMessage(`"all regions"`)},
		{Name: "bucket", Type: template.VarInteger, Required: true, Default: json.RawMessage(`10`)},
		{Name: "verbose", Type: template.VarBoolean, Required: true, Default: json.RawMessage(`false`)},
		{Name: "rate", Type: template.VarNumber, Required: true, Default: json.RawMessage(`0`)},
		{Name: "tier", Type: template.VarEnum, Required: true, Values: []string{"gold"}, Default: json.RawMessage(`"gold"`)},
		{Name: "segs", Type: template.VarList, Required: true, Items: template.VarString, Default: json.RawMessage(`[]`)},
	}
	for _, v := range cases {
		t.Run(v.Name, func(t *testing.T) {
			if err := template.Validate(tmpl(only(v))); err != nil {
				t.Errorf("required+default rejected for %s variable: %v", v.Type, err)
			}
		})
	}
}

// TestValidate_TargetDetailNamesOffendingValue asserts the target error
// carries the rejected spelling, so a caller sees what they wrote.
func TestValidate_TargetDetailNamesOffendingValue(t *testing.T) {
	err := template.Validate(tmpl(func(tm *template.Template) { tm.Target = "Request" }))
	if err == nil {
		t.Fatal("Validate() = nil, want PULSE_TEMPLATE_TARGET_UNKNOWN")
	}
	ce := codedError(t, err)
	if ce.Code != perr.PULSE_TEMPLATE_TARGET_UNKNOWN {
		t.Fatalf("code = %s, want PULSE_TEMPLATE_TARGET_UNKNOWN", ce.Code)
	}
	if got := ce.Details["target"]; got != "Request" {
		t.Errorf("details[\"target\"] = %v, want \"Request\"", got)
	}
	if !strings.Contains(ce.Message, `"request"`) {
		t.Errorf("message %q does not list the valid lowercase spellings", ce.Message)
	}
}

// TestValidate_NilTemplate asserts a nil document is rejected rather than
// panicking.
func TestValidate_NilTemplate(t *testing.T) {
	err := template.Validate(nil)
	if err == nil {
		t.Fatal("Validate(nil) = nil, want PULSE_TEMPLATE_INVALID")
	}
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Errorf("Validate(nil) = %v, want PULSE_TEMPLATE_INVALID", err)
	}
}

// TestParse_ValidatesAndRejectsMalformedJSON asserts Parse runs the same
// declaration validation and reports malformed document bytes under
// PULSE_TEMPLATE_INVALID.
func TestParse_ValidatesAndRejectsMalformedJSON(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		wantCode perr.Code
	}{
		{
			name: "valid document",
			doc:  `{"target":"request","body":` + validBody + `}`,
		},
		{
			name:     "malformed JSON",
			doc:      `{"target":"request",`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "not an object",
			doc:      `["target"]`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "declaration fault surfaces from Parse",
			doc:      `{"target":"nope","body":` + validBody + `}`,
			wantCode: perr.PULSE_TEMPLATE_TARGET_UNKNOWN,
		},
		{
			name:     "variable fault surfaces from Parse",
			doc:      `{"target":"request","variables":[{"name":"a","type":"list"}],"body":` + validBody + `}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := template.Parse([]byte(tt.doc))
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("Parse() = %v, want nil", err)
				}
				if got == nil {
					t.Fatal("Parse() returned a nil template with no error")
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse() = nil error, want %s", tt.wantCode)
			}
			if !perr.HasCode(err, tt.wantCode) {
				t.Errorf("Parse() = %v, want code %s", err, tt.wantCode)
			}
			if got != nil {
				t.Error("Parse() returned a template alongside an error")
			}
		})
	}
}

// TestParse_UnknownWrapperKeysRejected pins the strict wrapper decode. A
// typo'd "varaibles" would otherwise parse cleanly into a template with
// zero variables and fail much later — the exact silent-failure class this
// surface exists to eliminate — so the wrapper and the variable
// declarations are decoded with DisallowUnknownFields.
func TestParse_UnknownWrapperKeysRejected(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{
			name: "unknown wrapper key",
			doc:  `{"target":"request","author":"finance","body":` + validBody + `}`,
		},
		{
			name: "misspelled variables key",
			doc:  `{"target":"request","varaibles":[],"body":` + validBody + `}`,
		},
		{
			name: "unknown key on a variable declaration",
			doc: `{"target":"request","variables":[{"name":"a","type":"integer","deafult":1}],"body":` +
				validBody + `}`,
		},
		{
			name: "trailing content after the document",
			doc:  `{"target":"request","body":` + validBody + `} {"target":"request"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := template.Parse([]byte(tt.doc))
			if err == nil {
				t.Fatal("Parse() = nil error, want PULSE_TEMPLATE_INVALID")
			}
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
				t.Errorf("Parse() = %v, want PULSE_TEMPLATE_INVALID", err)
			}
			if got != nil {
				t.Error("Parse() returned a template alongside an error")
			}
		})
	}
}

// TestParse_BodyStaysUnstrict is the other half of that boundary: the
// strictness stops at `body`. Before substitution the body is not a
// request and its markers are not request fields, so it stays raw JSON
// here and is strict-decoded against the target type only after rendering.
func TestParse_BodyStaysUnstrict(t *testing.T) {
	doc := `{"target":"request","variables":[{"name":"x","type":"string"}],` +
		`"body":{"cohort":{"filename":"sales.pulse"},"not_a_request_field":{"$var":"x"}}}`
	if _, err := template.Parse([]byte(doc)); err != nil {
		t.Errorf("Parse() = %v, want nil — body keys are not checked until the rendered strict decode", err)
	}
}

// TestParse_FixtureValidates asserts the on-disk fixture is a valid
// declaration — it is the shape every later story renders against.
func TestParse_FixtureValidates(t *testing.T) {
	if _, err := template.Parse(readFixture(t)); err != nil {
		t.Fatalf("Parse(fixture) = %v, want nil", err)
	}
}
