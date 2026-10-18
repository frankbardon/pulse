package numrange

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func f(v float64) *float64 { return &v }

func TestCompile_HalfOpen(t *testing.T) {
	set, err := Compile([]Spec{
		{Label: "holdout", Start: f(91), End: f(104)},
		{Label: "train", Start: f(0), End: f(91)},
		{Label: "tail", Start: f(104)},
		{Label: "neg", End: f(0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for v, want := range map[float64]string{
		-5: "neg", 0: "train", 90.5: "train", 91: "holdout", 103.999: "holdout", 104: "tail", 1e9: "tail",
	} {
		if got, ok := set.Match(v); !ok || got != want {
			t.Errorf("Match(%v) = %q, %v; want %q", v, got, ok, want)
		}
	}
	if _, ok := set.Match(math.NaN()); ok {
		t.Error("NaN matched a range")
	}
	if set.Len() != 4 {
		t.Errorf("Len = %d", set.Len())
	}
	gap, _ := Compile([]Spec{{Label: "a", Start: f(0), End: f(1)}, {Label: "b", Start: f(2), End: f(3)}})
	if _, ok := gap.Match(1.5); ok {
		t.Error("a value in the gap matched")
	}
}

func TestCompile_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		specs []Spec
		code  errors.Code
	}{
		{"empty", nil, errors.PULSE_RANGE_EMPTY},
		{"inverted", []Spec{{Label: "a", Start: f(2), End: f(1)}}, errors.PULSE_RANGE_INVALID},
		{"zero_width", []Spec{{Label: "a", Start: f(1), End: f(1)}}, errors.PULSE_RANGE_INVALID},
		{"inf", []Spec{{Label: "a", Start: f(math.Inf(-1))}}, errors.PULSE_RANGE_INVALID},
		{"nan", []Spec{{Label: "a", End: f(math.NaN())}}, errors.PULSE_RANGE_INVALID},
		{"duplicate", []Spec{{Label: "a", End: f(1)}, {Label: "a", Start: f(1)}}, errors.PULSE_RANGE_DUPLICATE_LABEL},
		{"overlap", []Spec{{Label: "a", Start: f(0), End: f(1.5)}, {Label: "b", Start: f(1), End: f(2)}}, errors.PULSE_RANGE_OVERLAP},
		{"open_upper_then_more", []Spec{{Label: "a", Start: f(0)}, {Label: "b", Start: f(5), End: f(6)}}, errors.PULSE_RANGE_OVERLAP},
	} {
		if _, err := Compile(tc.specs); !errors.HasCode(err, tc.code) {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}
	// Adjacent half-open ranges are disjoint.
	if _, err := Compile([]Spec{{Label: "a", Start: f(0), End: f(91)}, {Label: "b", Start: f(91), End: f(104)}}); err != nil {
		t.Errorf("adjacent: %v", err)
	}
}

func TestParse(t *testing.T) {
	cfg, err := Parse(json.RawMessage(`{"ranges":[{"label":"b","start":5},{"label":"a","end":5}],"unmatched_label":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UnmatchedLabel != "x" || len(cfg.Order) != 2 || cfg.Order[0] != "b" || cfg.Set == nil {
		t.Errorf("cfg = %+v", cfg)
	}
	cfg, err = Parse(json.RawMessage(`{"table":" split "}`))
	if err != nil || cfg.Table != "split" || cfg.Set != nil || cfg.UnmatchedLabel != DefaultUnmatchedLabel {
		t.Errorf("table cfg = %+v, %v", cfg, err)
	}
	for raw, code := range map[string]errors.Code{
		`{`:                                      errors.PROCESSING_CONFIG,
		`{}`:                                     errors.PULSE_RANGE_SOURCE_AMBIGUOUS,
		`{"ranges":[{"label":"a"}],"table":"t"}`: errors.PULSE_RANGE_SOURCE_AMBIGUOUS,
		`{"ranges":[{"label":"unmatched"}]}`:     errors.PROCESSING_CONFIG,
		`{"ranges":[{"label":"a","start":"1"}]}`: errors.PROCESSING_CONFIG,
	} {
		if _, err := Parse(json.RawMessage(raw)); !errors.HasCode(err, code) {
			t.Errorf("Parse(%s) err = %v, want %s", raw, err, code)
		}
	}
	if _, err := Parse(nil); !errors.HasCode(err, errors.PULSE_RANGE_SOURCE_AMBIGUOUS) {
		t.Errorf("Parse(nil) err = %v", err)
	}
}
