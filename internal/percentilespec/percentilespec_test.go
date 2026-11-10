package percentilespec_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/percentilespec"
	"github.com/frankbardon/pulse/types"
)

// TestResolve: the percentile a slot reads, and the refusal (code,
// message, details) predict and the runtime constructors share.
func TestResolve(t *testing.T) {
	t.Parallel()

	ok := []struct {
		raw  string
		want float64
	}{
		{``, percentilespec.Default},
		{`{}`, 0},
		{`{"percentile":null}`, 0},
		{`{"percentile":0}`, 0},
		{`{"percentile":100}`, 100},
		{`{"percentile":97.5}`, 97.5},
		{`{"percentile":90,"ordered":true}`, 90},
	}
	for _, tc := range ok {
		got, cerr := percentilespec.Resolve(json.RawMessage(tc.raw))
		if cerr != nil || got != tc.want {
			t.Errorf("Resolve(%s) = %v, %v; want %v", tc.raw, got, cerr, tc.want)
		}
	}

	refused := []struct {
		raw, msg string
		details  map[string]any
	}{
		{`{"percentile":100.0001}`, "percentile must be between 0 and 100",
			map[string]any{"aggregation": "AGG_PERCENTILE", "param": "percentile", "value": 100.0001}},
		{`{"percentile":-1}`, "percentile must be between 0 and 100",
			map[string]any{"aggregation": "AGG_PERCENTILE", "param": "percentile", "value": -1.0}},
		{`{"percentile":"50"}`, "invalid percentile params: json: cannot unmarshal string into Go struct field Params.percentile of type float64",
			map[string]any{"aggregation": "AGG_PERCENTILE", "param": "percentile"}},
	}
	for _, tc := range refused {
		_, cerr := percentilespec.Resolve(json.RawMessage(tc.raw))
		if cerr == nil {
			t.Errorf("Resolve(%s) accepted; want a refusal", tc.raw)
			continue
		}
		if cerr.Code != errors.PROCESSING_CONFIG || cerr.Message != tc.msg || !reflect.DeepEqual(cerr.Details, tc.details) {
			t.Errorf("Resolve(%s) = %s %q %v; want %s %q %v", tc.raw, cerr.Code, cerr.Message, cerr.Details, errors.PROCESSING_CONFIG, tc.msg, tc.details)
		}
	}

	if !percentilespec.Applies(types.AGG_PERCENTILE) || percentilespec.Applies(types.AGG_MEDIAN) {
		t.Error("Applies: want AGG_PERCENTILE only")
	}
}
