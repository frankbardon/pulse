package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The two U14 roadmap gates. Both are tables: a later story that makes
// another operator (or export) zone-aware adds ROWS — an
// utcIdentityCases entry and a dstOps entry — never a new harness.

// ---------------------------------------------------------------------
// TestUTCZoneIsIdentity
// ---------------------------------------------------------------------

// identityHalves splits 2026 at the June/July boundary, which the
// identity fixture straddles at UTC evening — a Berlin zone moves rows
// across it, UTC never does.
var identityHalves = []pulse.DateRangeSpec{
	{Label: "h1", Start: new("2026-01-01"), End: new("2026-06-30")},
	{Label: "h2", Start: new("2026-07-01"), End: new("2026-12-31")},
}

// identitySecondHalf is the filter's table: keeps local days from July.
var identitySecondHalf = []pulse.DateRangeSpec{{Label: "h2", Start: new("2026-07-01"), End: new("2026-12-31")}}

const identityHalvesInline = `{"ranges":[{"label":"h1","start":"2026-01-01","end":"2026-06-30"},{"label":"h2","start":"2026-07-01","end":"2026-12-31"}]}`

// identityCase is one zone-aware operator shape. mk builds the request
// with the zone on the slot (slotTZ) and/or the request (reqTZ); "" is
// absent.
type identityCase struct {
	name string
	// slotTZ: an explicit slot tz is legal on this shape (false for an
	// operator over a `date` column, where it is a refusal by design).
	slotTZ bool
	// zoneSensitive: a real zone (Europe/Berlin) changes the answer on
	// the fixture — the control that proves the fixture can tell an
	// applied zone from an ignored one.
	zoneSensitive bool
	// arms the shape runs on; nil means every identityArms entry.
	arms []string
	mk   func(cohort, slotTZ, reqTZ string) *types.Request
}

func identityGroupReq(cohort, reqTZ string, g *types.Group) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: cohort}, TimeZone: reqTZ, Aggregations: countAgg(), Groups: []*types.Group{g}}
}

func identityFilterReq(cohort, reqTZ string, f *types.Filterer) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: cohort}, TimeZone: reqTZ, Aggregations: countAgg(),
		Filterers: []*types.Filterer{f}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}}
}

// utcIdentityCases: every zone-aware operator landed so far. Add a row
// per newly zone-aware operator (ATTR_DATE_PART / FEAT_DATE_FEATURES
// over a datetime, export, ...).
var utcIdentityCases = []identityCase{
	{name: "GROUP_DATE/day", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityGroupReq(c, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"day"}`), TimeZone: s})
	}},
	{name: "GROUP_DATE/month", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityGroupReq(c, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"month"}`), TimeZone: s})
	}},
	{name: "GROUP_DATE/hour", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityGroupReq(c, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"hour"}`), TimeZone: s})
	}},
	{name: "GROUP_DATE/week-iso", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityGroupReq(c, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"week"}`), TimeZone: s})
	}},
	{name: "GROUP_DATE/week-sunday", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityGroupReq(c, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"week","week_start":"sunday"}`), TimeZone: s})
	}},
	{name: "crosstab/GROUP_DATE-hour-fused", slotTZ: true, zoneSensitive: true, arms: []string{"process", "compose"}, mk: func(c, s, r string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: c}, TimeZone: r, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"hour"}`), TimeZone: s}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n", Label: "count"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		}}
	}},
	{name: "GROUP_DATE_RANGES/inline", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityGroupReq(c, r, &types.Group{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: json.RawMessage(identityHalvesInline), TimeZone: s})
	}},
	{name: "GROUP_DATE_RANGES/range-table", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityGroupReq(c, r, &types.Group{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: json.RawMessage(`{"table":"halves"}`), TimeZone: s})
	}},
	{name: "FILTER_DATE_RANGES/inline", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityFilterReq(c, r, &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(`{"ranges":[{"label":"h2","start":"2026-07-01"}]}`), TimeZone: s})
	}},
	{name: "FILTER_DATE_RANGES/range-table", slotTZ: true, zoneSensitive: true, mk: func(c, s, r string) *types.Request {
		return identityFilterReq(c, r, &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(`{"table":"second_half"}`), TimeZone: s})
	}},
	{name: "crosstab/GROUP_DATE-fused", slotTZ: true, zoneSensitive: true, arms: []string{"process", "compose"}, mk: func(c, s, r string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: c}, TimeZone: r, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"day"}`), TimeZone: s}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n", Label: "count"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		}}
	}},
	// An inherited zone on a `date` column is never applied, so even a
	// real zone leaves it alone; an explicit slot tz there is refused.
	{name: "ATTR_DATE_PART/date-inherited", mk: func(c, _, r string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: c}, TimeZone: r, Aggregations: countAgg(),
			Attributes: []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "d", Label: "mo", Params: json.RawMessage(`{"part":"month"}`)}},
			Groups:     []*types.Group{{Type: types.GROUP_CATEGORY, Field: "mo"}}}
	}},
}

// identityArms: how a request reaches the wire. Each returns the bytes
// `--json` would print for it (the envelope, data included).
var identityArms = map[string]func(t *testing.T, p *pulse.Pulse, req *types.Request) []byte{
	"process": func(t *testing.T, p *pulse.Pulse, req *types.Request) []byte {
		t.Helper()
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		return identityWire(t, resp)
	},
	"stream": func(t *testing.T, p *pulse.Pulse, req *types.Request) []byte {
		t.Helper()
		ctx := context.Background()
		it, err := p.ProcessStream(ctx, req)
		if err != nil {
			t.Fatalf("ProcessStream: %v", err)
		}
		defer it.Close()
		var rows []map[string]any
		for {
			row, ok, err := it.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			rows = append(rows, row)
		}
		return identityWire(t, rows)
	},
	"compose": func(t *testing.T, p *pulse.Pulse, req *types.Request) []byte {
		t.Helper()
		out, err := p.Compose(context.Background(), &types.ComposedRequest{Requests: []*types.Request{req}})
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		return identityWire(t, out)
	},
}

func identityWire(t *testing.T, data any) []byte {
	t.Helper()
	b, err := json.Marshal(descriptor.NewEnvelope(data))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// identityCohort imports half-hourly instants over the 2026 Berlin
// spring-forward and the June/July month boundary: a `datetime` ts, a
// `date` d (the UTC day of ts), a categorical cat and an integer n.
func identityCohort(t *testing.T, fs afero.Fs) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("ts,d,cat,n\n")
	i := 0
	for _, w := range [][2]string{{"2026-03-28T06:00:00Z", "2026-03-30T06:00:00Z"}, {"2026-06-30T12:00:00Z", "2026-07-01T06:00:00Z"}} {
		from, _ := time.Parse(time.RFC3339, w[0])
		to, _ := time.Parse(time.RFC3339, w[1])
		for at := from; !at.After(to); at = at.Add(30 * time.Minute) {
			fmt.Fprintf(&b, "%s,%s,%s,%d\n", at.Format(time.RFC3339), at.Format("2006-01-02"), []string{"a", "b", "c"}[i%3], i)
			i++
		}
	}
	if err := afero.WriteFile(fs, "identity.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "identity.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	ins, err := p.Inspect(ctx, res.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"d": "date", "ts": "datetime"}
	for _, f := range ins.Fields {
		if w, ok := want[f.Name]; ok && f.Type != w {
			t.Fatalf("field %s inferred as %s, want %s", f.Name, f.Type, w)
		}
	}
	return res.Path
}

// TestUTCZoneIsIdentity (U14 roadmap gate; extends U03's request-level
// TestTimeZone_UTCIdentity, which it replaces): for every zone-aware
// operator, naming UTC — "UTC" or the UTC-equivalent "Etc/UTC", on the
// slot, the request or Options.DefaultTimeZone — prints `--json` output
// byte-identical to naming no zone, on every arm. A Europe/Berlin
// control per zone-sensitive row proves the fixture would notice a zone
// being applied.
func TestUTCZoneIsIdentity(t *testing.T) {
	fs := afero.NewMemMapFs()
	cohort := identityCohort(t, fs)
	newPulse := func(defaultZone string) *pulse.Pulse {
		t.Helper()
		o := pulse.Options{FS: fs, DefaultTimeZone: defaultZone}
		o.Extensions.RangeTables = map[string]pulse.RangeTable{
			"halves":      {Ranges: identityHalves},
			"second_half": {Ranges: identitySecondHalf},
		}
		p, err := pulse.New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := newPulse("")
	berlin := newPulse("")
	variants := []struct{ name, slot, req, opts string }{
		{"request-UTC", "", "UTC", ""},
		{"request-Etc/UTC", "", "Etc/UTC", ""},
		{"slot-UTC", "UTC", "", ""},
		{"slot-Etc/UTC", "Etc/UTC", "", ""},
		{"options-UTC", "", "", "UTC"},
		{"options-Etc/UTC", "", "", "Etc/UTC"},
	}
	instances := map[string]*pulse.Pulse{"": base}
	for _, v := range variants {
		if _, ok := instances[v.opts]; !ok {
			instances[v.opts] = newPulse(v.opts)
		}
	}
	armNames := slices.Sorted(maps.Keys(identityArms))
	for _, tc := range utcIdentityCases {
		arms := tc.arms
		if arms == nil {
			arms = armNames
		}
		for _, arm := range arms {
			run := identityArms[arm]
			want := run(t, base, tc.mk(cohort, "", ""))
			if tc.zoneSensitive {
				t.Run(tc.name+"/"+arm+"/control-Europe/Berlin", func(t *testing.T) {
					if got := run(t, berlin, tc.mk(cohort, "", berlinZone)); string(got) == string(want) {
						t.Fatalf("fixture proves nothing: Europe/Berlin answers like UTC:\n%s", got)
					}
				})
			}
			for _, v := range variants {
				if v.slot != "" && !tc.slotTZ {
					continue
				}
				t.Run(tc.name+"/"+arm+"/"+v.name, func(t *testing.T) {
					got := run(t, instances[v.opts], tc.mk(cohort, v.slot, v.req))
					if string(got) != string(want) {
						t.Fatalf("output differs from the zone-free baseline:\n got %s\nwant %s", got, want)
					}
				})
			}
		}
	}
}

// ---------------------------------------------------------------------
// TestDSTBoundaries
// ---------------------------------------------------------------------

// dstZoneCase is one zone under test. Its fixture is every quarter hour
// from 36h before to 36h after each 2026 offset transition (found by
// scanning time.In, not by trusting a hand-written date) — or around
// anchor when the zone has none.
type dstZoneCase struct {
	zone string
	// transitions is how many 2026 offset changes the zone must have —
	// a fixture guard (a tzdata change that drops one fails loudly).
	transitions int
	// anchor centres the window of a zone with no transition.
	anchor time.Time
	// skipsMidnight: at least one transition jumps from 00:00 to 01:00,
	// so a local day has no instant reading midnight.
	skipsMidnight bool
}

var dstZoneCases = []dstZoneCase{
	{zone: "Europe/Berlin", transitions: 2},
	// Half-hour offset, no DST: every UTC 18:30 starts a new local day.
	{zone: "Asia/Kolkata", anchor: time.Date(2026, 6, 15, 18, 30, 0, 0, time.UTC)},
	// Southern hemisphere: DST ends in April, starts in October.
	{zone: "Australia/Sydney", transitions: 2},
	// Southern hemisphere AND a skipped midnight (spring forward
	// 00:00 -> 01:00 in September).
	{zone: "America/Santiago", transitions: 2, skipsMidnight: true},
}

// dstFixture is one zone's imported cohort plus what the reference
// needs: the instants (row i ↔ instants[i], cat = i%3) and the focus
// days the range operators label — the local day of each transition
// (the anchor's for a transition-free zone) and of each window start.
type dstFixture struct {
	zone      string
	loc       *time.Location
	cohort    string
	instants  []int64
	focusDays []string
}

func (f *dstFixture) localDay(s int64) string {
	return time.Unix(s, 0).In(f.loc).Format("2006-01-02")
}

// rangeSpecs labels each focus day as its own range (in date order).
func (f *dstFixture) rangeSpecs() string {
	var parts []string
	for i, d := range f.focusDays {
		parts = append(parts, fmt.Sprintf(`{"label":"focus_%d","start":%q,"end":%q}`, i, d, d))
	}
	return `{"ranges":[` + strings.Join(parts, ",") + `]}`
}

func (f *dstFixture) rangeLabel(day string) string {
	if i := slices.Index(f.focusDays, day); i >= 0 {
		return fmt.Sprintf("focus_%d", i)
	}
	return "unmatched"
}

// dstOp is one zone-aware operator under the DST fixtures: mk builds the
// request (zone on the slot or the request), key is the response column
// counted by, want is the time.In reference. Add a row per newly
// zone-aware operator.
type dstOp struct {
	name string
	key  string
	mk   func(f *dstFixture, slotTZ, reqTZ string) *types.Request
	// want is the reference; at reads an instant on the clock under
	// test (the zone's, or UTC for the control).
	want func(f *dstFixture, at func(int64) time.Time) map[string]float64
}

// dstDay is the calendar day at shows for instant s.
func dstDay(at func(int64) time.Time, s int64) string { return at(s).Format("2006-01-02") }

// weekStart is the week start the week_start row uses on f: the weekday
// of f's first focus day, so a week boundary falls on a local midnight
// inside the window in every zone (Berlin / Sydney transition on a
// Sunday; the Kolkata anchor day is a Tuesday).
func (f *dstFixture) weekStart() time.Weekday {
	d, _ := time.Parse("2006-01-02", f.focusDays[0])
	return d.Weekday()
}

// weekKey is the time.In reference for a GROUP_DATE week key under
// week start ws: ISO `YYYY-Www` for Monday, else the date of the week's
// first day.
func weekKey(t time.Time, ws time.Weekday) string {
	if ws == time.Monday {
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	}
	return t.AddDate(0, 0, -((int(t.Weekday()) - int(ws) + 7) % 7)).Format("2006-01-02")
}

var dstOps = []dstOp{
	{
		name: "GROUP_DATE/day", key: "ts",
		mk: func(f *dstFixture, s, r string) *types.Request {
			return identityGroupReq(f.cohort, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"day"}`), TimeZone: s})
		},
		want: func(f *dstFixture, at func(int64) time.Time) map[string]float64 {
			out := map[string]float64{}
			for _, s := range f.instants {
				out[dstDay(at, s)]++
			}
			return out
		},
	},
	{
		// The fall-back hour merges (one bucket, twice the rows); the
		// spring-forward hour has no bucket — time.In formats the same.
		name: "GROUP_DATE/hour", key: "ts",
		mk: func(f *dstFixture, s, r string) *types.Request {
			return identityGroupReq(f.cohort, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"hour"}`), TimeZone: s})
		},
		want: func(f *dstFixture, at func(int64) time.Time) map[string]float64 {
			out := map[string]float64{}
			for _, s := range f.instants {
				out[at(s).Format("2006-01-02T15")]++
			}
			return out
		},
	},
	{
		name: "GROUP_DATE/week_start", key: "ts",
		mk: func(f *dstFixture, s, r string) *types.Request {
			params := fmt.Sprintf(`{"component":"week","week_start":%q}`, strings.ToLower(f.weekStart().String()))
			return identityGroupReq(f.cohort, r, &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(params), TimeZone: s})
		},
		want: func(f *dstFixture, at func(int64) time.Time) map[string]float64 {
			out := map[string]float64{}
			for _, s := range f.instants {
				out[weekKey(at(s), f.weekStart())]++
			}
			return out
		},
	},
	{
		name: "GROUP_DATE_RANGES", key: "ts",
		mk: func(f *dstFixture, s, r string) *types.Request {
			return identityGroupReq(f.cohort, r, &types.Group{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: json.RawMessage(f.rangeSpecs()), TimeZone: s})
		},
		want: func(f *dstFixture, at func(int64) time.Time) map[string]float64 {
			out := map[string]float64{}
			for _, s := range f.instants {
				out[f.rangeLabel(dstDay(at, s))]++
			}
			return out
		},
	},
	{
		name: "FILTER_DATE_RANGES", key: "cat",
		mk: func(f *dstFixture, s, r string) *types.Request {
			return identityFilterReq(f.cohort, r, &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(f.rangeSpecs()), TimeZone: s})
		},
		want: func(f *dstFixture, at func(int64) time.Time) map[string]float64 {
			out := map[string]float64{}
			for i, s := range f.instants {
				if f.rangeLabel(dstDay(at, s)) != "unmatched" {
					out[[]string{"a", "b", "c"}[i%3]]++
				}
			}
			return out
		},
	},
}

// zoneTransitions2026 returns every instant in 2026 at which loc's
// offset changes, found by an hourly time.In scan and bisected to the
// second.
func zoneTransitions2026(loc *time.Location) []time.Time {
	offset := func(t time.Time) int { _, o := t.In(loc).Zone(); return o }
	var out []time.Time
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	for at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); at.Before(end); at = at.Add(time.Hour) {
		next := at.Add(time.Hour)
		if offset(at) == offset(next) {
			continue
		}
		lo, hi := at, next
		for hi.Sub(lo) > time.Second {
			mid := lo.Add(hi.Sub(lo) / 2)
			if offset(mid) == offset(lo) {
				lo = mid
			} else {
				hi = mid
			}
		}
		out = append(out, hi)
	}
	return out
}

func buildDSTFixture(t *testing.T, p *pulse.Pulse, fs afero.Fs, zc dstZoneCase) *dstFixture {
	t.Helper()
	z, err := temporal.LoadZone(zc.zone)
	if err != nil {
		t.Fatal(err)
	}
	f := &dstFixture{zone: zc.zone, loc: z.Location()}
	centres := zoneTransitions2026(f.loc)
	if len(centres) != zc.transitions {
		t.Fatalf("%s: %d transitions in 2026, the fixture expects %d", zc.zone, len(centres), zc.transitions)
	}
	if zc.skipsMidnight {
		skipped := false
		for _, c := range centres {
			before, after := c.Add(-time.Second).In(f.loc), c.In(f.loc)
			if after.Hour() == 1 && after.Minute() == 0 && before.Day() != after.Day() {
				skipped = true
			}
		}
		if !skipped {
			t.Fatalf("%s: no 2026 transition skips local midnight", zc.zone)
		}
	}
	if len(centres) == 0 {
		centres = []time.Time{zc.anchor}
	}
	for _, c := range centres {
		f.focusDays = append(f.focusDays, c.In(f.loc).Format("2006-01-02"))
		first := c.Add(-36 * time.Hour).Truncate(15 * time.Minute)
		for at := first; !at.After(c.Add(36 * time.Hour)); at = at.Add(15 * time.Minute) {
			f.instants = append(f.instants, at.Unix())
		}
		// The window's first, PARTIAL local day too: a whole day holds
		// as many quarter hours locally as in UTC, so with only whole
		// days a fixed-offset zone (Kolkata) would count alike.
		if d := f.localDay(first.Unix()); !slices.Contains(f.focusDays, d) {
			f.focusDays = append(f.focusDays, d)
		}
	}
	name := strings.ReplaceAll(zc.zone, "/", "_")
	if err := afero.WriteFile(fs, name+".csv", []byte(dstCSV(f.instants)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: name + ".csv"})
	if err != nil {
		t.Fatalf("ImportFile %s: %v", name, err)
	}
	f.cohort = res.Path
	return f
}

// TestDSTBoundaries (U14 roadmap gate): across spring-forward and
// fall-back in Europe/Berlin, a half-hour offset (Asia/Kolkata), the
// southern hemisphere (Australia/Sydney) and a skipped local midnight
// (America/Santiago), every zone-aware operator equals a time.In
// reference over the embedded zone — with the zone on the slot or on
// the request. A per-zone UTC control proves the fixture tells the
// local day from the UTC day.
func TestDSTBoundaries(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	utcAt := func(s int64) time.Time { return time.Unix(s, 0).UTC() }
	for _, zc := range dstZoneCases {
		f := buildDSTFixture(t, p, fs, zc)
		localAt := func(s int64) time.Time { return time.Unix(s, 0).In(f.loc) }
		for _, op := range dstOps {
			want := op.want(f, localAt)
			if maps.Equal(want, op.want(f, utcAt)) {
				t.Fatalf("%s/%s: fixture proves nothing — the local and UTC answers coincide", zc.zone, op.name)
			}
			for _, src := range []struct{ name, slot, req string }{{"slot", zc.zone, ""}, {"request", "", zc.zone}} {
				t.Run(zc.zone+"/"+op.name+"/"+src.name, func(t *testing.T) {
					resp, err := p.Process(ctx, op.mk(f, src.slot, src.req))
					if err != nil {
						t.Fatal(err)
					}
					requireCounts(t, dayCountsFromRows(t, resp.Data, op.key), want)
				})
			}
		}
	}
}
