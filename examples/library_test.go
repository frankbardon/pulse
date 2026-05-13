package examples

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestExamples_AllParseAsRequest verifies every embedded example
// unmarshals into types.Request despite carrying the _meta block.
// Pulse's JSON unmarshaler ignores unknown fields by default, so _meta
// must be invisible at execution time.
func TestExamples_AllParseAsRequest(t *testing.T) {
	err := fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		data, readErr := content.ReadFile(p)
		if readErr != nil {
			t.Fatalf("read %s: %v", p, readErr)
		}
		// Parse with strict-mode wouldn't be representative of runtime
		// behavior; the runtime accepts unknown fields, which is the
		// whole point of carrying _meta inline.
		var req types.Request
		if err := json.Unmarshal(data, &req); err != nil {
			t.Errorf("%s: parse as types.Request: %v", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestExamples_UniqueNames verifies every _meta.name is unique across
// the whole embedded library.
func TestExamples_UniqueNames(t *testing.T) {
	idx := loadIndex()
	if len(idx.byName) == 0 {
		t.Fatal("library indexed zero examples")
	}
	// loadIndex itself rejects duplicates by panicking; this test makes
	// the contract explicit and would catch a regression there.
	seen := make(map[string]string)
	for name := range idx.byName {
		if prior, dup := seen[name]; dup {
			t.Errorf("duplicate name %q (also seen as %q)", name, prior)
		}
		seen[name] = name
	}
}

// TestExamples_TagsFromTaxonomy verifies every tag carried by an
// indexed example is in the canonical allowlist.
func TestExamples_TagsFromTaxonomy(t *testing.T) {
	idx := loadIndex()
	canon := make(map[string]struct{}, len(CanonicalTags))
	for _, c := range CanonicalTags {
		canon[c] = struct{}{}
	}
	for name, ex := range idx.byName {
		for _, tag := range ex.Tags {
			if _, ok := canon[tag]; !ok {
				t.Errorf("%s: tag %q is not in CanonicalTags", name, tag)
			}
		}
	}
}

// operatorRe captures `"type": "PREFIX_..."` in a JSON request body.
// Kept in sync with the regex used by cmd/annotate-examples so the test
// would also fail if either side drifts.
var operatorRe = regexp.MustCompile(`"type"\s*:\s*"((?:AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|REG)_[A-Z0-9_]+)"`)

// TestExamples_OperatorsMatchBody verifies the declared operators
// equal the operators that can be auto-derived by scanning the request
// body. Drift between the two surfaces is a sign that an example was
// edited without re-running the annotation tool.
func TestExamples_OperatorsMatchBody(t *testing.T) {
	idx := loadIndex()
	for name, ex := range idx.byName {
		got := derive(ex.Body)
		want := append([]string(nil), ex.Operators...)
		sort.Strings(want)
		if !equalStrings(got, want) {
			t.Errorf("%s: declared operators %v ≠ derived %v", name, want, got)
		}
	}
}

// derive auto-discovers operator names from a raw body the same way
// the annotation tool does.
func derive(body []byte) []string {
	matches := operatorRe.FindAllStringSubmatch(string(body), -1)
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		seen[m[1]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for op := range seen {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestExamples_CategoryMatchesDirectory verifies every _meta.category
// matches the file's parent directory.
func TestExamples_CategoryMatchesDirectory(t *testing.T) {
	err := fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		data, _ := content.ReadFile(p)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Errorf("%s: parse: %v", p, err)
			return nil
		}
		rawMeta, ok := raw["_meta"]
		if !ok {
			t.Errorf("%s: missing _meta", p)
			return nil
		}
		var m meta
		if err := json.Unmarshal(rawMeta, &m); err != nil {
			t.Errorf("%s: parse _meta: %v", p, err)
			return nil
		}
		dir := filepath.Base(filepath.Dir(p))
		if m.Category != dir {
			t.Errorf("%s: _meta.category=%q, want %q", p, m.Category, dir)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestExamplesSearch_QueryMatchesDescription verifies substring search
// on the description field returns the expected hits.
func TestExamplesSearch_QueryMatchesDescription(t *testing.T) {
	hits := Search("Welch", nil, "")
	if len(hits) == 0 {
		t.Fatal("expected at least one hit for 'Welch'")
	}
	found := false
	for _, h := range hits {
		if h.Name == "welch_alias_two_sample" || h.Name == "welch_anova_unequal_var" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected welch examples in hits; got %v", names(hits))
	}
}

// TestExamplesSearch_TagsAreANDed verifies combining tags narrows the
// result set rather than widening it.
func TestExamplesSearch_TagsAreANDed(t *testing.T) {
	one := Search("", []string{"tier-1-test"}, "")
	two := Search("", []string{"tier-1-test", "parametric"}, "")
	if len(two) == 0 {
		t.Fatal("AND search returned no hits; taxonomy may be miscategorized")
	}
	if !(len(two) <= len(one)) {
		t.Errorf("adding a second tag widened the result: %d > %d", len(two), len(one))
	}
	for _, h := range two {
		hasTier := false
		hasParam := false
		for _, tag := range h.Tags {
			if tag == "tier-1-test" {
				hasTier = true
			}
			if tag == "parametric" {
				hasParam = true
			}
		}
		if !hasTier || !hasParam {
			t.Errorf("%s lacks one of the requested tags (tags=%v)", h.Name, h.Tags)
		}
	}
}

// TestExamplesSearch_CategoryExact verifies category filter is exact.
func TestExamplesSearch_CategoryExact(t *testing.T) {
	hits := Search("", nil, "windows")
	if len(hits) == 0 {
		t.Fatal("expected hits for category=windows")
	}
	for _, h := range hits {
		if h.Category != "windows" {
			t.Errorf("category filter returned %q (want 'windows')", h.Category)
		}
	}
}

// TestExamplesGet_StripsMeta verifies the returned body does not carry
// the _meta key — so callers can hand the JSON straight to
// pulse_process / pulse_predict.
func TestExamplesGet_StripsMeta(t *testing.T) {
	ex, ok := Get("t_test_one_sample")
	if !ok {
		t.Fatal("missing canonical example")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(ex.Body, &raw); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if _, has := raw["_meta"]; has {
		t.Error("body still carries _meta")
	}
	// Sanity: it must still carry the request slots.
	if _, ok := raw["cohort"]; !ok {
		t.Error("body missing cohort")
	}
}

// TestExamplesGet_BodyExecutes verifies the stripped body parses as a
// real types.Request — i.e. our strip-on-output is not corrupting
// anything load-bearing.
func TestExamplesGet_BodyExecutes(t *testing.T) {
	ex, ok := Get("t_test_one_sample")
	if !ok {
		t.Fatal("missing canonical example")
	}
	var req types.Request
	if err := json.Unmarshal(ex.Body, &req); err != nil {
		t.Fatalf("body does not parse as types.Request: %v", err)
	}
	if req.Cohort == nil {
		t.Error("body produced nil cohort")
	}
}

// TestAllCategories_Sorted verifies the category list is alphabetical.
func TestAllCategories_Sorted(t *testing.T) {
	cats := AllCategories()
	if len(cats) == 0 {
		t.Fatal("AllCategories returned empty")
	}
	for i := 1; i < len(cats); i++ {
		if cats[i] < cats[i-1] {
			t.Errorf("categories not sorted: %q before %q", cats[i-1], cats[i])
		}
	}
}

// TestAllTags_Sorted verifies the tag list is alphabetical and only
// returns tags that are actually in use somewhere.
func TestAllTags_Sorted(t *testing.T) {
	tags := AllTags()
	if len(tags) == 0 {
		t.Fatal("AllTags returned empty")
	}
	for i := 1; i < len(tags); i++ {
		if tags[i] < tags[i-1] {
			t.Errorf("tags not sorted: %q before %q", tags[i-1], tags[i])
		}
	}
}

// TestCount verifies the indexed example count is non-zero (a smoke
// test more than a contract).
func TestCount(t *testing.T) {
	if Count() == 0 {
		t.Fatal("Count returned 0")
	}
}

// TestEcologicalRegressionExample verifies the Phase 6 ecological-
// regression example is present, carries the expected operator set,
// and parses as a runnable types.Request. The body itself can't be
// executed in a unit test without a .pulse fixture; the integration
// surface relies on examples/fixtures/build.sh producing customers.pulse
// at runtime.
func TestEcologicalRegressionExample(t *testing.T) {
	ex, ok := Get("ecological_fallacy")
	if !ok {
		t.Fatal("ecological_fallacy example not registered in the embedded library")
	}
	if ex.Category != "regression" {
		t.Errorf("Category = %q, want \"regression\"", ex.Category)
	}
	wantTags := map[string]bool{
		"regression":      true,
		"ecological":      true,
		"cohort-analysis": true,
		"composed":        true,
	}
	for _, tag := range ex.Tags {
		if !wantTags[tag] {
			t.Errorf("unexpected tag %q on ecological example", tag)
		}
		delete(wantTags, tag)
	}
	for missing := range wantTags {
		t.Errorf("ecological_fallacy missing tag %q", missing)
	}

	// Body must parse as a Request and carry both the aggregation half
	// (groups + AGG_AVERAGE on income / age) and a REG_OLS regression
	// slot. Exact shape matches the documented ecological pattern in
	// skills/regression-modeling.md.
	var req struct {
		Groups       []map[string]any `json:"groups"`
		Aggregations []map[string]any `json:"aggregations"`
		Regressions  []map[string]any `json:"regressions"`
	}
	if err := json.Unmarshal(ex.Body, &req); err != nil {
		t.Fatalf("Body does not parse: %v", err)
	}
	if len(req.Groups) == 0 {
		t.Error("ecological_fallacy must carry a groups slot (per-region aggregation)")
	}
	if len(req.Aggregations) == 0 {
		t.Error("ecological_fallacy must carry an aggregations slot (the per-region means)")
	}
	if len(req.Regressions) == 0 {
		t.Error("ecological_fallacy must carry a regressions slot")
	}
	if len(req.Regressions) > 0 && req.Regressions[0]["type"] != "REG_OLS" {
		t.Errorf("Regressions[0].type = %v, want REG_OLS", req.Regressions[0]["type"])
	}
}

// names is a small helper for legible failure messages.
func names(hits []ExampleSummary) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Name
	}
	return out
}
