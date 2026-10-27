package pulse_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// filter_to_file_identity_test.go pins U34 E3-S6: the filter-to-file
// dedup hash keys on the CONTENT of every registered table the predicate
// references — a RangeTable a FILTER_DATE_RANGES names, a LookupTable a
// lookup() names — so two instances whose same-named tables differ
// never share an output, while identical contents still reuse and an
// unreferenced table never moves the hash. A predicate running embedder
// code (an extension filterer, an ExprFunction, a function-driven
// lookup table) is never reused. Before, the hash carried only the
// table's NAME: an instance silently reused another's output.

// fidRange is a FILTER_DATE_RANGES table over calendar rows lo..hi.
func fidRange(lo, hi int) pulse.RangeTable {
	iso := func(day int64) string { return time.Unix(day*86400, 0).UTC().Format("2006-01-02") }
	s, e := iso(calfDay(lo)), iso(calfDay(hi))
	return pulse.RangeTable{Ranges: []pulse.DateRangeSpec{{Label: "w", Start: &s, End: &e}}}
}

// fidExprCount is Process's AGG_COUNT over c.pulse under filterers.
func fidCount(t *testing.T, p *pulse.Pulse, filterers []*types.Filterer) int64 {
	t.Helper()
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: "c.pulse"},
		Filterers:    filterers,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return zepCount(t, resp.Data[0]["n"])
}

// TestFilterToFile_TableContentKeysDedup: per referenced table kind,
// instance a and b register a same-named table of different content;
// c registers a's content again; d registers a's content plus an
// unreferenced table. b writes its own output with its own rows; c and
// d reuse a's.
func TestFilterToFile_TableContentKeysDedup(t *testing.T) {
	t.Parallel()

	rangeParams, _ := json.Marshal(map[string]any{"table": "win"})
	cases := map[string]struct {
		ext       func(variant int) pulse.Extensions // 0 = a, 1 = b, 2 = a + unreferenced
		filterers []*types.Filterer
	}{
		"range_table": {
			ext: func(v int) pulse.Extensions {
				tables := map[string]pulse.RangeTable{"win": fidRange(9, 30)}
				switch v {
				case 1:
					tables["win"] = fidRange(9, 20)
				case 2:
					tables["other"] = fidRange(1, 2)
				}
				return pulse.Extensions{RangeTables: tables}
			},
			filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "d", Params: rangeParams}},
		},
		"lookup_table": {
			ext: func(v int) pulse.Extensions {
				tables := map[string]pulse.LookupTable{"thr": {Rows: map[string]float64{"k": 20, "j": 1}}}
				switch v {
				case 1:
					tables["thr"] = pulse.LookupTable{Rows: map[string]float64{"k": 40, "j": 1}}
				case 2:
					tables["other"] = pulse.LookupTable{Rows: map[string]float64{"k": 1}}
				}
				return pulse.Extensions{LookupTables: tables}
			},
			filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `y > lookup("thr", "k")`}},
		},
		"lookup_table_dynamic_name": {
			ext: func(v int) pulse.Extensions {
				tables := map[string]pulse.LookupTable{"thr": {Rows: map[string]float64{"k": 20}}}
				if v == 1 {
					tables["thr"] = pulse.LookupTable{Rows: map[string]float64{"k": 40}}
				}
				return pulse.Extensions{LookupTables: tables}
			},
			filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `y > lookup(x >= 0 ? "thr" : "none", "k")`}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fsys := afero.NewMemMapFs()
			ftzWrite(t, fsys, "c.pulse", calfRows)
			newP := func(v int) *pulse.Pulse {
				p, err := pulse.New(pulse.Options{FS: fsys, Extensions: c.ext(v)})
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			run := func(p *pulse.Pulse) *pulse.FilterToFileResult {
				t.Helper()
				res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: "c.pulse", Filterers: c.filterers, OutputDir: "fid"})
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			pa, pb := newP(0), newP(1)
			wantA, wantB := fidCount(t, pa, c.filterers), fidCount(t, pb, c.filterers)
			if wantA == wantB {
				t.Fatalf("tables keep the same %d rows: the case does not separate them", wantA)
			}
			a := run(pa)
			if a.Reused || a.RowCount != wantA {
				t.Fatalf("a: rows %d reused=%v, want %d fresh", a.RowCount, a.Reused, wantA)
			}
			b := run(pb)
			if b.Reused || b.OutputPath == a.OutputPath || b.RowCount != wantB {
				t.Fatalf("b: path %s rows %d reused=%v; want a fresh output of %d rows (a: %s)", b.OutputPath, b.RowCount, b.Reused, wantB, a.OutputPath)
			}
			if again := run(newP(0)); !again.Reused || again.OutputPath != a.OutputPath {
				t.Fatalf("identical content: path %s reused=%v, want %s reused", again.OutputPath, again.Reused, a.OutputPath)
			}
			if name == "lookup_table_dynamic_name" {
				return // a dynamic name reads every table: no unreferenced one
			}
			if d := run(newP(2)); !d.Reused || d.OutputPath != a.OutputPath {
				t.Fatalf("unreferenced table: path %s reused=%v, want %s reused", d.OutputPath, d.Reused, a.OutputPath)
			}
		})
	}
}

// fidGreaterX keeps the rows whose x exceeds min.
type fidGreaterX struct{ min float64 }

func (g fidGreaterX) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(rec extend.Record) (bool, error) {
		x, ok := rec.NumericValue("x")
		return ok && x > g.min, nil
	}, nil
}

const fidExtFilter = types.FiltererType("FILTER_FID_GREATER_X")

// TestFilterToFile_EmbedderCodeNeverReused: a predicate running embedder
// code — an extension filterer, an ExprFunction, a function-driven
// lookup table — is re-run on every call, never reused: two instances
// registering the same name with different code each get their own
// rows, and a second call on the same instance re-runs too.
func TestFilterToFile_EmbedderCodeNeverReused(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		ext       func(min float64) pulse.Extensions
		filterers []*types.Filterer
	}{
		"extension_filterer": {
			ext: func(min float64) pulse.Extensions {
				return pulse.Extensions{Filterers: []pulse.FiltererRegistration{{
					Name:    fidExtFilter,
					Factory: func() extend.FiltererBuilder { return fidGreaterX{min: min} },
				}}}
			},
			filterers: []*types.Filterer{{Type: fidExtFilter, Field: "x"}},
		},
		"expr_function": {
			ext: func(min float64) pulse.Extensions {
				return pulse.Extensions{ExprFunctions: []pulse.ExprFunction{{
					Name: "fid_min",
					Fn:   func() float64 { return min },
				}}}
			},
			filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `x > fid_min()`}},
		},
		"function_lookup_table": {
			ext: func(min float64) pulse.Extensions {
				return pulse.Extensions{LookupTables: map[string]pulse.LookupTable{"thr": {
					Lookup: func(...string) (float64, bool, error) { return min, true, nil },
				}}}
			},
			filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `x > lookup("thr", "k")`}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fsys := afero.NewMemMapFs()
			ftzWrite(t, fsys, "c.pulse", calfRows)
			newP := func(min float64) *pulse.Pulse {
				p, err := pulse.New(pulse.Options{FS: fsys, Extensions: c.ext(min)})
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			pa, pb := newP(0), newP(3)
			wantA, wantB := fidCount(t, pa, c.filterers), fidCount(t, pb, c.filterers)
			if wantA == wantB {
				t.Fatalf("both keep %d rows: the case does not separate them", wantA)
			}
			for i, step := range []struct {
				p    *pulse.Pulse
				want int64
			}{{pa, wantA}, {pb, wantB}, {pa, wantA}} {
				res, err := step.p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: "c.pulse", Filterers: c.filterers, OutputDir: "fid"})
				if err != nil {
					t.Fatal(err)
				}
				if res.Reused || res.RowCount != step.want {
					t.Fatalf("call %d: rows %d reused=%v, want %d fresh", i, res.RowCount, res.Reused, step.want)
				}
				if all := fidOutputRows(t, step.p, res.OutputPath); all != step.want {
					t.Fatalf("call %d: output holds %d rows, want %d", i, all, step.want)
				}
			}
		})
	}
}

// fidOutputRows is the record count of the file at path.
func fidOutputRows(t *testing.T, p *pulse.Pulse, path string) int64 {
	t.Helper()
	n, err := p.CountRecords(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return int64(n)
}
