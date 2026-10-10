package guide

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// paramNames lists op's drafted params by name.
func paramNames(op opInfo) []string {
	var out []string
	for _, p := range op.params {
		out = append(out, p.name)
	}
	return out
}

// TestCatalog_ExactlyOneOfDraftsFirstMember: an exactly_one_of group
// drafts its first member (the others ride as alternatives), for a
// built-in and for an extension registration alike.
func TestCatalog_ExactlyOneOfDraftsFirstMember(t *testing.T) {
	cat := catalog(descx.BuildManifestForInstance(nil))
	for op, want := range map[string]struct {
		param string
		alts  []string
	}{
		"WIN_EWMA":       {"alpha", []string{"decay"}},
		"FEAT_BUCKETIZE": {"boundaries", []string{"quantiles"}},
	} {
		info := cat[op]
		if got := paramNames(info); !slices.Equal(got, []string{want.param}) {
			t.Errorf("%s: drafted params %v, want [%s]", op, got, want.param)
			continue
		}
		if !slices.Equal(info.params[0].alternatives, want.alts) {
			t.Errorf("%s: alternatives %v, want %v", op, info.params[0].alternatives, want.alts)
		}
	}

	m := &descriptor.Manifest{}
	m.Extensions.Windows = []descriptor.OperatorMeta{{
		Name: "WIN_ACME_SMOOTH",
		Params: []descriptor.OperatorParamMeta{
			{Name: "adjust", JSONType: "boolean"},
			{Name: "span", JSONType: "number", ExactlyOneOf: "smoothing"},
			{Name: "halflife", JSONType: "number", ExactlyOneOf: "smoothing"},
			{Name: "com", JSONType: "number", ExactlyOneOf: "smoothing"},
		},
	}}
	ext := catalog(m)["WIN_ACME_SMOOTH"]
	if got := paramNames(ext); !slices.Equal(got, []string{"span"}) {
		t.Fatalf("extension drafted params %v, want [span]", got)
	}
	if !slices.Equal(ext.params[0].alternatives, []string{"halflife", "com"}) {
		t.Errorf("extension alternatives %v", ext.params[0].alternatives)
	}
}

// TestRecommendBound_OneOfNeedNamesAlternatives: a need drafted from an
// exactly_one_of group says the caller may give another member instead.
func TestRecommendBound_OneOfNeedNamesAlternatives(t *testing.T) {
	found := false
	for _, r := range boundAll(t, nil, descx.IntentChangeOverTime).Recommendations {
		if r.Operator != "WIN_EWMA" {
			continue
		}
		for _, n := range r.Needs {
			if n.Param != "alpha" {
				continue
			}
			found = true
			if !strings.Contains(n.Why, "Or give decay instead") {
				t.Errorf("WIN_EWMA alpha need does not name decay: %q", n.Why)
			}
		}
		if !strings.Contains(string(r.Request), `"alpha": "<alpha>"`) && !strings.Contains(string(r.Request), `"alpha":"<alpha>"`) {
			t.Errorf("WIN_EWMA draft has no alpha placeholder: %s", r.Request)
		}
	}
	if !found {
		t.Fatal("no WIN_EWMA draft with an alpha need under change_over_time")
	}
}
