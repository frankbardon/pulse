package targetencode

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func seed(v int64) *int64 { return &v }

// TestCheck pins the shared params rules in their judged order; the
// message is the one the constructor and predict both raise.
func TestCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		p    Params
		want string // "" = accepted
	}{
		{"target only", Params{Target: "y"}, ""},
		{"fit_on and folds", Params{Target: "y", FitOn: "split == 0", Folds: 5, RngSeed: seed(7)}, ""},
		{"folds without fit_on", Params{Target: "y", Folds: 2}, ""},
		{"max folds", Params{Target: "y", Folds: MaxFolds}, ""},
		{"no target", Params{FitOn: "split == 0"}, "FEAT_TARGET_ENCODE: 'target' is required in params"},
		{"negative smoothing", Params{Target: "y", Smoothing: -1}, "FEAT_TARGET_ENCODE: 'smoothing' must be >= 0"},
		{"blank fit_on", Params{Target: "y", FitOn: "  "}, "FEAT_TARGET_ENCODE: 'fit_on' must be a non-blank boolean expression (omit it to fit on every row)"},
		{"one fold", Params{Target: "y", Folds: 1}, "FEAT_TARGET_ENCODE: 'folds' must be 0 (off) or an integer from 2 to 100; got 1"},
		{"negative folds", Params{Target: "y", Folds: -3}, "FEAT_TARGET_ENCODE: 'folds' must be 0 (off) or an integer from 2 to 100; got -3"},
		{"too many folds", Params{Target: "y", Folds: MaxFolds + 1}, "FEAT_TARGET_ENCODE: 'folds' must be 0 (off) or an integer from 2 to 100; got 101"},
		{"seed without folds", Params{Target: "y", RngSeed: seed(0)}, "FEAT_TARGET_ENCODE: 'rng_seed' seeds the fold assignment and is only valid with 'folds'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Check(c.p)
			if c.want == "" {
				if got != nil {
					t.Fatalf("refused %v", got)
				}
				return
			}
			if got == nil || got.Code != errors.PROCESSING_CONFIG || got.Message != c.want {
				t.Fatalf("got %v, want PROCESSING_CONFIG %q", got, c.want)
			}
		})
	}
}

// TestStreamable: the pre-pass reads field, target and every fit_on name
// from the raw record, so each must be a stored field.
func TestStreamable(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "s", Type: encoding.FieldTypeU8},
	}}
	cases := []struct {
		name  string
		field string
		p     Params
		want  bool
	}{
		{"plain", "g", Params{Target: "y"}, true},
		{"fit_on stored", "g", Params{Target: "y", FitOn: "s == 0 && y >= 0"}, true},
		{"fit_on derived", "g", Params{Target: "y", FitOn: "split == 0"}, false},
		{"fit_on mixed", "g", Params{Target: "y", FitOn: "s == 0 || split == 0"}, false},
		{"fit_on $env", "g", Params{Target: "y", FitOn: `$env["s"] == 0`}, false},
		{"fit_on unparsable", "g", Params{Target: "y", FitOn: "s == ("}, false},
		{"derived target", "g", Params{Target: "LOG_y"}, false},
		{"derived field", "BUCKET_y", Params{Target: "y"}, false},
	}
	for _, c := range cases {
		if got := Streamable(c.field, c.p, schema); got != c.want {
			t.Errorf("%s: Streamable = %v, want %v", c.name, got, c.want)
		}
	}
	if Streamable("g", Params{Target: "y"}, nil) {
		t.Error("a nil schema knows no stored field")
	}
}
