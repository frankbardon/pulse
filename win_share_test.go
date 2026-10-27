package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// gainsDefaults is the casebook credit-risk step 13 holdout: defaults
// per decile, riskiest (D10) first, 1,800 loans in each.
var gainsDefaults = []int{610, 368, 308, 261, 176, 142, 123, 92, 46, 23}

// gainsCohort writes one record per loan: its decile (1 = lowest risk),
// a score that orders the deciles, the 0/1 default and a categorical
// region (for the type refusals).
func gainsCohort(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "decile", Type: encoding.FieldTypeU8, Description: "Score decile of the loan, 10 is the riskiest"},
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Predicted probability of default at origination"},
		{Name: "defaulted", Type: encoding.FieldTypeU8, Description: "Whether the loan defaulted within twelve months"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region that originated the loan"},
	}}
	b, err := p.NewCohortBuilder(context.Background(), "loans.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range gainsDefaults {
		decile := 10 - i
		for k := range 1800 {
			def := uint64(0)
			if k < d {
				def = 1
			}
			region := "north"
			if k%2 == 1 {
				region = "south"
			}
			if err := b.Append(pulse.CohortRow{uint64(decile), float64(decile) / 10, def, region}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// gainsReq is the step 13 decile table with the gains columns as
// windows instead of hand division.
func gainsReq(windows string) *types.Request {
	var r types.Request
	body := `{
		"groups": [{"type": "GROUP_CATEGORY", "field": "decile"}],
		"aggregations": [
			{"type": "AGG_COUNT", "field": "decile", "label": "loans"},
			{"type": "AGG_SUM", "field": "defaulted", "label": "defaults"},
			{"type": "AGG_AVERAGE", "field": "score", "label": "mean_pd"}
		],
		"windows": ` + windows + `,
		"sort": [{"field": "mean_pd", "desc": true}]
	}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		panic(err)
	}
	r.Cohort = &types.Cohort{Filename: "loans.pulse"}
	return &r
}

const gainsWindows = `[
	{"type": "WIN_SHARE", "field": "defaults", "label": "cum_share_defaults", "order_by": [{"field": "mean_pd", "desc": true}], "params": {"cumulative": true}},
	{"type": "WIN_SHARE", "field": "loans", "label": "cum_share_loans", "order_by": [{"field": "mean_pd", "desc": true}], "params": {"cumulative": true}},
	{"type": "WIN_LIFT", "field": "defaults", "label": "cum_lift", "order_by": [{"field": "mean_pd", "desc": true}], "params": {"cumulative": true, "base": "loans"}},
	{"type": "WIN_SHARE", "field": "defaults", "label": "share_defaults"}
]`

func ratio(a, b float64) float64 { return a / b }

// TestWinShare_DecileGainsTable reproduces the casebook step 13 shape
// end to end on grouped rows: cumulative share of defaults, of loans and
// the cumulative lift — D9 reads 978/2149, 3600/18000 and their quotient
// — through Process, ProcessStream (buffered) and predict.
func TestWinShare_DecileGainsTable(t *testing.T) {
	t.Parallel()

	p, fsys := gainsCohort(t)
	ctx := context.Background()
	resp, err := p.Process(ctx, gainsReq(gainsWindows))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 10 {
		t.Fatalf("rows = %d, want 10", len(resp.Data))
	}
	d9 := resp.Data[1]
	wantDef, wantLoans := ratio(978, 2149), ratio(3600, 18000)
	if d9["cum_share_defaults"] != wantDef || d9["cum_share_loans"] != wantLoans {
		t.Fatalf("D9 cumulative shares = %v, %v; want %v, %v", d9["cum_share_defaults"], d9["cum_share_loans"], wantDef, wantLoans)
	}
	if want := ratio(wantDef, wantLoans); d9["cum_lift"] != want {
		t.Fatalf("D9 cumulative lift = %v, want %v", d9["cum_lift"], want)
	}
	if got := resp.Data[0]["share_defaults"]; got != ratio(610, 2149) {
		t.Fatalf("D10 share of defaults = %v, want 610/2149", got)
	}
	last := resp.Data[9]
	if last["cum_share_defaults"] != 1.0 || last["cum_share_loans"] != 1.0 || last["cum_lift"] != 1.0 {
		t.Fatalf("D1 closes every curve at 1: %v", last)
	}

	it, err := p.ProcessStream(ctx, gainsReq(gainsWindows))
	if err != nil {
		t.Fatal(err)
	}
	var streamed []map[string]any
	for {
		row, ok, err := it.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		streamed = append(streamed, row)
	}
	_ = it.Close()
	if len(streamed) != 10 || streamed[1]["cum_lift"] != d9["cum_lift"] {
		t.Fatalf("ProcessStream D9 lift = %v, Process %v", streamed, d9["cum_lift"])
	}

	pr, err := p.Predict(ctx, gainsReq(gainsWindows))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Streamable {
		t.Fatal("predict marks a WIN_SHARE / WIN_LIFT request streamable; windows are buffered")
	}
	if env := predictEnvelope(t, p, fsys, "loans.pulse", gainsReq(gainsWindows)); len(env.Errors) != 0 {
		t.Fatalf("predict refused the gains table: %+v", env.Errors)
	}
}

// TestWinShare_ZeroTotalIsNullOnTheWire: a partition whose total is 0
// reads null — the key kept, never 0 — on the JSON wire.
func TestWinShare_ZeroTotalIsNullOnTheWire(t *testing.T) {
	t.Parallel()

	p, _ := gainsCohort(t)
	// The share of a column that is 0 on every row, and a lift over it.
	req := gainsReq(`[
		{"type": "WIN_SHARE", "field": "none", "label": "s"},
		{"type": "WIN_LIFT", "field": "loans", "label": "l", "params": {"base": "none"}}
	]`)
	req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "zero", Expression: "0"}}
	req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "zero", Label: "none"})
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// In Go the cell is nil, like every window's undefined row (never a
	// NaN or an Inf a caller could add up).
	for i, row := range resp.Data {
		if v, ok := row["s"]; !ok || v != nil {
			t.Fatalf("row %d: s = %v (present %v), want nil", i, v, ok)
		}
		if v, ok := row["l"]; !ok || v != nil {
			t.Fatalf("row %d: l = %v (present %v), want nil", i, v, ok)
		}
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"s":null`) || !strings.Contains(string(raw), `"l":null`) {
		t.Fatalf("zero totals not null on the wire: %s", raw)
	}
	if strings.Contains(string(raw), `"s":0`) || strings.Contains(string(raw), `"l":0`) {
		t.Fatalf("a zero total read 0: %s", raw)
	}
}

// TestWinShare_PredictMatchesRuntime: every refusal of the two operators
// raises the same code on predict and on Process / ProcessStream /
// Materialize; the shared-reading refusals carry the same rule sentence
// (predict "window[0] (TYPE): rule", runtime "TYPE: rule"). A
// non-cumulative slot without order_by runs on every surface.
func TestWinShare_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fsys := gainsCohort(t)
	ctx := context.Background()
	records := func(windows string) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(`{"windows": `+windows+`}`), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort = &types.Cohort{Filename: "loans.pulse"}
		return &r
	}
	order := `"order_by": [{"field": "score"}]`
	cases := []struct {
		name    string
		req     func() *types.Request
		code    errors.Code
		rule    string // the shared reading's sentence, "" when another rule refuses
		windowT string
	}{
		{"share frame", func() *types.Request {
			return records(`[{"type": "WIN_SHARE", "field": "score", ` + order + `, "frame": {"mode": "rows", "preceding": null, "following": 0}}]`)
		}, errors.PULSE_WINDOW_INVALID, "frame is not allowed", "WIN_SHARE"},
		{"share cumulative without order_by", func() *types.Request {
			return records(`[{"type": "WIN_SHARE", "field": "score", "params": {"cumulative": true}}]`)
		}, errors.PULSE_WINDOW_INVALID, "order_by is required when params.cumulative is true", "WIN_SHARE"},
		{"share missing field", func() *types.Request {
			return records(`[{"type": "WIN_SHARE", ` + order + `}]`)
		}, errors.PULSE_WINDOW_INVALID, "field is required", "WIN_SHARE"},
		{"share malformed params", func() *types.Request {
			return records(`[{"type": "WIN_SHARE", "field": "score", "params": {"cumulative": "yes"}}]`)
		}, errors.PULSE_WINDOW_INVALID, "malformed params", "WIN_SHARE"},
		{"lift missing base", func() *types.Request {
			return records(`[{"type": "WIN_LIFT", "field": "score"}]`)
		}, errors.PULSE_WINDOW_INVALID, "params.base is required", "WIN_LIFT"},
		{"lift frame", func() *types.Request {
			return records(`[{"type": "WIN_LIFT", "field": "score", ` + order + `, "params": {"base": "decile"}, "frame": {"mode": "rows"}}]`)
		}, errors.PULSE_WINDOW_INVALID, "frame is not allowed", "WIN_LIFT"},
		{"lift cumulative without order_by", func() *types.Request {
			return records(`[{"type": "WIN_LIFT", "field": "score", "params": {"base": "decile", "cumulative": true}}]`)
		}, errors.PULSE_WINDOW_INVALID, "order_by is required when params.cumulative is true", "WIN_LIFT"},
		{"lift base names no column", func() *types.Request {
			return records(`[{"type": "WIN_LIFT", "field": "score", "params": {"base": "nope"}}]`)
		}, errors.SERVICE_VALIDATION, "", ""},
		{"lift base categorical", func() *types.Request {
			return records(`[{"type": "WIN_LIFT", "field": "score", "params": {"base": "region"}}]`)
		}, errors.PULSE_FIELD_TYPE_UNSUPPORTED, "", ""},
		{"share field categorical", func() *types.Request {
			return records(`[{"type": "WIN_SHARE", "field": "region"}]`)
		}, errors.PULSE_FIELD_TYPE_UNSUPPORTED, "", ""},
		{"share unknown params key", func() *types.Request {
			return records(`[{"type": "WIN_SHARE", "field": "score", "params": {"base": "decile"}}]`)
		}, errors.PULSE_REQUEST_UNKNOWN_FIELD, "", ""},
		{"request weight", func() *types.Request {
			r := records(`[{"type": "WIN_SHARE", "field": "score"}]`)
			r.Weight = &types.WeightSpec{Field: "score"}
			return r
		}, errors.PROCESSING_CONFIG, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := predictEnvelope(t, p, fsys, "loans.pulse", tc.req())
			var predictRule string
			found := false
			for _, e := range env.Errors {
				if e.Code == string(tc.code) {
					found = true
					predictRule = strings.TrimPrefix(e.Message, "window[0] ("+tc.windowT+"): ")
				}
			}
			if !found {
				t.Fatalf("predict did not report %s: %+v", tc.code, env.Errors)
			}
			if tc.rule != "" && !strings.HasPrefix(predictRule, tc.rule) {
				t.Fatalf("predict rule %q, want %q", predictRule, tc.rule)
			}
			_, perr := p.Process(ctx, tc.req())
			_, serr := p.ProcessStream(ctx, tc.req())
			_, merr := p.Materialize(ctx, tc.req(), "bad.pulse", pulse.MaterializeOptions{})
			for surface, err := range map[string]error{"Process": perr, "ProcessStream": serr, "Materialize": merr} {
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != tc.code {
					t.Errorf("%s: err = %v, want %s", surface, err, tc.code)
					continue
				}
				if tc.rule != "" && ce.Message != tc.windowT+": "+predictRule {
					t.Errorf("%s message %q, predict rule %q", surface, ce.Message, predictRule)
				}
			}
		})
	}

	// Valid: no order_by on the non-cumulative forms, on every surface.
	for _, w := range []string{
		`[{"type": "WIN_SHARE", "field": "score", "partition_by": ["decile"]}]`,
		`[{"type": "WIN_LIFT", "field": "score", "params": {"base": "decile"}}]`,
		`[{"type": "WIN_LIFT", "field": "score", ` + order + `, "params": {"base": "decile", "cumulative": true}}]`,
	} {
		if env := predictEnvelope(t, p, fsys, "loans.pulse", records(w)); len(env.Errors) != 0 {
			t.Errorf("predict refused %s: %+v", w, env.Errors)
		}
		if _, err := p.Process(ctx, records(w)); err != nil {
			t.Errorf("Process refused %s: %v", w, err)
		}
		if _, err := p.Materialize(ctx, records(w), "ok.pulse", pulse.MaterializeOptions{Overwrite: true}); err != nil {
			t.Errorf("Materialize refused %s: %v", w, err)
		}
	}
}

// TestWinLift_BaseTypePredictMatchesRuntime: WIN_LIFT's params.base is
// judged like a value window's field on every field type — the
// accepted-types table's refusal (PULSE_FIELD_TYPE_UNSUPPORTED) or, for a
// vector, PULSE_VECTOR_FIELD_NOT_SCALAR — with the same code on predict
// and at runtime, and an accepted type runs on both.
func TestWinLift_BaseTypePredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, ft := range acceptanceFieldTypes() {
		t.Run(ft.String(), func(t *testing.T) {
			mk := func() *types.Request {
				var r types.Request
				body := `{"windows":[{"type":"WIN_LIFT","field":"x","order_by":[{"field":"x"}],"params":{"base":"t_` + ft.String() + `","cumulative":true}}]}`
				if err := json.Unmarshal([]byte(body), &r); err != nil {
					t.Fatal(err)
				}
				r.Cohort = &types.Cohort{Filename: cohort}
				return &r
			}
			var want errors.Code
			switch {
			case ft.IsVector():
				want = errors.PULSE_VECTOR_FIELD_NOT_SCALAR
			case fieldaccept.Refusal("WIN_LIFT", fieldaccept.SlotBase, "b", ft) != nil:
				want = errors.PULSE_FIELD_TYPE_UNSUPPORTED
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			_, rerr := p.Process(ctx, mk())
			if want == "" {
				if len(env.Errors) != 0 || rerr != nil {
					t.Fatalf("accepted base refused: predict %+v, runtime %v", env.Errors, rerr)
				}
				return
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(want) {
				t.Fatalf("predict errors %+v, want one %s", env.Errors, want)
			}
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != want {
				t.Fatalf("runtime %v, want %s", rerr, want)
			}
		})
	}
}
