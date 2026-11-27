package skills

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

var (
	paramsSection = regexp.MustCompile(`(?s)## Params\n(.*?)\n## `)
	paramsRowName = regexp.MustCompile("(?m)^\\| `([a-z_0-9]+)`")
)

func jsonKeys(v any) map[string]bool {
	out := map[string]bool{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		if name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// TestParamsTableSaysWhereKeyGoes: an atomic skill whose Params table
// lists a key that is a top-level slot field (a filterer's `values`, a
// test's `alpha`) must say so, or an agent nests it under `params` and
// gets PULSE_REQUEST_UNKNOWN_FIELD.
func TestParamsTableSaysWhereKeyGoes(t *testing.T) {
	t.Parallel()

	slots := map[string]map[string]bool{
		"FILTER": jsonKeys(types.Filterer{}),
		"ATTR":   jsonKeys(types.Attribute{}),
		"TEST":   jsonKeys(types.Test{}),
		"GROUP":  jsonKeys(types.Group{}),
	}
	for _, stem := range embeddedMarkdownNames(t) {
		raw, _ := Get(stem)
		keys, ok := slots[ParseFrontmatter(raw)["category"]]
		if !ok || !strings.HasPrefix(stem, "op-") {
			continue
		}
		m := paramsSection.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		var top []string
		for _, r := range paramsRowName.FindAllStringSubmatch(m[1], -1) {
			if keys[r[1]] && r[1] != "field" && r[1] != "type" {
				top = append(top, r[1])
			}
		}
		if len(top) == 0 {
			continue
		}
		// The skill must say top-level / slot key somewhere in its body.
		if !strings.Contains(raw, "top-level") && !strings.Contains(raw, "slot key") && !strings.Contains(raw, "slot's") && !strings.Contains(raw, "`expression` slot") {
			t.Errorf("%s: Params lists top-level slot key(s) %v but never says they are not under params", stem, top)
		}
	}
}
