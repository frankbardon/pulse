package doccheck

import (
	"encoding/json"
	"reflect"
	"strings"
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
		name    string
		body    string
		want    RootKind
		ok      bool
		wantErr string // a root that fails the strict decode: its error names this
	}{
		{name: "request", body: `{"cohort": {"filename": "a.pulse"}, "aggregations": [{"type": "AGG_COUNT"}]}`, want: RootRequest, ok: true},
		{name: "request with meta", body: `{"_meta": {"name": "x"}, "cohort": {"filename": "a.pulse"}}`, want: RootRequest, ok: true},
		{name: "request fragment without cohort decodes", body: `{"regressions": []}`, want: RootRequest, ok: true},
		{name: "compose", body: `{"requests": [{"label": "a", "cohort": {"filename": "a.pulse"}}]}`, want: RootComposed, ok: true},
		{name: "compose by banner", body: `{"banner": {"request": {"cohort": {"filename": "a.pulse"}}, "sections": [{"label": "all"}]}}`, want: RootComposed, ok: true},
		{name: "compose by base", body: `{"base": {"cohort": {"filename": "a.pulse"}}}`, want: RootComposed, ok: true},
		{name: "chain", body: `{"cohort": {"filename": "a.pulse"}, "stages": [{}]}`, want: RootChain, ok: true},
		{name: "facet", body: `{"cohort": {"filename": "a.pulse"}, "fields": ["region"]}`, want: RootFacet, ok: true},
		{name: "misspelled slot", body: `{"cohort": {"filename": "a.pulse"}, "agregations": []}`, want: RootRequest, ok: true, wantErr: "agregations"},
		{name: "misspelled compose slot", body: `{"base": {"cohort": {"filename": "a.pulse"}}, "bannr": {}}`, want: RootComposed, ok: true, wantErr: "bannr"},
		{name: "misspelled facet slot", body: `{"cohort": {"filename": "a.pulse"}, "fields": ["region"], "filtrs": []}`, want: RootFacet, ok: true, wantErr: "filtrs"},
		{name: "response envelope", body: `{"format_version": "1.1", "data": [], "errors": [], "warnings": []}`},
		{name: "compose response", body: `{"banner": {"sections": []}, "fits": {"m": {}}, "responses": []}`},
		{name: "chain response", body: `{"stages": [{"data": []}], "final": {"data": []}}`},
		{name: "sidecar naming its cohort file", body: `{"kind": "pulse.index-manifest", "cohort": "sales.pulse", "indexes": []}`},
		{name: "slot fragment", body: `{"type": "AGG_SUM", "field": "amount"}`},
		{name: "elided", body: `{"cohort": {...}}`},
		{name: "commented", body: "{\"cohort\": {\"filename\": \"a.pulse\"} // the cohort\n}"},
		{name: "empty object", body: `{}`},
		{name: "meta only", body: `{"_meta": {}}`},
		{name: "array", body: `[1, 2]`},
	}
	for _, c := range cases {
		got, ok, err := RequestRoot([]byte(c.body))
		if got != c.want || ok != c.ok {
			t.Errorf("%s: RequestRoot = (%q, %v, %v), want (%q, %v)", c.name, got, ok, err, c.want, c.ok)
			continue
		}
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected decode error %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: decode error %v, want one naming %q", c.name, err, c.wantErr)
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
