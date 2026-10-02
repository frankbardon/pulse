package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// outputCollisionRequests are requests whose output rows would hold two
// values under one name. The row map keeps one, so before the shared
// field rule judged the output namespace an aggregation labelled like a
// group field vanished (the group key is written after the
// aggregations) and a duplicate label kept only the last figure — a
// confident answer with a column missing.
func outputCollisionRequests(cohort string) map[string]struct {
	req     func() *types.Request
	message string
	details map[string]any
} {
	co := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	cat := func() []*types.Group { return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}} }
	return map[string]struct {
		req     func() *types.Request
		message string
		details map[string]any
	}{
		"aggregation label equals group field": {func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}, {Type: types.AGG_SUM, Field: "n", Label: "cat"}}}
		}, "aggregation[1] label cat collides with group[0] field cat",
			map[string]any{"label": "cat", "aggregation_index": 1, "aggregation": "AGG_SUM", "group_index": 0, "group": "GROUP_CATEGORY"}},
		"duplicate explicit labels": {func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}, {Type: types.AGG_MAX, Field: "n", Label: "s"}}}
		}, "aggregation[1] label s collides with aggregation[0] label s",
			map[string]any{"label": "s", "aggregation_index": 1, "aggregation": "AGG_MAX", "other_aggregation_index": 0, "other_aggregation": "AGG_SUM"}},
		"defaulted Type collides with explicit default label (ungrouped)": {func() *types.Request {
			return &types.Request{Cohort: co(),
				Aggregations: []*types.Aggregation{{Field: "n"}, {Type: types.AGG_SUM, Field: "n"}}}
		}, "aggregation[1] label AGG_SUM_n collides with aggregation[0] label AGG_SUM_n",
			map[string]any{"label": "AGG_SUM_n", "aggregation_index": 1, "aggregation": "AGG_SUM", "other_aggregation_index": 0, "other_aggregation": "AGG_SUM"}},
	}
}

// TestOutputNamespace_CollisionRefusedOnEverySide: an aggregation label
// equal to a group field or to another aggregation's label is refused
// by the shared field rule with one code, message and details on
// Process, ProcessStream and predict — and located inside Compose
// (serial and FailFast parallel) and a chain, where the validators
// agree.
func TestOutputNamespace_CollisionRefusedOnEverySide(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}

	for name, tc := range outputCollisionRequests(cohort) {
		t.Run(name, func(t *testing.T) {
			_, perr := p.Process(ctx, tc.req())
			ce := requireCode(t, perr, errors.SERVICE_VALIDATION)
			if ce.Message != tc.message {
				t.Fatalf("runtime message = %q, want %q", ce.Message, tc.message)
			}
			want, _ := json.Marshal(tc.details)
			have, _ := json.Marshal(ce.Details)
			if string(want) != string(have) {
				t.Fatalf("runtime details = %s, want %s", have, want)
			}
			_, serr := p.ProcessStream(ctx, tc.req())
			if se := requireCode(t, serr, errors.SERVICE_VALIDATION); se.Message != ce.Message {
				t.Fatalf("stream = %q, process = %q", se.Message, ce.Message)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, tc.req()), perr)

			compose := func() *types.ComposedRequest {
				return &types.ComposedRequest{Requests: []*types.Request{
					{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()}, tc.req()}}
			}
			_, cerr := p.Compose(ctx, compose())
			if c := requireCode(t, cerr, errors.SERVICE_VALIDATION); c.Details["request"] != 1 {
				t.Fatalf("compose details = %v, want request=1", c.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), cerr)
			_, pcerr := p.ComposeParallel(ctx, compose(), pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			if c := requireCode(t, pcerr, errors.SERVICE_VALIDATION); c.Details["request"] != 1 {
				t.Fatalf("parallel compose details = %v, want request=1", c.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), pcerr)
		})
	}

	// Chains: a collision on stage 0 and on the FINAL stage (whose
	// output no later stage synthesises, so nothing else refused it).
	stage := func(field, label string) *types.Request {
		return &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: field, Label: label}}}
	}
	chains := map[string]struct {
		stages []*types.ChainStage
		stage  int
	}{
		"stage 0":     {[]*types.ChainStage{{Name: "a", Request: stage("n", "cat")}, {Name: "b", Request: stage("s", "t")}}, 0},
		"final stage": {[]*types.ChainStage{{Name: "a", Request: stage("n", "s")}, {Name: "b", Request: stage("s", "cat")}}, 1},
	}
	for name, c := range chains {
		t.Run("chain "+name, func(t *testing.T) {
			mk := func() *types.ChainRequest {
				stages := make([]*types.ChainStage, len(c.stages))
				for i, s := range c.stages {
					b, _ := json.Marshal(s)
					stages[i] = &types.ChainStage{}
					_ = json.Unmarshal(b, stages[i])
				}
				return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: stages}
			}
			_, rerr := p.ProcessChain(ctx, mk())
			ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
			if ce.Details["stage"] != c.stage || ce.Message != "aggregation[0] label cat collides with group[0] field cat" {
				t.Fatalf("chain refusal = %q %v, want the collision at stage %d", ce.Message, ce.Details, c.stage)
			}
			sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
		})
	}
}

// TestOutputNamespace_DistinctNamesAccepted: names that only LOOK alike
// are not collisions — an aggregation labelled like the field it reads
// (not an output column), distinct labels over one field, a label equal
// to an attribute label (attributes are not output columns), and a
// repeated group field (one output column). Both sides accept, and the
// figures all reach the row.
func TestOutputNamespace_DistinctNamesAccepted(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "n * 2"}},
			Groups:     []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}, {Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "n", Label: "n"},
				{Type: types.AGG_MAX, Field: "n"},
				{Type: types.AGG_SUM, Field: "x", Label: "x"},
			}}
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	for _, col := range []string{"cat", "n", "AGG_MAX_n", "x"} {
		if _, ok := resp.Data[0][col]; !ok {
			t.Fatalf("row %v lacks %s", resp.Data[0], col)
		}
	}
	if env := predictEnvelope(t, p, fs, cohort, req()); len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors[0])
	}
}
