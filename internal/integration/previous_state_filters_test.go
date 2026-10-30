package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// prevStateWaves is one subject's plan at three waves (epoch days 1, 2,
// 3); nil is a null state.
var prevStateWaves = [][3]any{
	{"free", "paid", "paid"}, {"free", "free", "pro"}, {"paid", "pro", "free"},
	{"pro", "free", "free"}, {"paid", "paid", "pro"}, {nil, "paid", "free"},
	{"pro", "pro", "pro"}, {"free", "paid", nil},
}

// prevStateWavesLongAndWide writes the waves as a long panel
// (waves.pulse: id, seen, plan, x — three rows per subject, the waves
// interleaved across subjects) and its wide twin (waves_wide.pulse: id,
// plan_t1..plan_t3 — one row per subject).
func prevStateWavesLongAndWide(t *testing.T, opts pulse.Options) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	var long [][3]any
	for wave := range 3 {
		for i, w := range prevStateWaves {
			long = append(long, [3]any{uint64(i + 1), int32(wave + 1), w[wave]})
		}
	}
	panelCohort(t, p, "waves.pulse", long)

	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, Description: "Customer identifier of the wide row"},
		{Name: "plan_t1", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan at the first wave"},
		{Name: "plan_t2", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan at the second wave"},
		{Name: "plan_t3", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan at the third wave"},
	}}
	bld, err := p.NewCohortBuilder(context.Background(), "waves_wide.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range prevStateWaves {
		if err := bld.Append(pulse.CohortRow{uint64(i + 1), w[0], w[1], w[2]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bld.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fs
}

// dayWindow is a FILTER_DATE_RANGES slot on seen keeping the one epoch
// day day (1970-01-01 + day) — wave day.
func dayWindow(t *testing.T, day int) *types.Filterer {
	t.Helper()
	d := "1970-01-0" + string(rune('1'+day))
	params, err := json.Marshal(map[string]any{"ranges": []map[string]any{{"label": "window", "start": d, "end": d}}})
	if err != nil {
		t.Fatal(err)
	}
	return &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "seen", Params: params}
}

// TestPreviousState_DateWindowKeepsFirstInWindowTransition: a date
// window keeping one wave of the long panel still gives each subject's
// in-window record its TRUE previous state (the wave before, outside
// the window) — the attribute is computed over the whole cohort before
// the filter — so the previous × state crosstab equals the wide
// plan_t(k-1) × plan_t(k) crosstab cell for cell and label for label,
// for each window. Computed after the filter, every in-window record
// would be its subject's first and the grid would be empty.
func TestPreviousState_DateWindowKeepsFirstInWindowTransition(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	for _, tc := range []struct {
		day      int
		from, to string
	}{{2, "plan_t1", "plan_t2"}, {3, "plan_t2", "plan_t3"}} {
		req := transitionCrosstab("waves.pulse", "prev", "plan", prevStateAttr())
		req.Filterers = []*types.Filterer{dayWindow(t, tc.day)}
		long, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		wide, err := p.Process(ctx, transitionCrosstab("waves_wide.pulse", tc.from, tc.to, nil))
		if err != nil {
			t.Fatal(err)
		}
		lm, wm := long.Crosstab.Matrix, wide.Crosstab.Matrix
		if len(lm.RowKeys) == 0 {
			t.Fatalf("day %d: empty previous-state axis — computed after the filter", tc.day)
		}
		if !reflect.DeepEqual(lm.RowKeys, wm.RowKeys) || !reflect.DeepEqual(lm.ColumnKeys, wm.ColumnKeys) {
			t.Fatalf("day %d: axes long %v x %v, wide %v x %v", tc.day, lm.RowKeys, lm.ColumnKeys, wm.RowKeys, wm.ColumnKeys)
		}
		if !reflect.DeepEqual(lm.Cells, wm.Cells) {
			t.Fatalf("day %d: cells differ:\nlong %v\nwide %v", tc.day, lm.Cells, wm.Cells)
		}
		// The grouped (non-crosstab) buffered arm reads the same column.
		grouped := &types.Request{
			Cohort:       &types.Cohort{Filename: "waves.pulse"},
			Attributes:   prevStateAttr(),
			Filterers:    []*types.Filterer{dayWindow(t, tc.day)},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "prev"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan", Label: "n"}},
		}
		gresp, err := p.Process(ctx, grouped)
		if err != nil {
			t.Fatal(err)
		}
		var total float64
		for _, row := range gresp.Data {
			total += row["n"].(float64)
		}
		var cells float64
		for _, r := range wm.Cells {
			for _, c := range r {
				cells += c.Scalar()
			}
		}
		if total != cells {
			t.Errorf("day %d: grouped arm counts %v transitions, wide crosstab %v", tc.day, total, cells)
		}
	}
}

// TestPreviousState_ExpressionFilterReadsOutput: an expression filter
// may read the previous-state column — `prev != nil` drops each
// subject's first record (and the gap / null rows) — on predict and at
// runtime alike; the filter's Components count the filter itself
// (n_in = every record, n_out = the survivors), Run keeps the pre-filter
// total, and the attribute's floor counts every record it scored.
func TestPreviousState_ExpressionFilterReadsOutput(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	mk := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "waves.pulse"},
			Attributes:   prevStateAttr(),
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "prev != nil"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}, {Type: types.AGG_NULL_COUNT, Field: "prev", Label: "null_prev"}},
		}
	}
	pr, err := p.Predict(ctx, mk())
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Valid || len(pr.Errors) > 0 {
		t.Fatalf("predict refused the expression filter: %+v", pr.Errors)
	}
	if !reflect.DeepEqual(pr.ComputedBeforeFilters, []string{"prev"}) {
		t.Errorf("predict computed_before_filters %v, want [prev]", pr.ComputedBeforeFilters)
	}
	resp, err := p.Process(ctx, mk())
	if err != nil {
		t.Fatal(err)
	}
	// 24 records: 8 firsts and subject 6's wave-2 gap (its wave-1 state
	// is null) have no previous state → 15 kept.
	const total, kept = 24, 15
	row := resp.Data[0]
	if row["n"] != float64(kept) || row["null_prev"] != 0.0 {
		t.Fatalf("data %+v, want n=%d null_prev=0", row, kept)
	}
	c := resp.Components
	if c == nil || len(c.Filterers) != 1 || c.Run == nil || len(c.Attributes) != 1 {
		t.Fatalf("components %+v", c)
	}
	if f := c.Filterers[0]; f.NIn != total || f.NOut != kept {
		t.Errorf("filter components n_in=%d n_out=%d, want %d / %d", f.NIn, f.NOut, total, kept)
	}
	if c.Run.TotalRecords != total || c.Run.FilteredRecords != kept {
		t.Errorf("run total=%d filtered=%d, want %d / %d", c.Run.TotalRecords, c.Run.FilteredRecords, total, kept)
	}
	if a := c.Attributes[0]; a.N != kept || a.NNull != total-kept {
		t.Errorf("attribute floor n=%d n_null=%d, want %d / %d (every record it scored)", a.N, a.NNull, kept, total-kept)
	}
}

// TestPreviousState_FieldFilterOnOutputRefused: only an expression
// filter reads the output column; a field-keyed filter naming it is the
// field-reference walk's unknown-field refusal, on predict and at
// runtime alike.
func TestPreviousState_FieldFilterOnOutputRefused(t *testing.T) {
	t.Parallel()

	p, fs := prevStateWavesLongAndWide(t, pulse.Options{})
	mk := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "waves.pulse"},
			Attributes:   prevStateAttr(),
			Filterers:    []*types.Filterer{{Type: types.FILTER_NULL, Field: "prev"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan"}},
		}
	}
	if ce := assertPredictMatchesRefusal(t, p, fs, "waves.pulse", mk); ce.Code != errors.SERVICE_VALIDATION {
		t.Errorf("code %s (%s), want %s", ce.Code, ce.Message, errors.SERVICE_VALIDATION)
	}
}

// TestPreviousState_OtherAttributesRunAfterFilters: every other
// attribute still runs over the filter-passing records — an
// ATTR_PERCENTILE beside the previous-state slot ranks only the kept
// wave, exactly as it does without the previous-state slot — and the
// attribute warnings / Components keep declared slot order.
func TestPreviousState_OtherAttributesRunAfterFilters(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	pct := &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "x", Label: "pct"}
	mk := func(withPrev bool) *types.Request {
		attrs := []*types.Attribute{pct}
		if withPrev {
			attrs = append(attrs, prevStateAttr()...)
		}
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "waves.pulse"},
			Attributes:   attrs,
			Filterers:    []*types.Filterer{dayWindow(t, 2)},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "pct", Label: "s"}, {Type: types.AGG_MIN, Field: "pct", Label: "lo"}},
		}
	}
	without, err := p.Process(ctx, mk(false))
	if err != nil {
		t.Fatal(err)
	}
	with, err := p.Process(ctx, mk(true))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(without.Data, with.Data) {
		t.Fatalf("ATTR_PERCENTILE moved beside a previous-state slot: %v vs %v", without.Data, with.Data)
	}
	// A Components-bearing post-filter attribute (ATTR_SCALE_SCORE)
	// declared before the previous-state slots: entries follow the
	// declared order, and its floor counts the filter-passing records
	// alone while a previous-state floor counts every record.
	req := mk(true)
	req.Attributes = []*types.Attribute{
		{Type: types.ATTR_SCALE_SCORE, Fields: []string{"x", "id"}, Label: "sc"},
		{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", SubjectField: "id", OrderField: "seen", Label: "prev2"},
		pct,
		prevStateAttr()[0],
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	scored := map[string]int{}
	for _, a := range resp.Components.Attributes {
		labels = append(labels, a.Label)
		scored[a.Label] = a.N + a.NNull
	}
	if !reflect.DeepEqual(labels, []string{"sc", "prev2", "prev"}) {
		t.Errorf("attribute components order %v, want declared [sc prev2 prev]", labels)
	}
	if want := map[string]int{"sc": 8, "prev2": 24, "prev": 24}; !reflect.DeepEqual(scored, want) {
		t.Errorf("records scored %v, want %v", scored, want)
	}
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pr.ComputedBeforeFilters, []string{"prev2", "prev"}) {
		t.Errorf("predict computed_before_filters %v, want [prev2 prev]", pr.ComputedBeforeFilters)
	}
	if pr0, err := p.Predict(ctx, mk(false)); err != nil || pr0.ComputedBeforeFilters != nil {
		t.Errorf("no previous-state slot: computed_before_filters %v (err %v), want absent", pr0.ComputedBeforeFilters, err)
	}
}

// TestPreviousState_ComposeSlotUnderDateWindow: a Compose slot runs the
// same pipeline — its date-windowed previous-state crosstab equals the
// stand-alone Process.
func TestPreviousState_ComposeSlotUnderDateWindow(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	mk := func() *types.Request {
		req := transitionCrosstab("waves.pulse", "prev", "plan", prevStateAttr())
		req.Filterers = []*types.Filterer{dayWindow(t, 2)}
		return req
	}
	solo, err := p.Process(ctx, mk())
	if err != nil {
		t.Fatal(err)
	}
	comp, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{mk()}})
	if err != nil {
		t.Fatal(err)
	}
	if got := comp.Responses[0].Crosstab.Matrix; !reflect.DeepEqual(got.Cells, solo.Crosstab.Matrix.Cells) || len(got.RowKeys) == 0 {
		t.Fatalf("compose cells %v (rows %v), process %v", got.Cells, got.RowKeys, solo.Crosstab.Matrix.Cells)
	}
}

// TestPreviousState_LimitsJudgeUnfilteredBuffer: the instance memory
// limit judges the unfiltered buffer the attribute reads — the estimate
// of a one-wave window equals the unfiltered request's — and the
// runtime refuses exactly when predict reports a certain finding.
func TestPreviousState_LimitsJudgeUnfilteredBuffer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mk := func(filtered bool) *types.Request {
		req := transitionCrosstab("waves.pulse", "prev", "plan", prevStateAttr())
		if filtered {
			req.Filterers = []*types.Filterer{dayWindow(t, 2)}
		}
		return req
	}
	estimate := func(filtered bool) int64 {
		p, _ := prevStateWavesLongAndWide(t, pulse.Options{Limits: pulse.Limits{MaxEstimatedMemory: 1}})
		pr, err := p.Predict(ctx, mk(filtered))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range pr.LimitFindings {
			if f.Limit == "max_estimated_memory" {
				return f.Estimated
			}
		}
		t.Fatalf("no max_estimated_memory finding in %+v", pr.LimitFindings)
		return 0
	}
	est := estimate(true)
	if unf := estimate(false); est != unf {
		t.Fatalf("filtered estimate %d, unfiltered %d: the limit must judge the unfiltered buffer", est, unf)
	}
	for _, lim := range []int64{est - 1, est} {
		p, _ := prevStateWavesLongAndWide(t, pulse.Options{Limits: pulse.Limits{MaxEstimatedMemory: lim}})
		pr, err := p.Predict(ctx, mk(true))
		if err != nil {
			t.Fatal(err)
		}
		_, perr := p.Process(ctx, mk(true))
		var ce *errors.CodedError
		refused := stderrors.As(perr, &ce) && ce.Code == errors.PULSE_LIMIT_EXCEEDED
		if perr != nil && !refused {
			t.Fatalf("limit %d: unexpected error %v", lim, perr)
		}
		if refused != !pr.Valid || refused != (lim < est) {
			t.Errorf("limit %d (estimate %d): runtime refused=%v, predict valid=%v", lim, est, refused, pr.Valid)
		}
	}
}
