package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------
// Date-operator benchmarks (zone-aware-operators E1-S1).
//
// The U14 acceptance bar is that a request with `tz` absent or "UTC"
// stays byte-identical AND same-speed once the date operators learn
// local-day bucketing. These benchmarks are the baseline captured
// BEFORE any operator change; later stories re-run them unchanged and
// compare with benchstat.
//
// Axes (all encoded in the sub-benchmark name, stable for benchstat):
//   - col:       date (epoch days) vs datetime (epoch seconds)
//   - component: GROUP_DATE day / month
//   - order:     clustered (time-ordered rows) vs random (shuffled) —
//                the random arm defeats any per-zone transition cache
//   - tz:        absent / UTC / Europe_Berlin, applied as the
//                REQUEST-level time_zone so a `date` column inherits it
//                (an explicit slot tz on a date column is refused by
//                design) and every zone-capable slot sees it
//   - arm:       crosstab buffered vs fused
//
// Non-UTC variants are SKIPPED (never failed) while the engine still
// refuses a non-UTC zone on a datetime operator; they start reporting
// automatically once the refusal lifts.
//
// Run with:
//
//	go test ./internal/service/ -run='^$' -bench=BenchmarkDateOps -benchmem -count=6
// ---------------------------------------------------------------------

const dateOpsBenchRows = 100_000

// dateOpsZones is the tz axis. "" means the time_zone slot is absent.
var dateOpsZones = []string{"", "UTC", "Europe/Berlin"}

var dateOpsOrders = []string{"clustered", "random"}

// dateOpsQuarters2024 is the shared labeled-range set for
// FILTER_DATE_RANGES / GROUP_DATE_RANGES — the fixture spans 2023-2024,
// so roughly half the rows fall inside a range.
var dateOpsQuarters2024 = json.RawMessage(`{"ranges":[` +
	`{"label":"q1","start":"2024-01-01","end":"2024-03-31"},` +
	`{"label":"q2","start":"2024-04-01","end":"2024-06-30"},` +
	`{"label":"q3","start":"2024-07-01","end":"2024-09-30"},` +
	`{"label":"q4","start":"2024-10-01","end":"2024-12-31"}]}`)

var (
	dateOpsFixtureOnce sync.Once
	dateOpsFixtureCfg  *fs.Config
	dateOpsFixtureSch  *encoding.Schema
	dateOpsFixtureErr  error
)

// dateOpsSchema: ts datetime, v f64, d date, seg categorical_u8.
func dateOpsSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for i := range 8 {
		_, _ = dict.Add(fmt.Sprintf("seg_%d", i))
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
			{Name: "d", Type: encoding.FieldTypeDate, ByteOffset: 16, CsvColumnIdx: 2},
			{Name: "seg", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 20, CsvColumnIdx: 3, Dictionary: dict},
		},
	}
}

// dateOpsTimestamps returns dateOpsBenchRows strictly increasing epoch
// seconds from 2023-01-01T00:00:00Z spanning ~2 years (step 631s plus a
// deterministic <600s jitter, so the sequence stays monotonic).
func dateOpsTimestamps() []int64 {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	out := make([]int64, dateOpsBenchRows)
	for i := range out {
		out[i] = start + int64(i)*631 + int64((i*37)%600)
	}
	return out
}

func writeDateOpsCohort(memFs afero.Fs, path string, schema *encoding.Schema, ts []int64) error {
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		return err
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		return err
	}
	for i, sec := range ts {
		vals := []uint64{
			uint64(sec),
			math.Float64bits(float64((i*37)%1009) + 0.5),
			uint64(uint32(int32(encoding.DateTimeToDay(sec)))),
			uint64((i * 5) % 8),
		}
		for fi, f := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, f.Type, vals[fi]); err != nil {
				return err
			}
		}
	}
	return afero.WriteFile(memFs, path, buf.Bytes(), 0o644)
}

// dateOpsFixture builds both cohorts once per process (clustered and a
// deterministically shuffled twin of the same rows).
func dateOpsFixture(b *testing.B) (*fs.Config, *encoding.Schema) {
	b.Helper()
	dateOpsFixtureOnce.Do(func() {
		cfg := fs.NewMemMap()
		schema := dateOpsSchema()
		ts := dateOpsTimestamps()
		if err := writeDateOpsCohort(cfg.Fs(), "dates_clustered.pulse", schema, ts); err != nil {
			dateOpsFixtureErr = err
			return
		}
		shuffled := append([]int64(nil), ts...)
		r := rand.New(rand.NewPCG(14, 1))
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if err := writeDateOpsCohort(cfg.Fs(), "dates_random.pulse", schema, shuffled); err != nil {
			dateOpsFixtureErr = err
			return
		}
		dateOpsFixtureCfg, dateOpsFixtureSch = cfg, schema
	})
	if dateOpsFixtureErr != nil {
		b.Fatalf("build date-ops fixture: %v", dateOpsFixtureErr)
	}
	return dateOpsFixtureCfg, dateOpsFixtureSch
}

func dateOpsTZName(tz string) string {
	if tz == "" {
		return "absent"
	}
	return strings.ReplaceAll(tz, "/", "_")
}

// runDateOpsBench preflights one Process call (skipping a non-UTC
// variant the engine still refuses) and then times the request.
func runDateOpsBench(b *testing.B, svc *Service, mk func() *types.Request, tz string) {
	b.Helper()
	ctx := context.Background()
	if _, err := svc.Process(ctx, mk()); err != nil {
		if tz != "" && tz != "UTC" {
			b.Skipf("tz=%s refused by the engine (pre zone-aware operators): %v", tz, err)
		}
		b.Fatalf("Process preflight: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := svc.Process(ctx, mk()); err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}

// BenchmarkDateOps_GroupDate: GROUP_DATE day / month over a date and a
// datetime column, SUM(v) per bucket.
func BenchmarkDateOps_GroupDate(b *testing.B) {
	cfg, _ := dateOpsFixture(b)
	for _, col := range []string{"date", "datetime"} {
		field := "d"
		if col == "datetime" {
			field = "ts"
		}
		for _, comp := range []string{"day", "month"} {
			for _, order := range dateOpsOrders {
				for _, tz := range dateOpsZones {
					name := fmt.Sprintf("col=%s/component=%s/order=%s/tz=%s", col, comp, order, dateOpsTZName(tz))
					b.Run(name, func(b *testing.B) {
						svc := New(cfg)
						params := json.RawMessage(fmt.Sprintf(`{"component":%q}`, comp))
						mk := func() *types.Request {
							return &types.Request{
								Cohort:       &types.Cohort{Filename: "dates_" + order + ".pulse"},
								TimeZone:     tz,
								Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: field, Params: params}},
								Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
							}
						}
						runDateOpsBench(b, svc, mk, tz)
					})
				}
			}
		}
	}
}

// BenchmarkDateOps_GroupDateCalendar: GROUP_DATE week (ISO and
// week_start sunday) and hour over the datetime column, SUM(v) per
// bucket (zone-aware-operators E2-S1).
func BenchmarkDateOps_GroupDateCalendar(b *testing.B) {
	cfg, _ := dateOpsFixture(b)
	for _, comp := range []struct{ name, params string }{
		{"week", `{"component":"week"}`},
		{"week-sunday", `{"component":"week","week_start":"sunday"}`},
		{"hour", `{"component":"hour"}`},
	} {
		for _, order := range dateOpsOrders {
			for _, tz := range dateOpsZones {
				b.Run(fmt.Sprintf("component=%s/order=%s/tz=%s", comp.name, order, dateOpsTZName(tz)), func(b *testing.B) {
					svc := New(cfg)
					mk := func() *types.Request {
						return &types.Request{
							Cohort:       &types.Cohort{Filename: "dates_" + order + ".pulse"},
							TimeZone:     tz,
							Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(comp.params)}},
							Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
						}
					}
					runDateOpsBench(b, svc, mk, tz)
				})
			}
		}
	}
}

// BenchmarkDateOps_FilterDateRanges: FILTER_DATE_RANGES (2024 quarters)
// over the datetime column, SUM(v) over the survivors.
func BenchmarkDateOps_FilterDateRanges(b *testing.B) {
	cfg, _ := dateOpsFixture(b)
	for _, order := range dateOpsOrders {
		for _, tz := range dateOpsZones {
			b.Run(fmt.Sprintf("col=datetime/order=%s/tz=%s", order, dateOpsTZName(tz)), func(b *testing.B) {
				svc := New(cfg)
				mk := func() *types.Request {
					return &types.Request{
						Cohort:       &types.Cohort{Filename: "dates_" + order + ".pulse"},
						TimeZone:     tz,
						Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: dateOpsQuarters2024}},
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
					}
				}
				runDateOpsBench(b, svc, mk, tz)
			})
		}
	}
}

// BenchmarkDateOps_GroupDateRanges: GROUP_DATE_RANGES (2024 quarters)
// over the datetime column, SUM(v) per range.
func BenchmarkDateOps_GroupDateRanges(b *testing.B) {
	cfg, _ := dateOpsFixture(b)
	for _, order := range dateOpsOrders {
		for _, tz := range dateOpsZones {
			b.Run(fmt.Sprintf("col=datetime/order=%s/tz=%s", order, dateOpsTZName(tz)), func(b *testing.B) {
				svc := New(cfg)
				mk := func() *types.Request {
					return &types.Request{
						Cohort:       &types.Cohort{Filename: "dates_" + order + ".pulse"},
						TimeZone:     tz,
						Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: dateOpsQuarters2024}},
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
					}
				}
				runDateOpsBench(b, svc, mk, tz)
			})
		}
	}
}

// BenchmarkDateOps_DateParts: ATTR_DATE_PART (month) and
// FEAT_DATE_FEATURES over a date and a datetime column, SUM of the
// derived month (zone-aware-operators E2-S2). The `date` rows are the
// byte-identical / same-speed bar: an inherited zone never applies
// there. The `datetime` rows (refused before E2-S2, so skipped on a
// pre-E2-S2 baseline) measure local-calendar extraction.
func BenchmarkDateOps_DateParts(b *testing.B) {
	cfg, _ := dateOpsFixture(b)
	for _, op := range []string{"attr", "feat"} {
		for _, col := range []string{"date", "datetime"} {
			field := "d"
			if col == "datetime" {
				field = "ts"
			}
			for _, order := range dateOpsOrders {
				for _, tz := range dateOpsZones {
					b.Run(fmt.Sprintf("op=%s/col=%s/order=%s/tz=%s", op, col, order, dateOpsTZName(tz)), func(b *testing.B) {
						svc := New(cfg)
						mk := func() *types.Request {
							req := &types.Request{
								Cohort:   &types.Cohort{Filename: "dates_" + order + ".pulse"},
								TimeZone: tz,
							}
							if op == "attr" {
								req.Attributes = []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: field, Label: "mo", Params: json.RawMessage(`{"part":"month"}`)}}
								req.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "mo", Label: "s"}}
							} else {
								req.Features = []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: field, Label: "df"}}
								req.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "df_month", Label: "s"}}
							}
							return req
						}
						if col == "datetime" {
							if _, err := svc.Process(context.Background(), mk()); err != nil {
								b.Skipf("datetime refused (pre E2-S2): %v", err)
							}
						}
						runDateOpsBench(b, svc, mk, tz)
					})
				}
			}
		}
	}
}

// BenchmarkDateOps_CrosstabGroupDate: crosstab with a GROUP_DATE month
// row over the datetime column × GROUP_CATEGORY column, COUNT cell,
// all margins — buffered arm (fusion forced off) vs fused arm (asserted
// fusable so the arm cannot silently fall back to buffered).
func BenchmarkDateOps_CrosstabGroupDate(b *testing.B) {
	cfg, schema := dateOpsFixture(b)
	monthParams := json.RawMessage(`{"component":"month"}`)
	for _, arm := range []string{"buffered", "fused"} {
		for _, order := range dateOpsOrders {
			for _, tz := range dateOpsZones {
				b.Run(fmt.Sprintf("arm=%s/order=%s/tz=%s", arm, order, dateOpsTZName(tz)), func(b *testing.B) {
					svc := New(cfg)
					svc.SetDisableCrosstabFusion(arm == "buffered")
					mk := func() *types.Request {
						return &types.Request{
							Cohort:   &types.Cohort{Filename: "dates_" + order + ".pulse"},
							TimeZone: tz,
							Crosstab: &types.CrosstabSpec{
								Rows:    []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: monthParams}},
								Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}},
								Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "v", Label: "n"},
								Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
								Shape:   types.CrosstabShapeMatrix,
							},
						}
					}
					if arm == "fused" {
						if ok, reason := processing.CanFuseCrosstab(mk(), schema, svc.Extensions()); !ok {
							b.Fatalf("CanFuseCrosstab rejected the GROUP_DATE crosstab: %s", reason)
						}
					}
					runDateOpsBench(b, svc, mk, tz)
				})
			}
		}
	}
}

// ---------------------------------------------------------------------
// Parallel date-operator benchmarks (zone-aware-operators E1-S4).
//
// Every worker of a parallel run shares ONE *temporal.Zone (the
// process-wide slotZones cache), whose last-span lookup cache is a
// single atomic. These benchmarks measure whether workers contend on
// it: compare the Europe_Berlin / absent ratio at workers=1 against
// workers=N. (Each worker builds its own operator set, and the
// date-family operators Fork the zone per operator — this benchmark is
// what showed that fork is needed.) Both orders matter — clustered rows give each decode
// segment its own time range, so workers keep overwriting each other's
// span; random rows miss the span on nearly every lookup.
//
//	go test ./internal/service/ -run='^$' -bench='BenchmarkDateOps_(ParallelDecode|ShardWorkers)' -benchmem -count=6
// ---------------------------------------------------------------------

const (
	dateOpsParallelRows   = 400_000
	dateOpsArchiveShards  = 8
	dateOpsParallelMaxCPU = 8
)

var dateOpsParallelZones = []string{"", "Europe/Berlin"}

// dateOpsParallelTimestamps: dateOpsParallelRows strictly increasing
// instants from 2023-01-01, ~2 years (the dateOpsTimestamps shape at
// 4x density).
func dateOpsParallelTimestamps() []int64 {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	out := make([]int64, dateOpsParallelRows)
	for i := range out {
		out[i] = start + int64(i)*157 + int64((i*37)%150)
	}
	return out
}

func dateOpsOrdered(order string) []int64 {
	ts := dateOpsParallelTimestamps()
	if order == "random" {
		r := rand.New(rand.NewPCG(14, 2))
		r.Shuffle(len(ts), func(i, j int) { ts[i], ts[j] = ts[j], ts[i] })
	}
	return ts
}

// dateOpsParallelOps: a zone-aware request the parallel arms really
// fan out. Both arms are mergeable-only and GROUP_DATE is not
// mergeable (types.GroupType.Mergeable), so it always runs serial;
// FILTER_DATE_RANGES (an ungrouped aggregation) is the tightest per-row
// zone loop that does fan out — GROUP_DATE_RANGES shares its seam.
func dateOpsParallelOps(path, tz string) map[string]func() *types.Request {
	return map[string]func() *types.Request{
		"FILTER_DATE_RANGES": func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, TimeZone: tz,
				Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: dateOpsQuarters2024}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}}}
		},
	}
}

// BenchmarkDateOps_ParallelDecode: the DecodeWorkers arm over a real
// on-disk (mmap'd) single-file cohort; the gate is asserted open for
// workers > 1 so the arm cannot silently run serial.
func BenchmarkDateOps_ParallelDecode(b *testing.B) {
	dir := b.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		b.Fatal(err)
	}
	schema := dateOpsSchema()
	for _, order := range dateOpsOrders {
		if err := writeDateOpsCohort(osFs, dir+"/par_"+order+".pulse", schema, dateOpsOrdered(order)); err != nil {
			b.Fatal(err)
		}
	}
	for _, opName := range []string{"FILTER_DATE_RANGES"} {
		for _, order := range dateOpsOrders {
			path := dir + "/par_" + order + ".pulse"
			for _, workers := range []int{1, 4, dateOpsParallelMaxCPU} {
				for _, tz := range dateOpsParallelZones {
					mk := dateOpsParallelOps(path, tz)[opName]
					b.Run(fmt.Sprintf("op=%s/order=%s/workers=%d/tz=%s", opName, order, workers, dateOpsTZName(tz)), func(b *testing.B) {
						svc := New(cfg)
						svc.SetDecodeWorkers(workers)
						if workers > 1 {
							c, err := svc.Open(context.Background(), path)
							if err != nil {
								b.Fatal(err)
							}
							if ok, why := svc.canParallelDecode(mk(), c.Schema(), c, workers, dateOpsParallelRows); !ok {
								b.Fatalf("parallel decode gate closed: %s", why)
							}
						}
						runDateOpsBench(b, svc, mk, tz)
					})
				}
			}
		}
	}
}

// BenchmarkDateOps_ShardWorkers: the per-shard reducer over a
// dateOpsArchiveShards-shard archive (hermetic MemMapFs — the shard arm
// needs no mmap).
func BenchmarkDateOps_ShardWorkers(b *testing.B) {
	cfg := fs.NewMemMap()
	schema := dateOpsSchema()
	ctx := context.Background()
	for _, order := range dateOpsOrders {
		ts := dateOpsOrdered(order)
		per := len(ts) / dateOpsArchiveShards
		var shards []string
		for i := range dateOpsArchiveShards {
			p := fmt.Sprintf("shard_%s_%d.pulse", order, i)
			if err := writeDateOpsCohort(cfg.Fs(), p, schema, ts[i*per:(i+1)*per]); err != nil {
				b.Fatal(err)
			}
			shards = append(shards, p)
		}
		if _, err := New(cfg).CreateShardArchive(ctx, "arch_"+order+".pulse", shards); err != nil {
			b.Fatal(err)
		}
	}
	for _, opName := range []string{"FILTER_DATE_RANGES"} {
		for _, order := range dateOpsOrders {
			for _, workers := range []int{1, dateOpsArchiveShards} {
				for _, tz := range dateOpsParallelZones {
					mk := dateOpsParallelOps("arch_"+order+".pulse", tz)[opName]
					b.Run(fmt.Sprintf("op=%s/order=%s/workers=%d/tz=%s", opName, order, workers, dateOpsTZName(tz)), func(b *testing.B) {
						svc := New(cfg)
						svc.SetShardWorkers(workers)
						runDateOpsBench(b, svc, mk, tz)
					})
				}
			}
		}
	}
}
