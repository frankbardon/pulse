package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestPayloadSchema_LookupAndLabelModeEnums: LookupRequest.multiplicity
// and LabelBinding.mode reference closed enum defs drawn from
// types.AllLookupMultiplicities() / types.AllLabelModes() (#507). The
// CLI-only `assert-unique` alias is not offered.
func TestPayloadSchema_LookupAndLabelModeEnums(t *testing.T) {
	t.Parallel()

	var doc struct {
		Defs map[string]struct {
			Enum       []string       `json:"enum"`
			Properties map[string]any `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(BuildPayloadSchema(), &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	cases := []struct {
		host, prop, def string
		want            []string
	}{
		{"LookupRequest", "multiplicity", "LookupMultiplicity", stringify(types.AllLookupMultiplicities())},
		{"LabelBinding", "mode", "LabelMode", stringify(types.AllLabelModes())},
	}
	for _, c := range cases {
		prop, _ := doc.Defs[c.host].Properties[c.prop].(map[string]any)
		if got := prop["$ref"]; got != "#/$defs/"+c.def {
			t.Errorf("%s.%s $ref = %q, want #/$defs/%s", c.host, c.prop, got, c.def)
		}
		if got := doc.Defs[c.def].Enum; !equalStrings(got, c.want) {
			t.Errorf("$defs.%s enum = %v, want %v", c.def, got, c.want)
		}
		for _, v := range doc.Defs[c.def].Enum {
			if v == "assert-unique" {
				t.Errorf("$defs.%s offers the CLI-only alias %q", c.def, v)
			}
		}
	}
}
