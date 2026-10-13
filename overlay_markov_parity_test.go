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
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// OVERLAY_MARKOV refuses identically in predict and at runtime (code,
// message and details — index included) through the shared overlayhost
// refusals, runs end to end on a weighted host, and leaves the host
// crosstab byte-identical to the overlay-free run.

func markovCrosstab(cohort string, cols []*types.Group, cell types.AggregationType, w *types.WeightSpec, overlays ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Weight: w,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: cols,
			Cell:    &types.Aggregation{Type: cell, Field: "x", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: overlays,
	}
}

func markovOverlay(params string) types.OverlaySpec {
	s := types.OverlaySpec{Name: "flow", Kind: types.OverlayKindMarkov, Scope: types.OverlayScopeMatrix}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func TestMarkov_RefusalsMatchPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	square := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	twoCols := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "subj"}}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"average cell", markovCrosstab(cohort, square, types.AGG_AVERAGE, nil, markovOverlay("")), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"columns carry two groupers", markovCrosstab(cohort, twoCols, types.AGG_COUNT, nil, markovOverlay("")), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"step out of range", markovCrosstab(cohort, square, types.AGG_COUNT, nil, markovOverlay(`{"steps":[0]}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown initial form", markovCrosstab(cohort, square, types.AGG_COUNT, nil, markovOverlay(`{"initial":"uniform"}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", markovCrosstab(cohort, square, types.AGG_COUNT, nil, markovOverlay(`{"horizon":12}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Process(ctx, c.req)
			ce := requireCode(t, err, c.want)
			if ce.Details["index"] != 0 {
				t.Fatalf("runtime details %v lack the spec index", ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, c.req), err)
		})
	}

	// The key-set refusal is runtime-only (predict reads no record): g x
	// subj has one grouper per axis but different keys.
	req := markovCrosstab(cohort, []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}, types.AGG_COUNT, nil, markovOverlay(""))
	if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
		t.Fatalf("predict refused a one-grouper-per-axis host: %+v", env.Errors)
	}
	_, err := p.Process(ctx, req)
	requireCode(t, err, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE)
}

func TestMarkov_EndToEnd(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	square := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	spec := markovOverlay(`{"steps":[2],"initial":"margin"}`)
	for name, w := range map[string]*types.WeightSpec{
		"unweighted":  nil,
		"probability": {Field: "y", Kind: types.WeightKindProbability},
	} {
		t.Run(name, func(t *testing.T) {
			for _, cell := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM} {
				req := markovCrosstab(cohort, square, cell, w, spec)
				if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
					t.Fatalf("%s: predict %+v", cell, env.Errors)
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("%s: %v", cell, err)
				}
				if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil || resp.Overlays[0].Payload.Parts["step_2"] == nil || resp.Overlays[0].Payload.Parts["distribution_2"] == nil {
					t.Fatalf("%s: overlays %+v", cell, resp.Overlays)
				}
				if _, err := json.Marshal(resp); err != nil {
					t.Fatalf("%s: marshal: %v", cell, err)
				}

				// The host crosstab is byte-identical to the overlay-free run.
				bare, err := p.Process(ctx, markovCrosstab(cohort, square, cell, w))
				if err != nil {
					t.Fatal(err)
				}
				got, _ := json.Marshal(resp.Crosstab)
				want, _ := json.Marshal(bare.Crosstab)
				if string(got) != string(want) || bare.Overlays != nil {
					t.Fatalf("%s: the overlay moved the host crosstab", cell)
				}
			}
		})
	}
}

// churnCohort imports a from × to transition cohort where nobody leaves
// churned: churned is a to-only (column-only) state.
func churnCohort(t *testing.T) (*pulse.Pulse, afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("from,to\n")
	for _, m := range []struct {
		from, to string
		n        int
	}{{"active", "active", 6}, {"active", "lapsed", 3}, {"active", "churned", 1}, {"lapsed", "active", 2}, {"lapsed", "lapsed", 4}, {"lapsed", "churned", 4}} {
		for range m.n {
			b.WriteString(m.from + "," + m.to + "\n")
		}
	}
	if err := afero.WriteFile(fs, "churn.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fields := []encoding.Field{
		{Name: "from", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), CsvColumnIdx: 0, ByteOffset: 0},
		{Name: "to", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), CsvColumnIdx: 1, ByteOffset: 1},
	}
	job := &pio.ImportJob{Source: csv.NewReader(fs, "churn.csv"), Target: "churn.pulse", Schema: &encoding.Schema{Fields: fields}, FS: fs}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import: %v", err)
	}
	return p, fs, "churn.pulse"
}

// TestMarkov_ColumnOnlyAbsorbing: a column-only state declared in
// params.absorbing gets a synthesized unit row end to end (predict
// accepts; the absorption times are the closed form [5, 10/3]); left
// undeclared, the runtime still refuses it. Predict reads no record, so
// it cannot see which keys were observed and accepts both: the key-set
// judgement stays runtime-only.
func TestMarkov_ColumnOnlyAbsorbing(t *testing.T) {
	p, fs, cohort := churnCohort(t)
	ctx := context.Background()
	req := func(params string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "from"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "to"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "from", Label: "n"},
			},
			Overlays: []types.OverlaySpec{markovOverlay(params)},
		}
	}

	declared := req(`{"absorbing":["churned"],"steps":[1]}`)
	if env := predictEnvelope(t, p, fs, cohort, declared); len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors)
	}
	resp, err := p.Process(ctx, declared)
	if err != nil {
		t.Fatal(err)
	}
	times := resp.Overlays[0].Payload.Parts["absorption_time"]
	if times == nil || times.Series == nil || len(times.Series.Entries) != 2 {
		t.Fatalf("absorption_time %+v", times)
	}
	want := map[string]float64{"active": 5, "lapsed": 10.0 / 3}
	for _, e := range times.Series.Entries {
		if got := *e.Summary.Statistic; math.Abs(got-want[e.Key[0].(string)]) > 1e-12 {
			t.Fatalf("absorption_time[%v] = %v, want %v", e.Key, got, want[e.Key[0].(string)])
		}
	}
	if resp.Overlays[0].Summary.Parameters["synthesized_rows"] != 1 {
		t.Fatalf("summary %v", resp.Overlays[0].Summary.Parameters)
	}

	undeclared := req(`{"steps":[1]}`)
	if env := predictEnvelope(t, p, fs, cohort, undeclared); len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors)
	}
	_, err = p.Process(ctx, undeclared)
	requireCode(t, err, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE)
}
