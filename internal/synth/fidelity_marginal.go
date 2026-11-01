package synth

import (
	"bytes"
	"math"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// This file decides which statistic scores each scalar field type's
// marginal, and carries the two measures the report computes itself
// rather than through a test operator: a datetime's daily and weekly
// cycle, and a set option's effect size.
//
// # The statistic per type, and why
//
//   - u8 … u64, f32, f64, decimal128: TEST_KS + Cohen's d, as before.
//   - u4: TEST_KS + Cohen's d — the same arm as the wider integers. A
//     u4 is an ordinal 0..15 and reconstructs from the same per-level
//     histogram a u8 does (`discrete`), so scoring it differently would
//     make two columns generated the same way read on two scales.
//   - date: TEST_KS + Cohen's d over epoch DAYS.
//   - datetime: TEST_KS + Cohen's d over epoch SECONDS, plus Shape —
//     the hour-of-day and weekday histograms' total variation distance.
//     A planted schedule (Sunday closures, a night shift) lives in the
//     cycle, and both KS and d read the instants as one long line: a
//     twin drawing the right days at the wrong hours passes both.
//   - packed_bool: TEST_CHISQ + Cramér's V (= |phi| on the 2x2 table
//     against `_synthetic`), the categorical arm. A boolean's marginal
//     is one proportion; the χ² test of a 2x2 table is the two-sample
//     proportion test, and V puts it on the same w bands a categorical
//     field reads.
//   - set_*: per option, the selection rates (set_fields) plus V of the
//     option's own 2x2 table, computed directly — a multi-select is
//     neither a scalar KS reads nor one partition χ² reads.
//
// # The clock Shape reads
//
// UTC, always. The schema stores instants and carries no zone (a
// synth datetime's zone lives in its distribution params), so the
// report has no local clock to read. Both partitions are read through
// the same clock, so a twin faithful to a local schedule is faithful
// on UTC too; for one fixed zone the UTC histogram is the local one
// shifted by the offset, and only a DST change moves an hour's bin.

// TimeShapeClockUTC is the clock TimeShapeFidelity reads hours and
// weekdays on.
const TimeShapeClockUTC = "utc"

// TimeShapeFidelity is a datetime field's cycle comparison: the total
// variation distance between the source and synthetic partitions'
// hour-of-day (24 bins) and ISO weekday (7 bins) histograms, each in
// [0, 1] — 0 when the two cycles match, 1 when they share no bin.
type TimeShapeFidelity struct {
	// Clock is the clock the bins are read on (TimeShapeClockUTC).
	Clock      string  `json:"clock"`
	HourTVD    float64 `json:"hour_tvd"`
	WeekdayTVD float64 `json:"weekday_tvd"`
}

// marginalTest returns the test that scores f's marginal against
// `_synthetic`, or nil for a type the fields section does not score
// (set_* and vectors have their own sections).
func marginalTest(f *encoding.Field) *types.Test {
	switch {
	case ksMarginalType(f.Type):
		return &types.Test{Type: types.TEST_KS, Field: f.Name, SplitBy: SyntheticFieldName}
	case f.Type.IsCategorical() || f.Type == encoding.FieldTypePackedBool:
		return &types.Test{Type: types.TEST_CHISQ, Rows: f.Name, Cols: SyntheticFieldName}
	}
	return nil
}

// ksMarginalType reports whether a type's marginal is scored with
// TEST_KS: the strict numerics plus u4, date and datetime.
func ksMarginalType(ft encoding.FieldType) bool {
	if ft.IsNumeric() {
		return true
	}
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeDate, encoding.FieldTypeDateTime:
		return true
	}
	return false
}

// cohensDType reports whether a KS-scored type also gets Cohen's d:
// every one but decimal128, whose float64 echo is lossy.
func cohensDType(ft encoding.FieldType) bool {
	return ksMarginalType(ft) && !ft.IsDecimal()
}

// attachTimeShapes sets FieldFidelity.Shape on every datetime entry
// with a result, decoding records at most once.
func attachTimeShapes(report *FidelityReport, mergedSchema *encoding.Schema, records []byte) {
	type cycle struct {
		hour    [2][24]int
		weekday [2][7]int
		n       [2]int
	}
	var entries []*FieldFidelity
	for _, e := range report.Fields {
		if e.Result == nil {
			continue
		}
		if f := mergedSchema.Field(e.Field); f != nil && f.Type == encoding.FieldTypeDateTime {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return
	}
	cycles := make([]cycle, len(entries))
	rr := encx.NewRecordReader(bytes.NewReader(records), mergedSchema)
	values := make(map[string]float64, len(mergedSchema.Fields))
	nulls := make(map[string]bool, len(mergedSchema.Fields))
	wide := make(map[string]any, len(mergedSchema.Fields))
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		part := 0
		if values[SyntheticFieldName] != 0 {
			part = 1
		}
		for i, e := range entries {
			if nulls[e.Field] {
				continue
			}
			v := values[e.Field]
			if math.IsNaN(v) {
				continue
			}
			p := temporal.LocalParts(int64(v), temporal.UTC)
			c := &cycles[i]
			c.hour[part][p.Hour]++
			c.weekday[part][temporal.ISOWeekdayOf(p.Weekday)-1]++
			c.n[part]++
		}
	}
	for i, e := range entries {
		c := &cycles[i]
		if c.n[0] == 0 || c.n[1] == 0 {
			continue
		}
		e.Shape = &TimeShapeFidelity{
			Clock:      TimeShapeClockUTC,
			HourTVD:    histogramTVD(c.hour[0][:], c.hour[1][:], c.n[0], c.n[1]),
			WeekdayTVD: histogramTVD(c.weekday[0][:], c.weekday[1][:], c.n[0], c.n[1]),
		}
	}
}

// histogramTVD is half the L1 distance between two count histograms
// over the same bins, each normalised by its own total. Summed in bin
// order with no product, so it is FMA-free and order-fixed.
func histogramTVD(a, b []int, na, nb int) float64 {
	var sum float64
	for i := range a {
		sum += math.Abs(float64(a[i])/float64(na) - float64(b[i])/float64(nb))
	}
	return sum / 2
}

// setOptionEffectSize is Cramér's V of one set option's 2x2 table —
// selected or not, by partition — which for a 2x2 table is |phi|:
// |ad − bc| / sqrt(r1·r2·c1·c2). Read on Cohen's w bands, exactly as a
// categorical field's χ² entry is. nil when a margin is zero (an empty
// partition, or an option selected on every row or on none).
func setOptionEffectSize(srcSel, srcN, synSel, synN int) *FieldEffectSize {
	a, b := float64(srcSel), float64(srcN-srcSel) // source: selected, not
	c, d := float64(synSel), float64(synN-synSel) // synthetic: selected, not
	r1, r2 := a+b, c+d
	c1, c2 := a+c, b+d
	denom := float64(float64(r1*r2) * float64(c1*c2))
	if !(denom > 0) {
		return nil
	}
	v := math.Abs(float64(a*d)-float64(b*c)) / math.Sqrt(denom)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return newFieldEffectSize(EffectMeasureCramersV, v, effectBandsW)
}
