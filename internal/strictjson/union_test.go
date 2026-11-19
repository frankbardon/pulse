package strictjson

import (
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// A `union` written where no union can be read is refused with its own
// code, still located like any unknown key: on a join (its right side)
// PULSE_UNION_JOIN_RIGHT, inside a union member or a member's cohort
// PULSE_UNION_NESTED. A `union` anywhere else stays
// PULSE_REQUEST_UNKNOWN_FIELD.
func TestDecode_MisplacedUnionIsCoded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body string
		code       perr.Code
		path       string
	}{
		{"join right side",
			`{"union":[{"cohort":{"filename":"a.pulse"},"label":"a"}],"joins":[{"union":[{"cohort":{"filename":"b.pulse"},"label":"b"}],"on":[{"left_field":"k","right_field":"k"}]}]}`,
			perr.PULSE_UNION_JOIN_RIGHT, "joins[0].union"},
		{"member",
			`{"union":[{"cohort":{"filename":"a.pulse"},"label":"a","union":[{"cohort":{"filename":"b.pulse"},"label":"b"}]}]}`,
			perr.PULSE_UNION_NESTED, "union[0].union"},
		{"member cohort",
			`{"union":[{"cohort":{"filename":"a.pulse","union":[]},"label":"a"}]}`,
			perr.PULSE_UNION_NESTED, "union[0].cohort.union"},
		{"request cohort",
			`{"cohort":{"filename":"a.pulse","union":[]}}`,
			perr.PULSE_REQUEST_UNKNOWN_FIELD, "cohort.union"},
		{"compose slot member",
			`{"requests":[{"union":[{"cohort":{"filename":"a.pulse"},"label":"a","union":[]}]}]}`,
			perr.PULSE_UNION_NESTED, "union[0].union"},
	}
	for _, tc := range cases {
		var err error
		if tc.name == "compose slot member" {
			_, err = Decode[types.ComposedRequest]([]byte(tc.body), Options{})
		} else {
			_, err = Decode[types.Request]([]byte(tc.body), Options{})
		}
		ce := codedOf(t, err)
		if ce.Code != tc.code || ce.Details["path"] != tc.path {
			t.Errorf("%s: %s at %v, want %s at %s (%s)", tc.name, ce.Code, ce.Details["path"], tc.code, tc.path, ce.Message)
		}
	}
	// A well-placed union decodes.
	req, err := Decode[types.Request]([]byte(`{"union":[{"cohort":{"filename":"a.pulse"},"label":"a"}],"union_source_field":"wave"}`), Options{})
	if err != nil || len(req.Union) != 1 || req.Union[0].Label != "a" || req.UnionSourceField != "wave" {
		t.Fatalf("decode = %+v, %v", req, err)
	}
}
