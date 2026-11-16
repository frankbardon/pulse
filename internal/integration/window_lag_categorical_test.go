package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// window_lag_categorical_test.go: a lagged category — WIN_LAG / WIN_LEAD
// over a categorical field (mmm-optimize-depth E4-S1, #581) — through
// the public facade.

var wlcRegions = []string{"north", "south", "east"}

// wlcCohort writes visits.pulse: t orders it, region is a categorical,
// x a value.
func wlcCohort(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "t", Type: encoding.FieldTypeU16, Description: "Period index that orders the visits"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region the visit happened in"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Revenue booked during the visit"},
	}}
	b, err := p.NewCohortBuilder(context.Background(), "visits.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		if err := b.Append(pulse.CohortRow{uint64(i), wlcRegions[i%3], float64(i%5) + 0.5}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func wlcRequest(t *testing.T, body string) *types.Request {
	t.Helper()
	var r types.Request
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	r.Cohort = &types.Cohort{Filename: "visits.pulse"}
	return &r
}

const wlcLag = `{"type": "WIN_LAG", "field": "region", "label": "prev_region", "order_by": [{"field": "t"}]`

// TestWinLagCategorical_PredictMatchesRuntime: predict and Process agree
// on every lagged-category verdict — grouped and crosstab by it run, a
// label default runs, a lag of a lag runs; an aggregation or value
// window reading it and a default that is no dictionary label are
// refused by both with the same code (and, for PULSE_WINDOW_INVALID,
// the same rule sentence under each side's prefix).
func TestWinLagCategorical_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fsys := wlcCohort(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		body string
		code errors.Code // "" = valid
	}{
		"group":          {body: `{"windows": [` + wlcLag + `}], "groups": [{"type": "GROUP_CATEGORY", "field": "prev_region"}], "aggregations": [{"type": "AGG_SUM", "field": "x", "label": "s"}]}`},
		"crosstab":       {body: `{"windows": [` + wlcLag + `}], "crosstab": {"rows": [{"type": "GROUP_CATEGORY", "field": "prev_region"}], "columns": [{"type": "GROUP_CATEGORY", "field": "region"}], "cell": {"type": "AGG_COUNT", "field": "x", "label": "n"}}}`},
		"label default":  {body: `{"windows": [` + wlcLag + `, "params": {"default": "east"}}]}`},
		"lead of lag":    {body: `{"windows": [` + wlcLag + `}, {"type": "WIN_LEAD", "field": "prev_region", "label": "back", "order_by": [{"field": "t"}]}], "groups": [{"type": "GROUP_CATEGORY", "field": "back"}], "aggregations": [{"type": "AGG_COUNT", "field": "x", "label": "n"}]}`},
		"aggregated":     {body: `{"windows": [` + wlcLag + `}], "aggregations": [{"type": "AGG_MODE", "field": "prev_region", "label": "m"}]}`, code: errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		"other grouper":  {body: `{"windows": [` + wlcLag + `}], "groups": [{"type": "GROUP_ROUNDED", "field": "prev_region", "interval": 1}], "aggregations": [{"type": "AGG_COUNT", "field": "x", "label": "n"}]}`, code: errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		"value window":   {body: `{"windows": [` + wlcLag + `}, {"type": "WIN_DELTA", "field": "prev_region", "label": "d", "order_by": [{"field": "t"}]}]}`, code: errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		"bad default":    {body: `{"windows": [` + wlcLag + `, "params": {"default": "west"}}]}`, code: errors.PULSE_WINDOW_INVALID},
		"number default": {body: `{"windows": [` + wlcLag + `, "params": {"default": 0}}]}`, code: errors.PULSE_WINDOW_INVALID},
	} {
		t.Run(name, func(t *testing.T) {
			env := predictEnvelope(t, p, fsys, "visits.pulse", wlcRequest(t, c.body))
			_, err := p.Process(ctx, wlcRequest(t, c.body))
			if c.code == "" {
				if len(env.Errors) != 0 || err != nil {
					t.Fatalf("predict %+v, process %v; want both to run", env.Errors, err)
				}
				return
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("process err = %v, want %s", err, c.code)
			}
			matched := false
			for _, e := range env.Errors {
				if e.Code != string(c.code) {
					continue
				}
				msg := e.Message
				if rule, ok := strings.CutPrefix(msg, "window[0] (WIN_LAG): "); ok {
					msg = "WIN_LAG: " + rule
				}
				if msg == ce.Message && reflect.DeepEqual(e.Details["reason"], ce.Details["reason"]) {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("process raised %s %q %v, which predict did not report: %+v", ce.Code, ce.Message, ce.Details, env.Errors)
			}
		})
	}
}

// TestWinLagCategorical_GroupedLabels: GROUP_CATEGORY by a lagged
// category keys by the source's labels (the first record, with no
// previous visit, is null and keys nothing).
func TestWinLagCategorical_GroupedLabels(t *testing.T) {
	t.Parallel()

	p, _ := wlcCohort(t)
	resp, err := p.Process(context.Background(), wlcRequest(t, `{"windows": [`+wlcLag+`}], "groups": [{"type": "GROUP_CATEGORY", "field": "prev_region"}], "aggregations": [{"type": "AGG_COUNT", "field": "x", "label": "n"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(resp.Data)
	total := 0.0
	for _, row := range resp.Data {
		if !reflect.DeepEqual(row["prev_region"], "north") && !reflect.DeepEqual(row["prev_region"], "south") && !reflect.DeepEqual(row["prev_region"], "east") {
			t.Fatalf("group key not a label: %s", b)
		}
		n, _ := row["n"].(float64)
		total += n
	}
	if total != 11 {
		t.Fatalf("grouped count %v, want 11 (12 visits, the first has no previous): %s", total, b)
	}
}

// TestWinLagCategorical_Materialize: predict types a materialized lagged
// category as the source's categorical, and the written cohort stores
// it so — on the source dictionary — so grouping the derived cohort by
// the stored column equals grouping the source by the window.
func TestWinLagCategorical_Materialize(t *testing.T) {
	t.Parallel()

	p, _ := wlcCohort(t)
	ctx := context.Background()
	body := `{"windows": [` + wlcLag + `, "params": {"default": "east"}}]}`
	env, err := p.PredictMaterialize(ctx, wlcRequest(t, body))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict materialize: %v %+v", err, env)
	}
	plan := env.Data.(*descriptor.MaterializePredictResult)
	var col *descriptor.MaterializePredictColumn
	for i := range plan.Columns {
		if plan.Columns[i].Name == "prev_region" {
			col = &plan.Columns[i]
		}
	}
	if col == nil || col.Type != "categorical_u8" || !col.Nullable {
		t.Fatalf("predicted column %+v, want nullable categorical_u8", col)
	}
	if _, err := p.Materialize(ctx, wlcRequest(t, body), "derived.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	insp, err := p.Inspect(ctx, "derived.pulse")
	if err != nil {
		t.Fatal(err)
	}
	var stored, source *descriptor.InspectField
	for i := range insp.Fields {
		switch insp.Fields[i].Name {
		case "prev_region":
			stored = insp.Fields[i]
		case "region":
			source = insp.Fields[i]
		}
	}
	if stored == nil || source == nil || stored.Type != "categorical_u8" || !reflect.DeepEqual(stored.Dictionary, source.Dictionary) {
		t.Fatalf("stored %+v, source %+v: want categorical_u8 on the source dictionary", stored, source)
	}
	group := `"groups": [{"type": "GROUP_CATEGORY", "field": "prev_region"}], "aggregations": [{"type": "AGG_SUM", "field": "x", "label": "s"}]}`
	viaWindow, err := p.Process(ctx, wlcRequest(t, `{"windows": [`+wlcLag+`, "params": {"default": "east"}}], `+group))
	if err != nil {
		t.Fatal(err)
	}
	r := wlcRequest(t, `{`+group)
	r.Cohort = &types.Cohort{Filename: "derived.pulse"}
	viaStored, err := p.Process(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(viaWindow.Data, viaStored.Data) {
		t.Fatalf("window %v\nstored %v", viaWindow.Data, viaStored.Data)
	}
}
