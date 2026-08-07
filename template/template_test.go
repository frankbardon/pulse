package template_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/template"
)

// TestTarget_ClosedEnum pins the five lowercase wire spellings and asserts
// nothing outside the set validates. The spellings are load-bearing: they
// are what a template file writes, what PULSE_TEMPLATE_TARGET_UNKNOWN's
// fixup examples advertise, and what selects the strict-decode type.
func TestTarget_ClosedEnum(t *testing.T) {
	tests := []struct {
		target template.Target
		want   bool
	}{
		{template.TargetRequest, true},
		{template.TargetComposed, true},
		{template.TargetChain, true},
		{template.TargetFacet, true},
		{template.TargetSample, true},
		{template.Target(""), false},
		{template.Target("Request"), false},
		{template.Target("REQUEST"), false},
		{template.Target("ComposedRequest"), false},
		{template.Target("process"), false},
		{template.Target("predict"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.target), func(t *testing.T) {
			if got := tt.target.Valid(); got != tt.want {
				t.Errorf("Target(%q).Valid() = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

// TestAllTargets_SpellingsAndCopy asserts the exact wire spellings, their
// order, and that the accessor hands back a defensive copy.
func TestAllTargets_SpellingsAndCopy(t *testing.T) {
	want := []template.Target{"request", "composed", "chain", "facet", "sample"}
	got := template.AllTargets()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllTargets() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if again := template.AllTargets(); again[0] != template.TargetRequest {
		t.Errorf("AllTargets() is not a defensive copy: got %q after caller mutation", again[0])
	}
	if s := template.TargetComposed.String(); s != "composed" {
		t.Errorf("TargetComposed.String() = %q, want \"composed\"", s)
	}
}

// TestVarType_ClosedEnum pins the nine variable-type spellings.
func TestVarType_ClosedEnum(t *testing.T) {
	tests := []struct {
		vt   template.VarType
		want bool
	}{
		{template.VarString, true},
		{template.VarNumber, true},
		{template.VarInteger, true},
		{template.VarBoolean, true},
		{template.VarField, true},
		{template.VarEnum, true},
		{template.VarList, true},
		{template.VarDate, true},
		{template.VarPeriod, true},
		{template.VarType(""), false},
		{template.VarType("String"), false},
		{template.VarType("int"), false},
		{template.VarType("float"), false},
		{template.VarType("object"), false},
		{template.VarType("array"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.vt), func(t *testing.T) {
			if got := tt.vt.Valid(); got != tt.want {
				t.Errorf("VarType(%q).Valid() = %v, want %v", tt.vt, got, tt.want)
			}
		})
	}
}

// TestAllVarTypes_SpellingsAndCopy asserts the exact wire spellings, their
// order, and the defensive copy.
func TestAllVarTypes_SpellingsAndCopy(t *testing.T) {
	want := []template.VarType{
		"string", "number", "integer", "boolean",
		"field", "enum", "list", "date", "period",
	}
	got := template.AllVarTypes()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllVarTypes() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if again := template.AllVarTypes(); again[0] != template.VarString {
		t.Errorf("AllVarTypes() is not a defensive copy: got %q after caller mutation", again[0])
	}
	if s := template.VarPeriod.String(); s != "period" {
		t.Errorf("VarPeriod.String() = %q, want \"period\"", s)
	}
}

// TestVarType_IsScalar fixes the scalar set — the exact types legal as a
// list's element type. list nests, period is an object, and enum draws its
// membership from a variable-level slot a list cannot express per element.
func TestVarType_IsScalar(t *testing.T) {
	tests := []struct {
		vt   template.VarType
		want bool
	}{
		{template.VarString, true},
		{template.VarNumber, true},
		{template.VarInteger, true},
		{template.VarBoolean, true},
		{template.VarField, true},
		{template.VarDate, true},
		{template.VarEnum, false},
		{template.VarList, false},
		{template.VarPeriod, false},
		{template.VarType("bogus"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.vt), func(t *testing.T) {
			if got := tt.vt.IsScalar(); got != tt.want {
				t.Errorf("VarType(%q).IsScalar() = %v, want %v", tt.vt, got, tt.want)
			}
		})
	}
}

// readFixture loads the on-disk fixture template.
func readFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "revenue.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return data
}

// compactBody rewrites a template's raw body to its whitespace-free form.
// Unmarshaling into json.RawMessage keeps the source literal verbatim
// (indentation and all) while marshaling compacts it, so a body must be
// normalized before two documents can be compared structurally.
func compactBody(t *testing.T, tmpl *template.Template) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, tmpl.Body); err != nil {
		t.Fatalf("compact body: %v", err)
	}
	tmpl.Body = json.RawMessage(buf.Bytes())
}

// TestTemplate_JSONRoundTripIsLossless asserts unmarshal → marshal yields
// an equivalent document: re-decoding the marshaled form produces a
// deep-equal Template. Every declared slot on the fixture participates, so
// a dropped or renamed json tag fails here.
func TestTemplate_JSONRoundTripIsLossless(t *testing.T) {
	data := readFixture(t)

	var first template.Template
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	compactBody(t, &first)

	encoded, err := json.Marshal(&first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var second template.Template
	if err := json.Unmarshal(encoded, &second); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}

	if !reflect.DeepEqual(&first, &second) {
		t.Errorf("round-trip is lossy:\n first  = %+v\n second = %+v", first, second)
	}

	// A second marshal must be byte-identical to the first — the encoded
	// form is stable, so the document can be rewritten to disk safely.
	reencoded, err := json.Marshal(&second)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Errorf("re-marshal drifted:\n first  = %s\n second = %s", encoded, reencoded)
	}
}

// TestTemplate_RoundTripPreservesBodyMarkers asserts the body survives the
// round-trip verbatim, markers intact. The body is raw JSON precisely so
// that an unrendered `{"$var": "metric"}` marker, a `{{label}}`
// interpolation token, and a `$when` guard are never mangled or resolved
// in transit.
func TestTemplate_RoundTripPreservesBodyMarkers(t *testing.T) {
	data := readFixture(t)

	var tmpl template.Template
	if err := json.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(tmpl.Body, &body); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}
	for _, key := range []string{"cohort", "aggregations", "groups"} {
		if _, ok := body[key]; !ok {
			t.Errorf("body lost key %q in transit", key)
		}
	}

	encoded, err := json.Marshal(&tmpl)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, fragment := range []string{
		`{"$var":"metric"}`,
		`{"$var":"bucket"}`,
		`"$when":"segments"`,
		`{{label}}`,
	} {
		if !bytes.Contains(encoded, []byte(fragment)) {
			t.Errorf("marshaled document dropped the %s marker: %s", fragment, encoded)
		}
	}
}

// TestTemplate_FixtureFieldsDecode asserts the fixture decodes into the
// declared model slot by slot — the model is the file's contract.
func TestTemplate_FixtureFieldsDecode(t *testing.T) {
	tmpl, err := template.Parse(readFixture(t))
	if err != nil {
		t.Fatalf("Parse(fixture) = %v, want nil", err)
	}
	if tmpl.Description != "Revenue by region" {
		t.Errorf("Description = %q, want \"Revenue by region\"", tmpl.Description)
	}
	if tmpl.Target != template.TargetRequest {
		t.Errorf("Target = %q, want %q", tmpl.Target, template.TargetRequest)
	}
	if tmpl.Name != "" {
		t.Errorf("Name = %q, want empty (the store derives it from the file path)", tmpl.Name)
	}

	metric, ok := tmpl.Variable("metric")
	if !ok {
		t.Fatal("Variable(\"metric\") not found")
	}
	if metric.Type != template.VarField {
		t.Errorf("metric.Type = %q, want %q", metric.Type, template.VarField)
	}
	if !metric.Required {
		t.Error("metric.Required = false, want true")
	}
	if metric.Description == "" {
		t.Error("metric.Description was dropped")
	}

	bucket, ok := tmpl.Variable("bucket")
	if !ok {
		t.Fatal("Variable(\"bucket\") not found")
	}
	if string(bucket.Default) != "10" {
		t.Errorf("bucket.Default = %q, want \"10\"", bucket.Default)
	}

	segments, ok := tmpl.Variable("segments")
	if !ok {
		t.Fatal("Variable(\"segments\") not found")
	}
	if segments.Items != template.VarString {
		t.Errorf("segments.Items = %q, want %q", segments.Items, template.VarString)
	}

	tier, ok := tmpl.Variable("tier")
	if !ok {
		t.Fatal("Variable(\"tier\") not found")
	}
	if !reflect.DeepEqual(tier.Values, []string{"gold", "silver", "bronze"}) {
		t.Errorf("tier.Values = %v, want [gold silver bronze]", tier.Values)
	}

	if _, ok := tmpl.Variable("Metric"); ok {
		t.Error("Variable lookup is case-insensitive; it must be exact")
	}
	if _, ok := tmpl.Variable("absent"); ok {
		t.Error("Variable(\"absent\") reported found")
	}
}

// TestTemplate_VariableNames asserts author order is preserved and the
// accessor is nil-safe.
func TestTemplate_VariableNames(t *testing.T) {
	tmpl, err := template.Parse(readFixture(t))
	if err != nil {
		t.Fatalf("Parse(fixture) = %v, want nil", err)
	}
	want := []string{"metric", "bucket", "segments", "tier", "window", "since", "rate", "verbose", "label"}
	if got := tmpl.VariableNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("VariableNames() = %v, want %v", got, want)
	}

	var nilTmpl *template.Template
	if got := nilTmpl.VariableNames(); got != nil {
		t.Errorf("(*Template)(nil).VariableNames() = %v, want nil", got)
	}
	if _, ok := nilTmpl.Variable("metric"); ok {
		t.Error("(*Template)(nil).Variable reported found")
	}
	empty := &template.Template{}
	if got := empty.VariableNames(); got != nil {
		t.Errorf("empty template VariableNames() = %v, want nil", got)
	}

	// A null declaration is a validation fault, but the accessors must
	// still be total — a caller inspecting a rejected document (to build
	// the error report) must not panic on it.
	holey := &template.Template{Variables: []*template.Variable{
		{Name: "a", Type: template.VarString},
		nil,
		{Name: "b", Type: template.VarString},
	}}
	if got := holey.VariableNames(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("VariableNames() over a holey declaration list = %v, want [a b]", got)
	}
	if _, ok := holey.Variable("b"); !ok {
		t.Error("Variable(\"b\") not found past a null declaration")
	}
}

// TestTemplate_Summarize asserts the listing projection carries name,
// description, target, variable names, source path, and shadowing info,
// and that it copies the shadow slice defensively.
func TestTemplate_Summarize(t *testing.T) {
	tmpl := &template.Template{
		Name:        "finance/revenue",
		Description: "Revenue by region",
		Target:      template.TargetRequest,
		Variables: []*template.Variable{
			{Name: "metric", Type: template.VarField},
			{Name: "bucket", Type: template.VarInteger},
		},
		Body: json.RawMessage(`{"cohort":{"filename":"sales.pulse"}}`),
	}

	shadows := []string{"/b/finance/revenue.json"}
	got := tmpl.Summarize("/a/finance/revenue.json", shadows)

	want := template.Summary{
		Name:        "finance/revenue",
		Description: "Revenue by region",
		Target:      template.TargetRequest,
		Variables:   []string{"metric", "bucket"},
		Path:        "/a/finance/revenue.json",
		Shadows:     []string{"/b/finance/revenue.json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Summarize() = %+v, want %+v", got, want)
	}

	shadows[0] = "mutated"
	if got.Shadows[0] != "/b/finance/revenue.json" {
		t.Error("Summarize did not copy the shadows slice defensively")
	}

	bare := tmpl.Summarize("", nil)
	if bare.Path != "" || bare.Shadows != nil {
		t.Errorf("Summarize(\"\", nil) = %+v, want empty Path and nil Shadows", bare)
	}

	var nilTmpl *template.Template
	if s := nilTmpl.Summarize("p", nil); !reflect.DeepEqual(s, template.Summary{}) {
		t.Errorf("(*Template)(nil).Summarize() = %+v, want zero Summary", s)
	}
}

// TestSummary_JSONShape pins the Summary wire tags, including that the
// optional slots omit rather than emit null.
func TestSummary_JSONShape(t *testing.T) {
	minimal, err := json.Marshal(template.Summary{
		Name:   "revenue",
		Target: template.TargetFacet,
	})
	if err != nil {
		t.Fatalf("marshal minimal summary: %v", err)
	}
	if want := `{"name":"revenue","target":"facet"}`; string(minimal) != want {
		t.Errorf("minimal Summary JSON = %s, want %s", minimal, want)
	}

	full, err := json.Marshal(template.Summary{
		Name:        "finance/revenue",
		Description: "Revenue by region",
		Target:      template.TargetRequest,
		Variables:   []string{"metric"},
		Path:        "/a/finance/revenue.json",
		Shadows:     []string{"/b/finance/revenue.json"},
	})
	if err != nil {
		t.Fatalf("marshal full summary: %v", err)
	}
	want := `{"name":"finance/revenue","description":"Revenue by region","target":"request",` +
		`"variables":["metric"],"path":"/a/finance/revenue.json","shadows":["/b/finance/revenue.json"]}`
	if string(full) != want {
		t.Errorf("full Summary JSON = %s, want %s", full, want)
	}
}
