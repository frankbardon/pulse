package pulse_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// pairwise_letters_test.go pins significance letters on the pairwise
// overlays (U47 E3-S1 / #327): params.letters adds the `letters` part
// (host coordinates: each compared index's letter, group, the letters it
// is higher than — the kernel's direction — and the subset it is
// significantly ABOVE), honouring the multiplicity correction and alpha.

const lettersCohortPath = "letters.pulse"

// writeLettersCohort lays out the hand-computable 3-column table: col
// X / Y / Z, 100 rows each, ans = yes for the first 60 / 45 / 30 rows
// (so the yes shares are 0.60 / 0.45 / 0.30). wave alternates w1 / w2
// within each column. wt is a frequency weight: 2 on every Y yes row, 1
// elsewhere — weighted, Y's yes share is 90/145 and its base N* 145, so
// Y moves ABOVE X (not significantly).
func writeLettersCohort(t *testing.T, fs afero.Fs) {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "col", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: dict("X", "Y", "Z")},
		{Name: "ans", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: dict("yes", "no")},
		{Name: "wave", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 2, Dictionary: dict("w1", "w2")},
		{Name: "wt", Type: encoding.FieldTypeF64, ByteOffset: 3},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	stride := schema.RecordByteSize()
	yes := []int{60, 45, 30}
	for c := 0; c < 3; c++ {
		for j := 0; j < 100; j++ {
			rec := make([]byte, stride)
			rec[0] = byte(c)
			isYes := j < yes[c]
			if !isYes {
				rec[1] = 1
			}
			rec[2] = byte(j % 2)
			w := 1.0
			if c == 1 && isYes {
				w = 2
			}
			binary.LittleEndian.PutUint64(rec[3:11], math.Float64bits(w))
			buf.Write(rec)
		}
	}
	if err := afero.WriteFile(fs, lettersCohortPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lettersPulse(t *testing.T, disableFusion bool) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	writeLettersCohort(t, fs)
	p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disableFusion})
	if err != nil {
		t.Fatal(err)
	}
	return p, fs
}

// lettersRequest is ans (rows) x col (columns), a column-normalised
// count, with one OVERLAY_PAIRWISE_PROP_Z at COLUMN scope carrying
// params.
func lettersRequest(params string, w *types.WeightSpec, mult *types.Multiplicity) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: lettersCohortPath},
		Weight: w,
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "ans"}},
			Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "wt", Label: "n"},
			Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Normalize: types.CrosstabNormalizeColumn,
		},
		Overlays: []types.OverlaySpec{{
			Name: "pw", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn,
			Params: json.RawMessage(params), Multiplicity: mult,
		}},
	}
}

// lettersOf reads a layer's letters part as [hostRow][hostCol] -> the
// cell (nil when absent).
func lettersOf(t *testing.T, layer types.OverlayLayer) [][]*types.PairwiseLetterCell {
	t.Helper()
	part := layer.Payload.Parts["letters"]
	if part == nil || part.Matrix == nil {
		t.Fatalf("layer %s carries no letters part: %+v", layer.Name, layer.Payload.Parts)
	}
	out := make([][]*types.PairwiseLetterCell, len(part.Matrix.Cells))
	for r, row := range part.Matrix.Cells {
		out[r] = make([]*types.PairwiseLetterCell, len(row))
		for c, cell := range row {
			if !cell.Present {
				continue
			}
			v, ok := cell.Value.(types.PairwiseLetterCell)
			if !ok {
				t.Fatalf("letters[%d][%d] = %T, want types.PairwiseLetterCell", r, c, cell.Value)
			}
			out[r][c] = &v
		}
	}
	return out
}

// aboveGrid flattens a letters part to [row][col] "above" lists.
func aboveGrid(t *testing.T, layer types.OverlayLayer) [][][]string {
	t.Helper()
	ls := lettersOf(t, layer)
	out := make([][][]string, len(ls))
	for r := range ls {
		out[r] = make([][]string, len(ls[r]))
		for c, cell := range ls[r] {
			if cell != nil {
				out[r][c] = cell.Above
			}
		}
	}
	return out
}

// propZOracle is the pooled two-proportion z-test written out by hand:
// z = (p1 - p2) / sqrt(pool (1 - pool) (1/n1 + 1/n2)), p = erfc(|z|/√2).
func propZOracle(p1, n1, p2, n2 float64) (pValue float64, dir float64) {
	pool := (p1*n1 + p2*n2) / (n1 + n2)
	z := (p1 - p2) / math.Sqrt(pool*(1-pool)*(1/n1+1/n2))
	return math.Erfc(math.Abs(z) / math.Sqrt2), math.Copysign(1, z)
}

// TestPairwiseLetters_Oracle3Columns: on the hand-computed 3-column
// table every pair p-value and direction matches the written-out
// two-proportion z-test, and the letters follow raw p < 0.05 — X (A)
// above Y (B) and Z (C), Y above Z on the yes row, the mirror on the no
// row. Weighted (frequency), Y's share and base move: X vs Y turns
// round and loses significance, so only C is beaten. Both crosstab arms
// (fused, buffered) answer identically.
func TestPairwiseLetters_Oracle3Columns(t *testing.T) {
	t.Parallel()

	freq := &types.WeightSpec{Field: "wt", Kind: types.WeightKindFrequency}
	type want struct {
		shares [2][3]float64 // [no,yes][X,Y,Z] — row keys sort no < yes
		bases  [3]float64
		above  [][][]string
	}
	cases := map[string]struct {
		w    *types.WeightSpec
		want want
	}{
		"unweighted": {nil, want{
			shares: [2][3]float64{{0.40, 0.55, 0.70}, {0.60, 0.45, 0.30}},
			bases:  [3]float64{100, 100, 100},
			above: [][][]string{
				{{}, {"A"}, {"A", "B"}},
				{{"B", "C"}, {"C"}, {}},
			},
		}},
		"frequency": {freq, want{
			shares: [2][3]float64{{0.40, 55.0 / 145, 0.70}, {0.60, 90.0 / 145, 0.30}},
			bases:  [3]float64{100, 145, 100},
			above: [][][]string{
				{{}, {}, {"A", "B"}},
				{{"C"}, {"C"}, {}},
			},
		}},
	}
	pairs := [][2]int{{0, 1}, {0, 2}, {1, 2}}
	for name, tc := range cases {
		var arms []string
		for _, disable := range []bool{false, true} {
			t.Run(name, func(t *testing.T) {
				p, _ := lettersPulse(t, disable)
				resp, err := p.Process(context.Background(), lettersRequest(`{"letters":{}}`, tc.w, nil))
				if err != nil {
					t.Fatal(err)
				}
				layer := resp.Overlays[0]
				pm := layer.Payload.Matrix
				if got := pm.RowKeys; len(got) != 2 || got[0][0] != "no" || got[1][0] != "yes" {
					t.Fatalf("row keys = %v, want [no] [yes]", got)
				}
				ls := lettersOf(t, layer)
				names := []string{"A", "B", "C"}
				for r := 0; r < 2; r++ {
					for k, pr := range pairs {
						wantP, wantDir := propZOracle(tc.want.shares[r][pr[0]], tc.want.bases[pr[0]],
							tc.want.shares[r][pr[1]], tc.want.bases[pr[1]])
						gotP, _ := pm.Cells[r][k].Value.(float64)
						if math.Abs(gotP-wantP) > 1e-9*math.Max(1, wantP) {
							t.Errorf("row %d pair %v p = %v, oracle %v", r, pr, gotP, wantP)
						}
						// The kernel's sign surfaces as Higher: the higher
						// leg lists the lower one's letter, never the reverse.
						hi, lo := pr[0], pr[1]
						if wantDir < 0 {
							hi, lo = lo, hi
						}
						if !slices.Contains(ls[r][hi].Higher, names[lo]) || slices.Contains(ls[r][lo].Higher, names[hi]) {
							t.Errorf("row %d pair %v: higher %v / %v, oracle direction %v", r, pr, ls[r][pr[0]].Higher, ls[r][pr[1]].Higher, wantDir)
						}
					}
				}
				if got := aboveGrid(t, layer); !reflect.DeepEqual(got, tc.want.above) {
					t.Errorf("letters above = %v, want %v", got, tc.want.above)
				}
				for c, l := range []string{"A", "B", "C"} {
					if cell := lettersOf(t, layer)[0][c]; cell == nil || cell.Letter != l || cell.Group != 0 {
						t.Errorf("column %d letter cell = %+v, want %s group 0", c, cell, l)
					}
				}
				b, err := json.Marshal(layer)
				if err != nil {
					t.Fatal(err)
				}
				arms = append(arms, string(b))
			})
		}
		if len(arms) == 2 && arms[0] != arms[1] {
			t.Errorf("%s: fused and buffered layers differ:\n%s\n%s", name, arms[0], arms[1])
		}
	}
}

// TestPairwiseLetters_HonourCorrection: under a multiplicity correction
// the letters read the adjusted significance, never the raw p. The six
// layer p-values are {2.0e-5, 0.0285, 0.0337} twice: Holm keeps only
// X vs Z; BH at alpha 0.05 keeps all three, at multiplicity alpha 0.03
// only X vs Z; an explicit letters.alpha applies to p_adjusted (Holm's
// 0.114 < 0.2, so every pair) and is echoed as letters_alpha.
func TestPairwiseLetters_HonourCorrection(t *testing.T) {
	t.Parallel()

	// Rows: no, yes.
	onlyXZ := [][][]string{{{}, {}, {"A"}}, {{"C"}, {}, {}}}
	all := [][][]string{{{}, {"A"}, {"A", "B"}}, {{"B", "C"}, {"C"}, {}}}
	layerFam := types.MultiplicityFamilyLayer
	cases := []struct {
		name   string
		params string
		mult   *types.Multiplicity
		want   [][][]string
		alpha  float64 // echoed letters_alpha; 0 = absent
	}{
		{"holm", `{"letters":{}}`, &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: layerFam}, onlyXZ, 0},
		{"bh", `{"letters":{}}`, &types.Multiplicity{Method: types.MultiplicityMethodBH, Family: layerFam}, all, 0},
		{"bh_alpha_0.03", `{"letters":{}}`, &types.Multiplicity{Method: types.MultiplicityMethodBH, Family: layerFam, Alpha: 0.03}, onlyXZ, 0},
		{"holm_letters_alpha_0.2", `{"letters":{"alpha":0.2}}`, &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: layerFam}, all, 0.2},
		{"raw_letters_alpha_0.03", `{"letters":{"alpha":0.03}}`, nil, [][][]string{{{}, {}, {"A", "B"}}, {{"C"}, {"C"}, {}}}, 0.03},
	}
	p, _ := lettersPulse(t, false)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := p.Process(context.Background(), lettersRequest(tc.params, nil, tc.mult))
			if err != nil {
				t.Fatal(err)
			}
			layer := resp.Overlays[0]
			if tc.mult != nil && layer.Payload.PAdjusted == nil {
				t.Fatal("vacuous: no correction ran on the layer")
			}
			if got := aboveGrid(t, layer); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("letters above = %v, want %v", got, tc.want)
			}
			got, ok := layer.Summary.Parameters["letters_alpha"]
			if (tc.alpha != 0) != ok || got != tc.alpha {
				t.Errorf("letters_alpha = %v (present %v), want %v", got, ok, tc.alpha)
			}
		})
	}
}

// TestPairwiseLetters_ComposeMatchesProcess: a Compose slot's letters
// are refreshed after the barrier fold exactly as a standalone Process
// refreshes them after its own.
func TestPairwiseLetters_ComposeMatchesProcess(t *testing.T) {
	t.Parallel()

	p, _ := lettersPulse(t, false)
	ctx := context.Background()
	mult := &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyLayer}
	resp, err := p.Process(ctx, lettersRequest(`{"letters":{}}`, nil, mult))
	if err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error){
		"Compose": func(c *pulse.ComposedRequest) (*pulse.ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, pulse.ComposeOptions{MaxWorkers: 2})
		},
	} {
		slot := lettersRequest(`{"letters":{}}`, nil, mult)
		slot.Label = "s"
		out, err := run(&pulse.ComposedRequest{Requests: []*pulse.Request{slot}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, want := aboveGrid(t, out.Responses[0].Overlays[0]), aboveGrid(t, resp.Overlays[0]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s letters %v, Process %v", name, got, want)
		}
	}
}

// TestPairwiseLetters_GroupsTotalAndMeasures: letters restart in every
// pair_along_dim group (each wave's X / Y / Z is A / B / C); the
// implicit Total column (no columns) is compared to nothing — no cell
// carries a letter; a multi-measure host letters each measure's stub
// rows exactly as that measure's own crosstab does.
func TestPairwiseLetters_GroupsTotalAndMeasures(t *testing.T) {
	t.Parallel()

	p, _ := lettersPulse(t, false)
	ctx := context.Background()

	t.Run("pair_along_dim", func(t *testing.T) {
		req := lettersRequest(`{"letters":{},"pair_along_dim":1}`, nil, nil)
		req.Crosstab.Columns = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}, {Type: types.GROUP_CATEGORY, Field: "col"}}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		row := lettersOf(t, resp.Overlays[0])[0]
		if len(row) != 6 {
			t.Fatalf("want 6 wave x col columns, got %d", len(row))
		}
		for c, cell := range row {
			wantLetter, wantGroup := string(rune('A'+c%3)), c/3
			if cell == nil || cell.Letter != wantLetter || cell.Group != wantGroup {
				t.Errorf("column %d = %+v, want %s group %d", c, cell, wantLetter, wantGroup)
			}
		}
	})

	t.Run("implicit_total", func(t *testing.T) {
		req := lettersRequest(`{"letters":{}}`, nil, nil)
		req.Crosstab.Columns = nil
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for r, row := range lettersOf(t, resp.Overlays[0]) {
			for c, cell := range row {
				if cell != nil {
					t.Errorf("Total column cell [%d][%d] carries a letter: %+v", r, c, cell)
				}
			}
		}
	})

	t.Run("measures", func(t *testing.T) {
		single, err := p.Process(ctx, lettersRequest(`{"letters":{}}`, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		req := lettersRequest(`{"letters":{}}`, nil, nil)
		req.Crosstab.Cell = nil
		req.Crosstab.Cells = []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "wt", Label: "n"},
			{Type: types.AGG_COUNT, Field: "ans", Label: "n2"},
		}
		multi, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		want := aboveGrid(t, single.Overlays[0])
		got := aboveGrid(t, multi.Overlays[0])
		if len(got) != 2*len(want) {
			t.Fatalf("multi-measure letters rows = %d, want %d", len(got), 2*len(want))
		}
		for r := range want {
			for m := 0; m < 2; m++ {
				if !reflect.DeepEqual(got[r*2+m], want[r]) {
					t.Errorf("measure %d row %d letters %v, single-measure %v", m, r, got[r*2+m], want[r])
				}
			}
		}
	})
}

// TestPairwiseLetters_AbsentIsByteIdentical: without params.letters the
// layer carries no part and no letters_alpha.
func TestPairwiseLetters_AbsentIsByteIdentical(t *testing.T) {
	t.Parallel()

	p, _ := lettersPulse(t, false)
	resp, err := p.Process(context.Background(), lettersRequest(`{}`, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if layer := resp.Overlays[0]; layer.Payload.Parts != nil || layer.Summary.Parameters != nil {
		t.Fatalf("letter-free layer grew parts / parameters: %+v %+v", layer.Payload.Parts, layer.Summary.Parameters)
	}
}

// TestPairwiseLetters_ParamRefusalParity: a letters.alpha outside
// (0, 1), an unknown key inside letters, or a non-object letters is
// PULSE_OVERLAY_PARAM_MISSING with one message at predict and runtime,
// on a proportion kind and a Welford kind.
func TestPairwiseLetters_ParamRefusalParity(t *testing.T) {
	t.Parallel()

	p, fs := lettersPulse(t, false)
	ctx := context.Background()
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwisePropZ, types.OverlayKindPairwiseWelchT} {
		for _, params := range []string{
			`{"letters":{"alpha":0}}`,
			`{"letters":{"alpha":1}}`,
			`{"letters":{"alpha":-0.1}}`,
			`{"letters":{"alpha":1.5}}`,
			`{"letters":{"alfa":0.1}}`,
			`{"letters":true}`,
		} {
			t.Run(string(kind)+"/"+params, func(t *testing.T) {
				req := lettersRequest(params, nil, nil)
				req.Overlays[0].Kind = kind
				if kind == types.OverlayKindPairwiseWelchT {
					req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_WELFORD, Field: "wt", Label: "m"}
					req.Crosstab.Normalize = types.CrosstabNormalizeNone
				}
				_, err := p.Process(ctx, req)
				ce := requireCodedError(t, err, errors.PULSE_OVERLAY_PARAM_MISSING)
				env := predictEnvelope(t, p, fs, lettersCohortPath, req)
				if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
					t.Fatalf("predict %+v, runtime %s %q", env.Errors, ce.Code, ce.Message)
				}
			})
		}
	}
}
