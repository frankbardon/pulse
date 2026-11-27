package examples

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestServed_DropsOnlyTheFixtureDataDir: the fixture data_dir goes at
// every depth and a population cohort loses the fixture prefix; any
// other data_dir, path or number literal is kept, in its order.
func TestServed_DropsOnlyTheFixtureDataDir(t *testing.T) {
	t.Parallel()

	in := `{"cohort":{"filename":"a.pulse","data_dir":".data"},` +
		`"union":[{"cohort":{"filename":"b.pulse","data_dir":".data"}},{"cohort":{"filename":"c.pulse","data_dir":"waves"}}],` +
		`"overlays":[{"ref":{"population":{"cohort":".data/d.pulse"}}},{"ref":{"population":{"cohort":"other/.data/e.pulse"}}}],` +
		`"seed":18446744073709551615,"tol":1e-12}`
	got := string(Served(json.RawMessage(in)))
	want := `{"cohort":{"filename":"a.pulse"},` +
		`"union":[{"cohort":{"filename":"b.pulse"}},{"cohort":{"filename":"c.pulse","data_dir":"waves"}}],` +
		`"overlays":[{"ref":{"population":{"cohort":"d.pulse"}}},{"ref":{"population":{"cohort":"other/.data/e.pulse"}}}],` +
		`"seed":18446744073709551615,"tol":1e-12}`
	if got != want {
		t.Errorf("Served =\n%s\nwant\n%s", got, want)
	}
	// Nothing to strip: the bytes come back as given.
	plain := `{"b":1,"a":{"data_dir":"waves","x":"\u00e9"}}`
	if got := string(Served(json.RawMessage(plain))); got != plain {
		t.Errorf("Served(untouched) = %s, want %s", got, plain)
	}
}

// TestServed_EveryBuiltInBodyIsRootRelative: no served built-in body
// still names the fixture directory, and each still parses.
func TestServed_EveryBuiltInBodyIsRootRelative(t *testing.T) {
	t.Parallel()

	for _, ex := range All() {
		body := Served(ex.Body)
		if strings.Contains(string(body), `"`+FixtureDataDir+`"`) || strings.Contains(string(body), `"`+FixtureDataDir+`/`) {
			t.Errorf("%s: served body still names %s: %s", ex.Name, FixtureDataDir, body)
		}
		var v map[string]any
		if err := json.Unmarshal(body, &v); err != nil {
			t.Errorf("%s: served body does not parse: %v", ex.Name, err)
		}
	}
}
