package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// window_formula_test.go pins WIN_FORMULA (#479): an expression over a
// row's columns and earlier windows' outputs, judged by the ONE
// field-reference walk on both sides and compiled by internal/winformula
// for predict and the engine alike.

const winFormulaRecords = 600

// winFormulaRec is fixture record i: decile d (0..4), outcome y (0/1),
// value x and a nullable w (null on every seventh record).
func winFormulaRec(i int) (d uint64, y, x float64, w any) {
	d = uint64(i % 5)
	if (i*7+int(d))%(int(d)+2) == 0 {
		y = 1
	}
	x = float64(i%13) + 0.25
	if i%7 != 0 {
		w = float64(i%11) - 3
	}
	return d, y, x, w
}

// winFormulaCohort writes the fixture as gains.pulse (a shard archive of
// shards shards when shards > 0) on an instance built from opts.
func winFormulaCohort(t *testing.T, opts pulse.Options, shards int) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	if opts.FS == nil {
		opts.FS = afero.NewMemMapFs()
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeU8, Description: "Score decile the account falls in"},
		{Name: "y", Type: encoding.FieldTypeF64, Description: "Whether the account defaulted, one or zero"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Balance outstanding on the account"},
		{Name: "w", Type: encoding.FieldTypeF64, Nullable: true, Description: "Change in balance since last period"},
	}}
	bo := pulse.CohortBuilderOptions{}
	if shards > 0 {
		bo.Shards = &pulse.ShardSplit{MaxRecords: winFormulaRecords / shards}
	}
	b, err := p.NewCohortBuilder(context.Background(), "gains.pulse", schema, bo)
	if err != nil {
		t.Fatal(err)
	}
	for i := range winFormulaRecords {
		d, y, x, w := winFormulaRec(i)
		if err := b.Append(pulse.CohortRow{d, y, x, w}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, opts.FS
}

func winFormulaWindows(raw string) []*types.Window {
	var ws []*types.Window
	if err := json.Unmarshal([]byte(raw), &ws); err != nil {
		panic(err)
	}
	return ws
}

// wfGainsReq is the grouped gains table: per decile the event count and
// the account count, each column's share, then windows.
func wfGainsReq(windows string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "gains.pulse"},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "d"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "y", Label: "events"},
			{Type: types.AGG_COUNT, Field: "y", Label: "n"},
		},
		Params:  types.Params{"pct": json.Number("100")},
		Windows: winFormulaWindows(windows),
	}
}

const wfGainsWindows = `[
	{"type": "WIN_SHARE", "field": "events", "label": "s_events"},
	{"type": "WIN_SHARE", "field": "n", "label": "s_n"},
	{"type": "WIN_FORMULA", "label": "lift", "params": {"expression": "s_events / s_n"}},
	{"type": "WIN_FORMULA", "label": "rate", "params": {"expression": "events / n"}},
	{"type": "WIN_FORMULA", "label": "rate_pct", "params": {"expression": "rate * params.pct"}},
	{"type": "WIN_LIFT", "field": "events", "label": "lift_ref", "params": {"base": "n"}}
]`

func cellNumber(t *testing.T, row map[string]any, key string) float64 {
	t.Helper()
	switch v := row[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case uint64:
		return float64(v)
	}
	t.Fatalf("row %v: %s = %T %v, want a number", row, key, row[key], row[key])
	return 0
}

// TestWinFormula_GainsRatioMatchesOracle: a ratio of two WIN_SHARE
// outputs is the decile's lift — equal to the hand-computed
// (events/Σevents) / (n/Σn) over the fixture, and to WIN_LIFT's own
// figure to the bit; a formula over the aggregation columns is the
// decile's event rate, and one reading params.pct folds the request's
// params. Serial, parallel-decode, serial and parallel
// shard-archive runs are bit-identical.
func TestWinFormula_GainsRatioMatchesOracle(t *testing.T) {
	t.Parallel()

	var events, n [5]float64
	var totalEvents, totalN float64
	for i := range winFormulaRecords {
		d, y, _, _ := winFormulaRec(i)
		events[d] += y
		n[d]++
		totalEvents += y
		totalN++
	}
	ctx := context.Background()
	var ref string
	for _, arm := range []struct {
		name   string
		opts   pulse.Options
		shards int
	}{
		{"serial", pulse.Options{DecodeWorkers: 1}, 0},
		{"parallel decode", pulse.Options{DecodeWorkers: 4}, 0},
		{"archive serial", pulse.Options{ShardWorkers: 1}, 4},
		{"archive parallel", pulse.Options{ShardWorkers: 4}, 4},
	} {
		p, _ := winFormulaCohort(t, arm.opts, arm.shards)
		resp, err := p.Process(ctx, wfGainsReq(wfGainsWindows))
		if err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		if len(resp.Data) != 5 {
			t.Fatalf("%s: %d rows, want 5", arm.name, len(resp.Data))
		}
		for _, row := range resp.Data {
			ev, cnt := cellNumber(t, row, "events"), cellNumber(t, row, "n")
			d := -1
			for k := range n {
				if n[k] == cnt && events[k] == ev {
					d = k
				}
			}
			if d < 0 {
				t.Fatalf("%s: row %v matches no decile", arm.name, row)
			}
			want := (events[d] / totalEvents) / (n[d] / totalN)
			lift := cellNumber(t, row, "lift")
			if math.Abs(lift-want) > 1e-12 {
				t.Errorf("%s decile %d: lift %v, want %v", arm.name, d, lift, want)
			}
			if math.Float64bits(lift) != math.Float64bits(cellNumber(t, row, "lift_ref")) {
				t.Errorf("%s decile %d: WIN_FORMULA lift %v, WIN_LIFT %v", arm.name, d, lift, row["lift_ref"])
			}
			rate := cellNumber(t, row, "rate")
			if math.Abs(rate-events[d]/n[d]) > 1e-15 {
				t.Errorf("%s decile %d: rate %v, want %v", arm.name, d, rate, events[d]/n[d])
			}
			// The request params fold into the expression.
			if got := cellNumber(t, row, "rate_pct"); got != rate*100 {
				t.Errorf("%s decile %d: rate_pct %v, want %v", arm.name, d, got, rate*100)
			}
		}
		b, err := json.Marshal(resp.Data)
		if err != nil {
			t.Fatal(err)
		}
		if ref == "" {
			ref = string(b)
		} else if string(b) != ref {
			t.Errorf("%s rows differ from serial:\n%s\n%s", arm.name, b, ref)
		}
	}
}

// TestWinFormula_RefusalsPredictMatchRuntime: each refusal predict
// reports for a WIN_FORMULA expression — a forward reference (a later
// window's label, or its own), an unknown name, a record column the
// grouped rows do not carry, a result that is no number, ref(), an
// undeclared param, an undeclared function, a syntax error, a literal
// range_label() table the instance does not register — is the first
// error of the predict envelope, and Process and ProcessStream
// (and, over the record rows, Materialize) raise exactly it: code,
// message and details.
func TestWinFormula_RefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()
	p, fsys := winFormulaCohort(t, pulse.Options{}, 0)
	ctx := context.Background()
	cases := []struct {
		name    string
		windows string
		code    errors.Code
		reason  string
	}{
		{"later window", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "s_n * 2"}}, {"type": "WIN_SHARE", "field": "n", "label": "s_n"}]`, errors.PULSE_WINDOW_INVALID, "forward_reference"},
		{"own output", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "f + 1"}}]`, errors.PULSE_WINDOW_INVALID, "forward_reference"},
		{"unknown name", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "nope + events"}}]`, errors.SERVICE_VALIDATION, ""},
		{"record column on grouped rows", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "x / n"}}]`, errors.SERVICE_VALIDATION, "window_over_output_rows"},
		{"bool result", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "events > n"}}]`, errors.PULSE_FORMULA_COMPILE_ERROR, ""},
		{"string result", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "\"high\""}}]`, errors.PULSE_FORMULA_COMPILE_ERROR, ""},
		{"ref call", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "ref(\"t\", \"k\") + n"}}]`, errors.PULSE_FORMULA_COMPILE_ERROR, ""},
		{"undeclared param", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "events * params.k"}}]`, errors.PULSE_PARAMS_UNDECLARED, ""},
		{"coef undeclared", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "coef(\"m\", \"x\") * n"}}]`, errors.PULSE_FORMULA_COMPILE_ERROR, ""},
		{"syntax error", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "events +"}}]`, errors.PULSE_FORMULA_COMPILE_ERROR, ""},
		{"unknown range table", `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "range_label(\"nope\", n) == nil ? 0 : 1"}}]`, errors.PULSE_RANGE_TABLE_UNKNOWN, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := predictEnvelope(t, p, fsys, "gains.pulse", wfGainsReq(tc.windows))
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("predict errors = %+v, want %s first", env.Errors, tc.code)
			}
			if tc.reason != "" && env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict details = %v, want reason %s", env.Errors[0].Details, tc.reason)
			}
			_, perr := p.Process(ctx, wfGainsReq(tc.windows))
			sameEntry(t, env, perr)
			_, serr := p.ProcessStream(ctx, wfGainsReq(tc.windows))
			sameEntry(t, env, serr)
		})
	}

	// Over the record rows, on Materialize too.
	for i, windows := range []string{
		`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "g * 2"}}, {"type": "WIN_FORMULA", "label": "g", "params": {"expression": "x"}}]`,
		`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "x > w"}}]`,
		`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "events / x"}}]`,
	} {
		env := predictEnvelope(t, p, fsys, "gains.pulse", recordFormulaReq(windows))
		_, perr := p.Process(ctx, recordFormulaReq(windows))
		sameEntry(t, env, perr)
		_, merr := p.Materialize(ctx, recordFormulaReq(windows), fmt.Sprintf("wf_bad%d.pulse", i), pulse.MaterializeOptions{})
		sameEntry(t, env, merr)
	}
}

// recordFormulaReq runs windows over the record rows (nothing groups or
// aggregates).
func recordFormulaReq(windows string) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "gains.pulse"}, Windows: winFormulaWindows(windows)}
}

// TestWinFormula_NullInputReadsNull: over the record rows, a row whose
// expression input is null reads null — a guard such as `w ?? 0` never
// sees the nil — and every other row is the expression's value; a
// zero divisor (a non-finite result) reads null, never Inf.
func TestWinFormula_NullInputReadsNull(t *testing.T) {
	t.Parallel()
	p, _ := winFormulaCohort(t, pulse.Options{}, 0)
	resp, err := p.Process(context.Background(), recordFormulaReq(`[
		{"type": "WIN_FORMULA", "label": "w2", "params": {"expression": "w * 2 + x"}},
		{"type": "WIN_FORMULA", "label": "guarded", "params": {"expression": "(w ?? 0) + 1"}},
		{"type": "WIN_FORMULA", "label": "inv", "params": {"expression": "1 / (w2 - x - w * 2)"}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != winFormulaRecords {
		t.Fatalf("%d rows, want %d", len(resp.Data), winFormulaRecords)
	}
	nulls := 0
	for _, row := range resp.Data {
		x := cellNumber(t, row, "x")
		wv, present := row["w"].(float64)
		if !present {
			nulls++
			if row["w2"] != nil || row["guarded"] != nil {
				t.Errorf("null w: w2 %v, guarded %v, want null", row["w2"], row["guarded"])
			}
			continue
		}
		if got := cellNumber(t, row, "w2"); got != wv*2+x {
			t.Errorf("w %v x %v: w2 = %v, want %v", wv, x, got, wv*2+x)
		}
		if got := cellNumber(t, row, "guarded"); got != wv+1 {
			t.Errorf("w %v: guarded = %v, want %v", wv, got, wv+1)
		}
		if row["inv"] != nil {
			t.Errorf("1/0 read %v, want null", row["inv"])
		}
	}
	if nulls == 0 {
		t.Fatal("fixture has no null w")
	}
}

// TestWinFormula_WeightNeutral: an instance default weight is skipped
// by a window (the record-row formula reads exactly the unweighted
// figures), and an explicit request weight is refused by predict and
// the runtime alike.
func TestWinFormula_WeightNeutral(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	windows := `[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "x * 3 - 1"}}]`
	plain, fsys := winFormulaCohort(t, pulse.Options{}, 0)
	weighted, _ := winFormulaCohort(t, pulse.Options{DefaultWeight: &types.WeightSpec{Field: "x"}}, 0)
	a, err := plain.Process(ctx, recordFormulaReq(windows))
	if err != nil {
		t.Fatal(err)
	}
	b, err := weighted.Process(ctx, recordFormulaReq(windows))
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a.Data)
	jb, _ := json.Marshal(b.Data)
	if string(ja) != string(jb) {
		t.Errorf("default weight moved the formula:\n%s\n%s", jb, ja)
	}

	req := func() *types.Request {
		r := recordFormulaReq(windows)
		r.Weight = &types.WeightSpec{Field: "x"}
		return r
	}
	env := predictEnvelope(t, plain, fsys, "gains.pulse", req())
	_, perr := plain.Process(ctx, req())
	var ce *errors.CodedError
	if !stderrors.As(perr, &ce) || ce.Code != errors.PROCESSING_CONFIG {
		t.Fatalf("explicit weight: runtime %v, want PROCESSING_CONFIG", perr)
	}
	sameEntry(t, env, perr)
}

// TestWinFormula_WindowsFirstColumnAggregates: an aggregation reading a
// WIN_FORMULA label runs the windows first over the records, and sums
// the formula's per-record values — under the projected decode, which
// keeps the record columns the expression reads.
func TestWinFormula_WindowsFirstColumnAggregates(t *testing.T) {
	t.Parallel()
	p, fsys := winFormulaCohort(t, pulse.Options{}, 0)
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "gains.pulse"},
			// The count names d, so the needed set is not empty (an
			// empty one decodes every field): without the formula's own
			// projection entry x would decode null and sum_x2 read 0.
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "x2", Label: "sum_x2"},
				{Type: types.AGG_COUNT, Field: "d", Label: "n"},
			},
			Windows: winFormulaWindows(`[{"type": "WIN_FORMULA", "label": "x2", "params": {"expression": "x * 2"}}]`),
		}
	}
	if env := predictEnvelope(t, p, fsys, "gains.pulse", req()); len(env.Errors) != 0 {
		t.Fatalf("predict refused: %+v", env.Errors)
	}
	resp, err := p.Process(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	var want float64
	for i := range winFormulaRecords {
		_, _, x, _ := winFormulaRec(i)
		want += x * 2
	}
	if got := cellNumber(t, resp.Data[0], "sum_x2"); math.Abs(got-want) > 1e-9 {
		t.Errorf("sum_x2 = %v, want %v", got, want)
	}
}
