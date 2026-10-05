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
