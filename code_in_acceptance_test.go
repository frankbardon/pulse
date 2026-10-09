package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E1-S4 (code-in-attribute): the end-to-end acceptance proof that
// ATTR_CODE_IN + AGG_WEIGHTED_MEAN on a weighted crosstab is the top-box
// share of the WHOLE cell base, runs on the fused arm, and is not the
// FILTER_INCLUDE figure.
//
// EXACTNESS — BIT-EXACT, not a tolerance. AGG_WEIGHTED_MEAN is Σwx/Σw
// (weightedState.scalar), and an ATTR_CODE_IN indicator is exactly 0 or
// 1, so w·x is exactly w or 0. Every weight in the fixture is a multiple
// of 1/8 below 2, and the largest cell sum stays far below 2^49, so every
// partial sum Σw and Σw·x is exactly representable in float64 whatever
// the association order (row order, fused per-cell folds, parallel
// decode merges). The only rounding is the one final division, which is
// correctly rounded on both sides. weighting.md ("Operation-order
// exactness rule") reserves tolerances for figures whose operation order
// differs; here the order cannot matter, so the assertions use ==.

const codeInCohort = "codein.pulse"

// codeInLabels is the Likert-ish code frame: 0..7 substantive, 98 don't
// know, 99 not asked. The categorical dictionary is seeded in REVERSE so
// no label equals its dictionary id ("6" is id 3, "7" id 2): a code
// resolved by id instead of label lands on the wrong rows.
var codeInLabels = []string{"0", "1", "2", "3", "4", "5", "6", "7", "98", "99"}

var (
	codeInRegions = []string{"east", "north", "south"}
	codeInWaves   = []string{"w1", "w2"}
)

// codeInRow is one fixture record; code < 0 is null in both q and qn.
type codeInRow struct {
	region, wave string
	code         int // index into codeInLabels, -1 null
	w            float64
}

func codeInRows() []codeInRow {
	const n = 360
	rows := make([]codeInRow, n)
	for i := range rows {
		code := (i*7 + i/5) % len(codeInLabels)
		if i%11 == 4 {
			code = -1
		}
		rows[i] = codeInRow{
			region: codeInRegions[i%3],
			wave:   codeInWaves[(i/3)%2],
			code:   code,
			w:      0.125 * float64(1+(i*5)%13), // dyadic: 0.125 .. 1.625
		}
	}
	return rows
}

func codeInCodeValue(c int) uint64 {
	var v uint64
	_, _ = fmt.Sscan(codeInLabels[c], &v)
	return v
}

// codeInFixture writes the cohort: region / wave (axes), q (nullable
// categorical Likert), qn (nullable u8, the same codes as integers), w
// (f64 weight).
func codeInFixture(t *testing.T, fsys afero.Fs) {
	t.Helper()
	seed := encoding.NewDictionary()
	for i := len(codeInLabels) - 1; i >= 0; i-- {
		if _, err := seed.Add(codeInLabels[i]); err != nil {
			t.Fatal(err)
		}
	}
	fields := []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "wave", Type: encoding.FieldTypeCategoricalU8},
		{Name: "q", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: seed},
		{Name: "qn", Type: encoding.FieldTypeU8, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Code-in acceptance column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), codeInCohort, encoding.Schema{Fields: fields}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range codeInRows() {
		var q, qn any
		if r.code >= 0 {
			q, qn = codeInLabels[r.code], codeInCodeValue(r.code)
		}
		if err := b.Append(pulse.CohortRow{r.region, r.wave, q, qn, r.w}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// codeInArms records the execution arm of every finished operation.
type codeInArms struct {
	mu   sync.Mutex
	arms []observe.Arm
}

func (r *codeInArms) take() []observe.Arm {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.arms
	r.arms = nil
	return out
}

// codeInInstance opens a Pulse over the fixture with an arm recorder.
func codeInInstance(t *testing.T, fsys afero.Fs, disableFusion bool) (*pulse.Pulse, *codeInArms) {
	t.Helper()
	rec := &codeInArms{}
	p, err := pulse.New(pulse.Options{FS: fsys, DisableCrosstabFusion: disableFusion, Hooks: &observe.Hooks{
		OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, res observe.OperationResult) {
			rec.mu.Lock()
			rec.arms = append(rec.arms, res.Arm)
			rec.mu.Unlock()
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return p, rec
}

// codeInExpected is the hand-computed Σw(code ∈ set)/Σw per
// (region, wave) cell over EVERY row in the cell — out-of-set, don't
// know, not asked and null rows all in the denominator.
func codeInExpected(in func(code int) bool) map[[2]string]float64 {
	return codeInExpectedBy(func(r codeInRow) [2]string { return [2]string{r.region, r.wave} }, in)
}

// codeInExpectedBy is codeInExpected over any cell key.
func codeInExpectedBy(key func(codeInRow) [2]string, in func(code int) bool) map[[2]string]float64 {
	num, den := map[[2]string]float64{}, map[[2]string]float64{}
	for _, r := range codeInRows() {
		k := key(r)
		den[k] += r.w
		if r.code >= 0 && in(r.code) {
			num[k] += r.w
		}
	}
	out := map[[2]string]float64{}
	for k, d := range den {
		out[k] = num[k] / d
	}
	return out
}

func codeInTopTwo(code int) bool { l := codeInLabels[code]; return l == "6" || l == "7" }

// codeInXtab is the weighted top-box crosstab: region × wave, the cell
// AGG_WEIGHTED_MEAN of label top2 weighted by w.
func codeInXtab(attrs []*types.Attribute, filters []*types.Filterer) *types.Request {
	return &types.Request{
		Cohort:     &types.Cohort{Filename: codeInCohort},
		Attributes: attrs,
		Filterers:  filters,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}},
			Cell: &types.Aggregation{Type: types.AGG_WEIGHTED_MEAN, Field: "top2", Label: "share",
				Params: json.RawMessage(`{"weight_field":"w"}`)},
		},
	}
}

func codeInAttr(field, codes string) []*types.Attribute {
	return []*types.Attribute{{Type: types.ATTR_CODE_IN, Field: field, Label: "top2",
		Params: json.RawMessage(`{"codes":` + codes + `}`)}}
}

func codeInFormula(expr string) []*types.Attribute {
	return []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: expr, Label: "top2"}}
}

// codeInCells flattens a matrix response to (region, wave) → value,
// failing on an absent or non-float cell.
func codeInCells(t *testing.T, resp *types.Response) map[[2]string]float64 {
	t.Helper()
	if resp == nil || resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatalf("no crosstab matrix in response")
	}
	m := resp.Crosstab.Matrix
	out := map[[2]string]float64{}
	for i, rk := range m.RowKeys {
		for j, ck := range m.ColumnKeys {
			c := m.Cells[i][j]
			v, ok := c.Value.(float64)
			if !c.Present || !ok {
				t.Fatalf("cell [%v][%v] = %+v, want a present float64", rk, ck, c)
			}
			out[[2]string{fmt.Sprint(rk[0]), fmt.Sprint(ck[0])}] = v
		}
	}
	return out
}

func codeInRequireCells(t *testing.T, what string, got, want map[[2]string]float64) {
	t.Helper()
	if len(got) != len(want) || len(want) < len(codeInRegions) {
		t.Fatalf("%s: %d cells, want %d", what, len(got), len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("%s: cell %v missing", what, k)
			continue
		}
		if g != w {
			t.Errorf("%s: cell %v = %.17g, want %.17g (bit-exact; diff %g)", what, k, g, w, g-w)
		}
	}
}

func codeInRequireArm(t *testing.T, rec *codeInArms, want observe.Arm) {
	t.Helper()
	arms := rec.take()
	if len(arms) != 1 || arms[0] != want {
		t.Fatalf("execution arms %q, want exactly [%q]", arms, want)
	}
}

// codeInFieldCases runs every assertion on the categorical field (codes
// by label) and on the u8 field (codes by value).
var codeInFieldCases = []struct {
	name, field, codes, formula string
}{
	{name: "categorical_by_label", field: "q", codes: `["6","7"]`, formula: `q in ["6", "7"] ? 1 : 0`},
	{name: "u8_by_value", field: "qn", codes: `[6, 7]`, formula: `qn in [6, 7] ? 1 : 0`},
}

// TestCodeIn_AcceptanceTopBoxExactAndFused: per field kind, the fused
// ATTR_CODE_IN cells equal the hand-computed Σw(code∈{6,7})/Σw bit for
// bit; the runtime ran the fused_crosstab arm; predict says
// CrosstabFusable == true; the buffered arm (fusion disabled) gives the
// same bits; and the fixture is not degenerate (every cell strictly
// between 0 and 1, so the FILTER_INCLUDE control can differ).
func TestCodeIn_AcceptanceTopBoxExactAndFused(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	codeInFixture(t, fsys)
	fused, fusedRec := codeInInstance(t, fsys, false)
	buffered, bufRec := codeInInstance(t, fsys, true)
	want := codeInExpected(codeInTopTwo)
	for k, v := range want {
		if !(v > 0 && v < 1) {
			t.Fatalf("degenerate fixture: cell %v share %v", k, v)
		}
	}
	for _, c := range codeInFieldCases {
		t.Run(c.name, func(t *testing.T) {
			req := func() *types.Request { return codeInXtab(codeInAttr(c.field, c.codes), nil) }

			res, err := fused.Predict(ctx, req())
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			fusedRec.take()
			if res.CrosstabFusable == nil || !*res.CrosstabFusable {
				t.Fatalf("predict CrosstabFusable = %v (reasons %q), want true", res.CrosstabFusable, res.CrosstabFusionReasons)
			}

			resp, err := fused.Process(ctx, req())
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			codeInRequireArm(t, fusedRec, observe.ArmFusedCrosstab)
			codeInRequireCells(t, "fused", codeInCells(t, resp), want)

			bresp, err := buffered.Process(ctx, req())
			if err != nil {
				t.Fatalf("buffered Process: %v", err)
			}
			if arms := bufRec.take(); len(arms) != 1 || arms[0] == observe.ArmFusedCrosstab {
				t.Fatalf("fusion-disabled arms %q, want one non-fused arm", arms)
			}
			codeInRequireCells(t, "buffered", codeInCells(t, bresp), want)
		})
	}
}

// TestCodeIn_AcceptanceFilterIncludeControl: the same request with
// FILTER_INCLUDE on the codes shrinks each cell's base to the matching
// rows, so every cell reads exactly 1.0 — and every one differs from the
// whole-base ATTR_CODE_IN share.
func TestCodeIn_AcceptanceFilterIncludeControl(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	codeInFixture(t, fsys)
	p, _ := codeInInstance(t, fsys, false)
	base := codeInExpected(codeInTopTwo)
	for _, c := range codeInFieldCases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := p.Process(ctx, codeInXtab(codeInAttr(c.field, c.codes),
				[]*types.Filterer{{Type: types.FILTER_INCLUDE, Field: c.field, Values: []string{"6", "7"}}}))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			got := codeInCells(t, resp)
			ones := map[[2]string]float64{}
			for k := range base {
				ones[k] = 1
				if got[k] == base[k] {
					t.Errorf("cell %v: FILTER_INCLUDE %v equals the whole-base share", k, got[k])
				}
			}
			codeInRequireCells(t, "filter_include", got, ones)
		})
	}
}

// TestCodeIn_AcceptanceFormulaOracle: ATTR_FORMULA `field in [...] ? 1 :
// 0` (which declines fusion, so it runs buffered) gives the same cells,
// bit for bit, as ATTR_CODE_IN on the fused arm.
func TestCodeIn_AcceptanceFormulaOracle(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	codeInFixture(t, fsys)
	p, rec := codeInInstance(t, fsys, false)
	for _, c := range codeInFieldCases {
		t.Run(c.name, func(t *testing.T) {
			oracle, err := p.Process(ctx, codeInXtab(codeInFormula(c.formula), nil))
			if err != nil {
				t.Fatalf("formula Process: %v", err)
			}
			if arms := rec.take(); len(arms) != 1 || arms[0] == observe.ArmFusedCrosstab {
				t.Fatalf("formula arms %q, want one non-fused arm", arms)
			}
			got, err := p.Process(ctx, codeInXtab(codeInAttr(c.field, c.codes), nil))
			if err != nil {
				t.Fatalf("code-in Process: %v", err)
			}
			codeInRequireArm(t, rec, observe.ArmFusedCrosstab)
			codeInRequireCells(t, "code_in vs formula", codeInCells(t, got), codeInCells(t, oracle))
		})
	}
}

// TestCodeIn_AcceptanceWeightedTwoMeansZOverlay: the weighted two-means
// z overlay (kish basis — AGG_WEIGHTED_MEAN's weight_field is a
// probability weight) composes over the ATTR_CODE_IN target and yields a
// finite p-value in every pair cell.
func TestCodeIn_AcceptanceWeightedTwoMeansZOverlay(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	codeInFixture(t, fsys)
	p, _ := codeInInstance(t, fsys, false)
	for _, c := range codeInFieldCases {
		t.Run(c.name, func(t *testing.T) {
			req := codeInXtab(codeInAttr(c.field, c.codes), nil)
			req.Overlays = []types.OverlaySpec{{Name: "wz", Kind: types.OverlayKindPairwiseWeightedTwoMeansZ,
				Scope: types.OverlayScopeRow, Params: json.RawMessage(`{"n_basis":"kish"}`)}}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil {
				t.Fatalf("overlays %+v, want one matrix layer", resp.Overlays)
			}
			m := resp.Overlays[0].Payload.Matrix
			values := 0
			for _, row := range m.Cells {
				for _, cell := range row {
					if !cell.Present {
						continue
					}
					v, ok := cell.Value.(float64)
					if !ok || math.IsNaN(v) || v < 0 || v > 1 {
						t.Errorf("overlay cell %+v, want a p-value in [0,1]", cell)
						continue
					}
					values++
				}
			}
			// row scope: C(3,2) region pairs × 2 waves.
			if values != 6 {
				t.Errorf("overlay produced %d p-values, want 6", values)
			}
		})
	}
}

// TestCodeIn_AcceptanceStreamMatchesBuffered: ProcessStream returns the
// same rows as Process — on the default instance (where Process itself
// takes a fused / online arm and evaluates ATTR_CODE_IN through Row) and
// on a fusion-disabled instance (whose buffered arm evaluates it through
// Compute) — for the long-shape crosstab and for the grouped request by
// region (the plain grouped path reads one grouper); all carry the
// hand-computed shares.
func TestCodeIn_AcceptanceStreamMatchesBuffered(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	codeInFixture(t, fsys)
	p, _ := codeInInstance(t, fsys, false)
	buffered, _ := codeInInstance(t, fsys, true)
	wants := map[string]map[[2]string]float64{
		"crosstab_long": codeInExpected(codeInTopTwo),
		"grouped":       codeInExpectedBy(func(r codeInRow) [2]string { return [2]string{r.region, "<nil>"} }, codeInTopTwo),
	}
	for _, c := range codeInFieldCases {
		long := func() *types.Request {
			r := codeInXtab(codeInAttr(c.field, c.codes), nil)
			r.Crosstab.Shape = types.CrosstabShapeLong
			return r
		}
		grouped := func() *types.Request {
			return &types.Request{
				Cohort:     &types.Cohort{Filename: codeInCohort},
				Attributes: codeInAttr(c.field, c.codes),
				Groups:     []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_WEIGHTED_MEAN, Field: "top2", Label: "share",
					Params: json.RawMessage(`{"weight_field":"w"}`)}},
			}
		}
		for shape, mk := range map[string]func() *types.Request{"crosstab_long": long, "grouped": grouped} {
			t.Run(c.name+"/"+shape, func(t *testing.T) {
				want := wants[shape]
				resp, err := p.Process(ctx, mk())
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				bresp, err := buffered.Process(ctx, mk())
				if err != nil {
					t.Fatalf("buffered Process: %v", err)
				}
				iter, err := p.ProcessStream(ctx, mk())
				if err != nil {
					t.Fatalf("ProcessStream: %v", err)
				}
				defer func() { _ = iter.Close() }()
				var streamed []map[string]any
				for {
					row, ok, err := iter.Next(ctx)
					if err != nil {
						t.Fatalf("Next: %v", err)
					}
					if !ok {
						break
					}
					streamed = append(streamed, row)
				}
				if len(resp.Data) != len(want) {
					t.Fatalf("buffered rows %d, want %d: %v", len(resp.Data), len(want), resp.Data)
				}
				for arm, data := range map[string][]map[string]any{"default": resp.Data, "fusion_disabled": bresp.Data} {
					if !reflect.DeepEqual(codeInSortRows(streamed), codeInSortRows(data)) {
						t.Fatalf("stream rows\n%v\n%s Process rows\n%v", streamed, arm, data)
					}
				}
				got := map[[2]string]float64{}
				for _, row := range resp.Data {
					v, _ := row["share"].(float64)
					got[[2]string{fmt.Sprint(row["region"]), fmt.Sprint(row["wave"])}] = v
				}
				codeInRequireCells(t, "buffered "+shape, got, want)
			})
		}
	}
}

func codeInSortRows(rows []map[string]any) []map[string]any {
	out := append([]map[string]any(nil), rows...)
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out
}
