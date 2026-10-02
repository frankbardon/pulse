package pulse_test

// Parity suites for every extension category beyond the aggregator:
// each registers a test-only reimplementation of one built-in, written
// against the public extend package alone, and runs it through the
// shared cohort × mode matrix in extensions_parity_test.go.
//
// Cells the matrix does not exercise, by design:
//   - fused crosstab: carried by dedicated tests rather than matrix
//     rows — TestExtensions_SingleKeyGrouperFusesCrosstab (a single-key
//     extension grouper takes the fused arm and matches GROUP_CATEGORY)
//     and TestExtensions_TwoPassAttributeCrosstabNotFused (a two_pass
//     extension attribute declines it, as ATTR_ZSCORE does).
//   - parallel / per-shard: extension aggregators and groupers merge
//     when their registration declares Mergeable (the aggregator and
//     grouper suites assert Merge / MergeState ran); extension
//     filterers and row_local attributes merge as row-local operators.
//   - windows and post-tests run over materialised result rows, so the
//     streaming mode is only "streaming" up to the result set; their
//     suites carry no path probe.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

func sumAgg(label, field string) *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_SUM, Field: field, Label: label}
}

// ---------------------------------------------------------------------
// Grouper: GROUP_PARITY_CATEGORY (single-key streaming) and
// GROUP_PARITY_SET_PER_ELEMENT (fan-out, multi-key streaming).
// ---------------------------------------------------------------------

const (
	grpParityCategory   types.GroupType = "GROUP_PARITY_CATEGORY"
	grpParityPerElement types.GroupType = "GROUP_PARITY_SET_PER_ELEMENT"
)

// parityCategoryGrouper mirrors GROUP_CATEGORY: a categorical key
// resolves to its dictionary label (falling back to the index), any
// other numeric key renders as an integer when integral and the
// shortest float otherwise; a null key drops the row.
type parityCategoryGrouper struct {
	probe *parityProbe
	dict  *encoding.Dictionary
	live  map[string]int
}

func (g *parityCategoryGrouper) key(rec extend.Record, field string) (string, bool) {
	v, ok := rec.NumericValue(field)
	if !ok {
		return "", false
	}
	if g.dict != nil {
		if k := g.dict.Resolve(uint32(v)); k != "" {
			return k, true
		}
		return strconv.FormatUint(uint64(uint32(v)), 10), true
	}
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10), true
	}
	return strconv.FormatFloat(v, 'f', -1, 64), true
}

func (g *parityCategoryGrouper) KeyForRow(rec extend.Record, field string) (string, bool, error) {
	g.probe.online.Add(1)
	k, ok := g.key(rec, field)
	if ok {
		g.live[k]++
	}
	return k, ok, nil
}

func (g *parityCategoryGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	g.probe.buffered.Add(1)
	// Reset like the built-in: a buffered Group replaces the live state.
	g.live = map[string]int{}
	out := map[string][]int{}
	for i := 0; i < rows.Len(); i++ {
		if k, ok := g.key(rows.At(i), field); ok {
			out[k] = append(out[k], i)
			g.live[k]++
		}
	}
	return out, nil
}

// MergeState folds another partition's per-bucket counts into the
// receiver exactly as the built-in GROUP_CATEGORY's MergeGrouperState
// does (a per-key sum). The probe counts the call so the parallel
// modes can prove a merge ran.
func (g *parityCategoryGrouper) MergeState(other extend.Grouper) error {
	o, ok := other.(*parityCategoryGrouper)
	if !ok {
		return fmt.Errorf("parityCategoryGrouper.MergeState: got %T", other)
	}
	g.probe.merges.Add(1)
	for k, n := range o.live {
		g.live[k] += n
	}
	return nil
}

// parityPerElementGrouper mirrors GROUP_SET_PER_ELEMENT: one bucket per
// selected dictionary label in ascending bit order; a null or empty
// mask drops the row.
type parityPerElementGrouper struct {
	probe *parityProbe
	dict  *encoding.Dictionary
	// live mirrors the built-in's per-label {count, dict_index} state.
	live map[string]parityLabelStat
}

type parityLabelStat struct{ count, dictIndex int }

// keys returns the selected labels and their dictionary indices, and
// folds each observation into the live state.
func (g *parityPerElementGrouper) keys(rec extend.Record, field string) []string {
	m, ok := rec.SetMaskValue(field)
	if !ok || g.dict == nil {
		return nil
	}
	var out []string
	n := g.dict.Count()
	for i, ok := m.NextBit(0); ok && i < n; i, ok = m.NextBit(i + 1) {
		if l := g.dict.Resolve(uint32(i)); l != "" {
			out = append(out, l)
			st := g.live[l]
			st.count++
			st.dictIndex = i
			g.live[l] = st
		}
	}
	return out
}

func (g *parityPerElementGrouper) KeysForRow(rec extend.Record, field string) ([]string, bool, error) {
	g.probe.online.Add(1)
	ks := g.keys(rec, field)
	return ks, len(ks) > 0, nil
}

func (g *parityPerElementGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	g.probe.buffered.Add(1)
	g.live = map[string]parityLabelStat{}
	out := map[string][]int{}
	for i := 0; i < rows.Len(); i++ {
		for _, k := range g.keys(rows.At(i), field) {
			out[k] = append(out[k], i)
		}
	}
	return out, nil
}

// MergeState folds another partition's per-label counts into the
// receiver, as the built-in GROUP_SET_PER_ELEMENT does (counts sum; the
// dictionary index is a pure function of the label).
func (g *parityPerElementGrouper) MergeState(other extend.Grouper) error {
	o, ok := other.(*parityPerElementGrouper)
	if !ok {
		return fmt.Errorf("parityPerElementGrouper.MergeState: got %T", other)
	}
	g.probe.merges.Add(1)
	for l, os := range o.live {
		st := g.live[l]
		st.count += os.count
		st.dictIndex = os.dictIndex
		g.live[l] = st
	}
	return nil
}

var (
	_ extend.StreamingGrouper         = (*parityCategoryGrouper)(nil)
	_ extend.MultiKeyStreamingGrouper = (*parityPerElementGrouper)(nil)
	_ extend.MergeableGrouper         = (*parityCategoryGrouper)(nil)
	_ extend.MergeableGrouper         = (*parityPerElementGrouper)(nil)
)

func fieldDict(schema *encoding.Schema, name string) *encoding.Dictionary {
	if schema == nil {
		return nil
	}
	if f := schema.Field(name); f != nil && f.Type.HasDictionary() {
		return f.Dictionary
	}
	return nil
}

func grouperParitySuite() paritySuite {
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{sumAgg("s", "score"), sumAgg("q", "qty"),
			{Type: types.AGG_COUNT, Field: "qty", Label: "n"}}
	}
	row := func(name string, b, e types.GroupType, field string, filters []*types.Filterer) parityRow {
		return parityRow{
			name:      name,
			builtin:   &types.Request{Aggregations: aggs(), Groups: []*types.Group{{Type: b, Field: field}}, Filterers: filters},
			extension: &types.Request{Aggregations: aggs(), Groups: []*types.Group{{Type: e, Field: field}}, Filterers: filters},
		}
	}
	contains := func(r parityRow, subs ...string) parityRow { r.mustContain = subs; return r }
	cat, fan := types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT
	return paritySuite{
		name:       "grouper",
		mergeProbe: true,
		register: func(probe *parityProbe) pulse.Extensions {
			return pulse.Extensions{Groupers: []pulse.GrouperRegistration{
				{
					Name:        grpParityCategory,
					Description: "Test-only extend reimplementation of GROUP_CATEGORY.",
					Streamable:  true,
					Mergeable:   true,
					Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
						var dict *encoding.Dictionary
						if schema != nil {
							if f := schema.Field(spec.Field); f != nil && f.Type.IsCategorical() {
								dict = f.Dictionary
							}
						}
						return &parityCategoryGrouper{probe: probe, dict: dict, live: map[string]int{}}, nil
					},
					FieldInputs: func(json.RawMessage) []string { return nil },
				},
				{
					Name:        grpParityPerElement,
					Description: "Test-only extend reimplementation of GROUP_SET_PER_ELEMENT.",
					Streamable:  true,
					Mergeable:   true,
					FansOut:     true,
					Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
						return &parityPerElementGrouper{probe: probe, dict: fieldDict(schema, spec.Field), live: map[string]parityLabelStat{}}, nil
					},
					FieldInputs: func(json.RawMessage) []string { return nil },
				},
			}}
		},
		rows: []parityRow{
			contains(row("categorical", cat, grpParityCategory, "region", nil), `"region":"north"`),
			row("numeric_key", cat, grpParityCategory, "qty", nil),
			row("categorical_filtered", cat, grpParityCategory, "region",
				[]*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"south", "east"}}}),
			row("fanout_narrow_set", fan, grpParityPerElement, "tags", nil),
			contains(row("fanout_wide_u128_nullable", fan, grpParityPerElement, "w128", nil), "a070", "a073"),
			contains(row("fanout_wide_u256", fan, grpParityPerElement, "w256", nil), "b200", "b202"),
		},
		// Known gap: extension groupers surface no per-operator
		// Components figures (built-in GROUP_CATEGORY / GROUP_SET_PER_ELEMENT
		// emit buckets); the universal floor is still compared.
		stripGrouperOperator: true,
	}
}

// ---------------------------------------------------------------------
// Filterer: FILTER_PARITY_INCLUDE, an extend reimplementation of
// FILTER_INCLUDE.
// ---------------------------------------------------------------------

const fltParityInclude types.FiltererType = "FILTER_PARITY_INCLUDE"

type parityIncludeBuilder struct{}

func (parityIncludeBuilder) Build(spec *types.Filterer, schema *encoding.Schema) (extend.FilterFunc, error) {
	var dict *encoding.Dictionary
	if f := schema.Field(spec.Field); f != nil && f.Type.IsCategorical() {
		dict = f.Dictionary
	}
	set := make(map[float64]bool, len(spec.Values))
	for _, v := range spec.Values {
		if dict != nil {
			id, ok := dict.IDFor(v)
			if !ok {
				return nil, strconv.ErrSyntax
			}
			set[float64(id)] = true
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, err
		}
		set[f] = true
	}
	return func(rec extend.Record) (bool, error) {
		v, ok := rec.NumericValue(spec.Field)
		return ok && set[v], nil
	}, nil
}

func filtererParitySuite() paritySuite {
	row := func(name string, groups []*types.Group, field string, values ...string) parityRow {
		aggs := []*types.Aggregation{sumAgg("s", "score"), {Type: types.AGG_COUNT, Field: "qty", Label: "n"}}
		return parityRow{
			name: name,
			builtin: &types.Request{Aggregations: aggs, Groups: groups,
				Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: field, Values: values}}},
			extension: &types.Request{Aggregations: aggs, Groups: groups,
				Filterers: []*types.Filterer{{Type: fltParityInclude, Field: field, Values: values}}},
		}
	}
	byRegion := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	return paritySuite{
		name:   "filterer",
		noPath: true, // filterers are row-local in every mode
		register: func(*parityProbe) pulse.Extensions {
			return pulse.Extensions{Filterers: []pulse.FiltererRegistration{{
				Name:        fltParityInclude,
				Description: "Test-only extend reimplementation of FILTER_INCLUDE.",
				Factory:     func() extend.FiltererBuilder { return parityIncludeBuilder{} },
				FieldInputs: func(json.RawMessage) []string { return nil },
			}}}
		},
		rows: []parityRow{
			row("categorical", nil, "region", "north", "west"),
			row("categorical_grouped", byRegion, "region", "north", "west"),
			row("numeric_u32", byRegion, "qty", "1", "5", "12"),
			row("nullable_f64", nil, "score", "3.5", "10.5", "40.5"),
		},
	}
}

// ---------------------------------------------------------------------
// Attribute: ATTR_PARITY_ZSCORE (two_pass) and ATTR_PARITY_SET_POPCOUNT
// (row_local).
// ---------------------------------------------------------------------

const (
	attrParityZScore   types.AttributeType = "ATTR_PARITY_ZSCORE"
	attrParityPopcount types.AttributeType = "ATTR_PARITY_SET_POPCOUNT"
)

// parityZScore mirrors ATTR_ZSCORE: a population Welford pass, then
// (x - mean) / sd per row; a null row, or a zero sd, yields 0.
type parityZScore struct {
	probe          *parityProbe
	count          uint64
	mean, m2       float64
	fMean, fStdDev float64
}

func (a *parityZScore) PrePass(rec extend.Record, field string) error {
	if a.count == 0 && a.mean == 0 {
		a.probe.online.Add(1)
	}
	a.prePass(rec, field)
	return nil
}

func (a *parityZScore) prePass(rec extend.Record, field string) {
	v, ok := rec.NumericValue(field)
	if !ok {
		return
	}
	a.count++
	d := v - a.mean
	a.mean += d / float64(a.count)
	a.m2 += d * (v - a.mean)
}

func (a *parityZScore) Finalize() error {
	if a.count > 0 {
		a.fMean, a.fStdDev = a.mean, math.Sqrt(a.m2/float64(a.count))
	}
	return nil
}

func (a *parityZScore) Row(rec extend.Record, field string) (float64, error) {
	v, ok := rec.NumericValue(field)
	if !ok || a.fStdDev == 0 {
		return 0, nil
	}
	return (v - a.fMean) / a.fStdDev, nil
}

func (a *parityZScore) Compute(rows extend.Rows, field string) ([]float64, error) {
	a.probe.buffered.Add(1)
	if rows.Len() == 0 {
		return []float64{}, nil
	}
	for i := 0; i < rows.Len(); i++ {
		a.prePass(rows.At(i), field)
	}
	_ = a.Finalize()
	out := make([]float64, rows.Len())
	for i := range out {
		out[i], _ = a.Row(rows.At(i), field)
	}
	return out, nil
}

// parityPopcount mirrors ATTR_SET_POPCOUNT: the selected-member count of
// the row's set mask, 0 for a null row. The online probe counts Row
// calls the engine makes outside Compute.
type parityPopcount struct {
	probe     *parityProbe
	computing bool
}

func (a *parityPopcount) Row(rec extend.Record, field string) (float64, error) {
	if !a.computing {
		a.probe.online.Add(1)
	}
	m, ok := rec.SetMaskValue(field)
	if !ok {
		return 0, nil
	}
	return float64(m.PopCount()), nil
}

func (a *parityPopcount) Compute(rows extend.Rows, field string) ([]float64, error) {
	a.probe.buffered.Add(1)
	a.computing = true
	defer func() { a.computing = false }()
	out := make([]float64, rows.Len())
	for i := range out {
		out[i], _ = a.Row(rows.At(i), field)
	}
	return out, nil
}

var (
	_ extend.TwoPassAttribute  = (*parityZScore)(nil)
	_ extend.RowLocalAttribute = (*parityPopcount)(nil)
)

func attributeParitySuite() paritySuite {
	row := func(name string, b, e types.AttributeType, field string, groups []*types.Group) parityRow {
		req := func(at types.AttributeType) *types.Request {
			return &types.Request{
				Attributes:   []*types.Attribute{{Type: at, Field: field, Label: "derived"}},
				Aggregations: []*types.Aggregation{sumAgg("total", "derived"), {Type: types.AGG_MAX, Field: "derived", Label: "hi"}},
				Groups:       groups,
			}
		}
		return parityRow{name: name, builtin: req(b), extension: req(e)}
	}
	// A two-pass attribute alongside a grouper runs buffered and serial.
	nonStreaming := func(r parityRow) parityRow { r.bufferedOnly, r.serialOnly = true, true; return r }
	serial := func(r parityRow) parityRow { r.serialOnly = true; return r }
	// ATTR_SET_POPCOUNT is outside the built-in FORMULA / DATE_PART
	// merge set, but a row_local extension attribute merges.
	rowLocal := func(r parityRow) parityRow { r.serialOnly, r.extMerges = true, true; return r }
	byRegion := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	z, pc := types.ATTR_ZSCORE, types.ATTR_SET_POPCOUNT
	return paritySuite{
		name: "attribute",
		register: func(probe *parityProbe) pulse.Extensions {
			return pulse.Extensions{Attributes: []pulse.AttributeRegistration{
				{
					Name: attrParityZScore, Mode: pulse.AttributeModeTwoPass,
					Description: "Test-only extend reimplementation of ATTR_ZSCORE.",
					Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
						return &parityZScore{probe: probe}, nil
					},
					FieldInputs: func(json.RawMessage) []string { return nil },
				},
				{
					Name: attrParityPopcount, Mode: pulse.AttributeModeRowLocal,
					Description: "Test-only extend reimplementation of ATTR_SET_POPCOUNT.",
					Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
						return &parityPopcount{probe: probe}, nil
					},
					FieldInputs: func(json.RawMessage) []string { return nil },
				},
			}}
		},
		rows: []parityRow{
			serial(row("two_pass_nullable_f64", z, attrParityZScore, "score", nil)),
			serial(row("two_pass_u32", z, attrParityZScore, "qty", nil)),
			nonStreaming(row("two_pass_grouped", z, attrParityZScore, "score", byRegion)),
			rowLocal(row("row_local_narrow_set", pc, attrParityPopcount, "tags", nil)),
			rowLocal(row("row_local_wide_u128_nullable", pc, attrParityPopcount, "w128", byRegion)),
			rowLocal(row("row_local_wide_u256", pc, attrParityPopcount, "w256", byRegion)),
		},
	}
}

// ---------------------------------------------------------------------
// Feature: FEAT_PARITY_LOG, an extend reimplementation of FEAT_LOG
// (log1p with null propagation, streaming via EmitRow).
// ---------------------------------------------------------------------

const featParityLog types.FeatureType = "FEAT_PARITY_LOG"

type parityLog struct {
	probe *parityProbe
	label string
}

func parityLogValue(rec extend.Record, field string) (float64, bool) {
	v, ok := rec.NumericValue(field)
	if !ok || v <= -1 {
		return 0, true
	}
	return math.Log1p(v), false
}

func (c *parityLog) Compute(rows extend.Rows, field string) (map[string]extend.FeatureOutput, error) {
	c.probe.buffered.Add(1)
	vals, nulls := make([]float64, rows.Len()), make([]bool, rows.Len())
	for i := range vals {
		vals[i], nulls[i] = parityLogValue(rows.At(i), field)
	}
	return map[string]extend.FeatureOutput{c.label: {Values: vals, Nulls: nulls}}, nil
}

func (c *parityLog) PrePass(extend.Record, string) error { return nil }
func (c *parityLog) Finalize() error                     { return nil }

func (c *parityLog) EmitRow(rec extend.Record, field string) (map[string]extend.FeatureOutput, error) {
	c.probe.online.Add(1)
	v, null := parityLogValue(rec, field)
	return map[string]extend.FeatureOutput{c.label: {Values: []float64{v}, Nulls: []bool{null}}}, nil
}

var _ extend.StreamingFeatureComputer = (*parityLog)(nil)

func featureParitySuite() paritySuite {
	row := func(name, field string, groups []*types.Group) parityRow {
		req := func(ft types.FeatureType) *types.Request {
			return &types.Request{
				Features: []*types.Feature{{Type: ft, Field: field, Label: "lg"}},
				Aggregations: []*types.Aggregation{sumAgg("total", "lg"),
					{Type: types.AGG_COUNT, Field: "lg", Label: "n"}},
				Groups: groups,
			}
		}
		return parityRow{name: name, builtin: req(types.FEAT_LOG), extension: req(featParityLog), serialOnly: true}
	}
	byRegion := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	return paritySuite{
		name: "feature",
		register: func(probe *parityProbe) pulse.Extensions {
			return pulse.Extensions{Features: []pulse.FeatureRegistration{{
				Name: featParityLog, Streamable: true,
				Description: "Test-only extend reimplementation of FEAT_LOG.",
				Factory: func(spec *types.Feature, _ *encoding.Schema) (extend.FeatureComputer, error) {
					label := spec.Label
					if label == "" {
						label = "LOG_" + spec.Field
					}
					return &parityLog{probe: probe, label: label}, nil
				},
				FieldInputs: func(json.RawMessage) []string { return nil },
			}}}
		},
		rows: []parityRow{
			row("nullable_f64", "score", nil),
			row("u32", "qty", nil),
			row("grouped", "score", byRegion),
		},
	}
}

// ---------------------------------------------------------------------
// Window: WIN_PARITY_LAG, an extend reimplementation of WIN_LAG.
// ---------------------------------------------------------------------

const winParityLag types.WindowType = "WIN_PARITY_LAG"

type parityLag struct {
	field  string
	offset int
	hasDef bool
	def    any
}

func (c *parityLag) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, idx := range part {
			var v any
			if src := i - c.offset; src >= 0 && src < len(part) {
				v = rows[part[src]][c.field]
			} else if c.hasDef {
				v = c.def
			}
			rows[idx][label] = v
		}
	}
	return nil
}

func windowParitySuite() paritySuite {
	row := func(name string, params string, partition []string) parityRow {
		req := func(wt types.WindowType) *types.Request {
			w := &types.Window{Type: wt, Field: "total", Label: "prev", PartitionBy: partition,
				OrderBy: []types.OrderKey{{Field: "qty"}}}
			if params != "" {
				w.Params = json.RawMessage(params)
			}
			// 13 qty buckets; the per-bucket count "n" takes two distinct
			// values, so partitioning by it yields two multi-row partitions.
			return &types.Request{
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
				Aggregations: []*types.Aggregation{sumAgg("total", "score"),
					{Type: types.AGG_COUNT, Field: "qty", Label: "n"}},
				Windows: []*types.Window{w},
			}
		}
		// Windows force the buffered, serial path (CanStreamRequest /
		// CanMergeRequest refuse them): the streaming and parallel cells
		// of the matrix degrade to buffered-serial on both arms.
		return parityRow{name: name, builtin: req(types.WIN_LAG), extension: req(winParityLag),
			bufferedOnly: true, serialOnly: true, mustContain: []string{`"prev":`}}
	}
	return paritySuite{
		name:   "window",
		noPath: true, // windows run over materialised result rows in every mode
		register: func(*parityProbe) pulse.Extensions {
			return pulse.Extensions{Windows: []pulse.WindowRegistration{{
				Name:        winParityLag,
				Description: "Test-only extend reimplementation of WIN_LAG.",
				Factory: func(spec *types.Window, _ extend.WindowOptions) (extend.WindowComputer, error) {
					c := &parityLag{field: spec.Field, offset: 1}
					if len(spec.Params) > 0 {
						var p struct {
							Offset  *int            `json:"offset"`
							Default json.RawMessage `json:"default"`
						}
						if err := json.Unmarshal(spec.Params, &p); err != nil {
							return nil, err
						}
						if p.Offset != nil {
							c.offset = *p.Offset
						}
						if len(p.Default) > 0 && string(p.Default) != "null" {
							if err := json.Unmarshal(p.Default, &c.def); err != nil {
								return nil, err
							}
							c.hasDef = true
						}
					}
					return c, nil
				},
				FieldInputs: func(json.RawMessage) []string { return nil },
			}}}
		},
		rows: []parityRow{
			row("lag_single_partition", "", nil),
			row("lag_partitioned", "", []string{"n"}),
			row("lag_offset_default", `{"offset":2,"default":-1}`, []string{"n"}),
		},
	}
}

// ---------------------------------------------------------------------
// Tests: TEST_PARITY_T (row tier: one-sample and Welch two-sample) and
// TEST_PARITY_PAIRED_T (post tier), extend reimplementations of TEST_T
// and the tier-2 TEST_PAIRED_T. The Student-t tail is the same
// Numerical Recipes continued fraction the engine uses, so results
// agree bit-for-bit.
// ---------------------------------------------------------------------

const (
	testParityT       types.TestType = "TEST_PARITY_T"
	testParityPairedT types.TestType = "TEST_PARITY_PAIRED_T"
)

type parityWelford struct {
	n        int64
	mean, m2 float64
}

func (b *parityWelford) add(v float64) {
	b.n++
	d := v - b.mean
	b.mean += d / float64(b.n)
	b.m2 += d * (v - b.mean)
}

func (b *parityWelford) variance() float64 {
	if b.n < 2 {
		return 0
	}
	return b.m2 / float64(b.n-1)
}

func parityStudentP(t, df float64) float64 {
	if df <= 0 || math.IsNaN(t) || math.IsNaN(df) {
		return math.NaN()
	}
	if math.IsInf(t, 0) {
		return 0
	}
	if t == 0 {
		return 1
	}
	x, a, b := df/(df+t*t), df/2, 0.5
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lga, _ := math.Lgamma(a)
	lgb, _ := math.Lgamma(b)
	lgab, _ := math.Lgamma(a + b)
	bt := math.Exp(lgab - lga - lgb + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return bt * parityBetaCF(a, b, x) / a
	}
	return 1 - bt*parityBetaCF(b, a, 1-x)/b
}

func parityBetaCF(a, b, x float64) float64 {
	const eps, tiny = 3e-15, 1e-300
	clamp := func(v float64) float64 {
		if math.Abs(v) < tiny {
			return tiny
		}
		return v
	}
	qab, qap, qam := a+b, a+1, a-1
	c, d := 1.0, 1/clamp(1-qab*x/qap)
	h := d
	for m := 1; m <= 200; m++ {
		mf, m2 := float64(m), float64(2*m)
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		d = 1 / clamp(1+aa*d)
		c = clamp(1 + aa/c)
		h *= d * c
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		d = 1 / clamp(1+aa*d)
		c = clamp(1 + aa/c)
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

func parityStudentInv(alpha, df float64) float64 {
	if df <= 0 || alpha <= 0 || alpha >= 1 {
		return math.NaN()
	}
	lo, hi := 0.0, 200.0
	for range 100 {
		mid := 0.5 * (lo + hi)
		if parityStudentP(mid, df) > alpha {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < 1e-9 {
			break
		}
	}
	return 0.5 * (lo + hi)
}

func parityAlpha(spec *types.Test) float64 {
	if spec.Alpha == 0 {
		return 0.05
	}
	return spec.Alpha
}

// parityTTest mirrors the TEST_T row test on the happy path (the
// insufficient-n / zero-variance refusals are out of the fixture's
// reach).
type parityTTest struct {
	spec   *types.Test
	mu     float64
	groups map[string]*parityWelford
	order  []string
}

func (tt *parityTTest) UpdateRow(rec extend.Record) error {
	v, ok := rec.NumericValue(tt.spec.Field)
	if !ok {
		return nil
	}
	key := ""
	if tt.spec.SplitBy != "" {
		if key, ok = rec.StringValue(tt.spec.SplitBy); !ok {
			return nil
		}
	}
	b, seen := tt.groups[key]
	if !seen {
		b = &parityWelford{}
		tt.groups[key] = b
		tt.order = append(tt.order, key)
	}
	b.add(v)
	return nil
}

func (tt *parityTTest) Finalize() (*types.TestResult, error) {
	alpha := parityAlpha(tt.spec)
	res := &types.TestResult{Label: tt.spec.Label, Type: tt.spec.Type, Alpha: alpha}
	if tt.spec.SplitBy == "" {
		b := tt.groups[""]
		variance := b.variance()
		sd := math.Sqrt(variance)
		se := sd / math.Sqrt(float64(b.n))
		res.Variant, res.Statistic, res.DF = "one_sample", (b.mean-tt.mu)/se, float64(b.n-1)
		res.PValue = parityStudentP(res.Statistic, res.DF)
		tcrit := parityStudentInv(alpha, res.DF)
		res.Details = map[string]any{"mu": tt.mu, "n": b.n, "mean": b.mean, "variance": variance,
			"ci_low": b.mean - tcrit*se, "ci_high": b.mean + tcrit*se}
	} else {
		keys := append([]string(nil), tt.order...)
		sort.Strings(keys)
		a, b := tt.groups[keys[0]], tt.groups[keys[1]]
		va, vb := a.variance(), b.variance()
		na, nb := float64(a.n), float64(b.n)
		se := math.Sqrt(va/na + vb/nb)
		diff := a.mean - b.mean
		num := va/na + vb/nb
		den := (va*va)/(na*na*(na-1)) + (vb*vb)/(nb*nb*(nb-1))
		res.Variant, res.Statistic, res.DF = "welch_two_sample", diff/se, (num*num)/den
		res.PValue = parityStudentP(res.Statistic, res.DF)
		tcrit := parityStudentInv(alpha, res.DF)
		pooled := math.Sqrt(((na-1)*va + (nb-1)*vb) / (na + nb - 2))
		var d float64
		if pooled > 0 {
			d = diff / pooled
		}
		res.Details = map[string]any{"groups": keys, "n": []int64{a.n, b.n},
			"mean": []float64{a.mean, b.mean}, "variance": []float64{va, vb}, "diff": diff,
			"ci_low": diff - tcrit*se, "ci_high": diff + tcrit*se,
			"effect_size": map[string]any{"cohens_d": d}}
	}
	res.RejectNull = res.PValue < alpha
	tt.groups, tt.order = map[string]*parityWelford{}, nil
	return res, nil
}

// parityPairedT mirrors the tier-2 TEST_PAIRED_T: a one-sample t on
// field − field2 across the result rows.
type parityPairedT struct{ spec *types.Test }

func (p parityPairedT) Run(rows []map[string]any) (*types.TestResult, error) {
	b := &parityWelford{}
	for _, row := range rows {
		x, _ := row[p.spec.Field].(float64)
		y, _ := row[p.spec.Field2].(float64)
		b.add(x - y)
	}
	alpha := parityAlpha(p.spec)
	variance := b.variance()
	sd := math.Sqrt(variance)
	se := sd / math.Sqrt(float64(b.n))
	t, df := b.mean/se, float64(b.n-1)
	pv := parityStudentP(t, df)
	tcrit := parityStudentInv(alpha, df)
	return &types.TestResult{
		Label: p.spec.Label, Type: p.spec.Type, Variant: "paired_two_sided_post",
		Statistic: t, DF: df, PValue: pv, Alpha: alpha, RejectNull: pv < alpha,
		Details: map[string]any{"n": b.n, "mean_diff": b.mean, "variance": variance,
			"ci_low": b.mean - tcrit*se, "ci_high": b.mean + tcrit*se,
			"effect_size": map[string]any{"cohens_d": b.mean / sd}},
	}, nil
}

var (
	_ extend.RowTest  = (*parityTTest)(nil)
	_ extend.PostTest = parityPairedT{}
)

func testParitySuite() paritySuite {
	rowTest := func(name string, test types.Test, filters []*types.Filterer) parityRow {
		req := func(tt types.TestType) *types.Request {
			c := test
			c.Type = tt
			return &types.Request{Aggregations: []*types.Aggregation{sumAgg("s", "score")},
				Tests: []*types.Test{&c}, Filterers: filters}
		}
		// Row tests stream but never merge: the parallel and per-shard
		// cells run serially on both arms.
		return parityRow{name: name, builtin: req(types.TEST_T), extension: req(testParityT), serialOnly: true,
			mustContain: []string{`"label":"t"`, `"p_value":`}}
	}
	post := func(name string, groups []*types.Group) parityRow {
		req := func(tt types.TestType) *types.Request {
			return &types.Request{
				Groups:       groups,
				Aggregations: []*types.Aggregation{sumAgg("s", "score"), sumAgg("q", "qty")},
				PostTests:    []*types.Test{{Type: tt, Field: "s", Field2: "q", Label: "paired"}},
			}
		}
		return parityRow{name: name, builtin: req(types.TEST_PAIRED_T), extension: req(testParityPairedT), serialOnly: true,
			mustContain: []string{`"label":"paired"`, `"mean_diff"`}}
	}
	twoRegions := []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "south"}}}
	return paritySuite{
		name:   "test",
		noPath: true, // row tests fold per row on both the streaming and the buffered pass
		rename: map[string]string{string(testParityT): string(types.TEST_T),
			string(testParityPairedT): string(types.TEST_PAIRED_T)},
		register: func(*parityProbe) pulse.Extensions {
			return pulse.Extensions{Tests: []pulse.TestRegistration{
				{
					Name: testParityT, Tier: pulse.TestTierRow, Streamable: true,
					Description: "Test-only extend reimplementation of TEST_T.",
					RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
						tt := &parityTTest{spec: spec, groups: map[string]*parityWelford{}}
						if len(spec.Params) > 0 {
							var p struct {
								Mu float64 `json:"mu"`
							}
							if err := json.Unmarshal(spec.Params, &p); err != nil {
								return nil, err
							}
							tt.mu = p.Mu
						}
						return tt, nil
					},
					FieldInputs: func(json.RawMessage) []string { return nil },
				},
				{
					Name: testParityPairedT, Tier: pulse.TestTierPost,
					Description: "Test-only extend reimplementation of the tier-2 TEST_PAIRED_T.",
					PostFactory: func(spec *types.Test, _ *encoding.Schema) (extend.PostTest, error) {
						return parityPairedT{spec: spec}, nil
					},
				},
			}}
		},
		rows: []parityRow{
			rowTest("one_sample", types.Test{Field: "score", Label: "t"}, nil),
			rowTest("one_sample_mu", types.Test{Field: "score", Label: "t", Params: json.RawMessage(`{"mu":20}`)}, nil),
			rowTest("one_sample_u32_alpha", types.Test{Field: "qty", Label: "t", Alpha: 0.01}, nil),
			rowTest("welch_two_sample", types.Test{Field: "score", SplitBy: "region", Label: "t"}, twoRegions),
			post("post_paired_by_region", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}),
			post("post_paired_by_day", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "day"}}),
		},
	}
}
