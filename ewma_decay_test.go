package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ewmaCohort writes a small series (week, x) whose EWMA at alpha 0.3
// and at the float64 1 - 0.7 (0.30000000000000004) differ in the last
// bits, so a decay converted by float subtraction is caught.
func ewmaCohort(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number within the season"},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true, Description: "Weekly media spend in thousands"},
	}}
	b, err := p.NewCohortBuilder(context.Background(), "s.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range []any{13.7, 2.9, nil, 101.3, 7.77, 55.1, 0.3, 19.9} {
		if err := b.Append(pulse.CohortRow{uint64(i + 1), v}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func ewmaReq(params string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "s.pulse"},
		Windows: []*types.Window{{
			Type: types.WIN_EWMA, Field: "x", Label: "e",
			OrderBy: []types.OrderKey{{Field: "week"}},
			Frame:   &types.FrameSpec{Mode: "rows"},
			Params:  json.RawMessage(params),
		}},
	}
}

func ewmaProcess(t *testing.T, p *pulse.Pulse, params string) []any {
	t.Helper()
	resp, err := p.Process(context.Background(), ewmaReq(params))
	if err != nil {
		t.Fatalf("Process %s: %v", params, err)
	}
	out := make([]any, len(resp.Data))
	for i, r := range resp.Data {
		out[i] = r["e"]
	}
	return out
}

func sameBits(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		af, aok := a[i].(float64)
		bf, bok := b[i].(float64)
		if aok != bok || (aok && math.Float64bits(af) != math.Float64bits(bf)) || (!aok && a[i] != b[i]) {
			return false
		}
	}
	return true
}

// TestEWMADecay_EqualsLiteralAlphaOnEverySurface: decay 0.7 smooths with
// exactly the double of the literal alpha 0.3 — through Process,
// ProcessStream and Materialize — and not with the float64 1 - 0.7.
func TestEWMADecay_EqualsLiteralAlphaOnEverySurface(t *testing.T) {
	p, _ := ewmaCohort(t)
	ctx := context.Background()
	alpha := ewmaProcess(t, p, `{"alpha": 0.3}`)
	decay := ewmaProcess(t, p, `{"decay": 0.7}`)
	if !sameBits(alpha, decay) {
		t.Fatalf("Process decay 0.7 = %v, alpha 0.3 = %v", decay, alpha)
	}
	if float := ewmaProcess(t, p, `{"alpha": 0.30000000000000004}`); sameBits(alpha, float) {
		t.Fatal("fixture cannot tell alpha 0.3 from 1.0 - 0.7; the parity above proves nothing")
	}

	it, err := p.ProcessStream(ctx, ewmaReq(`{"decay": 0.7}`))
	if err != nil {
		t.Fatal(err)
	}
	var streamed []any
	for {
		row, ok, err := it.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		streamed = append(streamed, row["e"])
	}
	_ = it.Close()
	if !sameBits(alpha, streamed) {
		t.Fatalf("ProcessStream decay 0.7 = %v, alpha 0.3 = %v", streamed, alpha)
	}

	if _, err := p.Materialize(ctx, ewmaReq(`{"decay": 0.7}`), "m.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	c, err := p.Open(ctx, "m.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var mat []any
	for i := int64(0); i < r.Len(); i++ {
		row, err := r.RecordAt(i)
		if err != nil {
			t.Fatal(err)
		}
		mat = append(mat, row[len(row)-1])
	}
	if !sameBits(alpha, mat) {
		t.Fatalf("Materialize decay 0.7 = %v, alpha 0.3 = %v", mat, alpha)
	}
}

// TestEWMADecay_PredictRefusesWhatRuntimeRefuses: for every params
// violation, predict reports PULSE_WINDOW_INVALID and Process,
// ProcessStream and Materialize refuse with PULSE_WINDOW_INVALID too —
// the window family's one code (internal/windowparams), the same rule
// sentence on every surface — and a valid params set passes all.
func TestEWMADecay_PredictRefusesWhatRuntimeRefuses(t *testing.T) {
	p, fsys := ewmaCohort(t)
	ctx := context.Background()
	for name, params := range map[string]string{
		"neither":          `{}`,
		"both":             `{"alpha": 0.3, "decay": 0.7}`,
		"decay one":        `{"decay": 1}`,
		"decay negative":   `{"decay": -0.2}`,
		"decay string":     `{"decay": "0.7"}`,
		"alpha zero":       `{"alpha": 0}`,
		"alpha above one":  `{"alpha": 1.5}`,
		"malformed params": `{"alpha": "x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := predictEnvelope(t, p, fsys, "s.pulse", ewmaReq(params))
			var rule string
			for _, e := range env.Errors {
				if e.Code == string(errors.PULSE_WINDOW_INVALID) {
					rule = strings.TrimPrefix(e.Message, "window[0] (WIN_EWMA): ")
				}
			}
			if rule == "" {
				t.Fatalf("predict accepted %s: %+v", params, env.Errors)
			}
			_, perr := p.Process(ctx, ewmaReq(params))
			_, serr := p.ProcessStream(ctx, ewmaReq(params))
			_, merr := p.Materialize(ctx, ewmaReq(params), "bad.pulse", pulse.MaterializeOptions{})
			for surface, err := range map[string]error{"Process": perr, "ProcessStream": serr, "Materialize": merr} {
				ce := requireCode(t, err, errors.PULSE_WINDOW_INVALID)
				if ce.Message != "WIN_EWMA: "+rule {
					t.Errorf("%s message %q, predict rule %q", surface, ce.Message, rule)
				}
			}
		})
	}
	for _, params := range []string{`{"decay": 0}`, `{"decay": 0.5}`, `{"alpha": 1}`} {
		env := predictEnvelope(t, p, fsys, "s.pulse", ewmaReq(params))
		if len(env.Errors) != 0 {
			t.Errorf("predict refused %s: %+v", params, env.Errors)
		}
		ewmaProcess(t, p, params)
	}
}
