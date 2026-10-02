package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// errorsHiddenTokens is every token naming a feature the instance hides:
// the bare hidden operator names plus the MCP tools of hidden features.
func errorsHiddenTokens(p *Pulse) map[string]bool {
	inst := p.svc.InstanceSnapshot()
	out := map[string]bool{}
	for _, n := range inst.HiddenNames() {
		if !strings.Contains(n, ":") {
			out[n] = true
		}
	}
	for _, b := range descx.MCPToolBindings() {
		if b.Feature != "" && !inst.Enabled(b.Feature) {
			out[b.Tool] = true
		}
	}
	return out
}

// namesHiddenToken reports the first hidden token s names as a whole
// [A-Za-z0-9_] run, or "".
func namesHiddenToken(s string, hidden map[string]bool) string {
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
	}
	start := -1
	for i := 0; i <= len(s); i++ {
		w := i < len(s) && isWord(s[i])
		switch {
		case w && start < 0:
			start = i
		case !w && start >= 0:
			if hidden[s[start:i]] {
				return s[start:i]
			}
			start = -1
		}
	}
	return ""
}

func errorsJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestFeatureSet_ErrorsShowOnlyInstanceCodes: per fixture, no hidden name
// appears in any error list, lookup result, fixup or message reachable
// from the instance, and the manifest error fields agree with lookup.
func TestFeatureSet_ErrorsShowOnlyInstanceCodes(t *testing.T) {
	for _, name := range featureSetFixtures {
		t.Run(name, func(t *testing.T) {
			p := newFixturePulse(t, name, Options{})
			hidden := errorsHiddenTokens(p)
			if len(hidden) == 0 {
				t.Fatal("fixture hides no named feature: vacuous")
			}

			var visible []string
			for _, c := range errors.AllCodes() {
				r, ok := p.ErrorLookup(string(c))
				if !ok {
					continue
				}
				visible = append(visible, r.Code)
				if tok := namesHiddenToken(errorsJSON(t, r), hidden); tok != "" {
					t.Errorf("ErrorLookup(%s) names hidden %s", c, tok)
				}
			}
			if len(visible) == len(errors.AllCodes()) {
				t.Error("fixture hides no error code")
			}
			sort.Strings(visible)

			m := p.Manifest(context.Background())
			if !slices.Equal(m.ErrorCodes, visible) || m.ErrorCodesCount != len(visible) {
				t.Errorf("manifest lists %d codes, lookup finds %d", len(m.ErrorCodes), len(visible))
			}
			var byDomain []string
			for _, d := range errors.AllDomains() {
				rs := p.ErrorsByDomain(d)
				if len(rs) > 0 != slices.Contains(m.ErrorDomains, d) {
					t.Errorf("domain %s: %d codes, manifest lists it = %v", d, len(rs), slices.Contains(m.ErrorDomains, d))
				}
				for _, r := range rs {
					byDomain = append(byDomain, r.Code)
					if tok := namesHiddenToken(errorsJSON(t, r), hidden); tok != "" {
						t.Errorf("ErrorsByDomain(%s) %s names hidden %s", d, r.Code, tok)
					}
				}
			}
			sort.Strings(byDomain)
			if !slices.Equal(byDomain, visible) {
				t.Errorf("ErrorsByDomain covers %d codes, lookup %d", len(byDomain), len(visible))
			}

			for tok := range hidden {
				for _, r := range p.ErrorsSearch(tok) {
					if !slices.Contains(visible, r.Code) {
						t.Errorf("ErrorsSearch(%s) returned hidden code %s", tok, r.Code)
					}
					rendered := errorsJSON(t, r)
					if got := namesHiddenToken(rendered, hidden); got != "" {
						t.Errorf("ErrorsSearch(%s) %s names hidden %s", tok, r.Code, got)
					}
					if !strings.Contains(strings.ToLower(rendered), strings.ToLower(tok)) {
						t.Errorf("ErrorsSearch(%s) matched %s on text the instance does not render", tok, r.Code)
					}
				}
			}
		})
	}
}

// TestFeatureSet_ErrorsDefaultIsFull: with no profile the errors facade
// and the manifest error fields are the full registry, unchanged.
func TestFeatureSet_ErrorsDefaultIsFull(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, c := range errors.AllCodes() {
		want, _ := errors.Lookup(string(c))
		if got, ok := p.ErrorLookup(string(c)); !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("ErrorLookup(%s) differs from errors.Lookup", c)
		}
	}
	for _, d := range errors.AllDomains() {
		if errorsJSON(t, p.ErrorsByDomain(d)) != errorsJSON(t, errors.ByDomain(d)) {
			t.Errorf("ErrorsByDomain(%s) differs", d)
		}
	}
	for _, q := range []string{"overlay", "TEST_WELCH", "spss", "join"} {
		if errorsJSON(t, p.ErrorsSearch(q)) != errorsJSON(t, errors.Search(q)) {
			t.Errorf("ErrorsSearch(%q) differs", q)
		}
	}
	m := p.Manifest(context.Background())
	if !slices.Equal(m.ErrorCodes, errors.SortedCodeNames()) || m.ErrorCodesCount != len(errors.AllCodes()) ||
		!slices.Equal(m.ErrorDomains, errors.AllDomains()) {
		t.Error("default manifest error fields differ from the registry")
	}
}
