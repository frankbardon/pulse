package pulse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// Every public entry point that executes a zone-sensitive slot resolves
// its zone the way Process does — request `time_zone`, then
// Options.DefaultTimeZone, then UTC (U45 E4-S1 follow-up). The instance
// below carries ONLY a default zone; no request names one, so a path
// that skipped the zone choke point would compute in UTC.

const zepZone = "America/New_York"

// zepPredicate is one zone-sensitive filter: its request filterers and,
// when it can ride the expression-only filter-to-file paths, the
// expression.
type zepPredicate struct {
	name      string
	filterers []*types.Filterer
	expr      string // "" = not expressible as a FilterToFile expression
	keeps     func(t *testing.T, r int, zone string) bool
}

func zepPredicates(t *testing.T) []zepPredicate {
	t.Helper()
	lo, hi := calfDay(0), calfDay(calfRows-1)
	iso := func(day int64) string { return time.Unix(day*86400, 0).UTC().Format("2006-01-02") }
	params, err := json.Marshal(map[string]any{"ranges": []map[string]any{{"label": "span", "start": iso(lo), "end": iso(hi)}}})
	if err != nil {
		t.Fatal(err)
	}
	return []zepPredicate{
		{
			name:      "to_day",
			filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "to_day(ts) == d"}},
			expr:      "to_day(ts) == d",
			keeps:     func(t *testing.T, r int, zone string) bool { return oracleLocalDay(t, r, zone) == calfDay(r) },
		},
		{
			name:      "weekday",
			filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "weekday(to_day(ts)) == weekday(d)"}},
			expr:      "weekday(to_day(ts)) == weekday(d)",
			keeps: func(t *testing.T, r int, zone string) bool {
				return oracleWeekday(time.Unix(oracleLocalDay(t, r, zone)*86400, 0).UTC()) == oracleWeekday(oracleDay(r))
			},
		},
		{
			name:      "FILTER_DATE_RANGES",
			filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params}},
			keeps: func(t *testing.T, r int, zone string) bool {
				d := oracleLocalDay(t, r, zone)
				return d >= lo && d <= hi
			},
		},
	}
}

func zepWant(t *testing.T, pr zepPredicate, zone string) int64 {
	t.Helper()
	var n int64
	for r := 0; r < calfRows; r++ {
		if pr.keeps(t, r, zone) {
			n++
		}
	}
	return n
}

// zepCount reads an AGG_COUNT figure whatever its numeric spelling.
func zepCount(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	t.Fatalf("count = %#v", v)
	return 0
}

func zepRequest(pr zepPredicate) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: calfCohort},
		Filterers:    pr.filterers,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	}
}

type zepEntry struct {
	name string
	// exprOnly: the entry point takes a predicate expression, not
	// request filterers.
	exprOnly bool
	run      func(t *testing.T, p *pulse.Pulse, fsys afero.Fs, pr zepPredicate) int64
}

func zepEntries() []zepEntry {
	ctx := context.Background()
	return []zepEntry{
		{name: "Process", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			resp, err := p.Process(ctx, zepRequest(pr))
			if err != nil {
				t.Fatal(err)
			}
			return zepCount(t, resp.Data[0]["n"])
		}},
		{name: "Process shard archive", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			req := zepRequest(pr)
			req.Cohort = &types.Cohort{Filename: "zep_archive.pulse"}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			return zepCount(t, resp.Data[0]["n"])
		}},
		{name: "Process crosstab", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			req := zepRequest(pr)
			req.Aggregations = nil
			req.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			var n int64
			for _, row := range resp.Components.Crosstab.CellCounts {
				for _, c := range row {
					n += int64(c)
				}
			}
			return n
		}},
		{name: "ProcessStream", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			it, err := p.ProcessStream(ctx, zepRequest(pr))
			if err != nil {
				t.Fatal(err)
			}
			defer it.Close()
			row, ok, err := it.Next(ctx)
			if err != nil || !ok {
				t.Fatalf("Next: ok=%v err=%v", ok, err)
			}
			return zepCount(t, row["n"])
		}},
		{name: "ProcessStreamResult", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			res, err := p.ProcessStreamResult(ctx, zepRequest(pr))
			if err != nil {
				t.Fatal(err)
			}
			var n int64 = -1
			for c := range res.Chunks {
				n = zepCount(t, c.Data["n"])
			}
			if term := <-res.Done; term.Error != nil {
				t.Fatal(term.Error)
			}
			return n
		}},
		{name: "Compose", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			resp, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{zepRequest(pr)}})
			if err != nil {
				t.Fatal(err)
			}
			return zepCount(t, resp.Responses[0].Data[0]["n"])
		}},
		{name: "ComposeParallel", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			resp, err := p.ComposeParallel(ctx, &pulse.ComposedRequest{Requests: []*types.Request{zepRequest(pr), zepRequest(pr)}}, pulse.ComposeOptions{MaxWorkers: 2})
			if err != nil {
				t.Fatal(err)
			}
			return zepCount(t, resp.Responses[1].Data[0]["n"])
		}},
		{name: "ProcessChain", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			stage := zepRequest(pr)
			stage.Cohort = nil
			resp, err := p.ProcessChain(ctx, &pulse.ChainRequest{Cohort: &types.Cohort{Filename: calfCohort}, Stages: []*types.ChainStage{{Request: stage}}})
			if err != nil {
				t.Fatal(err)
			}
			return zepCount(t, resp.Final.Data[0]["n"])
		}},
		{name: "Materialize", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			req := &types.Request{Cohort: &types.Cohort{Filename: calfCohort}, Filterers: pr.filterers,
				Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x2", Expression: "x * 2"}}}
			res, err := p.Materialize(ctx, req, "zep_mat.pulse", pulse.MaterializeOptions{Overwrite: true})
			if err != nil {
				t.Fatal(err)
			}
			return res.Records
		}},
		{name: "FacetSchema", run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			res, err := p.FacetSchema(ctx, &pulse.FacetRequest{Cohort: &types.Cohort{Filename: calfCohort}, Fields: []string{"cat"}, Filterers: pr.filterers})
			if err != nil {
				t.Fatal(err)
			}
			return res.FilteredRecords
		}},
		{name: "FilterToFile", exprOnly: true, run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			n, err := p.FilterToFile(ctx, calfCohort, "zep_ftf.pulse", pr.expr)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{name: "FilterToFile shard archive", exprOnly: true, run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			n, err := p.FilterToFile(ctx, "zep_archive.pulse", "zep_ftf_arch.pulse", pr.expr)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{name: "FilterToFileBySetAndExpr", exprOnly: true, run: func(t *testing.T, p *pulse.Pulse, _ afero.Fs, pr zepPredicate) int64 {
			schema, err := p.ResolveCanonicalSchema(ctx, calfCohort)
			if err != nil {
				t.Fatal(err)
			}
			set, err := pulse.LoadMemberSetFromReader(strings.NewReader(strings.Join(calfUnits, "\n")), schema, "cat")
			if err != nil {
				t.Fatal(err)
			}
			n, err := p.FilterToFileBySetAndExpr(ctx, calfCohort, "zep_ftf_set.pulse", "cat", set.Set, pr.expr)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{name: "FilterToFileWithRequest expression", exprOnly: true, run: func(t *testing.T, p *pulse.Pulse, fsys afero.Fs, pr zepPredicate) int64 {
			res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: calfCohort, Expression: pr.expr, OutputDir: "zep_out_expr"})
			if err != nil {
				t.Fatal(err)
			}
			return res.RowCount
		}},
		{name: "FilterToFileWithRequest filterers", exprOnly: true, run: func(t *testing.T, p *pulse.Pulse, fsys afero.Fs, pr zepPredicate) int64 {
			res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: calfCohort, Filterers: pr.filterers, OutputDir: "zep_out_filt"})
			if err != nil {
				t.Fatal(err)
			}
			return res.RowCount
		}},
	}
}

// zepPulse is a calendar-cohort instance (plus a one-shard archive of
// it) with defaultZone as its only zone.
func zepPulse(t *testing.T, fsys afero.Fs, defaultZone string) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fsys, DefaultTimeZone: defaultZone, ShardWorkers: 2})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func zepFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeCalfCohort(t, fsys, calfUnits)
	p := zepPulse(t, fsys, "")
	if _, err := p.CreateShardArchive(context.Background(), "zep_archive.pulse", []string{calfCohort}); err != nil {
		t.Fatal(err)
	}
	return fsys
}

// TestTimeZone_DefaultZoneReachesEveryEntryPoint: under an instance
// whose only zone is Options.DefaultTimeZone, every public entry point
// that runs a filter reads to_day / weekday(to_day(...)) and
// FILTER_DATE_RANGES in that zone (the UTC instance is the control —
// the cohort separates the two).
func TestTimeZone_DefaultZoneReachesEveryEntryPoint(t *testing.T) {
	preds := zepPredicates(t)
	for _, pr := range preds {
		if zepWant(t, pr, "") == zepWant(t, pr, zepZone) {
			t.Fatalf("%s: the cohort does not separate UTC and %s", pr.name, zepZone)
		}
	}
	for _, zone := range []string{"", zepZone} {
		fsys := zepFS(t)
		p := zepPulse(t, fsys, zone)
		for _, e := range zepEntries() {
			for _, pr := range preds {
				if e.exprOnly && pr.expr == "" {
					continue
				}
				t.Run(e.name+"/"+pr.name+"/default="+zone, func(t *testing.T) {
					if got, want := e.run(t, p, fsys, pr), zepWant(t, pr, zone); got != want {
						t.Errorf("kept %d rows, want %d (default zone %q)", got, want, zone)
					}
				})
			}
		}
	}
}

// TestTimeZone_FilterToFileDateRangesRefused: the filter-to-file request
// cannot carry FILTER_DATE_RANGES (it translates to an expression), so
// it is refused rather than computed in some zone.
func TestTimeZone_FilterToFileDateRangesRefused(t *testing.T) {
	fsys := zepFS(t)
	p := zepPulse(t, fsys, zepZone)
	for _, pr := range zepPredicates(t) {
		if pr.expr != "" {
			continue
		}
		_, err := p.FilterToFileWithRequest(context.Background(), &pulse.FilterToFileRequest{SourcePath: calfCohort, Filterers: pr.filterers, OutputDir: "zep_out"})
		if err == nil || !strings.Contains(err.Error(), "unsupported filter type") {
			t.Fatalf("%s: err = %v, want the unsupported-type refusal", pr.name, err)
		}
	}
}

// TestTimeZone_FilterToFileDedupKeysTheZone: two instances sharing a
// filesystem but not a default zone never reuse each other's
// filter-to-file output for a to_day predicate — the zone the predicate
// read joins its dedup hash. A zone-free predicate keeps one output.
func TestTimeZone_FilterToFileDedupKeysTheZone(t *testing.T) {
	ctx := context.Background()
	fsys := zepFS(t)
	utc, ny := zepPulse(t, fsys, ""), zepPulse(t, fsys, zepZone)
	pr := zepPredicates(t)[0]
	run := func(p *pulse.Pulse, expr string) *pulse.FilterToFileResult {
		res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: calfCohort, Expression: expr, OutputDir: "zep_dedup"})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	a, b := run(utc, pr.expr), run(ny, pr.expr)
	if b.Reused || a.OutputPath == b.OutputPath {
		t.Fatalf("New York run reused the UTC output %s (reused=%v)", a.OutputPath, b.Reused)
	}
	if a.RowCount != zepWant(t, pr, "") || b.RowCount != zepWant(t, pr, zepZone) {
		t.Fatalf("rows utc=%d ny=%d, want %d / %d", a.RowCount, b.RowCount, zepWant(t, pr, ""), zepWant(t, pr, zepZone))
	}
	// A zone-free predicate hashes as it always has: one shared output.
	c, d := run(utc, "x > 1"), run(ny, "x > 1")
	if !d.Reused || c.OutputPath != d.OutputPath {
		t.Fatalf("zone-free predicate: paths %s / %s, reused=%v", c.OutputPath, d.OutputPath, d.Reused)
	}
}
