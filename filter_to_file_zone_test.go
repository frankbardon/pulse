package pulse_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// filter_to_file_zone_test.go pins U34 E3-S5: FilterToFileWithRequest
// runs the zone-capable FILTER_DATE_RANGES — over a `date` and a
// `datetime` field, under the request `time_zone`, a per-filterer `tz`
// and a per-row `tz_field` — resolving its zone exactly as Process does,
// and the resolved zone keys the dedup predicate hash, so two requests
// that differ only in zone never share an output. Before, the type was
// refused PROCESSING_CONFIG reason "zone_capable".

// ftzZones are the calendar cohort's `cat` labels, here zone names, so
// `cat` serves as a per-row `tz_field` (row r carries ftzZones[r%4]):
// row 9, whose instant leaves ftzRanges' window in New York, reads New
// York.
var ftzZones = []string{"UTC", "America/New_York", "Pacific/Honolulu", "Europe/Berlin"}

// ftzWrite writes a calendar cohort of rows records at path: record r
// repeats calendar row r%calfRows, its `cat` carrying a zone name.
func ftzWrite(t *testing.T, fsys afero.Fs, path string, rows int) {
	t.Helper()
	s := calfSchema(t, ftzZones)
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	stride, bm := s.RecordByteSize(), s.BitmapByteSize()
	for i := 0; i < rows; i++ {
		r := i % calfRows
		rec := make([]byte, stride)
		binary.LittleEndian.PutUint32(rec[s.Fields[0].ByteOffset:], uint32(calfDay(r)))
		binary.LittleEndian.PutUint64(rec[s.Fields[1].ByteOffset:], uint64(calfSecond(r)))
		if calfDnNull(r) {
			encoding.BitmapSetNull(rec[stride-bm:], 2)
		} else {
			binary.LittleEndian.PutUint32(rec[s.Fields[2].ByteOffset:], uint32(calfDay(r)+3))
		}
		binary.LittleEndian.PutUint64(rec[s.Fields[3].ByteOffset:], math.Float64bits(float64(r%7)))
		binary.LittleEndian.PutUint64(rec[s.Fields[4].ByteOffset:], math.Float64bits(float64(r)))
		rec[s.Fields[5].ByteOffset] = byte(r % len(ftzZones))
		buf = append(buf, rec...)
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ftzMode is one execution arm: an instance and the cohort it reads.
type ftzMode struct {
	p    *pulse.Pulse
	path string
	rows int
}

// ftzOpts adds the test's extension filterer to opts.
func ftzOpts(opts pulse.Options) pulse.Options {
	opts.Extensions = pulse.Extensions{Filterers: []pulse.FiltererRegistration{{
		Name:    ftzExtFilter,
		Factory: func() extend.FiltererBuilder { return ftzPositiveX{} },
	}}}
	return opts
}

// ftzExtFilter keeps the rows whose x is positive; it is not
// zone-capable, so a `tz` on it is refused like any such operator.
const ftzExtFilter = types.FiltererType("FILTER_FTZ_POSITIVE_X")

type ftzPositiveX struct{}

func (ftzPositiveX) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(rec extend.Record) (bool, error) {
		x, ok := rec.NumericValue("x")
		return ok && x > 0, nil
	}, nil
}

// ftzModes: a single file, a cohort past the parallel-decode threshold
// on a DecodeWorkers instance, and a three-shard archive.
func ftzModes(t *testing.T) map[string]ftzMode {
	t.Helper()
	out := map[string]ftzMode{}
	fsys := afero.NewMemMapFs()
	ftzWrite(t, fsys, "c.pulse", calfRows)
	p, err := pulse.New(ftzOpts(pulse.Options{FS: fsys}))
	if err != nil {
		t.Fatal(err)
	}
	out["single_file"] = ftzMode{p, "c.pulse", calfRows}

	lfs := afero.NewMemMapFs()
	ftzWrite(t, lfs, "large.pulse", parityLargeRows)
	lp, err := pulse.New(ftzOpts(pulse.Options{FS: lfs, DecodeWorkers: 4}))
	if err != nil {
		t.Fatal(err)
	}
	out["parallel_decode"] = ftzMode{lp, "large.pulse", parityLargeRows}

	sfs := afero.NewMemMapFs()
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for _, path := range shards {
		ftzWrite(t, sfs, path, calfRows)
	}
	sp, err := pulse.New(ftzOpts(pulse.Options{FS: sfs, ShardWorkers: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	out["shard_archive"] = ftzMode{sp, "archive.pulse", 3 * calfRows}
	return out
}

// ftzRanges is a FILTER_DATE_RANGES params window over calendar rows
// 9..30. Row 9's instant (02:00Z) is the previous local day in New York
// and Honolulu, so a zone moves it out of the window.
func ftzRanges(t *testing.T) json.RawMessage {
	t.Helper()
	iso := func(day int64) string { return time.Unix(day*86400, 0).UTC().Format("2006-01-02") }
	raw, err := json.Marshal(map[string]any{"ranges": []map[string]any{{"label": "w", "start": iso(calfDay(9)), "end": iso(calfDay(30))}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ftzCount is Process's AGG_COUNT under filterers and request zone tz.
func ftzCount(t *testing.T, m ftzMode, filterers []*types.Filterer, tz string) int64 {
	t.Helper()
	resp, err := m.p.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: m.path},
		Filterers:    filterers,
		TimeZone:     tz,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return zepCount(t, resp.Data[0]["n"])
}

// ftzUTC is filterers with every zone stripped.
func ftzUTC(filterers []*types.Filterer) []*types.Filterer {
	out := make([]*types.Filterer, len(filterers))
	for i, f := range filterers {
		c := *f
		c.TimeZone, c.TimeZoneField = "", ""
		out[i] = &c
	}
	return out
}

// TestFilterToFile_ResolvedZoneKeysDedup: the zone a FILTER_DATE_RANGES
// resolves keys the dedup predicate hash, whichever knob named it —
// request `time_zone`, Options.DefaultTimeZone, a per-filterer `tz` or
// `tz_field` — so two requests differing only in resolved zone never
// share an output (no stale reuse), and the same resolved zone lands on
// the same output. A `date` field applies no inherited zone, so its
// output is shared across zones.
func TestFilterToFile_ResolvedZoneKeysDedup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	ftzWrite(t, fsys, "c.pulse", calfRows)
	newP := func(def string) *pulse.Pulse {
		p, err := pulse.New(ftzOpts(pulse.Options{FS: fsys, DefaultTimeZone: def}))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	utc, ny := newP(""), newP("America/New_York")
	params := ftzRanges(t)
	ts := []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params}}
	run := func(p *pulse.Pulse, filterers []*types.Filterer, tz string) *pulse.FilterToFileResult {
		t.Helper()
		res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: "c.pulse", Filterers: filterers, TimeZone: tz, OutputDir: "ftz-dedup"})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	m := ftzMode{utc, "c.pulse", calfRows}

	a := run(utc, ts, "")
	if a.Reused || a.RowCount != ftzCount(t, m, ts, "") {
		t.Fatalf("UTC run: rows %d reused=%v", a.RowCount, a.Reused)
	}
	// UTC named is UTC absent: the same output.
	if b := run(utc, ts, "UTC"); !b.Reused || b.OutputPath != a.OutputPath {
		t.Fatalf("request UTC: path %s reused=%v, want %s", b.OutputPath, b.Reused, a.OutputPath)
	}
	// The request zone: a fresh output with New York's rows.
	c := run(utc, ts, "America/New_York")
	if c.Reused || c.OutputPath == a.OutputPath || c.RowCount != ftzCount(t, m, ts, "America/New_York") {
		t.Fatalf("request NY: path %s rows %d reused=%v", c.OutputPath, c.RowCount, c.Reused)
	}
	// The same zone through the instance default: the same output.
	if d := run(ny, ts, ""); !d.Reused || d.OutputPath != c.OutputPath {
		t.Fatalf("default NY: path %s reused=%v, want %s", d.OutputPath, d.Reused, c.OutputPath)
	}
	// The request zone wins over the default.
	if e := run(ny, ts, "UTC"); !e.Reused || e.OutputPath != a.OutputPath {
		t.Fatalf("request UTC over default NY: path %s reused=%v, want %s", e.OutputPath, e.Reused, a.OutputPath)
	}
	// A third zone, a per-filterer tz and a tz_field are outputs of their own.
	seen := map[string]bool{a.OutputPath: true, c.OutputPath: true}
	for name, f := range map[string][]*types.Filterer{
		"request_tokyo": ts,
		"slot_tz":       {{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params, TimeZone: "Pacific/Honolulu"}},
		"tz_field":      {{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params, TimeZoneField: "cat"}},
	} {
		tz := ""
		if name == "request_tokyo" {
			tz = "Asia/Tokyo"
		}
		r := run(utc, f, tz)
		if r.Reused || seen[r.OutputPath] {
			t.Fatalf("%s reused output %s", name, r.OutputPath)
		}
		seen[r.OutputPath] = true
	}
	// A date field applies no inherited zone: one output across zones.
	d := []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "d", Params: params}}
	f, g := run(utc, d, ""), run(ny, d, "Asia/Tokyo")
	if !g.Reused || g.OutputPath != f.OutputPath {
		t.Fatalf("date field: paths %s / %s reused=%v", f.OutputPath, g.OutputPath, g.Reused)
	}
}

// TestFilterToFile_ZoneRefusalsMatchProcess: every zone refusal on a
// structured filter-to-file filterer is Process's own — code, message
// and details, the slot index the request's — refused before any output
// is written. An extension filterer is not zone-capable, so its `tz` /
// `tz_field` is refused as on any other such operator, built-in or not.
func TestFilterToFile_ZoneRefusalsMatchProcess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	ftzWrite(t, fsys, "c.pulse", calfRows)
	p, err := pulse.New(ftzOpts(pulse.Options{FS: fsys}))
	if err != nil {
		t.Fatal(err)
	}
	params := ftzRanges(t)
	ok := &types.Filterer{Type: types.FILTER_RANGE, Field: "x", Values: []string{"0", "9"}}
	cases := []struct {
		name string
		f    *types.Filterer
		code errors.Code
	}{
		{"extension_tz", &types.Filterer{Type: ftzExtFilter, Field: "x", TimeZone: "UTC"}, errors.PROCESSING_CONFIG},
		{"extension_tz_field", &types.Filterer{Type: ftzExtFilter, Field: "x", TimeZoneField: "cat"}, errors.PROCESSING_CONFIG},
		{"translated_tz", &types.Filterer{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{"UTC"}, TimeZone: "UTC"}, errors.PROCESSING_CONFIG},
		{"set_family_tz_field", &types.Filterer{Type: types.FILTER_TRUE, Field: "x", TimeZoneField: "cat"}, errors.PROCESSING_CONFIG},
		{"date_field_tz", &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "d", Params: params, TimeZone: "America/New_York"}, errors.PROCESSING_CONFIG},
		{"tz_and_tz_field", &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params, TimeZone: "UTC", TimeZoneField: "cat"}, errors.PROCESSING_CONFIG},
		{"unknown_tz", &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params, TimeZone: "Mars/Olympus"}, errors.PULSE_TIMEZONE_UNKNOWN},
		{"tz_field_not_categorical", &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params, TimeZoneField: "x"}, errors.PROCESSING_CONFIG},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			filterers := []*types.Filterer{ok, c.f}
			_, perr := p.Process(ctx, &types.Request{
				Cohort: &types.Cohort{Filename: "c.pulse"}, Filterers: filterers,
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			})
			want := requireCode(t, perr, c.code)
			_, ferr := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: "c.pulse", Filterers: filterers, OutputDir: "ftz-refused"})
			got := requireCode(t, ferr, c.code)
			if got.Message != want.Message || !reflect.DeepEqual(got.Details, want.Details) {
				t.Fatalf("filter_to_file refusal %q %v, Process %q %v", got.Message, got.Details, want.Message, want.Details)
			}
			if names, _ := afero.Glob(fsys, "ftz-refused/*"); len(names) != 0 {
				t.Fatalf("refused request wrote %v", names)
			}
		})
	}
}
