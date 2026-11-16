package splitmode

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestCheck pins the mode rule: which params each mode reads and the
// refusal (code + details.reason) for each disagreement.
func TestCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		p      Params
		reason string
	}{
		{"absent mode", Params{SeedSet: true, Stratify: "g"}, ""},
		{"random", Params{Mode: Random, SeedSet: true}, ""},
		{"temporal", Params{Mode: Temporal, OrderBy: "week"}, ""},
		{"order_by without temporal", Params{OrderBy: "week"}, ReasonOrderByUnused},
		{"order_by under random", Params{Mode: Random, OrderBy: "week"}, ReasonOrderByUnused},
		{"temporal without order_by", Params{Mode: Temporal}, ReasonOrderByMissing},
		{"temporal with a seed", Params{Mode: Temporal, OrderBy: "week", SeedSet: true}, ReasonSeedTemporal},
		{"temporal with stratify", Params{Mode: Temporal, OrderBy: "week", Stratify: "g"}, ReasonStratifyTemporal},
		{"unknown mode", Params{Mode: "time"}, ReasonModeUnknown},
	}
	for _, c := range cases {
		got := Check(c.p)
		if c.reason == "" {
			if got != nil {
				t.Errorf("%s: refused %v", c.name, got)
			}
			continue
		}
		if got == nil || got.Code != errors.PROCESSING_CONFIG || got.Details["reason"] != c.reason {
			t.Errorf("%s: %v, want PROCESSING_CONFIG reason %s", c.name, got, c.reason)
		}
	}
	if !IsTemporal(Temporal) || IsTemporal(Random) || IsTemporal("") {
		t.Error("IsTemporal")
	}
	if m := Modes(); len(m) != 2 || m[0] != Random {
		t.Errorf("Modes() = %v, want the default first", m)
	}
}
