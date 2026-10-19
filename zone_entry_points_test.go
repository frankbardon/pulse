package pulse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
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
		{name: "FilterToFileWithRequest filterers", run: func(t *testing.T, p *pulse.Pulse, fsys afero.Fs, pr zepPredicate) int64 {
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

// TestTimeZone_FilterToFileDateRanges: the filter-to-file request
// runs FILTER_DATE_RANGES in the zone Process resolves (here the
// instance default — TestTimeZone_DefaultZoneReachesEveryEntryPoint
// covers the kept rows), and that zone keys the deterministic output
// name: the same request on a UTC instance writes a different output
// rather than reusing the New York one. Before U34 E3-S5 the type was
// refused (reason zone_capable). A never-registered type keeps its
// coded refusal.
func TestTimeZone_FilterToFileDateRanges(t *testing.T) {
	fsys := zepFS(t)
	ny, utc := zepPulse(t, fsys, zepZone), zepPulse(t, fsys, "")
	for _, pr := range zepPredicates(t) {
		if pr.expr != "" {
			continue
		}
		req := func() *pulse.FilterToFileRequest {
			return &pulse.FilterToFileRequest{SourcePath: calfCohort, Filterers: pr.filterers, OutputDir: "zep_out"}
		}
		a, err := ny.FilterToFileWithRequest(context.Background(), req())
		if err != nil {
			t.Fatalf("%s: %v", pr.name, err)
		}
		if a.RowCount != zepWant(t, pr, zepZone) {
			t.Fatalf("%s: kept %d rows, want %d", pr.name, a.RowCount, zepWant(t, pr, zepZone))
		}
		b, err := utc.FilterToFileWithRequest(context.Background(), req())
		if err != nil {
			t.Fatalf("%s: %v", pr.name, err)
		}
		if b.Reused || b.OutputPath == a.OutputPath || b.RowCount != zepWant(t, pr, "") {
			t.Fatalf("%s: UTC run path %s rows %d reused=%v; the New York output is %s", pr.name, b.OutputPath, b.RowCount, b.Reused, a.OutputPath)
		}
	}
	// A never-registered type takes the coded refusal.
	_, err := ny.FilterToFileWithRequest(context.Background(), &pulse.FilterToFileRequest{SourcePath: calfCohort, Filterers: []*types.Filterer{{Type: "FILTER_NOPE", Field: "x"}}, OutputDir: "zep_out"})
	if ce := requireCode(t, err, errors.PROCESSING_CONFIG); ce.Details["operator"] != "FILTER_NOPE" {
		t.Errorf("unknown type: details = %v", ce.Details)
	}
}

// TestTimeZone_FilterToFileRequestZone: FilterToFileRequest.TimeZone
// (#467) is the zone a to_day() predicate reads — over
// Options.DefaultTimeZone, exactly as Request.TimeZone is — on both
// predicate shapes; the zone the predicate read keys the dedup name, so
// the same resolved zone lands on the same output whichever knob named
// it and different zones never share one.
func TestTimeZone_FilterToFileRequestZone(t *testing.T) {
	ctx := context.Background()
	fsys := zepFS(t)
	utc, ny := zepPulse(t, fsys, ""), zepPulse(t, fsys, zepZone)
	pr := zepPredicates(t)[0]
	shapes := map[string]func(tz string) *pulse.FilterToFileRequest{
		"expression": func(tz string) *pulse.FilterToFileRequest {
			return &pulse.FilterToFileRequest{SourcePath: calfCohort, Expression: pr.expr, OutputDir: "zep_reqtz_expr", TimeZone: tz}
		},
		"filterers": func(tz string) *pulse.FilterToFileRequest {
			return &pulse.FilterToFileRequest{SourcePath: calfCohort, Filterers: pr.filterers, OutputDir: "zep_reqtz_filt", TimeZone: tz}
		},
	}
	for name, mk := range shapes {
		t.Run(name, func(t *testing.T) {
			run := func(p *pulse.Pulse, tz string) *pulse.FilterToFileResult {
				t.Helper()
				res, err := p.FilterToFileWithRequest(ctx, mk(tz))
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			// The request zone on a UTC instance selects local-day rows.
			a := run(utc, zepZone)
			if a.Reused || a.RowCount != zepWant(t, pr, zepZone) {
				t.Fatalf("request zone: rows %d (reused=%v), want %d", a.RowCount, a.Reused, zepWant(t, pr, zepZone))
			}
			// Same resolved zone via the instance default: same output.
			if b := run(ny, ""); !b.Reused || b.OutputPath != a.OutputPath {
				t.Fatalf("default-zone run: path %s reused=%v, want the request-zone output %s", b.OutputPath, b.Reused, a.OutputPath)
			}
			// The request zone wins over the default.
			c := run(ny, "UTC")
			if c.OutputPath == a.OutputPath || c.RowCount != zepWant(t, pr, "") {
				t.Fatalf("UTC request over NY default: path %s rows %d, want a fresh output with %d rows", c.OutputPath, c.RowCount, zepWant(t, pr, ""))
			}
			// UTC named or absent is the same zone, so the same output.
			if d := run(utc, ""); !d.Reused || d.OutputPath != c.OutputPath {
				t.Fatalf("empty TimeZone on a UTC instance: path %s reused=%v, want %s", d.OutputPath, d.Reused, c.OutputPath)
			}
			// A third zone is a third output.
			if e := run(utc, "Asia/Tokyo"); e.Reused || e.OutputPath == a.OutputPath || e.OutputPath == c.OutputPath {
				t.Fatalf("Asia/Tokyo reused another zone's output %s", e.OutputPath)
			}
		})
	}
	// A zone-free predicate reads no zone, so a request zone never
	// changes its output name.
	run := func(tz string) *pulse.FilterToFileResult {
		res, err := utc.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: calfCohort, Expression: "x > 1", OutputDir: "zep_reqtz_free", TimeZone: tz})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if f, g := run(""), run(zepZone); !g.Reused || f.OutputPath != g.OutputPath {
		t.Fatalf("zone-free predicate: paths %s / %s reused=%v", f.OutputPath, g.OutputPath, g.Reused)
	}
}

// TestTimeZone_FilterToFileRequestUnknownZone: an unknown
// FilterToFileRequest.TimeZone is PULSE_TIMEZONE_UNKNOWN — validated up
// front, as Request.TimeZone is, whether or not the predicate reads it,
// before the source is read — and no output is written.
func TestTimeZone_FilterToFileRequestUnknownZone(t *testing.T) {
	fsys := zepFS(t)
	p := zepPulse(t, fsys, "")
	for _, expr := range []string{"to_day(ts) == d", "x > 1"} {
		_, err := p.FilterToFileWithRequest(context.Background(), &pulse.FilterToFileRequest{SourcePath: calfCohort, Expression: expr, OutputDir: "zep_badtz", TimeZone: "Mars/Olympus"})
		requireCode(t, err, errors.PULSE_TIMEZONE_UNKNOWN)
	}
	// Refused before any read: a missing source never gets as far as a
	// file error.
	_, err := p.FilterToFileWithRequest(context.Background(), &pulse.FilterToFileRequest{SourcePath: "missing.pulse", Expression: "x > 1", OutputDir: "zep_badtz", TimeZone: "Mars/Olympus"})
	requireCode(t, err, errors.PULSE_TIMEZONE_UNKNOWN)
	if ok, _ := afero.DirExists(fsys, "zep_badtz"); ok {
		entries, _ := afero.ReadDir(fsys, "zep_badtz")
		if len(entries) > 0 {
			t.Fatalf("an unknown zone wrote %d entries", len(entries))
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
