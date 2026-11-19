package synth

import (
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// Month volume (`month_weights`, #594) for uniform_date and
// uniform_datetime.
//
// `month_weights` lists one weight per CALENDAR MONTH the [start, end]
// range touches, start's month first, so a series of N weights carries
// both a trend (its slope) and a seasonal swing (its shape) — month bins
// are bounded (12 a year), where per-day bins would not be. Like
// `weekday_weights` the weight is a per-DAY intensity: a day's weight is
// its weekday weight TIMES its month weight (the two are assumed
// separable), so a month the range only half covers draws half its
// days' worth, never its whole weight.
//
// # Determinism
//
// The param switches the day draw onto this picker and ONLY then: a spec
// without it draws its day exactly as before, so every shipped spec's
// bytes are unchanged. With it, the day is ONE Float64 — a month by
// binary search over the cumulative month totals, then the day inside
// that month by a walk over the same per-day products — so a
// uniform_datetime row is THREE draws (day, hour, second) instead of
// four and a uniform_date row stays one; the count is a function of the
// spec alone. Every product is barriered (float64(a*b)) and every sum
// runs in day order, so the totals are the same bits on every
// architecture.

// monthWeightsParam is the shared params key.
const monthWeightsParam = "month_weights"

// monthSpan returns how many calendar months the inclusive epoch-day
// range [start, end] touches.
func monthSpan(start, end int64) int {
	return int(temporal.MonthIndex(end)-temporal.MonthIndex(start)) + 1
}

// monthDayPicker draws a calendar day by weekday × month intensity.
type monthDayPicker struct {
	weekday []float64 // 7 per-day weekday weights, ISO Monday first
	month   []float64 // one per calendar month in range
	first   []int64   // first in-range day of each month
	last    []int64   // last in-range day of each month
	cum     []float64 // cumulative month totals
	total   float64
}

// newMonthDayPicker reads f's month_weights; the picker is nil when the
// param is absent — the caller keeps its own draw.
// weekday is the field's 7 per-day weekday weights (all 1 for a
// uniform_date).
func newMonthDayPicker(f FieldSpec, startDay, endDay int64, weekday []float64) (*monthDayPicker, error) {
	if _, present, err := paramFloatSlice(f.Name, f.Params, monthWeightsParam); err != nil || !present {
		return nil, err
	}
	n := monthSpan(startDay, endDay)
	month, err := profileWeights(f, monthWeightsParam, n)
	if err != nil {
		return nil, err
	}
	p := &monthDayPicker{
		weekday: weekday,
		month:   month,
		first:   make([]int64, n),
		last:    make([]int64, n),
		cum:     make([]float64, n),
	}
	totals := monthDayProducts(startDay, endDay, weekday, month, p.first, p.last)
	for m, t := range totals {
		p.total += t
		p.cum[m] = p.total
	}
	if !(p.total > 0) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: no day in [start, end] has a positive weekday × month weight", f.Name),
			map[string]any{"field": f.Name, "param": monthWeightsParam})
	}
	return p, nil
}

// monthDayProducts sums, per calendar month of [start, end], the day
// weights weekday[wd(d)] × month[m(d)] in day order (month nil = 1), and
// records each month's first and last in-range day when first / last
// are non-nil. The one walk both the picker and the profile
// reconstruction read, so the two agree on every day's weight.
func monthDayProducts(start, end int64, weekday, month []float64, first, last []int64) []float64 {
	base := temporal.MonthIndex(start)
	out := make([]float64, monthSpan(start, end))
	prev := -1
	for d := start; d <= end; d++ {
		m := int(temporal.MonthIndex(d) - base)
		if first != nil && m != prev {
			first[m] = d
		}
		prev = m
		if last != nil {
			last[m] = d
		}
		w := weekday[temporal.ISOWeekday(d)-1]
		if month != nil {
			w = float64(w * month[m])
		}
		out[m] += w
	}
	return out
}

// pick maps one uniform u in [0, 1) to a day: the month whose
// cumulative total first exceeds u × total (a zero-width month is never
// chosen), then the day inside it whose running product sum first
// exceeds the remainder. Rounding past either total lands on the last
// positive-weight slot.
func (p *monthDayPicker) pick(u float64) int64 {
	x := float64(u * p.total)
	m := sort.Search(len(p.cum), func(i int) bool { return p.cum[i] > x })
	if m == len(p.cum) {
		m = p.lastPositiveMonth()
	}
	r := x
	if m > 0 {
		r = x - p.cum[m-1]
	}
	acc := 0.0
	lastPositive := p.first[m]
	for d := p.first[m]; d <= p.last[m]; d++ {
		w := float64(p.weekday[temporal.ISOWeekday(d)-1] * p.month[m])
		if !(w > 0) {
			continue
		}
		acc += w
		lastPositive = d
		if r < acc {
			return d
		}
	}
	return lastPositive
}

func (p *monthDayPicker) lastPositiveMonth() int {
	prev := 0.0
	last := 0
	for i, c := range p.cum {
		if c > prev {
			last = i
		}
		prev = c
	}
	return last
}

// monthWeightsFromCounts rebuilds a captured per-month volume series as
// month_weights: each month's count divided by the sum of its in-range
// days' weekday weights, so weekday × month reproduces the month's
// volume in expectation whatever share of the month the range covers.
// ok is false when the series does not fit [start, end] (a hand-edited
// document), so the caller emits no month weights rather than wrong
// ones.
func monthWeightsFromCounts(startDay, endDay int64, monthStart string, counts []int, weekday []float64) ([]any, bool) {
	if len(counts) != monthSpan(startDay, endDay) || monthStart != monthLabel(temporal.MonthIndex(startDay)) {
		return nil, false
	}
	sums := monthDayProducts(startDay, endDay, weekday, nil, nil, nil)
	out := make([]any, len(counts))
	for m, c := range counts {
		w := 0.0
		if sums[m] > 0 {
			w = float64(c) / sums[m]
		}
		out[m] = w
	}
	return out, true
}

// monthLabel formats a running month index (year*12 + month-1) as
// YYYY-MM.
func monthLabel(idx int64) string {
	y := idx / 12
	m := idx % 12
	if m < 0 {
		m += 12
		y--
	}
	return fmt.Sprintf("%04d-%02d", y, m+1)
}

// monthSeries flattens a month-index → count map into the contiguous
// series from its lowest to its highest month, labelled by the first.
// Empty for an empty map.
func monthSeries(counts map[int64]int) (string, []int) {
	if len(counts) == 0 {
		return "", nil
	}
	lo, hi := int64(0), int64(0)
	firstKey := true
	for k := range counts {
		if firstKey || k < lo {
			lo = k
		}
		if firstKey || k > hi {
			hi = k
		}
		firstKey = false
	}
	out := make([]int, hi-lo+1)
	for k, c := range counts {
		out[k-lo] = c
	}
	return monthLabel(lo), out
}
