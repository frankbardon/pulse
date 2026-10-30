package integration_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// prevModeRows is the execution-mode matrix's long panel {id, day,
// plan}: clean three-wave sequences, a null state (subject 5's wave-2
// record is a gap), a differing tie (subject 6's day 1, its day-2
// record ambiguous), a null order value (subject 7) and a skipped wave
// (subject 9).
var prevModeRows = [][3]any{
	{uint64(1), int32(1), "free"}, {uint64(1), int32(2), "paid"}, {uint64(1), int32(3), "pro"},
	{uint64(2), int32(1), "free"}, {uint64(2), int32(2), "free"}, {uint64(2), int32(3), "paid"},
	{uint64(3), int32(1), "paid"}, {uint64(3), int32(2), "pro"},
	{uint64(4), int32(1), "pro"}, {uint64(4), int32(2), "free"}, {uint64(4), int32(3), "free"},
	{uint64(5), int32(1), nil}, {uint64(5), int32(2), "paid"}, {uint64(5), int32(3), "paid"},
	{uint64(6), int32(1), "free"}, {uint64(6), int32(1), "paid"}, {uint64(6), int32(2), "pro"}, {uint64(6), int32(3), "pro"},
	{uint64(7), nil, "free"}, {uint64(7), int32(2), "paid"},
	{uint64(8), int32(2), "paid"}, {uint64(8), int32(3), "free"},
	{uint64(9), int32(1), "pro"}, {uint64(9), int32(3), "pro"},
}

// prevModeShards splits prevModeRows into a shard archive whose shards
// hold LATER waves first — every subject's records span shards, and
// each shard's dictionary is built in its own first-seen order (the
// archive union-merges and remaps them): wave 3 (reversed), an empty
// shard, wave 2, then wave 1 with the null-order record.
func prevModeShards() [][][3]any {
	byDay := map[any][][3]any{}
	for _, r := range prevModeRows {
		byDay[r[1]] = append(byDay[r[1]], r)
	}
	w3 := slices.Clone(byDay[int32(3)])
	slices.Reverse(w3)
	return [][][3]any{w3, nil, byDay[int32(2)], append(byDay[int32(1)], byDay[nil]...)}
}

// prevModeKeep is the id window every matrix request carries: it keeps
// every panel record and drops the parallel-decode cohort's filler.
func prevModeKeep() *types.Filterer {
	return &types.Filterer{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", "1000"}}
}

// prevModeRequests is the matrix's request set over cohort: grouped by
// the previous state, ungrouped null count, the previous × state
// crosstab, and an expression filter reading the output by state name
// (grouped and as a crosstab).
func prevModeRequests(cohort string) map[string]func() *types.Request {
	base := func() *types.Request {
		return &types.Request{
			Cohort:     &types.Cohort{Filename: cohort},
			Attributes: prevStateAttr(),
			Filterers:  []*types.Filterer{prevModeKeep()},
		}
	}
	paid := func() *types.Filterer {
		return &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: `prev == "paid"`}
	}
	return map[string]func() *types.Request{
		"grouped_by_prev": func() *types.Request {
			r := base()
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "prev"}}
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan", Label: "n"}}
			return r
		},
		"ungrouped": func() *types.Request {
			r := base()
			r.Aggregations = []*types.Aggregation{
				{Type: types.AGG_NULL_COUNT, Field: "prev", Label: "null_prev"},
				{Type: types.AGG_COUNT, Field: "plan", Label: "n"},
			}
			return r
		},
		"crosstab": func() *types.Request {
			r := transitionCrosstab(cohort, "prev", "plan", prevStateAttr())
			r.Filterers = []*types.Filterer{prevModeKeep()}
			return r
		},
		"expr_filter_grouped": func() *types.Request {
			r := base()
			r.Filterers = append(r.Filterers, paid())
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}}
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan", Label: "n"}}
			return r
		},
		"expr_filter_crosstab": func() *types.Request {
			r := transitionCrosstab(cohort, "prev", "plan", prevStateAttr())
			r.Filterers = []*types.Filterer{prevModeKeep(), paid()}
			return r
		},
	}
}

// prevModeArm records the execution arm of the last operation an
// engine ran.
type prevModeArm struct {
	mu  sync.Mutex
	arm observe.Arm
}

func (a *prevModeArm) last() observe.Arm {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.arm
}

// prevModeEngine is a pulse instance over fs with opts and an
// arm-recording hook.
func prevModeEngine(t *testing.T, fs afero.Fs, opts pulse.Options) (*pulse.Pulse, *prevModeArm) {
	t.Helper()
	rec := &prevModeArm{}
	opts.FS = fs
	opts.Hooks = &observe.Hooks{OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, r observe.OperationResult) {
		rec.mu.Lock()
		rec.arm = r.Arm
		rec.mu.Unlock()
	}}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p, rec
}

// prevModeDoc is resp as a comparable JSON document: the whole response
// minus Metadata (the cohort filename), a Compose slot's label and Components.Run.ShardCount
// (the archive's shard count); counts=false also drops every figure the
// parallel-decode cohort's filtered-out filler moves (the attribute's
// and filterers' Components and Run, all over every decoded record).
func prevModeDoc(t *testing.T, resp *types.Response, counts bool) string {
	t.Helper()
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "metadata")
	delete(m, "label") // a Compose slot's echoed label
	if c, ok := m["components"].(map[string]any); ok {
		if run, ok := c["run"].(map[string]any); ok {
			delete(run, "shard_count")
		}
		if !counts {
			delete(c, "attributes")
			delete(c, "filterers")
			delete(c, "run")
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// prevModeRun runs req and returns its document.
func prevModeRun(t *testing.T, p *pulse.Pulse, req *types.Request, counts bool) string {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	return prevModeDoc(t, resp, counts)
}

// prevModeBaseline is the single-file buffered answer of every matrix
// request, after checking it is the expected shape (the tie warns, the
// crosstab keys by state name).
func prevModeBaseline(t *testing.T) (map[string]string, *pulse.Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, _ := prevModeEngine(t, fs, pulse.Options{ShardWorkers: 1, DecodeWorkers: 1})
	panelCohort(t, p, "panel.pulse", prevModeRows)
	want := map[string]string{}
	for name, mk := range prevModeRequests("panel.pulse") {
		want[name] = prevModeRun(t, p, mk(), true)
	}
	if !strings.Contains(want["crosstab"], "PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE") {
		t.Fatalf("baseline crosstab lacks the tie warning: %s", want["crosstab"])
	}
	if !strings.Contains(want["crosstab"], `"row_keys":[["free"],["paid"],["pro"]]`) {
		t.Fatalf("baseline crosstab not keyed by state name: %s", want["crosstab"])
	}
	return want, p, fs
}

// prevModeDecodeFiller is above the parallel-decode threshold (100,000
// records) so DecodeWorkers engages wherever the request admits it
// (TestPreviousStateInvariance_Heavy).
const prevModeDecodeFiller = 102_400

// TestPreviousState_EveryBufferedArmAgrees: on the single file, every
// knob that picks a different arm for other requests gives the same
// document — the default worker knobs, projected decode off, crosstab
// fusion off; the crosstab declines fusion (predict CrosstabFusable
// false with the buffered-attribute reason) and runs buffered;
// ProcessStream yields exactly the buffered rows and Components.
func TestPreviousState_EveryBufferedArmAgrees(t *testing.T) {
	t.Parallel()

	want, _, fs := prevModeBaseline(t)
	ctx := context.Background()
	for _, c := range []struct {
		name string
		opts pulse.Options
	}{
		{"default_knobs", pulse.Options{}},
		{"projection_off", pulse.Options{DisableProjection: true}},
		{"fusion_off", pulse.Options{DisableCrosstabFusion: true}},
	} {
		p, arm := prevModeEngine(t, fs, c.opts)
		for name, mk := range prevModeRequests("panel.pulse") {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				if got := prevModeRun(t, p, mk(), true); got != want[name] {
					t.Errorf("got\n%s\nwant\n%s", got, want[name])
				}
				if a := arm.last(); a != observe.ArmBuffered {
					t.Errorf("ran on arm %q, want buffered", a)
				}
				pr, err := p.Predict(ctx, mk())
				if err != nil {
					t.Fatal(err)
				}
				if pr.Streamable {
					t.Error("predict streamable")
				}
				if mk().Crosstab != nil && c.name == "default_knobs" {
					if pr.CrosstabFusable == nil || *pr.CrosstabFusable ||
						!slices.Contains(pr.CrosstabFusionReasons, "buffered attribute (ATTR_PREVIOUS_STATE)") {
						t.Errorf("predict fusable %v reasons %q, want declined as a buffered attribute", pr.CrosstabFusable, pr.CrosstabFusionReasons)
					}
				}
				if mk().Crosstab != nil {
					return
				}
				it, err := p.ProcessStream(ctx, mk())
				if err != nil {
					t.Fatal(err)
				}
				defer it.Close()
				var rows []map[string]any
				for {
					row, ok, err := it.Next(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if !ok {
						break
					}
					rows = append(rows, row)
				}
				resp, err := p.Process(ctx, mk())
				if err != nil {
					t.Fatal(err)
				}
				gotRows, _ := json.Marshal(rows)
				wantRows, _ := json.Marshal(resp.Data)
				if string(gotRows) != string(wantRows) {
					t.Errorf("stream rows %s, buffered %s", gotRows, wantRows)
				}
				gotC, _ := json.Marshal(it.Components())
				wantC, _ := json.Marshal(resp.Components)
				if string(gotC) != string(wantC) {
					t.Errorf("stream components %s, buffered %s", gotC, wantC)
				}
			})
		}
	}
}

// TestPreviousState_ComposeMatchesProcess: a Compose of every matrix
// request beside one without the attribute answers each slot exactly
// as Process does — the expression filter reading the output by name
// included — and the attribute-free slot is untouched by its
// neighbours.
func TestPreviousState_ComposeMatchesProcess(t *testing.T) {
	t.Parallel()

	want, p, _ := prevModeBaseline(t)
	plain := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "panel.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan", Label: "n"}},
		}
	}
	wantPlain := prevModeRun(t, p, plain(), true)
	for name, mk := range prevModeRequests("panel.pulse") {
		t.Run(name, func(t *testing.T) {
			comp, err := p.Compose(context.Background(), &pulse.ComposedRequest{Requests: []*types.Request{mk(), plain()}})
			if err != nil {
				t.Fatal(err)
			}
			if len(comp.Responses) != 2 {
				t.Fatalf("%d responses", len(comp.Responses))
			}
			if got := prevModeDoc(t, comp.Responses[0], true); got != want[name] {
				t.Errorf("compose slot\n%s\nprocess\n%s", got, want[name])
			}
			if got := prevModeDoc(t, comp.Responses[1], true); got != wantPlain {
				t.Errorf("attribute-free slot\n%s\nprocess\n%s", got, wantPlain)
			}
		})
	}
}

// TestPreviousState_OrderIsZoneIndependent: records ordered by a
// datetime across the America/New_York fall-back hour — 05:30Z (01:30
// EDT) then 06:10Z (01:10 EST, an EARLIER wall clock) — read the
// previous state by instant whatever zone the instance or request
// sets: the 06:10Z record's previous state is the 05:30Z one's, never
// the reverse a wall-clock order would give.
func TestPreviousState_OrderIsZoneIndependent(t *testing.T) {
	t.Parallel()

	at := func(s string) int64 {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts.Unix()
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, Description: "Customer identifier of the panel row"},
		{Name: "at", Type: encoding.FieldTypeDateTime, Description: "Instant the plan was observed"},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Description: "Subscription plan observed then"},
	}}
	rows := []pulse.CohortRow{
		{uint64(1), at("2024-11-03T05:30:00Z"), "free"},
		{uint64(1), at("2024-11-03T06:10:00Z"), "paid"},
		{uint64(2), at("2024-11-03T05:40:00Z"), "paid"},
		{uint64(2), at("2024-11-03T06:05:00Z"), "pro"},
	}
	req := func(tz string) *types.Request {
		r := transitionCrosstab("zoned.pulse", "prev", "plan",
			[]*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", SubjectField: "id", OrderField: "at", Label: "prev"}})
		r.TimeZone = tz
		return r
	}
	fs := afero.NewMemMapFs()
	var want string
	for _, c := range []struct{ name, defaultTZ, reqTZ string }{
		{"utc", "", ""},
		{"default_new_york", "America/New_York", ""},
		{"request_new_york", "", "America/New_York"},
		{"request_kiritimati", "Asia/Tokyo", "Pacific/Kiritimati"},
	} {
		p, _ := prevModeEngine(t, fs, pulse.Options{DefaultTimeZone: c.defaultTZ})
		if want == "" {
			bld, err := p.NewCohortBuilder(context.Background(), "zoned.pulse", schema, pulse.CohortBuilderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if err := bld.Append(r); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := bld.Close(); err != nil {
				t.Fatal(err)
			}
		}
		resp, err := p.Process(context.Background(), req(c.reqTZ))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		m := resp.Crosstab.Matrix
		cells, _ := json.Marshal([]any{m.RowKeys, m.ColumnKeys, m.Cells})
		if want == "" {
			want = string(cells)
			// Instant order: free → paid (subject 1), paid → pro
			// (subject 2); New York wall order would give paid → free
			// and pro → paid.
			const byInstant = `[[["free"],["paid"]],[["free"],["paid"],["pro"]],` +
				`[[{"present":false},{"value":1,"present":true},{"present":false}],` +
				`[{"present":false},{"present":false},{"value":1,"present":true}]]]`
			if want != byInstant {
				t.Fatalf("utc crosstab %s, want %s", want, byInstant)
			}
		}
		if string(cells) != want {
			t.Errorf("%s: crosstab %s, want the UTC answer %s", c.name, cells, want)
		}
	}
}
