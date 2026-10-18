package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// windowParityCohort is a small ordered series: t orders it, x and y are
// numeric value columns, g partitions it.
func windowParityCohort(t *testing.T, ext pulse.Extensions) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "t", Type: encoding.FieldTypeU16, Description: "Period index that orders the series"},
		{Name: "g", Type: encoding.FieldTypeU8, Description: "Series the period belongs to"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Measured value of the series in the period"},
		{Name: "y", Type: encoding.FieldTypeF64, Description: "Comparison value of the series in the period"},
	}}
	b, err := p.NewCohortBuilder(context.Background(), "series.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		if err := b.Append(pulse.CohortRow{uint64(i), uint64(i % 2), float64(i*7%5 + 1), float64(i%3 + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// windowParityRow is one window operator's parity fixture: a valid slot
// and the operator-specific params refusals (the type-neutral mutations
// are derived from the valid slot).
type windowParityRow struct {
	valid  string
	params []string // params objects predict refuses
}

// windowParityRows holds one row per registered window operator.
// TestWindowRefusals_PredictMatchesRuntime fails for a window type with
// no row, so a new operator cannot ship without its parity cases.
var windowParityRows = map[types.WindowType]windowParityRow{
	types.WIN_LAG: {`{"type": "WIN_LAG", "field": "x", "order_by": [{"field": "t"}]}`,
		[]string{`{"offset": -1}`, `{"offset": "one"}`}},
	types.WIN_LEAD: {`{"type": "WIN_LEAD", "field": "x", "order_by": [{"field": "t"}], "params": {"offset": 2, "default": 0}}`,
		[]string{`{"offset": -2}`, `{"offset": 1.5}`}},
	types.WIN_ROW_NUMBER:  {`{"type": "WIN_ROW_NUMBER", "partition_by": ["g"], "order_by": [{"field": "t"}]}`, nil},
	types.WIN_RANK:        {`{"type": "WIN_RANK", "order_by": [{"field": "x"}]}`, nil},
	types.WIN_DENSE_RANK:  {`{"type": "WIN_DENSE_RANK", "order_by": [{"field": "x", "desc": true}]}`, nil},
	types.WIN_RUNNING_SUM: {`{"type": "WIN_RUNNING_SUM", "field": "x", "order_by": [{"field": "t"}], "frame": {"mode": "rows", "preceding": null, "following": 0}}`, nil},
	types.WIN_RUNNING_AVG: {`{"type": "WIN_RUNNING_AVG", "field": "x", "order_by": [{"field": "t"}], "frame": {"mode": "rows", "preceding": 2, "following": 0}}`, nil},
	types.WIN_MOVING_AVG:  {`{"type": "WIN_MOVING_AVG", "field": "x", "order_by": [{"field": "t"}], "frame": {"mode": "rows", "preceding": 1, "following": 1}}`, nil},
	types.WIN_EWMA: {`{"type": "WIN_EWMA", "field": "x", "order_by": [{"field": "t"}], "frame": {"mode": "rows"}, "params": {"alpha": 0.5}}`,
		[]string{`{}`, `{"alpha": 0}`, `{"alpha": 0.3, "decay": 0.7}`}},
	types.WIN_PCT_CHANGE: {`{"type": "WIN_PCT_CHANGE", "field": "x", "order_by": [{"field": "t"}]}`,
		[]string{`{"periods": 0}`, `{"periods": "two"}`}},
	types.WIN_DELTA: {`{"type": "WIN_DELTA", "field": "x", "order_by": [{"field": "t"}], "params": {"periods": 2}}`,
		[]string{`{"periods": -1}`, `{"periods": [1]}`}},
	types.WIN_SHARE: {`{"type": "WIN_SHARE", "field": "x", "order_by": [{"field": "t"}], "params": {"cumulative": true}}`,
		[]string{`{"cumulative": "yes"}`}},
	types.WIN_LIFT: {`{"type": "WIN_LIFT", "field": "x", "order_by": [{"field": "t"}], "params": {"base": "y", "cumulative": true}}`,
		[]string{`{"cumulative": true}`, `{"base": "y", "cumulative": 1}`}},
}

// windowParityCase is one slot to judge, with what predict must say.
type windowParityCase struct {
	name   string
	slot   map[string]any
	refuse bool // predict must refuse it (PULSE_WINDOW_INVALID)
}

// windowParityCases derives every case for one operator: the valid slot,
// its params refusals and the type-neutral mutations, each with the
// verdict the shared reading's tables (internal/windowparams) demand.
func windowParityCases(t *testing.T, typ types.WindowType, row windowParityRow) []windowParityCase {
	t.Helper()
	parse := func() map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(row.valid), &m); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		return m
	}
	cases := []windowParityCase{{name: "valid", slot: parse()}}
	for _, raw := range row.params {
		m := parse()
		var params any
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			t.Fatalf("%s params %s: %v", typ, raw, err)
		}
		m["params"] = params
		cases = append(cases, windowParityCase{name: "params " + raw, slot: m, refuse: true})
	}

	m := parse()
	delete(m, "order_by")
	// A share / lift's order_by is required only by its cumulative form
	// (every share fixture is cumulative).
	cases = append(cases, windowParityCase{name: "no order_by", slot: m, refuse: true})

	m = parse()
	m["order_by"] = []any{map[string]any{"field": ""}}
	cases = append(cases, windowParityCase{name: "order_by key without field", slot: m, refuse: true})

	if _, ok := parse()["field"]; ok {
		m = parse()
		delete(m, "field")
		cases = append(cases, windowParityCase{name: "no field", slot: m, refuse: windowparams.FieldRequired(typ)})
	}

	if _, ok := parse()["frame"]; ok {
		m = parse()
		delete(m, "frame")
		cases = append(cases, windowParityCase{name: "no frame", slot: m, refuse: windowparams.FrameRequired(typ)})
		m = parse()
		m["frame"].(map[string]any)["mode"] = "range"
		cases = append(cases, windowParityCase{name: "frame mode range", slot: m, refuse: true})
		m = parse()
		m["frame"].(map[string]any)["preceding"] = -1
		cases = append(cases, windowParityCase{name: "frame preceding negative", slot: m, refuse: true})
	} else {
		// Every operator that takes no frame refuses one.
		m = parse()
		m["frame"] = map[string]any{"mode": "rows", "preceding": 1, "following": 0}
		cases = append(cases, windowParityCase{name: "frame given", slot: m, refuse: true})
	}
	return cases
}

// TestWindowRefusals_PredictMatchesRuntime: for every registered window
// operator, each slot configuration predict refuses is refused by
// Process, ProcessStream and Materialize with a code and message predict
// reported (PULSE_WINDOW_INVALID: predict "window[0] (TYPE): rule",
// runtime "TYPE: rule"), and each slot predict accepts runs on all three.
// The verdicts themselves follow the shared reading's tables: order_by
// required, field required on value-bearing operators, frame required
// on running / moving / EWMA aggregates and refused everywhere else.
func TestWindowRefusals_PredictMatchesRuntime(t *testing.T) {
	for _, typ := range types.AllWindowTypes() {
		if _, ok := windowParityRows[typ]; !ok {
			t.Errorf("%s has no windowParityRows entry: add its valid slot and params refusals", typ)
		}
	}
	p, fsys := windowParityCohort(t, pulse.Extensions{})
	ctx := context.Background()
	out := 0
	for _, typ := range types.AllWindowTypes() {
		row, ok := windowParityRows[typ]
		if !ok {
			continue
		}
		for _, tc := range windowParityCases(t, typ, row) {
			t.Run(string(typ)+"/"+tc.name, func(t *testing.T) {
				slot, err := json.Marshal(tc.slot)
				if err != nil {
					t.Fatal(err)
				}
				req := func() *types.Request {
					var r types.Request
					if err := json.Unmarshal([]byte(`{"windows": [`+string(slot)+`]}`), &r); err != nil {
						t.Fatal(err)
					}
					r.Cohort = &types.Cohort{Filename: "series.pulse"}
					return &r
				}
				env := predictEnvelope(t, p, fsys, "series.pulse", req())
				accept := map[string]bool{}
				invalid := 0
				for _, e := range env.Errors {
					accept[e.Code+"|"+e.Message] = true
					if e.Code == string(errors.PULSE_WINDOW_INVALID) {
						invalid++
						if rule, ok := strings.CutPrefix(e.Message, "window[0] ("+string(typ)+"): "); ok {
							accept[e.Code+"|"+string(typ)+": "+rule] = true
						}
					}
				}
				if tc.refuse && invalid == 0 {
					t.Fatalf("predict accepted %s: %+v", slot, env.Errors)
				}
				if !tc.refuse && len(env.Errors) != 0 {
					t.Fatalf("predict refused %s: %+v", slot, env.Errors)
				}

				out++
				_, perr := p.Process(ctx, req())
				_, serr := p.ProcessStream(ctx, req())
				_, merr := p.Materialize(ctx, req(), fmt.Sprintf("out%d.pulse", out), pulse.MaterializeOptions{})
				for _, s := range []struct {
					name string
					err  error
				}{{"Process", perr}, {"ProcessStream", serr}, {"Materialize", merr}} {
					if len(env.Errors) == 0 {
						if s.err != nil {
							t.Errorf("%s refused what predict accepts: %v", s.name, s.err)
						}
						continue
					}
					var ce *errors.CodedError
					if !stderrors.As(s.err, &ce) {
						t.Errorf("%s accepted what predict refuses (%+v): err = %v", s.name, env.Errors, s.err)
						continue
					}
					if !accept[string(ce.Code)+"|"+ce.Message] {
						t.Errorf("%s raised %s %q, which predict did not report: %+v", s.name, ce.Code, ce.Message, env.Errors)
					}
				}
			})
		}
	}
}

// ordinalComputer writes the row's 1-based position in its partition.
type ordinalComputer struct{}

func (ordinalComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for i, r := range part {
			rows[r][label] = int64(i + 1)
		}
	}
	return nil
}

// TestWindowRefusals_ExtensionPredictMatchesRuntime: an embedder's window
// is judged on the type-neutral rules of the shared reading — order_by
// required, every key names a field, a given frame is mode "rows" with
// bounds >= 0 — by predict and by every runtime surface alike, before
// its factory runs.
func TestWindowRefusals_ExtensionPredictMatchesRuntime(t *testing.T) {
	p, fsys := windowParityCohort(t, pulse.Extensions{Windows: []pulse.WindowRegistration{{
		Name:        "WIN_ACME_ORDINAL",
		Description: "Test-only ordinal window.",
		Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) {
			return ordinalComputer{}, nil
		},
		FieldInputs: func(json.RawMessage) []string { return nil },
	}}})
	ctx := context.Background()
	for i, tc := range []struct {
		name, slot, rule string
	}{
		{"valid", `{"type": "WIN_ACME_ORDINAL", "order_by": [{"field": "t"}], "frame": {"mode": "rows"}}`, ""},
		{"no order_by", `{"type": "WIN_ACME_ORDINAL"}`, "order_by is required (at least one key)"},
		{"order_by key without field", `{"type": "WIN_ACME_ORDINAL", "order_by": [{"field": ""}]}`, "order_by[0] missing field"},
		{"frame mode range", `{"type": "WIN_ACME_ORDINAL", "order_by": [{"field": "t"}], "frame": {"mode": "range"}}`, `frame.mode must be "rows" (got "range")`},
		{"frame following negative", `{"type": "WIN_ACME_ORDINAL", "order_by": [{"field": "t"}], "frame": {"mode": "rows", "following": -1}}`, "frame.following must be >= 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := func() *types.Request {
				var r types.Request
				if err := json.Unmarshal([]byte(`{"windows": [`+tc.slot+`]}`), &r); err != nil {
					t.Fatal(err)
				}
				r.Cohort = &types.Cohort{Filename: "series.pulse"}
				return &r
			}
			env := predictEnvelope(t, p, fsys, "series.pulse", req())
			if tc.rule == "" {
				if len(env.Errors) != 0 {
					t.Fatalf("predict refused a valid slot: %+v", env.Errors)
				}
			} else {
				found := false
				for _, e := range env.Errors {
					found = found || (e.Code == string(errors.PULSE_WINDOW_INVALID) &&
						e.Message == "window[0] (WIN_ACME_ORDINAL): "+tc.rule)
				}
				if !found {
					t.Fatalf("predict did not report %q: %+v", tc.rule, env.Errors)
				}
			}
			_, perr := p.Process(ctx, req())
			_, serr := p.ProcessStream(ctx, req())
			_, merr := p.Materialize(ctx, req(), fmt.Sprintf("ext%d.pulse", i), pulse.MaterializeOptions{})
			for surface, err := range map[string]error{"Process": perr, "ProcessStream": serr, "Materialize": merr} {
				if tc.rule == "" {
					if err != nil {
						t.Errorf("%s refused a valid slot: %v", surface, err)
					}
					continue
				}
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WINDOW_INVALID || ce.Message != "WIN_ACME_ORDINAL: "+tc.rule {
					t.Errorf("%s: err = %v, want PULSE_WINDOW_INVALID %q", surface, err, "WIN_ACME_ORDINAL: "+tc.rule)
				}
			}
		})
	}
}
