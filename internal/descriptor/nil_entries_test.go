package descriptor

import (
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestNilEntryGate pins the null-entry rule SlotRefusal runs first: the
// first null entry of a Request's operator lists in JSON order, a facet
// root's filterers, a Compose root's null Requests entry, then each
// slot's and each fit request's lists (details.request / details.fit);
// a clean root, a nil root and another root type pass.
func TestNilEntryGate(t *testing.T) {
	t.Parallel()

	g := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	cases := []struct {
		name    string
		v       any
		msg     string
		details string
	}{
		{"clean", &types.Request{Groups: []*types.Group{g}}, "", ""},
		{"nil request", (*types.Request)(nil), "", ""},
		{"other root", &types.ChainRequest{Stages: []*types.ChainStage{nil}}, "", ""},
		{"groups", &types.Request{Groups: []*types.Group{g, nil}},
			"groups contains a null grouper at index 1", `{"index":1,"slot":"groups"}`},
		{"first list in JSON order", &types.Request{Groups: []*types.Group{nil}, Filterers: []*types.Filterer{nil}},
			"filterers contains a null filterer at index 0", `{"index":0,"slot":"filterers"}`},
		{"post_tests", &types.Request{PostTests: []*types.Test{nil}},
			"post_tests contains a null test at index 0", `{"index":0,"slot":"post_tests"}`},
		{"facet", &types.FacetRequest{Filterers: []*types.Filterer{nil}},
			"filterers contains a null filterer at index 0", `{"index":0,"slot":"filterers"}`},
		{"compose requests", &types.ComposedRequest{Requests: []*types.Request{{}, nil}},
			"requests contains a null request at index 1", `{"index":1,"slot":"requests"}`},
		{"compose slot", &types.ComposedRequest{Requests: []*types.Request{{}, {Windows: []*types.Window{nil}}}},
			"windows contains a null window at index 0", `{"index":0,"request":1,"slot":"windows"}`},
		{"compose fit", &types.ComposedRequest{Fits: []types.FitSpec{{Request: &types.Request{Joins: []*types.JoinSpec{nil}}}}},
			"joins contains a null join at index 0", `{"fit":0,"index":0,"slot":"joins"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := nilEntryGate(c.v)
			if c.msg == "" {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("got %v, want PROCESSING_CONFIG", err)
			}
			if ce.Message != c.msg {
				t.Errorf("message = %q, want %q", ce.Message, c.msg)
			}
			if d, _ := json.Marshal(ce.Details); string(d) != c.details {
				t.Errorf("details = %s, want %s", d, c.details)
			}
		})
	}
}

// TestSlotRefusalRunsNilEntryGateFirst: SlotRefusal reports the null
// entry ahead of the params allowlist (the unknown `bogus` key would
// otherwise be refused PULSE_REQUEST_UNKNOWN_FIELD).
func TestSlotRefusalRunsNilEntryGateFirst(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Params: json.RawMessage(`{"bogus":1}`)}},
		Groups:       []*types.Group{nil},
	}
	err := SlotRefusal(req, nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["slot"] != "groups" {
		t.Fatalf("SlotRefusal = %v, want the groups null-entry refusal", err)
	}
}
