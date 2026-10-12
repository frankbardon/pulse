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

// TestCatalog_VectorAggregatorDraftsFields: a vector aggregator — the
// built-in AGG_VEC_* and an extension declared vector_input — is
// drafted with an inline fields list, never a field, bound (plan) and
// unbound (skeleton) alike; a plain extension aggregator keeps field.
func TestCatalog_VectorAggregatorDraftsFields(t *testing.T) {
	m := descx.BuildManifestForInstance(nil)
	m.Extensions.Aggregators = []descriptor.OperatorMeta{
		{Name: "AGG_ACME_PROFILE", VectorInput: true, ArrayValued: true},
		{Name: "AGG_ACME_PLAIN"},
	}
	cat := catalog(m)
	for _, c := range []struct {
		op     string
		fields bool
	}{
		{"AGG_VEC_MEAN", true},
		{"AGG_ACME_PROFILE", true},
		{"AGG_ACME_PLAIN", false},
		{"AGG_SUM", false},
	} {
		info := cat[c.op]
		if info.vectorInput != c.fields {
			t.Errorf("%s: vectorInput = %v, want %v", c.op, info.vectorInput, c.fields)
		}
		slots, _, ok := plan(info, descriptor.Intent{})
		if !ok {
			t.Fatalf("%s: no plan", c.op)
		}
		var keys []string
		for _, s := range slots {
			keys = append(keys, s.key)
		}
		if got := slices.Contains(keys, "fields"); got != c.fields || slices.Contains(keys, "field") == c.fields {
			t.Errorf("%s: bound slots %v, want fields=%v", c.op, keys, c.fields)
		}
		d, ok := skeleton(info, "describe")
		if !ok {
			t.Fatalf("%s: no skeleton", c.op)
		}
		body := string(d.request)
		if strings.Contains(body, `"fields"`) != c.fields || strings.Contains(body, `"field"`) == c.fields {
			t.Errorf("%s: skeleton %s, want fields=%v", c.op, body, c.fields)
		}
	}
}
