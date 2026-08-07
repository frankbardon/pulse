package template_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
)

// marshal renders a resolved value back to JSON so a table row can pin it
// as a literal. It also proves the resolved shape is re-marshalable, which
// the render walk depends on.
func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal resolved value %#v: %v", v, err)
	}
	return string(b)
}

// resolveRow is one accept/reject case in the per-type acceptance matrix.
// A row either resolves to wantJSON, or fails with wantCode — never both.
type resolveRow struct {
	name         string
	decl         *template.Variable
	supplied     map[string]any
	wantCode     perr.Code
	wantResolved bool
	wantJSON     string
}

// runResolveRows executes an acceptance-matrix table.
func runResolveRows(t *testing.T, rows []resolveRow) {
	t.Helper()
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			tm := tmpl(only(tt.decl))
			res, err := template.Resolve(tm, tt.supplied)

			if tt.wantCode != "" {
				if err == nil {
					t.Fatalf("Resolve() = nil error, want %s", tt.wantCode)
				}
				if !perr.HasCode(err, tt.wantCode) {
					t.Fatalf("Resolve() = %v, want code %s", err, tt.wantCode)
				}
				ce := codedError(t, err)
				if got := ce.Details[perr.DetailTemplate]; got != tm.Name {
					t.Errorf("details[%q] = %v, want %q", perr.DetailTemplate, got, tm.Name)
				}
				if got := ce.Details[perr.DetailVariable]; got != tt.decl.Name {
					t.Errorf("details[%q] = %v, want %q", perr.DetailVariable, got, tt.decl.Name)
				}
				if strings.TrimSpace(ce.Message) == "" {
					t.Error("coded error carries an empty message")
				}
				if res != nil {
					t.Error("Resolve() returned a resolution alongside an error")
				}
				return
			}

			if err != nil {
				t.Fatalf("Resolve() = %v, want nil", err)
			}
			got, ok := res.Get(tt.decl.Name)
			if ok != tt.wantResolved {
				t.Fatalf("Get(%q) resolved = %v, want %v (value %#v)", tt.decl.Name, ok, tt.wantResolved, got)
			}
			if !tt.wantResolved {
				return
			}
			if js := marshal(t, got); js != tt.wantJSON {
				t.Errorf("resolved value = %s, want %s", js, tt.wantJSON)
			}
		})
	}
}

// TestResolve_AcceptanceMatrix is the per-type contract: one accept and at
// least one reject row for every one of the nine declared variable types,
// exercised through a caller-supplied value.
func TestResolve_AcceptanceMatrix(t *testing.T) {
	runResolveRows(t, []resolveRow{
		// ---- string ----
		{
			name:         "string accepts a JSON string",
			decl:         &template.Variable{Name: "label", Type: template.VarString},
			supplied:     map[string]any{"label": "north"},
			wantResolved: true, wantJSON: `"north"`,
		},
		{
			name:     "string rejects a number",
			decl:     &template.Variable{Name: "label", Type: template.VarString},
			supplied: map[string]any{"label": 7},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "string rejects a bool",
			decl:     &template.Variable{Name: "label", Type: template.VarString},
			supplied: map[string]any{"label": true},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- field ----
		{
			name:         "field accepts a JSON string",
			decl:         &template.Variable{Name: "metric", Type: template.VarField},
			supplied:     map[string]any{"metric": "revenue"},
			wantResolved: true, wantJSON: `"revenue"`,
		},
		{
			name:     "field rejects a number",
			decl:     &template.Variable{Name: "metric", Type: template.VarField},
			supplied: map[string]any{"metric": 7},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- number ----
		{
			name:         "number accepts a fractional value",
			decl:         &template.Variable{Name: "rate", Type: template.VarNumber},
			supplied:     map[string]any{"rate": 0.15},
			wantResolved: true, wantJSON: `0.15`,
		},
		{
			name:         "number accepts an integral value",
			decl:         &template.Variable{Name: "rate", Type: template.VarNumber},
			supplied:     map[string]any{"rate": 3},
			wantResolved: true, wantJSON: `3`,
		},
		{
			name:     "number rejects a quoted number",
			decl:     &template.Variable{Name: "rate", Type: template.VarNumber},
			supplied: map[string]any{"rate": "0.15"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- integer ----
		{
			name:         "integer accepts 1",
			decl:         &template.Variable{Name: "bucket", Type: template.VarInteger},
			supplied:     map[string]any{"bucket": 1},
			wantResolved: true, wantJSON: `1`,
		},
		{
			name:         "integer accepts 1.0",
			decl:         &template.Variable{Name: "bucket", Type: template.VarInteger},
			supplied:     map[string]any{"bucket": json.RawMessage(`1.0`)},
			wantResolved: true, wantJSON: `1.0`,
		},
		{
			name:     "integer rejects 1.5",
			decl:     &template.Variable{Name: "bucket", Type: template.VarInteger},
			supplied: map[string]any{"bucket": 1.5},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "integer rejects a quoted integer",
			decl:     &template.Variable{Name: "bucket", Type: template.VarInteger},
			supplied: map[string]any{"bucket": "10"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- boolean ----
		{
			name:         "boolean accepts true",
			decl:         &template.Variable{Name: "verbose", Type: template.VarBoolean},
			supplied:     map[string]any{"verbose": true},
			wantResolved: true, wantJSON: `true`,
		},
		{
			name:     "boolean rejects a quoted bool",
			decl:     &template.Variable{Name: "verbose", Type: template.VarBoolean},
			supplied: map[string]any{"verbose": "true"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "boolean rejects 1",
			decl:     &template.Variable{Name: "verbose", Type: template.VarBoolean},
			supplied: map[string]any{"verbose": 1},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- enum ----
		{
			name: "enum accepts a declared member",
			decl: &template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold", "silver"},
			},
			supplied:     map[string]any{"tier": "silver"},
			wantResolved: true, wantJSON: `"silver"`,
		},
		{
			name: "enum rejects a non-member",
			decl: &template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold", "silver"},
			},
			supplied: map[string]any{"tier": "bronze"},
			wantCode: perr.PULSE_TEMPLATE_VAR_ENUM,
		},
		{
			name: "enum membership is case-sensitive",
			decl: &template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold"},
			},
			supplied: map[string]any{"tier": "Gold"},
			wantCode: perr.PULSE_TEMPLATE_VAR_ENUM,
		},
		{
			name: "enum rejects a non-string as a type fault, not an enum fault",
			decl: &template.Variable{
				Name: "tier", Type: template.VarEnum, Values: []string{"gold"},
			},
			supplied: map[string]any{"tier": 1},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- list ----
		{
			name: "list accepts an array of its items type",
			decl: &template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
			},
			supplied:     map[string]any{"segments": []string{"north", "south"}},
			wantResolved: true, wantJSON: `["north","south"]`,
		},
		{
			name: "list accepts an empty array",
			decl: &template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
			},
			supplied:     map[string]any{"segments": []any{}},
			wantResolved: true, wantJSON: `[]`,
		},
		{
			name: "list of integers accepts integral elements",
			decl: &template.Variable{
				Name: "buckets", Type: template.VarList, Items: template.VarInteger,
			},
			supplied:     map[string]any{"buckets": []any{1, 2, 3}},
			wantResolved: true, wantJSON: `[1,2,3]`,
		},
		{
			name: "list rejects a non-array",
			decl: &template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
			},
			supplied: map[string]any{"segments": "north"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name: "list rejects an element contradicting items",
			decl: &template.Variable{
				Name: "segments", Type: template.VarList, Items: template.VarString,
			},
			supplied: map[string]any{"segments": []any{"north", 7}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name: "list of integers rejects a fractional element",
			decl: &template.Variable{
				Name: "buckets", Type: template.VarList, Items: template.VarInteger,
			},
			supplied: map[string]any{"buckets": []any{1, 2.5}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name: "list of dates rejects an unparseable element",
			decl: &template.Variable{
				Name: "days", Type: template.VarList, Items: template.VarDate,
			},
			supplied: map[string]any{"days": []any{"2024-01-01", "not-a-date"}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- date ----
		{
			name:         "date accepts YYYY-MM-DD",
			decl:         &template.Variable{Name: "since", Type: template.VarDate},
			supplied:     map[string]any{"since": "2024-01-01"},
			wantResolved: true, wantJSON: `"2024-01-01"`,
		},
		{
			name:     "date rejects an unpadded spelling",
			decl:     &template.Variable{Name: "since", Type: template.VarDate},
			supplied: map[string]any{"since": "2024-1-1"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "date rejects a nonexistent day",
			decl:     &template.Variable{Name: "since", Type: template.VarDate},
			supplied: map[string]any{"since": "2024-02-30"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "date rejects a day integer",
			decl:     &template.Variable{Name: "since", Type: template.VarDate},
			supplied: map[string]any{"since": 20240101},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "date rejects an empty string",
			decl:     &template.Variable{Name: "since", Type: template.VarDate},
			supplied: map[string]any{"since": ""},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},

		// ---- period ----
		{
			name:         "period accepts a table reference",
			decl:         &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied:     map[string]any{"window": map[string]any{"table": "fiscal"}},
			wantResolved: true, wantJSON: `{"table":"fiscal"}`,
		},
		{
			name: "period accepts an inline range set",
			decl: &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(
				`{"ranges":[{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}]}`)},
			wantResolved: true,
			wantJSON:     `{"ranges":[{"end":"2024-03-31","label":"Q1","start":"2024-01-01"}]}`,
		},
		{
			name: "period accepts open boundaries",
			decl: &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(
				`{"ranges":[{"label":"early","end":"2024-03-31"},{"label":"late","start":"2024-04-01"}]}`)},
			wantResolved: true,
			wantJSON:     `{"ranges":[{"end":"2024-03-31","label":"early"},{"label":"late","start":"2024-04-01"}]}`,
		},
		{
			name:     "period rejects a non-object",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": "fiscal"},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name: "period rejects both ranges and table",
			decl: &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(
				`{"table":"fiscal","ranges":[{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}]}`)},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "period rejects neither ranges nor table",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": map[string]any{}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "period rejects an unknown sibling key",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(`{"table":"fiscal","unmatched":"other"}`)},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "period rejects an empty table name",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": map[string]any{"table": ""}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "period rejects a non-string table",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": map[string]any{"table": 1}},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "period rejects an empty ranges array",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(`{"ranges":[]}`)},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "period rejects a non-object range",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(`{"ranges":["Q1"]}`)},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:     "period rejects a range with no label",
			decl:     &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(`{"ranges":[{"start":"2024-01-01"}]}`)},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name: "period rejects a range with an unparseable boundary",
			decl: &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(
				`{"ranges":[{"label":"Q1","start":"01/01/2024"}]}`)},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name: "period rejects a range with a misspelled boundary key",
			decl: &template.Variable{Name: "window", Type: template.VarPeriod},
			supplied: map[string]any{"window": json.RawMessage(
				`{"ranges":[{"label":"Q1","startt":"2024-01-01"}]}`)},
			wantCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
	})
}

// TestResolve_FalsyValuesAreResolved is the explicit anti-regression for
// the distinction E1-S4's $when guards are built on: resolution is
// presence semantics, not truthiness. An empty string, an empty list, a
// zero, and a false are all real supplied values.
func TestResolve_FalsyValuesAreResolved(t *testing.T) {
	cases := []struct {
		name     string
		decl     *template.Variable
		supplied any
		wantJSON string
	}{
		{"empty string", &template.Variable{Name: "v", Type: template.VarString}, "", `""`},
		{"empty list", &template.Variable{Name: "v", Type: template.VarList, Items: template.VarString}, []any{}, `[]`},
		{"zero integer", &template.Variable{Name: "v", Type: template.VarInteger}, 0, `0`},
		{"zero number", &template.Variable{Name: "v", Type: template.VarNumber}, 0.0, `0`},
		{"false", &template.Variable{Name: "v", Type: template.VarBoolean}, false, `false`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := template.Resolve(tmpl(only(tc.decl)), map[string]any{"v": tc.supplied})
			if err != nil {
				t.Fatalf("Resolve() = %v, want nil", err)
			}
			if !res.IsResolved("v") {
				t.Fatalf("%s counted as unresolved; $when guards would drop a legitimate value", tc.name)
			}
			got, ok := res.Get("v")
			if !ok {
				t.Fatal("Get() reported unresolved after IsResolved reported resolved")
			}
			if js := marshal(t, got); js != tc.wantJSON {
				t.Errorf("resolved value = %s, want %s", js, tc.wantJSON)
			}
			if template.IsUnresolved(got) {
				t.Error("a supplied falsy value reported as the Unresolved sentinel")
			}
			if all := res.All(); template.IsUnresolved(all["v"]) {
				t.Error("All() reported a supplied falsy value as Unresolved")
			}
		})
	}
}

// TestResolve_UnresolvedSentinelIsDistinct pins the sentinel contract: it
// is not nil, it is not any JSON value, and it is what All reports for an
// unresolved variable.
func TestResolve_UnresolvedSentinelIsDistinct(t *testing.T) {
	if template.Unresolved == nil {
		t.Fatal("Unresolved is nil; a supplied JSON null would be indistinguishable from it")
	}
	if !template.IsUnresolved(template.Unresolved) {
		t.Fatal("IsUnresolved(Unresolved) = false")
	}
	for _, v := range []any{nil, "", 0, 0.0, false, []any{}, map[string]any{}, json.Number("0")} {
		if template.IsUnresolved(v) {
			t.Errorf("IsUnresolved(%#v) = true, want false", v)
		}
	}

	res, err := template.Resolve(tmpl(only(&template.Variable{
		Name: "optional", Type: template.VarString,
	})), nil)
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if res.IsResolved("optional") {
		t.Error("a variable with no supplied value and no default reported as resolved")
	}
	got, ok := res.Get("optional")
	if ok || got != nil {
		t.Errorf("Get(unresolved) = (%#v, %v), want (nil, false)", got, ok)
	}
	if all := res.All(); !template.IsUnresolved(all["optional"]) {
		t.Errorf("All()[\"optional\"] = %#v, want the Unresolved sentinel", all["optional"])
	}
	// The sentinel must not be mistakable for a declared-name miss.
	if !res.Declared("optional") {
		t.Error("Declared() = false for a declared but unresolved variable")
	}
	if res.Declared("absent") {
		t.Error("Declared() = true for a name the template never declared")
	}
}

// TestResolve_Order pins the resolution order: caller-supplied, then the
// declared default, then unresolved.
func TestResolve_Order(t *testing.T) {
	decl := &template.Variable{
		Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`10`),
	}

	res, err := template.Resolve(tmpl(only(decl)), map[string]any{"bucket": 25})
	if err != nil {
		t.Fatalf("Resolve(supplied) = %v, want nil", err)
	}
	if got, _ := res.Get("bucket"); marshal(t, got) != "25" {
		t.Errorf("supplied value did not win over the default: got %v", got)
	}

	res, err = template.Resolve(tmpl(only(decl)), nil)
	if err != nil {
		t.Fatalf("Resolve(nil) = %v, want nil", err)
	}
	if got, _ := res.Get("bucket"); marshal(t, got) != "10" {
		t.Errorf("default did not resolve: got %v", got)
	}

	bare := &template.Variable{Name: "bucket", Type: template.VarInteger}
	res, err = template.Resolve(tmpl(only(bare)), nil)
	if err != nil {
		t.Fatalf("Resolve(no default) = %v, want nil", err)
	}
	if res.IsResolved("bucket") {
		t.Error("a variable with neither a supplied value nor a default resolved")
	}
}

// TestResolve_SuppliedNullMeansSuppliedNothing documents the symmetry with
// the declaration side, where an explicit null default is "no default": a
// caller passing nil has supplied nothing, so resolution falls through to
// the default and then to unresolved.
func TestResolve_SuppliedNullMeansSuppliedNothing(t *testing.T) {
	withDefault := &template.Variable{
		Name: "bucket", Type: template.VarInteger, Default: json.RawMessage(`10`),
	}
	for _, supplied := range []any{nil, json.RawMessage(`null`)} {
		res, err := template.Resolve(tmpl(only(withDefault)), map[string]any{"bucket": supplied})
		if err != nil {
			t.Fatalf("Resolve(%#v) = %v, want nil", supplied, err)
		}
		if got, _ := res.Get("bucket"); marshal(t, got) != "10" {
			t.Errorf("supplied %#v did not fall through to the default: got %v", supplied, got)
		}
	}

	bare := &template.Variable{Name: "bucket", Type: template.VarInteger}
	res, err := template.Resolve(tmpl(only(bare)), map[string]any{"bucket": nil})
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if res.IsResolved("bucket") {
		t.Error("a supplied nil with no default resolved; it must count as supplied nothing")
	}

	// Supplying nil for a required variable is not silently tolerated: it
	// surfaces as the missing-variable fault, not as a null in the request.
	required := &template.Variable{Name: "bucket", Type: template.VarInteger, Required: true}
	if _, err := template.Resolve(tmpl(only(required)), map[string]any{"bucket": nil}); !perr.HasCode(err, perr.PULSE_TEMPLATE_VAR_MISSING) {
		t.Errorf("Resolve(nil for required) = %v, want PULSE_TEMPLATE_VAR_MISSING", err)
	}
}

// TestResolve_UnknownSuppliedName asserts an undeclared name is rejected
// rather than ignored, and that the error names it.
func TestResolve_UnknownSuppliedName(t *testing.T) {
	tm := tmpl(nil) // declares only "metric"
	_, err := template.Resolve(tm, map[string]any{"metric": "revenue", "buckets": 10})
	if err == nil {
		t.Fatal("Resolve() = nil, want PULSE_TEMPLATE_VAR_UNKNOWN")
	}
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_VAR_UNKNOWN) {
		t.Fatalf("Resolve() = %v, want PULSE_TEMPLATE_VAR_UNKNOWN", err)
	}
	ce := codedError(t, err)
	if got := ce.Details[perr.DetailVariable]; got != "buckets" {
		t.Errorf("details[%q] = %v, want \"buckets\"", perr.DetailVariable, got)
	}
	if got := ce.Details[perr.DetailTemplate]; got != tm.Name {
		t.Errorf("details[%q] = %v, want %q", perr.DetailTemplate, got, tm.Name)
	}

	// The unknown-name check runs before the missing-required check: a
	// typo against a declared name is the likeliest cause of both, and
	// naming the typo is the more useful report.
	_, err = template.Resolve(tm, map[string]any{"metrik": "revenue"})
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_VAR_UNKNOWN) {
		t.Errorf("Resolve(typo) = %v, want PULSE_TEMPLATE_VAR_UNKNOWN", err)
	}

	// A template declaring nothing tolerates an empty map but not a
	// populated one.
	none := tmpl(func(tm *template.Template) { tm.Variables = nil })
	if _, err := template.Resolve(none, map[string]any{}); err != nil {
		t.Errorf("Resolve(no declarations, empty map) = %v, want nil", err)
	}
	if _, err := template.Resolve(none, map[string]any{"x": 1}); !perr.HasCode(err, perr.PULSE_TEMPLATE_VAR_UNKNOWN) {
		t.Errorf("Resolve(no declarations, populated map) = %v, want PULSE_TEMPLATE_VAR_UNKNOWN", err)
	}
}

// TestResolve_RequiredMissing asserts a required variable that resolves to
// nothing fails, and that a required variable carrying a default never can.
func TestResolve_RequiredMissing(t *testing.T) {
	tm := tmpl(only(&template.Variable{Name: "metric", Type: template.VarField, Required: true}))
	_, err := template.Resolve(tm, nil)
	if err == nil {
		t.Fatal("Resolve() = nil, want PULSE_TEMPLATE_VAR_MISSING")
	}
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_VAR_MISSING) {
		t.Fatalf("Resolve() = %v, want PULSE_TEMPLATE_VAR_MISSING", err)
	}
	ce := codedError(t, err)
	if got := ce.Details[perr.DetailVariable]; got != "metric" {
		t.Errorf("details[%q] = %v, want \"metric\"", perr.DetailVariable, got)
	}
	if got := ce.Details[perr.DetailTemplate]; got != tm.Name {
		t.Errorf("details[%q] = %v, want %q", perr.DetailTemplate, got, tm.Name)
	}

	withDefault := tmpl(only(&template.Variable{
		Name: "metric", Type: template.VarField, Required: true,
		Default: json.RawMessage(`"revenue"`),
	}))
	res, err := template.Resolve(withDefault, nil)
	if err != nil {
		t.Fatalf("Resolve(required with default) = %v, want nil", err)
	}
	if got, _ := res.Get("metric"); marshal(t, got) != `"revenue"` {
		t.Errorf("required variable did not resolve from its default: got %v", got)
	}

	// An optional variable resolving to nothing is not a fault.
	optional := tmpl(only(&template.Variable{Name: "metric", Type: template.VarField}))
	if _, err := template.Resolve(optional, nil); err != nil {
		t.Errorf("Resolve(optional, unresolved) = %v, want nil", err)
	}
}

// TestResolve_ProvenanceSelectsTheCode is the decision this story rests
// on: the identical bad value reports a caller-facing code when the caller
// supplied it and PULSE_TEMPLATE_INVALID when the template author wrote it
// as a default. The fault is the same; the party at fault is not.
func TestResolve_ProvenanceSelectsTheCode(t *testing.T) {
	cases := []struct {
		name       string
		decl       template.Variable // Default is filled per sub-case
		bad        any
		badJSON    string
		callerCode perr.Code
	}{
		{
			name:       "kind mismatch",
			decl:       template.Variable{Name: "bucket", Type: template.VarInteger},
			bad:        "10",
			badJSON:    `"10"`,
			callerCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:       "fractional integer",
			decl:       template.Variable{Name: "bucket", Type: template.VarInteger},
			bad:        1.5,
			badJSON:    `1.5`,
			callerCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:       "enum non-membership",
			decl:       template.Variable{Name: "tier", Type: template.VarEnum, Values: []string{"gold"}},
			bad:        "bronze",
			badJSON:    `"bronze"`,
			callerCode: perr.PULSE_TEMPLATE_VAR_ENUM,
		},
		{
			name:       "unparseable date",
			decl:       template.Variable{Name: "since", Type: template.VarDate},
			bad:        "01/01/2024",
			badJSON:    `"01/01/2024"`,
			callerCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:       "period with both ranges and table",
			decl:       template.Variable{Name: "window", Type: template.VarPeriod},
			bad:        json.RawMessage(`{"table":"fiscal","ranges":[{"label":"Q1"}]}`),
			badJSON:    `{"table":"fiscal","ranges":[{"label":"Q1"}]}`,
			callerCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:       "period with neither ranges nor table",
			decl:       template.Variable{Name: "window", Type: template.VarPeriod},
			bad:        json.RawMessage(`{}`),
			badJSON:    `{}`,
			callerCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name:       "list element contradicting items",
			decl:       template.Variable{Name: "segs", Type: template.VarList, Items: template.VarString},
			bad:        json.RawMessage(`["north",7]`),
			badJSON:    `["north",7]`,
			callerCode: perr.PULSE_TEMPLATE_VAR_TYPE,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+" supplied by caller", func(t *testing.T) {
			decl := tc.decl
			_, err := template.Resolve(tmpl(only(&decl)), map[string]any{decl.Name: tc.bad})
			if !perr.HasCode(err, tc.callerCode) {
				t.Fatalf("Resolve(supplied) = %v, want %s", err, tc.callerCode)
			}
		})

		t.Run(tc.name+" written as a default", func(t *testing.T) {
			decl := tc.decl
			decl.Default = json.RawMessage(tc.badJSON)
			tm := tmpl(only(&decl))

			// Declaration time: a bad default must fail at Validate, so a
			// template carrying one never reaches a render call.
			err := template.Validate(tm)
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
				t.Fatalf("Validate(bad default) = %v, want PULSE_TEMPLATE_INVALID", err)
			}
			if perr.HasCode(err, tc.callerCode) && tc.callerCode != perr.PULSE_TEMPLATE_INVALID {
				t.Errorf("Validate() reported the caller-facing code %s for an author fault", tc.callerCode)
			}

			// Render time: the same fault keeps the same code.
			_, err = template.Resolve(tm, nil)
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
				t.Fatalf("Resolve(bad default) = %v, want PULSE_TEMPLATE_INVALID", err)
			}
			ce := codedError(t, err)
			if got := ce.Details[perr.DetailVariable]; got != decl.Name {
				t.Errorf("details[%q] = %v, want %q", perr.DetailVariable, got, decl.Name)
			}
		})
	}
}

// TestResolve_IntegerFidelity asserts a supplied integer beyond float64's
// exact-integer range survives resolution literally. Without UseNumber it
// would round-trip through float64 and reach the rendered request with its
// low bits rewritten — a u64 key silently becoming a different key.
func TestResolve_IntegerFidelity(t *testing.T) {
	const huge = uint64(math.MaxUint64) // 18446744073709551615
	decl := &template.Variable{Name: "id", Type: template.VarInteger}

	res, err := template.Resolve(tmpl(only(decl)), map[string]any{"id": huge})
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	got, ok := res.Get("id")
	if !ok {
		t.Fatal("Get(\"id\") reported unresolved")
	}
	num, ok := got.(json.Number)
	if !ok {
		t.Fatalf("resolved value is %T, want json.Number — integer fidelity is lost", got)
	}
	if num.String() != "18446744073709551615" {
		t.Errorf("resolved value = %s, want 18446744073709551615", num)
	}
	if js := marshal(t, got); js != "18446744073709551615" {
		t.Errorf("re-marshaled value = %s, want 18446744073709551615", js)
	}

	// The same holds for a default written in the document.
	fromDefault := &template.Variable{
		Name: "id", Type: template.VarInteger, Default: json.RawMessage(`18446744073709551615`),
	}
	res, err = template.Resolve(tmpl(only(fromDefault)), nil)
	if err != nil {
		t.Fatalf("Resolve(default) = %v, want nil", err)
	}
	if got, _ := res.Get("id"); marshal(t, got) != "18446744073709551615" {
		t.Errorf("default lost integer fidelity: got %v", got)
	}
}

// TestResolve_UnrepresentableSuppliedValue asserts a Go value that cannot
// be expressed as JSON is a caller type fault rather than a panic.
func TestResolve_UnrepresentableSuppliedValue(t *testing.T) {
	decl := &template.Variable{Name: "rate", Type: template.VarNumber}
	for _, bad := range []any{math.NaN(), math.Inf(1), make(chan int)} {
		_, err := template.Resolve(tmpl(only(decl)), map[string]any{"rate": bad})
		if !perr.HasCode(err, perr.PULSE_TEMPLATE_VAR_TYPE) {
			t.Errorf("Resolve(%T) = %v, want PULSE_TEMPLATE_VAR_TYPE", bad, err)
		}
	}
}

// TestResolve_DeclarationFaultsSurface asserts Resolve is total over an
// unvalidated document: a declaration fault is reported rather than
// producing a half-resolved set or a panic.
func TestResolve_DeclarationFaultsSurface(t *testing.T) {
	if _, err := template.Resolve(nil, nil); !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Errorf("Resolve(nil template) = %v, want PULSE_TEMPLATE_INVALID", err)
	}
	bad := tmpl(func(tm *template.Template) { tm.Target = "" })
	if _, err := template.Resolve(bad, nil); !perr.HasCode(err, perr.PULSE_TEMPLATE_TARGET_UNKNOWN) {
		t.Errorf("Resolve(bad target) = %v, want PULSE_TEMPLATE_TARGET_UNKNOWN", err)
	}
	dup := tmpl(func(tm *template.Template) {
		tm.Variables = []*template.Variable{
			{Name: "a", Type: template.VarString},
			{Name: "a", Type: template.VarString},
		}
	})
	if _, err := template.Resolve(dup, nil); !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Errorf("Resolve(duplicate names) = %v, want PULSE_TEMPLATE_INVALID", err)
	}
}

// TestResolution_Accessors pins the ordering, copy, and nil-safety
// contracts of the resolved set.
func TestResolution_Accessors(t *testing.T) {
	tm, err := template.Parse(readFixture(t))
	if err != nil {
		t.Fatalf("Parse(fixture) = %v, want nil", err)
	}
	res, err := template.Resolve(tm, map[string]any{"metric": "revenue"})
	if err != nil {
		t.Fatalf("Resolve(fixture) = %v, want nil", err)
	}

	want := []string{"metric", "bucket", "segments", "tier", "window", "since", "rate", "verbose", "label"}
	names := res.Names()
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Names() = %v, want %v (author order)", names, want)
	}
	names[0] = "mutated"
	if again := res.Names(); again[0] != "metric" {
		t.Error("Names() is not a defensive copy")
	}
	if res.Len() != len(want) {
		t.Errorf("Len() = %d, want %d", res.Len(), len(want))
	}

	// The fixture's only variables without a default are metric (supplied)
	// and segments/window (optional, unresolved).
	for _, name := range []string{"metric", "bucket", "tier", "since", "rate", "verbose", "label"} {
		if !res.IsResolved(name) {
			t.Errorf("IsResolved(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"segments", "window"} {
		if res.IsResolved(name) {
			t.Errorf("IsResolved(%q) = true, want false", name)
		}
	}

	all := res.All()
	if len(all) != len(want) {
		t.Errorf("All() has %d entries, want %d — every declared name must be present", len(all), len(want))
	}
	all["metric"] = "mutated"
	if got, _ := res.Get("metric"); marshal(t, got) != `"revenue"` {
		t.Error("All() is not a defensive copy")
	}

	// verbose defaults to false — a falsy default is still resolved.
	if !res.IsResolved("verbose") {
		t.Error("a false default counted as unresolved")
	}

	var nilRes *template.Resolution
	if nilRes.Names() != nil || nilRes.Len() != 0 || nilRes.All() != nil {
		t.Error("(*Resolution)(nil) accessors are not nil-safe")
	}
	if nilRes.Declared("metric") || nilRes.IsResolved("metric") {
		t.Error("(*Resolution)(nil) reported a variable as declared or resolved")
	}
	if v, ok := nilRes.Get("metric"); ok || v != nil {
		t.Errorf("(*Resolution)(nil).Get() = (%#v, %v), want (nil, false)", v, ok)
	}
}
