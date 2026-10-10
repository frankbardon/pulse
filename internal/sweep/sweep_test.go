package sweep

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func validSpec() *types.SweepSpec {
	return &types.SweepSpec{
		Axes: []types.SweepAxis{
			{Name: "tv", Values: []any{json.Number("10"), json.Number("0.5")}},
			{Name: "_flag2", Values: []any{true, "x"}},
		},
		Request: json.RawMessage(` {"cohort":{"filename":"c.pulse"}} `),
	}
}

func intPtr(n int) *int { return &n }

// TestValidate_Rules drives every rule: one valid twin per edge, and one
// fault per reason with the details that name the axis or field.
func TestValidate_Rules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *types.SweepSpec)
		reason string         // "" = valid
		want   map[string]any // details subset
	}{
		{"valid", func(*types.SweepSpec) {}, "", nil},
		{"valid grid", func(s *types.SweepSpec) { s.Mode = types.SweepModeGrid }, "", nil},
		{"valid zip equal lengths", func(s *types.SweepSpec) { s.Mode = types.SweepModeZip }, "", nil},
		{"valid go scalars", func(s *types.SweepSpec) {
			s.Axes[0].Values = []any{1, int8(2), uint64(3), 1.5, float32(2.5), "s", false}
		}, "", nil},
		{"valid overlays", func(s *types.SweepSpec) { s.Overlays = json.RawMessage(`[{"kind":"OVERLAY_RANK"}]`) }, "", nil},
		{"null overlays", func(s *types.SweepSpec) { s.Overlays = json.RawMessage(`null`) }, "", nil},
		{"valid rank", func(s *types.SweepSpec) {
			s.Rank = &types.SweepRank{By: "data[0].v", Order: types.SweepRankDesc, Top: intPtr(1)}
		}, "", nil},
		{"valid rank defaults", func(s *types.SweepSpec) { s.Rank = &types.SweepRank{By: "x"} }, "", nil},

		{"no axes", func(s *types.SweepSpec) { s.Axes = nil }, ReasonAxesEmpty,
			map[string]any{"field": "sweep.axes"}},
		{"empty name", func(s *types.SweepSpec) { s.Axes[1].Name = "" }, ReasonAxisNameInvalid,
			map[string]any{"field": "sweep.axes[1].name"}},
		{"name starts with digit", func(s *types.SweepSpec) { s.Axes[0].Name = "1tv" }, ReasonAxisNameInvalid,
			map[string]any{"field": "sweep.axes[0].name", "axis": "1tv"}},
		{"name with dash", func(s *types.SweepSpec) { s.Axes[0].Name = "t-v" }, ReasonAxisNameInvalid,
			map[string]any{"axis": "t-v"}},
		{"duplicate name", func(s *types.SweepSpec) { s.Axes[1].Name = "tv" }, ReasonAxisNameDuplicate,
			map[string]any{"field": "sweep.axes[1].name", "axis": "tv", "first": "sweep.axes[0]"}},
		{"empty values", func(s *types.SweepSpec) { s.Axes[1].Values = nil }, ReasonValuesEmpty,
			map[string]any{"field": "sweep.axes[1].values", "axis": "_flag2"}},
		{"null value", func(s *types.SweepSpec) { s.Axes[0].Values = []any{json.Number("1"), nil} }, ReasonValueNotScalar,
			map[string]any{"field": "sweep.axes[0].values[1]", "axis": "tv"}},
		{"object value", func(s *types.SweepSpec) { s.Axes[0].Values = []any{map[string]any{}} }, ReasonValueNotScalar,
			map[string]any{"field": "sweep.axes[0].values[0]"}},
		{"array value", func(s *types.SweepSpec) { s.Axes[0].Values = []any{[]any{1}} }, ReasonValueNotScalar, nil},
		{"NaN value", func(s *types.SweepSpec) { s.Axes[0].Values = []any{math.NaN()} }, ReasonValueNotScalar, nil},
		{"Inf float32 value", func(s *types.SweepSpec) { s.Axes[0].Values = []any{float32(math.Inf(1))} }, ReasonValueNotScalar, nil},
		{"bad number text", func(s *types.SweepSpec) { s.Axes[0].Values = []any{json.Number("1x")} }, ReasonValueNotScalar, nil},
		{"string number text", func(s *types.SweepSpec) { s.Axes[0].Values = []any{json.Number(`"1"`)} }, ReasonValueNotScalar, nil},
		{"unknown mode", func(s *types.SweepSpec) { s.Mode = "cross" }, ReasonModeUnknown,
			map[string]any{"field": "sweep.mode", "value": "cross", "valid": []string{"grid", "zip"}}},
		{"zip unequal", func(s *types.SweepSpec) {
			s.Mode = types.SweepModeZip
			s.Axes[1].Values = []any{true}
		}, ReasonZipLengthMismatch,
			map[string]any{"field": "sweep.axes[1].values", "axis": "_flag2", "lengths": map[string]any{"tv": 2, "_flag2": 1}}},
		{"grid unequal is fine", func(s *types.SweepSpec) { s.Axes[1].Values = []any{true} }, "", nil},
		{"request missing", func(s *types.SweepSpec) { s.Request = nil }, ReasonRequestMissing,
			map[string]any{"field": "sweep.request"}},
		{"request null", func(s *types.SweepSpec) { s.Request = json.RawMessage(`null`) }, ReasonRequestMissing, nil},
		{"request array", func(s *types.SweepSpec) { s.Request = json.RawMessage(`[]`) }, ReasonRequestNotObject,
			map[string]any{"field": "sweep.request"}},
		{"request malformed", func(s *types.SweepSpec) { s.Request = json.RawMessage(`{"a":`) }, ReasonRequestNotObject, nil},
		{"overlays object", func(s *types.SweepSpec) { s.Overlays = json.RawMessage(`{}`) }, ReasonOverlaysNotArray,
			map[string]any{"field": "sweep.overlays"}},
		{"overlays malformed", func(s *types.SweepSpec) { s.Overlays = json.RawMessage(`[`) }, ReasonOverlaysNotArray, nil},
		{"rank by empty", func(s *types.SweepSpec) { s.Rank = &types.SweepRank{} }, ReasonRankByEmpty,
			map[string]any{"field": "sweep.rank.by"}},
		{"rank order unknown", func(s *types.SweepSpec) { s.Rank = &types.SweepRank{By: "x", Order: "up"} }, ReasonRankOrderUnknown,
			map[string]any{"field": "sweep.rank.order", "value": "up", "valid": []string{"asc", "desc"}}},
		{"rank top zero", func(s *types.SweepSpec) { s.Rank = &types.SweepRank{By: "x", Top: intPtr(0)} }, ReasonRankTopInvalid,
			map[string]any{"field": "sweep.rank.top", "value": 0}},
		{"rank top negative", func(s *types.SweepSpec) { s.Rank = &types.SweepRank{By: "x", Top: intPtr(-2)} }, ReasonRankTopInvalid, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := validSpec()
			c.mutate(s)
			ce := Validate(s)
			if c.reason == "" {
				if ce != nil {
					t.Fatalf("valid spec refused: %v %v", ce, ce.Details)
				}
				return
			}
			if ce == nil {
				t.Fatalf("want %s, got nil", c.reason)
			}
			if ce.Code != errors.PULSE_SWEEP_INVALID {
				t.Fatalf("code %s", ce.Code)
			}
			if ce.Details["reason"] != c.reason {
				t.Fatalf("reason %v, want %s (%s)", ce.Details["reason"], c.reason, ce.Message)
			}
			if _, ok := ce.Details["field"]; !ok {
				t.Errorf("details carry no field: %v", ce.Details)
			}
			for k, v := range c.want {
				if !reflect.DeepEqual(ce.Details[k], v) {
					t.Errorf("details[%s] = %#v, want %#v", k, ce.Details[k], v)
				}
			}
		})
	}
}

func TestValidate_Nil(t *testing.T) {
	if ce := Validate(nil); ce != nil {
		t.Fatalf("nil spec refused: %v", ce)
	}
}

// TestValidate_FirstFaultWins: rules run in contract order — an axis
// fault is reported before a mode fault before a rank fault.
func TestValidate_FirstFaultWins(t *testing.T) {
	s := validSpec()
	s.Axes[1].Values = nil
	s.Mode = "bad"
	s.Rank = &types.SweepRank{}
	if ce := Validate(s); ce == nil || ce.Details["reason"] != ReasonValuesEmpty {
		t.Fatalf("got %v", ce)
	}
	s.Axes[1].Values = []any{1}
	if ce := Validate(s); ce == nil || ce.Details["reason"] != ReasonModeUnknown {
		t.Fatalf("got %v", ce)
	}
}

// TestValidate_DecodedJSON: a wire sweep decodes into values Validate
// accepts (numbers arrive as json.Number) and refuses a null value.
func TestValidate_DecodedJSON(t *testing.T) {
	var cr types.ComposedRequest
	if err := json.Unmarshal([]byte(`{"requests":[],"sweep":{"axes":[{"name":"a","values":[1,2.5,"s",false]}],"request":{}}}`), &cr); err != nil {
		t.Fatal(err)
	}
	if ce := Validate(cr.Sweep); ce != nil {
		t.Fatalf("refused: %v", ce)
	}
	if err := json.Unmarshal([]byte(`{"requests":[],"sweep":{"axes":[{"name":"a","values":[null]}],"request":{}}}`), &cr); err != nil {
		t.Fatal(err)
	}
	if ce := Validate(cr.Sweep); ce == nil || ce.Details["reason"] != ReasonValueNotScalar {
		t.Fatalf("got %v", ce)
	}
}
