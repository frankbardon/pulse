package doccheck

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestScanFences(t *testing.T) {
	t.Parallel()
	src := "# Page\n" + // 1
		"\n" + // 2
		"```json cohort=survey\n" + // 3
		"{\"cohort\": {}}\n" + // 4
		"```\n" + // 5
		"- item\n" + // 6
		"\n" + // 7
		"  ~~~go compile\n" + // 8
		"  package x\n" + // 9
		"  ~~~\n" + // 10
		"````md\n" + // 11
		"```json\n" + // 12  (inside a longer fence: content)
		"````\n" + // 13
		"inline ```not a fence``` here\n" + // 14
		"```json\n" + // 15 unclosed: runs to the end
		"{}\n"
	got := ScanFences([]byte(src))
	want := []Fence{
		{Info: "json cohort=survey", Body: "{\"cohort\": {}}\n", Line: 3},
		{Info: "go compile", Body: "package x\n", Line: 8},
		{Info: "md", Body: "```json\n", Line: 11},
		{Info: "json", Body: "{}\n\n", Line: 15},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanFences:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseSnippetInfo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		info string
		want SnippetInfo
	}{
		{"json", SnippetInfo{Lang: "json"}},
		{"json norun", SnippetInfo{Lang: "json", NoRun: true}},
		{"json cohort=survey", SnippetInfo{Lang: "json", Cohorts: []string{"survey"}}},
		{"json cohort=customers,customers_twin", SnippetInfo{Lang: "json", Cohorts: []string{"customers", "customers_twin"}}},
		{"go compile", SnippetInfo{Lang: "go", Compile: true}},
		{"go,compile", SnippetInfo{Lang: "go", Compile: true}},
		{"jsonc", SnippetInfo{Lang: "jsonc"}},
		{"", SnippetInfo{}},
	}
	for _, c := range cases {
		if got := ParseSnippetInfo(c.info); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseSnippetInfo(%q) = %+v, want %+v", c.info, got, c.want)
		}
	}
}

func TestRequestRoot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want RootKind
		ok   bool
	}{
		{"request", `{"cohort": {"filename": "a.pulse"}, "aggregations": [{"type": "AGG_COUNT"}]}`, RootRequest, true},
		{"request with meta", `{"_meta": {"name": "x"}, "cohort": {"filename": "a.pulse"}}`, RootRequest, true},
		{"compose", `{"requests": [{"label": "a", "cohort": {"filename": "a.pulse"}}]}`, RootComposed, true},
		{"chain", `{"cohort": {"filename": "a.pulse"}, "stages": [{}]}`, RootChain, true},
		{"facet", `{"cohort": {"filename": "a.pulse"}, "fields": ["region"]}`, RootFacet, true},
		{"response envelope", `{"format_version": "1.1", "data": [], "errors": [], "warnings": []}`, "", false},
		{"slot fragment", `{"type": "AGG_SUM", "field": "amount"}`, "", false},
		{"misspelled slot", `{"cohort": {"filename": "a.pulse"}, "agregations": []}`, "", false},
		{"elided", `{"cohort": {...}}`, "", false},
		{"empty object", `{}`, "", false},
		{"meta only", `{"_meta": {}}`, "", false},
		{"array", `[1, 2]`, "", false},
	}
	for _, c := range cases {
		got, ok := RequestRoot([]byte(c.body))
		if got != c.want || ok != c.ok {
			t.Errorf("%s: RequestRoot = (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestSnippetKey(t *testing.T) {
	t.Parallel()
	a := SnippetKey("json", []byte("{\"a\": 1}\n"))
	if a != SnippetKey("json", []byte("\n  {\"a\": 1}  \n\n")) {
		t.Error("surrounding whitespace changed the key")
	}
	if a == SnippetKey("json", []byte("{\"a\": 2}\n")) {
		t.Error("a body change kept the key")
	}
	if a == SnippetKey("go", []byte("{\"a\": 1}\n")) {
		t.Error("the language is not part of the key")
	}
	if len(a) != len("json:")+12 {
		t.Errorf("key %q: want json:<12 hex>", a)
	}
}

func TestRetargetCohorts(t *testing.T) {
	t.Parallel()
	body := `{"cohort": {"filename": "sales.pulse", "data_dir": ".data"},
	  "union": [{"cohort": {"filename": "customers.pulse"}}, {"cohort": {"filename": "twin.pulse#w1.pulse"}}],
	  "requests": [{"cohort": {"filename": "survey.pulse#wave1.pulse"}, "n": 1.50}]}`
	out, err := RetargetCohorts([]byte(body), []string{"survey", "customers"}, "/fx")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	ref := func(v any) map[string]any { return v.(map[string]any)["cohort"].(map[string]any) }
	check := func(where string, r map[string]any, wantFile string) {
		t.Helper()
		if r["filename"] != wantFile || r["data_dir"] != "/fx" {
			t.Errorf("%s: got %v, want filename %q data_dir /fx", where, r, wantFile)
		}
	}
	check("root", got["cohort"].(map[string]any), "survey.pulse")                     // not a fixture: the first cohort
	check("union[0]", ref(got["union"].([]any)[0]), "customers.pulse")                // a named fixture: kept
	check("union[1]", ref(got["union"].([]any)[1]), "survey.pulse")                   // not a fixture: the first cohort
	check("requests[0]", ref(got["requests"].([]any)[0]), "survey.pulse#wave1.pulse") // anchor on a fixture: kept
	if n := got["requests"].([]any)[0].(map[string]any)["n"]; n != 1.5 {
		t.Errorf("number not preserved: %v", n)
	}
	if _, err := RetargetCohorts([]byte(`{"cohort": `), []string{"survey"}, "/fx"); err == nil {
		t.Error("malformed body: want an error")
	}
}
