package pulse_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// r6GrouperCalls counts which partition path the engine drives.
type r6GrouperCalls struct{ group, keyForRow, keysForRow atomic.Int64 }

// r6Grouper buckets score into "lo" (< 30) / "hi". It implements both
// Group (buffered) and KeyForRow (streaming).
type r6Grouper struct{ calls *r6GrouperCalls }

func r6Key(v float64) string {
	if v < 30 {
		return "lo"
	}
	return "hi"
}

func (g r6Grouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	g.calls.group.Add(1)
	out := map[string][]int{}
	for i := 0; i < rows.Len(); i++ {
		if v, ok := rows.At(i).NumericValue(field); ok {
			out[r6Key(v)] = append(out[r6Key(v)], i)
		}
	}
	return out, nil
}

func (g r6Grouper) KeyForRow(r extend.Record, field string) (string, bool, error) {
	g.calls.keyForRow.Add(1)
	v, ok := r.NumericValue(field)
	if !ok {
		return "", false, nil
	}
	return r6Key(v), true, nil
}

// r6FanGrouper fans every row into both "a" and "b".
type r6FanGrouper struct{ calls *r6GrouperCalls }

func (g r6FanGrouper) Group(rows extend.Rows, _ string) (map[string][]int, error) {
	g.calls.group.Add(1)
	all := make([]int, rows.Len())
	for i := range all {
		all[i] = i
	}
	return map[string][]int{"a": all, "b": all}, nil
}

func (g r6FanGrouper) KeysForRow(extend.Record, string) ([]string, bool, error) {
	g.calls.keysForRow.Add(1)
	return []string{"a", "b"}, true, nil
}

func r6GrouperSchema() descriptor.ComponentSchema {
	return descriptor.ComponentSchema{
		Keys: []descriptor.ComponentKey{
			{Name: "marker", Type: "int", Description: "Constant marker proving the emitter ran."},
		},
		Mergeability: descriptor.Mergeable,
	}
}

func r6GrouperEmit(extend.Grouper) (map[string]any, error) {
	return map[string]any{"marker": 7}, nil
}

// TestExtensions_FanOutGrouperWithComponentsStaysMultiKey is the
// grouper half of the R6 regression, end to end: a FansOut grouper that
// ALSO supplies a ComponentsFunc must still be recognised as
// MultiKeyStreamingGrouper by the fused crosstab gate and driven
// through KeysForRow. The historical meta wrapper embedded only the
// Grouper interface, hiding MultiKeyStreamingGrouper, so the request
// silently fell back to the buffered Group path.
func TestExtensions_FanOutGrouperWithComponentsStaysMultiKey(t *testing.T) {
	calls := &r6GrouperCalls{}
	reg := pulse.GrouperRegistration{
		Name: "GROUP_ACME_R6_FAN",
		Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) {
			return r6FanGrouper{calls: calls}, nil
		},
		Streamable:      true,
		FansOut:         true,
		Accepts:         []encoding.FieldType{encoding.FieldTypeF64},
		FieldInputs:     func(json.RawMessage) []string { return nil },
		ComponentSchema: r6GrouperSchema(),
		ComponentsFunc:  r6GrouperEmit,
	}
	p, path := brandScoreCohort(t, pulse.Extensions{Groupers: []pulse.GrouperRegistration{reg}})
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: reg.Name, Field: "score"}},
			Columns: []*types.Group{{Type: types.GROUP_ROUNDED, Field: "score"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := calls.keysForRow.Load(); got < 5 {
		t.Errorf("KeysForRow calls = %d, want >= 5 (fused multi-key path); Group calls = %d", got, calls.group.Load())
	}
	if got := calls.group.Load(); got != 0 {
		t.Errorf("Group calls = %d, want 0 (buffered fallback taken)", got)
	}
	if resp.Crosstab == nil {
		t.Fatal("no crosstab payload")
	}
}

// TestExtensions_GrouperComponentsEmitOnGroupedPaths pins that the
// adapter keeps ComponentsFunc emission on both grouped Process paths:
// a Streamable=true registration streams (KeyForRow), a
// Streamable=false one runs buffered (Group), and the emitter's keys
// plus the universal floor reach Components.Groupers either way.
func TestExtensions_GrouperComponentsEmitOnGroupedPaths(t *testing.T) {
	for _, streamable := range []bool{true, false} {
		calls := &r6GrouperCalls{}
		reg := pulse.GrouperRegistration{
			Name: "GROUP_ACME_R6_STREAM",
			Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) {
				return r6Grouper{calls: calls}, nil
			},
			Streamable:      streamable,
			Accepts:         []encoding.FieldType{encoding.FieldTypeF64},
			ComponentSchema: r6GrouperSchema(),
			ComponentsFunc:  r6GrouperEmit,
		}
		p, path := brandScoreCohort(t, pulse.Extensions{Groupers: []pulse.GrouperRegistration{reg}})
		resp, err := p.Process(context.Background(), &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: reg.Name, Field: "score"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		})
		if err != nil {
			t.Fatalf("streamable=%v: Process: %v", streamable, err)
		}
		if resp.Components == nil || len(resp.Components.Groupers) != 1 ||
			resp.Components.Groupers[0].Operator["marker"] != 7 ||
			resp.Components.Groupers[0].TotalN != 5 {
			t.Errorf("streamable=%v: ComponentsFunc emission or floor lost: %+v", streamable, resp.Components)
		}
		if len(resp.Data) != 2 {
			t.Errorf("streamable=%v: rows = %d, want 2 (lo, hi): %+v", streamable, len(resp.Data), resp.Data)
		}
		if streamable && (calls.keyForRow.Load() == 0 || calls.group.Load() != 0) {
			t.Errorf("streamable: KeyForRow=%d Group=%d, want KeyForRow only", calls.keyForRow.Load(), calls.group.Load())
		}
		if !streamable && (calls.group.Load() == 0 || calls.keyForRow.Load() != 0) {
			t.Errorf("buffered: KeyForRow=%d Group=%d, want Group only", calls.keyForRow.Load(), calls.group.Load())
		}
	}
}
