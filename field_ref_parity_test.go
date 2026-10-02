package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// unknownFieldRequests is one request per slot family naming the
// unknown field "zz" over zoneCohort (d, ts, cat, n), and the code the
// family refuses with. Before the shared field-reference rule
// (descriptor.FieldRefRefusals) the runtime read every one of these
// names as an all-null column and answered with wrong numbers.
func unknownFieldRequests(cohort string) map[string]struct {
	code errors.Code
	req  func() *types.Request
} {
	co := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	grouped := func(r *types.Request) *types.Request {
		r.Cohort = co()
		if r.Groups == nil && r.Crosstab == nil {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
		}
		if r.Aggregations == nil && r.Crosstab == nil {
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}}
		}
		return r
	}
	crosstab := func(row, col, cell string) *types.CrosstabSpec {
		return &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: row}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: col}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: cell},
			Margins: types.CrosstabMargins{Grand: true},
		}
	}
	selfJoin := func() []*types.JoinSpec {
		return []*types.JoinSpec{{Right: cohort, As: "r_", On: []types.OnPair{{LeftField: "n", RightField: "n"}}}}
	}
	sv := errors.SERVICE_VALIDATION
	type entry = struct {
		code errors.Code
		req  func() *types.Request
	}
	return map[string]entry{
		"aggregation": {sv, func() *types.Request {
			return grouped(&types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "zz"}}})
		}},
		"filter": {sv, func() *types.Request {
			return grouped(&types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "zz", Values: []string{"a"}}}})
		}},
		"group": {sv, func() *types.Request {
			return grouped(&types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "zz"}}})
		}},
		"attribute": {sv, func() *types.Request {
			return grouped(&types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "zz"}}})
		}},
		"feature": {sv, func() *types.Request {
			return grouped(&types.Request{Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "zz"}}})
		}},
		"test": {sv, func() *types.Request {
			return grouped(&types.Request{Groups: []*types.Group{}, Tests: []*types.Test{{Type: types.TEST_T, Field: "zz"}}})
		}},
		"regression": {sv, func() *types.Request {
			return grouped(&types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "zz", Predictors: []string{"n"}}}})
		}},
		"window": {errors.PULSE_WINDOW_INVALID, func() *types.Request {
			return grouped(&types.Request{Windows: []*types.Window{{Type: types.WIN_LAG, Field: "s", OrderBy: []types.OrderKey{{Field: "zz"}}}}})
		}},
		"sort": {sv, func() *types.Request {
			return grouped(&types.Request{Sort: []types.OrderKey{{Field: "zz"}}})
		}},
		"post_test": {sv, func() *types.Request {
			return grouped(&types.Request{PostTests: []*types.Test{{Type: types.TEST_TREND, Field: "zz", OrderBy: []types.OrderKey{{Field: "s"}}}}})
		}},
		"crosstab_row": {sv, func() *types.Request {
			return grouped(&types.Request{Crosstab: crosstab("zz", "cat", "n")})
		}},
		"crosstab_column": {sv, func() *types.Request {
			return grouped(&types.Request{Crosstab: crosstab("cat", "zz", "n")})
		}},
		"crosstab_cell": {sv, func() *types.Request {
			return grouped(&types.Request{Crosstab: crosstab("cat", "cat", "zz")})
		}},
		// Over a join the rule judges the JOINED schema: r_cat exists,
		// zz does not.
		"joined_group": {sv, func() *types.Request {
			return grouped(&types.Request{Joins: selfJoin(), Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "zz"}}})
		}},
		"joined_crosstab_row": {sv, func() *types.Request {
			return grouped(&types.Request{Joins: selfJoin(), Crosstab: crosstab("zz", "r_cat", "n")})
		}},
	}
}

// TestFieldRefs_UnknownFieldRefusedLikePredict: every slot family that
// names a field refuses an unknown name at runtime with exactly the
// code, message and details predict reports — Process, ProcessStream,
// and a crosstab on both the fused-enabled and fused-disabled engines.
func TestFieldRefs_UnknownFieldRefusedLikePredict(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()
	for _, disable := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
		if err != nil {
			t.Fatal(err)
		}
		for name, c := range unknownFieldRequests(cohort) {
			t.Run(fmt.Sprintf("%s/disable_fusion_%v", name, disable), func(t *testing.T) {
				_, rerr := p.Process(ctx, c.req())
				ce := requireCode(t, rerr, c.code)
				env := predictEnvelope(t, p, fs, cohort, c.req())
				sameEntry(t, env, rerr)
				if env.Data.(*descriptor.PredictResult).Valid {
					t.Fatal("predict Valid=true")
				}
				_, serr := p.ProcessStream(ctx, c.req())
				se := requireCode(t, serr, c.code)
				if se.Message != ce.Message || !reflect.DeepEqual(se.Details, ce.Details) {
					t.Fatalf("stream = %q %v, process = %q %v", se.Message, se.Details, ce.Message, ce.Details)
				}
			})
		}
	}
}

// TestFieldRefs_UnknownFieldLocatedInComposeAndChain: inside Compose the
// refusal carries details.request (serial and fail-fast parallel), on a
// chain stage details.stage — stage 0 against the cohort, a later stage
// against the previous stage's synthesised output — and the Compose and
// chain validators report the identical entry.
func TestFieldRefs_UnknownFieldLocatedInComposeAndChain(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}

	for name, c := range unknownFieldRequests(cohort) {
		compose := func() *types.ComposedRequest {
			return &types.ComposedRequest{Requests: []*types.Request{
				{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()},
				c.req(),
			}}
		}
		t.Run(name+"/compose serial", func(t *testing.T) {
			_, rerr := p.Compose(ctx, compose())
			if ce := requireCode(t, rerr, c.code); ce.Details["request"] != 1 {
				t.Fatalf("details = %v, want request=1", ce.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), rerr)
		})
		t.Run(name+"/compose parallel fail-fast", func(t *testing.T) {
			_, rerr := p.ComposeParallel(ctx, compose(), pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			if ce := requireCode(t, rerr, c.code); ce.Details["request"] != 1 {
				t.Fatalf("details = %v, want request=1", ce.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), rerr)
		})
	}

	// Chains are mergeable-only, so only the mergeable families reach
	// the field rule there.
	chainStage := func(field string) *types.Request {
		return &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: field, Label: "s"}},
		}
	}
	cases := map[string]struct {
		stages []*types.ChainStage
		stage  int
	}{
		"stage 0": {[]*types.ChainStage{{Name: "a", Request: chainStage("zz")}}, 0},
		// Stage 1 reads stage 0's output (cat, s): "n" was a cohort
		// field but is not a column of that output.
		"stage 1": {[]*types.ChainStage{{Name: "a", Request: chainStage("n")}, {Name: "b", Request: chainStage("n")}}, 1},
	}
	// A joined stage 0 judges the JOINED schema: r_cat resolves, zz not.
	t.Run("chain joined stage 0", func(t *testing.T) {
		mk := func(group string) *types.ChainRequest {
			stage := chainStage("n")
			stage.Groups[0].Field = group
			stage.Joins = []*types.JoinSpec{{Right: cohort, As: "r_", On: []types.OnPair{{LeftField: "n", RightField: "n"}}}}
			return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{{Request: stage}}}
		}
		if _, err := p.ProcessChain(ctx, mk("r_cat")); err != nil {
			t.Fatalf("runtime refused a joined field: %v", err)
		}
		if env := descx.ValidateChainWithOptions(bytes.NewReader(data), mk("r_cat"), opts); len(env.Errors) != 0 {
			t.Fatalf("validator refused a joined field: %+v", env.Errors)
		}
		_, rerr := p.ProcessChain(ctx, mk("zz"))
		if ce := requireCode(t, rerr, errors.SERVICE_VALIDATION); ce.Details["stage"] != 0 {
			t.Fatalf("details = %v, want stage=0", ce.Details)
		}
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk("zz"), opts), rerr)
	})
	for name, c := range cases {
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
			if ce := requireCode(t, rerr, errors.SERVICE_VALIDATION); ce.Details["stage"] != c.stage {
				t.Fatalf("details = %v, want stage=%d", ce.Details, c.stage)
			}
			sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
		})
	}
}

// TestFieldRefs_DerivedNamesAccepted: names the pipeline itself
// produces are not unknown — feature outputs, attribute labels (an
// attribute reading an EARLIER attribute's label included), defaulted
// aggregation labels, join As-prefixed fields, window inputs naming an
// aggregation label, and a post-test over an aggregation label. Predict
// and the runtime accept every one.
func TestFieldRefs_DerivedNamesAccepted(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	co := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	cat := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
	cases := map[string]func() *types.Request{
		"feature output": func() *types.Request {
			return &types.Request{Cohort: co(),
				Features:     []*types.Feature{{Type: types.FEAT_LOG, Field: "n"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "LOG_n"}}}
		},
		"attribute chain": func() *types.Request {
			return &types.Request{Cohort: co(),
				Attributes: []*types.Attribute{
					{Type: types.ATTR_FORMULA, Label: "x", Expression: "n * 2"},
					{Type: types.ATTR_ZSCORE, Field: "x", Label: "zx"},
				},
				Groups:       cat,
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "zx"}, {Type: types.AGG_SUM, Field: "x"}}}
		},
		"defaulted aggregation label": func() *types.Request {
			return &types.Request{Cohort: co(),
				Groups:       cat,
				Aggregations: []*types.Aggregation{{Field: "n"}},
				Sort:         []types.OrderKey{{Field: "AGG_SUM_n"}}}
		},
		"join As-prefixed": func() *types.Request {
			return &types.Request{Cohort: co(),
				Joins:        []*types.JoinSpec{{Right: cohort, As: "r_", On: []types.OnPair{{LeftField: "n", RightField: "n"}}}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r_cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "r_n", Label: "s"}}}
		},
		"window over aggregation label": func() *types.Request {
			return &types.Request{Cohort: co(),
				Groups:       cat,
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}},
				Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "s", OrderBy: []types.OrderKey{{Field: "s"}}}},
				Sort:         []types.OrderKey{{Field: "WIN_LAG_s"}}}
		},
		"post-test over aggregation label": func() *types.Request {
			return &types.Request{Cohort: co(),
				Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "n", Params: json.RawMessage(`{"interval":5}`)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}},
				PostTests:    []*types.Test{{Type: types.TEST_TREND, Field: "s", OrderBy: []types.OrderKey{{Field: "n"}}}}}
		},
		"formula without a field": func() *types.Request {
			return &types.Request{Cohort: co(),
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "n * 2"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Process(ctx, mk()); err != nil {
				t.Fatalf("runtime refused a derived name: %v", err)
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			if len(env.Errors) != 0 {
				t.Fatalf("predict refused a derived name: %+v", env.Errors)
			}
		})
	}
}

// TestFieldRefs_PipelineOrder: a derived name is visible only
// downstream of the stage that produces it. A filter runs before the
// attributes, so it cannot read an attribute label (the runtime used to
// filter on an all-null column and keep nothing); an attribute cannot
// read a LATER attribute's label. Both sides refuse.
func TestFieldRefs_PipelineOrder(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	cases := map[string]func() *types.Request{
		"filter on attribute label": func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "n * 2"}},
				Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "x", Params: json.RawMessage(`{"min":0}`)}},
				Aggregations: countAgg()}
		},
		"attribute reading a later label": func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes: []*types.Attribute{
					{Type: types.ATTR_ZSCORE, Field: "x", Label: "zx"},
					{Type: types.ATTR_FORMULA, Label: "x", Expression: "n * 2"},
				},
				Aggregations: countAgg()}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			_, rerr := p.Process(ctx, mk())
			requireCode(t, rerr, errors.SERVICE_VALIDATION)
			sameEntry(t, predictEnvelope(t, p, fs, cohort, mk()), rerr)
		})
	}
}

// TestJoinKeys_UnknownKeyRefusedLikeRuntime: an OnPair naming a key
// the left or right cohort lacks is refused by predict, the Compose
// slot validator and the chain stage-0 validator with the runtime's own
// PULSE_JOIN_FIELD_UNKNOWN code, message and details — the one rule
// (internal/encoding.JoinKeysRefusals). Inside Compose / a chain it is
// located like the join-count rule.
func TestJoinKeys_UnknownKeyRefusedLikeRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	for _, side := range []struct {
		name        string
		left, right string
		message     string
	}{
		{"left", "zz", "n", "OnPair.LeftField not found in left schema"},
		{"right", "n", "zz", "OnPair.RightField not found in right schema"},
	} {
		mk := func() *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: cohort},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}},
				Joins:        []*types.JoinSpec{{Right: cohort, As: "r_", On: []types.OnPair{{LeftField: side.left, RightField: side.right}}}},
			}
		}
		t.Run(side.name+"/process and predict", func(t *testing.T) {
			_, rerr := p.Process(ctx, mk())
			ce := requireCode(t, rerr, errors.PULSE_JOIN_FIELD_UNKNOWN)
			if ce.Message != side.message || ce.Details["field"] != "zz" || ce.Details["index"] != 0 {
				t.Fatalf("runtime = %q %v", ce.Message, ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, mk()), rerr)
			_, serr := p.ProcessStream(ctx, mk())
			requireCode(t, serr, errors.PULSE_JOIN_FIELD_UNKNOWN)
		})
		t.Run(side.name+"/crosstab", func(t *testing.T) {
			xt := func() *types.Request {
				r := mk()
				r.Groups, r.Aggregations = nil, nil
				r.Crosstab = &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r_cat"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
				}
				return r
			}
			_, rerr := p.Process(ctx, xt())
			requireCode(t, rerr, errors.PULSE_JOIN_FIELD_UNKNOWN)
			sameEntry(t, predictEnvelope(t, p, fs, cohort, xt()), rerr)
		})
		t.Run(side.name+"/compose slot", func(t *testing.T) {
			compose := func() *types.ComposedRequest {
				return &types.ComposedRequest{Requests: []*types.Request{
					{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()},
					mk(),
				}}
			}
			_, rerr := p.Compose(ctx, compose())
			if ce := requireCode(t, rerr, errors.PULSE_JOIN_FIELD_UNKNOWN); ce.Details["request"] != 1 {
				t.Fatalf("details = %v, want request=1", ce.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), rerr)
		})
		t.Run(side.name+"/chain stage 0", func(t *testing.T) {
			chain := func() *types.ChainRequest {
				stage := mk()
				stage.Cohort = nil
				return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{{Request: stage}}}
			}
			_, rerr := p.ProcessChain(ctx, chain())
			if ce := requireCode(t, rerr, errors.PULSE_JOIN_FIELD_UNKNOWN); ce.Details["stage"] != 0 {
				t.Fatalf("details = %v, want stage=0", ce.Details)
			}
			sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), chain(), opts), rerr)
		})
	}
}
