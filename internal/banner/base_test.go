package banner_test

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/banner"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

func baseCompose() *types.ComposedRequest {
	off := false
	return &types.ComposedRequest{
		Base: &types.Request{
			Cohort:            &types.Cohort{Filename: "base.pulse"},
			Filterers:         []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "wave", Values: []string{"w2"}}},
			Windows:           []*types.Window{{Type: types.WIN_LAG, Field: "spend", Label: "spend_lag1"}},
			Features:          []*types.Feature{{Type: types.FEAT_LOG, Field: "spend", Label: "log_spend"}},
			Aggregations:      []*types.Aggregation{{Type: types.AGG_SUM, Field: "spend", Label: "spend_total"}},
			TimeZone:          "UTC",
			DisableComponents: &off,
			Params:            types.Params{"a": 1, "b": 2},
		},
		Requests: []*types.Request{
			{
				Label:        "one",
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "spend", Label: "spend_mean"}},
				Params:       types.Params{"b": 20, "c": 30},
			},
			{
				Label:    "two",
				Cohort:   &types.Cohort{Filename: "own.pulse"},
				TimeZone: "Europe/Paris",
			},
		},
	}
}

func labelsOf[T any](items []*T, label func(*T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = label(it)
	}
	return out
}

// TestMergeBase_Matrix pins the merge rules per slot kind: a scalar or
// pointer the slot leaves unset takes the base's, a set one keeps the
// slot's; params merge key-wise, the slot's key winning; every list
// slot carries the base items first, then the slot's own; Base is gone
// from the result and the input is untouched.
func TestMergeBase_Matrix(t *testing.T) {
	t.Parallel()

	c := baseCompose()
	before, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	got, ce := banner.MergeBase(c)
	if ce != nil {
		t.Fatalf("MergeBase: %v", ce)
	}
	after, _ := json.Marshal(c)
	if string(before) != string(after) {
		t.Fatalf("MergeBase mutated its input:\n%s\n%s", before, after)
	}
	if got == c || got.Base != nil {
		t.Fatalf("want a fresh request without base, got same=%v base=%v", got == c, got.Base)
	}
	one, two := got.Requests[0], got.Requests[1]

	// Scalars and pointers: unset takes the base's, set keeps the slot's.
	if one.Label != "one" || two.Label != "two" {
		t.Errorf("labels = %q, %q", one.Label, two.Label)
	}
	if one.Cohort == nil || one.Cohort.Filename != "base.pulse" {
		t.Errorf("slot one cohort = %+v, want the base's", one.Cohort)
	}
	if two.Cohort.Filename != "own.pulse" {
		t.Errorf("slot two cohort = %+v, want its own", two.Cohort)
	}
	if one.TimeZone != "UTC" || two.TimeZone != "Europe/Paris" {
		t.Errorf("time zones = %q, %q", one.TimeZone, two.TimeZone)
	}
	if one.DisableComponents == nil || *one.DisableComponents {
		t.Errorf("disable_components = %v, want the base's false", one.DisableComponents)
	}

	// Params: key-wise, the slot's key wins.
	if want := (types.Params{"a": 1, "b": 20, "c": 30}); !reflect.DeepEqual(map[string]any(one.Params), map[string]any(want)) {
		t.Errorf("slot one params = %v, want %v", one.Params, want)
	}
	if want := (types.Params{"a": 1, "b": 2}); !reflect.DeepEqual(map[string]any(two.Params), map[string]any(want)) {
		t.Errorf("slot two params = %v, want %v", two.Params, want)
	}

	// Lists: base items first, then the slot's.
	aggLabel := func(a *types.Aggregation) string { return a.Label }
	if got, want := labelsOf(one.Aggregations, aggLabel), []string{"spend_total", "spend_mean"}; !reflect.DeepEqual(got, want) {
		t.Errorf("slot one aggregations = %v, want %v", got, want)
	}
	if got, want := labelsOf(two.Aggregations, aggLabel), []string{"spend_total"}; !reflect.DeepEqual(got, want) {
		t.Errorf("slot two aggregations = %v, want %v", got, want)
	}
	for _, slot := range got.Requests {
		if len(slot.Windows) != 1 || slot.Windows[0].Label != "spend_lag1" ||
			len(slot.Features) != 1 || slot.Features[0].Label != "log_spend" ||
			len(slot.Filterers) != 1 || slot.Filterers[0].Field != "wave" {
			t.Errorf("slot %s lacks a base list item: %+v", slot.Label, slot)
		}
	}

	// Copy-on-write: no base item is shared between slots or with the
	// input, so a slot's in-place rewrite (smart defaults, item fan-out)
	// cannot leak into a sibling.
	if one.Windows[0] == two.Windows[0] || one.Windows[0] == c.Base.Windows[0] {
		t.Error("a base window item is shared across slots or with the input")
	}
	if one.Aggregations[1] != c.Requests[0].Aggregations[0] {
		t.Error("a slot's own item should pass through as written")
	}
}

// TestMergeBase_AbsentIsIdentity: without a base the request comes back
// as the same pointer, so every base-free batch is untouched.
func TestMergeBase_AbsentIsIdentity(t *testing.T) {
	t.Parallel()

	c := baseCompose()
	c.Base = nil
	got, ce := banner.MergeBase(c)
	if ce != nil || got != c {
		t.Fatalf("MergeBase without base = %p, %v; want the input pointer", got, ce)
	}
	if got, ce := banner.MergeBase(nil); got != nil || ce != nil {
		t.Fatalf("MergeBase(nil) = %v, %v", got, ce)
	}
}

// TestMergeBase_Collision: a base item whose label or name equals one of
// the slot's own in the same list is refused, located at the slot.
func TestMergeBase_Collision(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(c *types.ComposedRequest)
		list string
		id   string
	}{
		{"aggregation label", func(c *types.ComposedRequest) {
			c.Requests[1].Aggregations = []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "spend", Label: "spend_total"}}
		}, "aggregations", "spend_total"},
		{"window label", func(c *types.ComposedRequest) {
			c.Requests[1].Windows = []*types.Window{{Type: types.WIN_LAG, Field: "spend", Label: "spend_lag1"}}
		}, "windows", "spend_lag1"},
		{"matrix name", func(c *types.ComposedRequest) {
			c.Base.Matrices = []types.MatrixSpec{{Name: "corr", Type: types.MAT_CORRELATION, Vector: "v"}}
			c.Requests[1].Matrices = []types.MatrixSpec{{Name: "corr", Type: types.MAT_COVARIANCE, Vector: "v"}}
		}, "matrices", "corr"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := baseCompose()
			tc.edit(c)
			_, ce := banner.MergeBase(c)
			if ce == nil || ce.Code != errors.PULSE_COMPOSE_BASE_COLLISION {
				t.Fatalf("want PULSE_COMPOSE_BASE_COLLISION, got %v", ce)
			}
			d := ce.Details
			if d["reason"] != "label_collision" || d["request"] != 1 || d["slot"] != "two" || d["list"] != tc.list || d["label"] != tc.id {
				t.Errorf("details = %v", d)
			}
		})
	}

	// An item with no label (its default is derived later) is never a
	// collision here: a duplicate default surfaces as that slot kind's
	// own duplicate refusal.
	c := baseCompose()
	c.Requests[1].Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "spend"}}
	c.Base.Aggregations[0].Label = ""
	if _, ce := banner.MergeBase(c); ce != nil {
		t.Errorf("unlabelled items refused: %v", ce)
	}
}

// TestMergeBase_LabelRefused: a base carries no label — merged into two
// unlabelled slots it would name both alike.
func TestMergeBase_LabelRefused(t *testing.T) {
	t.Parallel()

	c := baseCompose()
	c.Base.Label = "shared"
	_, ce := banner.MergeBase(c)
	if ce == nil || ce.Code != errors.PULSE_COMPOSE_BASE_COLLISION || ce.Details["reason"] != "base_label" {
		t.Fatalf("want PULSE_COMPOSE_BASE_COLLISION base_label, got %v", ce)
	}
}

// TestExpandCompose_MergesBaseBeforeSweepAndBanner: the merge runs in the
// shared seam, on the explicit slots only — the sweep body and the
// banner slots are untouched, and the result carries no base.
func TestExpandCompose_MergesBaseBeforeSweepAndBanner(t *testing.T) {
	t.Parallel()

	c := &types.ComposedRequest{
		Base: &types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}}},
		Requests: []*types.Request{
			{Label: "explicit", Cohort: &types.Cohort{Filename: "c.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "brand"}}},
		},
		Sweep: &types.SweepSpec{
			Axes:    []types.SweepAxis{{Name: "w", Values: []any{"w1", "w2"}}},
			Request: json.RawMessage(`{"cohort":{"filename":"c.pulse"},"filterers":[{"type":"FILTER_INCLUDE","field":"wave","values":["{{w}}"]}],"aggregations":[{"type":"AGG_COUNT","field":"brand"}]}`),
		},
		Banner: spec(),
	}
	exp, err := banner.ExpandCompose(c, sweep.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Composed.Base != nil {
		t.Error("the effective request still carries base")
	}
	reqs := exp.Composed.Requests
	if exp.Explicit != 1 || len(reqs) < 4 {
		t.Fatalf("explicit=%d slots=%d", exp.Explicit, len(reqs))
	}
	if fs := reqs[0].Filterers; len(fs) != 1 || fs[0].Field != "region" {
		t.Errorf("explicit slot filterers = %+v, want the base's", fs)
	}
	for _, r := range reqs[1:] {
		for _, f := range r.Filterers {
			if f.Field == "region" {
				t.Errorf("slot %q (sweep or banner) received the base", r.Label)
			}
		}
	}

	c.Base.Label = "x"
	_, err = banner.ExpandCompose(c, sweep.Options{}, nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_COMPOSE_BASE_COLLISION {
		t.Fatalf("ExpandCompose with a labelled base = %v", err)
	}
}
