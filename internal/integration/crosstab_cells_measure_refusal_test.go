package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// crosstab_cells_measure_refusal_test.go pins U66 #476: a moment
// pairwise kind (OVERLAY_PAIRWISE_WELCH_T / _TWO_MEANS_Z) over a
// multi-measure crosstab whose measures are of mixed type refuses the
// measure that cannot carry the AGG_WELFORD triple ALONE — its folded
// rows are null cells, a warning names it, and every other measure
// scores exactly as its single-measure crosstab would. Only when every
// measure is refused does the whole layer refuse, as before.

// mixedCells is a Welford measure, a count (refused by the moment
// kinds) and a second Welford measure: the refused one sits between two
// scorers, so neither the first nor the last measure index is special.
func mixedCells() []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_WELFORD, Field: "score", Label: "s"},
		{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		{Type: types.AGG_WELFORD, Field: "wt", Label: "w"},
	}
}

const mixedRefused = 1

func mixedSpec(cells []*types.Aggregation) *types.CrosstabSpec {
	return &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Columns: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
		Cells:   cells,
	}
}

// measureRefusalWarnings are the warnings of ws naming a refused measure.
func measureRefusalWarnings(ws []types.OverlayWarning) []types.OverlayWarning {
	var out []types.OverlayWarning
	for _, w := range ws {
		if _, ok := w.Details["measure"]; ok {
			out = append(out, w)
		}
	}
	return out
}

func letterCell(t *testing.T, c types.MatrixCell) types.PairwiseLetterCell {
	t.Helper()
	v, ok := c.Value.(types.PairwiseLetterCell)
	if !c.Present || !ok {
		t.Fatalf("letters cell %+v is not a present PairwiseLetterCell", c)
	}
	return v
}

// TestCrosstabCells_PairwiseMixedMeasureRefusedAlone pins the per-measure
// refusal on both dispatch arms, ROW and COLUMN scope, with and without
// significance letters, for both moment kinds.
func TestCrosstabCells_PairwiseMixedMeasureRefusedAlone(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	cells := mixedCells()
	M := len(cells)
	for _, disable := range []bool{false, true} {
		p := cellsInstance(t, fs, disable)
		for _, kind := range []types.OverlayKind{types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ} {
			for _, scope := range []types.OverlayScope{types.OverlayScopeColumn, types.OverlayScopeRow} {
				for _, letters := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/letters_%v/disable_fusion_%v", kind, scope, letters, disable)
					t.Run(name, func(t *testing.T) {
						spec := types.OverlaySpec{Kind: kind, Scope: scope}
						if letters {
							spec.Params = json.RawMessage(`{"letters":{"alpha":0.5}}`)
						}
						req := cellsRequest(mixedSpec(cells), nil)
						req.Overlays = []types.OverlaySpec{spec}
						multi, err := p.Process(ctx, req)
						if err != nil {
							t.Fatalf("a mixed measure set refused the whole layer: %v", err)
						}
						layer := multi.Overlays[0]
						got := layer.Payload.Matrix

						// The warning: one, naming the refused measure, on the
						// layer and on the response.
						lw := measureRefusalWarnings(layer.Warnings)
						if len(lw) != 1 {
							t.Fatalf("layer measure warnings = %+v, want exactly one", lw)
						}
						d := lw[0].Details
						if lw[0].Code != string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) ||
							d["measure"] != "n" || d["measure_index"] != mixedRefused || d["reason"] == nil ||
							d["cell_type"] != string(types.AGG_COUNT) || d["kind"] != string(kind) {
							t.Errorf("measure warning = %s %v", lw[0].Code, d)
						}
						onResp := 0
						for _, w := range multi.Warnings {
							if w.Code == lw[0].Code && w.Details["measure"] == "n" {
								onResp++
							}
						}
						if onResp != 1 {
							t.Errorf("response carries %d measure warnings, want 1", onResp)
						}

						var gotLetters *types.MatrixPayload
						if letters {
							lp := layer.Payload.Parts["letters"]
							if lp == nil || lp.Matrix == nil {
								t.Fatal("letters part missing")
							}
							gotLetters = lp.Matrix
						}
						rowsOut, count, ranked := 0, 0, false
						// The skip tally is the scored measures' alone: a
						// refused measure is never evaluated pair by pair.
						skipped := map[string]int{}
						for m := range cells {
							single := mixedSpec(nil)
							single.Cell = cells[m]
							sreq := cellsRequest(single, nil)
							sreq.Overlays = []types.OverlaySpec{spec}
							if m == mixedRefused {
								// The single-measure crosstab of the refused
								// measure is refused: what nulls its rows here.
								if _, err := p.Process(ctx, sreq); !errors.HasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
									t.Fatalf("single refused measure err = %v", err)
								}
								continue
							}
							sresp, err := p.Process(ctx, sreq)
							if err != nil {
								t.Fatal(err)
							}
							want := sresp.Overlays[0].Payload.Matrix
							count += *sresp.Overlays[0].Summary.Count
							for _, w := range sresp.Overlays[0].Warnings {
								skipped[w.Details["reason"].(string)] += w.Details["skipped"].(int)
							}
							var wantLetters *types.MatrixPayload
							if letters {
								wantLetters = sresp.Overlays[0].Payload.Parts["letters"].Matrix
								for r := range wantLetters.Cells {
									if r*M+m >= len(gotLetters.Cells) {
										t.Fatalf("letters rows = %d, want > %d", len(gotLetters.Cells), r*M+m)
									}
									for c := range wantLetters.Cells[r] {
										g := letterCell(t, gotLetters.Cells[r*M+m][c])
										w := letterCell(t, wantLetters.Cells[r][c])
										if g.Letter != w.Letter || mustJSON(t, g.Higher) != mustJSON(t, w.Higher) || mustJSON(t, g.Above) != mustJSON(t, w.Above) {
											t.Errorf("measure %d letters [%d][%d] = %+v, want %+v", m, r, c, g, w)
										}
										if len(w.Higher) > 0 {
											ranked = true
										}
									}
								}
							}
							if scope == types.OverlayScopeColumn {
								for r := range want.Cells {
									if g, w := mustJSON(t, got.Cells[r*M+m]), mustJSON(t, want.Cells[r]); g != w {
										t.Errorf("measure %d row %d p = %s, want %s", m, r, g, w)
									}
								}
								continue
							}
							if m > mixedRefused {
								rowsOut += len(want.Cells) // the refused measure's pair rows sit before
							}
							for pi := range want.Cells {
								at := rowsOut + pi
								if g, w := mustJSON(t, got.Cells[at]), mustJSON(t, want.Cells[pi]); g != w {
									t.Errorf("measure %d pair %d p = %s, want %s", m, pi, g, w)
								}
							}
							rowsOut += len(want.Cells)
						}
						if letters && !ranked {
							t.Fatal("vacuous: no scored letters cell is higher than another")
						}
						gotSkipped := map[string]int{}
						for _, w := range layer.Warnings {
							if _, ok := w.Details["measure"]; !ok {
								gotSkipped[w.Details["reason"].(string)] += w.Details["skipped"].(int)
							}
						}
						if mustJSON(t, gotSkipped) != mustJSON(t, skipped) {
							t.Errorf("skip tally = %v, want the scored measures' %v", gotSkipped, skipped)
						}
						if *layer.Summary.Count != count || count == 0 {
							t.Errorf("summary count = %d, want %d (non-zero)", *layer.Summary.Count, count)
						}

						// The refused measure's rows: null cells.
						nullRows := 0
						if scope == types.OverlayScopeColumn {
							for r := 0; r*M+mixedRefused < len(got.Cells); r++ {
								row := got.Cells[r*M+mixedRefused]
								if len(row) != len(got.ColumnKeys) || len(row) == 0 {
									t.Fatalf("refused row %d has %d cells, want %d", r, len(row), len(got.ColumnKeys))
								}
								for _, c := range row {
									if c.Present {
										t.Errorf("refused measure row %d holds a p-value %v", r, c.Value)
									}
								}
								nullRows++
							}
						} else {
							if len(got.Cells) != len(got.RowKeys) {
								t.Fatalf("pair rows %d vs keys %d", len(got.Cells), len(got.RowKeys))
							}
							for at, key := range got.RowKeys {
								legs := 0
								for _, leg := range key {
									if s, _ := leg.(string); strings.HasSuffix(s, "n") {
										legs++
									}
								}
								if legs == 0 {
									continue
								}
								if legs != 2 {
									t.Fatalf("pair %d crosses measures: %v", at, key)
								}
								for _, c := range got.Cells[at] {
									if c.Present {
										t.Errorf("refused measure pair %d holds a p-value %v", at, c.Value)
									}
								}
								nullRows++
							}
						}
						if nullRows == 0 {
							t.Fatal("vacuous: the refused measure laid no null row")
						}

						// Letters skip the refused measure: its cells keep a
						// letter but are never higher or above anything.
						if letters {
							for r := range gotLetters.Cells {
								if r%M != mixedRefused {
									continue
								}
								for _, c := range gotLetters.Cells[r] {
									if !c.Present {
										continue
									}
									v := letterCell(t, c)
									if len(v.Higher) != 0 || len(v.Above) != 0 {
										t.Errorf("refused measure letters row %d = %+v, want no comparison", r, v)
									}
								}
							}
						}
					})
				}
			}
		}
	}
}

// TestCrosstabCells_PairwiseMixedMeasureMultiplicity pins that the
// multiplicity fold never counts the refused measure's null pairs: the
// layer family's m and excluded are the scored measures' alone, the
// adjusted twins stay null on the refused rows, and the letters refresh
// on the adjusted figures succeeds.
func TestCrosstabCells_PairwiseMixedMeasureMultiplicity(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	p := cellsInstance(t, fs, false)
	cells := mixedCells()
	M := len(cells)
	for _, scope := range []types.OverlayScope{types.OverlayScopeColumn, types.OverlayScopeRow} {
		t.Run(string(scope), func(t *testing.T) {
			spec := types.OverlaySpec{Kind: types.OverlayKindPairwiseWelchT, Scope: scope,
				Params:       json.RawMessage(`{"letters":{}}`),
				Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodBonferroni, Family: types.MultiplicityFamilyLayer}}
			req := cellsRequest(mixedSpec(cells), nil)
			req.Overlays = []types.OverlaySpec{spec}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			layer := resp.Overlays[0]
			if layer.Multiplicity == nil || layer.Payload.PAdjusted == nil {
				t.Fatal("no multiplicity correction ran")
			}
			defined, undefined := 0, 0
			for r, row := range layer.Payload.Matrix.Cells {
				for c, cell := range row {
					refused := (scope == types.OverlayScopeColumn && r%M == mixedRefused)
					if scope == types.OverlayScopeRow {
						for _, leg := range layer.Payload.Matrix.RowKeys[r] {
							if s, _ := leg.(string); strings.HasSuffix(s, "n") {
								refused = true
							}
						}
					}
					if refused {
						if cell.Present || layer.Payload.PAdjusted.Cells[r][c].Present {
							t.Errorf("refused [%d][%d] p = %+v adjusted = %+v, want null", r, c, cell, layer.Payload.PAdjusted.Cells[r][c])
						}
						continue
					}
					if !cell.Present {
						continue
					}
					if v, _ := cell.Value.(float64); math.IsNaN(v) {
						undefined++
					} else {
						defined++
					}
				}
			}
			if defined == 0 {
				t.Fatal("vacuous: no scored p-value")
			}
			if layer.Multiplicity.M != defined || layer.Multiplicity.Excluded != undefined {
				t.Errorf("multiplicity m = %d excluded = %d, want %d / %d", layer.Multiplicity.M, layer.Multiplicity.Excluded, defined, undefined)
			}
			if layer.Payload.Parts["letters"] == nil {
				t.Error("letters part missing after the fold's refresh")
			}
		})
	}
}

// TestCrosstabCells_PairwiseMeasureRefusalPredictMatchesRuntime pins
// predict ≡ runtime: a mixed measure set predicts clean with the same
// per-measure warning the runtime raises (code, measure, index, reason,
// cell type, message), and a set where every measure is refused is the
// whole-layer refusal on both.
func TestCrosstabCells_PairwiseMeasureRefusalPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	for _, disable := range []bool{false, true} {
		p := cellsInstance(t, fs, disable)
		for _, kind := range []types.OverlayKind{types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ} {
			t.Run(fmt.Sprintf("%s/mixed/disable_fusion_%v", kind, disable), func(t *testing.T) {
				cells := append(mixedCells(), &types.Aggregation{Type: types.AGG_SUM, Field: "wt", Label: "total"})
				req := cellsRequest(mixedSpec(cells), nil)
				req.Overlays = []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeColumn}}
				env := predictEnvelope(t, p, fs, cellsCohortPath, req)
				if len(env.Errors) != 0 {
					t.Fatalf("predict refused a mixed measure set: %q", envCodes(env))
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				rw := measureRefusalWarnings(resp.Overlays[0].Warnings)
				var pw []map[string]any
				var pmsg []string
				for _, w := range env.Warnings {
					if _, ok := w.Details["measure"]; ok && w.Code == string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
						pw = append(pw, w.Details)
						pmsg = append(pmsg, w.Message)
					}
				}
				if len(rw) != 2 || len(pw) != len(rw) {
					t.Fatalf("runtime measure warnings %d, predict %d; want 2 each", len(rw), len(pw))
				}
				for i := range rw {
					for _, k := range []string{"kind", "measure", "measure_index", "reason", "cell_type"} {
						if fmt.Sprint(rw[i].Details[k]) != fmt.Sprint(pw[i][k]) {
							t.Errorf("warning %d %s: runtime %v, predict %v", i, k, rw[i].Details[k], pw[i][k])
						}
					}
					if rw[i].Message != pmsg[i] || rw[i].Code != string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
						t.Errorf("warning %d: runtime %s %q, predict %q", i, rw[i].Code, rw[i].Message, pmsg[i])
					}
					if pw[i]["index"] != 0 {
						t.Errorf("predict warning %d index = %v, want 0", i, pw[i]["index"])
					}
				}
				if pw[0]["measure"] != "n" || pw[1]["measure"] != "total" {
					t.Errorf("refused measures = %v, %v; want n, total", pw[0]["measure"], pw[1]["measure"])
				}
			})
			t.Run(fmt.Sprintf("%s/every_measure_refused/disable_fusion_%v", kind, disable), func(t *testing.T) {
				req := cellsRequest(mixedSpec([]*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
					{Type: types.AGG_SUM, Field: "wt", Label: "total"},
				}), nil)
				req.Overlays = []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeRow}}
				env := predictEnvelope(t, p, fs, cellsCohortPath, req)
				_, err := p.Process(ctx, req)
				if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
					t.Errorf("predict errors = %q, want %s", envCodes(env), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
				}
				if !errors.HasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
					t.Errorf("runtime err = %v, want %s", err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
				}
				for _, w := range env.Warnings {
					if _, ok := w.Details["measure"]; ok {
						t.Errorf("a whole-layer refusal also warned per measure: %+v", w)
					}
				}
			})
		}
	}
}
