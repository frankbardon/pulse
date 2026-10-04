package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Undefined figures on the wire (weighting-descriptive E3; the rule is
// types.MarshalFinite, .claude/reference/response-components.md
// "Undefined figures"). The engine keeps NaN where that is its Go
// contract — AGG_RATIO over an all-zero denominator, AGG_CI_* below two
// rows, INDEX_VS_PRIOR's first entry, an unfilled rolling window — and
// the JSON envelope says null there instead of failing the whole
// response with `json: unsupported value: NaN`.

// nonFiniteRows: g=0 has an all-zero denominator (y = 0) on every row,
// so AGG_RATIO is 0/0 unweighted; g=1 has a valid denominator but
// weight 0 on every row, so a weighted AGG_RATIO is 0/0; g=2 is a
// single row, so AGG_CI_* (n < 2) is undefined; g=3 is ordinary.
func nonFiniteRows(int) []parityRow {
	var rows []parityRow
	add := func(g uint8, n int, y, w float64) {
		for range n {
			id := uint32(len(rows))
			rows = append(rows, parityRow{id: id, x: float64(id%5) + 1, y: y, g: g, w: w, s: 3})
		}
	}
	add(0, 6, 0, 2)
	add(1, 6, 1.5, 0)
	add(2, 1, 2.5, 1)
	add(3, 9, 0.75, 3)
	return rows
}

// envelopeJSON is the --json wire path every CLI leaf and MCP tool
// takes: descriptor.NewEnvelope + encoding/json. It must succeed, and
// the result is decoded for inspection.
func envelopeJSON(t *testing.T, data any) map[string]any {
	t.Helper()
	body, err := json.Marshal(descriptor.NewEnvelope(data))
	if err != nil {
		t.Fatalf("the envelope does not serialise: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("the envelope is not valid JSON: %v\n%s", err, body)
	}
	return out
}

// dataRow returns the decoded data row whose group field g equals want.
func dataRow(t *testing.T, env map[string]any, want float64) map[string]any {
	t.Helper()
	rows, _ := env["data"].(map[string]any)["data"].([]any)
	for _, r := range rows {
		m := r.(map[string]any)
		if fmt.Sprint(m["g"]) == fmt.Sprint(want) {
			return m
		}
	}
	t.Fatalf("no data row with g=%v in %v", want, rows)
	return nil
}

// nullKeyCount counts the maps under v that carry key with a JSON null.
func nullKeyCount(v any, key string) int {
	n := 0
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if k == key && e == nil {
				n++
			}
			n += nullKeyCount(e, key)
		}
	case []any:
		for _, e := range x {
			n += nullKeyCount(e, key)
		}
	}
	return n
}

func runNonFinite(t *testing.T, store *parityStore, req *types.Request, wantStream bool) *types.Response {
	t.Helper()
	if got := processing.CanStreamRequest(processing.StampWeights(req, nil), paritySchema()); got != wantStream {
		t.Fatalf("streams = %v, want %v: the case does not take its arm", got, wantStream)
	}
	svc := New(store.mem)
	svc.SetShardWorkers(1)
	svc.SetDecodeWorkers(1)
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

func ratioAgg(label string) *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_RATIO, Field: "x", Label: label,
		Params: json.RawMessage(`{"numerator_field":"x","denominator_field":"y"}`)}
}

// TestNonFiniteFiguresMarshalAsNull runs every known producer of an
// undefined figure through the real JSON envelope and asserts it
// serialises with null at exactly the undefined spot.
func TestNonFiniteFiguresMarshalAsNull(t *testing.T) {
	store := newParityStore(t, "nonfinite", nonFiniteRows)
	path := store.paths[paritySingleFile]
	grouped := func(aggs ...*types.Aggregation) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: aggs}
	}
	// AGG_MEDIAN never streams: it pins the buffered arm.
	bufferedSteer := &types.Aggregation{Type: types.AGG_MEDIAN, Field: "x", Label: "steer"}

	for _, arm := range []struct {
		name   string
		stream bool
	}{{"buffered", false}, {"streaming", true}} {
		t.Run("ratio_zero_denominator/"+arm.name, func(t *testing.T) {
			req := grouped(ratioAgg("r"))
			if !arm.stream {
				req.Aggregations = append(req.Aggregations, bufferedSteer)
			}
			env := envelopeJSON(t, runNonFinite(t, store, req, arm.stream))
			if v, ok := dataRow(t, env, 0)["r"]; !ok || v != nil {
				t.Errorf("g=0 ratio = %v (present %v), want null", v, ok)
			}
			if _, ok := dataRow(t, env, 3)["r"].(float64); !ok {
				t.Errorf("g=3 ratio = %v, want a number", dataRow(t, env, 3)["r"])
			}
		})
		t.Run("ratio_all_zero_weight/"+arm.name, func(t *testing.T) {
			req := grouped(ratioAgg("r"))
			req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
			if !arm.stream {
				steer := *bufferedSteer
				steer.Weight = types.NullSlotWeight()
				req.Aggregations = append(req.Aggregations, &steer)
			}
			env := envelopeJSON(t, runNonFinite(t, store, req, arm.stream))
			if v, ok := dataRow(t, env, 1)["r"]; !ok || v != nil {
				t.Errorf("g=1 weighted ratio = %v (present %v), want null", v, ok)
			}
		})
		t.Run("ci_below_two_rows/"+arm.name, func(t *testing.T) {
			req := grouped(
				&types.Aggregation{Type: types.AGG_CI_LOWER, Field: "x", Label: "lo", Params: json.RawMessage(`{"confidence":0.95,"method":"normal"}`)},
				&types.Aggregation{Type: types.AGG_CI_UPPER, Field: "x", Label: "hi", Params: json.RawMessage(`{"confidence":0.95,"method":"normal"}`)},
			)
			if !arm.stream {
				req.Aggregations = append(req.Aggregations, bufferedSteer)
			}
			env := envelopeJSON(t, runNonFinite(t, store, req, arm.stream))
			row := dataRow(t, env, 2)
			for _, k := range []string{"lo", "hi"} {
				if v, ok := row[k]; !ok || v != nil {
					t.Errorf("g=2 %s = %v (present %v), want null", k, v, ok)
				}
			}
		})
	}

	// Run-level components: an ungrouped run filtered down to the
	// degenerate group carries the undefined figure in its operator map
	// (Components.Aggregations[i].operator) as null.
	t.Run("components", func(t *testing.T) {
		for _, tc := range []struct {
			g    string
			agg  *types.Aggregation
			keys []string
		}{
			{"0", ratioAgg("r"), []string{"ratio"}},
			{"2", &types.Aggregation{Type: types.AGG_CI_LOWER, Field: "x", Label: "lo", Params: json.RawMessage(`{"confidence":0.95,"method":"normal"}`)}, []string{"lower"}},
			{"2", &types.Aggregation{Type: types.AGG_CI_UPPER, Field: "x", Label: "hi", Params: json.RawMessage(`{"confidence":0.95,"method":"normal"}`)}, []string{"upper"}},
		} {
			req := &types.Request{Cohort: &types.Cohort{Filename: path},
				Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "g == " + tc.g}},
				Aggregations: []*types.Aggregation{tc.agg}}
			svc := New(store.mem)
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			env := envelopeJSON(t, resp)
			aggs := env["data"].(map[string]any)["components"].(map[string]any)["aggregations"].([]any)
			op := aggs[0].(map[string]any)["operator"].(map[string]any)
			for _, k := range tc.keys {
				if v, ok := op[k]; !ok || v != nil {
					t.Errorf("%s components %s = %v (present %v), want null", tc.agg.Type, k, v, ok)
				}
			}
		}
	})

	// Both crosstab arms: the cell over an all-zero denominator is
	// present (rows reached it) with a null value.
	for _, arm := range crosstabArms() {
		for _, weighted := range []bool{false, true} {
			name := "crosstab_ratio/" + arm.name
			row := 0
			if weighted {
				name += "/weighted"
				row = 1
			}
			t.Run(name, func(t *testing.T) {
				req := &types.Request{
					Cohort:    &types.Cohort{Filename: path},
					Filterers: []*types.Filterer{crosstabBufferedSteer()},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
						Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "id", Interval: 200}},
						Cell:    ratioAgg("cell"),
						Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					},
				}
				var src *paritySource
				if weighted {
					src = &paritySources()[1] // request_frequency
				}
				env := envelopeJSON(t, runCrosstabOn(t, store, req, src, arm))
				matrix := env["data"].(map[string]any)["crosstab"].(map[string]any)["matrix"].(map[string]any)
				keys := matrix["row_keys"].([]any)
				cells := matrix["cells"].([]any)
				found := false
				for i, k := range keys {
					if fmt.Sprint(k.([]any)[0]) != fmt.Sprint(row) {
						continue
					}
					for _, c := range cells[i].([]any) {
						cell := c.(map[string]any)
						if cell["present"] == true {
							if v, ok := cell["value"]; !ok || v != nil {
								t.Errorf("row g=%d cell value = %v (present %v), want null", row, v, ok)
							}
							found = true
						}
					}
				}
				if !found {
					t.Fatalf("no present cell on row g=%d", row)
				}
				if nullKeyCount(env["data"], "ratio") == 0 {
					t.Error("no crosstab components map carries ratio: null")
				}
			})
		}
	}

	// INDEX_VS_PRIOR's first entry and INDEX_VS_ROLLING_MEAN's entries
	// before the window fills are undefined on every host.
	t.Run("series_index_overlays", func(t *testing.T) {
		req := grouped(&types.Aggregation{Type: types.AGG_SUM, Field: "x", Label: "s"})
		req.Overlays = []types.OverlaySpec{
			{Name: "i_prior", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup},
			{Name: "i_roll", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: json.RawMessage(`{"window":3}`)},
		}
		bare := *req
		bare.Overlays = nil
		svc := New(store.mem)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		env := envelopeJSON(t, resp)
		layers := env["data"].(map[string]any)["overlays"].([]any)
		for i, wantNull := range []int{1, 3} {
			entries := layers[i].(map[string]any)["payload"].(map[string]any)["series"].(map[string]any)["entries"].([]any)
			nulls := 0
			for j, e := range entries {
				stat, ok := e.(map[string]any)["summary"].(map[string]any)["statistic"]
				switch {
				case !ok:
					t.Errorf("layer %d entry %d: statistic absent, want null or a number", i, j)
				case stat == nil:
					nulls++
				}
			}
			if nulls != wantNull {
				t.Errorf("layer %d: %d null statistics, want %d", i, nulls, wantNull)
			}
		}
		// The overlay layer marshals on its own too (an embedder's
		// json.Marshal of one layer), and the host payload is untouched.
		if _, err := json.Marshal(resp.Overlays[0]); err != nil {
			t.Errorf("an OverlayLayer does not serialise on its own: %v", err)
		}
		plain, err := svc.Process(context.Background(), &bare)
		if err != nil {
			t.Fatal(err)
		}
		if g, w := mustMarshal(t, resp.Data), mustMarshal(t, plain.Data); string(g) != string(w) {
			t.Errorf("overlays changed the host payload\nwith    %s\nwithout %s", g, w)
		}
	})

	// Streamed rows (ProcessStream; the CLI's --stream NDJSON) carry the
	// same undefined figure; the untyped Row marshals through
	// types.MarshalFinite.
	t.Run("stream_rows", func(t *testing.T) {
		svc := New(store.mem)
		iter, err := svc.ProcessStream(context.Background(), grouped(ratioAgg("r")))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = iter.Close() }()
		nulls := 0
		for {
			row, ok, err := iter.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			body, err := types.MarshalFinite(row)
			if err != nil {
				t.Fatalf("a streamed row does not serialise: %v", err)
			}
			if strings.Contains(string(body), `"r":null`) {
				nulls++
			}
		}
		if nulls != 1 {
			t.Errorf("%d streamed rows carry r: null, want 1", nulls)
		}
	})
}

// nonFiniteSweepRows adds g=4, whose x and y are null on every row, so
// each aggregator also meets a group with no value at all.
func nonFiniteSweepRows(n int) []parityRow {
	rows := nonFiniteRows(n)
	for range 4 {
		rows = append(rows, parityRow{id: uint32(len(rows)), g: 4, w: 1, xNull: true, yNull: true, sNull: true})
	}
	return rows
}

// TestNonFiniteSweepMarshals: every weight-aware aggregator, weighted
// and not, on both Process arms, over groups whose inputs are degenerate
// (an all-zero denominator, all-zero weights, a single row, all-null
// values) serialises
// through the envelope. A future operator that emits NaN or ±Inf for an
// undefined figure cannot fail the response.
func TestNonFiniteSweepMarshals(t *testing.T) {
	store := newParityStore(t, "nonfinite_sweep", nonFiniteSweepRows)
	path := store.paths[paritySingleFile]
	for _, row := range parityOps {
		for _, weighted := range []bool{false, true} {
			name := string(row.op)
			if weighted {
				name += "/weighted"
			} else if row.baseline != "" {
				continue // AGG_WEIGHTED_MEAN needs a weight
			}
			t.Run(name, func(t *testing.T) {
				for _, buffered := range []bool{false, true} {
					req := parityRequest(path, row, row.op)
					req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
					if weighted {
						req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
					}
					if buffered {
						req.Aggregations = append(req.Aggregations, &types.Aggregation{
							Type: types.AGG_MEDIAN, Field: "y", Label: paritySteer + "median", Weight: types.NullSlotWeight()})
					}
					svc := New(store.mem)
					svc.SetShardWorkers(1)
					svc.SetDecodeWorkers(1)
					resp, err := svc.Process(context.Background(), req)
					if err != nil {
						t.Fatalf("Process (buffered=%v): %v", buffered, err)
					}
					envelopeJSON(t, resp)
				}
			})
		}
	}
}
