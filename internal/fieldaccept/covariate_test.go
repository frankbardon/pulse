package fieldaccept

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestCovariateRefusal(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	if _, err := dict.Add("a"); err != nil {
		t.Fatal(err)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "pre", Type: encoding.FieldTypeU16},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "dec", Type: encoding.FieldTypeDecimal128},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
	}}
	for _, tc := range []struct {
		name, split, cov string
		schema           *encoding.Schema
		reason           string // "" = accepted
	}{
		{"absent", "arm", "", schema, ""},
		{"absent without split", "", "", schema, ""},
		{"integer covariate", "arm", "pre", schema, ""},
		{"packed_bool covariate", "arm", "flag", schema, ""},
		{"decimal covariate", "arm", "dec", schema, ""},
		{"unknown name is the field-reference walk's", "arm", "nope", schema, ""},
		{"no schema", "arm", "pre", nil, ""},
		{"without split_by", "", "pre", schema, CovariateReasonWithoutSplitBy},
		{"without split_by, no schema", "", "pre", nil, CovariateReasonWithoutSplitBy},
		{"the test's own field", "arm", "y", schema, CovariateReasonIsField},
		{"categorical", "arm", "arm", schema, CovariateReasonNotNumeric},
		{"set", "arm", "tags", schema, CovariateReasonNotNumeric},
	} {
		cerr := CovariateRefusal("TEST_WELCH", "y", tc.split, tc.cov, tc.schema)
		if tc.reason == "" {
			if cerr != nil {
				t.Errorf("%s: refused %v", tc.name, cerr)
			}
			continue
		}
		if cerr == nil || cerr.Code != errors.PULSE_TEST_COVARIATE_INVALID || cerr.Details["reason"] != tc.reason {
			t.Errorf("%s: %v, want PULSE_TEST_COVARIATE_INVALID reason %s", tc.name, cerr, tc.reason)
		}
	}
}

func TestTakesCovariate(t *testing.T) {
	t.Parallel()

	var got []string
	for _, op := range types.AllTestTypes() {
		if TakesCovariate(op) {
			got = append(got, string(op))
		}
	}
	if want := CovariateTests(); !slices.Equal(got, want) || len(want) != 4 {
		t.Fatalf("TakesCovariate %v, CovariateTests %v", got, want)
	}
	cerr := CovariateUnsupportedRefusal("tests[0]", "TEST_MANN_WHITNEY_U")
	if cerr.Code != errors.PULSE_TEST_COVARIATE_INVALID || cerr.Details["reason"] != CovariateReasonUnsupported ||
		cerr.Details["path"] != "tests[0].params.covariate" {
		t.Fatalf("unsupported refusal %+v", cerr)
	}
}

func TestCovariateParam(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		``:                                  "",
		`{}`:                                "",
		`{"covariate":"pre"}`:               "pre",
		`{"covariate":3}`:                   "",
		`[1]`:                               "",
		`{"reference":"c","covariate":"x"}`: "x",
	} {
		if got := CovariateParam([]byte(raw)); got != want {
			t.Errorf("CovariateParam(%s) = %q, want %q", raw, got, want)
		}
	}
}
