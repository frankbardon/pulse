package groupfit

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		params string
		want   string
		bad    string // refused value, "" = accepted
	}{
		{"", Global, ""},
		{"null", Global, ""},
		{"[1]", Global, ""},
		{`{}`, Global, ""},
		{`{"k":3}`, Global, ""},
		{`{"fit":null}`, Global, ""},
		{`{"fit":"global"}`, Global, ""},
		{`{"fit":"within"}`, Within, ""},
		{`{"fit":"WITHIN"}`, "", "WITHIN"},
		{`{"fit":""}`, "", `""`},
		{`{"fit":1}`, "", "1"},
		{`{"fit":true}`, "", "true"},
	} {
		got, err := Parse(types.GROUP_QUANTILE, json.RawMessage(c.params))
		if c.bad == "" {
			if err != nil || got != c.want {
				t.Errorf("%s: got %q, %v; want %q", c.params, got, err, c.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: accepted %q", c.params, got)
		}
		if err.Code != errors.PROCESSING_CONFIG {
			t.Errorf("%s: code %s", c.params, err.Code)
		}
		want := map[string]any{"type": "GROUP_QUANTILE", "param": "fit", "value": c.bad, "valid": []string{"global", "within"}, "reason": "bad_fit"}
		if !reflect.DeepEqual(err.Details, want) {
			t.Errorf("%s: details %v, want %v", c.params, err.Details, want)
		}
	}
}

func TestIsWithin(t *testing.T) {
	within := json.RawMessage(`{"fit":"within"}`)
	for _, c := range []struct {
		g    *types.Group
		want bool
	}{
		{nil, false},
		{&types.Group{Type: types.GROUP_QUANTILE, Params: within}, true},
		{&types.Group{Type: types.GROUP_KMEANS, Params: json.RawMessage(`{"k":2,"fit":"within"}`)}, true},
		{&types.Group{Type: types.GROUP_QUANTILE}, false},
		{&types.Group{Type: types.GROUP_QUANTILE, Params: json.RawMessage(`{"fit":"nope"}`)}, false},
		// A record-local grouper has no fit to choose.
		{&types.Group{Type: types.GROUP_CATEGORY, Params: within}, false},
	} {
		if got := IsWithin(c.g); got != c.want {
			t.Errorf("%+v: got %v, want %v", c.g, got, c.want)
		}
	}
}
