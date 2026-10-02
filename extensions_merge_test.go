package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension aggregator / grouper merge (U02b E4-B1, E4-B2): a
// registration declaring Mergeable folds per-partition partials through
// extend.MergeableAggregator.Merge / extend.MergeableGrouper.MergeState
// under ShardWorkers / DecodeWorkers and is admitted to ProcessChain.
// The cross-mode output parity lives in aggregatorParitySuite and
// grouperParitySuite (extensions_parity_*_test.go), which also assert
// the merge actually ran in both parallel modes.

const (
	aggMrgSum    types.AggregationType = "AGG_MRG_SUM"
	aggMrgSerial types.AggregationType = "AGG_MRG_SERIAL"
	grpMrgCat    types.GroupType       = "GROUP_MRG_CAT"
	grpMrgMerge  types.GroupType       = "GROUP_MRG_MERGE"
	grpMrgFloor  types.GroupType       = "GROUP_MRG_FLOOR"
	grpMrgFan    types.GroupType       = "GROUP_MRG_FAN"
	fltMrgAll    types.FiltererType    = "FILTER_MRG_ALL"
	attrMrgRow   types.AttributeType   = "ATTR_MRG_ROW"
	attrMrgTwo   types.AttributeType   = "ATTR_MRG_TWO"
	attrMrgBuf   types.AttributeType   = "ATTR_MRG_BUF"
)

// onlineNoMerge is an OnlineAggregator without Merge.
type onlineNoMerge struct{}

func (onlineNoMerge) Aggregate(extend.Rows, string) (float64, error) { return 0, nil }
func (onlineNoMerge) UpdateRow(extend.Record, string) error          { return nil }
func (onlineNoMerge) Finalize() (float64, error)                     { return 0, nil }

func mergeMismatch(t *testing.T, reg pulse.AggregatorRegistration, reason string) {
	t.Helper()
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError, got %T: %v", err, err)
	}
	if ce.Code != perr.PULSE_EXTENSION_MERGEABLE_MISMATCH {
		t.Fatalf("code = %s (%s), want PULSE_EXTENSION_MERGEABLE_MISMATCH", ce.Code, ce.Message)
	}
	if got := ce.Details["reason"]; got != reason {
		t.Errorf("details.reason = %v, want %s", got, reason)
	}
	if got := ce.Details["name"]; got != string(reg.Name) {
		t.Errorf("details.name = %v, want %s", got, reg.Name)
	}
}

// TestExtensions_ProbeAggregator_MergeableMismatch covers the three
// probe-validation triggers for Mergeable=true.
func TestExtensions_ProbeAggregator_MergeableMismatch(t *testing.T) {
	probe := &parityProbe{}
	t.Run("not_streamable", func(t *testing.T) {
		reg := paritySumRegistration(probe, aggMrgSum, true)
		reg.Streamable = false
		mergeMismatch(t, reg, "mergeable_without_streamable")
	})
	t.Run("value_lacks_merge", func(t *testing.T) {
		mergeMismatch(t, pulse.AggregatorRegistration{
			Name:       aggMrgSum,
			Streamable: true,
			Mergeable:  true,
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return onlineNoMerge{}, nil
			},
		}, "missing_merge_interface")
	})
	t.Run("components_not_mergeable", func(t *testing.T) {
		reg := paritySumRegistration(probe, aggMrgSum, true)
		reg.ComponentSchema.Mergeability = descriptor.None
		mergeMismatch(t, reg, "components_not_mergeable")
	})
	t.Run("partial_components_accepted", func(t *testing.T) {
		reg := paritySumRegistration(probe, aggMrgSum, true)
		reg.ComponentSchema.Mergeability = descriptor.Partial
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
			Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}}); err != nil {
			t.Fatalf("Mergeable aggregator with partial components refused: %v", err)
		}
	})
	t.Run("undeclared_merge_value_accepted", func(t *testing.T) {
		// A value that CAN merge but whose registration does not
		// declare it is fine: the declaration, not the interface,
		// routes the request.
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{
			Aggregators: []pulse.AggregatorRegistration{paritySumRegistration(probe, aggMrgSum, false)},
		}}); err != nil {
			t.Fatalf("undeclared mergeable value refused: %v", err)
		}
	})
}

func mergeGateExtensions(probe *parityProbe) pulse.Extensions {
	noFields := func(json.RawMessage) []string { return nil }
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			paritySumRegistration(probe, aggMrgSum, true),
			paritySumRegistration(probe, aggMrgSerial, false),
		},
		Groupers: []pulse.GrouperRegistration{{
			Name:       grpMrgCat,
			Streamable: true,
			Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
				return &parityCategoryGrouper{probe: probe, dict: fieldDict(schema, spec.Field), live: map[string]int{}}, nil
			},
			FieldInputs: noFields,
		}, {
			Name:       grpMrgMerge,
			Streamable: true,
			Mergeable:  true,
			Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
				return &parityCategoryGrouper{probe: probe, dict: fieldDict(schema, spec.Field), live: map[string]int{}}, nil
			},
			FieldInputs: noFields,
		}, {
			Name:       grpMrgFan,
			Streamable: true,
			Mergeable:  true,
			FansOut:    true,
			Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
				return &parityPerElementGrouper{probe: probe, dict: fieldDict(schema, spec.Field), live: map[string]parityLabelStat{}}, nil
			},
			FieldInputs: noFields,
		}},
		Filterers: []pulse.FiltererRegistration{{
			Name:        fltMrgAll,
			Factory:     func() extend.FiltererBuilder { return parityIncludeBuilder{} },
			FieldInputs: noFields,
		}},
		Attributes: []pulse.AttributeRegistration{
			{Name: attrMrgRow, Mode: pulse.AttributeModeRowLocal, FieldInputs: noFields,
				Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
					return &parityPopcount{probe: probe}, nil
				}},
			{Name: attrMrgTwo, Mode: pulse.AttributeModeTwoPass, FieldInputs: noFields,
				Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
					return &parityZScore{probe: probe}, nil
				}},
			{Name: attrMrgBuf, Mode: pulse.AttributeModeBuffered, FieldInputs: noFields,
				Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
					return &parityPopcount{probe: probe}, nil
				}},
		},
	}
}

// TestExtensions_CanMergeRequestWithExtensions pins the merge gate per
// extension category: aggregators and groupers (single-key and
// fan-out) on their declaration (decimal targets included), filterers
// and row_local attributes as row-local operators, two_pass / buffered
// attributes never. A nil registry is exactly CanMergeRequest.
func TestExtensions_CanMergeRequestWithExtensions(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: mergeGateExtensions(&parityProbe{})})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	reg := pulse.ServiceForTest(p).Extensions()
	schema := paritySchema(t)
	agg := func(at types.AggregationType, field string) []*types.Aggregation {
		return []*types.Aggregation{{Type: at, Field: field, Label: "v"}}
	}
	attr := func(at types.AttributeType) []*types.Attribute {
		return []*types.Attribute{{Type: at, Field: "tags", Label: "d"}}
	}
	byRegion := func(gt types.GroupType) []*types.Group { return []*types.Group{{Type: gt, Field: "region"}} }
	cases := []struct {
		name  string
		req   *types.Request
		want  bool
		chain bool
	}{
		{"ext_mergeable", &types.Request{Aggregations: agg(aggMrgSum, "score")}, true, true},
		{"ext_undeclared", &types.Request{Aggregations: agg(aggMrgSerial, "score")}, false, false},
		{"ext_mergeable_decimal", &types.Request{Aggregations: agg(aggMrgSum, "amount")}, true, true},
		{"builtin_decimal", &types.Request{Aggregations: agg(types.AGG_SUM, "amount")}, false, false},
		{"ext_agg_builtin_grouper", &types.Request{Aggregations: agg(aggMrgSum, "score"), Groups: byRegion(types.GROUP_CATEGORY)}, true, true},
		{"ext_grouper_undeclared", &types.Request{Aggregations: agg(types.AGG_SUM, "score"), Groups: byRegion(grpMrgCat)}, false, false},
		{"ext_grouper_mergeable", &types.Request{Aggregations: agg(types.AGG_SUM, "score"), Groups: byRegion(grpMrgMerge)}, true, true},
		{"ext_grouper_fanout_mergeable", &types.Request{Aggregations: agg(types.AGG_SUM, "score"),
			Groups: []*types.Group{{Type: grpMrgFan, Field: "tags"}}}, true, true},
		{"ext_agg_ext_grouper", &types.Request{Aggregations: agg(aggMrgSum, "score"), Groups: byRegion(grpMrgMerge)}, true, true},
		{"ext_filterer", &types.Request{Aggregations: agg(types.AGG_SUM, "score"),
			Filterers: []*types.Filterer{{Type: fltMrgAll, Field: "region", Values: []string{"north"}}}}, true, true},
		{"ext_row_local_attr", &types.Request{Aggregations: agg(types.AGG_SUM, "d"), Attributes: attr(attrMrgRow)}, true, true},
		{"ext_two_pass_attr", &types.Request{Aggregations: agg(types.AGG_SUM, "d"), Attributes: attr(attrMrgTwo)}, false, false},
		{"ext_buffered_attr", &types.Request{Aggregations: agg(types.AGG_SUM, "d"), Attributes: attr(attrMrgBuf)}, false, false},
		{"builtin_mergeable", &types.Request{Aggregations: agg(types.AGG_SUM, "score")}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := processing.CanMergeRequestWithExtensions(c.req, schema, reg); got != c.want {
				t.Errorf("CanMergeRequestWithExtensions = %v, want %v", got, c.want)
			}
			if got := processing.CanChainRequestWithExtensions(c.req, schema, reg); got != c.chain {
				t.Errorf("CanChainRequestWithExtensions = %v, want %v", got, c.chain)
			}
			nilReg := processing.CanMergeRequestWithExtensions(c.req, schema, nil)
			if builtin := processing.CanMergeRequest(c.req, schema); nilReg != builtin {
				t.Errorf("nil registry = %v but CanMergeRequest = %v", nilReg, builtin)
			}
		})
	}
}

// openMergeArchive builds a three-shard archive of the parity fixture
// behind a Pulse configured with ext and three shard workers.
func openMergeArchive(t *testing.T, ext pulse.Extensions) *pulse.Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	s := paritySchema(t)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeParityCohort(t, fsys, path, s, i*40, (i+1)*40)
	}
	p, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 3, Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return p
}

// TestExtensions_MergeableAggregatorMergesShards asserts the
// declaration — not the value's method set — decides whether the
// per-shard reducer folds an extension aggregator through Merge: the
// same paritySum merges when declared and runs serially when not, and
// both answer exactly what AGG_SUM answers.
func TestExtensions_MergeableAggregatorMergesShards(t *testing.T) {
	probe := &parityProbe{}
	p := openMergeArchive(t, pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		paritySumRegistration(probe, aggMrgSum, true),
		paritySumRegistration(probe, aggMrgSerial, false),
	}})
	run := func(at types.AggregationType) string {
		t.Helper()
		resp, err := p.Process(context.Background(), &types.Request{
			Cohort:       &types.Cohort{Filename: "archive.pulse"},
			Aggregations: []*types.Aggregation{{Type: at, Field: "score", Label: "total"}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		})
		if err != nil {
			t.Fatalf("Process(%s): %v", at, err)
		}
		return mustJSON(t, resp.Data)
	}
	want := run(types.AGG_SUM)
	probe.reset()
	if got := run(aggMrgSum); got != want {
		t.Errorf("mergeable extension: %s, want %s", got, want)
	}
	if probe.merges.Load() == 0 {
		t.Error("Mergeable extension over a shard archive never merged")
	}
	probe.reset()
	if got := run(aggMrgSerial); got != want {
		t.Errorf("undeclared extension: %s, want %s", got, want)
	}
	if n := probe.merges.Load(); n != 0 {
		t.Errorf("undeclared extension merged %d times; the declaration must route it serially", n)
	}
}

// TestExtensions_MergeableAggregatorChains asserts a Mergeable
// extension aggregator is admitted to ProcessChain and an undeclared
// one is refused with PULSE_CHAIN_NOT_MERGEABLE.
func TestExtensions_MergeableAggregatorChains(t *testing.T) {
	probe := &parityProbe{}
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		paritySumRegistration(probe, aggMrgSum, true),
		paritySumRegistration(probe, aggMrgSerial, false),
	}}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	chain := func(at types.AggregationType) (*types.ChainResponse, error) {
		return p.ProcessChain(context.Background(), &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "parity.pulse"},
			Stages: []*types.ChainStage{
				{Name: "per_region", Request: &types.Request{
					Aggregations: []*types.Aggregation{{Type: at, Field: "score", Label: "total"}},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				}},
				{Name: "overall", Request: &types.Request{
					Aggregations: []*types.Aggregation{{Type: at, Field: "total", Label: "grand"}},
				}},
			},
		})
	}
	builtin, err := chain(types.AGG_SUM)
	if err != nil {
		t.Fatalf("built-in chain: %v", err)
	}
	ext, err := chain(aggMrgSum)
	if err != nil {
		t.Fatalf("mergeable extension chain: %v", err)
	}
	if b, e := mustJSON(t, builtin.Stages[1].Data), mustJSON(t, ext.Stages[1].Data); b != e {
		t.Errorf("chain output differs\nbuiltin:   %s\nextension: %s", b, e)
	}
	_, err = chain(aggMrgSerial)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_CHAIN_NOT_MERGEABLE {
		t.Fatalf("undeclared extension chain: err = %v, want PULSE_CHAIN_NOT_MERGEABLE", err)
	}
}

// TestExtensions_ManifestProjectsMergeable asserts the declaration
// reaches the manifest's extensions block (OperatorMeta.Mergeable).
func TestExtensions_ManifestProjectsMergeable(t *testing.T) {
	probe := &parityProbe{}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			paritySumRegistration(probe, aggMrgSum, true),
			paritySumRegistration(probe, aggMrgSerial, false),
		},
	}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	got := map[string]bool{}
	for _, m := range p.Manifest(context.Background()).Extensions.Aggregators {
		got[m.Name] = m.Mergeable
	}
	if !got[string(aggMrgSum)] || got[string(aggMrgSerial)] {
		t.Errorf("manifest mergeable = %v; want %s true, %s false", got, aggMrgSum, aggMrgSerial)
	}
}

// TestRunNullRecords_AttributePrimaryFieldAcrossModes pins
// Components.Run.NullRecords when the primary aggregation field is a
// row-local attribute LABEL, over a built-in ATTR_FORMULA. The
// serial grouped streaming path and both parallel reducers used to
// tally the primary field BEFORE the attribute landed: the serial path
// then read an absent field on the first row and the previous row's
// value on every reused record after it (null_records 1), and the
// reducers read it absent on every row (null_records = filtered). The
// buffered exit, which counts after attributes, is the reference.
func TestRunNullRecords_AttributePrimaryFieldAcrossModes(t *testing.T) {
	large := parityLargeCohort(t.TempDir())
	req := func(extra ...*types.Attribute) *types.Request {
		return &types.Request{
			Attributes: append([]*types.Attribute{{
				Type: types.ATTR_FORMULA, Field: "qty", Expression: "qty * 2", Label: "d",
			}}, extra...),
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "total"}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		}
	}
	// ATTR_PERCENTILE forces the buffered exit without touching Data.
	buffered := req(&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"})
	for _, mode := range parityModes(large) {
		if mode.decorate != nil {
			continue
		}
		t.Run(mode.name, func(t *testing.T) {
			p, path := mode.open(t, pulse.Extensions{})
			nulls := func(r *types.Request) int64 {
				t.Helper()
				c := *r
				c.Cohort = &types.Cohort{Filename: path}
				resp, err := p.Process(context.Background(), &c)
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if resp.Components == nil || resp.Components.Run == nil {
					t.Fatal("no Components.Run")
				}
				return resp.Components.Run.NullRecords
			}
			want := nulls(buffered)
			if got := nulls(req()); got != want {
				t.Errorf("NullRecords = %d, buffered exit says %d", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// Extension grouper merge (E4-B2).
// ---------------------------------------------------------------------

// floorCategoryGrouper keys exactly like GROUP_CATEGORY but carries no
// components state and no MergeState: the bucket-less shape whose
// Components.Groupers floor (TotalN) can only come from the
// orchestrator's own (record, bucket) assignment count.
type floorCategoryGrouper struct{ inner *parityCategoryGrouper }

func (g floorCategoryGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	return g.inner.Group(rows, field)
}

func (g floorCategoryGrouper) KeyForRow(rec extend.Record, field string) (string, bool, error) {
	return g.inner.KeyForRow(rec, field)
}

// floorPerElementGrouper is the fan-out twin of floorCategoryGrouper.
type floorPerElementGrouper struct{ inner *parityPerElementGrouper }

func (g floorPerElementGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	return g.inner.Group(rows, field)
}

func (g floorPerElementGrouper) KeysForRow(rec extend.Record, field string) ([]string, bool, error) {
	return g.inner.KeysForRow(rec, field)
}

// selfEmittingGrouper carries its own Components() method — an emitter
// the adapter adopts — but no MergeState.
type selfEmittingGrouper struct{ floorCategoryGrouper }

func (selfEmittingGrouper) Components() (map[string]any, error) { return map[string]any{"n": 0}, nil }

func floorGrouperRegistrations(probe *parityProbe) []pulse.GrouperRegistration {
	noFields := func(json.RawMessage) []string { return nil }
	return []pulse.GrouperRegistration{{
		Name:       grpMrgFloor,
		Streamable: true,
		Mergeable:  true,
		Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
			return floorCategoryGrouper{&parityCategoryGrouper{probe: probe, dict: fieldDict(schema, spec.Field), live: map[string]int{}}}, nil
		},
		FieldInputs: noFields,
	}, {
		Name:       grpMrgFan,
		Streamable: true,
		Mergeable:  true,
		FansOut:    true,
		Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
			return floorPerElementGrouper{&parityPerElementGrouper{probe: probe, dict: fieldDict(schema, spec.Field), live: map[string]parityLabelStat{}}}, nil
		},
		FieldInputs: noFields,
	}}
}

func grouperMergeMismatch(t *testing.T, reg pulse.GrouperRegistration, reason string) {
	t.Helper()
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{Groupers: []pulse.GrouperRegistration{reg}}})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError, got %T: %v", err, err)
	}
	if ce.Code != perr.PULSE_EXTENSION_MERGEABLE_MISMATCH {
		t.Fatalf("code = %s (%s), want PULSE_EXTENSION_MERGEABLE_MISMATCH", ce.Code, ce.Message)
	}
	if got := ce.Details["reason"]; got != reason {
		t.Errorf("details.reason = %v, want %s", got, reason)
	}
	if got := ce.Details["category"]; got != "grouper" {
		t.Errorf("details.category = %v, want grouper", got)
	}
}

// TestExtensions_ProbeGrouper_MergeableMismatch covers the probe
// triggers for a grouper declaring Mergeable=true. MergeState is
// required only when the grouper EMITS components (ComponentsFunc or
// its own Components() method): a bucket-less grouper has no state to
// fold, so the adapter supplies a no-op merge.
func TestExtensions_ProbeGrouper_MergeableMismatch(t *testing.T) {
	probe := &parityProbe{}
	floor := floorGrouperRegistrations(probe)[0]
	emitSchema := descriptor.ComponentSchema{
		Keys:         []descriptor.ComponentKey{{Name: "n", Type: "int", Description: "Bucket count."}},
		Mergeability: descriptor.Mergeable,
	}
	emit := func(extend.Grouper) (map[string]any, error) { return map[string]any{"n": 0}, nil }
	t.Run("not_streamable", func(t *testing.T) {
		reg := floor
		reg.Streamable = false
		grouperMergeMismatch(t, reg, "mergeable_without_streamable")
	})
	t.Run("emitter_lacks_merge", func(t *testing.T) {
		reg := floor
		reg.ComponentSchema, reg.ComponentsFunc = emitSchema, emit
		grouperMergeMismatch(t, reg, "missing_merge_interface")
	})
	t.Run("self_emitter_lacks_merge", func(t *testing.T) {
		// No ComponentsFunc and no schema: the value's own Components()
		// method is the emitter the adapter adopts.
		reg := floor
		reg.Factory = func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
			return selfEmittingGrouper{floorCategoryGrouper{&parityCategoryGrouper{probe: probe, live: map[string]int{}}}}, nil
		}
		grouperMergeMismatch(t, reg, "missing_merge_interface")
	})
	t.Run("components_not_mergeable", func(t *testing.T) {
		reg := floor
		reg.Factory = func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
			return &parityCategoryGrouper{probe: probe, live: map[string]int{}}, nil
		}
		reg.ComponentSchema, reg.ComponentsFunc = emitSchema, emit
		reg.ComponentSchema.Mergeability = descriptor.None
		grouperMergeMismatch(t, reg, "components_not_mergeable")
	})
	t.Run("emitter_with_merge_accepted", func(t *testing.T) {
		reg := floor
		reg.Factory = func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
			return &parityCategoryGrouper{probe: probe, live: map[string]int{}}, nil
		}
		reg.ComponentSchema, reg.ComponentsFunc = emitSchema, emit
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
			Extensions: pulse.Extensions{Groupers: []pulse.GrouperRegistration{reg}}}); err != nil {
			t.Fatalf("Mergeable emitting grouper with MergeState refused: %v", err)
		}
	})
	t.Run("floor_only_accepted", func(t *testing.T) {
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
			Extensions: pulse.Extensions{Groupers: floorGrouperRegistrations(probe)}}); err != nil {
			t.Fatalf("Mergeable floor-only grouper refused: %v", err)
		}
	})
}

// TestExtensions_MergeableGrouperFloorAcrossModes pins
// Components.Groupers' universal floor for a Mergeable extension
// grouper that emits no components (single-key and fan-out) against
// its built-in twin in every mode. With no buckets payload TotalN is
// the orchestrator's (record, bucket) assignment count; the parallel
// reducers used to drop it from the merged GroupedTail, so the merged
// arm reported total_n 0 and n_null = every filtered record.
func TestExtensions_MergeableGrouperFloorAcrossModes(t *testing.T) {
	large := parityLargeCohort(t.TempDir())
	cases := []struct {
		name    string
		builtin types.GroupType
		ext     types.GroupType
		field   string
	}{
		{"single_key", types.GROUP_CATEGORY, grpMrgFloor, "region"},
		{"fan_out", types.GROUP_SET_PER_ELEMENT, grpMrgFan, "tags"},
	}
	for _, mode := range parityModes(large) {
		t.Run(mode.name, func(t *testing.T) {
			p, path := mode.open(t, pulse.Extensions{Groupers: floorGrouperRegistrations(&parityProbe{})})
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					run := func(gt types.GroupType) (*types.Response, *types.Request) {
						t.Helper()
						r := &types.Request{
							Cohort:       &types.Cohort{Filename: path},
							Aggregations: []*types.Aggregation{sumAgg("s", "score")},
							Groups:       []*types.Group{{Type: gt, Field: c.field}},
						}
						if mode.decorate != nil {
							r = mode.decorate(r)
						}
						resp, err := p.Process(context.Background(), r)
						if err != nil {
							t.Fatalf("Process(%s): %v", gt, err)
						}
						return resp, r
					}
					b, _ := run(c.builtin)
					e, eReq := run(c.ext)
					if mode.mergeable {
						reg := pulse.ServiceForTest(p).Extensions()
						if !processing.CanMergeRequestWithExtensions(eReq, paritySchema(t), reg) {
							t.Fatal("extension request does not clear the merge gate; the mode would not merge")
						}
					}
					if bd, ed := mustJSON(t, b.Data), mustJSON(t, e.Data); bd != ed {
						t.Errorf("Data differs\nbuiltin:   %s\nextension: %s", bd, ed)
					}
					if b.Components == nil || e.Components == nil || len(b.Components.Groupers) != 1 || len(e.Components.Groupers) != 1 {
						t.Fatalf("missing Components.Groupers: builtin %+v extension %+v", b.Components, e.Components)
					}
					bg, eg := b.Components.Groupers[0], e.Components.Groupers[0]
					if bg.TotalN == 0 {
						t.Fatal("built-in total_n is 0; the fixture proves nothing")
					}
					if bg.TotalN != eg.TotalN || bg.NNull != eg.NNull {
						t.Errorf("floor differs: builtin {total_n %d, n_null %d}, extension {total_n %d, n_null %d}",
							bg.TotalN, bg.NNull, eg.TotalN, eg.NNull)
					}
				})
			}
		})
	}
}

// TestExtensions_ManifestProjectsMergeableGrouper asserts the grouper
// declaration reaches the manifest's extensions block.
func TestExtensions_ManifestProjectsMergeableGrouper(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: mergeGateExtensions(&parityProbe{})})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	got := map[string]bool{}
	for _, m := range p.Manifest(context.Background()).Extensions.Groupers {
		got[m.Name] = m.Mergeable
	}
	if !got[string(grpMrgMerge)] || !got[string(grpMrgFan)] || got[string(grpMrgCat)] {
		t.Errorf("manifest mergeable = %v; want %s and %s true, %s false", got, grpMrgMerge, grpMrgFan, grpMrgCat)
	}
}
