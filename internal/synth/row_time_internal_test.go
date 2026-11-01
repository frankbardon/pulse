package synth

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// seq renders "[a, a+1, …]" of n entries, the weights literal that makes
// season() return its own index (+a).
func seq(a, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprint(a + i)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func f64Out(name string) FieldSpec {
	return FieldSpec{Name: name, Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 0.0}}
}

// timeEffectsSpec: a per-row-zone datetime, a fixed-zone datetime and a
// date, each read by trend / season / between into f64 outputs.
func timeEffectsSpec(extra ...RuleSpec) *Spec {
	fields := []FieldSpec{
		{Name: "site", Type: "categorical_u8", Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{"Asia/Kolkata", "America/New_York", "UTC"}}},
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2026-01-05", "end": "2026-12-27", "time_zone_field": "site", "dst_policy": "earlier"}},
		{Name: "fixed", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2026-02-01", "end": "2026-11-30", "time_zone": "Australia/Sydney", "dst_policy": "later"}},
		{Name: "d", Type: "date", Distribution: DistUniformDate, Params: map[string]any{"start": "2026-01-01", "end": "2026-12-31"}},
	}
	for _, n := range []string{"tr", "hr", "dw", "mo", "win", "win2", "ftr", "fhr", "dtr", "ddw", "dmo", "dwin", "ntr"} {
		fields = append(fields, f64Out(n))
	}
	rules := append(extra, RuleSpec{SetExpr: map[string]string{
		"tr":   "trend(ts, 2)",
		"hr":   `season(ts, "hour", ` + seq(0, 24) + `)`,
		"dw":   `season(ts, "dow", ` + seq(1, 7) + `)`,
		"mo":   `season(ts, "month", ` + seq(1, 12) + `)`,
		"win":  `between(ts, "2026-05-12", "2026-05-14") ? 1 : 0`,
		"win2": `between(ts, "2026-05-12T09:30:00", "2026-05-19 17:00") ? 1 : 0`,
		"ftr":  "trend(fixed, 0.5)",
		"fhr":  `season(fixed, "hour", ` + seq(0, 24) + `)`,
		"dtr":  "trend(d, 1)",
		"ddw":  `season(d, "dow", ` + seq(1, 7) + `)`,
		"dmo":  `season(d, "month", ` + seq(1, 12) + `)`,
		"dwin": `between(d, "2026-03-01", "2026-03-31") ? 1 : 0`,
		"ntr":  "trend(row(), 0.5)",
	}})
	return &Spec{RowCount: 1500, Fields: fields, Rules: rules}
}

func loadLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestRowTime_ReadsTheFieldsClock: every time function reads a datetime
// on its FIELD's clock — the row's own zone for a time_zone_field, the
// fixed zone otherwise — and a date as a calendar day. Each output is
// checked against an independent time.In reference.
func TestRowTime_ReadsTheFieldsClock(t *testing.T) {
	t.Parallel()

	data, _, err := SynthBytes(timeEffectsSpec(), Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	rows, _ := datetimeRows(t, data)
	syd := loadLoc(t, "Australia/Sydney")
	dayStart := func(day string, loc *time.Location) time.Time {
		d, _ := time.ParseInLocation("2006-01-02", day, loc)
		return d
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	b2f := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	sawWindow, sawWin2 := false, false
	for i, r := range rows {
		loc := loadLoc(t, r["site"].(string))
		ts := time.Unix(int64(r["ts"].(float64)), 0).In(loc)
		// Elapsed on the LOCAL clock: wall reading minus the wall
		// reading of local start midnight, in days.
		wall := func(x time.Time) float64 {
			y, m, d := x.Date()
			return float64(time.Date(y, m, d, x.Hour(), x.Minute(), x.Second(), 0, time.UTC).Unix())
		}
		elapsed := (wall(ts) - wall(dayStart("2026-01-05", loc))) / 86400
		inWin := ts.Format("2006-01-02") >= "2026-05-12" && ts.Format("2006-01-02") <= "2026-05-14"
		lw := ts.Format("2006-01-02T15:04:05")
		inWin2 := lw >= "2026-05-12T09:30:00" && lw <= "2026-05-19T17:00:00"
		sawWindow = sawWindow || inWin
		sawWin2 = sawWin2 || inWin2
		fx := time.Unix(int64(r["fixed"].(float64)), 0).In(syd)
		fElapsed := (wall(fx) - wall(dayStart("2026-02-01", syd))) / 86400
		d := time.Unix(int64(r["d"].(float64))*86400, 0).UTC()
		for _, c := range []struct {
			col  string
			want float64
		}{
			{"tr", float64(2 * elapsed)},
			{"hr", float64(ts.Hour())},
			{"dw", float64((int(ts.Weekday())+6)%7 + 1)},
			{"mo", float64(ts.Month())},
			{"win", b2f(inWin)},
			{"win2", b2f(inWin2)},
			{"ftr", float64(0.5 * fElapsed)},
			{"fhr", float64(fx.Hour())},
			{"dtr", r["d"].(float64) - float64(dayStart("2026-01-01", time.UTC).Unix()/86400)},
			{"ddw", float64((int(d.Weekday())+6)%7 + 1)},
			{"dmo", float64(d.Month())},
			{"dwin", b2f(d.Month() == time.March)},
			{"ntr", float64(i) * 0.5},
		} {
			if got := r[c.col].(float64); !near(got, c.want) {
				t.Fatalf("row %d (%s, ts %s): %s = %v, want %v", i, r["site"], ts.Format(time.RFC3339), c.col, got, c.want)
			}
		}
	}
	if !sawWindow || !sawWin2 {
		t.Fatalf("no row inside the window (%v / %v); the case is untested", sawWindow, sawWin2)
	}
}

// TestRowTime_PerRowZoneIsTheStagesNotARewrite: a rule that rewrites the
// zone field BEFORE a time function reads the datetime does not move the
// reading — the function reads the zone the conversion stage drew it in.
// The rules consume no RNG, so the two runs draw the same instants.
func TestRowTime_PerRowZoneIsTheStagesNotARewrite(t *testing.T) {
	t.Parallel()

	plain, _, err := SynthBytes(timeEffectsSpec(), Options{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	rewritten, _, err := SynthBytes(timeEffectsSpec(RuleSpec{Set: map[string]any{"site": "UTC"}}), Options{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := datetimeRows(t, plain)
	b, _ := datetimeRows(t, rewritten)
	differs := false
	for i := range a {
		if a[i]["ts"] != b[i]["ts"] || a[i]["hr"] != b[i]["hr"] || a[i]["dw"] != b[i]["dw"] || a[i]["tr"] != b[i]["tr"] {
			t.Fatalf("row %d: rewriting the zone field moved a reading: %v vs %v", i, a[i], b[i])
		}
		if b[i]["site"] != "UTC" {
			t.Fatalf("row %d: the rewrite did not land", i)
		}
		differs = differs || a[i]["site"] != "UTC"
	}
	if !differs {
		t.Fatal("every row was already UTC; the case is untested")
	}
}

// TestRowTime_FixedSeedIsByteIdentical: the functions read the row and
// consume no RNG, so a spec using them regenerates byte-identically, and
// the drawn columns are the bytes a spec without the rule writes.
func TestRowTime_FixedSeedIsByteIdentical(t *testing.T) {
	t.Parallel()

	a, _, err := SynthBytes(timeEffectsSpec(), Options{Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := SynthBytes(timeEffectsSpec(), Options{Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same spec and seed generated different bytes")
	}
	noRules := timeEffectsSpec()
	noRules.Rules = nil
	c, _, err := SynthBytes(noRules, Options{Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := datetimeRows(t, a)
	rc, _ := datetimeRows(t, c)
	for i := range ra {
		for _, f := range []string{"site", "ts", "fixed", "d"} {
			if ra[i][f] != rc[i][f] {
				t.Fatalf("row %d: %s moved when the time rule was added: %v vs %v", i, f, ra[i][f], rc[i][f])
			}
		}
	}
}

// TestRowTime_NullReadingIsNull: a null datetime returns null, as lag
// does — a loud row-time fault unguarded, the declared value guarded.
func TestRowTime_NullReadingIsNull(t *testing.T) {
	t.Parallel()

	spec := func(when string) *Spec {
		return &Spec{RowCount: 200, Fields: []FieldSpec{
			{Name: "ts", Type: "datetime", Nullable: true, NullRate: 0.3, Distribution: DistUniformDateTime,
				Params: map[string]any{"start": "2026-01-01", "end": "2026-01-31"}},
			f64Out("hr"),
		}, Rules: []RuleSpec{{When: when, SetExpr: map[string]string{"hr": `season(ts, "hour", ` + seq(1, 24) + `)`}}}}
	}
	if _, _, err := SynthBytes(spec(""), Options{Seed: 3}); err == nil {
		t.Fatal("an unguarded season over a null datetime generated; want a row-time fault")
	}
	data, _, err := SynthBytes(spec("!isnull(ts)"), Options{Seed: 3})
	if err != nil {
		t.Fatalf("guarded: %v", err)
	}
	rows, nulls := datetimeRows(t, data)
	nulled := 0
	for i, r := range rows {
		switch {
		case nulls[i]["ts"]:
			nulled++
			if r["hr"].(float64) != 0 {
				t.Fatalf("row %d: null ts but hr %v", i, r["hr"])
			}
		case r["hr"].(float64) != float64(time.Unix(int64(r["ts"].(float64)), 0).UTC().Hour()+1):
			t.Fatalf("row %d: hr %v for ts %v", i, r["hr"], r["ts"])
		}
	}
	if nulled == 0 {
		t.Fatal("no null ts drawn; the case is untested")
	}
}

// TestRowTime_Refusals: every malformed call is refused at parse, by
// validateSpec AND by compileRules / compileConstraints for a Spec that
// bypassed validation, with the same code and message.
func TestRowTime_Refusals(t *testing.T) {
	t.Parallel()

	base := func() *Spec {
		return &Spec{RowCount: 10, Fields: []FieldSpec{
			{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{"start": "2026-01-01", "end": "2026-01-31"}},
			{Name: "d", Type: "date", Distribution: DistUniformDate, Params: map[string]any{"start": "2026-01-01", "end": "2026-01-31"}},
			{Name: "seq", Type: "date", Distribution: DistMonotonicFrom, Params: map[string]any{"start": 20000.0}},
			f64Out("x"),
		}}
	}
	for _, tc := range []struct {
		call, want string
	}{
		{`season(x, "dow", ` + seq(1, 7) + `)`, "must name a declared date or datetime field"},
		{`season(row(), "dow", ` + seq(1, 7) + `)`, "must name a declared date or datetime field"},
		{`season(ts, "week", ` + seq(1, 7) + `)`, `unit must be the literal "dow", "hour" or "month"`},
		{`season(ts, "dow", ` + seq(1, 6) + `)`, "exactly 7 numbers"},
		{`season(ts, "hour", ` + seq(1, 7) + `)`, "exactly 24 numbers"},
		{`season(ts, "dow", [1, 2, 3, 4, 5, 6, x])`, "weights[6] must be a finite number literal"},
		{`season(d, "hour", ` + seq(1, 24) + `)`, "is a date and has no hour"},
		{`season(ts, "dow")`, "takes 3 arguments, got 2"},
		{`between(ts, "2026-01-14", "2026-01-12") ? 1 : 0`, "b is before a"},
		{`between(ts, "2026-01-12T00:00:00Z", "2026-01-14") ? 1 : 0`, "is not YYYY-MM-DD or a naive datetime"},
		{`between(ts, "soon", "2026-01-14") ? 1 : 0`, "is not YYYY-MM-DD or a naive datetime"},
		{`between(d, "2026-01-12T10:00", "2026-01-14") ? 1 : 0`, "its bounds must be dates"},
		{`between(ts, "2026-01-12", x) ? 1 : 0`, "must be string literals"},
		{`between(x, "2026-01-12", "2026-01-14") ? 1 : 0`, "must name a declared date or datetime field"},
		{`trend(ts)`, "takes 2 arguments, got 1"},
		{`trend(seq, 1)`, "declares none"},
	} {
		ruled := base()
		ruled.Rules = []RuleSpec{{SetExpr: map[string]string{"x": tc.call}}}
		validateErr := validateSpec(ruled)
		_, wfs, err := buildSchema(ruled)
		if err != nil {
			t.Fatalf("buildSchema: %v", err)
		}
		_, _, compileErr := compileRules(ruled.Rules, wfs)

		constrained := base()
		constrained.Constraints = []ConstraintSpec{{Expr: "(" + tc.call + ") != nil"}}
		specErr := validateSpec(constrained)
		_, cwfs, err := buildSchema(constrained)
		if err != nil {
			t.Fatalf("buildSchema: %v", err)
		}
		_, consErr := compileConstraints(constrained.Constraints, cwfs)

		for _, pair := range []struct {
			code errors.Code
			errs [2]error
		}{
			{errors.PULSE_SYNTH_RULE_EXPR_INVALID, [2]error{validateErr, compileErr}},
			{errors.SERVICE_VALIDATION, [2]error{specErr, consErr}},
		} {
			var msgs [2]string
			for i, e := range pair.errs {
				ce, ok := e.(*errors.CodedError)
				if !ok || ce.Code != pair.code || !strings.Contains(ce.Message, tc.want) {
					t.Fatalf("%s: side %d returned %v, want %s containing %q", tc.call, i, e, pair.code, tc.want)
				}
				msgs[i] = ce.Message
			}
			if msgs[0] != msgs[1] {
				t.Errorf("%s: messages differ:\n %q\n %q", tc.call, msgs[0], msgs[1])
			}
		}
	}
}

// TestRowTime_FieldNamedLikeAFunction: a field named `season` reads as a
// bare name; calling season() then is refused, never silently the field.
func TestRowTime_FieldNamedLikeAFunction(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 20, Fields: []FieldSpec{
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{"start": "2026-01-01", "end": "2026-01-31"}},
		{Name: "season", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 3.0}},
		f64Out("x"),
	}, Rules: []RuleSpec{{SetExpr: map[string]string{"x": "season * 2"}}}}
	data, _, err := SynthBytes(spec, Options{Seed: 1})
	if err != nil {
		t.Fatalf("bare read of a field named season: %v", err)
	}
	if rows, _ := datetimeRows(t, data); rows[0]["x"] != 6.0 {
		t.Fatalf("x = %v, want 6", rows[0]["x"])
	}
	spec.Rules = []RuleSpec{{SetExpr: map[string]string{"x": `season(ts, "dow", ` + seq(1, 7) + `)`}}}
	if err := validateSpec(spec); codeOf(err) != errors.PULSE_SYNTH_RULE_EXPR_INVALID || !strings.Contains(err.Error(), "declares a field named") {
		t.Fatalf("calling a shadowed season: %v", err)
	}
}

// TestRowTime_DetectorsCompileAgainstTheLoaderEnvironment: the
// --suggest-rules detectors compile against the same time functions the
// loader accepts.
func TestRowTime_DetectorsCompileAgainstTheLoaderEnvironment(t *testing.T) {
	t.Parallel()

	gate := (&gateDetector{schema: &encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime},
	}}}).predicateCompiler()
	if err := gate(`between(ts, "2026-01-01", "2026-01-02")`); err != nil {
		t.Fatalf("gate detector refused a valid between: %v", err)
	}
	if err := gate(`season(ts, "dow", [1, 2])  > 1`); err == nil {
		t.Fatal("gate detector compiled a malformed season")
	}
}
