package opconfig_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/opconfig"
	"github.com/frankbardon/pulse/types"
)

// TestOpConfig_Rules pins each rule's accept / refuse verdict; the
// predict ≡ runtime parity is pinned at the facade
// (TestOperatorConfig_PredictMatchesRuntime).
func TestOpConfig_Rules(t *testing.T) {
	t.Parallel()
	refused := func(t *testing.T, cerr *errors.CodedError, want bool) {
		t.Helper()
		if (cerr != nil) != want {
			t.Fatalf("refused = %v (%v), want %v", cerr != nil, cerr, want)
		}
		if cerr != nil && cerr.Code != errors.PROCESSING_CONFIG {
			t.Fatalf("code = %s, want PROCESSING_CONFIG", cerr.Code)
		}
	}
	t.Run("distinct_sum", func(t *testing.T) {
		key, cerr := opconfig.DistinctSumKey(json.RawMessage(`{"distinct_by":"id"}`))
		refused(t, cerr, false)
		if key != "id" {
			t.Fatalf("key = %q", key)
		}
		_, cerr = opconfig.DistinctSumKey(nil)
		refused(t, cerr, true)
	})
	t.Run("ci", func(t *testing.T) {
		ci, cerr := opconfig.CIParams(types.AGG_CI_LOWER, json.RawMessage(`{"confidence":0}`))
		refused(t, cerr, false)
		if ci.Confidence != 0.95 || ci.Method != opconfig.CIMethodNormal {
			t.Fatalf("defaults = %+v", ci)
		}
		for _, raw := range []string{`{"method":"bootstrap"}`, `{"method":"x"}`, `{"confidence":1}`} {
			_, cerr = opconfig.CIParams(types.AGG_CI_UPPER, json.RawMessage(raw))
			refused(t, cerr, true)
		}
	})
	t.Run("set", func(t *testing.T) {
		d := encoding.NewDictionary()
		for _, l := range []string{"a", "b"} {
			if _, err := d.Add(l); err != nil {
				t.Fatal(err)
			}
		}
		f := &encoding.Field{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: d}
		bit, cerr := opconfig.SetHasBit("tags", f, "b")
		refused(t, cerr, false)
		if bit != 1 {
			t.Fatalf("bit = %d", bit)
		}
		_, cerr = opconfig.SetHasBit("tags", f, "z")
		refused(t, cerr, true)
		_, cerr = opconfig.SetHasLabel(json.RawMessage(`{"label":""}`))
		refused(t, cerr, true)
		mask, cerr := opconfig.SetQueryMask(types.FILTER_SET_EQUALS, "tags", f, []string{"a", "b"})
		refused(t, cerr, false)
		if mask.PopCount() != 2 {
			t.Fatalf("mask = %v", mask)
		}
		_, cerr = opconfig.SetQueryMask(types.FILTER_SET_EQUALS, "tags", f, []string{"z"})
		refused(t, cerr, true)
	})
	t.Run("filters", func(t *testing.T) {
		keep, cerr := opconfig.NullMode([]string{"is_null"})
		refused(t, cerr, false)
		if !keep {
			t.Fatal("is_null keeps nulls")
		}
		_, cerr = opconfig.NullMode(nil)
		refused(t, cerr, true)
		truthy, cerr := opconfig.BoolMode(types.FILTER_TRUE, []string{"truthy"})
		refused(t, cerr, false)
		if !truthy {
			t.Fatal("truthy mode")
		}
		_, cerr = opconfig.BoolMode(types.FILTER_FALSE, []string{"x"})
		refused(t, cerr, true)
		lo, hi, cerr := opconfig.RangeBounds([]string{"1", "2.5"})
		refused(t, cerr, false)
		if lo != 1 || hi != 2.5 {
			t.Fatalf("bounds = %v %v", lo, hi)
		}
		_, _, cerr = opconfig.RangeBounds([]string{"1"})
		refused(t, cerr, true)
	})
}

// TestQuantileBuckets pins GROUP_QUANTILE's bucket-count rule: 0 is the
// unset slot (default 4), a whole count from 1 to MaxQuantileBuckets
// stands, anything else is PROCESSING_CONFIG.
func TestQuantileBuckets(t *testing.T) {
	t.Parallel()
	ok := map[float64]int{0: opconfig.DefaultQuantileBuckets, 1: 1, 4: 4, 10: 10, opconfig.MaxQuantileBuckets: opconfig.MaxQuantileBuckets}
	for in, want := range ok {
		got, cerr := opconfig.QuantileBuckets(types.GROUP_QUANTILE, in)
		if cerr != nil || got != want {
			t.Errorf("QuantileBuckets(%v) = %d, %v; want %d", in, got, cerr, want)
		}
	}
	for _, in := range []float64{-1, -0.5, 0.5, 2.5, opconfig.MaxQuantileBuckets + 1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, cerr := opconfig.QuantileBuckets(types.GROUP_QUANTILE, in)
		if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["param"] != "interval" {
			t.Errorf("QuantileBuckets(%v) = %v, want a PROCESSING_CONFIG interval refusal", in, cerr)
		}
	}
}
