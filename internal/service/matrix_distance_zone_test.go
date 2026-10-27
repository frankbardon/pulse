package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_distance_zone_test.go is TODO #411's date half: a date-family
// grouper (GROUP_DATE, GROUP_DATE_RANGES) as MAT_DISTANCE's
// params.between, read on the LOCAL calendar day of the zone the zone
// pass resolves for the slot "matrices[0].params.between" — inherited
// from the request time_zone (ZonedRequest writes it into the between)
// or set on the between itself. The oracle is the same MAT_DISTANCE
// with a GROUP_CATEGORY between over a column precomputed from
// time.In: the centroid arithmetic is pinned against R elsewhere
// (TestMatrixDistance_MatchesROracle), so equal buckets must give equal
// bits.

const distZoneRows = 240

// distZoneRanges are three-day ranges over the first month: a UTC-day
// reading moves the late-evening rows across their boundaries.
const distZoneRanges = `{"ranges": [` +
	`{"label": "r1", "start": "2026-03-26", "end": "2026-03-28"},` +
	`{"label": "r2", "start": "2026-03-29", "end": "2026-03-31"},` +
	`{"label": "r3", "start": "2026-04-01", "end": "2026-04-03"},` +
	`{"label": "r4", "start": "2026-04-04", "end": "2026-04-06"},` +
	`{"label": "r5", "start": "2026-04-07", "end": "2026-04-09"}]}`

func distZoneRangeLabel(day string) string {
	switch {
	case day >= "2026-03-26" && day <= "2026-03-28":
		return "r1"
	case day >= "2026-03-29" && day <= "2026-03-31":
		return "r2"
	case day >= "2026-04-01" && day <= "2026-04-03":
		return "r3"
	case day >= "2026-04-04" && day <= "2026-04-06":
		return "r4"
	case day >= "2026-04-07" && day <= "2026-04-09":
		return "r5"
	}
	return "unmatched"
}

// distZoneCohort writes distZoneRows rows: an instant ts every 3h 17m
// from 2026-03-26 (the Europe/Berlin spring change inside), members x1
// / x3, and three categorical oracle columns — bday (the Berlin local
// day), brange (its range label) and uday (the UTC day).
func distZoneCohort(t *testing.T) *fs.Config {
	t.Helper()
	z, err := temporal.LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC).Unix()
	ts := make([]int64, distZoneRows)
	bday, brange, uday := make([]string, distZoneRows), make([]string, distZoneRows), make([]string, distZoneRows)
	for i := range ts {
		ts[i] = start + int64(i)*(3*3600+17*60)
		bday[i] = time.Unix(ts[i], 0).In(z.Location()).Format("2006-01-02")
		brange[i] = distZoneRangeLabel(bday[i])
		uday[i] = time.Unix(ts[i], 0).UTC().Format("2006-01-02")
	}
	if slices.Equal(bday, uday) {
		t.Fatal("fixture proves nothing: Berlin and UTC days coincide")
	}
	dict := func(labels []string) (*encoding.Dictionary, map[string]uint64) {
		uniq := slices.Clone(labels)
		slices.Sort(uniq)
		uniq = slices.Compact(uniq)
		d := encoding.NewDictionary()
		idx := map[string]uint64{}
		for i, l := range uniq {
			if _, err := d.Add(l); err != nil {
				t.Fatalf("dict.Add: %v", err)
			}
			idx[l] = uint64(i)
		}
		return d, idx
	}
	bd, bdi := dict(bday)
	br, bri := dict(brange)
	ud, udi := dict(uday)
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "x1", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
		{Name: "x3", Type: encoding.FieldTypeF64, ByteOffset: 16, CsvColumnIdx: 2},
		{Name: "bday", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 24, CsvColumnIdx: 3, Dictionary: bd},
		{Name: "brange", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 25, CsvColumnIdx: 4, Dictionary: br},
		{Name: "uday", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 26, CsvColumnIdx: 5, Dictionary: ud},
	}}
	recs := make([][]uint64, distZoneRows)
	for i := range recs {
		x1 := 10*math.Sin(float64(i)) + float64(i%7)
		x3 := 5*math.Cos(0.7*float64(i)) + float64(i%5)
		recs[i] = []uint64{uint64(ts[i]), math.Float64bits(x1), math.Float64bits(x3), bdi[bday[i]], bri[brange[i]], udi[uday[i]]}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "z.pulse", writeNullablePulse(t, schema, recs, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func distZoneReq(between, tz string) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "z.pulse"}, TimeZone: tz,
		Matrices: []types.MatrixSpec{{Name: "d", Type: types.MAT_DISTANCE, Fields: []string{"x1", "x3"},
			Params: json.RawMessage(`{"between": ` + between + `, "metric": "mahalanobis", "standardize": true}`)}}}
}

// TestMatrixDistance_DateBetweenReadsTheZone: a GROUP_DATE (day) or
// GROUP_DATE_RANGES between buckets by the Europe/Berlin local day —
// inherited from the request zone or set on the between — and gives
// byte-identically the GROUP_CATEGORY answer over the precomputed
// Berlin column; with no zone it is the UTC-day answer, which differs.
// The caller's request stays as written (the zone rides a copy).
func TestMatrixDistance_DateBetweenReadsTheZone(t *testing.T) {
	t.Parallel()

	cfg := distZoneCohort(t)
	run := func(req *types.Request) string {
		t.Helper()
		resp := processMatrices(t, cfg, req)
		return mustJSON(t, map[string]any{"m": resp.Matrices, "c": resp.Components.Matrices})
	}
	berlinDays := run(distZoneReq(`{"type": "GROUP_CATEGORY", "field": "bday"}`, ""))
	utcDays := run(distZoneReq(`{"type": "GROUP_CATEGORY", "field": "uday"}`, ""))
	berlinRanges := run(distZoneReq(`{"type": "GROUP_CATEGORY", "field": "brange"}`, ""))
	if berlinDays == utcDays {
		t.Fatal("fixture proves nothing: the Berlin and UTC distances coincide")
	}
	for _, c := range []struct {
		name, between, tz, want string
	}{
		{"GROUP_DATE inherited", `{"type": "GROUP_DATE", "field": "ts", "params": {"component": "day"}}`, "Europe/Berlin", berlinDays},
		{"GROUP_DATE slot tz", `{"type": "GROUP_DATE", "field": "ts", "tz": "Europe/Berlin", "params": {"component": "day"}}`, "", berlinDays},
		{"GROUP_DATE slot tz wins", `{"type": "GROUP_DATE", "field": "ts", "tz": "Europe/Berlin", "params": {"component": "day"}}`, "UTC", berlinDays},
		{"GROUP_DATE no zone", `{"type": "GROUP_DATE", "field": "ts", "params": {"component": "day"}}`, "", utcDays},
		{"GROUP_DATE_RANGES inherited", `{"type": "GROUP_DATE_RANGES", "field": "ts", "params": ` + distZoneRanges + `}`, "Europe/Berlin", berlinRanges},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := distZoneReq(c.between, c.tz)
			before := string(req.Matrices[0].Params)
			if got := run(req); got != c.want {
				t.Errorf("date between\n got %s\nwant %s", got, c.want)
			}
			if string(req.Matrices[0].Params) != before {
				t.Errorf("caller params rewritten: %s", req.Matrices[0].Params)
			}
		})
	}
}

// TestMatrixDistance_DateBetweenPredictMatchesRuntime: predict echoes
// the between's resolved zone at slot matrices[0].params.between,
// reports the run's streamability, and refuses exactly what the run
// refuses — a tz on a between that is not zone-capable, an unknown
// zone, a GROUP_DATE hour component — with the same code and message.
func TestMatrixDistance_DateBetweenPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	cfg := distZoneCohort(t)
	data, err := afero.ReadFile(cfg.Fs(), "z.pulse")
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := New(cfg).Open(context.Background(), "z.pulse")
	if err != nil {
		t.Fatal(err)
	}
	schema := cohort.Schema()
	for _, c := range []struct {
		name, between, tz, source string
		streamable                bool
	}{
		{"day", `{"type": "GROUP_DATE", "field": "ts", "params": {"component": "day"}}`, "Europe/Berlin", descriptor.ZoneSourceRequest, false},
		{"slot", `{"type": "GROUP_DATE", "field": "ts", "tz": "Europe/Berlin", "params": {"component": "day"}}`, "", descriptor.ZoneSourceSlot, false},
		{"ranges", `{"type": "GROUP_DATE_RANGES", "field": "ts", "params": ` + distZoneRanges + `}`, "Europe/Berlin", descriptor.ZoneSourceRequest, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := distZoneReq(c.between, c.tz)
			env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %v", env.Errors)
			}
			res := env.Data.(*descriptor.PredictResult)
			var found bool
			for _, z := range res.TimeZones {
				if z.Slot == "matrices[0].params.between" {
					found = true
					if z.TZ == nil || *z.TZ != "Europe/Berlin" || z.Source != c.source || z.FieldType != "datetime" {
						t.Errorf("zone echo %+v", z)
					}
				}
			}
			if !found {
				t.Errorf("no between zone in %+v", res.TimeZones)
			}
			if mp := res.Matrices[0]; mp.Streamable != c.streamable || processing.CanStreamRequest(req, schema) != c.streamable {
				t.Errorf("predict streamable %v, runtime %v, want %v", mp.Streamable, processing.CanStreamRequest(req, schema), c.streamable)
			}
			if _, err := New(cfg).Process(context.Background(), req); err != nil {
				t.Errorf("run refused what predict accepts: %v", err)
			}
		})
	}
	for _, c := range []struct {
		name, between, tz string
		code              errors.Code
	}{
		{"tz on a category between", `{"type": "GROUP_CATEGORY", "field": "bday", "tz": "Europe/Berlin"}`, "", errors.PROCESSING_CONFIG},
		{"unknown between tz", `{"type": "GROUP_DATE", "field": "ts", "tz": "Mars/Olympus"}`, "", errors.PULSE_TIMEZONE_UNKNOWN},
		// A non-date-family field is the accepted-types table's refusal,
		// raised before the sub-day datetime rule.
		{"hour over a date-free field", `{"type": "GROUP_DATE", "field": "x1", "params": {"component": "hour"}}`, "", errors.PULSE_FIELD_TYPE_UNSUPPORTED},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := distZoneReq(c.between, c.tz)
			env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("run: %v, want %s", err, c.code)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
				t.Errorf("predict %v, run %s %q", env.Errors, ce.Code, ce.Message)
			}
			if c.code == errors.PROCESSING_CONFIG && c.name == "tz on a category between" && ce.Details["slot"] != "matrices[0].params.between" {
				t.Errorf("refusal details %v", ce.Details)
			}
		})
	}
}
