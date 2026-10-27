package vectors

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestExprStaticIssue(t *testing.T) {
	t.Parallel()

	lenOf := func(name string) (int, bool) {
		if name == "xy" {
			return 2, true
		}
		return 0, false
	}
	for _, c := range []struct {
		src, what string
		code      errors.Code
		msg       string
	}{
		{"dot(xy)", "formula", errors.PROCESSING_CONFIG, "dot() takes 2 argument(s); got 1"},
		{"vsum(xy, xy)", "formula", errors.PROCESSING_CONFIG, "vsum() takes 1 argument(s); got 2"},
		{`vsum(vnormalize(xy, "L1"))`, "filter", errors.PROCESSING_CONFIG, `vnormalize(): mode "L1" is not "l1" or "l2" (case-sensitive)`},
		{"vcenter(xy)", "filter", errors.PROCESSING_CONFIG, "filter expression returns a vector; reduce it to a scalar (vsum, vmean, dot, cosine, argmax, ...) — vcenter / vzscore / vnormalize are usable inside an expression only"},
		{"dot(xy, [1, 2, 3])", "formula", errors.PROCESSING_VECTOR_LENGTH_MISMATCH, "dot(): vector length mismatch: 2 vs 3"},
		{"dot(xy, xy)", "formula", "", ""},
		{`vsum(vnormalize(xy, m))`, "formula", "", ""},
		{"dot(xy", "formula", "", ""},
	} {
		ce := ExprStaticIssue(c.src, c.what, lenOf)
		if c.code == "" {
			if ce != nil {
				t.Errorf("%s: %v, want none", c.src, ce)
			}
			continue
		}
		if ce == nil || ce.Code != c.code || ce.Message != c.msg {
			t.Errorf("%s: %v, want %s %q", c.src, ce, c.code, c.msg)
		}
	}
}

func TestStaticIssues_Sites(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "vcenter(xy)"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "a", Expression: "x / total(vzscore(xy))"},
			{Type: types.ATTR_FORMULA, Label: "b", Expression: "total(total(vzscore(xy)))"},
			{Type: types.ATTR_FORMULA, Label: "c", Expression: "vcenter(xy)"},
		},
	}
	got := StaticIssues(req, nil, nil)
	want := []string{"filter", "formula total() argument", "formula"}
	if len(got) != len(want) {
		t.Fatalf("got %d issues %v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i].Details["expression_site"] != w {
			t.Errorf("issue %d: %v, want site %q", i, got[i].Details, w)
		}
	}
}
