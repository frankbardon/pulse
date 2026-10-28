package processing

// TestManifestNamesColumnMatchesProjection holds the manifest's
// Param.NamesColumn flag against the one runtime path that knows which
// params name a record column: the projection extractor (NeededFields),
// which must decode every column an operator reads or the run sees an
// all-null column.
//
// For every built-in operator with a params object (the six component
// categories, row tests, regressions and matrices), each params key
// declared "string", "list" or "field" is probed alone: a minimal slot
// of that operator carries the key set to a schema field nothing else
// names ("probe", as a string and as a one-element list), and the
// projected field set says whether the operator decodes it. A key the
// projection decodes MUST carry NamesColumn — the case this gate exists
// for, a newly registered operator reading a column through a param the
// manifest still calls a plain string. A key that carries NamesColumn
// but the probe cannot see must be listed in namesColumnUnprobed with
// the reason, so the flag never drifts to a param that reads nothing.
//
// Limits: a probe whose slot widens the projection (a vector input that
// does not resolve, an expression the extractor cannot read) proves
// nothing, and an operator that reads a column only when other params
// select a mode is seen only in the mode the bare probe exercises. Slot
// entries (Param.Slot) are slot fields, not params keys, and are held
// by descriptor's TestCapabilities_FieldParamsNameColumns instead. An
// expression-bearing params key (internal/exprslots) is skipped: what it
// reads is its expression's free names, not one column it names.

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/types"
)

// namesColumnUnprobed lists the params keys that carry NamesColumn
// although the bare probe cannot see the projection decode them, each
// with the reason. Keep it short: an entry here is a claim no runtime
// path checks.
var namesColumnUnprobed = map[string]string{
	"REG_MMM_BAYES.channels": "names a subset of the predictors, decoded through Predictors, never on its own",
	"REG_MMM_HILL.channels":  "names a subset of the predictors, decoded through Predictors, never on its own",
}

// namesColumnProbeOp is one operator under probe: its manifest entry
// params and a builder for a one-slot request carrying the given
// params object.
type namesColumnProbeOp struct {
	slot   string // the request key the operator's slots sit under
	name   string
	params []descriptor.Param
	build  func(params json.RawMessage) *types.Request
}

func namesColumnProbeOps() []namesColumnProbeOp {
	m := descx.BuildManifest()
	var ops []namesColumnProbeOp
	add := func(slot, name string, params []descriptor.Param, build func(json.RawMessage) *types.Request) {
		ops = append(ops, namesColumnProbeOp{slot: slot, name: name, params: params, build: build})
	}
	members := []string{"m1", "m2", "m3"}
	for _, o := range m.Components.Aggregators {
		typ := types.AggregationType(o.Name)
		add("aggregations", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{Aggregations: []*types.Aggregation{{Type: typ, Field: "base", Params: p}}}
		})
	}
	for _, o := range m.Components.Attributes {
		typ := types.AttributeType(o.Name)
		add("attributes", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Attributes:   []*types.Attribute{{Type: typ, Fields: members, Label: "out", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "base"}},
			}
		})
	}
	for _, o := range m.Components.Filterers {
		typ := types.FiltererType(o.Name)
		add("filterers", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Filterers:    []*types.Filterer{{Type: typ, Field: "base", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "base"}},
			}
		})
	}
	for _, o := range m.Components.Groupers {
		typ := types.GroupType(o.Name)
		add("groups", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: typ, Field: "base", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "base"}},
			}
		})
	}
	for _, o := range m.Components.Windows {
		typ := types.WindowType(o.Name)
		add("windows", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "m1"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "base"}},
				Windows:      []*types.Window{{Type: typ, Field: "base", Label: "out", Params: p}},
			}
		})
	}
	for _, o := range m.Components.Features {
		typ := types.FeatureType(o.Name)
		add("features", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Features:     []*types.Feature{{Type: typ, Field: "base", Label: "out", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "base"}},
			}
		})
	}
	for _, o := range m.Tests {
		typ := types.TestType(o.Name)
		add("tests", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Tests:        []*types.Test{{Type: typ, Field: "base", SplitBy: "m1", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "base"}},
			}
		})
	}
	for _, o := range m.Regressions {
		typ := types.RegressionType(o.Name)
		add("regressions", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Regressions:  []*types.RegressionSpec{{Type: typ, Target: "base", Predictors: members, Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "base"}},
			}
		})
	}
	for _, o := range m.Matrices {
		typ := types.MatrixType(o.Name)
		add("matrices", o.Name, o.Params, func(p json.RawMessage) *types.Request {
			return &types.Request{
				Matrices:     []types.MatrixSpec{{Name: "mx", Type: typ, Fields: members, Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "base"}},
			}
		})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].name < ops[j].name })
	return ops
}

// namesColumnProbe reports whether the projection decodes "probe" for
// op with params {key: value}, and whether any probe value gave a
// narrow (conclusive) projection at all.
func namesColumnProbe(t *testing.T, op namesColumnProbeOp, key string) (decoded, conclusive bool) {
	t.Helper()
	schema := mkSchema("base", "m1", "m2", "m3", "probe")
	for _, v := range []any{"probe", []string{"probe"}} {
		raw, err := json.Marshal(map[string]any{key: v})
		if err != nil {
			t.Fatal(err)
		}
		got := NeededFields(op.build(raw), schema, nil)
		if got.IsWide() {
			continue
		}
		conclusive = true
		if got.Has("probe") {
			return true, true
		}
	}
	return false, conclusive
}

func TestManifestNamesColumnMatchesProjection(t *testing.T) {
	t.Parallel()

	ops := namesColumnProbeOps()
	if len(ops) == 0 {
		t.Fatal("no operator to probe; the manifest walk is broken")
	}
	probed, marked := 0, 0
	seenUnprobed := map[string]bool{}
	for _, op := range ops {
		for _, p := range op.params {
			if p.Slot {
				continue
			}
			if p.NamesColumn {
				marked++
			}
			if p.Type != "string" && p.Type != "list" && p.Type != "field" {
				if p.NamesColumn {
					t.Errorf("%s params.%s carries NamesColumn on type %q; only string, list and field values name columns", op.name, p.Name, p.Type)
				}
				continue
			}
			// An expression-bearing params key (FEAT_TARGET_ENCODE
			// fit_on) reads the columns its expression names: it is an
			// expression slot (internal/exprslots), not a column name.
			if _, expr := exprslots.Match([]string{op.slot, exprslots.ParamsKey, p.Name}); expr {
				if p.NamesColumn {
					t.Errorf("%s params.%s is an expression slot and must not carry NamesColumn", op.name, p.Name)
				}
				continue
			}
			probed++
			id := op.name + "." + p.Name
			decoded, conclusive := namesColumnProbe(t, op, p.Name)
			_, listed := namesColumnUnprobed[id]
			if listed {
				seenUnprobed[id] = true
			}
			switch {
			case decoded && !p.NamesColumn:
				t.Errorf("%s: the projection decodes the column params.%s names, but its manifest Param lacks NamesColumn — mark it in internal/descriptor/capabilities_*.go", op.name, p.Name)
			case decoded && listed:
				t.Errorf("%s is in namesColumnUnprobed, but the probe now sees the projection decode it — drop the entry", id)
			case !decoded && p.NamesColumn && !listed:
				t.Errorf("%s params.%s carries NamesColumn, but the projection never decodes the column it names (conclusive probe: %v) — drop the flag, or list it in namesColumnUnprobed with the reason", op.name, p.Name, conclusive)
			case !decoded && !p.NamesColumn && listed:
				t.Errorf("%s is in namesColumnUnprobed but its Param does not carry NamesColumn — drop the entry", id)
			}
		}
	}
	for id := range namesColumnUnprobed {
		if !seenUnprobed[id] {
			t.Errorf("namesColumnUnprobed lists %s, which is no probed params key of a registered operator", id)
		}
	}
	if probed == 0 || marked == 0 {
		t.Fatalf("probed %d params keys, %d marked; the walk is broken", probed, marked)
	}
}
