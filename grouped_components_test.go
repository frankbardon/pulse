package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// gcExtSum is a NON-streamable extension aggregator with a components
// hook: it forces the buffered grouped arm, and proves the per-group
// operator map comes from each per-bucket instance's own hook.
const gcExtSum types.AggregationType = "AGG_ACME_GC_SUM"

func openGroupedComponents(t *testing.T, disable bool) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "gc.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: gcExtensions(), DisableComponents: disable})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	grouped := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, Aggregations: gcAggregations()}
	if processing.CanStreamRequestWithExtensions(grouped, paritySchema(t), pulse.ServiceForTest(p).Extensions()) {
		t.Fatal("fixture: the grouped request streams; it must take the buffered arm")
	}
	return p, &types.Cohort{Filename: "gc.pulse"}
}

func gcExtensions() pulse.Extensions {
	probe := &parityProbe{}
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name: gcExtSum,
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return &paritySum{probe: probe, dec: encoding.ZeroDecimal128()}, nil
			},
			Streamable:  false,
			FieldInputs: func(json.RawMessage) []string { return nil },
			ComponentSchema: descriptor.ComponentSchema{
				Keys:         []descriptor.ComponentKey{{Name: "ext_sum", Type: "float64", Description: "The running sum."}},
				Mergeability: descriptor.Mergeable,
			},
			ComponentsFunc: func(inst extend.Aggregator) (map[string]any, error) {
				return map[string]any{"ext_sum": inst.(*paritySum).sum}, nil
			},
		}},
	}
}

// gcAggregations: a built-in over a nullable field (a non-zero n_null),
// a floor-only built-in, a weighted slot and the extension aggregator.
func gcAggregations() []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "score", Label: "s"},
		{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		{Type: types.AGG_STDDEV, Field: "score", Label: "wm",
			Weight: types.SlotWeightOf(types.WeightSpec{Field: "qty", Kind: types.WeightKindProbability})},
		{Type: gcExtSum, Field: "score", Label: "x"},
	}
}

func gcProcess(t *testing.T, p *pulse.Pulse, req *types.Request) *types.Response {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// asSlotEntry re-spells a per-group entry as the ungrouped slot shape
// (label dropped) so the two marshal comparably byte-for-byte.
func asSlotEntry(g types.AggregationGroupComponents) types.AggregationComponents {
	return types.AggregationComponents{N: g.N, NNull: g.NNull, SumWeights: g.SumWeights,
		NEff: g.NEff, NWeightInvalid: g.NWeightInvalid, Operator: g.Operator}
}

// TestGroupedComponents_PerGroupMatchesFilteredUngrouped pins the
// per-group contract on the buffered grouped arm: every bucket's
// figures — floor, weighted floor keys and operator map, for a
// built-in, a floor-only, a weighted and an extension slot — are
// byte-identical to an ungrouped run filtered to that bucket's records;
// the slot-level floor is the cohort-wide total.
func TestGroupedComponents_PerGroupMatchesFilteredUngrouped(t *testing.T) {
	p, cohort := openGroupedComponents(t, false)
	grouped := gcProcess(t, p, &types.Request{
		Cohort:       cohort,
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: gcAggregations(),
	})
	if grouped.Components == nil || len(grouped.Components.Aggregations) != len(gcAggregations()) {
		t.Fatalf("grouped run emitted no per-slot aggregation components: %+v", grouped.Components)
	}
	ungrouped := gcProcess(t, p, &types.Request{Cohort: cohort, Aggregations: gcAggregations()})

	for i, slot := range grouped.Components.Aggregations {
		// Cohort-wide slot floor = the ungrouped run's floor, no operator.
		total := ungrouped.Components.Aggregations[i]
		total.Operator = nil
		if got, want := mustJSON(t, types.AggregationComponents{Label: slot.Label, N: slot.N, NNull: slot.NNull,
			SumWeights: slot.SumWeights, NEff: slot.NEff, NWeightInvalid: slot.NWeightInvalid, Operator: slot.Operator}),
			mustJSON(t, total); got != want {
			t.Errorf("slot %s: cohort-wide floor\n got  %s\n want %s", slot.Label, got, want)
		}
		if len(slot.Groups) != len(grouped.Data) {
			t.Fatalf("slot %s: %d groups for %d Data rows", slot.Label, len(slot.Groups), len(grouped.Data))
		}
		for _, g := range slot.Groups {
			key, _ := g.GroupKey[0].(string)
			filtered := gcProcess(t, p, &types.Request{
				Cohort:       cohort,
				Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{key}}},
				Aggregations: gcAggregations(),
			})
			want := filtered.Components.Aggregations[i]
			want.Label = ""
			if got, want := mustJSON(t, asSlotEntry(g)), mustJSON(t, want); got != want {
				t.Errorf("slot %s group %s:\n got  %s\n want %s", slot.Label, key, got, want)
			}
		}
	}
	// The fixture must exercise every figure the test claims to cover.
	byLabel := map[string]types.AggregationComponents{}
	for _, s := range grouped.Components.Aggregations {
		byLabel[s.Label] = s
	}
	if g := byLabel["s"].Groups[0]; g.NNull == 0 || g.Operator == nil {
		t.Errorf("fixture: built-in group entry lacks n_null / operator: %+v", g)
	}
	if g := byLabel["wm"].Groups[0]; g.SumWeights == nil || g.NEff == nil {
		t.Errorf("fixture: weighted group entry lacks sum_weights / n_eff: %+v", g)
	}
	if g := byLabel["x"].Groups[0]; g.Operator["ext_sum"] == nil {
		t.Errorf("fixture: extension group entry lacks its operator map: %+v", g)
	}
	if len(ungrouped.Components.Aggregations[0].Groups) != 0 {
		t.Error("ungrouped run carries groups[]")
	}
}

// TestGroupedComponents_FollowDataOrder pins groups[i] ↔ Data[i], with
// the default (alphabetical) order and under Request.Sort.
func TestGroupedComponents_FollowDataOrder(t *testing.T) {
	p, cohort := openGroupedComponents(t, false)
	for _, tc := range []struct {
		name string
		sort []types.OrderKey
	}{
		{"default", nil},
		{"sorted", []types.OrderKey{{Field: "s", Desc: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := gcProcess(t, p, &types.Request{
				Cohort:       cohort,
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: gcAggregations(),
				Sort:         tc.sort,
			})
			if resp.Components == nil || len(resp.Components.Aggregations) == 0 {
				t.Fatal("no aggregation components")
			}
			var order []string
			for _, row := range resp.Data {
				order = append(order, row["region"].(string))
			}
			if tc.sort != nil && order[0] == "east" {
				t.Fatalf("fixture: sort left the alphabetical order in place (%v); the test proves nothing", order)
			}
			for _, slot := range resp.Components.Aggregations {
				for i, g := range slot.Groups {
					if g.GroupKey[0] != order[i] {
						t.Errorf("slot %s: groups[%d] = %v, Data[%d] = %s", slot.Label, i, g.GroupKey, i, order[i])
					}
					if s := resp.Data[i]["s"]; slot.Label == "s" && g.Operator["sum"] != s {
						t.Errorf("groups[%d].operator.sum = %v, Data[%d].s = %v", i, g.Operator["sum"], i, s)
					}
				}
			}
		})
	}
}

// TestGroupedComponents_OptOut: both opt-out knobs leave the buffered
// grouped response with no Components at all.
func TestGroupedComponents_OptOut(t *testing.T) {
	p, cohort := openGroupedComponents(t, false)
	off := true
	resp := gcProcess(t, p, &types.Request{
		Cohort:            cohort,
		Groups:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations:      gcAggregations(),
		DisableComponents: &off,
	})
	if resp.Components != nil {
		t.Errorf("request opt-out: Components = %s, want nil", mustJSON(t, resp.Components))
	}
	pe, cohort := openGroupedComponents(t, true)
	resp = gcProcess(t, pe, &types.Request{
		Cohort:       cohort,
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: gcAggregations(),
	})
	if resp.Components != nil {
		t.Errorf("engine opt-out: Components = %s, want nil", mustJSON(t, resp.Components))
	}
}
