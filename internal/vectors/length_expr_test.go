package vectors

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestExprLengthIssue(t *testing.T) {
	t.Parallel()

	lenOf := func(name string) (int, bool) {
		switch name {
		case "a":
			return 2, true
		case "b":
			return 3, true
		}
		return 0, false
	}
	for _, c := range []struct {
		src         string
		fn          string
		left, right int
	}{
		{"dot(a, b)", "dot", 2, 3},
		{"cosine(b, [1, 2])", "cosine", 3, 2},
		{"dist([1], [1, 2])", "dist", 1, 2},
		{"vsum(a) + dot(a, b)", "dot", 2, 3},
		{"dot(a, a)", "", 0, 0},
		{"dot(a, [1, 2])", "", 0, 0},
		{"dot(vcenter(a), b)", "", 0, 0},
		{"dot(b[0:2], a)", "", 0, 0},
		{"dot(a, unknown)", "", 0, 0},
		{"let a = [1, 2, 3]; dot(a, b)", "", 0, 0},
		{"dot(a)", "", 0, 0},
		{"dot(", "", 0, 0},
	} {
		got := ExprLengthIssue(c.src, lenOf)
		if c.fn == "" {
			if got != nil {
				t.Errorf("%s: %v, want nil", c.src, got)
			}
			continue
		}
		if got == nil || got.Code != errors.PROCESSING_VECTOR_LENGTH_MISMATCH ||
			got.Details["function"] != c.fn || got.Details["left"] != c.left || got.Details["right"] != c.right {
			t.Errorf("%s: %v, want %s %d vs %d", c.src, got, c.fn, c.left, c.right)
		}
	}
}

// TestExprBindings: a request vector binds unless a scalar field of its
// name shadows it; a native vector field binds although it is a field;
// an unknown name binds nothing; the order is refs'.
func TestExprBindings(t *testing.T) {
	t.Parallel()

	resolved := []Resolved{
		{Name: "bat", Members: []string{"a", "b"}},
		{Name: "shadowed", Members: []string{"a"}},
		{Name: "emb", Members: []string{"emb[0]"}, Native: &NativeVector{}},
	}
	fields := map[string]bool{"shadowed": true, "emb": true, "a": true}
	got := ExprBindings([]string{"emb", "a", "shadowed", "nope", "bat"}, resolved, func(n string) bool { return fields[n] })
	var names []string
	for _, v := range got {
		names = append(names, v.Name)
	}
	if strings.Join(names, ",") != "emb,bat" {
		t.Fatalf("ExprBindings = %v, want [emb bat]", names)
	}
	if ExprBindings([]string{"bat"}, nil, func(string) bool { return false }) != nil {
		t.Error("no resolved vectors must bind nothing")
	}
}
