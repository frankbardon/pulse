package requestroot

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		keys []string
		want Kind
	}{
		{"request", []string{"cohort", "aggregations"}, Request},
		{"empty", nil, Request},
		{"chain", []string{"cohort", "stages"}, Chain},
		{"chain wins over compose", []string{"stages", "requests"}, Chain},
		{"facet", []string{"cohort", "fields"}, Facet},
		{"compose wins over facet", []string{"fields", "base"}, Composed},
	}
	for _, k := range ComposeKeys {
		cases = append(cases, struct {
			name string
			keys []string
			want Kind
		}{"compose/" + k, []string{"cohort", k}, Composed})
	}
	for _, c := range cases {
		top := map[string]any{}
		for _, k := range c.keys {
			top[k] = nil // a null value still counts as present
		}
		if got := Of(top); got != c.want {
			t.Errorf("%s: Of(%v) = %q, want %q", c.name, c.keys, got, c.want)
		}
	}
}

// TestRequestRoot_ImportBoundary: the rule is a stdlib-only leaf, so the
// no-execute descriptor, the doc checks and the example library's tests
// can all import it.
func TestRequestRoot_ImportBoundary(t *testing.T) {
	t.Parallel()
	const prefix = "github.com/frankbardon/pulse"
	out, err := exec.Command("go", "list", "-deps", prefix+"/internal/requestroot").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, prefix) && dep != prefix+"/internal/requestroot" {
			t.Errorf("internal/requestroot imports %s; it may import only the standard library", dep)
		}
	}
}
