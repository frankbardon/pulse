package strictjson

import (
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestDecodeRequestInto_EveryFaultCoded: at a request entry every fault
// is coded — an unknown key keeps PULSE_REQUEST_UNKNOWN_FIELD, every
// other decode fault is SERVICE_VALIDATION "parsing <what> JSON",
// details.reason request_decode, the encoding/json fault kept as Cause.
func TestDecodeRequestInto_EveryFaultCoded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		code   perr.Code
		offset bool
		field  string
	}{
		{name: "unknown key", body: `{"filters":[]}`, code: perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{name: "malformed", body: `{"cohort":`, code: perr.SERVICE_VALIDATION},
		{name: "syntax", body: `{"cohort" 1}`, code: perr.SERVICE_VALIDATION, offset: true},
		{name: "wrong type", body: `{"label":7}`, code: perr.SERVICE_VALIDATION, offset: true, field: "label"},
		{name: "trailing data", body: `{} {}`, code: perr.SERVICE_VALIDATION},
		{name: "type's own unmarshal fault", body: `{"weight":7}`, code: perr.SERVICE_VALIDATION, offset: true, field: "weight"},
		{name: "not an object", body: `[1]`, code: perr.SERVICE_VALIDATION, offset: true},
	}
	for _, c := range cases {
		var req types.Request
		err := DecodeRequestInto([]byte(c.body), &req, Options{AllowMeta: true}, "request")
		ce := codedOf(t, err)
		if ce.Code != c.code {
			t.Fatalf("%s: code %s, want %s (%v)", c.name, ce.Code, c.code, err)
		}
		if c.code != perr.SERVICE_VALIDATION {
			continue
		}
		if !strings.HasPrefix(ce.Message, "parsing request JSON: ") || ce.Cause == nil {
			t.Fatalf("%s: message %q / cause %v", c.name, ce.Message, ce.Cause)
		}
		if ce.Details["reason"] != ReasonRequestDecode {
			t.Fatalf("%s: details %v", c.name, ce.Details)
		}
		if _, ok := ce.Details["offset"]; ok != c.offset {
			t.Fatalf("%s: offset present %v, want %v (%v)", c.name, ok, c.offset, ce.Details)
		}
		if got, _ := ce.Details["field"].(string); got != c.field {
			t.Fatalf("%s: field %q, want %q", c.name, got, c.field)
		}
	}

	var req types.Request
	if err := DecodeRequestInto([]byte(`{"cohort":{"filename":"c.pulse"},"_meta":{}}`), &req, Options{AllowMeta: true}, "request"); err != nil {
		t.Fatalf("a well-formed request refused: %v", err)
	}
}
