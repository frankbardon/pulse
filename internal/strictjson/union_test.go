package strictjson

import (
	"reflect"
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

// Every root that reads a union (a chain's stage 0, a facet, a sample,
// a lookup) refuses a nested union PULSE_UNION_NESTED, and a chain
// stage's join with a union on its right PULSE_UNION_JOIN_RIGHT —
// located like any unknown key — while its well-placed union decodes.
func TestDecode_MisplacedUnionIsCodedOnEveryRoot(t *testing.T) {
	t.Parallel()
	member := `{"cohort":{"filename":"a.pulse"},"label":"a"}`
	nested := `{"cohort":{"filename":"a.pulse"},"label":"a","union":[` + member + `]}`
	nestedCohort := `{"cohort":{"filename":"a.pulse","union":[` + member + `]},"label":"a"}`
	decoders := map[string]func(string) error{
		"chain": func(b string) error {
			_, err := Decode[types.ChainRequest]([]byte(b), Options{})
			return err
		},
		"facet": func(b string) error {
			_, err := Decode[types.FacetRequest]([]byte(b), Options{})
			return err
		},
		"sample": func(b string) error {
			_, err := Decode[types.SampleRequest]([]byte(b), Options{})
			return err
		},
		"lookup": func(b string) error {
			_, err := Decode[types.LookupRequest]([]byte(b), Options{})
			return err
		},
	}
	for root, decode := range decoders {
		for _, tc := range []struct{ name, body, path string }{
			{"member", `{"union":[` + nested + `]}`, "union[0].union"},
			{"member cohort", `{"union":[` + nestedCohort + `]}`, "union[0].cohort.union"},
		} {
			ce := codedOf(t, decode(tc.body))
			if ce.Code != perr.PULSE_UNION_NESTED || ce.Details["path"] != tc.path {
				t.Errorf("%s %s: %s at %v, want PULSE_UNION_NESTED at %s", root, tc.name, ce.Code, ce.Details["path"], tc.path)
			}
		}
		if err := decode(`{"union":[` + member + `],"union_source_field":"wave"}`); err != nil {
			t.Errorf("%s: a well-placed union: %v", root, err)
		}
	}
	ce := codedOf(t, decoders["chain"](`{"union":[`+member+`],"stages":[{"request":{"joins":[{"union":[`+member+`],"on":[{"left_field":"k","right_field":"k"}]}]}}]}`))
	if ce.Code != perr.PULSE_UNION_JOIN_RIGHT || ce.Details["path"] != "joins[0].union" {
		t.Errorf("chain stage join: %s at %v, want PULSE_UNION_JOIN_RIGHT", ce.Code, ce.Details["path"])
	}
}

// On an instance hiding the union (Options.Hidden names `union` on a
// types.Request), a `union` key on a join is a plain unknown key — the
// refusal a build without a union gives — never a PULSE_UNION_* code.
func TestDecode_MisplacedUnionPlainWhenUnionHidden(t *testing.T) {
	t.Parallel()
	hidden := Options{Hidden: func(t reflect.Type) []string {
		if t == reflect.TypeFor[types.Request]() {
			return []string{"union", "union_source_field"}
		}
		return nil
	}}
	body := `{"joins":[{"union":[{"cohort":{"filename":"b.pulse"},"label":"b"}],"on":[{"left_field":"k","right_field":"k"}]}]}`
	_, err := Decode[types.Request]([]byte(body), hidden)
	ce := codedOf(t, err)
	if ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["path"] != "joins[0].union" {
		t.Errorf("hidden: %s at %v, want PULSE_REQUEST_UNKNOWN_FIELD at joins[0].union", ce.Code, ce.Details["path"])
	}
	// The visible twin keeps the coded refusal.
	_, err = Decode[types.Request]([]byte(body), Options{})
	if ce := codedOf(t, err); ce.Code != perr.PULSE_UNION_JOIN_RIGHT {
		t.Errorf("visible: %s, want PULSE_UNION_JOIN_RIGHT", ce.Code)
	}
}
