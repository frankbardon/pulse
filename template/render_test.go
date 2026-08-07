package template_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
)

// canonical normalises a JSON literal the way RenderJSON emits: object
// keys sorted (Go's map marshaling does that), number literals preserved
// exactly (UseNumber), HTML escaping off. Comparing against the canonical
// form of an expectation keeps the table rows readable — a row can write
// its keys in template order — while still pinning number formatting
// byte-for-byte, which is the fidelity the walk has to guarantee.
func canonical(t *testing.T, s string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		t.Fatalf("canonical(%s): %v", s, err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(tree); err != nil {
		t.Fatalf("canonical(%s): re-encode: %v", s, err)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// renderTmpl builds a template from a declaration list and a raw body.
func renderTmpl(vars []*template.Variable, body string) *template.Template {
	return &template.Template{
		Name:      "finance/revenue",
		Target:    template.TargetRequest,
		Variables: vars,
		Body:      json.RawMessage(body),
	}
}

// decls is shorthand for a declaration list.
func decls(v ...*template.Variable) []*template.Variable { return v }

// varDecl builds one declaration.
func varDecl(name string, vt template.VarType) *template.Variable {
	return &template.Variable{Name: name, Type: vt}
}

// listDecl builds a list declaration with the given element type.
func listDecl(name string, items template.VarType) *template.Variable {
	return &template.Variable{Name: name, Type: template.VarList, Items: items}
}

// renderRow is one case in the render matrix. A row either renders to
// wantJSON or fails with wantCode — never both.
type renderRow struct {
	name     string
	vars     []*template.Variable
	body     string
	supplied map[string]any
	wantJSON string
	wantCode perr.Code
	wantVar  string // expected DetailVariable when the fault is variable-scoped
}

// runRenderRows executes a render-matrix table.
func runRenderRows(t *testing.T, rows []renderRow) {
	t.Helper()
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			tm := renderTmpl(tt.vars, tt.body)
			got, err := template.RenderJSON(tm, tt.supplied)

			if tt.wantCode != "" {
				if err == nil {
					t.Fatalf("RenderJSON() = %s, want code %s", got, tt.wantCode)
				}
				if !perr.HasCode(err, tt.wantCode) {
					t.Fatalf("RenderJSON() = %v, want code %s", err, tt.wantCode)
				}
				ce := codedError(t, err)
				if ce.Details[perr.DetailTemplate] != tm.Name {
					t.Errorf("details[%q] = %v, want %q",
						perr.DetailTemplate, ce.Details[perr.DetailTemplate], tm.Name)
				}
				if tt.wantVar != "" {
					if got := ce.Details[perr.DetailVariable]; got != tt.wantVar {
						t.Errorf("details[%q] = %v, want %q", perr.DetailVariable, got, tt.wantVar)
					}
				}
				if strings.TrimSpace(ce.Message) == "" {
					t.Error("coded error carries an empty message")
				}
				if got != nil {
					t.Errorf("RenderJSON() returned %s alongside an error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("RenderJSON() = %v, want nil", err)
			}
			if want := canonical(t, tt.wantJSON); string(got) != want {
				t.Errorf("RenderJSON() =\n  %s\nwant\n  %s", got, want)
			}
			if !json.Valid(got) {
				t.Errorf("RenderJSON() emitted invalid JSON: %s", got)
			}
		})
	}
}

// TestRenderJSON_Matrix is the render-walk contract: markers, string
// sugar, guards, and every fault each can raise, one row apiece.
func TestRenderJSON_Matrix(t *testing.T) {
	runRenderRows(t, []renderRow{
		// ---- marker replacement is type-preserving, per variable type ----
		{
			name:     "marker splices a string",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"field":{"$var":"metric"}}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"field":"revenue"}`,
		},
		{
			name:     "marker splices a field name",
			vars:     decls(varDecl("metric", template.VarField)),
			body:     `{"field":{"$var":"metric"}}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"field":"revenue"}`,
		},
		{
			name:     "marker splices an integer as a number, not a string",
			vars:     decls(varDecl("bucket", template.VarInteger)),
			body:     `{"interval":{"$var":"bucket"}}`,
			supplied: map[string]any{"bucket": 10},
			wantJSON: `{"interval":10}`,
		},
		{
			name:     "marker splices a fractional number",
			vars:     decls(varDecl("rate", template.VarNumber)),
			body:     `{"rate":{"$var":"rate"}}`,
			supplied: map[string]any{"rate": 0.15},
			wantJSON: `{"rate":0.15}`,
		},
		{
			name:     "marker splices a boolean",
			vars:     decls(varDecl("verbose", template.VarBoolean)),
			body:     `{"verbose":{"$var":"verbose"}}`,
			supplied: map[string]any{"verbose": false},
			wantJSON: `{"verbose":false}`,
		},
		{
			name:     "marker splices an enum member as a string",
			vars:     decls(&template.Variable{Name: "tier", Type: template.VarEnum, Values: []string{"gold", "silver"}}),
			body:     `{"tier":{"$var":"tier"}}`,
			supplied: map[string]any{"tier": "silver"},
			wantJSON: `{"tier":"silver"}`,
		},
		{
			name:     "marker splices a date as its ISO string",
			vars:     decls(varDecl("since", template.VarDate)),
			body:     `{"since":{"$var":"since"}}`,
			supplied: map[string]any{"since": "2024-01-01"},
			wantJSON: `{"since":"2024-01-01"}`,
		},
		{
			name:     "marker splices a list as an array",
			vars:     decls(listDecl("segments", template.VarString)),
			body:     `{"include":{"$var":"segments"}}`,
			supplied: map[string]any{"segments": []string{"north", "south"}},
			wantJSON: `{"include":["north","south"]}`,
		},
		{
			name: "marker splices a period as an object",
			vars: decls(varDecl("window", template.VarPeriod)),
			body: `{"params":{"$var":"window"}}`,
			supplied: map[string]any{"window": map[string]any{
				"ranges": []any{map[string]any{"label": "q1", "start": "2024-01-01", "end": "2024-03-31"}},
			}},
			wantJSON: `{"params":{"ranges":[{"end":"2024-03-31","label":"q1","start":"2024-01-01"}]}}`,
		},
		{
			name:     "marker splices an empty list, not null",
			vars:     decls(listDecl("segments", template.VarString)),
			body:     `{"include":{"$var":"segments"}}`,
			supplied: map[string]any{"segments": []string{}},
			wantJSON: `{"include":[]}`,
		},

		// ---- marker recognition is exact ----
		{
			name:     "$var alongside another key is literal data",
			vars:     decls(varDecl("x", template.VarString)),
			body:     `{"slot":{"$var":"x","other":1}}`,
			supplied: map[string]any{"x": "ignored"},
			wantJSON: `{"slot":{"$var":"x","other":1}}`,
		},
		{
			name:     "a bare $name string is not a marker",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"field":"$metric"}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"field":"$metric"}`,
		},

		// ---- string sugar ----
		{
			name:     "token interpolates inside a string value",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"label":"{{metric}} total"}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"label":"revenue total"}`,
		},
		{
			name:     "several tokens interpolate in one string",
			vars:     decls(varDecl("a", template.VarString), varDecl("b", template.VarString)),
			body:     `{"label":"{{a}}-{{b}}-{{a}}"}`,
			supplied: map[string]any{"a": "x", "b": "y"},
			wantJSON: `{"label":"x-y-x"}`,
		},
		{
			name:     "token renders a number as its canonical literal",
			vars:     decls(varDecl("bucket", template.VarInteger)),
			body:     `{"label":"bucket {{bucket}}"}`,
			supplied: map[string]any{"bucket": 10},
			wantJSON: `{"label":"bucket 10"}`,
		},
		{
			name:     "token renders a boolean as true/false",
			vars:     decls(varDecl("verbose", template.VarBoolean)),
			body:     `{"label":"verbose={{verbose}}"}`,
			supplied: map[string]any{"verbose": true},
			wantJSON: `{"label":"verbose=true"}`,
		},
		{
			name:     "token renders a date as its ISO string",
			vars:     decls(varDecl("since", template.VarDate)),
			body:     `{"label":"since {{since}}"}`,
			supplied: map[string]any{"since": "2024-01-01"},
			wantJSON: `{"label":"since 2024-01-01"}`,
		},
		{
			name:     "surrounding whitespace inside a token is trimmed",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"label":"{{  metric  }}"}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"label":"revenue"}`,
		},
		{
			name:     "quadrupled brace escapes to a literal double brace",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"label":"{{{{metric}} is not a token"}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"label":"{{metric}} is not a token"}`,
		},
		{
			name:     "escape and token compose in one string",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"label":"{{{{}} {{metric}}"}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"label":"{{}} revenue"}`,
		},
		{
			name:     "a lone closing brace pair is ordinary text",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"label":"}} tail"}`,
			supplied: map[string]any{"metric": "revenue"},
			wantJSON: `{"label":"}} tail"}`,
		},
		{
			name:     "a list variable inside a string is a type fault",
			vars:     decls(listDecl("segments", template.VarString)),
			body:     `{"label":"{{segments}}"}`,
			supplied: map[string]any{"segments": []string{"north"}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
			wantVar:  "segments",
		},
		{
			name: "a period variable inside a string is a type fault",
			vars: decls(varDecl("window", template.VarPeriod)),
			body: `{"label":"{{window}}"}`,
			supplied: map[string]any{"window": map[string]any{
				"ranges": []any{map[string]any{"label": "q1", "start": "2024-01-01", "end": "2024-03-31"}},
			}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
			wantVar:  "window",
		},

		// ---- guards ----
		{
			name:     "passing guard is stripped from the output",
			vars:     decls(varDecl("x", template.VarString)),
			body:     `{"slot":{"$when":"x","type":"AGG_SUM"}}`,
			supplied: map[string]any{"x": "present"},
			wantJSON: `{"slot":{"type":"AGG_SUM"}}`,
		},
		{
			name:     "failing guard removes the parent object key entirely",
			vars:     decls(varDecl("x", template.VarString)),
			body:     `{"keep":1,"slot":{"$when":"x","type":"AGG_SUM"}}`,
			wantJSON: `{"keep":1}`,
		},
		{
			name:     "failing guard removes an array element and compacts the slice",
			vars:     decls(varDecl("x", template.VarString)),
			body:     `{"groups":[{"type":"a"},{"$when":"x","type":"b"},{"type":"c"}]}`,
			wantJSON: `{"groups":[{"type":"a"},{"type":"c"}]}`,
		},
		{
			name:     "every array element dropped leaves an empty array, not null",
			vars:     decls(varDecl("x", template.VarString)),
			body:     `{"groups":[{"$when":"x","type":"b"}]}`,
			wantJSON: `{"groups":[]}`,
		},
		{
			name:     "a dropped block containing an unresolved marker drops cleanly",
			vars:     decls(varDecl("x", template.VarString), varDecl("y", template.VarString)),
			body:     `{"groups":[{"$when":"x","field":{"$var":"y"}}]}`,
			wantJSON: `{"groups":[]}`,
		},
		{
			name:     "a dropped block containing an unresolved token drops cleanly",
			vars:     decls(varDecl("x", template.VarString), varDecl("y", template.VarString)),
			body:     `{"groups":[{"$when":"x","label":"{{y}}"}]}`,
			wantJSON: `{"groups":[]}`,
		},
		{
			name:     "a dropped block containing a nested dropped block drops cleanly",
			vars:     decls(varDecl("x", template.VarString), varDecl("y", template.VarString)),
			body:     `{"groups":[{"$when":"x","inner":{"$when":"y","field":{"$var":"y"}}}]}`,
			wantJSON: `{"groups":[]}`,
		},
		{
			name:     "a surviving outer block still drops its failing inner block",
			vars:     decls(varDecl("x", template.VarString), varDecl("y", template.VarString)),
			body:     `{"outer":{"$when":"x","keep":1,"inner":{"$when":"y","drop":1}}}`,
			supplied: map[string]any{"x": "present"},
			wantJSON: `{"outer":{"keep":1}}`,
		},

		// ---- unresolved is a fault, never a silent omission ----
		{
			name:     "unguarded unresolved marker is a fault",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"field":{"$var":"metric"}}`,
			wantCode: perr.PULSE_TEMPLATE_UNRESOLVED,
			wantVar:  "metric",
		},
		{
			name:     "unguarded unresolved token is a fault",
			vars:     decls(varDecl("metric", template.VarString)),
			body:     `{"label":"{{metric}} total"}`,
			wantCode: perr.PULSE_TEMPLATE_UNRESOLVED,
			wantVar:  "metric",
		},
		{
			name:     "an unresolved marker beside a passing guard is still a fault",
			vars:     decls(varDecl("x", template.VarString), varDecl("y", template.VarString)),
			body:     `{"groups":[{"$when":"x","field":{"$var":"y"}}]}`,
			supplied: map[string]any{"x": "present"},
			wantCode: perr.PULSE_TEMPLATE_UNRESOLVED,
			wantVar:  "y",
		},

		// ---- literal passthrough ----
		{
			name:     "a body with no syntax renders unchanged",
			vars:     nil,
			body:     `{"cohort":{"filename":"sales.pulse"},"aggregations":[{"type":"AGG_COUNT","field":"id"}]}`,
			wantJSON: `{"cohort":{"filename":"sales.pulse"},"aggregations":[{"type":"AGG_COUNT","field":"id"}]}`,
		},
		{
			name:     "a JSON null in the body survives as null",
			vars:     nil,
			body:     `{"params":null}`,
			wantJSON: `{"params":null}`,
		},
	})
}

// TestRenderJSON_GuardsArePresenceNotTruthiness pins the semantics that
// make a legitimate zero templatable: a variable resolved to "", [], 0, or
// false is RESOLVED, and a block guarded on it stays.
func TestRenderJSON_GuardsArePresenceNotTruthiness(t *testing.T) {
	tests := []struct {
		name  string
		decl  *template.Variable
		value any
	}{
		{"empty string", varDecl("x", template.VarString), ""},
		{"empty list", listDecl("x", template.VarString), []string{}},
		{"zero", varDecl("x", template.VarInteger), 0},
		{"false", varDecl("x", template.VarBoolean), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := renderTmpl(decls(tt.decl), `{"groups":[{"$when":"x","type":"GROUP_CATEGORY"}]}`)
			got, err := template.RenderJSON(tm, map[string]any{"x": tt.value})
			if err != nil {
				t.Fatalf("RenderJSON() = %v, want nil", err)
			}
			want := canonical(t, `{"groups":[{"type":"GROUP_CATEGORY"}]}`)
			if string(got) != want {
				t.Errorf("RenderJSON() = %s, want %s — a falsy value is resolved and must keep its block", got, want)
			}
		})
	}
}

// TestRenderJSON_GuardsOnDefaultedVariableKeepBlock asserts a declared
// `default` resolves the variable, so a guard on it passes even though the
// caller supplied nothing.
func TestRenderJSON_GuardsOnDefaultedVariableKeepBlock(t *testing.T) {
	tm := renderTmpl(
		decls(&template.Variable{Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`10`)}),
		`{"groups":[{"$when":"bucket","interval":{"$var":"bucket"}}]}`)

	got, err := template.RenderJSON(tm, nil)
	if err != nil {
		t.Fatalf("RenderJSON() = %v, want nil", err)
	}
	if want := canonical(t, `{"groups":[{"interval":10}]}`); string(got) != want {
		t.Errorf("RenderJSON() = %s, want %s", got, want)
	}
}

// TestRenderJSON_IntegerFidelity asserts the UseNumber decode survives the
// whole walk. A u64 beyond float64's exact-integer range must reach the
// rendered request as the literal it was written as — both when it is
// baked into the body and when it is spliced through a marker.
func TestRenderJSON_IntegerFidelity(t *testing.T) {
	const big = "9007199254740993" // 2^53 + 1: not representable as a float64

	t.Run("literal in the body", func(t *testing.T) {
		tm := renderTmpl(nil, `{"id":`+big+`,"rate":1.50,"exp":1e3}`)
		got, err := template.RenderJSON(tm, nil)
		if err != nil {
			t.Fatalf("RenderJSON() = %v, want nil", err)
		}
		for _, literal := range []string{big, "1.50", "1e3"} {
			if !strings.Contains(string(got), literal) {
				t.Errorf("RenderJSON() = %s, lost the exact literal %s", got, literal)
			}
		}
	})

	t.Run("spliced through a marker", func(t *testing.T) {
		tm := renderTmpl(decls(varDecl("id", template.VarInteger)), `{"id":{"$var":"id"}}`)
		got, err := template.RenderJSON(tm, map[string]any{"id": uint64(9007199254740993)})
		if err != nil {
			t.Fatalf("RenderJSON() = %v, want nil", err)
		}
		if want := `{"id":` + big + `}`; string(got) != want {
			t.Errorf("RenderJSON() = %s, want %s", got, want)
		}
	})

	t.Run("interpolated into a string", func(t *testing.T) {
		tm := renderTmpl(decls(varDecl("id", template.VarInteger)), `{"label":"id {{id}}"}`)
		got, err := template.RenderJSON(tm, map[string]any{"id": uint64(9007199254740993)})
		if err != nil {
			t.Fatalf("RenderJSON() = %v, want nil", err)
		}
		if want := `{"label":"id ` + big + `"}`; string(got) != want {
			t.Errorf("RenderJSON() = %s, want %s", got, want)
		}
	})
}

// TestRenderJSON_SubstitutedValuesAreNotRewalked asserts substitution is
// one pass. A caller-supplied string that happens to look like template
// syntax is DATA — re-walking it would make every caller-supplied value an
// injection vector into the template grammar.
func TestRenderJSON_SubstitutedValuesAreNotRewalked(t *testing.T) {
	tm := renderTmpl(
		decls(varDecl("metric", template.VarString), varDecl("other", template.VarString)),
		`{"field":{"$var":"metric"},"label":"{{other}}"}`)

	got, err := template.RenderJSON(tm, map[string]any{
		"metric": "{{other}}",
		"other":  `{"$var":"metric"}`,
	})
	if err != nil {
		t.Fatalf("RenderJSON() = %v, want nil", err)
	}
	want := canonical(t, `{"field":"{{other}}","label":"{\"$var\":\"metric\"}"}`)
	if string(got) != want {
		t.Errorf("RenderJSON() =\n  %s\nwant\n  %s", got, want)
	}
}

// TestRenderJSON_UndeclaredIsAuthorErrorAtValidation is the separation the
// whole error family turns on. A body naming a variable the template does
// not DECLARE is a template author's typo: it is PULSE_TEMPLATE_INVALID
// and it fails at Validate, before any caller can render. A body naming a
// DECLARED variable that resolves to nothing is a different failure —
// PULSE_TEMPLATE_UNRESOLVED, and only knowable at render.
func TestRenderJSON_UndeclaredIsAuthorErrorAtValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"marker", `{"field":{"$var":"metrc"}}`},
		{"interpolation token", `{"label":"{{metrc}} total"}`},
		{"guard", `{"groups":[{"$when":"metrc","type":"a"}]}`},
		{"marker inside a guarded block", `{"groups":[{"$when":"metric","field":{"$var":"metrc"}}]}`},
		{"marker inside a nested array", `{"groups":[[{"field":{"$var":"metrc"}}]]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := renderTmpl(decls(varDecl("metric", template.VarField)), tt.body)

			err := template.Validate(tm)
			if err == nil {
				t.Fatal("Validate() = nil; an undeclared reference is an author error and must fail at declaration time")
			}
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
				t.Fatalf("Validate() = %v, want %s", err, perr.PULSE_TEMPLATE_INVALID)
			}
			ce := codedError(t, err)
			if got := ce.Details[perr.DetailVariable]; got != "metrc" {
				t.Errorf("details[%q] = %v, want \"metrc\"", perr.DetailVariable, got)
			}
			if got := ce.Details["path"]; got == nil || got == "" {
				t.Error("body fault carries no \"path\" detail")
			}

			// The same fault surfaces from a render, since Resolve
			// validates first — but it is still the author's code.
			if _, err := template.RenderJSON(tm, map[string]any{"metric": "revenue"}); !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
				t.Errorf("RenderJSON() = %v, want %s", err, perr.PULSE_TEMPLATE_INVALID)
			}
		})
	}
}

// TestRenderJSON_DeclaredButUnresolvedIsRenderError is the other half of
// that separation: the declaration validates, and only the render fails.
func TestRenderJSON_DeclaredButUnresolvedIsRenderError(t *testing.T) {
	tm := renderTmpl(decls(varDecl("metric", template.VarField)), `{"field":{"$var":"metric"}}`)

	if err := template.Validate(tm); err != nil {
		t.Fatalf("Validate() = %v, want nil — the variable IS declared", err)
	}
	_, err := template.RenderJSON(tm, nil)
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_UNRESOLVED) {
		t.Fatalf("RenderJSON() = %v, want %s", err, perr.PULSE_TEMPLATE_UNRESOLVED)
	}
	ce := codedError(t, err)
	if got := ce.Details[perr.DetailVariable]; got != "metric" {
		t.Errorf("details[%q] = %v, want \"metric\"", perr.DetailVariable, got)
	}
	if !strings.Contains(ce.Message, "$when") {
		t.Errorf("message %q does not point at the `$when` remedy", ce.Message)
	}
}

// TestValidate_BodySyntaxFaults covers the malformed-syntax rejections.
// Each of these would otherwise survive into the rendered request as
// literal data and fail the post-render strict decode with a far worse
// message, so they are caught at declaration time.
func TestValidate_BodySyntaxFaults(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // a fragment the message must carry
	}{
		{"lone $var holding a number", `{"field":{"$var":3}}`, "$var"},
		{"lone $var holding an object", `{"field":{"$var":{"a":1}}}`, "$var"},
		{"lone $var holding an empty name", `{"field":{"$var":"   "}}`, "empty variable name"},
		{"$when holding a number", `{"slot":{"$when":3,"a":1}}`, "$when"},
		{"$when holding an empty name", `{"slot":{"$when":"","a":1}}`, "$when"},
		{"$when on the root body", `{"$when":"metric","cohort":{}}`, "root"},
		{"unterminated token", `{"label":"{{metric"}`, "unterminated"},
		{"empty token", `{"label":"{{}}"}`, "empty interpolation token"},
		{"token in an object key", `{"{{metric}}":1}`, "object key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := renderTmpl(decls(varDecl("metric", template.VarField)), tt.body)
			err := template.Validate(tm)
			if err == nil {
				t.Fatal("Validate() = nil, want PULSE_TEMPLATE_INVALID")
			}
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
				t.Fatalf("Validate() = %v, want %s", err, perr.PULSE_TEMPLATE_INVALID)
			}
			if ce := codedError(t, err); !strings.Contains(ce.Message, tt.want) {
				t.Errorf("message %q does not mention %q", ce.Message, tt.want)
			}
		})
	}
}

// TestRenderJSON_RootGuardIsRejected asserts the root body cannot be
// guarded: the root IS the request, and dropping it would render nothing
// at all.
func TestRenderJSON_RootGuardIsRejected(t *testing.T) {
	tm := renderTmpl(decls(varDecl("metric", template.VarField)),
		`{"$when":"metric","cohort":{"filename":"sales.pulse"}}`)

	if _, err := template.RenderJSON(tm, map[string]any{"metric": "revenue"}); !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Fatalf("RenderJSON() = %v, want %s even with the guard variable resolved", err, perr.PULSE_TEMPLATE_INVALID)
	}
	if _, err := template.RenderJSON(tm, nil); !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Fatalf("RenderJSON() = %v, want %s", err, perr.PULSE_TEMPLATE_INVALID)
	}
}

// TestRenderJSON_ResolutionFaultsSurface asserts render does not swallow
// the resolution stage's faults — a bad caller value still reports its own
// caller-provenance code rather than an unresolved-marker fault.
func TestRenderJSON_ResolutionFaultsSurface(t *testing.T) {
	tests := []struct {
		name     string
		decl     *template.Variable
		supplied map[string]any
		want     perr.Code
	}{
		{
			name:     "wrong kind",
			decl:     varDecl("metric", template.VarInteger),
			supplied: map[string]any{"metric": "ten"},
			want:     perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "non-member enum",
			decl:     &template.Variable{Name: "metric", Type: template.VarEnum, Values: []string{"gold"}},
			supplied: map[string]any{"metric": "platinum"},
			want:     perr.PULSE_TEMPLATE_VAR_ENUM,
		},
		{
			name:     "unknown supplied name",
			decl:     varDecl("metric", template.VarString),
			supplied: map[string]any{"metrc": "revenue"},
			want:     perr.PULSE_TEMPLATE_VAR_UNKNOWN,
		},
		{
			name:     "required and missing",
			decl:     &template.Variable{Name: "metric", Type: template.VarString, Required: true},
			supplied: nil,
			want:     perr.PULSE_TEMPLATE_VAR_MISSING,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := renderTmpl(decls(tt.decl), `{"field":{"$var":"metric"}}`)
			got, err := template.RenderJSON(tm, tt.supplied)
			if !perr.HasCode(err, tt.want) {
				t.Fatalf("RenderJSON() = (%s, %v), want code %s", got, err, tt.want)
			}
			if got != nil {
				t.Errorf("RenderJSON() returned %s alongside an error", got)
			}
		})
	}
}

// TestRenderJSON_Fixture renders the on-disk fixture end to end: markers,
// interpolation, and a guard that drops in one call and survives in the
// next.
func TestRenderJSON_Fixture(t *testing.T) {
	tmpl, err := template.Parse(readFixture(t))
	if err != nil {
		t.Fatalf("Parse(fixture) = %v, want nil", err)
	}

	t.Run("guard drops the optional grouper", func(t *testing.T) {
		got, err := template.RenderJSON(tmpl, map[string]any{"metric": "revenue"})
		if err != nil {
			t.Fatalf("RenderJSON() = %v, want nil", err)
		}
		want := canonical(t, `{
			"label": "all regions since 2024-01-01",
			"cohort": {"filename": "sales.pulse"},
			"aggregations": [{"type":"AGG_SUM","field":"revenue"}],
			"groups": [{"type":"GROUP_RANGE","field":"revenue","interval":10}]
		}`)
		if string(got) != want {
			t.Errorf("RenderJSON() =\n  %s\nwant\n  %s", got, want)
		}
	})

	t.Run("guard keeps the optional grouper", func(t *testing.T) {
		got, err := template.RenderJSON(tmpl, map[string]any{
			"metric":   "revenue",
			"bucket":   25,
			"label":    "north and south",
			"segments": []string{"north", "south"},
		})
		if err != nil {
			t.Fatalf("RenderJSON() = %v, want nil", err)
		}
		want := canonical(t, `{
			"label": "north and south since 2024-01-01",
			"cohort": {"filename": "sales.pulse"},
			"aggregations": [{"type":"AGG_SUM","field":"revenue"}],
			"groups": [
				{"type":"GROUP_RANGE","field":"revenue","interval":25},
				{"type":"GROUP_CATEGORY","field":"region","include":["north","south"]}
			]
		}`)
		if string(got) != want {
			t.Errorf("RenderJSON() =\n  %s\nwant\n  %s", got, want)
		}
	})

	t.Run("the required variable is still required", func(t *testing.T) {
		if _, err := template.RenderJSON(tmpl, nil); !perr.HasCode(err, perr.PULSE_TEMPLATE_VAR_MISSING) {
			t.Fatalf("RenderJSON() = %v, want %s", err, perr.PULSE_TEMPLATE_VAR_MISSING)
		}
	})
}

// TestRenderJSON_NilTemplate asserts the entry point is nil-safe.
func TestRenderJSON_NilTemplate(t *testing.T) {
	got, err := template.RenderJSON(nil, nil)
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Fatalf("RenderJSON(nil) = (%s, %v), want %s", got, err, perr.PULSE_TEMPLATE_INVALID)
	}
}
