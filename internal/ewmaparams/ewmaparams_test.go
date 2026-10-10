package ewmaparams

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// A decay converts through the exact decimal 1 − decay: the result is the
// very double the literal alpha = 1 − decay parses to, never the float64
// subtraction (1.0 − 0.7 = 0.30000000000000004).
func TestResolve_DecayIsExactDecimalComplement(t *testing.T) {
	cases := map[string]string{
		"0":    "1",
		"0.1":  "0.9",
		"0.3":  "0.7",
		"0.5":  "0.5",
		"0.6":  "0.4",
		"0.7":  "0.3",
		"0.8":  "0.2",
		"0.9":  "0.1",
		"7e-1": "0.3",
		"0.99": "0.01",
	}
	for decay, alphaLit := range cases {
		got, ref := Resolve(json.RawMessage(`{"decay": ` + decay + `}`))
		if ref != nil {
			t.Fatalf("decay %s refused: %s", decay, ref.Message)
		}
		want, _ := strconv.ParseFloat(alphaLit, 64)
		if got != want {
			t.Errorf("decay %s: alpha = %v (%b), want literal %s = %v (%b)", decay, got, got, alphaLit, want, want)
		}
	}
	// The float64 route would be wrong here — the test above is not vacuous.
	if d := 0.7; 1-d == 0.3 {
		t.Fatal("float64 1 - 0.7 equals 0.3; the exactness cases prove nothing")
	}
}

func TestResolve_Alpha(t *testing.T) {
	for _, a := range []string{"0.3", "1", "1e-3"} {
		got, ref := Resolve(json.RawMessage(`{"alpha": ` + a + `}`))
		want, _ := strconv.ParseFloat(a, 64)
		if ref != nil || got != want {
			t.Errorf("alpha %s: got %v, %v", a, got, ref)
		}
	}
	// null reads absent, as for any optional param.
	if got, ref := Resolve(json.RawMessage(`{"alpha": 0.5, "decay": null}`)); ref != nil || got != 0.5 {
		t.Errorf("alpha with null decay: got %v, %v", got, ref)
	}
}

func TestResolve_Refusals(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{``, "exactly one of params.alpha or params.decay is required"},
		{`{}`, "exactly one of params.alpha or params.decay is required"},
		{`{"decay": null}`, "exactly one of params.alpha or params.decay is required"},
		{`{"alpha": 0.3, "decay": 0.7}`, "mutually exclusive"},
		{`{"alpha": 0}`, "params.alpha must be in (0, 1]"},
		{`{"alpha": 1.5}`, "params.alpha must be in (0, 1]"},
		{`{"alpha": -0.1}`, "params.alpha must be in (0, 1]"},
		{`{"decay": 1}`, "params.decay must be in [0, 1)"},
		{`{"decay": 1.0}`, "params.decay must be in [0, 1)"},
		{`{"decay": -0.1}`, "params.decay must be in [0, 1)"},
		{`{"decay": 2}`, "params.decay must be in [0, 1)"},
		{`{"decay": "0.7"}`, "params.decay must be a number"},
		{`{"decay": true}`, "params.decay must be a number"},
		{`{"decay": [0.7]}`, "params.decay must be a number"},
		{`{"alpha": "0.3"}`, "malformed params"},
		{`{`, "malformed params"},
	}
	for _, tc := range cases {
		_, ref := Resolve(json.RawMessage(tc.raw))
		if ref == nil {
			t.Errorf("%q: accepted, want %q", tc.raw, tc.want)
			continue
		}
		if !strings.Contains(ref.Message, tc.want) || ref.Error() != ref.Message {
			t.Errorf("%q: message %q, want %q", tc.raw, ref.Message, tc.want)
		}
		ce := ref.Coded()
		if ce.Code != errors.PROCESSING_CONFIG || ce.Message != "WIN_EWMA: "+ref.Message {
			t.Errorf("%q: coded %v %q", tc.raw, ce.Code, ce.Message)
		}
		if len(ref.Details) != len(ce.Details) {
			t.Errorf("%q: details %v, coded %v", tc.raw, ref.Details, ce.Details)
		}
	}
}
