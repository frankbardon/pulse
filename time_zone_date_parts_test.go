package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// datePartsOverTS is ATTR_DATE_PART (hour) and FEAT_DATE_FEATURES over
// the `datetime` field ts, the zone on the slot and/or the request.
func datePartsOverTS(cohort, slotTZ, reqTZ string) map[string]*types.Request {
	c := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	return map[string]*types.Request{
		"ATTR_DATE_PART": {Cohort: c(), TimeZone: reqTZ, Aggregations: countAgg(),
			Attributes: []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "ts", Label: "hr", Params: json.RawMessage(`{"part":"hour"}`), TimeZone: slotTZ}},
			Groups:     []*types.Group{{Type: types.GROUP_CATEGORY, Field: "hr"}}},
		"FEAT_DATE_FEATURES": {Cohort: c(), TimeZone: reqTZ, Aggregations: countAgg(),
			Features: []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: "ts", Label: "df", TimeZone: slotTZ}},
			Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "df_hour"}}},
	}
}

// TestDateParts_DateTimeZoneEcho: ATTR_DATE_PART / FEAT_DATE_FEATURES
// over a `datetime` run with a non-UTC zone from the slot or the
// request, and predict echoes the REAL zone (not null) with its source.
func TestDateParts_DateTimeZoneEcho(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	for _, src := range []struct{ name, slot, req string }{{"slot", "Asia/Tokyo", ""}, {"request", "", "Asia/Tokyo"}} {
		for op, req := range datePartsOverTS(cohort, src.slot, src.req) {
			t.Run(src.name+"/"+op, func(t *testing.T) {
				if _, err := p.Process(ctx, req); err != nil {
					t.Fatalf("Process: %v", err)
				}
				pr, err := p.Predict(ctx, req)
				if err != nil || !pr.Valid {
					t.Fatalf("Predict: valid=%v err=%v", pr != nil && pr.Valid, err)
				}
				if len(pr.TimeZones) != 1 {
					t.Fatalf("TimeZones = %+v", pr.TimeZones)
				}
				z := pr.TimeZones[0]
				if z.TZ == nil || *z.TZ != "Asia/Tokyo" || z.Source != src.name || z.Operator != op || z.FieldType != "datetime" {
					t.Fatalf("TimeZones[0] = %+v (tz %v), want Asia/Tokyo from %s on %s over datetime", z, z.TZ, src.name, op)
				}
			})
		}
	}
}

// TestDateParts_RefusalsPredictParity: what the factories refuse over a
// field predict refuses too — ATTR_DATE_PART with the runtime's own code
// and message (one reading, internal/datepart); a non-temporal
// FEAT_DATE_FEATURES field on both sides. The explicit-`tz`-on-`date`
// refusal is TestDateFieldRejectsTZ's.
func TestDateParts_RefusalsPredictParity(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	attr := func(field, params string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
			Attributes: []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: field, Label: "p", Params: json.RawMessage(params)}}}
	}
	for name, mk := range map[string]func() *types.Request{
		"attr-hour-over-date":    func() *types.Request { return attr("d", `{"part":"hour"}`) },
		"attr-over-numeric":      func() *types.Request { return attr("n", `{"part":"year"}`) },
		"attr-over-categorical":  func() *types.Request { return attr("cat", `{"part":"month"}`) },
		"attr-unknown-part":      func() *types.Request { return attr("ts", `{"part":"minute"}`) },
		"attr-hour-inherited-tz": func() *types.Request { r := attr("d", `{"part":"hour"}`); r.TimeZone = "Asia/Tokyo"; return r },
	} {
		t.Run(name, func(t *testing.T) {
			_, rerr := p.Process(ctx, mk())
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) {
				t.Fatalf("runtime: want a coded refusal, got %v", rerr)
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			if len(env.Errors) == 0 {
				t.Fatalf("predict accepted what the runtime refuses (%v)", rerr)
			}
			if e := env.Errors[0]; e.Code != string(ce.Code) || e.Message != ce.Message {
				t.Fatalf("predict %s %q, runtime %s %q", e.Code, e.Message, ce.Code, ce.Message)
			}
		})
	}
	t.Run("feat-over-categorical", func(t *testing.T) {
		mk := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
				Features: []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: "cat", Label: "df"}}}
		}
		if _, err := p.Process(ctx, mk()); err == nil {
			t.Fatal("runtime accepted FEAT_DATE_FEATURES over a categorical")
		}
		if env := predictEnvelope(t, p, fs, cohort, mk()); len(env.Errors) == 0 {
			t.Fatal("predict accepted FEAT_DATE_FEATURES over a categorical")
		}
	})
}
