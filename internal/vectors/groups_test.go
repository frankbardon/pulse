package vectors

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func groupSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "q_1", Type: encoding.FieldTypeF64},
		{Name: "q_2", Type: encoding.FieldTypeF64},
		{Name: "label", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
}

// TestResolveGroups_Rules pins the vector-grouper contract: exactly one
// of vector / fields, no field, a known vector, GROUP_KMEANS params, and
// every other grouper refusing a vector input — on groups and both
// crosstab axes.
func TestResolveGroups_Rules(t *testing.T) {
	km := func(mut func(*types.Group)) *types.Group {
		g := &types.Group{Type: types.GROUP_KMEANS, Fields: []string{"q_*"}, Params: json.RawMessage(`{"k":2}`)}
		if mut != nil {
			mut(g)
		}
		return g
	}
	cases := []struct {
		name   string
		req    *types.Request
		code   errors.Code
		reason string
	}{
		{"ok fields", &types.Request{Groups: []*types.Group{km(nil)}}, "", ""},
		{"ok vector", &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_2"}}},
			Groups: []*types.Group{km(func(g *types.Group) { g.Fields, g.Vector = nil, "v" })}}, "", ""},
		{"field", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Field = "q_1" })}}, errors.SERVICE_VALIDATION, "field_on_vector_group"},
		{"both", &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"q_1"}}},
			Groups: []*types.Group{km(func(g *types.Group) { g.Vector = "v" })}}, errors.SERVICE_VALIDATION, "vector_and_fields"},
		{"neither", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Fields = nil })}}, errors.SERVICE_VALIDATION, "no_vector_or_fields"},
		{"unknown vector", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Fields, g.Vector = nil, "nope" })}}, errors.PULSE_VECTOR_UNKNOWN, ""},
		{"no k", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Params = nil })}}, errors.SERVICE_VALIDATION, "k_required"},
		{"k 1", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Params = json.RawMessage(`{"k":1}`) })}}, errors.SERVICE_VALIDATION, "bad_params"},
		{"n_init 0", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Params = json.RawMessage(`{"k":2,"n_init":0}`) })}}, errors.SERVICE_VALIDATION, "bad_params"},
		{"unknown key", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Params = json.RawMessage(`{"k":2,"metric":"cosine"}`) })}}, errors.SERVICE_VALIDATION, "bad_params"},
		{"pairwise", &types.Request{Groups: []*types.Group{km(func(g *types.Group) { g.Params = json.RawMessage(`{"k":2,"missing":"pairwise"}`) })}}, errors.SERVICE_VALIDATION, "pairwise_unsupported"},
		{"category with fields", &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "label", Fields: []string{"q_1"}}}}, errors.SERVICE_VALIDATION, "vector_not_accepted"},
		{"crosstab axis", &types.Request{Crosstab: &types.CrosstabSpec{Columns: []*types.Group{km(func(g *types.Group) { g.Fields = nil })}}}, errors.SERVICE_VALIDATION, "no_vector_or_fields"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveGroups(c.req, groupSchema(), nil, nil, nil)
			if c.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Code != c.code || (c.reason != "" && err.Details["reason"] != c.reason) {
				t.Fatalf("err = %v, want %s %s", err, c.code, c.reason)
			}
		})
	}
}

// TestKMeansParams_Defaults: seed 0 resolves to the fixed default, the
// documented defaults apply, and an explicit seed is kept.
func TestKMeansParams_Defaults(t *testing.T) {
	cfg, err := KMeansParams("groups[0]", json.RawMessage(`{"k":4,"seed":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.K != 4 || cfg.Seed != KMeansDefaultSeed || cfg.MaxIter != 100 || cfg.NInit != 10 || !cfg.Standardize {
		t.Fatalf("defaults %+v", cfg)
	}
	cfg, _ = KMeansParams("groups[0]", json.RawMessage(`{"k":2,"seed":-5,"standardize":false}`))
	if cfg.Seed != -5 || cfg.Standardize {
		t.Fatalf("explicit %+v", cfg)
	}
}

// TestGroupKeyField_ReferencedAndBuckets: the key column rule, the
// vector reference set and the bucket estimate (params.k).
func TestGroupKeyField_ReferencedAndBuckets(t *testing.T) {
	g := &types.Group{Type: types.GROUP_KMEANS, Vector: "v", Params: json.RawMessage(`{"k":5}`)}
	if got := GroupKeyField(types.GROUP_KMEANS, g); got != "GROUP_KMEANS_v" {
		t.Fatalf("key %q", got)
	}
	if got := GroupKeyField(types.GROUP_KMEANS, &types.Group{Type: types.GROUP_KMEANS, Fields: []string{"q_*", "z"}}); got != "GROUP_KMEANS_q_*" {
		t.Fatalf("inline key %q", got)
	}
	if got := GroupKeyField(types.GROUP_CATEGORY, &types.Group{Type: types.GROUP_CATEGORY, Field: "label"}); got != "label" {
		t.Fatalf("category key %q", got)
	}
	req := &types.Request{Vectors: []types.VectorSpec{{Name: "v"}}, Crosstab: &types.CrosstabSpec{Rows: []*types.Group{g}}}
	if !Referenced(req)["v"] {
		t.Fatal("a crosstab-axis vector grouper does not reference its vector")
	}
	if b, basis, ok := EstimateBuckets([]*types.Group{g}, nil); !ok || b != 5 || basis != BucketBasisClusters {
		t.Fatalf("EstimateBuckets = %d %s %v", b, basis, ok)
	}
	members, ok := GroupInputMembers(&types.Request{Groups: []*types.Group{{Type: types.GROUP_KMEANS, Fields: []string{"q_*"}}}}, groupSchema())
	if !ok || len(members) != 2 {
		t.Fatalf("members %v ok %v", members, ok)
	}
}
