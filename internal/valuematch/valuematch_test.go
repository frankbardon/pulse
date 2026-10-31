package valuematch

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestFilterKeys(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	for _, s := range []string{"a", "b"} {
		if _, err := dict.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	cat := &encoding.Field{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}
	num := &encoding.Field{Name: "x", Type: encoding.FieldTypeF64}

	keys, err := FilterKeys(types.FILTER_INCLUDE, "g", cat, []string{"b", "a"})
	if err != nil || len(keys) != 2 || !keys[0] || !keys[1] {
		t.Fatalf("labels: keys %v err %v, want IDs 0 and 1", keys, err)
	}
	if keys, err := FilterKeys(types.FILTER_EXCLUDE, "x", num, []string{"2.5"}); err != nil || !keys[2.5] {
		t.Fatalf("number: keys %v err %v", keys, err)
	}
	if keys, err := FilterKeys(types.FILTER_INCLUDE, "y", nil, []string{"7"}); err != nil || !keys[7] {
		t.Fatalf("absent field parses as number: keys %v err %v", keys, err)
	}

	cases := []struct {
		ft    types.FiltererType
		field *encoding.Field
		name  string
		value string
		want  string
	}{
		{types.FILTER_INCLUDE, cat, "g", "zz", `include value "zz" not found in dictionary for field "g"`},
		{types.FILTER_EXCLUDE, cat, "g", "zz", `exclude value "zz" not found in dictionary for field "g"`},
		{types.FILTER_INCLUDE, num, "x", "abc", `parsing include value "abc"`},
		{types.FILTER_EXCLUDE, num, "x", "abc", `parsing exclude value "abc"`},
	}
	for _, c := range cases {
		_, err := FilterKeys(c.ft, c.name, c.field, []string{c.value})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Message != c.want {
			t.Fatalf("%s %s %q: err %v, want PROCESSING_CONFIG %q", c.ft, c.name, c.value, err, c.want)
		}
	}
}
